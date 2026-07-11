package vowifi

// 常驻接收：保持 IMS 注册常开，监听 P-CSCF 主动推来的 MT 短信（SIP MESSAGE，
// body 为 RP-DATA(DELIVER)），解码后回调上层入库，并回 200 OK。附带 NAT-T keepalive
// 与到期前重新 REGISTER。
//
// 注意：MT 走服务端方向——P-CSCF 从 port-c 主动连到 UE 的 port-us 推消息，UE 在此做
// 服务端 TCP（收 SYN 回 SYN-ACK，收数据回 200 OK）。MO 一次性发送已实测到 202；MT
// 这条服务端路径按 3GPP/VoHive 日志的模型实现，需真实来信验证。

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// InboundSMS 是解码后的一条 MT 短信（可能是长短信的一段）。
type InboundSMS struct {
	Sender string
	Text   string
	Raw    []byte // 原始 RPDU，便于排错
	// 长短信 concat 信息（来自 UDH）：Total<=1 表示单段；否则上层按 (Sender,Ref) 缓冲拼接。
	Ref   int
	Total int
	Seq   int
}

// parseUDHConcat 从用户数据的 UDH 里取出 concat 信息 (ref,total,seq)；无 concat 头则返回 (0,1,1)。
func parseUDHConcat(ud []byte) (ref, total, seq int) {
	if len(ud) == 0 {
		return 0, 1, 1
	}
	udhl := int(ud[0])
	if udhl == 0 || 1+udhl > len(ud) {
		return 0, 1, 1
	}
	h := ud[1 : 1+udhl]
	for i := 0; i+1 < len(h); {
		iei := h[i]
		iedl := int(h[i+1])
		if i+2+iedl > len(h) {
			break
		}
		d := h[i+2 : i+2+iedl]
		switch iei {
		case 0x00: // 8-bit ref concat
			if len(d) >= 3 {
				return int(d[0]), int(d[1]), int(d[2])
			}
		case 0x08: // 16-bit ref concat
			if len(d) >= 4 {
				return int(d[0])<<8 | int(d[1]), int(d[2]), int(d[3])
			}
		}
		i += 2 + iedl
	}
	return 0, 1, 1
}

