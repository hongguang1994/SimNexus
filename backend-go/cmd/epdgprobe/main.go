// epdgprobe：独立验证 ePDG 动态 DNS 解析（DoH）在部署环境里能否拿到真实 IP。
// 用法： ./epdgprobe [mcc] [mnc]   默认 234 10（giffgaff / O2 UK）
package main

import (
	"fmt"
	"os"
	"time"

	"simnexus-go/services/vowifi"
)

func main() {
	mcc, mnc := "234", "10"
	if len(os.Args) >= 3 {
		mcc, mnc = os.Args[1], os.Args[2]
	}
	fqdn := vowifi.EPDGFQDN(mcc, mnc)
	fmt.Printf("FQDN: %s\n", fqdn)
	start := time.Now()
	ips, err := vowifi.ResolveEPDG(mcc, mnc, 8*time.Second)
	dur := time.Since(start)
	if err != nil {
		fmt.Printf("解析失败（%v）: %v\n", dur, err)
		os.Exit(1)
	}
	fmt.Printf("解析成功（%v），共 %d 个 IP:\n", dur, len(ips))
	for i, ip := range ips {
		fmt.Printf("  %d) %s\n", i+1, ip)
	}
}
