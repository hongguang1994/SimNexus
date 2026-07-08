package vowifi

// 本文件是 vowifi_poc6.py 中密码学辅助函数的 Go 移植：
// IKEv2 的 prf/prf+（HMAC-SHA256）、EAP-AKA 用的 FIPS186-2 PRF（基于 SHA1 压缩函数）、
// 以及 AES-CBC 加解密封装。所有算法选择与 poc6 保持一致（改动会导致自解密乱码）。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"math/big"
)

// modp2048 (RFC3526 Group 14) 与生成元，用于 IKE_SA_INIT 的 DH 交换。
// 与 poc6 中的常量 P、G=2 完全一致。
var dhG = big.NewInt(2)
var dhPrime = mustHexBig(
	"FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74" +
		"020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F1437" +
		"4FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED" +
		"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF05" +
		"98DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB" +
		"9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B" +
		"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF695581718" +
		"3995497CEA956AE515D2261898FA051015728E5A8AACAA68FFFFFFFFFFFFFFFF")

func mustHexBig(h string) *big.Int {
	n, ok := new(big.Int).SetString(h, 16)
	if !ok {
		panic("vowifi: bad DH prime hex")
	}
	return n
}

// dhPublic 计算 G^x mod P，返回定长 256 字节（2048 位）大端表示。
func dhPublic(x *big.Int) []byte {
	return leftPad(new(big.Int).Exp(dhG, x, dhPrime).Bytes(), 256)
}

// dhShared 计算 peerPub^x mod P，返回定长 256 字节。
func dhShared(peerPub []byte, x *big.Int) []byte {
	p := new(big.Int).SetBytes(peerPub)
	return leftPad(new(big.Int).Exp(p, x, dhPrime).Bytes(), 256)
}

func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b[len(b)-n:]
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

// prf = HMAC-SHA256（对应 poc6 的 prf）。
func prf(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// hmacSHA256 返回完整的 32 字节 HMAC-SHA256（IKE SK 消息的 ICV 取前 16 字节）。
func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// prfPlus 实现 RFC5996 的 prf+，生成 n 字节密钥材料。
func prfPlus(key, seed []byte, n int) []byte {
	var out, t []byte
	for i := byte(1); len(out) < n; i++ {
		h := prf(key, append(append(append([]byte{}, t...), seed...), i))
		out = append(out, h...)
		t = h
	}
	return out[:n]
}

// ---- FIPS186-2 PRF（EAP-AKA 密钥派生用，基于 SHA1 压缩函数），对应 poc6 fips186_prf ----

func sha1Compress(h [5]uint32, block []byte) [5]uint32 {
	var w [80]uint32
	for i := 0; i < 16; i++ {
		w[i] = binary.BigEndian.Uint32(block[i*4 : i*4+4])
	}
	for i := 16; i < 80; i++ {
		v := w[i-3] ^ w[i-8] ^ w[i-14] ^ w[i-16]
		w[i] = (v << 1) | (v >> 31)
	}
	a, b, c, d, e := h[0], h[1], h[2], h[3], h[4]
	for i := 0; i < 80; i++ {
		var f, k uint32
		switch {
		case i < 20:
			f = (b & c) | (^b & d)
			k = 0x5A827999
		case i < 40:
			f = b ^ c ^ d
			k = 0x6ED9EBA1
		case i < 60:
			f = (b & c) | (b & d) | (c & d)
			k = 0x8F1BBCDC
		default:
			f = b ^ c ^ d
			k = 0xCA62C1D6
		}
		t := ((a << 5) | (a >> 27)) + f + e + k + w[i]
		e = d
		d = c
		c = (b << 30) | (b >> 2)
		b = a
		a = t
	}
	return [5]uint32{h[0] + a, h[1] + b, h[2] + c, h[3] + d, h[4] + e}
}

// fips186PRF 对应 poc6 的 fips186_prf(xkey, n)。
func fips186PRF(xkey []byte, n int) []byte {
	t := [5]uint32{0x67452301, 0xEFCDAB89, 0x98BADCFE, 0x10325476, 0xC3D2E1F0}
	mod := new(big.Int).Lsh(big.NewInt(1), 160) // 2^160
	xk := new(big.Int).SetBytes(xkey)
	var out []byte
	for len(out) < n {
		for j := 0; j < 2; j++ {
			xval := new(big.Int).Mod(xk, mod)
			blk := make([]byte, 64)
			copy(blk[:20], leftPad(xval.Bytes(), 20))
			hc := sha1Compress(t, blk)
			w := make([]byte, 20)
			for i := 0; i < 5; i++ {
				binary.BigEndian.PutUint32(w[i*4:], hc[i])
			}
			out = append(out, w...)
			// xk = (1 + xk + w) mod 2^160
			wi := new(big.Int).SetBytes(w)
			xk.Add(xk, wi)
			xk.Add(xk, big.NewInt(1))
			xk.Mod(xk, mod)
		}
	}
	return out[:n]
}

// aesCBCEncrypt / aesCBCDecrypt 封装 AES-CBC，对应 poc6 里对 Cipher(AES,CBC) 的使用。
func aesCBCEncrypt(key, iv, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	ct := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, plain)
	return ct, nil
}

func aesCBCDecrypt(key, iv, ct []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)
	return pt, nil
}
