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
	"unicode/utf16"
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
	// Contact 里声明 SMS-over-IP 能力(+g.3gpp.smsip)，让网络/IP-SM-GW 把 MT 短信路由到 IMS；
	// 否则网络认为该用户不支持经 IMS 收短信，MT 会走蜂窝。
	sb.WriteString(fmt.Sprintf("Contact: <sip:%s@%s:%d>;+sip.instance=\"%s\";+g.3gpp.smsip\r\n", id.deviceUUID, srcStr, vport, id.sipInstance))
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
	// P-CSCF 通告的 MSS 只反映它到 UE 内层链路的 MTU，未计入我们这层
	// ESP-in-UDP + 英国代理隧道的封装开销；直接用会让出网包被 IP 分片、在高丢包链路上丢失。
	// 与 MT 方向压到 MSS=1000 同理，这里把 MO 段长也封顶到能整段通过的保守值。
	if segMax > 1000 {
		segMax = 1000
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
	s.logf("[tx] tcpSendData sport=%d dport=%d mySeq=%d peerAck=%d datalen=%d segs=%d", sport, dport, mySeq, peerAck, len(data), len(segPkts))
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
			tsp := binary.BigEndian.Uint16(l4[0:2])
			tdp := binary.BigEndian.Uint16(l4[2:4])
			tseq := binary.BigEndian.Uint32(l4[4:8])
			doffFlags := binary.BigEndian.Uint16(l4[12:14])
			doff := int(doffFlags>>12) * 4
			flags := byte(doffFlags & 0x3f)
			tcpPayload := l4[doff:]
			preview := ""
			if len(tcpPayload) > 0 {
				// 提取状态行 + CSeq，判断这条响应属于哪个请求（REGISTER/SUBSCRIBE/MESSAGE）
				full := string(tcpPayload)
				status0 := full
				if i := strings.Index(full, "\r\n"); i >= 0 {
					status0 = full[:i]
				}
				cseq := ""
				if i := strings.Index(full, "CSeq:"); i >= 0 {
					cs := full[i:]
					if j := strings.Index(cs, "\r\n"); j >= 0 {
						cseq = cs[:j]
					}
				}
				s.logf("[tx] 响应 status=%q %s", status0, cseq)
				n := len(tcpPayload)
				if n > 80 {
					n = 80
				}
				preview = strings.Map(func(r rune) rune {
					if r < 32 || r > 126 {
						return '.'
					}
					return r
				}, string(tcpPayload[:n]))
			}
			s.logf("[tx] attempt=%d 收到 sport=%d dport=%d flags=0x%02x seq=%d payload=%d 内容=%q", attempt, tsp, tdp, flags, tseq, len(tcpPayload), preview)
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

// gsm7Encodable 判断文本是否可用基础 GSM7（这里从简：全为 ASCII 即可）；否则走 UCS2。
func gsm7Encodable(s string) bool {
	for _, r := range s {
		if r > 0x7f {
			return false
		}
	}
	return true
}

// ucs2Units 把文本编成 UTF-16 码元序列（UCS2 短信即 UTF-16BE）。
func ucs2Units(s string) []uint16 { return utf16.Encode([]rune(s)) }

func ucs2Bytes(u []uint16) []byte {
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c>>8), byte(c))
	}
	return b
}

