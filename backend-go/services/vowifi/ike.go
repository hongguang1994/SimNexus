package vowifi

// IKEv2 + EAP-AKA 会话层，对应 poc6 的主流程（IKE_SA_INIT → IKE_AUTH#1 → USIM AKA →
// EAP-Response → 最终 IKE_AUTH → 解析 CFG_REPLY）。Session 持有整条隧道的共享状态。

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"
)

var natMark = []byte{0, 0, 0, 0} // 非 ESP 标记，IKE 消息在 4500 上带此前缀

// Config 是建立一次 VoWiFi 会话所需的参数。
type Config struct {
	EPDGIP      string // ePDG IPv4，如 87.194.89.8
	EPDGFromDNS bool   // 该 IP 是否由 DoH 动态解析得来（供界面标记「DNS」）
	IMSI        string
	MCC         string
	MNC         string // 保持与 NAI 一致（如 "10"）
	USIMAID     string // 默认 A0000000871002FF44FFFF8901010100
	APN         string // 默认 ims
	IMEI        string // DEVICE_IDENTITY 用
	ATPort      string // 串口设备，如 /dev/ttyUSB2
	// USIM AKA 的 QMI 备选：配置了 QMIDevice（如 /dev/cdc-wdm0）则 AT 失败时回退到 QMI
	// （qmicli 逻辑通道 + Send APDU）。USIMSlot 一般为 1。
	QMIDevice string
	USIMSlot  int
	PreferQMI bool // true 时 USIM AKA 走 QMI 主、AT 备（env VOWIFI_AKA_ORDER=qmi）
	Verbose   bool
}

func (c *Config) nai() string {
	return "0" + c.IMSI + "@nai.epc.mnc0" + c.MNC + ".mcc" + c.MCC + ".3gppnetwork.org"
}
func (c *Config) homeDomain() string {
	return "ims.mnc0" + c.MNC + ".mcc" + c.MCC + ".3gppnetwork.org"
}

// Session 持有 IKE/ESP 隧道及派生密钥。
type Session struct {
	cfg  Config
	conn *net.UDPConn
	epdg *net.UDPAddr

	spii, spir []byte
	x          *big.Int
	Ni, Nr     []byte
	initMsg    []byte // IKE_SA_INIT 请求体（AUTH 签名要用）

	skD, skAi, skAr, skEi, skEr, skPi, skPr []byte
	lastSKRaw                               []byte

	// 最终 IKE_AUTH 拿到的结果
	AssignedIPv6 []byte
	PCSCFv6      [][]byte
	// IMS 注册 200 OK 的 P-Associated-URI 解析出的本机号码（MSISDN），如 +447902247547
	MSISDN    string
	spirChild []byte
	// 子 SA（outer ESP）密钥
	encrI, authI, encrR, authR []byte
	espSeq                     uint32
	imsSeq                     uint32
	steps                      *Steps // 分步骤状态追踪（可空）

	// 存活时间戳：收到任意内层 ESP 包 / MO 发送成功 / 周期心跳重注册成功 时刷新。
	// 看门狗据此判断会话是否"悄悄死掉"需要重建。
	aliveMu   sync.Mutex
	lastAlive time.Time
}

// touch 刷新存活时间戳（有生命迹象时调用）。
func (s *Session) touch() {
	s.aliveMu.Lock()
	s.lastAlive = time.Now()
	s.aliveMu.Unlock()
}

// LastAlive 返回最近一次有生命迹象的时刻。
func (s *Session) LastAlive() time.Time {
	s.aliveMu.Lock()
	defer s.aliveMu.Unlock()
	return s.lastAlive
}

// NewSession 用给定配置创建一个 VoWiFi 会话（尚未建立连接，调用 Register 才开始握手）。
func NewSession(cfg Config) *Session {
	return &Session{cfg: cfg}
}

// LogHook 若被上层设置，则每条 VoWiFi 详细日志除打到 stdout 外，也回调它（用于汇入日志缓冲、
// 经 WebSocket 推给前端）。仅在 Verbose 时触发，与 stdout 输出保持一致。
var LogHook func(msg string)

func (s *Session) logf(format string, a ...any) {
	if s.cfg.Verbose {
		msg := fmt.Sprintf(format, a...)
		fmt.Printf("   [vowifi] %s\n", msg)
		if LogHook != nil {
			LogHook(msg)
		}
	}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

// Dial 建立到 ePDG 的 UDP(4500) 套接字。
func (s *Session) dial() error {
	addr := &net.UDPAddr{IP: net.ParseIP(s.cfg.EPDGIP), Port: 4500}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 4500})
	if err != nil {
		return fmt.Errorf("绑定本地 4500 失败: %w", err)
	}
	s.conn = c
	s.epdg = addr
	return nil
}

