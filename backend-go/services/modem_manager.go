package services

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ModemInfo 调制解调器信息结构，镜像 Python modem_manager 返回的字典。
// Source="zte" 标识来自 ZTE HTTP 驱动而非 mmcli。
type ModemInfo struct {
	MmObjectPath       string
	MmIndex            string
	DevicePath         string
	Manufacturer       string
	Model              string
	Imei               string
	Operator           string
	SignalQuality      int
	RawState           string
	Status             string
	PhoneNumber        string
	AccessTechnologies string
	RegistrationState  string
	TxBytes            int64
	RxBytes            int64
	ConnectionDuration int64
	Imsi               string
	Iccid              string
	FirmwareRevision   string
	HardwareRevision   string
	CurrentBands       string
	SimOperatorName    string
	SimOperatorCode    string
	CurrentModes       string
	Ports              string
	Plugin             string
	FailedReason       string // state=failed 时的原因，如 sim-missing
	Source             string // "zte" for ZTE devices
}

// InboxMessage 从调制解调器解析出的一条收件短信。
type InboxMessage struct {
	SmsIndex    string
	PhoneNumber string
	Content     string
	Timestamp   string
	State       string
}

var (
	reModemIdx  = regexp.MustCompile(`/Modem/(\d+)$`)
	reBearerIdx = regexp.MustCompile(`/Bearer/(\d+)$`)
	reSimIdx    = regexp.MustCompile(`/SIM/(\d+)$`)
	reSmsIdx    = regexp.MustCompile(`/SMS/(\d+)`)
	reSmsIdxEnd = regexp.MustCompile(`/SMS/(\d+)$`)
	reCnum      = regexp.MustCompile(`\+CNUM:.*?"(\+?\d+)"`)
)

var errTimeout = &execErr{"command timed out"}

type execErr struct{ msg string }

func (e *execErr) Error() string { return e.msg }

// run 执行外部命令（通常是 mmcli），带超时控制；超时时杀死进程并返回 errTimeout。
func run(timeout time.Duration, args ...string) (string, string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return "", "timeout", errTimeout
	}
}

// ListModems returns all modems detected by ModemManager.
func ListModems() []ModemInfo {
	out, _, err := run(30*time.Second, "mmcli", "-L", "-J")
	if err != nil {
		return nil
	}
	var data struct {
		ModemList []string `json:"modem-list"`
	}
	if json.Unmarshal([]byte(out), &data) != nil {
		return nil
	}
	var modems []ModemInfo
	for _, p := range data.ModemList {
		if info := GetModemInfo(p); info != nil {
			modems = append(modems, *info)
		}
	}
	return modems
}

// GetModemInfo fetches detail for a single modem by D-Bus path.
func GetModemInfo(mmPath string) *ModemInfo {
	m := reModemIdx.FindStringSubmatch(mmPath)
	if m == nil {
		return nil
	}
	idx := m[1]
	out, _, err := run(30*time.Second, "mmcli", "-m", idx, "-J")
	if err != nil {
		return nil
	}
	var raw struct {
		Modem struct {
			Generic map[string]json.RawMessage `json:"generic"`
			Threegp map[string]json.RawMessage `json:"3gpp"`
		} `json:"modem"`
	}
	if json.Unmarshal([]byte(out), &raw) != nil {
		return nil
	}
	g := raw.Modem.Generic
	tg := raw.Modem.Threegp

	sig := 0
	if v, ok := g["signal-quality"]; ok {
		var sq struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(v, &sq) == nil {
			sig, _ = strconv.Atoi(sq.Value)
		}
	}
	state := asString(g["state"])
	bearer := getBearerStats(idx)
	sim := getSimInfo(asString(g["sim"]))
	phone := parseOwnNumber(g)
	if phone == "" {
		phone = getPhoneNumber(idx)
	}

	return &ModemInfo{
		MmObjectPath:       mmPath,
		MmIndex:            idx,
		DevicePath:         asString(g["primary-port"]),
		Manufacturer:       asString(g["manufacturer"]),
		Model:              asString(g["model"]),
		Imei:               asString(tg["imei"]),
		Operator:           asString(tg["operator-name"]),
		SignalQuality:      sig,
		RawState:           state,
		Status:             mapState(state),
		PhoneNumber:        phone,
		AccessTechnologies: joinList(g["access-technologies"]),
		RegistrationState:  asString(tg["registration-state"]),
		TxBytes:            bearer.tx,
		RxBytes:            bearer.rx,
		ConnectionDuration: bearer.dur,
		Imsi:               sim.imsi,
		Iccid:              sim.iccid,
		FirmwareRevision:   asString(g["revision"]),
		HardwareRevision:   asString(g["hardware-revision"]),
		CurrentBands:       joinList(g["current-bands"]),
		SimOperatorName:    sim.opName,
		SimOperatorCode:    sim.opCode,
		CurrentModes:       asString(g["current-modes"]),
		Ports:              joinList(g["ports"]),
		Plugin:             asString(g["plugin"]),
		FailedReason:       asString(g["state-failed-reason"]),
	}
}

