package vowifi

// SIP 层：REGISTER（首条 UDP 触发 401 + 第二条认证 TCP）、HTTP Digest AKAv1-MD5、
// TCP-over-IMS-ESP（SYN 指纹 + MSS 分段 + 重传 + 连接复用）、以及 MO 短信编码与发送。
// 对应 poc6 的 build_register / send_and_wait_sip / tcp_ims_register / tcp_send_on_conn /
// build_sms_submit_tpdu / build_rp_data_mo / SIP MESSAGE 构造。

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TCP 标志位
const (
	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
	tcpPSH = 0x08
	tcpACK = 0x10
)

// sipIdentity 保存一次 SMS 会话的 SIP 层参数。
type sipIdentity struct {
	homeDomain  string
	impi, impu  string
	ueSpiC      uint32
	ueSpiS      uint32
	uePortC     uint16
	uePortS     uint16
	secClient   string
	deviceUUID  string
	sipInstance string
	userAgent   string
	wlanNodeID  string
	paniCountry string
}

func (s *Session) newSIPIdentity() *sipIdentity {
	hd := s.cfg.homeDomain()
	id := &sipIdentity{
		homeDomain:  hd,
		impi:        s.cfg.IMSI + "@" + hd,
		impu:        "sip:" + s.cfg.IMSI + "@" + hd,
		ueSpiC:      binary.BigEndian.Uint32(randBytes(4)) | 0x01000000,
		ueSpiS:      binary.BigEndian.Uint32(randBytes(4)) | 0x01000000,
		uePortC:     6100,
		uePortS:     6101,
		userAgent:   "iOS/18.2.1 iPhone (iPhone15,4)",
		wlanNodeID:  "001a2b3c4d5e",
		paniCountry: "GB",
	}
	u := hex.EncodeToString(randBytes(16))
	id.deviceUUID = fmt.Sprintf("%s-%s-%s-%s-%s", u[0:8], u[8:12], u[12:16], u[16:20], u[20:32])
	imei := s.cfg.IMEI
	if len(imei) >= 14 {
		id.sipInstance = fmt.Sprintf("<urn:gsma:imei:%s-%s>", imei[:8], imei[8:14])
	} else {
		id.sipInstance = "<urn:gsma:imei:86034905-607353>"
	}
	id.secClient = fmt.Sprintf("ipsec-3gpp;alg=hmac-sha-1-96;ealg=null;spi-c=%d;spi-s=%d;port-c=%d;port-s=%d",
		id.ueSpiC, id.ueSpiS, id.uePortC, id.uePortS)
	return id
}

func md5hex(b []byte) string {
	h := md5.Sum(b)
	return hex.EncodeToString(h[:])
}

