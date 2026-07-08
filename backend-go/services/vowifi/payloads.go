package vowifi

// IKEv2 载荷构造与解析，对应 poc6 的 tr/sa_ike/sa_esp/ke_p/no_p/idi_p/idr_p/ts_p/cp_p/
// notify_p/device_id_notify/auth_p/eap_p/parse。字节布局与 poc6 完全一致。

import "encoding/binary"

func be16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func be32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }

// tr 构造一个 IKEv2 Transform（tt=type, tid=id, kl=可选密钥长度属性）。
func tr(last bool, tt byte, tid uint16, kl int) []byte {
	var a []byte
	if kl > 0 {
		a = append(be16(0x800E), be16(uint16(kl))...) // 密钥长度属性 (AF=1, type=14)
	}
	b := append([]byte{tt, 0}, be16(tid)...)
	b = append(b, a...)
	first := byte(0)
	if last {
		first = 3
	}
	hdr := append([]byte{first, 0}, be16(uint16(4+len(b)))...)
	return append(hdr, b...)
}

// saIKE: IKE SA proposal（AES-256-CBC + HMAC-SHA256 + PRF-SHA256 + MODP2048），与 poc6 sa_ike 一致。
func saIKE(nxt byte) []byte {
	tfs := append(append(append(
		tr(false, 1, 12, 256),
		tr(false, 2, 5, 0)...),
		tr(false, 3, 12, 0)...),
		tr(true, 4, 14, 0)...)
	pr := append([]byte{0, 0}, be16(uint16(8+len(tfs)))...)
	pr = append(pr, 1, 1, 0, 4) // propnum=1, protoid=1(IKE), spisize=0, numtransforms=4
	pr = append(pr, tfs...)
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(pr)))...)
	return append(hdr, pr...)
}

// saESP: ESP Child SA proposal（AES-256-CBC + HMAC-SHA256 + no ESN），与 poc6 sa_esp 一致。
func saESP(nxt byte, spi []byte) []byte {
	tfs := append(append(
		tr(false, 1, 12, 256),
		tr(false, 3, 12, 0)...),
		tr(true, 5, 0, 0)...) // type5=ESN, id=0(no ESN)
	pr := append([]byte{0, 0}, be16(uint16(12+len(tfs)))...)
	pr = append(pr, 1, 3, 4, 3) // propnum=1, protoid=3(ESP), spisize=4, numtransforms=3
	pr = append(pr, spi...)
	pr = append(pr, tfs...)
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(pr)))...)
	return append(hdr, pr...)
}

func keP(nxt byte, kei []byte) []byte { // KE payload, DH group 14
	b := append(be16(14), be16(0)...)
	b = append(b, kei...)
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(b)))...)
	return append(hdr, b...)
}

func noP(nxt byte, n []byte) []byte { // Nonce payload
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(n)))...)
	return append(hdr, n...)
}

func idRest(nai string) []byte { // ID payload body: IDType(1)=3(RFC822) + RESERVED(3)
	return append([]byte{3, 0, 0, 0}, []byte(nai)...)
}

func idiP(nxt byte, nai string) []byte {
	b := idRest(nai)
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(b)))...)
	return append(hdr, b...)
}

func idrP(nxt byte, fqdn string) []byte { // IDr: IDType(1)=2(FQDN) + RESERVED(3)
	b := append([]byte{2, 0, 0, 0}, []byte(fqdn)...)
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(b)))...)
	return append(hdr, b...)
}

// tsP: Traffic Selector payload，同时协商 IPv4(7) 和 IPv6(8)，与 poc6 ts_p 一致。
func tsP(nxt byte) []byte {
	ts4 := append([]byte{7, 0}, be16(16)...)
	ts4 = append(ts4, be16(0)...)
	ts4 = append(ts4, be16(65535)...)
	ts4 = append(ts4, 0, 0, 0, 0, 255, 255, 255, 255)
	ts6 := append([]byte{8, 0}, be16(40)...)
	ts6 = append(ts6, be16(0)...)
	ts6 = append(ts6, be16(65535)...)
	ts6 = append(ts6, make([]byte, 16)...)
	for i := 0; i < 16; i++ {
		ts6 = append(ts6, 0xff)
	}
	b := append([]byte{2, 0, 0, 0}, ts4...)
	b = append(b, ts6...)
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(b)))...)
	return append(hdr, b...)
}

// cpP: CFG_REQUEST，请求 IPv4/IPv6 地址+DNS+P-CSCF，与 poc6 cp_p 一致。
func cpP(nxt byte) []byte {
	at := []byte{}
	for _, a := range []uint16{1, 3, 8, 10, 20, 21} {
		at = append(at, be16(a)...)
		at = append(at, be16(0)...)
	}
	b := append([]byte{1, 0, 0, 0}, at...) // CFG type=1(REQUEST)
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(b)))...)
	return append(hdr, b...)
}

func notifyP(nxt byte, ntype uint16, data []byte) []byte {
	body := append([]byte{0, 0}, be16(ntype)...) // protoid=0, spisize=0
	body = append(body, data...)
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(body)))...)
	return append(hdr, body...)
}

func imeiBCD(imei string) []byte {
	d := make([]int, len(imei))
	for i, c := range imei {
		d[i] = int(c - '0')
	}
	if len(d)%2 == 1 {
		d = append(d, 0xF)
	}
	out := make([]byte, 0, len(d)/2)
	for i := 0; i < len(d); i += 2 {
		out = append(out, byte(d[i]|(d[i+1]<<4)))
	}
	return out
}

// deviceIDNotify: 3GPP TS24.302 DEVICE_IDENTITY (type 41101)，与 poc6 device_id_notify 一致。
func deviceIDNotify(nxt byte, imei string) []byte {
	bcd := imeiBCD(imei)
	contents := append([]byte{1}, bcd...) // idtype=1(IMEI)
	data := append(be16(uint16(len(contents))), contents...)
	return notifyP(nxt, 41101, data)
}

func authP(nxt byte, data []byte) []byte { // AUTH payload, method=2(shared-key/PSK-style)
	b := append([]byte{2, 0, 0, 0}, data...)
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(b)))...)
	return append(hdr, b...)
}

func eapP(nxt byte, eap []byte) []byte {
	hdr := append([]byte{nxt, 0}, be16(uint16(4+len(eap)))...)
	return append(hdr, eap...)
}

// ikePayload 表示一个解析出来的载荷 (type, body)。
type ikePayload struct {
	Type byte
	Body []byte
}

// parsePayloads 对应 poc6 的 parse(first, data)。
func parsePayloads(first byte, data []byte) []ikePayload {
	var out []ikePayload
	nxt := first
	off := 0
	for nxt != 0 && off < len(data) {
		if off+4 > len(data) {
			break
		}
		pn := data[off]
		pl := int(binary.BigEndian.Uint16(data[off+2 : off+4]))
		if pl < 4 || off+pl > len(data) {
			break
		}
		out = append(out, ikePayload{Type: nxt, Body: data[off+4 : off+pl]})
		nxt = pn
		off += pl
	}
	return out
}