type bearerStats struct{ tx, rx, dur int64 }

// getBearerStats 通过 mmcli 读取 bearer 的流量统计和连接时长。
func getBearerStats(idx string) bearerStats {
	out, _, err := run(30*time.Second, "mmcli", "-m", idx, "--list-bearers", "-J")
	if err != nil {
		return bearerStats{}
	}
	var data struct {
		DBusPath   []string `json:"modem.bearers.dbus-path"`
		BearerList []string `json:"bearer-list"`
	}
	if json.Unmarshal([]byte(out), &data) != nil {
		return bearerStats{}
	}
	paths := data.DBusPath
	if len(paths) == 0 {
		paths = data.BearerList
	}
	if len(paths) == 0 {
		return bearerStats{}
	}
	bm := reBearerIdx.FindStringSubmatch(paths[0])
	if bm == nil {
		return bearerStats{}
	}
	out2, _, err := run(30*time.Second, "mmcli", "-b", bm[1], "-J")
	if err != nil {
		return bearerStats{}
	}
	var b struct {
		Bearer struct {
			Stats  map[string]string `json:"stats"`
			Status map[string]string `json:"status"`
		} `json:"bearer"`
	}
	if json.Unmarshal([]byte(out2), &b) != nil {
		return bearerStats{}
	}
	return bearerStats{
		tx:  atoi64(b.Bearer.Stats["tx-bytes"]),
		rx:  atoi64(b.Bearer.Stats["rx-bytes"]),
		dur: atoi64(b.Bearer.Status["connection-duration"]),
	}
}

type simInfo struct{ imsi, iccid, opName, opCode string }

// getSimInfo 通过 mmcli 读取 SIM 卡的 IMSI、ICCID 和运营商信息。
func getSimInfo(simPath string) simInfo {
	if simPath == "" {
		return simInfo{}
	}
	m := reSimIdx.FindStringSubmatch(simPath)
	if m == nil {
		return simInfo{}
	}
	out, _, err := run(30*time.Second, "mmcli", "-i", m[1], "-J")
	if err != nil {
		return simInfo{}
	}
	// properties 里部分字段（如 emergency-numbers）是数组而非字符串，
	// 用 map[string]string 解析会因类型不匹配导致整体 unmarshal 失败，
	// 因此只声明我们关心的字符串字段，忽略其余字段类型。
	var data struct {
		Sim struct {
			Properties struct {
				Imsi   string `json:"imsi"`
				Iccid  string `json:"iccid"`
				OpName string `json:"operator-name"`
				OpCode string `json:"operator-code"`
			} `json:"properties"`
		} `json:"sim"`
	}
	if json.Unmarshal([]byte(out), &data) != nil {
		return simInfo{}
	}
	p := data.Sim.Properties
	return simInfo{p.Imsi, p.Iccid, p.OpName, p.OpCode}
}

// parseOwnNumber 从 mmcli generic 字段的 own-numbers 数组提取第一个号码。
func parseOwnNumber(g map[string]json.RawMessage) string {
	if v, ok := g["own-numbers"]; ok {
		var arr []string
		if json.Unmarshal(v, &arr) == nil && len(arr) > 0 {
			return arr[0]
		}
	}
	return ""
}

// getPhoneNumber 通过 AT+CNUM 命令从调制解调器查询本机号码（own-numbers 为空时的兜底方案）。
func getPhoneNumber(idx string) string {
	out, _, err := run(30*time.Second, "mmcli", "-m", idx, "--command=AT+CNUM", "-J")
	if err != nil {
		return ""
	}
	var data struct {
		Modem struct {
			Command struct {
				Response string `json:"response"`
			} `json:"command"`
		} `json:"modem"`
	}
	if json.Unmarshal([]byte(out), &data) != nil {
		return ""
	}
	if m := reCnum.FindStringSubmatch(data.Modem.Command.Response); m != nil {
		return m[1]
	}
	return ""
}

// mapState 将 mmcli 原始状态字符串映射为系统统一状态常量（connected/disconnected/error/unknown）。
func mapState(state string) string {
	switch strings.ToLower(state) {
	case "registered", "connected":
		return "connected"
	case "disabled", "disabling", "enabling", "searching":
		return "disconnected"
	case "failed":
		return "error"
	}
	return "unknown"
}

