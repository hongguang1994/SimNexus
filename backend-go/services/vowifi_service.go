package services

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"simnexus-go/models"
	"simnexus-go/services/vowifi"
)

// VowifiService 把自建的 VoWiFi 协议栈（services/vowifi 包）接进业务层：
// 当某张 SIM 卡切到 VoWiFi 模式时，用它建立 IKEv2/EAP-AKA/IMS-ESP 隧道并发短信，
// 而不是走 mmcli。该模式要求该卡已从 ModemManager 分离（独占串口 + 裸 4500 socket）。
type VowifiService struct{}

// NewVowifiService 创建实例。
func NewVowifiService() *VowifiService { return &VowifiService{} }

// buildConfig 从 modem 记录 + 环境变量兜底构造 vowifi.Config。
func (v *VowifiService) buildConfig(m *models.Modem) (vowifi.Config, error) {
	return buildVowifiConfig(m)
}

// buildVowifiConfig 从 modem 记录 + 环境变量兜底构造 vowifi.Config（供 VowifiManager 复用）。
func buildVowifiConfig(m *models.Modem) (vowifi.Config, error) {
	imsi := strings.TrimSpace(m.Imsi)
	if imsi == "" {
		return vowifi.Config{}, fmt.Errorf("该卡无 IMSI，无法建立 VoWiFi 会话")
	}
	mcc, mnc := splitMCCMNC(m.SimOperatorCode, imsi)
	if mcc == "" || mnc == "" {
		return vowifi.Config{}, fmt.Errorf("无法确定 MCC/MNC")
	}
	epdg := vwFirstNonEmpty(m.VowifiEpdgIP, os.Getenv("VOWIFI_EPDG_IP"))
	// 第二方案（opt-in）：VOWIFI_EPDG_DNS=1 时用 DoH 动态解析 ePDG FQDN，
	// 绕过系统解析器的 Clash fake-ip。解析成功则优先沿用配置里的已知可用 IP
	//（若它仍在解析结果中，兼顾路由安全），否则取解析到的第一个；解析失败回退配置值。
	fromDNS := false
	if os.Getenv("VOWIFI_EPDG_DNS") == "1" {
		epdg, fromDNS = resolveEpdgWithFallback(epdg, mcc, mnc)
	}
	if epdg == "" {
		return vowifi.Config{}, fmt.Errorf("未配置 ePDG 地址（modem.vowifi_epdg_ip 或环境变量 VOWIFI_EPDG_IP）")
	}
	atPort := vwFirstNonEmpty(m.VowifiATPort, os.Getenv("VOWIFI_AT_PORT"), guessATPort(m.Ports))
	if atPort == "" {
		return vowifi.Config{}, fmt.Errorf("未配置串口（modem.vowifi_at_port 或环境变量 VOWIFI_AT_PORT）")
	}
	imei := vwFirstNonEmpty(m.Imei, os.Getenv("VOWIFI_IMEI"), "351263400674742")
	return vowifi.Config{
		EPDGIP:      epdg,
		EPDGFromDNS: fromDNS,
		IMSI:        imsi,
		MCC:         mcc,
		MNC:         mnc,
		USIMAID:     vwFirstNonEmpty(os.Getenv("VOWIFI_USIM_AID"), "A0000000871002FF44FFFF8901010100"),
		APN:         vwFirstNonEmpty(os.Getenv("VOWIFI_APN"), "ims"),
		IMEI:        imei,
		ATPort:      atPort,
		QMIDevice:   vwFirstNonEmpty(os.Getenv("VOWIFI_QMI_DEV"), "/dev/cdc-wdm0"), // AT/QMI 互为兜底
		USIMSlot:    1,
		PreferQMI:   strings.EqualFold(os.Getenv("VOWIFI_AKA_ORDER"), "qmi"), // QMI 主、AT 备
		Verbose:     os.Getenv("VOWIFI_VERBOSE") != "",
	}, nil
}

// SendSMS 通过 VoWiFi 隧道发送一条短信。整条隧道（IKE→REGISTER→MESSAGE）在一次调用内完成后拆除。
// 返回是否成功及错误信息。
func (v *VowifiService) SendSMS(m *models.Modem, phone, content string) (bool, string) {
	cfg, err := v.buildConfig(m)
	if err != nil {
		return false, err.Error()
	}
	// VoWiFi 需要独占该卡的 AT 串口（跑 USIM AKA）。默认用 mmcli 的按卡抑制
	// (mmcli -m <idx> --inhibit) 只释放这一张卡，不影响同机其它卡的正常收发；
	// 抑制进程存活期间该卡对 ModemManager 不可见，退出即自动恢复。
	// VOWIFI_MANAGE_MM=0 完全关闭自动管理（该卡已由运维手动分离时用）；
	// VOWIFI_MANAGE_MM=stop 退回到全局停 ModemManager（inhibit 不可用时的兜底）。
	switch os.Getenv("VOWIFI_MANAGE_MM") {
	case "0":
		// 不管理，假定运维已释放该卡
	case "stop":
		stopModemManager()
		defer startModemManager()
		time.Sleep(2 * time.Second)
	default:
		release, ierr := inhibitModem(m.Imei, m.MmObjectPath)
		if ierr != nil {
			// inhibit 失败则兜底为全局停止，保证至少能独占
			stopModemManager()
			defer startModemManager()
		} else {
			defer release()
		}
		time.Sleep(2 * time.Second)
	}

	sess := vowifi.NewSession(cfg)
	defer sess.Close()
	if err := sess.Register(); err != nil {
		return false, "VoWiFi 注册失败: " + err.Error()
	}
	smsc := vwFirstNonEmpty(os.Getenv("VOWIFI_SMSC"), "+447802002606")
	code, err := sess.SendSMS(phone, content, smsc)
	if err != nil {
		return false, fmt.Sprintf("VoWiFi 发短信失败(code=%d): %s", code, err.Error())
	}
	return true, ""
}

