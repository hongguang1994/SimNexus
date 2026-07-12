package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"simnexus-go/config"
	"simnexus-go/database"
	"simnexus-go/models"
)

// prevStatus 记录每个设备上次轮询时的状态，用于检测上下线转变并推送通知。
var prevStatus = map[string]string{}

// lastResetAttempt 记录每个设备上次自动复位的时间（按 IMEI 索引，复位后路径会变化），
// 避免设备持续处于异常状态时反复触发复位造成 USB 总线抖动。
var lastResetAttempt = map[string]time.Time{}

// resetCooldown 两次自动复位之间的最小间隔。
const resetCooldown = 90 * time.Second

// lastEnableAttempt 记录每个设备上次自动 enable 的时间。
// enable 操作本身会触发状态变化（进而触发 D-Bus 事件回调本轮询），如果不加冷却，
// 会在 enable 尚未完成状态转换时被下一次触发打断，导致 ModemManager 报
// "Invalid transition" 并使设备反复卡在 disabled，形成活锁。
var lastEnableAttempt = map[string]time.Time{}

// enableCooldown 两次自动 enable 之间的最小间隔，需大于模块完成一次状态转换所需时间。
const enableCooldown = 20 * time.Second

// StartPolling runs the modem poll loop until ctx is cancelled. 定时轮询作为兜底机制持续运行；
// 同时启动 D-Bus 事件监听，一旦 ModemManager 广播设备增删/状态变化信号，立即触发一次扫描，
// 无需等到下一个轮询周期。D-Bus 监听失败（如权限不足）不影响轮询兜底继续工作。
func StartPolling(ctx context.Context) {
	interval := time.Duration(config.C.ModemPollSeconds) * time.Second
	trigger := make(chan struct{}, 1)
	go watchModemDBusEvents(ctx, trigger)

	safePoll := func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("poller panic", "err", r)
			}
		}()
		poll()
	}

	safePoll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
			safePoll()
		case <-trigger:
			safePoll()
		}
	}
}