// SendSMS creates, sends and deletes an SMS object. Returns (success, message).
func SendSMS(mmIndex, phoneNumber, text string) (bool, string) {
	escaped := strings.ReplaceAll(text, `"`, `\"`)
	numEscaped := strings.ReplaceAll(phoneNumber, `"`, `\"`)
	createArg := `--messaging-create-sms=number="` + numEscaped + `",text="` + escaped + `"`
	out, stderr, err := run(30*time.Second, "mmcli", "-m", mmIndex, createArg)
	if err != nil {
		return false, stderr
	}
	m := reSmsIdx.FindStringSubmatch(out)
	if m == nil {
		return false, "Could not find created SMS index"
	}
	smsIdx := m[1]
	// 删除用 --messaging-delete-sms=<idx>；`-s <idx> --delete` 在 mmcli 1.20 会报
	// "no actions specified"，导致删除静默失败、SMS 对象在 modem 存储里堆积，最终写满存储
	// 使后续 create 失败。
	delSMS := func() { run(30*time.Second, "mmcli", "-m", mmIndex, "--messaging-delete-sms="+smsIdx) }
	_, stderr2, err2 := run(20*time.Second, "mmcli", "-m", mmIndex, "-s", smsIdx, "--send")
	if err2 == errTimeout {
		delSMS()
		return false, "发送超时：设备已注册但网络拒绝短信，请确认SIM卡已开通短信服务或VoLTE功能"
	}
	if err2 != nil {
		delSMS()
		return false, stderr2
	}
	delSMS()
	return true, "sent"
}

// ListInbox returns received (deliver) SMS messages for a modem.
func ListInbox(mmIndex string) []InboxMessage {
	out, _, err := run(30*time.Second, "mmcli", "-m", mmIndex, "--messaging-list-sms", "-J")
	if err != nil {
		return nil
	}
	var data struct {
		Paths []string `json:"modem.messaging.sms"`
	}
	if json.Unmarshal([]byte(out), &data) != nil {
		return nil
	}
	var msgs []InboxMessage
	for _, p := range data.Paths {
		m := reSmsIdxEnd.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		smsIdx := m[1]
		out2, _, e := run(30*time.Second, "mmcli", "-m", mmIndex, "-s", smsIdx, "-J")
		if e != nil {
			continue
		}
		// 同 getSimInfo：只声明关心的字符串字段，避免因未知字段类型不匹配导致整体解析失败。
		var sd struct {
			Sms struct {
				Content struct {
					Number string `json:"number"`
					Text   string `json:"text"`
				} `json:"content"`
				Properties struct {
					PduType   string `json:"pdu-type"`
					Timestamp string `json:"timestamp"`
					State     string `json:"state"`
				} `json:"properties"`
			} `json:"sms"`
		}
		if json.Unmarshal([]byte(out2), &sd) != nil {
			continue
		}
		if sd.Sms.Properties.PduType != "deliver" {
			continue
		}
		msgs = append(msgs, InboxMessage{
			SmsIndex:    smsIdx,
			PhoneNumber: sd.Sms.Content.Number,
			Content:     sd.Sms.Content.Text,
			Timestamp:   sd.Sms.Properties.Timestamp,
			State:       sd.Sms.Properties.State,
		})
	}
	return msgs
}

// DeleteSmsFromModem removes an inbound SMS object from the physical modem.
func DeleteSmsFromModem(mmObjectPath, mmSmsIndex string) bool {
	if strings.HasPrefix(mmObjectPath, "zte:") {
		return true
	}
	base := strings.SplitN(mmObjectPath, "/Modem/", 2)[0]
	smsPath := base + "/SMS/" + mmSmsIndex
	m := reModemIdx.FindStringSubmatch(mmObjectPath)
	if m == nil {
		return false
	}
	_, _, err := run(30*time.Second, "mmcli", "-m", m[1], "--messaging-delete-sms="+smsPath)
	return err == nil
}

// EnableModem enables a disabled modem.
func EnableModem(mmIndex string) bool {
	_, _, err := run(30*time.Second, "mmcli", "-m", mmIndex, "-e")
	return err == nil
}

// ResetModem 对模块做硬件级复位（触发 USB 重新枚举）。
// 用于换卡后模块固件卡在 sim-missing 等异常状态、仅重启 ModemManager 服务无法恢复的场景。
func ResetModem(mmIndex string) bool {
	_, _, err := run(30*time.Second, "mmcli", "-m", mmIndex, "--reset")
	return err == nil
}

// asString 将 json.RawMessage 转为字符串，支持带引号和不带引号两种格式。
func asString(r json.RawMessage) string {
	if r == nil {
		return ""
	}
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	return strings.Trim(string(r), `"`)
}

// joinList 将 json.RawMessage 数组转为逗号分隔字符串（用于 access-technologies、ports 等列表字段）。
func joinList(r json.RawMessage) string {
	if r == nil {
		return ""
	}
	var arr []string
	if json.Unmarshal(r, &arr) == nil {
		return strings.Join(arr, ",")
	}
	return asString(r)
}

// atoi64 将字符串解析为 int64，解析失败时返回 0。
func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