// ---- helpers ----

func stopModemManager()  { exec.Command("systemctl", "stop", "ModemManager").Run() }
func startModemManager() { exec.Command("systemctl", "start", "ModemManager").Run() }

// resolveModemIndex 按 IMEI 在当前 ModemManager 里动态定位这张卡的 mmcli 索引。
// modem 每次 USB 重新枚举或 MM 重启后索引都会变（Modem/0→7→…），DB 里的 mm_object_path
// 很快过期；用过期索引去 inhibit 会打到别的/不存在的 modem，导致 MM 仍占着真卡的 AT 串口、
// 抢读 CCHO 响应。这里优先按稳定的 IMEI 匹配，匹配不到再退回旧路径解析。
func resolveModemIndex(imei, mmObjectPath string) string {
	if imei != "" {
		for _, mi := range ListModems() {
			if mi.Imei == imei && mi.MmIndex != "" {
				return mi.MmIndex
			}
		}
	}
	if m := reModemSvc.FindStringSubmatch(mmObjectPath); m != nil {
		return m[1]
	}
	return ""
}

// inhibitModem 用 `mmcli -m <idx> --inhibit` 让 ModemManager 释放指定的这一张卡
// （关闭其端口，使 AT 串口可被独占），返回的 release 函数结束抑制并交还给 MM。
// mmcli --inhibit 会一直阻塞持有抑制，所以后台启动、发完后 kill 即恢复。
// 索引按 IMEI 动态解析，避免 DB 路径过期打到错误的 modem。
func inhibitModem(imei, mmObjectPath string) (func(), error) {
	idx := resolveModemIndex(imei, mmObjectPath)
	if idx == "" {
		return nil, fmt.Errorf("无法定位 modem（imei=%s path=%s）", imei, mmObjectPath)
	}
	cmd := exec.Command("mmcli", "-m", idx, "--inhibit")
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 mmcli --inhibit 失败: %w", err)
	}
	return func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}, nil
}

// splitMCCMNC 优先从 SimOperatorCode（MCC+MNC 连写）解析，退化到 IMSI 前缀。
func splitMCCMNC(opCode, imsi string) (string, string) {
	opCode = strings.TrimSpace(opCode)
	if len(opCode) >= 5 {
		return opCode[:3], opCode[3:]
	}
	if len(imsi) >= 5 {
		// 无法可靠区分 2/3 位 MNC，默认按 2 位（英国 giffgaff=234/10 为 2 位）
		return imsi[:3], imsi[3:5]
	}
	return "", ""
}

// resolveEpdgWithFallback 用 DoH 动态解析 ePDG，解析成功时：
//   - 若配置的 static 仍在解析结果中，保留 static（已验证可用 + 路由已知安全）；
//   - 否则改用解析到的第一个 IP（说明运营商更换了 ePDG，static 已过期）。
//
// 解析失败或无结果则原样返回 static，绝不阻断发送。
// 返回 (选用的 ePDG IP, 是否来自 DNS 动态解析)。解析失败回退 static 时 fromDNS=false。
func resolveEpdgWithFallback(static, mcc, mnc string) (string, bool) {
	ips, err := vowifi.ResolveEPDG(mcc, mnc, 8*time.Second)
	if err != nil || len(ips) == 0 {
		if os.Getenv("VOWIFI_VERBOSE") != "" {
			vlog("ePDG 动态解析失败，回退配置值 %s：%v", static, err)
		}
		return static, false
	}
	for _, ip := range ips {
		if ip == static {
			if os.Getenv("VOWIFI_VERBOSE") != "" {
				vlog("ePDG 动态解析确认 %s 仍有效（候选 %v）", static, ips)
			}
			return static, true
		}
	}
	if os.Getenv("VOWIFI_VERBOSE") != "" {
		vlog("ePDG 配置值 %s 已不在解析结果，改用 %s（候选 %v）", static, ips[0], ips)
	}
	return ips[0], true
}

func vwFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// guessATPort 从 modem.Ports（如 "ttyUSB0,ttyUSB1,ttyUSB2(at)"）里猜一个 AT 串口。
func guessATPort(ports string) string {
	for _, p := range strings.FieldsFunc(ports, func(r rune) bool { return r == ',' || r == ' ' }) {
		p = strings.TrimSpace(p)
		if strings.Contains(p, "(at)") {
			name := strings.SplitN(p, "(", 2)[0]
			return "/dev/" + name
		}
	}
	return ""
}
