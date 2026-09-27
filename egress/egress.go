// Package egress 出口围栏：LLM 可控 URL 的字面量层 SSRF 防护——阻断指向
// 本机/私网/保留段的 IP 字面量与 localhost 域，放行普通域名。
//
// 对标 ZCode webfetch-egress-guard.ts：
//   - 只做字面量判定，不对域名做 DNS preflight——部分网络 1s 内解析不完会
//     误杀公网目标（解析层防护由消费方按需叠加，如自定义 DialContext 的
//     Control 钩子二次校验解析结果）；
//   - IPv4-mapped IPv6（::ffff:0:0/96）与 NAT64 well-known prefix
//     （64:ff9b::/96）先还原内嵌 IPv4 再套同一套 IPv4 策略——这是绕过
//     IP 围栏的经典逃逸路径；
//   - 显式排除 special-use IPv6 段（ORCHID/discard/benchmark/文档段）——
//     标准库 IsPrivate 等判定不覆盖这些段。
//
// 用法：
//
//	if err := egress.Check(u); err != nil { …拒绝抓取… }
package egress

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// BlockedError 围栏命中（结构化：调用方按需呈现原因与目标）。
type BlockedError struct {
	Host   string // 原始 host（URL Hostname 形态）
	Addr   string // 命中判定的地址（载体还原后的字面量；域名为空）
	Reason string // 命中原因（人可读）
}

func (e *BlockedError) Error() string {
	if e.Addr == "" {
		return fmt.Sprintf("egress: %s: %s", e.Reason, e.Host)
	}
	return fmt.Sprintf("egress: %s: %s（host=%s）", e.Reason, e.Addr, e.Host)
}

// Check 校验完整 URL 的 host（解析失败 fail-closed）。
func Check(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("egress: URL 解析失败: %w", err)
	}
	return CheckHost(u.Hostname())
}

// CheckHost 校验 host（URL hostname 或去端口/括号后的 host 字面量）。
// 空值 fail-closed；非 IP 字面量（域名）放行——见包注释的 preflight 取舍。
func CheckHost(host string) error {
	h := normalizeHost(host)
	if h == "" {
		return fmt.Errorf("egress: 空 host")
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return &BlockedError{Host: h, Reason: "localhost 域"}
	}
	addr, err := netip.ParseAddr(h)
	if err != nil {
		return nil // 域名：不解析
	}
	effective := addr
	if addr.Is6() && !addr.Is4In6() {
		if v4, ok := unwrapNAT64(addr); ok {
			effective = v4
		}
	}
	if effective.Is4() || effective.Is4In6() {
		if reason := ipv4Policy(effective.Unmap()); reason != "" {
			return &BlockedError{Host: h, Addr: effective.Unmap().String(), Reason: reason}
		}
		return nil
	}
	if reason := ipv6Policy(effective); reason != "" {
		return &BlockedError{Host: h, Addr: effective.String(), Reason: reason}
	}
	return nil
}

// normalizeHost 小写/去空白/去 IPv6 括号/去尾点（DNS 大小写不敏感 + FQDN 尾点归一）。
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	return strings.TrimSuffix(h, ".")
}

// unwrapNAT64 还原 NAT64/DNS64 well-known prefix（64:ff9b::/96）内嵌的 IPv4；
// 非 NAT64 形态返回 false（64:ff9b:1::/48 等 special-use 段走 IPv6 策略）。
func unwrapNAT64(a netip.Addr) (netip.Addr, bool) {
	b := a.As16()
	if b[0] != 0x00 || b[1] != 0x64 || b[2] != 0xff || b[3] != 0x9b {
		return netip.Addr{}, false
	}
	for _, x := range b[4:12] {
		if x != 0 {
			return netip.Addr{}, false
		}
	}
	var v4 [4]byte
	copy(v4[:], b[12:16])
	return netip.AddrFrom4(v4), true
}

// ipv4ExplicitBlocks 标准库判定之外显式列出的非公网 IPv4 段
// （对标 ipaddr.js range()!=="unicast" 的补集 + benchmark 段）。
var ipv4ExplicitBlocks = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT 共享地址空间
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF 协议赋值
	netip.MustParsePrefix("192.0.2.0/24"),    // 文档段 TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmark（对标 ZCode 显式排除）
	netip.MustParsePrefix("198.51.100.0/24"), // 文档段 TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // 文档段 TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // 保留（含 255.255.255.255 受限广播）
}

func ipv4Policy(a netip.Addr) string {
	switch {
	case a.IsLoopback():
		return "回环地址"
	case a.IsPrivate():
		return "私网地址"
	case a.IsLinkLocalUnicast():
		return "链路本地地址"
	case a.IsMulticast():
		return "组播地址"
	case a.IsUnspecified():
		return "未指定地址"
	}
	for _, p := range ipv4ExplicitBlocks {
		if p.Contains(a) {
			return "保留段 " + p.String()
		}
	}
	return ""
}

// ipv6SpecialUse 标准库判定之外的 special-use IPv6 段（对标 ZCode
// SPECIAL_USE_IPV6_NETWORKS + ipaddr.js 文档/6to4/Teredo 段）。
var ipv6SpecialUse = []netip.Prefix{
	netip.MustParsePrefix("64:ff9b:1::/48"), // NAT64 本地 use（不还原）
	netip.MustParsePrefix("100::/64"),       // discard-only
	netip.MustParsePrefix("2001:2::/48"),    // benchmark
	netip.MustParsePrefix("2001:10::/28"),   // ORCHID（废弃）
	netip.MustParsePrefix("2001:20::/28"),   // ORCHID2
	netip.MustParsePrefix("2001:db8::/32"),  // 文档段
	netip.MustParsePrefix("2002::/16"),      // 6to4
	netip.MustParsePrefix("2001:0::/32"),    // Teredo
}

func ipv6Policy(a netip.Addr) string {
	switch {
	case a.IsLoopback():
		return "回环地址"
	case a.IsPrivate():
		return "唯一本地地址"
	case a.IsLinkLocalUnicast():
		return "链路本地地址"
	case a.IsMulticast():
		return "组播地址"
	case a.IsUnspecified():
		return "未指定地址"
	}
	for _, p := range ipv6SpecialUse {
		if p.Contains(a) {
			return "special-use 段 " + p.String()
		}
	}
	return ""
}
