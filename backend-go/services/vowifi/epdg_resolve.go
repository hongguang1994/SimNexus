package vowifi

// ePDG 动态解析（第二方案，opt-in）。
//
// 标准 3GPP 流程：用 SIM 的 MCC/MNC 拼出 ePDG 的 FQDN
//   epdg.epc.mnc<MNC3>.mcc<MCC3>.pub.3gppnetwork.org
// 再用 DNS 解析出运营商的 ePDG IPv4 列表。
//
// 为什么不用系统解析器：本项目部署在中国的服务器，DNS 常被 Clash/OpenClash
// 劫持成 fake-ip（198.18.x.x），net.LookupHost 拿不到真实 IP。这里改走 DoH
// （DNS over HTTPS，HTTPS 走代理路由）绕过劫持，直接向 dns.google / cloudflare
// 拿到公网权威应答（实测 O2/giffgaff 返回 87.194.{8,9,88,89}.8）。
//
// P-CSCF 不需要在这里查：它由 ePDG 在 IKE_AUTH 的 CFG_REPLY 里动态下发
//（见 ike.go 的 parseFinalAuth / PCSCFv6），无需硬编码运营商特例表。

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// EPDGFQDN 按 3GPP 规范拼装 ePDG 的 FQDN。MNC 补零到 3 位，MCC 已是 3 位。
func EPDGFQDN(mcc, mnc string) string {
	mcc = strings.TrimSpace(mcc)
	mnc = strings.TrimSpace(mnc)
	for len(mnc) < 3 {
		mnc = "0" + mnc
	}
	return fmt.Sprintf("epdg.epc.mnc%s.mcc%s.pub.3gppnetwork.org", mnc, mcc)
}

// dohAnswer 是 Google/Cloudflare DoH JSON 应答里的一条记录。
type dohAnswer struct {
	Name string `json:"name"`
	Type int    `json:"type"` // 1 = A
	Data string `json:"data"`
}

type dohResp struct {
	Status int         `json:"Status"`
	Answer []dohAnswer `json:"Answer"`
}

// 依次尝试的 DoH 端点（都返回同构的 JSON 格式）。
var dohEndpoints = []string{
	"https://dns.google/resolve",
	"https://cloudflare-dns.com/dns-query",
}

// ResolveEPDG 用 DoH 解析 ePDG FQDN，返回去重后的 IPv4 列表（保序）。
// 任一端点成功即返回；全部失败返回错误，调用方应回退到配置的静态 IP。
func ResolveEPDG(mcc, mnc string, timeout time.Duration) ([]string, error) {
	fqdn := EPDGFQDN(mcc, mnc)
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	var lastErr error
	for _, ep := range dohEndpoints {
		ips, err := dohQueryA(client, ep, fqdn)
		if err != nil {
			lastErr = err
			continue
		}
		if len(ips) > 0 {
			return ips, nil
		}
		lastErr = fmt.Errorf("%s 无 A 记录", ep)
	}
	return nil, fmt.Errorf("解析 %s 失败: %v", fqdn, lastErr)
}

func dohQueryA(client *http.Client, endpoint, fqdn string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), client.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("name", fqdn)
	q.Set("type", "A")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/dns-json") // Cloudflare 需要此头

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var dr dohResp
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return nil, err
	}
	if dr.Status != 0 {
		return nil, fmt.Errorf("DoH Status=%d", dr.Status)
	}
	seen := map[string]bool{}
	var ips []string
	for _, a := range dr.Answer {
		if a.Type != 1 {
			continue
		}
		ip := strings.TrimSpace(a.Data)
		if net.ParseIP(ip).To4() == nil || seen[ip] {
			continue
		}
		seen[ip] = true
		ips = append(ips, ip)
	}
	return ips, nil
}