// Close 关闭套接字。
func (s *Session) Close() {
	if s.conn != nil {
		s.conn.Close()
	}
}

// natdNotify 构造 NAT_DETECTION_SOURCE/DEST 通知，对应 poc6 natd_notify。
func (s *Session) natdNotify(nxt byte, ntype uint16, ipb []byte, port uint16) []byte {
	h := sha1.New()
	h.Write(s.spii)
	h.Write(make([]byte, 8)) // SPIr=0
	h.Write(ipb)
	h.Write(be16(port))
	return notifyP(nxt, ntype, h.Sum(nil))
}

// sendRecvSAInit 发送 IKE_SA_INIT 并带指数退避重传 + SPI 校验，对应 poc6 send_recv_with_retry。
func (s *Session) sendRecvSAInit(raw []byte, retries int, base float64) ([]byte, error) {
	buf := make([]byte, 4096)
	for attempt := 0; attempt < retries; attempt++ {
		s.conn.WriteToUDP(raw, s.epdg)
		to := time.Duration(base*pow15(attempt)*1000) * time.Millisecond
		deadline := time.Now().Add(to)
		for {
			s.conn.SetReadDeadline(deadline)
			n, _, err := s.conn.ReadFromUDP(buf)
			if err != nil {
				s.logf("IKE_SA_INIT 第%d次超时，重传...", attempt+1)
				break
			}
			if n < 4 || !equal(buf[:4], natMark) {
				continue
			}
			d := buf[4:n]
			if len(d) < 8 || !equal(d[0:8], s.spii) {
				s.logf("收到 SPI 不匹配的陈旧响应，忽略")
				continue
			}
			out := make([]byte, len(d))
			copy(out, d)
			return out, nil
		}
	}
	return nil, fmt.Errorf("IKE_SA_INIT 重传%d次后仍无响应", retries)
}

// IKESAInit 执行 IKE_SA_INIT 并派生 SKEYSEED/SK_* 密钥。
func (s *Session) ikeSAInit() error {
	s.spii = randBytes(8)
	s.x = new(big.Int).SetBytes(randBytes(256))
	kei := dhPublic(s.x)
	s.Ni = randBytes(32)

	epdgIPb := net.ParseIP(s.cfg.EPDGIP).To4()
	natdSrc := s.natdNotify(41, 16388, []byte{0, 0, 0, 0}, 4500)
	natdDst := s.natdNotify(0, 16389, epdgIPb, 4500)

	body := concat(saIKE(34), keP(40, kei), noP(41, s.Ni), natdSrc, natdDst)

	// IKE header: SPIi | SPIr(0) | nextpayload=33(SA) | ver=0x20 | exch=34(SA_INIT) | flags=0x08 | msgid=0 | len
	hdr := append(append([]byte{}, s.spii...), make([]byte, 8)...)
	hdr = append(hdr, 33, 0x20, 34, 0x08)
	hdr = append(hdr, be32(0)...)
	hdr = append(hdr, be32(uint32(28+len(body)))...)
	s.initMsg = append(hdr, body...)

	resp, err := s.sendRecvSAInit(append(natMark, s.initMsg...), 6, 3)
	if err != nil {
		return err
	}
	s.spir = append([]byte{}, resp[8:16]...)
	pls := parsePayloads(resp[16], resp[28:])
	var ker []byte
	for _, p := range pls {
		switch p.Type {
		case 34: // KE
			ker = p.Body[4:]
		case 40: // Nonce
			s.Nr = p.Body
		}
	}
	if ker == nil || s.Nr == nil {
		return fmt.Errorf("IKE_SA_INIT 响应缺少 KE 或 Nonce")
	}
	s.logf("IKE_SA_INIT ok, SPIr=%x", s.spir)

	gir := dhShared(ker, s.x)
	skeyseed := prf(append(append([]byte{}, s.Ni...), s.Nr...), gir)
	seed := append(append(append(append([]byte{}, s.Ni...), s.Nr...), s.spii...), s.spir...)
	km := prfPlus(skeyseed, seed, 32*7)
	s.skD = km[0:32]
	s.skAi = km[32:64]
	s.skAr = km[64:96]
	s.skEi = km[96:128]
	s.skEr = km[128:160]
	s.skPi = km[160:192]
	s.skPr = km[192:224]
	return nil
}