// buildSubmitTPDUs 把一条 MO 文本编码成一或多条 SMS-SUBMIT TPDU：
//   - 纯 ASCII 且 ≤160 → 单条 GSM7；
//   - 中文/其它 且 ≤70 → 单条 UCS2；
//   - 超长 → UCS2 多段（每段 67 码元 + 6 字节 UDH concat 头，TP-UDHI 置位，共享 ref）。
// 用 UCS2 处理所有多段场景，避免 GSM7+UDH 的 7bit 位对齐坑；ref/total/seq 写在 TPDU 里。
func buildSubmitTPDUs(destE164, text string) [][]byte {
	digits := strings.TrimPrefix(destE164, "+")
	da := append([]byte{byte(len(digits)), 0x91}, bcdSwap(digits)...)
	mk := func(firstOctet, dcs byte, udl int, ud []byte) []byte {
		t := append([]byte{firstOctet, randBytes(1)[0]}, da...) // TP-MTI=SUBMIT, TP-MR 随机
		t = append(t, 0x00, dcs, byte(udl))                     // TP-PID=00, TP-DCS, TP-UDL
		return append(t, ud...)
	}
	if gsm7Encodable(text) && len([]rune(text)) <= 160 {
		ud, udl := gsm7Pack(text)
		return [][]byte{mk(0x01, 0x00, udl, ud)}
	}
	units := ucs2Units(text)
	if len(units) <= 70 {
		ud := ucs2Bytes(units)
		return [][]byte{mk(0x01, 0x08, len(ud), ud)} // DCS=0x08 UCS2, UDL=字节数
	}
	// UCS2 多段
	var chunks [][]uint16
	for i := 0; i < len(units); {
		end := i + 67
		if end > len(units) {
			end = len(units)
		}
		// 不要把代理对(emoji)从中间切开
		if end < len(units) && units[end-1] >= 0xD800 && units[end-1] <= 0xDBFF {
			end--
		}
		chunks = append(chunks, units[i:end])
		i = end
	}
	ref := randBytes(1)[0]
	total := byte(len(chunks))
	out := make([][]byte, 0, len(chunks))
	for seq, ch := range chunks {
		udh := []byte{0x05, 0x00, 0x03, ref, total, byte(seq + 1)} // UDHL=05, IEI=00(8bit concat), len=03
		ud := append(udh, ucs2Bytes(ch)...)
		out = append(out, mk(0x41, 0x08, len(ud), ud)) // 0x41=SUBMIT|UDHI, UDL=UD 字节数(含UDH)
	}
	return out
}

func buildRPDataMO(smscE164 string, tpdu []byte, rpMR byte) []byte {
	digits := strings.TrimPrefix(smscE164, "+")
	bcd := bcdSwap(digits)
	rpDA := append([]byte{byte(1 + len(bcd)), 0x91}, bcd...)
	out := append([]byte{0x00, rpMR, 0x00}, rpDA...)
	out = append(out, byte(len(tpdu)))
	return append(out, tpdu...)
}

// regContext 保存一次 IMS 注册后的会话上下文，供 MO 发送 / MT 接收 / 重注册复用。
type regContext struct {
	id           *sipIdentity
	conn         *tcpConn // 认证 REGISTER 建立的常开 TCP 连接
	secserver    string
	serviceRoute string
	aor          string // 本机 MSISDN 身份，如 sip:+447902247547@o2.co.uk
	aorHost      string
	callid       string
	fromtag      string
	cseq         int // 已用到的 CSeq（REGISTER 用 1、2；后续请求从 3 递增）
	imsIK        []byte
	// MT 服务端方向：P-CSCF 主动连到 UE 的 port-us 推 MT 短信；
	// UE 的响应（SYN-ACK/200 OK）经 spi-c/port-c 发回 P-CSCF 的 client 端口。
	pcscfSpiC  uint32
	pcscfPortC uint16
	// 服务端方向：UE 的客户端连接(uePortC)发往 P-CSCF 的 spi-s/port-s。
	// 存下来供 MO 复用发送时重开客户端连接（不再重跑 REGISTER/AKA）。
	pcscfSpiS  uint32
	pcscfPortS uint16
	target     []byte // P-CSCF IPv6
}

