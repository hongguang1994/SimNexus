package vowifi

// ESP 数据面：外层 ePDG Child SA（AES-256-CBC + HMAC-SHA256-128）与内层 IMS ESP
// （ESP-NULL + HMAC-SHA1-96，传输模式）。对应 poc6 的 esp_encrypt_send/esp_recv_decrypt/
// ims_esp_send2/ims_esp_recv2_raw/build_udp。

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"hash"
	"net"
	"time"
)

// espEncryptSend 用外层子 SA 密钥加密并发送一个内层 IP 包（next_header 通常 41=IPv6）。
func (s *Session) espEncryptSend(payload []byte, nextHeader byte) error {
	iv := randBytes(16)
	padNeeded := (16 - ((len(payload) + 2) % 16)) % 16
	padding := make([]byte, padNeeded)
	for i := 0; i < padNeeded; i++ {
		padding[i] = byte(i + 1)
	}
	plain := append(append(append(append([]byte{}, payload...), padding...), byte(padNeeded)), nextHeader)
	ct, err := aesCBCEncrypt(s.encrI, iv, plain)
	if err != nil {
		return err
	}
	espBody := append(append(append([]byte{}, s.spirChild...), be32(s.espSeq)...), append(iv, ct...)...)
	icv := hmacSHA256(s.authI, espBody)[:16]
	s.espSeq++
	_, err = s.conn.WriteToUDP(append(espBody, icv...), s.epdg)
	return err
}

// espRecvDecrypt 收一个外层 ESP 包并解密，返回 (spi, seq, innerNextHeader, inner)。
// 跳过 IKE 消息（带 natMark 前缀）与短包/keepalive。对应 poc6 esp_recv_decrypt。
func (s *Session) espRecvDecrypt(timeout time.Duration) (spi []byte, seq uint32, nextHdr byte, inner []byte, err error) {
	buf := make([]byte, 4096)
	s.conn.SetReadDeadline(time.Now().Add(timeout))
	n, _, rerr := s.conn.ReadFromUDP(buf)
	if rerr != nil {
		return nil, 0, 0, nil, rerr
	}
	raw := buf[:n]
	if n >= 4 && equal(raw[:4], natMark) {
		return nil, 0, 0, nil, fmt.Errorf("收到的是 IKE 消息，忽略")
	}
	if n < 24 {
		return nil, 0, 0, nil, fmt.Errorf("keepalive/短包 len=%d", n)
	}
	spi = append([]byte{}, raw[0:4]...)
	seq = binary.BigEndian.Uint32(raw[4:8])
	icvR := raw[len(raw)-16:]
	body := raw[8 : len(raw)-16]
	calc := hmacSHA256(s.authR, raw[:len(raw)-16])[:16]
	if !equal(calc, icvR) {
		return nil, 0, 0, nil, fmt.Errorf("外层 ESP ICV 校验失败")
	}
	iv := body[:16]
	ct := body[16:]
	pt, derr := aesCBCDecrypt(s.encrR, iv, ct)
	if derr != nil || len(pt) < 2 {
		return nil, 0, 0, nil, fmt.Errorf("外层 ESP 解密失败")
	}
	padLen := int(pt[len(pt)-2])
	nextHdr = pt[len(pt)-1]
	if 2+padLen > len(pt) {
		return nil, 0, 0, nil, fmt.Errorf("外层 ESP 填充非法")
	}
	inner = pt[:len(pt)-2-padLen]
	return spi, seq, nextHdr, inner, nil
}

// buildUDP 构造带 IPv6 伪首部校验和的 UDP 报文（不含 IPv6 头），对应 poc6 build_udp。
func buildUDP(src, dst []byte, sport, dport uint16, payload []byte) []byte {
	udpLen := 8 + len(payload)
	hdr := append(append(be16(sport), be16(dport)...), append(be16(uint16(udpLen)), be16(0)...)...)
	full := append(hdr, payload...)
	chk := l4Checksum(src, dst, 17, full)
	binary.BigEndian.PutUint16(full[6:8], chk)
	return full
}