// sendSK 加密并发送一个 SK 保护的消息，对应 poc6 send_sk。
func (s *Session) sendSK(inner []byte, firstInner byte, mid uint32, exch byte) error {
	iv := randBytes(16)
	pad := (16 - ((len(inner) + 1) % 16)) % 16
	padded := append(append(append([]byte{}, inner...), make([]byte, pad)...), byte(pad))
	ct, err := aesCBCEncrypt(s.skEi, iv, padded)
	if err != nil {
		return err
	}
	skb := append(iv, ct...)
	skLen := 4 + len(skb) + 16
	tot := 28 + skLen
	hdr := append(append([]byte{}, s.spii...), s.spir...)
	hdr = append(hdr, 46, 0x20, exch, 0x08)
	hdr = append(hdr, be32(mid)...)
	hdr = append(hdr, be32(uint32(tot))...)
	skHdr := append([]byte{firstInner, 0}, be16(uint16(skLen))...)
	m := append(append(hdr, skHdr...), skb...)
	icv := hmacSHA256(s.skAi, m)[:16]
	raw := append(append(natMark, m...), icv...)
	s.lastSKRaw = raw
	_, err = s.conn.WriteToUDP(raw, s.epdg)
	return err
}

// recvSK 接收并解密一个 SK 保护的消息，对应 poc6 recv_sk（含丢包重发上一条）。
// 返回 (内层首载荷类型, 解密后的内层字节)。
func (s *Session) recvSK(timeout float64, retries int, base float64) (byte, []byte, error) {
	buf := make([]byte, 4096)
	for attempt := 0; ; {
		eff := base * pow15(attempt)
		if timeout > 0 {
			eff = timeout
		}
		s.conn.SetReadDeadline(time.Now().Add(time.Duration(eff*1000) * time.Millisecond))
		gotThisRound := false
		for i := 0; i < 20; i++ {
			n, _, err := s.conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			gotThisRound = true
			if n < 4 || !equal(buf[:4], natMark) {
				continue // ESP 数据包，跳过
			}
			d := buf[4:n]
			if len(d) < 28 || !equal(d[0:8], s.spii) || !equal(d[8:16], s.spir) {
				continue
			}
			// 找到 Encrypted(46) 载荷
			first := d[16]
			off := 28
			nxt := first
			var body []byte
			var innerFirst byte
			for nxt != 0 && off+4 <= len(d) {
				pn := d[off]
				pl := int(binary.BigEndian.Uint16(d[off+2 : off+4]))
				if pl < 4 {
					break
				}
				if nxt == 46 {
					body = d[off+4 : off+pl]
					innerFirst = pn
					break
				}
				nxt = pn
				off += pl
			}
			if body == nil || len(body) < 32 {
				continue
			}
			iv := body[:16]
			ct := body[16 : len(body)-16]
			pt, err := aesCBCDecrypt(s.skEr, iv, ct)
			if err != nil || len(pt) == 0 {
				continue
			}
			padLen := int(pt[len(pt)-1])
			if 1+padLen > len(pt) {
				continue
			}
			pt = pt[:len(pt)-1-padLen]
			return innerFirst, pt, nil
		}
		_ = gotThisRound
		attempt++
		if attempt >= retries || s.lastSKRaw == nil {
			return 0, nil, fmt.Errorf("no IKE packet (重试%d次后放弃)", attempt)
		}
		s.logf("IKE_AUTH 第%d次超时，重发上一条消息", attempt)
		s.conn.WriteToUDP(s.lastSKRaw, s.epdg)
	}
}