// imsRegister 执行完整 SIP 注册（首条 UDP 触发 401 → 认证 TCP REGISTER 得 200），
// 返回常开连接与注册上下文，不发短信、不拆隧道。假定 Register()（IKE 层）已成功。
// subscribe=true 时注册后发 SUBSCRIBE(reg)（常驻会话用，让网络把 MT 走 IMS 推送）。
// refresh!=nil 时做“刷新式重注册”：复用同一身份(uePortC/SPI) + Call-ID + fromtag、续用 CSeq，
// 让网络当成对现有绑定的刷新而非新注册——否则新注册会与已有注册冲突，后续 SUBSCRIBE/MESSAGE
// 被 S-CSCF 回 500。MO 发送前用它重开被 FIN 掉的连接：refresh=当前 rc、subscribe=false（复用
// 已有订阅），拿到活连接后立刻发 MESSAGE。
func (s *Session) imsRegister(subscribe bool, refresh *regContext) (*regContext, error) {
	if s.AssignedIPv6 == nil || len(s.PCSCFv6) == 0 {
		return nil, fmt.Errorf("IKE 未就绪或无 P-CSCF")
	}
	src := s.AssignedIPv6
	target := s.PCSCFv6[0]
	var id *sipIdentity
	var callid, fromtag string
	baseCSeq := 0
	if refresh != nil {
		// 刷新：沿用现有身份/对话，CSeq 从上次续增
		id = refresh.id
		callid = refresh.callid
		fromtag = refresh.fromtag
		baseCSeq = refresh.cseq
	} else {
		id = s.newSIPIdentity()
		callid = hex.EncodeToString(randBytes(8)) + "@" + hex.EncodeToString(src)
		fromtag = hex.EncodeToString(randBytes(6))
	}

	// ---- 第1条 REGISTER（UDP，触发 401）----
	branch1 := "z9hG4bK" + hex.EncodeToString(randBytes(8))
	msg1 := s.buildRegister(id, src, callid, fromtag, branch1, baseCSeq+1, 5060, "", "", "", "", "UDP")
	udp1 := buildUDPOverIPv6(src, target, 5060, 5060, msg1)
	resp1 := s.sendAndWaitSIP(udp1, 10, 4*time.Second)
	if resp1 == "" {
		return nil, fmt.Errorf("首条 REGISTER 无响应")
	}
	code1, hdrs1 := parseSIPResponse(resp1)
	if code1 != 401 {
		return nil, fmt.Errorf("期望 401，实际 %d", code1)
	}
	www := firstHeader(hdrs1, "WWW-Authenticate")
	secserver := firstHeader(hdrs1, "Security-Server")
	serviceRoute := strings.Join(hdrs1["Service-Route"], ",")
	nonceB64 := extractQuoted(www, "nonce")
	sec := parseSecurityServer(secserver)
	if sec == nil {
		return nil, fmt.Errorf("Security-Server 无 SA 参数")
	}

	// ---- IMS-AKA：对 401 nonce 再跑一次 USIM AKA ----
	nonceRaw, err := base64.StdEncoding.DecodeString(padB64(nonceB64))
	if err != nil || len(nonceRaw) < 32 {
		return nil, fmt.Errorf("nonce 解码失败")
	}
	aid := s.cfg.USIMAID
	if aid == "" {
		aid = "A0000000871002FF44FFFF8901010100"
	}
	aka, err := s.usimAKA(aid, nonceRaw[:16], nonceRaw[16:32])
	if err != nil {
		return nil, fmt.Errorf("IMS-AKA USIM 失败: %w", err)
	}
	ha1 := md5hex(append([]byte(id.impi+":"+id.homeDomain+":"), aka.RES...))
	ha2 := md5hex([]byte("REGISTER:sip:" + id.homeDomain))
	digestResp := md5hex([]byte(ha1 + ":" + nonceB64 + ":" + ha2))
	imsIKPadded := append(append([]byte{}, aka.IK...), 0, 0, 0, 0)

	pcscfSpiS := atoiSafe(sec["spi-s"])
	pcscfPortS := uint16(atoiSafe(sec["port-s"]))

	// ---- 第2条 REGISTER（TCP，带认证）----
	secClient2 := fmt.Sprintf("ipsec-3gpp;alg=hmac-sha-1-96;ealg=%s;spi-c=%d;spi-s=%d;port-c=%d;port-s=%d",
		orDefault(sec["ealg"], "null"), id.ueSpiC, id.ueSpiS, id.uePortC, id.uePortS)
	branch2 := "z9hG4bK" + hex.EncodeToString(randBytes(8))
	msg2 := s.buildRegister(id, src, callid, fromtag, branch2, baseCSeq+2, id.uePortC, nonceB64, digestResp, secserver, serviceRoute, "TCP")
	msg2 = replaceOnce(msg2, id.secClient, secClient2)
	if refresh == nil {
		s.step(StepRegister, StepRunning, "")
	}
	resp2, conn := s.tcpImsRegister(src, target, id.uePortC, pcscfPortS, uint32(pcscfSpiS), imsIKPadded, msg2)
	if resp2 == "" || conn == nil {
		if refresh == nil {
			s.step(StepRegister, StepFail, "第2条 REGISTER 无响应")
		}
		return nil, fmt.Errorf("第2条 REGISTER 无响应")
	}
	code2, hdrs2 := parseSIPResponse(resp2)
	if code2 != 200 {
		if refresh == nil {
			s.step(StepRegister, StepFail, fmt.Sprintf("REGISTER code=%d", code2))
		}
		return nil, fmt.Errorf("REGISTER 失败 code=%d", code2)
	}
	s.logf("IMS REGISTER 成功 200 OK")
	if refresh == nil {
		s.step(StepRegister, StepOK, "200 OK")
	}

	aor := id.impu
	for _, v := range hdrs2["P-Associated-URI"] {
		if m := reAssocURI.FindStringSubmatch(v); m != nil {
			aor = "sip:" + m[1]
			break
		}
	}
	aorHost := id.homeDomain
	if i := strings.Index(aor, "@"); i >= 0 {
		aorHost = aor[i+1:]
	}
	// 从 aor 的用户名部分提取本机号码（MSISDN）：sip:+447902247547@o2.co.uk → +447902247547
	if user := strings.TrimPrefix(strings.SplitN(aor, "@", 2)[0], "sip:"); strings.HasPrefix(user, "+") {
		s.MSISDN = user
		s.logf("本机号码（MSISDN）= %s", user)
	}
	rc := &regContext{
		id: id, conn: conn, secserver: secserver, serviceRoute: serviceRoute,
		aor: aor, aorHost: aorHost, callid: callid, fromtag: fromtag, cseq: baseCSeq + 2, imsIK: imsIKPadded,
		pcscfSpiC: uint32(atoiSafe(sec["spi-c"])), pcscfPortC: uint16(atoiSafe(sec["port-c"])),
		pcscfSpiS: uint32(pcscfSpiS), pcscfPortS: pcscfPortS, target: target,
	}

	// 注册成功后订阅自己的注册事件（reg event package）——这是 VoHive 的做法，
	// 让网络/IP-SM-GW 认定该用户经 IMS 可达，从而把 MT 短信改走 IMS 推送到本连接。
	if subscribe {
		s.step(StepSubscribe, StepRunning, "")
		subResp, nc := s.tcpSendOnConn(rc.conn, s.buildSubscribeReg(rc))
		if nc != nil {
			rc.conn = nc // 回写推进后的 seq/ack，否则后续 MESSAGE 用旧序号会被 P-CSCF 忽略
		}
		if subResp != "" {
			if code, _ := parseSIPResponse(subResp); code == 200 {
				s.logf("SUBSCRIBE(reg) 成功 200 OK")
				s.step(StepSubscribe, StepOK, "200 OK")
			} else {
				s.logf("SUBSCRIBE(reg) 返回 code=%d", code)
				s.step(StepSubscribe, StepFail, fmt.Sprintf("code=%d", code))
			}
		} else {
			s.logf("SUBSCRIBE(reg) 无响应")
			s.step(StepSubscribe, StepFail, "无响应")
		}
	}
	return rc, nil
}