// RunReceiver 常驻运行：keepalive + 到期前重注册 + 监听处理 MT 短信 + 处理 MO 发送请求。
// 所有对 socket 的读写都在本 goroutine 内串行完成，避免并发。stop 关闭后返回。
func (s *Session) RunReceiver(rc *regContext, stop <-chan struct{}, sendCh <-chan sendReq, onSMS func(InboundSMS)) {
	lastKeepalive := time.Now()
	lastRegister := time.Now()
	s.touch() // 会话刚建立，标记存活
	heartbeatInterval := 8 * time.Minute
	if v := os.Getenv("VOWIFI_HEARTBEAT_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			heartbeatInterval = time.Duration(n) * time.Minute
		}
	}
	// 服务端半连接状态：每条 MT 连接（按 P-CSCF 源端口区分）独立保存序号、已发字节、重组缓冲
	type srvConn struct {
		myISN, peerNext, mySent uint32
		asm                     []byte
		msgAt                   time.Time // 当前消息第一段到达的时刻，量"重组耗时"（不含网络迟迟不推的空等）
	}
	srv := map[uint16]*srvConn{}

	for {
		select {
		case <-stop:
			return
		case req := <-sendCh:
			// 源端口须用 sec-agree 协商的保护端口 uePortC——IPsec SA 只保护这对端口，随机端口
			// 的 SYN 不受保护、P-CSCF 不理（无 SYN-ACK）。O2 不接受在新连接上不经 REGISTER 直发
			// MESSAGE，故每条都要一条经 REGISTER 顶活的连接。
			//
			// 批量摊薄：贵的是 REGISTER+USIM AKA，不是 MESSAGE。这里把当前请求 + 队列里已堆积的
			// 请求一次性取出，只做一次刷新式重注册，然后在同一条活连接上连发多条 MESSAGE，趁
			// P-CSCF FIN 之前打完——把"每条一次 AKA"降为"每批一次 AKA"，避免连发耗尽 USIM 通道/
			// 推失步 SQN。某条发送无响应（连接已被 FIN）时，再重注册一次续发剩余的。
			batch := []sendReq{req}
		drain:
			for len(batch) < 20 {
				select {
				case r := <-sendCh:
					batch = append(batch, r)
				default:
					break drain
				}
			}
			nrc, rerr := s.imsRegister(false, rc)
			if rerr != nil {
				for _, b := range batch {
					b.resp <- sendResp{err: fmt.Errorf("发前重注册失败: %w", rerr)}
				}
				continue
			}
			*rc = *nrc
			srv = map[uint16]*srvConn{} // SA 端口/连接已更新，清掉旧服务端半连接状态
			sentOnConn := 0
			for i := range batch {
				b := batch[i]
				// 一条短信可能被编码成多段（长文本/中文 UCS2 concat），逐段在同一连接上发出。
				tpdus := buildSubmitTPDUs(b.to, b.text)
				reqCode := 0
				var reqErr error
				for pi := 0; pi < len(tpdus); pi++ {
					msg := s.wrapMOMessage(rc, b.smsc, buildRPDataMO(b.smsc, tpdus[pi], randBytes(1)[0]), rc.id.uePortC)
					resp, nc := s.tcpSendOnConn(rc.conn, msg)
					if nc != nil {
						rc.conn = nc
					}
					if resp == "" && sentOnConn > 0 {
						// 连接被 P-CSCF FIN（本连接已发 sentOnConn 段），重注册续发这一段
						s.logf("[tx] 分段发送无响应（本连接已发%d段），重注册续发", sentOnConn)
						if nrc2, rerr2 := s.imsRegister(false, rc); rerr2 == nil {
							*rc = *nrc2
							srv = map[uint16]*srvConn{}
							sentOnConn = 0
							msg = s.wrapMOMessage(rc, b.smsc, buildRPDataMO(b.smsc, tpdus[pi], randBytes(1)[0]), rc.id.uePortC)
							resp, nc = s.tcpSendOnConn(rc.conn, msg)
							if nc != nil {
								rc.conn = nc
							}
						} else {
							reqErr = fmt.Errorf("发前重注册失败: %w", rerr2)
							break
						}
					}
					if resp == "" {
						reqErr = fmt.Errorf("MESSAGE 无响应")
						break
					}
					code, _ := parseSIPResponse(resp)
					reqCode = code
					if code != 200 && code != 202 {
						reqErr = fmt.Errorf("MESSAGE 被拒 code=%d", code)
						break
					}
					sentOnConn++
					s.touch() // MO 拿到 202：会话活着
				}
				if len(tpdus) > 1 && reqErr == nil {
					s.logf("[tx] 长短信分 %d 段全部发出", len(tpdus))
				}
				b.resp <- sendResp{code: reqCode, err: reqErr}
			}
			if len(batch) > 1 {
				s.logf("[tx] 批量发送完成：本批 %d 条", len(batch))
			}
			continue
		default:
		}
		// 定时 NAT-T keepalive（每 20s 发一个 0xFF 到 4500）
		if time.Since(lastKeepalive) > 20*time.Second {
			s.conn.WriteToUDP([]byte{0xff}, s.epdg)
			lastKeepalive = time.Now()
		}
		// 心跳式刷新注册：既保活注册，又作为存活探测供看门狗判断会话是否已死。
		// 用 refresh(subscribe=false) 而非整套重注册——一次 REGISTER 往返即证明 IKE 隧道 +
		// IMS 注册整条路都活着；成功则 touch()。间隔默认 8 分钟（VOWIFI_HEARTBEAT_MINUTES 可调）。
		if time.Since(lastRegister) > heartbeatInterval {
			if nrc, err := s.imsRegister(false, rc); err == nil {
				*rc = *nrc
				lastRegister = time.Now()
				srv = map[uint16]*srvConn{}
				s.touch()
				s.logf("VoWiFi 心跳重注册成功")
			} else {
				s.logf("VoWiFi 心跳重注册失败: %v", err) // 不 touch：连续失败会被看门狗判死重建
			}
		}

		// 读一个内层 IMS-ESP 包（短超时，便于周期性做上面的 keepalive/心跳）
		innh, l4, ok := s.imsEspRecv2Raw(rc.imsIK, 2*time.Second)
		if !ok {
			continue
		}
		s.touch() // 收到任意内层包：隧道活着
		if innh != 6 || len(l4) < 20 {
			s.logf("[recv] 收到内层包 nh=%d len=%d（非TCP，忽略）", innh, len(l4))
			continue
		}
		tsport := binary.BigEndian.Uint16(l4[0:2])
		tdport := binary.BigEndian.Uint16(l4[2:4])
		tseq := binary.BigEndian.Uint32(l4[4:8])
		doffFlags := binary.BigEndian.Uint16(l4[12:14])
		doff := int(doffFlags>>12) * 4
		flags := byte(doffFlags & 0x3f)
		payload := l4[doff:]
		s.logf("[recv] 入向TCP sport=%d dport=%d(uePortS=%d) flags=0x%02x seq=%d payload=%d",
			tsport, tdport, rc.id.uePortS, flags, tseq, len(payload))

		// 发往客户端连接(rc.conn)的入向包（如 SUBSCRIBE 后 P-CSCF 推来的 NOTIFY）：
		// 必须 ACK 保活并对请求回 200 OK，否则 P-CSCF 收不到 ACK 会重传、几次后判死连接，
		// 导致随后的 MO MESSAGE 零响应（连 TCP ACK 都收不到）。
		if rc.conn != nil && tdport == rc.conn.sport && tsport == rc.conn.dport {
			s.logf("[recv] 客户端连接入向 flags=0x%02x seq=%d(期望%d) payload=%d", flags, tseq, rc.conn.peerAck, len(payload))
			if len(payload) > 0 {
				if tseq == rc.conn.peerAck {
					rc.conn.peerAck = tseq + uint32(len(payload))
					if ok200 := build200OK(string(payload)); ok200 != nil {
						seg := buildTCP(rc.conn.src, rc.conn.dst, rc.conn.sport, rc.conn.dport, rc.conn.mySeq, rc.conn.peerAck, tcpPSH|tcpACK, 0x7fff, ok200, nil)
						s.imsEspSend2(rc.conn.src, rc.conn.dst, rc.conn.sport, rc.conn.dport, rc.conn.spi, rc.conn.ik, nil, 6, seg)
						rc.conn.mySeq += uint32(len(ok200))
					}
				}
				// 无论按序与否，都回当前累计 ACK 保活
				ackp := buildTCP(rc.conn.src, rc.conn.dst, rc.conn.sport, rc.conn.dport, rc.conn.mySeq, rc.conn.peerAck, tcpACK, 0x7fff, nil, nil)
				s.imsEspSend2(rc.conn.src, rc.conn.dst, rc.conn.sport, rc.conn.dport, rc.conn.spi, rc.conn.ik, nil, 6, ackp)
			}
			// P-CSCF 事务后会 FIN 关闭常开连接：回 FIN+ACK 干净收尾，
			// 释放该保护端口(uePortC)供随后的 MO MESSAGE 重开新连接。
			if flags&tcpFIN != 0 {
				rc.conn.peerAck = tseq + 1
				finack := buildTCP(rc.conn.src, rc.conn.dst, rc.conn.sport, rc.conn.dport, rc.conn.mySeq, rc.conn.peerAck, tcpFIN|tcpACK, 0x7fff, nil, nil)
				s.imsEspSend2(rc.conn.src, rc.conn.dst, rc.conn.sport, rc.conn.dport, rc.conn.spi, rc.conn.ik, nil, 6, finack)
			}
			continue
		}

		// 只处理发往我们服务端口(port-us)的 MT 连接
		if tdport != rc.id.uePortS {
			continue
		}
		sc := srv[tsport]

		if flags&tcpSYN != 0 && flags&tcpACK == 0 {
			// 收到 P-CSCF 的 SYN → 回 SYN-ACK，建立服务端连接
			myISN := binary.BigEndian.Uint32(randBytes(4))
			sc = &srvConn{myISN: myISN, peerNext: tseq + 1}
			srv[tsport] = sc
			// 通告一个更小的 MSS（500）：MT 消息经双层 ESP+UDP+代理链路，MTU 余量很小，
			// 较大的段会被分片、在这条高丢包链路上丢失后只能等 P-CSCF RTO 重传（数秒）。
			// 更小的段更容易整段通过，减少"丢段→等重传"的停顿，是让 MT 更及时的主要手段。
			synack := buildTCP(s.AssignedIPv6, rc.target, rc.id.uePortS, tsport, myISN, sc.peerNext, tcpSYN|tcpACK, 0x7fff, nil,
				[]byte{0x02, 0x04, 0x01, 0xf4}) // MSS=500
			s.imsEspSend2(s.AssignedIPv6, rc.target, rc.id.uePortS, tsport, rc.pcscfSpiC, rc.imsIK, nil, 6, synack)
			continue
		}
		if sc == nil {
			continue // 未建立的连接，忽略
		}
		if len(payload) > 0 {
			// 按序接收：只接受 seq==期望序号 的段；乱序段不收，靠下面的累积 ACK 触发对方重传。
			// 这条链路丢包率高，第一段常丢，必须正确处理否则重组缓冲缺头、解不出消息。
			if tseq == sc.peerNext {
				if len(sc.asm) == 0 {
					sc.msgAt = time.Now() // 本条消息第一段到达
				}
				sc.peerNext += uint32(len(payload))
				sc.asm = append(sc.asm, payload...)
				for {
					msg, rest, done := extractSIPMessage(sc.asm)
					if !done {
						break
					}
					sc.asm = rest
					if !sc.msgAt.IsZero() {
						s.logf("[recv] MT 消息重组耗时 %v（第一段到达→解码完成，不含网络推送前的空等）",
							time.Since(sc.msgAt).Round(time.Millisecond))
						sc.msgAt = time.Time{}
					}
					sent := s.handleInboundSIP(rc, tsport, sc.myISN+1+sc.mySent, sc.peerNext, msg, onSMS)
					sc.mySent += sent
				}
			} else {
				s.logf("[recv] 乱序段 seq=%d 期望=%d，发重复ACK请求重传", tseq, sc.peerNext)
			}
			// 累积 ACK 到当前按序收到的位置（乱序时即重复 ACK，请求对方重传缺失段）
			ackp := buildTCP(s.AssignedIPv6, rc.target, rc.id.uePortS, tsport, sc.myISN+1+sc.mySent, sc.peerNext, tcpACK, 0x7fff, nil, nil)
			s.imsEspSend2(s.AssignedIPv6, rc.target, rc.id.uePortS, tsport, rc.pcscfSpiC, rc.imsIK, nil, 6, ackp)
		}
		if flags&tcpFIN != 0 {
			delete(srv, tsport)
		}
	}
}

