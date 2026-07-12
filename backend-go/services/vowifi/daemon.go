package vowifi

// Daemon 是一张卡的常驻 VoWiFi 会话：建立 IKE 隧道 + 常开 IMS 注册，后台单 goroutine
// 同时负责接收 MT 短信、处理 MO 发送请求、keepalive 与到期重注册。上层（VowifiManager）
// 用它把一张卡的收发都收拢到一个常驻会话里。

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// formatIPv6 把 16 字节的 IPv6 地址格式化为字符串（空则返回空串）。
func formatIPv6(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	return net.IP(b).String()
}

type sendReq struct {
	to, text, smsc string
	resp           chan sendResp
}
type sendResp struct {
	code int
	err  error
}

// Daemon 表示一个常驻会话。
type Daemon struct {
	sess   *Session
	sendCh chan sendReq
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	steps  *Steps
}

// StartDaemon 建立 IKE + IMS 注册并启动常驻收发循环。onSMS 在收到 MT 短信时被调用。
// steps 记录各步骤状态（可空）。返回后即处于常驻状态；失败则返回错误且不占用资源。
// 这条经代理到英国 ePDG 的链路丢包率偏高，单次注册常因丢包失败；每次用全新 IKE SA
// 重试若干次（不重传旧消息，避免 ePDG 把重复消息当异常）。
func StartDaemon(cfg Config, steps *Steps, onSMS func(InboundSMS)) (*Daemon, error) {
	var sess *Session
	var rc *regContext
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(3+attempt*2) * time.Second)
		}
		sess = NewSession(cfg)
		sess.attachSteps(steps)
		if err := sess.Register(); err != nil {
			lastErr = fmt.Errorf("IKE 注册失败: %w", err)
			sess.Close()
			continue
		}
		var err error
		rc, err = sess.imsRegister(true, nil)
		if err != nil {
			lastErr = fmt.Errorf("IMS 注册失败: %w", err)
			sess.Close()
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return nil, lastErr
	}
	sess.logf("VoWiFi 常驻会话已建立（IMS 已注册）")
	steps.Set(StepReceiver, StepOK, "运行中")
	d := &Daemon{
		sess:   sess,
		sendCh: make(chan sendReq),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		steps:  steps,
	}
	go func() {
		defer close(d.done)
		sess.RunReceiver(rc, d.stop, d.sendCh, onSMS)
	}()
	return d, nil
}

// SessionInfo 常驻会话的运行态信息，用于界面展示 VoWiFi 链路（区别于独占前的 mmcli 旧值）。
type SessionInfo struct {
	EPDGIP      string `json:"epdg_ip"`       // 连接的 ePDG IPv4
	EPDGFromDNS bool   `json:"epdg_from_dns"` // ePDG IP 是否由 DoH 动态解析得来
	TunnelIPv6  string `json:"tunnel_ipv6"`   // ePDG 分配的隧道内网 IPv6
	PCSCFCount  int    `json:"pcscf_count"`   // 拿到的 P-CSCF 数量
	Registered  bool   `json:"registered"`    // IMS 是否已注册
	MSISDN      string `json:"msisdn"`        // IMS 注册返回的本机号码（P-Associated-URI）
}

// LastAlive 返回会话最近一次有生命迹象的时刻（供上层看门狗判死自愈）。
func (d *Daemon) LastAlive() time.Time { return d.sess.LastAlive() }

// Info 返回当前会话的运行态快照。
func (d *Daemon) Info() SessionInfo {
	return SessionInfo{
		EPDGIP:      d.sess.cfg.EPDGIP,
		EPDGFromDNS: d.sess.cfg.EPDGFromDNS,
		TunnelIPv6:  formatIPv6(d.sess.AssignedIPv6),
		PCSCFCount:  len(d.sess.PCSCFv6),
		Registered:  d.sess.AssignedIPv6 != nil,
		MSISDN:      d.sess.MSISDN,
	}
}

// Send 通过常驻会话发一条 MO 短信，阻塞直到拿到 SIP 响应码。
func (d *Daemon) Send(to, text, smsc string) (int, error) {
	if smsc == "" {
		smsc = "+447802002606"
	}
	req := sendReq{to: to, text: text, smsc: smsc, resp: make(chan sendResp, 1)}
	select {
	case d.sendCh <- req:
	case <-d.stop:
		return 0, fmt.Errorf("会话已停止")
	case <-time.After(60 * time.Second):
		return 0, fmt.Errorf("发送请求超时（接收循环忙）")
	}
	select {
	case r := <-req.resp:
		return r.code, r.err
	case <-time.After(120 * time.Second): // 发前重注册含 USIM AKA + REGISTER，留足时间
		return 0, fmt.Errorf("等待发送结果超时")
	}
}

// Stop 停止常驻会话并拆除隧道。
func (d *Daemon) Stop() {
	d.once.Do(func() {
		close(d.stop)
		<-d.done
		d.sess.Close()
	})
}