// Register 执行完整的 IKEv2+EAP-AKA 注册，成功后 Session 上带有 AssignedIPv6/PCSCFv6/child SA 密钥。
func (s *Session) Register() error {
	if err := s.dial(); err != nil {
		s.step(StepIKEInit, StepFail, err.Error())
		return err
	}
	s.step(StepIKEInit, StepRunning, "")
	if err := s.ikeSAInit(); err != nil {
		s.step(StepIKEInit, StepFail, err.Error())
		return err
	}
	s.step(StepIKEInit, StepOK, "SPIr="+fmt.Sprintf("%x", s.spir))
	s.step(StepEAPAKA, StepRunning, "")

	// ---- IKE_AUTH #1: IDi + IDr(APN) + EAP_ONLY + CP + SA(ESP) + TS ----
	spiChild := randBytes(4)
	nai := s.cfg.nai()
	apn := s.cfg.APN
	if apn == "" {
		apn = "ims"
	}
	var inner []byte
	if apn != "" {
		inner = concat(idiP(36, nai), idrP(41, apn), notifyP(47, 16417, nil),
			cpP(33), saESP(44, spiChild), tsP(45), tsP(0))
	} else {
		inner = concat(idiP(41, nai), notifyP(47, 16417, nil),
			cpP(33), saESP(44, spiChild), tsP(45), tsP(0))
	}
	if err := s.sendSK(inner, 35, 1, 35); err != nil {
		return err
	}
	first, pt, err := s.recvSK(0, 1, 20)
	if err != nil {
		return fmt.Errorf("等待 EAP-AKA Challenge 失败: %w", err)
	}
	ipls := parsePayloads(first, pt)
	if s.cfg.Verbose {
		for _, p := range ipls {
			s.logf("IKE_AUTH#1 响应载荷 type=%d len=%d head=%x", p.Type, len(p.Body), p.Body[:min(12, len(p.Body))])
		}
	}
	var rand16, autn16, chalEAP []byte
	var eapID byte
	deviceIDRequested := false
	for _, p := range ipls {
		if p.Type == 41 && len(p.Body) >= 4 {
			nt := binary.BigEndian.Uint16(p.Body[2:4])
			if nt == 41101 {
				deviceIDRequested = true
			}
		}
	}
	for _, p := range ipls {
		if p.Type == 48 { // EAP payload
			chalEAP = p.Body
			eapID = p.Body[1]
			i := 8
			for i < len(p.Body) {
				at := p.Body[i]
				al := int(p.Body[i+1]) * 4
				if al == 0 {
					break
				}
				v := p.Body[i+2 : i+al]
				if at == 1 {
					rand16 = v[2:18]
				} else if at == 2 {
					autn16 = v[2:18]
				}
				i += al
			}
		}
	}
	if rand16 == nil || autn16 == nil {
		s.step(StepEAPAKA, StepFail, "缺少 RAND/AUTN")
		return fmt.Errorf("EAP-AKA Challenge 缺少 RAND/AUTN")
	}
	s.logf("EAP-AKA Challenge RAND=%x AUTN=%x", rand16, autn16)

	// ---- USIM AKA（串口）----
	aid := s.cfg.USIMAID
	if aid == "" {
		aid = "A0000000871002FF44FFFF8901010100"
	}
	aka, err := s.usimAKA(aid, rand16, autn16)
	if err != nil {
		s.step(StepEAPAKA, StepFail, "USIM AKA: "+err.Error())
		return fmt.Errorf("USIM AKA 失败: %w", err)
	}
	s.logf("SIM 认证成功 RES=%x", aka.RES)

	// ---- EAP-AKA 密钥派生 (RFC4187) ----
	mkh := sha1.New()
	mkh.Write([]byte(nai))
	mkh.Write(aka.IK)
	mkh.Write(aka.CK)
	mk := mkh.Sum(nil)
	keys := fips186PRF(mk, 16+16+64+64)
	kAut := keys[16:32]
	msk := keys[32:96]

	// 校验 challenge 的 AT_MAC
	var recvMAC []byte
	i := 8
	for i < len(chalEAP) {
		at := chalEAP[i]
		al := int(chalEAP[i+1]) * 4
		if al == 0 {
			break
		}
		if at == 11 {
			recvMAC = chalEAP[i+4 : i+20]
		}
		i += al
	}
	calc := hmacSHA1(kAut, zeroATMAC(chalEAP))[:16]
	if !equal(calc, recvMAC) {
		s.logf("警告：Challenge AT_MAC 不匹配（身份或密钥问题），继续尝试")
	}

	// ---- 构造 EAP-Response/AKA-Challenge ----
	atRes := append(append([]byte{3, 3}, be16(uint16(len(aka.RES)*8))...), aka.RES...)
	atMac := append([]byte{11, 5, 0, 0}, make([]byte, 16)...)
	respEAP := append(append([]byte{2, eapID}, be16(0)...), append([]byte{23, 1, 0, 0}, append(atRes, atMac...)...)...)
	binary.BigEndian.PutUint16(respEAP[2:4], uint16(len(respEAP)))
	mac := hmacSHA1(kAut, respEAP)[:16]
	copy(respEAP[len(respEAP)-16:], mac)

	if deviceIDRequested {
		msg2 := append(deviceIDNotify(48, s.cfg.IMEI), eapP(0, respEAP)...)
		if err := s.sendSK(msg2, 41, 2, 35); err != nil {
			return err
		}
	} else {
		if err := s.sendSK(eapP(0, respEAP), 48, 2, 35); err != nil {
			return err
		}
	}
	first, pt, err = s.recvSK(0, 5, 3)
	if err != nil {
		s.step(StepEAPAKA, StepFail, "等待 EAP-Success: "+err.Error())
		return fmt.Errorf("等待 EAP-Success 失败: %w", err)
	}
	ipls = parsePayloads(first, pt)
	eapOK := false
	for _, p := range ipls {
		if p.Type == 48 && len(p.Body) > 0 && p.Body[0] == 3 {
			eapOK = true
		}
	}
	if !eapOK {
		s.step(StepEAPAKA, StepFail, "未收到 EAP-Success")
		return fmt.Errorf("未收到 EAP-Success")
	}
	s.logf("EAP-Success ✓")
	s.step(StepEAPAKA, StepOK, "EAP-Success")
	s.step(StepIKEAuth, StepRunning, "")

	// ---- 最终 IKE_AUTH (mid=3) AUTH ----
	macedID := prf(s.skPi, idRest(nai))
	signed := append(append(append([]byte{}, s.initMsg...), s.Nr...), macedID...)
	authData := prf(prf(msk, []byte("Key Pad for IKEv2")), signed)
	if err := s.sendSK(authP(0, authData), 39, 3, 35); err != nil {
		s.step(StepIKEAuth, StepFail, err.Error())
		return err
	}
	first, pt, err = s.recvSK(0, 1, 20)
	if err != nil {
		s.step(StepIKEAuth, StepFail, "等待响应: "+err.Error())
		return fmt.Errorf("等待最终 IKE_AUTH 响应失败: %w", err)
	}
	ipls = parsePayloads(first, pt)
	s.parseFinalAuth(ipls)
	if s.AssignedIPv6 == nil || s.spirChild == nil {
		s.step(StepIKEAuth, StepFail, "无分配 IPv6/SPI（被 Notify 拒绝）")
		return fmt.Errorf("最终 IKE_AUTH 缺少分配的 IPv6 或 ESP SPI（可能被 Notify 拒绝）")
	}
	s.step(StepIKEAuth, StepOK, fmt.Sprintf("IPv6=%x", s.AssignedIPv6))

	// 派生子 SA（outer ESP）密钥：KEYMAT = prf+(SK_d, Ni|Nr)
	keymat := prfPlus(s.skD, append(append([]byte{}, s.Ni...), s.Nr...), 32*4)
	s.encrI = keymat[0:32]
	s.authI = keymat[32:64]
	s.encrR = keymat[64:96]
	s.authR = keymat[96:128]
	s.imsSeq = 1
	s.logf("子 SA 密钥派生完成，AssignedIPv6=%x", s.AssignedIPv6)
	return nil
}