// handleInboundSIP 处理一条从 P-CSCF 推来的 SIP 请求（NOTIFY/MESSAGE 等）：
// 一律回 200 OK（否则 P-CSCF 会 RST）；若是携带 vnd.3gpp.sms 的 MESSAGE，解码 MT 短信并回调。
// 返回本次为响应而发出的 TCP 载荷字节数（供调用方推进我方序号）。
func (s *Session) handleInboundSIP(rc *regContext, peerPort uint16, mySeq, peerAck uint32, msg []byte, onSMS func(InboundSMS)) uint32 {
	text := string(msg)
	line0 := text
	if i := strings.Index(text, "\r\n"); i >= 0 {
		line0 = text[:i]
	}
	hdrEnd := strings.Index(text, "\r\n\r\n")
	headers := text
	var body []byte
	if hdrEnd >= 0 {
		headers = text[:hdrEnd]
		body = msg[hdrEnd+4:]
	}
	isSMS := strings.HasPrefix(line0, "MESSAGE ") && strings.Contains(headers, "application/vnd.3gpp.sms")
	s.logf("[recv] 入向SIP %q sms=%v bodylen=%d", line0, isSMS, len(body))

	// 对任何请求都回 200 OK
	var sent uint32
	if ok200 := build200OK(text); ok200 != nil {
		seg := buildTCP(s.AssignedIPv6, rc.target, rc.id.uePortS, peerPort, mySeq, peerAck, tcpPSH|tcpACK, 0x7fff, ok200, nil)
		s.imsEspSend2(s.AssignedIPv6, rc.target, rc.id.uePortS, peerPort, rc.pcscfSpiC, rc.imsIK, nil, 6, seg)
		sent = uint32(len(ok200))
	}

	if isSMS && len(body) > 0 {
		n := len(body)
		if n > 60 {
			n = 60
		}
		s.logf("[recv] MT RPDU 头=%x", body[:n])
		if sms, ok := decodeMTDeliver(body); ok {
			s.logf("收到 MT 短信 sender=%q text=%q", sms.Sender, sms.Text)
			if onSMS != nil {
				onSMS(sms)
			}
			// 关键：回 RP-ACK（短信层确认）。只回 SIP 200 OK 不够——SMSC 收不到 RP-ACK 会
			// 认为未送达，无限重发同一条旧短信并卡住后续新短信。RP-ACK 用收到的 RP-MR(body[1])，
			// 目标取收到 MESSAGE 的 From（发端 SMSC/服务中心），在同一条服务端连接上回发。
			rpMR := body[1]
			origURI := extractURI(firstHeaderLine(headers, "from:"))
			if origURI != "" {
				ack := s.buildMTRPAck(rc, origURI, rpMR, rc.id.uePortS)
				seg := buildTCP(s.AssignedIPv6, rc.target, rc.id.uePortS, peerPort, mySeq+sent, peerAck, tcpPSH|tcpACK, 0x7fff, ack, nil)
				s.imsEspSend2(s.AssignedIPv6, rc.target, rc.id.uePortS, peerPort, rc.pcscfSpiC, rc.imsIK, nil, 6, seg)
				sent += uint32(len(ack))
				s.logf("[recv] 已回 RP-ACK (RP-MR=%d → %s)", rpMR, origURI)
			} else {
				s.logf("[recv] 无法确定 RP-ACK 目标(From 缺失)，跳过")
			}
		} else {
			s.logf("[recv] MT DELIVER 解码失败")
		}
	}
	return sent
}

