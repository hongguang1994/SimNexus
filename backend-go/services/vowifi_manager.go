package services

// VowifiManager 管理"处于 VoWiFi 模式"的每张卡的常驻会话：
//   - 全程持有该卡的 mmcli --inhibit（独占串口）
//   - 一个常驻 vowifi.Daemon 负责该卡的 MO 发送与 MT 接收
//   - MT 短信通过回调入库（SmsMessage inbound）并触发 Telegram 推送
// 开关切换（SetVowifiMode）与后端启动时据此启停对应会话。

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"simnexus-go/database"
	"simnexus-go/models"
	"simnexus-go/services/vowifi"
)

type vowifiCard struct {
	daemon  *vowifi.Daemon
	release func() // 释放该卡的 mmcli --inhibit
}

// VowifiManager 单例，持有所有常驻 VoWiFi 会话。
type VowifiManager struct {
	mu    sync.Mutex
	cards map[uint]*vowifiCard
	steps map[uint]*vowifi.Steps // 每卡最近一次通道建立的分步状态（成功/失败都保留供界面查看）
}

var vowifiMgr = &VowifiManager{cards: map[uint]*vowifiCard{}, steps: map[uint]*vowifi.Steps{}}

// StepsFor 返回某卡最近一次 VoWiFi 通道建立的分步状态快照（无则 nil）。
func (m *VowifiManager) StepsFor(modemID uint) []vowifi.Step {
	m.mu.Lock()
	st := m.steps[modemID]
	m.mu.Unlock()
	return st.Snapshot()
}

// InfoFor 返回某卡常驻会话的运行态信息（未运行则 nil）。
func (m *VowifiManager) InfoFor(modemID uint) *vowifi.SessionInfo {
	m.mu.Lock()
	c := m.cards[modemID]
	m.mu.Unlock()
	if c == nil {
		return nil
	}
	info := c.daemon.Info()
	return &info
}

// GetVowifiManager 返回全局 VoWiFi 会话管理器。
func GetVowifiManager() *VowifiManager { return vowifiMgr }

// IsRunning 返回该卡是否有常驻会话。
func (m *VowifiManager) IsRunning(modemID uint) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.cards[modemID]
	return ok
}

// Start 为一张卡建立常驻 VoWiFi 会话（幂等）。会先 inhibit 该卡取得独占。
func (m *VowifiManager) Start(modem *models.Modem) error {
	m.mu.Lock()
	if _, ok := m.cards[modem.ID]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	// 新建一份步骤追踪，供界面显示通道建立到哪步、哪步失败（成功失败都保留）
	steps := vowifi.NewSteps()
	m.mu.Lock()
	m.steps[modem.ID] = steps
	m.mu.Unlock()

	cfg, err := buildVowifiConfig(modem)
	if err != nil {
		steps.Set(vowifi.StepInhibit, vowifi.StepFail, err.Error())
		return err
	}
	// 独占该卡（释放其 AT 串口）。整条会话生命周期内保持 inhibit。
	var release func()
	if os.Getenv("VOWIFI_MANAGE_MM") != "0" {
		steps.Set(vowifi.StepInhibit, vowifi.StepRunning, "")
		release, err = inhibitModem(modem.Imei, modem.MmObjectPath)
		if err != nil {
			steps.Set(vowifi.StepInhibit, vowifi.StepFail, err.Error())
			return fmt.Errorf("独占该卡失败（mmcli --inhibit）: %w", err)
		}
		time.Sleep(5 * time.Second) // 等 MM 完全释放该卡、AT 口就绪
	}
	steps.Set(vowifi.StepInhibit, vowifi.StepOK, "")

	mID := modem.ID
	daemon, err := vowifi.StartDaemon(cfg, steps, func(sms vowifi.InboundSMS) {
		ingestVowifiInbound(mID, sms)
	})
	if err != nil {
		if release != nil {
			release()
		}
		return err
	}
	m.mu.Lock()
	m.cards[mID] = &vowifiCard{daemon: daemon, release: release}
	m.mu.Unlock()

	// IMS 注册返回了本机号码（MSISDN）则回写 DB —— VoWiFi 模式下 mmcli 读不到号码，
	// 但 P-Associated-URI 里有，属于当前有效值。
	if info := daemon.Info(); info.MSISDN != "" {
		if err := database.DB.Model(&models.Modem{}).Where("id = ?", mID).
			Update("phone_number", info.MSISDN).Error; err != nil {
			fmt.Printf("[vowifi] 回写卡 %d 号码失败: %v\n", mID, err)
		}
	}
	return nil
}