// buildRegister 构造一条 REGISTER，对应 poc6 build_register。
func (s *Session) buildRegister(id *sipIdentity, srcIPv6 []byte, callid, fromtag, branch string,
	cseq int, vport uint16, authNonce, authResp, secVerify, route, transport string) []byte {
	srcStr := ipv6Bracket(srcIPv6)
	var authhdr string
	if authNonce == "" && authResp == "" {
		authhdr = fmt.Sprintf(`Authorization: Digest username="%s", realm="%s", nonce="", uri="sip:%s", response="", algorithm=AKAv1-MD5`+"\r\n",
			id.impi, id.homeDomain, id.homeDomain)
	} else {
		authhdr = fmt.Sprintf(`Authorization: Digest username="%s", realm="%s", nonce="%s", uri="sip:%s", response="%s", algorithm=AKAv1-MD5`+"\r\n",
			id.impi, id.homeDomain, authNonce, id.homeDomain, authResp)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("REGISTER sip:%s SIP/2.0\r\n", id.homeDomain))
	sb.WriteString(fmt.Sprintf("Via: SIP/2.0/%s %s:%d;branch=%s;rport\r\n", transport, srcStr, vport, branch))
	if route != "" {
		sb.WriteString("Route: " + route + "\r\n")
	}
	sb.WriteString("Max-Forwards: 70\r\n")
	sb.WriteString(fmt.Sprintf("From: <%s>;tag=%s\r\n", id.impu, fromtag))
	sb.WriteString(fmt.Sprintf("To: <%s>\r\n", id.impu))
	sb.WriteString(fmt.Sprintf("Call-ID: %s\r\n", callid))
	sb.WriteString(fmt.Sprintf("CSeq: %d REGISTER\r\n", cseq))
	sb.WriteString(authhdr)
	sb.WriteString(fmt.Sprintf("Contact: <sip:%s@%s:%d>;+sip.instance=\"%s\"\r\n", id.deviceUUID, srcStr, vport, id.sipInstance))
	sb.WriteString("Security-Client: " + id.secClient + "\r\n")
	if secVerify != "" {
		sb.WriteString("Security-Verify: " + secVerify + "\r\n")
	}
	sb.WriteString("Require: sec-agree\r\n")
	sb.WriteString("Proxy-Require: sec-agree\r\n")
	sb.WriteString("Supported: path, gruu, sec-agree\r\n")
	sb.WriteString("Expires: 600000\r\n")
	sb.WriteString("Allow: REGISTER, INVITE, ACK, CANCEL, BYE, MESSAGE, UPDATE, PRACK\r\n")
	sb.WriteString(fmt.Sprintf("P-Preferred-Identity: <%s>\r\n", id.impu))
	sb.WriteString("User-Agent: " + id.userAgent + "\r\n")
	sb.WriteString(fmt.Sprintf("P-Access-Network-Info: IEEE-802.11; i-wlan-node-id=\"%s\";country=%s\r\n", id.wlanNodeID, id.paniCountry))
	sb.WriteString("Content-Length: 0\r\n\r\n")
	return []byte(sb.String())
}

func ipv6Bracket(b []byte) string {
	parts := make([]string, 8)
	for i := 0; i < 8; i++ {
		parts[i] = fmt.Sprintf("%02x%02x", b[i*2], b[i*2+1])
	}
	return "[" + strings.Join(parts, ":") + "]"
}

// buildUDPOverIPv6 构造完整内层 IPv6+UDP 包，对应 poc6 build_udp_over_ipv6。
func buildUDPOverIPv6(src, dst []byte, sport, dport uint16, payload []byte) []byte {
	udp := buildUDP(src, dst, sport, dport, payload)
	ipv6 := append(append(be32(0x60000000), be16(uint16(len(udp)))...), byte(17), byte(64))
	ipv6 = append(ipv6, src...)
	ipv6 = append(ipv6, dst...)
	return append(ipv6, udp...)
}

// sendAndWaitSIP 通过外层 ESP 隧道发一个内层 IPv6+UDP 的 SIP 包并等响应，对应 poc6 send_and_wait_sip。
func (s *Session) sendAndWaitSIP(udpPkt []byte, attempts int, timeout time.Duration) string {
	s.espEncryptSend(udpPkt, 41)
	for i := 0; i < attempts; i++ {
		if i > 0 && i%3 == 0 {
			s.espEncryptSend(udpPkt, 41)
		}
		_, _, nh, inner, err := s.espRecvDecrypt(timeout)
		if err != nil {
			continue
		}
		if nh == 41 && len(inner) >= 40 {
			ip6nh := inner[6]
			l4 := inner[40:]
			if ip6nh == 17 && len(l4) >= 8 {
				payload := l4[8:]
				if len(payload) >= 20 && strings.Contains(string(payload[:20]), "SIP/2.0") {
					return string(payload)
				}
			}
		}
	}
	return ""
}

var reStatus = regexp.MustCompile(`^SIP/2\.0\s+(\d+)`)