// poll 执行一次完整的设备扫描：同步 mmcli 和 ZTE 设备状态、入库收件短信、
// 检测状态变化推送通知，并将不再存在的设备标记为离线。
func poll() {
	detected := ListModems()
	if zte := ZteGetModemInfo(); zte != nil {
		detected = append(detected, *zte)
	}

	db := database.DB
	seen := map[string]bool{}
	for _, info := range detected {
		path := info.MmObjectPath
		seen[path] = true

		var modem models.Modem
		// 身份 = SIM 的 ICCID（卡表面印的序列号，跟着 SIM 走）。按 ICCID 认卡：
		//   - 同卡换模组/换 USB 口/索引漂移 → 仍是同一张卡（ICCID 不变）；
		//   - 同模组换 SIM → 认成另一张卡（ICCID 变）。
		// 同一 ICCID 若有多条历史记录（早期重复），优先 VoWiFi 模式的、再按最近可见，
		// 避免匹配到废弃的重复行。ICCID 读不到时（sim-missing / ZTE 无 ICCID）退回按路径匹配。
		found := false
		if info.Iccid != "" {
			if db.Where("iccid = ?", info.Iccid).Order("vowifi_mode desc, last_seen desc").First(&modem).Error == nil {
				modem.MmObjectPath = path // SIM 可能已换模组/索引，更新到当前路径
				found = true
			}
		}
		if !found {
			// Fallback: match by D-Bus path (无 ICCID 的 ZTE、或读不到卡时)
			if db.Where("mm_object_path = ?", path).First(&modem).Error != nil {
				modem = models.Modem{MmObjectPath: path}
			}
		}

		modem.DevicePath = info.DevicePath
		modem.Manufacturer = info.Manufacturer
		modem.Model = info.Model
		if info.Imei != "" {
			modem.Imei = info.Imei
		}
		modem.Operator = info.Operator
		modem.SignalQuality = info.SignalQuality
		modem.Status = info.Status
		if info.PhoneNumber != "" {
			modem.PhoneNumber = info.PhoneNumber
		}
		modem.AccessTechnologies = info.AccessTechnologies
		modem.RegistrationState = info.RegistrationState
		modem.TxBytes = info.TxBytes
		modem.RxBytes = info.RxBytes
		modem.ConnectionDuration = info.ConnectionDuration
		if info.Imsi != "" {
			modem.Imsi = info.Imsi
		}
		if info.Iccid != "" {
			modem.Iccid = info.Iccid
		}
		if info.FirmwareRevision != "" {
			modem.FirmwareRevision = info.FirmwareRevision
		}
		if info.HardwareRevision != "" {
			modem.HardwareRevision = info.HardwareRevision
		}
		if info.CurrentBands != "" {
			modem.CurrentBands = info.CurrentBands
		}
		if info.SimOperatorName != "" {
			modem.SimOperatorName = info.SimOperatorName
		}
		if info.SimOperatorCode != "" {
			modem.SimOperatorCode = info.SimOperatorCode
		}
		if info.CurrentModes != "" {
			modem.CurrentModes = info.CurrentModes
		}
		if info.Ports != "" {
			modem.Ports = info.Ports
		}
		if info.Plugin != "" {
			modem.Plugin = info.Plugin
		}
		modem.LastSeen = time.Now()
		modem.IsActive = true
		db.Save(&modem)

		// auto-enable disabled modems（带冷却，避免与自身状态转换产生的 D-Bus 事件形成活锁）。
		// 跳过飞行模式的卡：用户主动关了射频(power-state-low → disabled)，不能再自动 enable 拉回来。
		if info.RawState == "disabled" && !strings.HasPrefix(path, "zte:") && !modem.VowifiAirplane {
			key := info.Imei
			if key == "" {
				key = path
			}
			if last, ok := lastEnableAttempt[key]; !ok || time.Since(last) > enableCooldown {
				lastEnableAttempt[key] = time.Now()
				slog.Info("auto-enabling disabled modem", "index", info.MmIndex)
				go EnableModem(info.MmIndex)
			}
		}

		// 飞行模式强制：非 VoWiFi 模式(VoWiFi 卡已被 inhibit 不在此)但开了飞行模式，
		// 若射频还没关(不是 disabled)，主动压到低功耗态。带冷却，纠正遗留状态并防活锁。
		if modem.VowifiAirplane && !modem.VowifiMode && info.RawState != "disabled" && !strings.HasPrefix(path, "zte:") {
			key := info.Imei
			if key == "" {
				key = path
			}
			if last, ok := lastEnableAttempt[key]; !ok || time.Since(last) > enableCooldown {
				lastEnableAttempt[key] = time.Now()
				slog.Info("airplane mode: forcing modem to low power", "index", info.MmIndex)
				go SetModemPowerLow(info.MmIndex)
			}
		}

		// 换卡等场景下模块可能卡在 sim-missing 等异常状态，仅重启 ModemManager 服务无法恢复，
		// 需要对模块做一次硬件级复位（--reset）触发 USB 重新枚举。带冷却时间防止反复复位。
		if info.FailedReason == "sim-missing" && !strings.HasPrefix(path, "zte:") {
			key := info.Imei
			if key == "" {
				key = path
			}
			if last, ok := lastResetAttempt[key]; !ok || time.Since(last) > resetCooldown {
				lastResetAttempt[key] = time.Now()
				slog.Info("modem sim-missing, attempting hardware reset", "index", info.MmIndex, "imei", info.Imei)
				go ResetModem(info.MmIndex)
			}
		}

		// status transitions
		label := info.Model
		if modem.Alias != "" {
			label = modem.Alias
		}
		if label == "" {
			label = path
		}
		old, had := prevStatus[path]
		if had && old != info.Status {
			if info.Status == "connected" {
				Push("modem_online", "设备上线", label+" 已连接", "admin", nil)
			} else if info.Status == "disconnected" || info.Status == "unknown" {
				Push("modem_offline", "设备离线", label+" 已断开连接", "admin", nil)
			}
		}
		prevStatus[path] = info.Status

		if info.Source == "zte" {
			ingestInbox(&modem, ZteListSMS())
		} else {
			ingestInbox(&modem, ListInbox(info.MmIndex))
		}
	}

	// mark gone modems disconnected
	// VoWiFi 模式的卡被 mmcli --inhibit 独占、对 mmcli 不可见，属正常状态，不参与"离线"判定。
	var gone []models.Modem
	q := db.Where("is_active = ? AND vowifi_mode = ?", true, false)
	if len(seen) > 0 {
		paths := make([]string, 0, len(seen))
		for p := range seen {
			paths = append(paths, p)
		}
		q = q.Where("mm_object_path NOT IN ?", paths)
	}
	q.Find(&gone)
	for i := range gone {
		m := &gone[i]
		label := m.Alias
		if label == "" {
			label = m.Model
		}
		if label == "" {
			label = m.MmObjectPath
		}
		Push("modem_offline", "设备离线", label+" 已断开连接", "admin", nil)
		m.Status = models.ModemDisconnected
		m.IsActive = false
		db.Save(m)
	}
}

// ingestInbox 将收件短信列表入库，按 (modem_id, mm_sms_index) 去重，
// 新收件同时触发 Telegram 推送。
func ingestInbox(modem *models.Modem, messages []InboxMessage) {
	db := database.DB
	for _, msg := range messages {
		var existing models.SmsMessage
		// mm_sms_index 由 ModemManager 分配，设备重置（如换卡触发的自动复位）后会从 0 重新编号，
		// 不是跨时间稳定唯一的标识符。仅按 index 判重会导致新短信复用旧索引号时被误判为重复而丢失，
		// 因此额外要求短信内容也一致才算真正重复。
		err := db.Where("modem_id = ? AND mm_sms_index = ? AND direction = ? AND content = ?",
			modem.ID, msg.SmsIndex, models.SmsInbound, msg.Content).First(&existing).Error
		if err == nil {
			continue
		}
		now := time.Now()
		sms := models.SmsMessage{
			ModemID:     modem.ID,
			MmSmsIndex:  msg.SmsIndex,
			Direction:   models.SmsInbound,
			Channel:     models.SmsChannelCellular,
			PhoneNumber: msg.PhoneNumber,
			Content:     msg.Content,
			Status:      models.SmsReceived,
			ReceivedAt:  &now,
		}
		db.Create(&sms)
		broadcastMessage(&sms) // WebSocket 实时推送
		label := modem.Alias
		if label == "" {
			label = modem.Model
		}
		if label == "" {
			label = fmt.Sprintf("设备#%d", modem.ID)
		}
		go TelegramPushInboundSMS(label, msg.PhoneNumber, msg.Content)
	}
}