// Stop 停止某卡的常驻会话并交还给 ModemManager。
func (m *VowifiManager) Stop(modemID uint) {
	m.mu.Lock()
	c := m.cards[modemID]
	delete(m.cards, modemID)
	delete(m.steps, modemID)
	m.mu.Unlock()
	if c == nil {
		return
	}
	c.daemon.Stop()
	if c.release != nil {
		c.release()
	}
}

// SendSMS 通过常驻会话发一条 MO 短信；若会话未运行则按需拉起。
func (m *VowifiManager) SendSMS(modem *models.Modem, phone, content string) (bool, string) {
	m.mu.Lock()
	c := m.cards[modem.ID]
	m.mu.Unlock()
	if c == nil {
		if err := m.Start(modem); err != nil {
			return false, "VoWiFi 会话未运行且启动失败: " + err.Error()
		}
		m.mu.Lock()
		c = m.cards[modem.ID]
		m.mu.Unlock()
		if c == nil {
			return false, "VoWiFi 会话不可用"
		}
	}
	smsc := vwFirstNonEmpty(os.Getenv("VOWIFI_SMSC"), "+447802002606")
	code, err := c.daemon.Send(phone, content, smsc)
	if err != nil {
		return false, fmt.Sprintf("VoWiFi 发送失败(code=%d): %s", code, err.Error())
	}
	return true, ""
}

// SyncOne 根据某卡最新的 vowifi_mode 启停其会话（切换开关后调用）。
func (m *VowifiManager) SyncOne(modem *models.Modem) {
	if modem.VowifiMode {
		if err := m.Start(modem); err != nil {
			// 启动失败不阻断 API；日志留痕即可
			fmt.Printf("[vowifi] 启动卡 %d 会话失败: %v\n", modem.ID, err)
		}
	} else {
		m.Stop(modem.ID)
	}
}

// StartWatchdog 启动全局看门狗：周期检查每张运行中的卡，若会话"悄悄死掉"（长时间无任何
// 生命迹象：收包/发成功/心跳重注册全无）则自动 Stop+Start 重建。带每卡指数退避 + 上限，
// 避免自愈反而把自己撞进 O2 的注册节流。判死阈值默认 20min（VOWIFI_DEAD_MINUTES 可调），
// 应大于心跳间隔(默认8min)，以容忍偶发的单次心跳失败。
func (m *VowifiManager) StartWatchdog() {
	deadThreshold := 20 * time.Minute
	if v := os.Getenv("VOWIFI_DEAD_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			deadThreshold = time.Duration(n) * time.Minute
		}
	}
	rebuildAt := map[uint]time.Time{} // 每卡上次重建时刻（退避用）
	streak := map[uint]int{}          // 每卡连续重建次数（退避指数）

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			type ck struct {
				id uint
				d  *vowifi.Daemon
			}
			m.mu.Lock()
			running := make([]ck, 0, len(m.cards))
			for id, c := range m.cards {
				running = append(running, ck{id, c.daemon})
			}
			m.mu.Unlock()

			now := time.Now()
			for _, c := range running {
				silent := now.Sub(c.d.LastAlive())
				if silent < deadThreshold {
					// 健康：若距上次重建已稳定超过 30min，清掉退避计数
					if t, ok := rebuildAt[c.id]; ok && now.Sub(t) > 30*time.Minute {
						delete(rebuildAt, c.id)
						delete(streak, c.id)
					}
					continue
				}
				// 疑似已死：按指数退避决定是否现在重建（3,6,12,24min…上限30min）
				shift := streak[c.id]
				if shift > 4 {
					shift = 4
				}
				cooldown := time.Duration(1<<uint(shift)) * 3 * time.Minute
				if cooldown > 30*time.Minute {
					cooldown = 30 * time.Minute
				}
				if t, ok := rebuildAt[c.id]; ok && now.Sub(t) < cooldown {
					continue
				}

				var modem models.Modem
				if database.DB.First(&modem, c.id).Error != nil || !modem.VowifiMode {
					continue // 卡已删除或已退出 VoWiFi 模式，不重建
				}
				fmt.Printf("[vowifi] 看门狗：卡 %d 会话疑似已死(静默 %v)，自愈重建\n", c.id, silent.Round(time.Second))
				Push("vowifi_down", "VoWiFi 会话重建",
					fmt.Sprintf("卡#%d(%s) 会话疑似断开，正在自愈重建", c.id, modemLabel(&modem)), "admin", nil)
				m.Stop(c.id)
				if err := m.Start(&modem); err != nil {
					fmt.Printf("[vowifi] 看门狗：卡 %d 重建失败: %v\n", c.id, err)
				}
				rebuildAt[c.id] = time.Now()
				streak[c.id]++
			}
		}
	}()
}