// buildSubscribeReg 构造一条 SUBSCRIBE（Event: reg，订阅自身注册状态），照抄 VoHive 报文格式。
func (s *Session) buildSubscribeReg(rc *regContext) []byte {
	rc.cseq++
	srcStr := ipv6Bracket(s.AssignedIPv6)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("SUBSCRIBE %s SIP/2.0\r\n", rc.aor))
	sb.WriteString(fmt.Sprintf("Via: SIP/2.0/TCP %s:%d;rport;branch=z9hG4bK%s\r\n", srcStr, rc.id.uePortC, hex.EncodeToString(randBytes(8))))
	if rc.serviceRoute != "" {
		sb.WriteString("Route: " + rc.serviceRoute + "\r\n")
	}
	sb.WriteString(fmt.Sprintf("From: <%s>;tag=%s\r\n", rc.aor, hex.EncodeToString(randBytes(4))))
	sb.WriteString(fmt.Sprintf("To: <%s>\r\n", rc.aor))
	sb.WriteString(fmt.Sprintf("Call-ID: %s\r\n", hex.EncodeToString(randBytes(10))))
	sb.WriteString(fmt.Sprintf("CSeq: %d SUBSCRIBE\r\n", rc.cseq))
	sb.WriteString("Max-Forwards: 70\r\n")
	sb.WriteString(fmt.Sprintf("Contact: <sip:%s@%s:%d>;+sip.instance=\"%s\"\r\n", rc.id.deviceUUID, srcStr, rc.id.uePortC, rc.id.sipInstance))
	sb.WriteString("Require: sec-agree\r\n")
	sb.WriteString("Proxy-Require: sec-agree\r\n")
	sb.WriteString(fmt.Sprintf("P-Access-Network-Info: IEEE-802.11; i-wlan-node-id=\"%s\";country=%s\r\n", rc.id.wlanNodeID, rc.id.paniCountry))
	sb.WriteString(fmt.Sprintf("P-Preferred-Identity: <%s>\r\n", rc.aor))
	sb.WriteString("Security-Verify: " + rc.secserver + "\r\n")
	sb.WriteString("User-Agent: " + rc.id.userAgent + "\r\n")
	sb.WriteString("Expires: 600000\r\n")
	sb.WriteString("Event: reg\r\n")
	sb.WriteString("Accept: application/reginfo+xml\r\n")
	sb.WriteString("Content-Length: 0\r\n\r\n")
	return []byte(sb.String())
}