// firstHeaderLine 返回 headers 中第一条以 prefix(小写) 打头的整行原文（保留大小写）。
func firstHeaderLine(headers, prefixLower string) string {
	for _, ln := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(strings.ToLower(ln), prefixLower) {
			return ln
		}
	}
	return ""
}

// extractURI 从一行 SIP 头里取出尖括号内的 URI（如 From: <sip:...> → sip:...）；
// 无尖括号时退化为取 sip:/tel: 起始到分号/空白前的一段。
func extractURI(line string) string {
	if l := strings.IndexByte(line, '<'); l >= 0 {
		if r := strings.IndexByte(line[l+1:], '>'); r >= 0 {
			return line[l+1 : l+1+r]
		}
	}
	for _, scheme := range []string{"sip:", "sips:", "tel:"} {
		if i := strings.Index(line, scheme); i >= 0 {
			rest := line[i:]
			if j := strings.IndexAny(rest, "; \t"); j >= 0 {
				return rest[:j]
			}
			return rest
		}
	}
	return ""
}

// extractSIPMessage 从 TCP 重组缓冲里切出一条完整 SIP 消息（按 Content-Length）。
// 返回 (消息, 剩余, 是否切出完整消息)。
func extractSIPMessage(buf []byte) ([]byte, []byte, bool) {
	s := string(buf)
	he := strings.Index(s, "\r\n\r\n")
	if he < 0 {
		return nil, buf, false
	}
	cl := 0
	for _, ln := range strings.Split(s[:he], "\r\n") {
		if strings.HasPrefix(strings.ToLower(ln), "content-length:") {
			cl = atoiSafe(strings.TrimSpace(ln[len("content-length:"):]))
			break
		}
	}
	total := he + 4 + cl
	if len(buf) < total {
		return nil, buf, false
	}
	return buf[:total], buf[total:], true
}

