// 独立测试程序：直接调用 services/vowifi 包，在真机上验证移植后的 VoWiFi 协议栈
// （IKEv2/EAP-AKA/IMS-ESP）能否注册并发送一条短信。不依赖数据库/docker 后端。
//
// 用法示例（在停掉 ModemManager、释放调制解调器后运行）：
//   VOWIFI_VERBOSE=1 ./vowifitest -to +8619143397207 -text "go vowifi test"
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"simnexus-go/services/vowifi"
)

func main() {
	epdg := flag.String("epdg", env("EPDG_IP", "87.194.89.8"), "ePDG IPv4")
	imsi := flag.String("imsi", env("IMSI", "234100876634353"), "IMSI")
	mcc := flag.String("mcc", env("MCC", "234"), "MCC")
	mnc := flag.String("mnc", env("MNC", "10"), "MNC")
	atport := flag.String("atport", env("ATPORT", "/dev/ttyUSB2"), "AT 串口")
	imei := flag.String("imei", env("IMEI_DEV", "351263400674742"), "IMEI")
	smsc := flag.String("smsc", env("VOWIFI_SMSC", "+447802002606"), "SMSC")
	to := flag.String("to", "", "收件号码，如 +8619143397207")
	text := flag.String("text", "go vowifi test", "短信正文")
	serve := flag.Bool("serve", false, "常驻模式：注册+SUBSCRIBE 后运行接收循环，等待 MT 短信")
	serveMin := flag.Int("min", 5, "常驻模式运行分钟数")
	flag.Parse()

	cfg := vowifi.Config{
		EPDGIP: *epdg, IMSI: *imsi, MCC: *mcc, MNC: *mnc,
		ATPort: *atport, IMEI: *imei, APN: "ims",
		USIMAID: "A0000000871002FF44FFFF8901010100",
		Verbose: os.Getenv("VOWIFI_VERBOSE") != "",
	}
	cfg.Verbose = true

	if *serve {
		fmt.Println("[*] 常驻模式：建立会话（注册 + SUBSCRIBE）并运行接收循环...")
		d, err := vowifi.StartDaemon(cfg, vowifi.NewSteps(), func(sms vowifi.InboundSMS) {
			fmt.Printf("\n[✓✓✓] 收到 MT 短信！ sender=%q text=%q\n", sms.Sender, sms.Text)
		})
		if err != nil {
			fmt.Println("[!] 常驻会话建立失败:", err)
			os.Exit(1)
		}
		fmt.Printf("[*] 常驻会话已就绪，等待 %d 分钟接收 MT 短信...\n", *serveMin)
		time.Sleep(time.Duration(*serveMin) * time.Minute)
		d.Stop()
		fmt.Println("[*] 常驻结束")
		return
	}

	sess := vowifi.NewSession(cfg)
	defer sess.Close()

	fmt.Println("[*] 开始 IKEv2/EAP-AKA 注册...")
	if err := sess.Register(); err != nil {
		fmt.Println("[!] Register 失败:", err)
		os.Exit(1)
	}
	fmt.Printf("[*] IKE 隧道就绪，AssignedIPv6=%x P-CSCF数=%d\n", sess.AssignedIPv6, len(sess.PCSCFv6))

	if *to == "" {
		fmt.Println("[*] 未指定 -to，仅验证注册，退出")
		return
	}
	fmt.Printf("[*] 通过 VoWiFi 发短信到 %s ...\n", *to)
	code, err := sess.SendSMS(*to, *text, *smsc)
	if err != nil {
		fmt.Printf("[!] SendSMS 失败 code=%d: %v\n", code, err)
		os.Exit(2)
	}
	fmt.Printf("[✓] 发短信成功，SIP code=%d\n", code)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