func parseSIPResponse(text string) (int, map[string][]string) {
	lines := strings.Split(text, "\r\n")
	code := 0
	if len(lines) > 0 {
		if m := reStatus.FindStringSubmatch(lines[0]); m != nil {
			code, _ = strconv.Atoi(m[1])
		}
	}
	hdrs := map[string][]string{}
	for _, ln := range lines[1:] {
		if ln == "" || !strings.Contains(ln, ":") {
			continue
		}
		kv := strings.SplitN(ln, ":", 2)
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		hdrs[k] = append(hdrs[k], v)
	}
	return code, hdrs
}

var reAlg = regexp.MustCompile(`alg=([a-zA-Z0-9-]+)`)

// parseSecurityServer 取带 spi-c/spi-s 的那条机制项，对应 poc6 parse_security_server。
func parseSecurityServer(raw string) map[string]string {
	for _, entry := range strings.Split(raw, ",") {
		if strings.Contains(entry, "spi-c=") && strings.Contains(entry, "spi-s=") {
			p := map[string]string{}
			for _, kv := range strings.Split(entry, ";") {
				if strings.Contains(kv, "=") {
					x := strings.SplitN(kv, "=", 2)
					p[strings.TrimSpace(x[0])] = strings.TrimSpace(x[1])
				}
			}
			if m := reAlg.FindStringSubmatch(entry); m != nil {
				p["alg"] = m[1]
			} else {
				p["alg"] = "hmac-md5-96"
			}
			return p
		}
	}
	return nil
}

// buildTCP 构造带 IPv6 伪首部校验和的 TCP 段，对应 poc6 build_tcp。
func buildTCP(src, dst []byte, sport, dport uint16, seq, ack uint32, flags byte, window uint16, payload, options []byte) []byte {
	doff := 5 + len(options)/4
	doffFlags := uint16(doff<<12) | uint16(flags)
	hdr := append(be16(sport), be16(dport)...)
	hdr = append(hdr, be32(seq)...)
	hdr = append(hdr, be32(ack)...)
	hdr = append(hdr, be16(doffFlags)...)
	hdr = append(hdr, be16(window)...)
	hdr = append(hdr, 0, 0, 0, 0) // checksum + urgent
	hdr = append(hdr, options...)
	full := append(hdr, payload...)
	chk := l4Checksum(src, dst, 6, full)
	binary.BigEndian.PutUint16(full[16:18], chk)
	return full
}

// tcpConn 保存一条已建立的 TCP-over-IMS-ESP 连接状态，用于复用（如发完 REGISTER 再发 MESSAGE）。
type tcpConn struct {
	src, dst     []byte
	sport, dport uint16
	spi          uint32
	ik           []byte
	mySeq        uint32
	peerAck      uint32
	segMax       int
}