var reAssocURI = regexp.MustCompile(`<sip:(\+[^>;]+)>`)

// buildMOMessages 把一条（可能很长/含中文的）MO 短信编码成一条或多条 MESSAGE：
// 纯 ASCII 且 ≤160 字符 → 单条 GSM7；否则(中文/超长) → UCS2，>70 字符再按 concat(UDH) 拆分。
// 每段各自成一条 SIP MESSAGE（各自 CSeq 递增），由调用方在同一连接上依次发出。
func (s *Session) buildMOMessages(rc *regContext, to, text, smsc string, srcPort uint16) [][]byte {
	tpdus := buildSubmitTPDUs(to, text)
	out := make([][]byte, 0, len(tpdus))
	for _, tpdu := range tpdus {
		rpdu := buildRPDataMO(smsc, tpdu, randBytes(1)[0])
		out = append(out, s.wrapMOMessage(rc, smsc, rpdu, srcPort))
	}
	return out
}

// buildMOMessage 单条便捷封装（一次性 SendSMS 用；长文本只取第一段）。
func (s *Session) buildMOMessage(rc *regContext, to, text, smsc string, srcPort uint16) []byte {
	return s.buildMOMessages(rc, to, text, smsc, srcPort)[0]
}

// wrapMOMessage 把给定 RP-DATA 包成一条 MO SIP MESSAGE（CSeq 递增）。
// srcPort 是本条 MESSAGE 所用 TCP 连接的源端口，必须与实际发送端口一致（Via rport）。
func (s *Session) wrapMOMessage(rc *regContext, smsc string, rpdu []byte, srcPort uint16) []byte {
	rc.cseq++
	srcStr := ipv6Bracket(s.AssignedIPv6)
	var mb strings.Builder
	mb.WriteString(fmt.Sprintf("MESSAGE sip:%s@%s;user=phone;transport=tcp SIP/2.0\r\n", smsc, rc.aorHost))
	mb.WriteString(fmt.Sprintf("Via: SIP/2.0/TCP %s:%d;rport;branch=z9hG4bK%s\r\n", srcStr, srcPort, hex.EncodeToString(randBytes(8))))
	if rc.serviceRoute != "" {
		mb.WriteString("Route: " + rc.serviceRoute + "\r\n")
	}
	mb.WriteString(fmt.Sprintf("From: <%s>;tag=%s\r\n", rc.aor, hex.EncodeToString(randBytes(4))))
	mb.WriteString(fmt.Sprintf("To: <sip:%s@%s;user=phone>\r\n", smsc, rc.aorHost))
	mb.WriteString(fmt.Sprintf("Call-ID: %s\r\n", hex.EncodeToString(randBytes(12))))
	mb.WriteString(fmt.Sprintf("CSeq: %d MESSAGE\r\n", rc.cseq))
	mb.WriteString("Max-Forwards: 70\r\n")
	mb.WriteString("Supported: path, 100rel, replaces, gruu, sec-agree\r\n")
	mb.WriteString(fmt.Sprintf("P-Access-Network-Info: IEEE-802.11; i-wlan-node-id=\"%s\";country=%s\r\n", rc.id.wlanNodeID, rc.id.paniCountry))
	mb.WriteString(fmt.Sprintf("P-Preferred-Identity: <%s>\r\n", rc.aor))
	mb.WriteString("Security-Verify: " + rc.secserver + "\r\n")
	mb.WriteString("User-Agent: " + rc.id.userAgent + "\r\n")
	mb.WriteString("Accept-Contact: *;+g.3gpp.smsip;explicit;require\r\n")
	mb.WriteString("Request-Disposition: no-fork\r\n")
	mb.WriteString("Content-Type: application/vnd.3gpp.sms\r\n")
	mb.WriteString(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(rpdu)))
	return append([]byte(mb.String()), rpdu...)
}