// build200OK 根据入向请求构造一个 200 OK（回显必要头 + Content-Length: 0）。
func build200OK(req string) []byte {
	var via, from, to, callid, cseq string
	for _, ln := range strings.Split(req, "\r\n") {
		l := strings.ToLower(ln)
		switch {
		case strings.HasPrefix(l, "via:"):
			via = ln
		case strings.HasPrefix(l, "from:"):
			from = ln
		case strings.HasPrefix(l, "to:"):
			to = ln
		case strings.HasPrefix(l, "call-id:"):
			callid = ln
		case strings.HasPrefix(l, "cseq:"):
			cseq = ln
		}
	}
	if via == "" || callid == "" || cseq == "" {
		return nil
	}
	return []byte("SIP/2.0 200 OK\r\n" + via + "\r\n" + from + "\r\n" + to + "\r\n" +
		callid + "\r\n" + cseq + "\r\n" + "Content-Length: 0\r\n\r\n")
}

// ---- MT DELIVER 解码（3GPP TS 23.040）----

// decodeMTDeliver 解析 MT 方向的 RP-DATA(网络→MS) → SMS-DELIVER TPDU，取发送方与正文。
func decodeMTDeliver(rpdu []byte) (InboundSMS, bool) {
	// RP-DATA(n→ms): MTI(1) + RP-MR(1) + RP-OA(len,addr) + RP-DA(len=0) + RP-UD(len + TPDU)
	i := 0
	if len(rpdu) < 3 {
		return InboundSMS{}, false
	}
	i++ // RP-MTI
	i++ // RP-MR
	// RP-OA
	if i >= len(rpdu) {
		return InboundSMS{}, false
	}
	oaLen := int(rpdu[i])
	i += 1 + oaLen
	// RP-DA
	if i >= len(rpdu) {
		return InboundSMS{}, false
	}
	daLen := int(rpdu[i])
	i += 1 + daLen
	// RP-UD
	if i >= len(rpdu) {
		return InboundSMS{}, false
	}
	udLen := int(rpdu[i])
	i++
	if i+udLen > len(rpdu) {
		udLen = len(rpdu) - i
	}
	tpdu := rpdu[i : i+udLen]
	return decodeDeliverTPDU(tpdu, rpdu)
}