// tcpImsRegister 三次握手 + 发送 SIP 数据（MSS 分段 + 重传）+ 读响应，返回 (响应, 连接状态)。
func (s *Session) tcpImsRegister(src, dst []byte, sport, dport uint16, spi uint32, ik, sipBytes []byte) (string, *tcpConn) {
	myISN := binary.BigEndian.Uint32(randBytes(4))
	tsval := binary.BigEndian.Uint32(randBytes(4))
	synOpts := concat([]byte{0x02, 0x04, 0x04, 0xc4}, []byte{0x04, 0x02},
		append([]byte{0x08, 0x0a}, append(be32(tsval), be32(0)...)...), []byte{0x01}, []byte{0x03, 0x03, 0x07})
	syn := buildTCP(src, dst, sport, dport, myISN, 0, tcpSYN, 24320, nil, synOpts)

	var peerISN uint32
	var peerMSS int
	gotSynAck := false
	for attempt := 0; attempt < 6; attempt++ {
		eff := time.Duration(2*pow15(attempt)*1000) * time.Millisecond
		s.imsEspSend2(src, dst, sport, dport, spi, ik, nil, 6, syn)
		deadline := time.Now().Add(eff)
		for time.Now().Before(deadline) {
			innh, l4, ok := s.imsEspRecv2Raw(ik, time.Until(deadline))
			if !ok || innh != 6 || len(l4) < 20 {
				continue
			}
			tseq := binary.BigEndian.Uint32(l4[4:8])
			doffFlags := binary.BigEndian.Uint16(l4[12:14])
			flags := byte(doffFlags & 0x3f)
			if flags&tcpSYN != 0 && flags&tcpACK != 0 {
				peerISN = tseq
				doff := int(doffFlags>>12) * 4
				peerMSS = parseMSS(l4[20:doff])
				gotSynAck = true
				break
			}
			if flags&tcpRST != 0 {
				return "", nil
			}
		}
		if gotSynAck {
			break
		}
	}
	if !gotSynAck {
		return "", nil
	}

	// ACK 完成握手
	ackPkt := buildTCP(src, dst, sport, dport, myISN+1, peerISN+1, tcpACK, 0x7fff, nil, nil)
	s.imsEspSend2(src, dst, sport, dport, spi, ik, nil, 6, ackPkt)

	segMax := (peerMSS) - 120
	if segMax < 536 {
		segMax = 536
	}
	if peerMSS == 0 {
		segMax = 1100
	}
	resp, conn := s.tcpSendData(src, dst, sport, dport, spi, ik, myISN+1, peerISN+1, sipBytes, segMax)
	return resp, conn
}

// tcpSendData 分段发送数据并读响应，返回连接状态供复用。
func (s *Session) tcpSendData(src, dst []byte, sport, dport uint16, spi uint32, ik []byte,
	mySeq, peerAck uint32, data []byte, segMax int) (string, *tcpConn) {
	var segPkts [][]byte
	off := 0
	for i := 0; i < len(data); i += segMax {
		end := i + segMax
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]
		flags := byte(tcpACK)
		if end >= len(data) {
			flags |= tcpPSH
		}
		segPkts = append(segPkts, buildTCP(src, dst, sport, dport, mySeq+uint32(off), peerAck, flags, 0x7fff, chunk, nil))
		off += len(chunk)
	}
	mySeqNext := mySeq + uint32(len(data))
	var collected []byte
	for attempt := 0; attempt < 6; attempt++ {
		eff := time.Duration(2*pow15(attempt)*1000) * time.Millisecond
		for _, sp := range segPkts {
			s.imsEspSend2(src, dst, sport, dport, spi, ik, nil, 6, sp)
		}
		deadline := time.Now().Add(eff)
		gotData := false
		for time.Now().Before(deadline) {
			innh, l4, ok := s.imsEspRecv2Raw(ik, time.Until(deadline))
			if !ok || innh != 6 || len(l4) < 20 {
				continue
			}
			tseq := binary.BigEndian.Uint32(l4[4:8])
			doffFlags := binary.BigEndian.Uint16(l4[12:14])
			doff := int(doffFlags>>12) * 4
			flags := byte(doffFlags & 0x3f)
			tcpPayload := l4[doff:]
			if len(tcpPayload) > 0 {
				collected = append(collected, tcpPayload...)
				gotData = true
				peerAck = tseq + uint32(len(tcpPayload))
				if strings.Contains(string(collected), "SIP/2.0") {
					ackp := buildTCP(src, dst, sport, dport, mySeqNext, peerAck, tcpACK, 0x7fff, nil, nil)
					s.imsEspSend2(src, dst, sport, dport, spi, ik, nil, 6, ackp)
					return string(collected), &tcpConn{src, dst, sport, dport, spi, ik, mySeqNext, peerAck, segMax}
				}
			}
			if flags&tcpFIN != 0 {
				conn := &tcpConn{src, dst, sport, dport, spi, ik, mySeqNext, peerAck, segMax}
				if len(collected) > 0 {
					return string(collected), conn
				}
				return "", conn
			}
		}
		if gotData {
			continue
		}
	}
	conn := &tcpConn{src, dst, sport, dport, spi, ik, mySeqNext, peerAck, segMax}
	if len(collected) > 0 {
		return string(collected), conn
	}
	return "", conn
}

