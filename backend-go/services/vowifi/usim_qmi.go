package vowifi

// USIM AKA 的 QMI 路径：通过 qmicli 的 UIM 逻辑通道 + Send APDU 完成 AUTHENTICATE，
// 作为裸 AT（CCHO/CGLA/CCHC）的备选。QMI 比解析 AT 串口更确定，能规避 AT 口时序抖动
// （+CCHO 空返回等）。要求容器内 qmicli ≥1.34（带 --uim-open-logical-channel/--uim-send-apdu），
// 且该卡已被 mmcli --inhibit 释放（cdc-wdm0 空闲可直连）。
//
// 关键点：qmicli 每次调用是独立 QMI client，退出即释放 CID，而逻辑通道绑定在该 client 上，
// client 一释放通道就关。因此开通道用 --client-no-release-cid 保住 CID，后续 send/close
// 用 --client-cid=<CID> 复用同一 client，最后 close 时才释放。

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reQMIChannel = regexp.MustCompile(`completed:\s*(\d+)`)
	reQMICID     = regexp.MustCompile(`CID:\s*'?(\d+)'?`)
	reQMIApdu    = regexp.MustCompile(`completed:\s*([0-9A-Fa-f:]+)`)
)

// runUSIMAKAViaQMI 用 qmicli 在 cdc-wdm0 上跑一次 USIM AUTHENTICATE，返回 RES/CK/IK。
// dev 如 /dev/cdc-wdm0，slot 一般为 1，aid 为 USIM 应用标识。
func runUSIMAKAViaQMI(dev string, slot int, aid string, rand, autn []byte) (*USIMAKAResult, error) {
	if dev == "" {
		return nil, fmt.Errorf("未配置 QMI 设备")
	}
	// 1) 开逻辑通道并保住 CID。USIM 逻辑通道有限（通常 3 个），失败重试风暴下易被
	// 泄漏占满（InsufficientResources）；遇到就 uim-reset 释放通道后重试一次。
	out, err := qmicli(dev, "", fmt.Sprintf("--uim-open-logical-channel=%d,%s", slot, aid), true)
	if err != nil && strings.Contains(out, "InsufficientResources") {
		qmicli(dev, "", "--uim-reset", false)
		time.Sleep(500 * time.Millisecond)
		out, err = qmicli(dev, "", fmt.Sprintf("--uim-open-logical-channel=%d,%s", slot, aid), true)
	}
	if err != nil {
		return nil, fmt.Errorf("QMI 开逻辑通道失败: %w", err)
	}
	chM := reQMIChannel.FindStringSubmatch(out)
	cidM := reQMICID.FindStringSubmatch(out)
	if chM == nil || cidM == nil {
		return nil, fmt.Errorf("QMI 开通道返回解析失败: %q", out)
	}
	ch, _ := strconv.Atoi(chM[1])
	cid := cidM[1]
	cla := fmt.Sprintf("%02X", ch)
	// 无论后续成败，结束时关通道（关时释放 CID）
	defer qmicli(dev, cid, fmt.Sprintf("--uim-close-logical-channel=%d,%d", slot, ch), false)

	// 2) 发 AUTHENTICATE：CLA(通道) 88 00 81 22 | 10 RAND(16) 10 AUTN(16)
	apdu := cla + "88008122" + "10" + strings.ToUpper(hexStr(rand)) + "10" + strings.ToUpper(hexStr(autn))
	data, err := qmiSendAPDU(dev, cid, slot, ch, apdu)
	if err != nil {
		return nil, fmt.Errorf("QMI 发 AUTHENTICATE 失败: %w", err)
	}
	// 3) 若返回 61XX 需 GET RESPONSE 取剩余数据
	if strings.HasPrefix(data, "61") && len(data) >= 4 {
		ln := data[2:4]
		data, err = qmiSendAPDU(dev, cid, slot, ch, cla+"C00000"+ln)
		if err != nil {
			return nil, fmt.Errorf("QMI GET RESPONSE 失败: %w", err)
		}
	}
	return parseAKAResult(data)
}

// qmiSendAPDU 发一条 APDU（复用 CID），返回去掉冒号的大写 hex 响应（含末尾 SW1SW2）。
func qmiSendAPDU(dev, cid string, slot, ch int, apduHex string) (string, error) {
	out, err := qmicli(dev, cid, fmt.Sprintf("--uim-send-apdu=%d,%d,%s", slot, ch, apduHex), true)
	if err != nil {
		return "", err
	}
	m := reQMIApdu.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("send-apdu 返回解析失败: %q", out)
	}
	return strings.ToUpper(strings.ReplaceAll(m[1], ":", "")), nil
}

// qmicli 执行一次 qmicli 调用。keepCID=true 时加 --client-no-release-cid 保住 client；
// cid 非空时加 --client-cid 复用已有 client。
func qmicli(dev, cid, action string, keepCID bool) (string, error) {
	args := []string{"-d", dev}
	if cid != "" {
		args = append(args, "--client-cid="+cid)
	}
	if keepCID {
		args = append(args, "--client-no-release-cid")
	}
	args = append(args, action)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "qmicli", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// parseAKAResult 解析 AUTHENTICATE 响应 hex（去 SW 后 0xDB || len RES || len CK || len IK）。
func parseAKAResult(data string) (*USIMAKAResult, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("USIM AKA 结果过短: %q", data)
	}
	raw, err := hexDecode(data[:len(data)-4]) // 去掉末尾 SW1SW2
	if err != nil {
		return nil, fmt.Errorf("APDU 结果非法 hex: %w", err)
	}
	if len(raw) == 0 || raw[0] != 0xDB {
		return nil, fmt.Errorf("SIM 鉴权失败(首字节非0xDB): %s", data)
	}
	i := 1
	rl := int(raw[i])
	i++
	if i+rl+1 > len(raw) {
		return nil, fmt.Errorf("AKA 结果长度越界: %s", data)
	}
	res := raw[i : i+rl]
	i += rl
	cl := int(raw[i])
	i++
	if i+cl+1 > len(raw) {
		return nil, fmt.Errorf("AKA 结果长度越界: %s", data)
	}
	ck := raw[i : i+cl]
	i += cl
	il := int(raw[i])
	i++
	if i+il > len(raw) {
		return nil, fmt.Errorf("AKA 结果长度越界: %s", data)
	}
	ik := raw[i : i+il]
	return &USIMAKAResult{RES: res, CK: ck, IK: ik}, nil
}