func decodeDeliverTPDU(t, raw []byte) (InboundSMS, bool) {
	if len(t) < 1 {
		return InboundSMS{}, false
	}
	p := 0
	first := t[p]
	p++
	udhi := first&0x40 != 0
	// TP-OA: 地址长度(半字节个数) + TOA + BCD
	if p >= len(t) {
		return InboundSMS{}, false
	}
	oaDigits := int(t[p])
	p++
	toa := t[p]
	p++
	oaBytes := (oaDigits + 1) / 2
	if p+oaBytes > len(t) {
		return InboundSMS{}, false
	}
	sender := decodeAddress(toa, t[p:p+oaBytes], oaDigits)
	p += oaBytes
	if p+3 > len(t) {
		return InboundSMS{}, false
	}
	p++ // TP-PID
	dcs := t[p]
	p++
	p += 7 // TP-SCTS 时间戳
	if p >= len(t) {
		return InboundSMS{}, false
	}
	udl := int(t[p])
	p++
	ud := t[p:]
	text := decodeUserData(ud, dcs, udl, udhi)
	sms := InboundSMS{Sender: sender, Text: text, Raw: raw, Total: 1, Seq: 1}
	if udhi {
		sms.Ref, sms.Total, sms.Seq = parseUDHConcat(ud)
	}
	return sms, true
}

// decodeAddress 解码 3GPP 地址（TOA + BCD 半字节），国际号加 '+'。
func decodeAddress(toa byte, bcd []byte, ndigits int) string {
	var sb strings.Builder
	if toa&0x70 == 0x10 { // 国际号
		sb.WriteByte('+')
	}
	digits := 0
	for _, b := range bcd {
		lo := b & 0x0f
		hi := b >> 4
		if lo <= 9 {
			sb.WriteByte('0' + lo)
		}
		digits++
		if digits >= ndigits {
			break
		}
		if hi <= 9 {
			sb.WriteByte('0' + hi)
		}
		digits++
		if digits >= ndigits {
			break
		}
	}
	// 若不是纯数字（alphanumeric，如 "giffgaff"），按 GSM7 解一次
	if toa&0x70 == 0x50 {
		return gsm7Unpack(bcd, (ndigits*4)/7)
	}
	return sb.String()
}

// decodeUserData 按 DCS 解码用户数据；支持 GSM7(默认) 与 UCS2；UDHI 时跳过头。
func decodeUserData(ud []byte, dcs byte, udl int, udhi bool) string {
	skip := 0
	if udhi && len(ud) > 0 {
		skip = int(ud[0]) + 1
	}
	switch {
	case dcs&0x0c == 0x08: // UCS2
		b := ud
		if skip < len(b) {
			b = b[skip:]
		}
		return decodeUCS2(b)
	default: // GSM7
		septets := udl
		if udhi {
			septets -= (skip*8 + 6) / 7
		}
		text := gsm7Unpack(ud, udl)
		// 若有 UDH，前面的字符已含头填充，简单起见按 septets 截尾
		_ = septets
		if skip > 0 && len(text) > (skip*8+6)/7 {
			text = text[(skip*8+6)/7:]
		}
		return text
	}
}

func gsm7Unpack(packed []byte, septets int) string {
	var out []byte
	var bitbuf uint32
	bits := 0
	for _, b := range packed {
		bitbuf |= uint32(b) << bits
		bits += 8
		for bits >= 7 && len(out) < septets {
			out = append(out, byte(bitbuf&0x7f))
			bitbuf >>= 7
			bits -= 7
		}
	}
	return string(out)
}

func decodeUCS2(b []byte) string {
	var sb strings.Builder
	for i := 0; i+1 < len(b); i += 2 {
		sb.WriteRune(rune(binary.BigEndian.Uint16(b[i : i+2])))
	}
	return sb.String()
}

var _ = fmt.Sprintf