// tcpSendOnConn 在已有连接上续发一条 SIP 请求（复用 seq/ack），对应 poc6 tcp_send_on_conn。
func (s *Session) tcpSendOnConn(c *tcpConn, sipBytes []byte) (string, *tcpConn) {
	resp, nc := s.tcpSendData(c.src, c.dst, c.sport, c.dport, c.spi, c.ik, c.mySeq, c.peerAck, sipBytes, c.segMax)
	return resp, nc
}

func parseMSS(opts []byte) int {
	oi := 0
	for oi+1 < len(opts) {
		k := opts[oi]
		if k == 0 {
			break
		}
		if k == 1 {
			oi++
			continue
		}
		ol := 2
		if oi+1 < len(opts) {
			ol = int(opts[oi+1])
		}
		if k == 2 && ol == 4 && oi+4 <= len(opts) {
			return int(binary.BigEndian.Uint16(opts[oi+2 : oi+4]))
		}
		if ol < 2 {
			ol = 2
		}
		oi += ol
	}
	return 0
}

// ---- MO 短信编码，与 delve 抓取的 VoHive 报文逐字节一致 ----

func bcdSwap(digits string) []byte {
	if len(digits)%2 == 1 {
		digits += "F"
	}
	out := make([]byte, 0, len(digits)/2)
	nib := func(c byte) byte {
		if c == 'F' {
			return 0xF
		}
		return c - '0'
	}
	for i := 0; i < len(digits); i += 2 {
		out = append(out, nib(digits[i+1])<<4|nib(digits[i]))
	}
	return out
}

func gsm7Pack(text string) ([]byte, int) {
	udl := len(text)
	var packed []byte
	var bitbuf uint32
	bits := 0
	for i := 0; i < len(text); i++ {
		bitbuf |= uint32(text[i]&0x7f) << bits
		bits += 7
		for bits >= 8 {
			packed = append(packed, byte(bitbuf&0xff))
			bitbuf >>= 8
			bits -= 8
		}
	}
	if bits > 0 {
		packed = append(packed, byte(bitbuf&0xff))
	}
	return packed, udl
}

func buildSMSSubmitTPDU(destE164, text string, mr byte) []byte {
	digits := strings.TrimPrefix(destE164, "+")
	da := append([]byte{byte(len(digits)), 0x91}, bcdSwap(digits)...)
	ud, udl := gsm7Pack(text)
	tpdu := append([]byte{0x01, mr}, da...)
	tpdu = append(tpdu, 0x00, 0x00, byte(udl))
	return append(tpdu, ud...)
}

func buildRPDataMO(smscE164 string, tpdu []byte, rpMR byte) []byte {
	digits := strings.TrimPrefix(smscE164, "+")
	bcd := bcdSwap(digits)
	rpDA := append([]byte{byte(1 + len(bcd)), 0x91}, bcd...)
	out := append([]byte{0x00, rpMR, 0x00}, rpDA...)
	out = append(out, byte(len(tpdu)))
	return append(out, tpdu...)
}

