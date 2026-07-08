package vowifi

// 通过串口 AT 命令执行 USIM AKA（AT+CCHO / AT+CGLA / AT+CCHC），对应 poc6 的 usim_aka。
// 直接用 termios 把 /dev/ttyUSBx 配成 115200 8N1 raw，避免引入额外串口依赖。
// 之所以走裸 AT 而不经 ModemManager：VoWiFi 期间该卡需保持飞行/独占，MM 已被停掉。

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// USIMAKAResult 保存一次 USIM AUTHENTICATE 的结果。
type USIMAKAResult struct {
	RES []byte
	CK  []byte
	IK  []byte
}

type serialPort struct {
	fd int
}

func openSerial(dev string) (*serialPort, error) {
	fd, err := unix.Open(dev, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("打开串口 %s 失败: %w", dev, err)
	}
	// 配成 115200 8N1 raw
	t := unix.Termios{
		Cflag: unix.CLOCAL | unix.CREAD | unix.CS8,
		Ispeed: unix.B115200,
		Ospeed: unix.B115200,
	}
	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &t); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("配置串口失败: %w", err)
	}
	// 切回阻塞模式（配合下面基于时间的读取循环）
	unix.SetNonblock(fd, false)
	return &serialPort{fd: fd}, nil
}

func (s *serialPort) close() { unix.Close(s.fd) }

// cmd 发送一条 AT 命令，等待 wait 后把这段时间内收到的全部数据返回（对应 poc6 mmcmd）。
func (s *serialPort) cmd(at string, wait time.Duration) string {
	// 清空输入缓冲
	unix.IoctlSetInt(s.fd, unix.TCFLSH, unix.TCIFLUSH)
	unix.Write(s.fd, []byte(at+"\r\n"))
	deadline := time.Now().Add(wait)
	buf := make([]byte, 4096)
	var out []byte
	for time.Now().Before(deadline) {
		// 用 select 等待可读，避免忙等
		var rfds unix.FdSet
		rfds.Bits[s.fd/64] |= 1 << (uint(s.fd) % 64)
		tv := unix.Timeval{Sec: 0, Usec: 100000} // 100ms
		n, _ := unix.Select(s.fd+1, &rfds, nil, nil, &tv)
		if n > 0 {
			r, err := unix.Read(s.fd, buf)
			if err == nil && r > 0 {
				out = append(out, buf[:r]...)
			}
		}
	}
	return string(out)
}

var (
	reCCHO = regexp.MustCompile(`CCHO:\s*(\d+)`)
	reCGLA = regexp.MustCompile(`CGLA:\s*\d+,"([0-9A-Fa-f]+)"`)
)

// runUSIMAKA 用 RAND/AUTN 在指定串口上跑一次 USIM AUTHENTICATE，返回 RES/CK/IK。
// dev 如 /dev/ttyUSB2，aid 为 USIM 应用标识（默认 A0000000871002FF44FFFF8901010100）。
func runUSIMAKA(dev, aid string, rand, autn []byte) (*USIMAKAResult, error) {
	sp, err := openSerial(dev)
	if err != nil {
		return nil, err
	}
	defer sp.close()

	sp.cmd("AT+CCHC=1", 600*time.Millisecond) // 清掉可能残留的逻辑通道
	o := sp.cmd(`AT+CCHO="`+aid+`"`, 600*time.Millisecond)
	m := reCCHO.FindStringSubmatch(o)
	if m == nil {
		return nil, fmt.Errorf("AT+CCHO 无有效返回: %q", o)
	}
	chStr := m[1]
	ch, _ := strconv.Atoi(chStr)
	cla := fmt.Sprintf("%02X", ch)

	apdu := cla + "88008122" + "10" + strings.ToUpper(hexStr(rand)) + "10" + strings.ToUpper(hexStr(autn))
	o = sp.cmd(fmt.Sprintf(`AT+CGLA=%s,%d,"%s"`, chStr, len(apdu), apdu), 600*time.Millisecond)
	dm := reCGLA.FindStringSubmatch(o)
	if dm == nil {
		return nil, fmt.Errorf("AT+CGLA(挑战) 无有效返回: %q", o)
	}
	data := strings.ToUpper(dm[1])
	if strings.HasPrefix(data, "61") { // 需要 GET RESPONSE 取剩余数据
		ln := data[2:4]
		o = sp.cmd(fmt.Sprintf(`AT+CGLA=%s,10,"%sC00000%s"`, chStr, cla, ln), 600*time.Millisecond)
		dm = reCGLA.FindStringSubmatch(o)
		if dm == nil {
			return nil, fmt.Errorf("AT+CGLA(GET RESPONSE) 无有效返回: %q", o)
		}
		data = strings.ToUpper(dm[1])
	}
	sp.cmd(fmt.Sprintf("AT+CCHC=%s", chStr), 600*time.Millisecond)

	raw, err := hexDecode(data[:len(data)-4]) // 去掉末尾 SW1SW2
	if err != nil {
		return nil, fmt.Errorf("APDU 结果非法 hex: %w", err)
	}
	if len(raw) == 0 || raw[0] != 0xDB {
		return nil, fmt.Errorf("SIM 鉴权失败(首字节非0xDB): %s", data)
	}
	// 解析 0xDB || len(RES)||RES || len(CK)||CK || len(IK)||IK
	i := 1
	rl := int(raw[i])
	i++
	res := raw[i : i+rl]
	i += rl
	cl := int(raw[i])
	i++
	ck := raw[i : i+cl]
	i += cl
	il := int(raw[i])
	i++
	ik := raw[i : i+il]
	return &USIMAKAResult{RES: res, CK: ck, IK: ik}, nil
}

// 避免与其它文件的辅助函数重名，这里用局部的 hex 辅助。
func hexStr(b []byte) string {
	const hexdig = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdig[c>>4]
		out[i*2+1] = hexdig[c&0xf]
	}
	return string(out)
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("hex 长度为奇数")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, err1 := hexNibble(s[i*2])
		lo, err2 := hexNibble(s[i*2+1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("非法 hex 字符")
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("bad nibble")
}

var _ = os.Stdout // 预留（若后续需要调试输出）