// l4Checksum 计算 IPv6 上层协议校验和（伪首部 + L4）。proto=17(UDP)/6(TCP)。
func l4Checksum(src, dst []byte, proto byte, l4 []byte) uint16 {
	pseudo := append(append([]byte{}, src...), dst...)
	pseudo = append(pseudo, be32(uint32(len(l4)))...)
	pseudo = append(pseudo, 0, 0, 0, proto)
	data := append(pseudo, l4...)
	if len(data)%2 == 1 {
		data = append(data, 0)
	}
	var sum uint32
	for i := 0; i < len(data); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	c := ^uint16(sum)
	if c == 0 {
		c = 0xffff
	}
	return c
}

// imsEspSend2 把一个 L4 报文（TCP 或 UDP）包进内层 IMS ESP，再套外层 ESP 发出。
// nh=6(TCP)/17(UDP)。l4Bytes 非空则直接作为 L4；否则用 sipBytes 构造 UDP。
// 对应 poc6 ims_esp_send2。
func (s *Session) imsEspSend2(src, dst []byte, sport, dport uint16, spi uint32, ik []byte,
	sipBytes []byte, nh byte, l4Bytes []byte) error {
	l4 := l4Bytes
	if l4 == nil {
		l4 = buildUDP(src, dst, sport, dport, sipBytes)
	}
	pad := (4 - ((len(l4) + 2) % 4)) % 4
	payload := append(append(append([]byte{}, l4...), make([]byte, pad)...), byte(pad), nh)
	esp := append(be32(spi), be32(s.imsSeq)...)
	s.imsSeq++
	esp = append(esp, payload...)
	icv := hmacSHA1(ik, esp)[:12]
	espFull := append(esp, icv...)
	// 内层 IPv6 头（传输模式：next_header=50 指向 ESP），外层 ESP next_header=41(IPv6)
	ipv6 := append(append(be32(0x60000000), be16(uint16(len(espFull)))...), byte(50), byte(64))
	ipv6 = append(ipv6, src...)
	ipv6 = append(ipv6, dst...)
	return s.espEncryptSend(append(ipv6, espFull...), 41)
}

// imsEspRecv2Raw 收一个内层 IMS ESP 包，校验 ICV(MD5/SHA1 都试)并去掉 IMS ESP 头/trailer，
// 返回 (内层 next_header, L4 载荷含头)。对应 poc6 ims_esp_recv2_raw。
func (s *Session) imsEspRecv2Raw(ik []byte, timeout time.Duration) (byte, []byte, bool) {
	_, _, nh, inner, err := s.espRecvDecrypt(timeout)
	if err != nil {
		return 0, nil, false
	}
	var esp []byte
	if nh == 50 {
		esp = inner
	} else if nh == 41 && len(inner) >= 40 && inner[6] == 50 {
		plen := int(binary.BigEndian.Uint16(inner[4:6]))
		if plen > 0 && 40+plen <= len(inner) {
			esp = inner[40 : 40+plen]
		} else {
			esp = inner[40:]
		}
	} else {
		return 0, nil, false
	}
	if len(esp) < 20 {
		return 0, nil, false
	}
	icv := esp[len(esp)-12:]
	body := esp[:len(esp)-12]
	okMD5 := equal(hmacTrunc12(md5.New, ik, body), icv)
	okSHA1 := equal(hmacTrunc12(sha1.New, ik, body), icv)
	if !okMD5 && !okSHA1 {
		s.logf("[recv] 内层 ESP ICV 校验失败 esp_len=%d（收到包但解不开，可能丢分片/密钥不符）", len(esp))
		return 0, nil, false
	}
	pl := body[8:]
	if len(pl) < 2 {
		return 0, nil, false
	}
	padlen := int(pl[len(pl)-2])
	innh := pl[len(pl)-1]
	if 2+padlen > len(pl) {
		return 0, nil, false
	}
	l4 := pl[:len(pl)-2-padlen]
	return innh, l4, true
}

// hmacTrunc12 返回 HMAC(h, key, data) 的前 12 字节（HMAC-*-96）。
func hmacTrunc12(h func() hash.Hash, key, data []byte) []byte {
	m := hmac.New(h, key)
	m.Write(data)
	return m.Sum(nil)[:12]
}

var _ = net.IPv4zero