// parseFinalAuth 从最终 IKE_AUTH 响应里取分配的 IPv6、P-CSCF、子 SA 的 responder SPI。
func (s *Session) parseFinalAuth(ipls []ikePayload) {
	for _, p := range ipls {
		b := p.Body
		switch p.Type {
		case 47: // CP CFG_REPLY
			i := 4
			for i+4 <= len(b) {
				at := binary.BigEndian.Uint16(b[i:i+2]) & 0x7fff
				al := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
				if i+4+al > len(b) {
					break
				}
				v := b[i+4 : i+4+al]
				switch {
				case at == 8 && al >= 16:
					s.AssignedIPv6 = append([]byte{}, v[:16]...)
				case at == 21 && al >= 16:
					s.PCSCFv6 = append(s.PCSCFv6, append([]byte{}, v[:16]...))
				}
				i += 4 + al
			}
		case 33: // SA - child ESP responder SPI
			if len(b) >= 8 {
				spisize := int(b[6])
				if 8+spisize <= len(b) {
					s.spirChild = append([]byte{}, b[8:8+spisize]...)
				}
			}
		}
	}
}

// ---- 小工具 ----

func pow15(n int) float64 {
	r := 1.0
	for i := 0; i < n; i++ {
		r *= 1.5
	}
	return r
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func hmacSHA1(key, data []byte) []byte {
	m := hmac.New(sha1.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// zeroATMAC 把 EAP 里 AT_MAC 属性的 16 字节 MAC 清零，用于校验/计算 MAC。
func zeroATMAC(eap []byte) []byte {
	b := append([]byte{}, eap...)
	i := 8
	for i < len(b) {
		at := b[i]
		al := int(b[i+1]) * 4
		if al == 0 {
			break
		}
		if at == 11 {
			for j := i + 4; j < i+al && j < len(b); j++ {
				b[j] = 0
			}
		}
		i += al
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