// SendSMS 假定 Register()（IKE 层）已成功，执行完整 SIP 注册 + 发一条 MO 短信。
// 返回最终 MESSAGE 的响应码（202/200 为成功）。
func (s *Session) SendSMS(to, text, smsc string) (int, error) {
	if s.AssignedIPv6 == nil || len(s.PCSCFv6) == 0 {
		return 0, fmt.Errorf("IKE 未就绪或无 P-CSCF")
	}
	if smsc == "" {
		smsc = "+447802002606"
	}
	id := s.newSIPIdentity()
	src := s.AssignedIPv6
	target := s.PCSCFv6[0]
	callid := hex.EncodeToString(randBytes(8)) + "@" + hex.EncodeToString(src)
	fromtag := hex.EncodeToString(randBytes(6))

	// ---- 第1条 REGISTER（UDP，触发 401）----
	branch1 := "z9hG4bK" + hex.EncodeToString(randBytes(8))
	msg1 := s.buildRegister(id, src, callid, fromtag, branch1, 1, 5060, "", "", "", "", "UDP")
	udp1 := buildUDPOverIPv6(src, target, 5060, 5060, msg1)
	resp1 := s.sendAndWaitSIP(udp1, 10, 4*time.Second)
	if resp1 == "" {
		return 0, fmt.Errorf("首条 REGISTER 无响应")
	}
	code1, hdrs1 := parseSIPResponse(resp1)
	if code1 != 401 {
		return 0, fmt.Errorf("期望 401，实际 %d", code1)
	}
	www := firstHeader(hdrs1, "WWW-Authenticate")
	secserver := firstHeader(hdrs1, "Security-Server")
	serviceRoute := strings.Join(hdrs1["Service-Route"], ",")
	nonceB64 := extractQuoted(www, "nonce")
	sec := parseSecurityServer(secserver)
	if sec == nil {
		return 0, fmt.Errorf("Security-Server 无 SA 参数")
	}

	// ---- IMS-AKA：对 401 nonce 再跑一次 USIM AKA ----
	nonceRaw, err := base64.StdEncoding.DecodeString(padB64(nonceB64))
	if err != nil || len(nonceRaw) < 32 {
		return 0, fmt.Errorf("nonce 解码失败")
	}
	imsRand := nonceRaw[:16]
	imsAutn := nonceRaw[16:32]
	aid := s.cfg.USIMAID
	if aid == "" {
		aid = "A0000000871002FF44FFFF8901010100"
	}
	aka, err := runUSIMAKA(s.cfg.ATPort, aid, imsRand, imsAutn)
	if err != nil {
		return 0, fmt.Errorf("IMS-AKA USIM 失败: %w", err)
	}
	// HTTP Digest AKAv1-MD5：password = RES 原始字节
	ha1 := md5hex(append([]byte(id.impi+":"+id.homeDomain+":"), aka.RES...))
	ha2 := md5hex([]byte("REGISTER:sip:" + id.homeDomain))
	digestResp := md5hex([]byte(ha1 + ":" + nonceB64 + ":" + ha2))
	imsIKPadded := append(append([]byte{}, aka.IK...), 0, 0, 0, 0) // sha1 用 20 字节密钥

	pcscfSpiS := atoiSafe(sec["spi-s"])
	pcscfPortS := uint16(atoiSafe(sec["port-s"]))

	// ---- 第2条 REGISTER（TCP，带认证）----
	secClient2 := fmt.Sprintf("ipsec-3gpp;alg=hmac-sha-1-96;ealg=%s;spi-c=%d;spi-s=%d;port-c=%d;port-s=%d",
		orDefault(sec["ealg"], "null"), id.ueSpiC, id.ueSpiS, id.uePortC, id.uePortS)
	branch2 := "z9hG4bK" + hex.EncodeToString(randBytes(8))
	msg2 := s.buildRegister(id, src, callid, fromtag, branch2, 2, id.uePortC, nonceB64, digestResp, secserver, serviceRoute, "TCP")
	msg2 = replaceOnce(msg2, id.secClient, secClient2)
	resp2, conn := s.tcpImsRegister(src, target, id.uePortC, pcscfPortS, uint32(pcscfSpiS), imsIKPadded, msg2)
	if resp2 == "" || conn == nil {
		return 0, fmt.Errorf("第2条 REGISTER 无响应")
	}
	code2, hdrs2 := parseSIPResponse(resp2)
	if code2 != 200 {
		return code2, fmt.Errorf("REGISTER 失败 code=%d", code2)
	}
	s.logf("IMS REGISTER 成功 200 OK")

	// ---- 取 MSISDN 身份（P-Associated-URI）----
	aor := id.impu
	for _, v := range hdrs2["P-Associated-URI"] {
		if m := regexp.MustCompile(`<sip:(\+[^>;]+)>`).FindStringSubmatch(v); m != nil {
			aor = "sip:" + m[1]
			break
		}
	}
	aorHost := id.homeDomain
	if i := strings.Index(aor, "@"); i >= 0 {
		aorHost = aor[i+1:]
	}

	// ---- 构造并发送 MO 短信 MESSAGE ----
	tpdu := buildSMSSubmitTPDU(to, text, 1)
	rpdu := buildRPDataMO(smsc, tpdu, randBytes(1)[0])
	smscUser := smsc
	srcStr := ipv6Bracket(src)
	var mb strings.Builder
	mb.WriteString(fmt.Sprintf("MESSAGE sip:%s@%s;user=phone;transport=tcp SIP/2.0\r\n", smscUser, aorHost))
	mb.WriteString(fmt.Sprintf("Via: SIP/2.0/TCP %s:%d;rport;branch=z9hG4bK%s\r\n", srcStr, id.uePortC, hex.EncodeToString(randBytes(8))))
	if serviceRoute != "" {
		mb.WriteString("Route: " + serviceRoute + "\r\n")
	}
	mb.WriteString(fmt.Sprintf("From: <%s>;tag=%s\r\n", aor, hex.EncodeToString(randBytes(4))))
	mb.WriteString(fmt.Sprintf("To: <sip:%s@%s;user=phone>\r\n", smscUser, aorHost))
	mb.WriteString(fmt.Sprintf("Call-ID: %s\r\n", hex.EncodeToString(randBytes(12))))
	mb.WriteString("CSeq: 3 MESSAGE\r\n")
	mb.WriteString("Max-Forwards: 70\r\n")
	mb.WriteString("Supported: path, 100rel, replaces, gruu, sec-agree\r\n")
	mb.WriteString(fmt.Sprintf("P-Access-Network-Info: IEEE-802.11; i-wlan-node-id=\"%s\";country=%s\r\n", id.wlanNodeID, id.paniCountry))
	mb.WriteString(fmt.Sprintf("P-Preferred-Identity: <%s>\r\n", aor))
	mb.WriteString("Security-Verify: " + secserver + "\r\n")
	mb.WriteString("User-Agent: " + id.userAgent + "\r\n")
	mb.WriteString("Accept-Contact: *;+g.3gpp.smsip;explicit;require\r\n")
	mb.WriteString("Request-Disposition: no-fork\r\n")
	mb.WriteString("Content-Type: application/vnd.3gpp.sms\r\n")
	mb.WriteString(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(rpdu)))
	msgSMS := append([]byte(mb.String()), rpdu...)

	respSMS, _ := s.tcpSendOnConn(conn, msgSMS)
	if respSMS == "" {
		return 0, fmt.Errorf("MESSAGE 无响应")
	}
	codeSMS, _ := parseSIPResponse(respSMS)
	s.logf("发短信结果 code=%d", codeSMS)
	if codeSMS != 200 && codeSMS != 202 {
		return codeSMS, fmt.Errorf("MESSAGE 被拒 code=%d", codeSMS)
	}
	return codeSMS, nil
}

// ---- 字符串辅助 ----

func firstHeader(h map[string][]string, k string) string {
	if v, ok := h[k]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

func extractQuoted(s, key string) string {
	re := regexp.MustCompile(key + `="([^"]+)"`)
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func padB64(s string) string {
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return s
}

func atoiSafe(s string) int { n, _ := strconv.Atoi(s); return n }

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func replaceOnce(b []byte, old, new string) []byte {
	return []byte(strings.Replace(string(b), old, new, 1))
}