// StartEnabled 后端启动时为所有 vowifi_mode=true 的卡拉起常驻会话。
// 后端重启瞬间 ModemManager 尚未完全释放该卡、AT 串口常未就绪（AT+CCHO 空返回），
// 首次启动易失败；这里带退避重试若干次让开机抖动自愈。
func (m *VowifiManager) StartEnabled() {
	var modems []models.Modem
	database.DB.Where("vowifi_mode = ?", true).Find(&modems)
	for i := range modems {
		mo := modems[i]
		go func() {
			for attempt := 0; attempt < 5; attempt++ {
				if m.IsRunning(mo.ID) {
					return
				}
				if err := m.Start(&mo); err != nil {
					fmt.Printf("[vowifi] 开机启动卡 %d 会话失败(第%d次): %v\n", mo.ID, attempt+1, err)
					time.Sleep(time.Duration(10+attempt*10) * time.Second) // 10s,20s,30s,40s 退避
					continue
				}
				return
			}
		}()
	}
}

// ── 长短信(concat)重组缓冲：按 (卡, 发件人, ref) 收集各段，齐了或超时再拼接入库 ──
type reasmBuf struct {
	modemID uint
	sender  string
	total   int
	parts   map[int]string
	first   time.Time
}

func (b *reasmBuf) join() string {
	var s strings.Builder
	for i := 1; i <= b.total; i++ {
		s.WriteString(b.parts[i]) // 缺段则为空，缺一段总比全丢好
	}
	return s.String()
}

type reasmKey struct {
	modemID uint
	sender  string
	ref     int
}

var (
	reasmMu sync.Mutex
	reasm   = map[reasmKey]*reasmBuf{}
)

// ingestVowifiInbound 处理一段 MT：单段直接入库；多段(长短信)先缓冲，拼齐或超时再入库。
func ingestVowifiInbound(modemID uint, sms vowifi.InboundSMS) {
	if sms.Total <= 1 {
		ingestVowifiFinal(modemID, sms.Sender, sms.Text)
		return
	}
	key := reasmKey{modemID, sms.Sender, sms.Ref}
	var ready []*reasmBuf
	reasmMu.Lock()
	buf := reasm[key]
	if buf == nil {
		buf = &reasmBuf{modemID: modemID, sender: sms.Sender, total: sms.Total, parts: map[int]string{}, first: time.Now()}
		reasm[key] = buf
	}
	buf.parts[sms.Seq] = sms.Text
	if len(buf.parts) >= buf.total {
		ready = append(ready, buf)
		delete(reasm, key)
	}
	// 清理超时(>2min)仍未拼齐的：有几段拼几段，避免永远卡住
	for k, b := range reasm {
		if time.Since(b.first) > 2*time.Minute {
			ready = append(ready, b)
			delete(reasm, k)
		}
	}
	reasmMu.Unlock()
	for _, b := range ready {
		ingestVowifiFinal(b.modemID, b.sender, b.join())
	}
}

// ingestVowifiFinal 把一条(已拼接完整的) MT 短信入库并触发推送（对齐 poller 的 ingestInbox）。
func ingestVowifiFinal(modemID uint, sender, text string) {
	db := database.DB
	var modem models.Modem
	if db.First(&modem, modemID).Error != nil {
		return
	}
	now := time.Now()
	// 兜底去重：若 RP-ACK 未能让网络停发，网络会重投同一条 MT。相同发件人+内容在近 10 分钟内
	// 只入库一次，避免界面被重复短信刷屏。正常场景验证码内容各不相同，不会误伤。
	var existing models.SmsMessage
	if db.Where("modem_id = ? AND direction = ? AND channel = ? AND phone_number = ? AND content = ? AND received_at > ?",
		modemID, models.SmsInbound, models.SmsChannelVowifi, sender, text, now.Add(-10*time.Minute)).
		First(&existing).Error == nil {
		return
	}
	rec := models.SmsMessage{
		ModemID:     modemID,
		Direction:   models.SmsInbound,
		Channel:     models.SmsChannelVowifi,
		PhoneNumber: sender,
		Content:     text,
		Status:      models.SmsReceived,
		ReceivedAt:  &now,
	}
	db.Create(&rec)
	broadcastMessage(&rec) // WebSocket 实时推送给消息中心
	label := modem.Alias
	if label == "" {
		label = modem.Model
	}
	if label == "" {
		label = fmt.Sprintf("设备#%d", modemID)
	}
	go TelegramPushInboundSMS(label, sender, text)
}
