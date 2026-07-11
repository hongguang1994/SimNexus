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
		Cflag:  unix.CLOCAL | unix.CREAD | unix.CS8,
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
	return s.cmdUntil(at, wait, nil)
}

// cmdUntil 发送 AT 命令并累积读取，直到 done(累积内容) 为真或 maxWait 超时才返回。
// done=nil 时退化为固定读取 maxWait（等同 cmd）。相比固定时间窗口，这样能应对 SIM
// 计算 AKA 的可变延迟——一旦读到期望响应（如 +CGLA:...）或明确的 OK/ERROR 就立即返回。
func (s *serialPort) cmdUntil(at string, maxWait time.Duration, done func(string) bool) string {
	unix.IoctlSetInt(s.fd, unix.TCFLSH, unix.TCIFLUSH)
	unix.Write(s.fd, []byte(at+"\r\n"))
	deadline := time.Now().Add(maxWait)
	buf := make([]byte, 4096)
	var out []byte
	for time.Now().Before(deadline) {
		var rfds unix.FdSet
		rfds.Bits[s.fd/64] |= 1 << (uint(s.fd) % 64)
		tv := unix.Timeval{Sec: 0, Usec: 100000} // 100ms
		n, _ := unix.Select(s.fd+1, &rfds, nil, nil, &tv)
		if n > 0 {
			if r, err := unix.Read(s.fd, buf); err == nil && r > 0 {
				out = append(out, buf[:r]...)
				if done != nil && done(string(out)) {
					return string(out)
				}
			}
		}
	}
	return string(out)
}

// hasResp 返回一个判定函数：累积内容里出现给定正则、或出现 ERROR 即算完成。
func hasResp(re *regexp.Regexp) func(string) bool {
	return func(s string) bool {
		return re.MatchString(s) || strings.Contains(s, "ERROR")
	}
}

var (
	reCCHO = regexp.MustCompile(`CCHO:\s*(\d+)`)
	reCGLA = regexp.MustCompile(`CGLA:\s*\d+,"([0-9A-Fa-f]+)"`)
)

// usimAKA 跑一次 USIM AUTHENTICATE，AT 与 QMI 两条独立路径互为兜底：任一路成功即返回。
// 主备顺序由 cfg.PreferQMI 决定（env VOWIFI_AKA_ORDER=qmi 时 QMI 主、AT 备；否则 AT 主、QMI 备），
// 便于实测比对两条路的成功率。两条路的失败模式相互独立（串口时序 vs QMI 通道/CID）。
func (s *Session) usimAKA(aid string, rand, autn []byte) (*USIMAKAResult, error) {
	slot := s.cfg.USIMSlot
	if slot == 0 {
		slot = 1
	}
	at := func() (*USIMAKAResult, error) { return runUSIMAKA(s.cfg.ATPort, aid, rand, autn) }
	qmi := func() (*USIMAKAResult, error) {
		if s.cfg.QMIDevice == "" {
			return nil, fmt.Errorf("未配置 QMI 设备")
		}
		return runUSIMAKAViaQMI(s.cfg.QMIDevice, slot, aid, rand, autn)
	}

	primaryName, backupName := "AT", "QMI"
	primary, backup := at, qmi
	if s.cfg.PreferQMI {
		primaryName, backupName = "QMI", "AT"
		primary, backup = qmi, at
	}

	res, pErr := primary()
	if pErr == nil {
		s.logf("%s USIM AKA 成功", primaryName)
		return res, nil
	}
	s.logf("%s USIM AKA 失败(%v)，回退 %s...", primaryName, pErr, backupName)
	bres, bErr := backup()
	if bErr != nil {
		return nil, fmt.Errorf("%s 失败(%v) 且 %s 回退也失败(%v)", primaryName, pErr, backupName, bErr)
	}
	s.logf("%s USIM AKA 成功（备选）", backupName)
	return bres, nil
}

// runUSIMAKA 用 RAND/AUTN 在指定串口上跑一次 USIM AUTHENTICATE，返回 RES/CK/IK。
// dev 如 /dev/ttyUSB2，aid 为 USIM 应用标识（默认 A0000000871002FF44FFFF8901010100）。
func runUSIMAKA(dev, aid string, rand, autn []byte) (*USIMAKAResult, error) {
	sp, err := openSerial(dev)
	if err != nil {
		return nil, err
	}
	defer sp.close()

	// 串口时序偶尔抖动（inhibit 刚释放、AT 口未就绪、SIM 计算 AKA 延迟不定），
	// 对整个"开通道→鉴权→取结果"序列做重试，且每步都读到期望响应模式为止。
	var data string
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(500+attempt*500) * time.Millisecond)
		}
		// 清掉所有残留逻辑通道（USIM 通常仅 3 个，之前会话/QMI 泄漏会占满导致 CCHO 报 ERROR）。
		// AT+CCHC 是卡级操作，逐个关 1/2/3，无论此前由 AT 还是 QMI 打开。
		for ch := 1; ch <= 3; ch++ {
			sp.cmd(fmt.Sprintf("AT+CCHC=%d", ch), 300*time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		o := sp.cmdUntil(`AT+CCHO="`+aid+`"`, 3*time.Second, hasResp(reCCHO))
		m := reCCHO.FindStringSubmatch(o)
		if m == nil {
			lastErr = fmt.Errorf("AT+CCHO 无有效返回: %q", o)
			continue
		}
		chStr := m[1]
		ch, _ := strconv.Atoi(chStr)
		cla := fmt.Sprintf("%02X", ch)

		apdu := cla + "88008122" + "10" + strings.ToUpper(hexStr(rand)) + "10" + strings.ToUpper(hexStr(autn))
		o = sp.cmdUntil(fmt.Sprintf(`AT+CGLA=%s,%d,"%s"`, chStr, len(apdu), apdu), 4*time.Second, hasResp(reCGLA))
		dm := reCGLA.FindStringSubmatch(o)
		if dm == nil {
			sp.cmd(fmt.Sprintf("AT+CCHC=%s", chStr), 300*time.Millisecond)
			lastErr = fmt.Errorf("AT+CGLA(挑战) 无有效返回: %q", o)
			continue
		}
		data = strings.ToUpper(dm[1])
		if strings.HasPrefix(data, "61") { // 需要 GET RESPONSE 取剩余数据
			ln := data[2:4]
			o = sp.cmdUntil(fmt.Sprintf(`AT+CGLA=%s,10,"%sC00000%s"`, chStr, cla, ln), 3*time.Second, hasResp(reCGLA))
			dm = reCGLA.FindStringSubmatch(o)
			if dm == nil {
				sp.cmd(fmt.Sprintf("AT+CCHC=%s", chStr), 300*time.Millisecond)
				lastErr = fmt.Errorf("AT+CGLA(GET RESPONSE) 无有效返回: %q", o)
				continue
			}
			data = strings.ToUpper(dm[1])
		}
		sp.cmd(fmt.Sprintf("AT+CCHC=%s", chStr), 400*time.Millisecond)
		lastErr = nil
		break
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return parseAKAResult(data)
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