// buildMTRPAck 构造对已收 MT 短信的 RP-ACK：一条 UE→网络的 MESSAGE，
// body 为 RP-ACK RPDU（MTI=0x02 ms→n，携带收到时的 RP-MR）。origURI 是收到的
// MESSAGE 的 From（发端 SMSC/服务中心），srcPort 为回发所用 TCP 源端口(uePortS)。
// 缺了这条，SMSC 认为未送达会无限重发同一条 MT 并卡住后续新短信。
func (s *Session) buildMTRPAck(rc *regContext, origURI string, rpMR byte, srcPort uint16) []byte {
	rpdu := []byte{0x02, rpMR}
	rc.cseq++
	srcStr := ipv6Bracket(s.AssignedIPv6)
	var mb strings.Builder
	mb.WriteString(fmt.Sprintf("MESSAGE %s SIP/2.0\r\n", origURI))
	mb.WriteString(fmt.Sprintf("Via: SIP/2.0/TCP %s:%d;rport;branch=z9hG4bK%s\r\n", srcStr, srcPort, hex.EncodeToString(randBytes(8))))
	if rc.serviceRoute != "" {
		mb.WriteString("Route: " + rc.serviceRoute + "\r\n")
	}
	mb.WriteString(fmt.Sprintf("From: <%s>;tag=%s\r\n", rc.aor, hex.EncodeToString(randBytes(4))))
	mb.WriteString(fmt.Sprintf("To: <%s>\r\n", origURI))
	mb.WriteString(fmt.Sprintf("Call-ID: %s\r\n", hex.EncodeToString(randBytes(12))))
	mb.WriteString(fmt.Sprintf("CSeq: %d MESSAGE\r\n", rc.cseq))
	mb.WriteString("Max-Forwards: 70\r\n")
	mb.WriteString(fmt.Sprintf("P-Preferred-Identity: <%s>\r\n", rc.aor))
	mb.WriteString("Security-Verify: " + rc.secserver + "\r\n")
	mb.WriteString("User-Agent: " + rc.id.userAgent + "\r\n")
	mb.WriteString("Content-Type: application/vnd.3gpp.sms\r\n")
	mb.WriteString(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(rpdu)))
	return append([]byte(mb.String()), rpdu...)
}

// SendSMS 一次性：IKE 之上做 SIP 注册 + 发一条 MO 短信。调用方随后 Close 拆隧道。
// 返回 MESSAGE 的响应码（202/200 为成功）。常驻模式请改用 imsRegister + RunReceiver。
func (s *Session) SendSMS(to, text, smsc string) (int, error) {
	if smsc == "" {
		smsc = "+447802002606"
	}
	rc, err := s.imsRegister(true, nil)
	if err != nil {
		return 0, err
	}
	respSMS, _ := s.tcpSendOnConn(rc.conn, s.buildMOMessage(rc, to, text, smsc, rc.id.uePortC))
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
