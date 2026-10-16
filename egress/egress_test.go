package egress

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckHostBlocked(t *testing.T) {
	blocked := []struct{ host, wantReason string }{
		{"localhost", "localhost 域"},
		{"LOCALHOST", "localhost 域"},
		{"api.localhost.", "localhost 域"}, // 尾点归一
		{"127.0.0.1", "回环地址"},
		{"0.0.0.0", "未指定地址"},
		{"10.1.2.3", "私网地址"},
		{"172.16.0.1", "私网地址"},
		{"192.168.1.1", "私网地址"},
		{"169.254.169.254", "链路本地地址"}, // 云元数据端点
		{"224.0.0.1", "组播地址"},
		{"255.255.255.255", "保留段"},
		{"100.64.0.1", "保留段"},
		{"198.18.5.5", "保留段"}, // benchmark
		{"192.0.2.1", "保留段"},
		{"240.1.2.3", "保留段"},
		{"::1", "回环地址"},
		{"::", "未指定地址"},
		{"fe80::1", "链路本地地址"},
		{"fd00::1", "唯一本地地址"},
		{"2001:db8::1", "special-use"},
		{"2002:0808:0808::1", "special-use"}, // 6to4
		{"64:ff9b:1::1", "special-use"},      // NAT64 本地 use 段
		{"100::1", "special-use"},
		// IPv4-mapped IPv6 还原后套 IPv4 策略（逃逸路径）
		{"::ffff:127.0.0.1", "回环地址"},
		{"::ffff:169.254.169.254", "链路本地地址"},
		{"[::FFFF:10.0.0.1]", "私网地址"},
		// NAT64 well-known prefix 还原
		{"64:ff9b::7f00:1", "回环地址"},      // 127.0.0.1
		{"64:ff9b::a00:1", "私网地址"},       // 10.0.0.1
		{"64:ff9b::a9fe:a9fe", "链路本地地址"}, // 169.254.169.254
	}
	for _, tc := range blocked {
		err := CheckHost(tc.host)
		if err == nil {
			t.Errorf("%s 应被阻断", tc.host)
			continue
		}
		var be *BlockedError
		if !errors.As(err, &be) {
			t.Errorf("%s 应为 BlockedError，得 %T", tc.host, err)
			continue
		}
		if !strings.Contains(be.Reason, tc.wantReason) {
			t.Errorf("%s 命中原因 %q 不含 %q", tc.host, be.Reason, tc.wantReason)
		}
	}
}

func TestCheckHostAllowed(t *testing.T) {
	allowed := []string{
		"example.com", // 域名不 preflight
		"EXAMPLE.com.",
		"sub.example.co.uk",
		"8.8.8.8",
		"1.1.1.1",
		"93.184.216.34",
		"2606:4700::1111",      // 公网 IPv6（Cloudflare DNS）
		"::ffff:8.8.8.8",       // 映射回公网 IPv4
		"64:ff9b::808:808",     // NAT64 回公网 IPv4（8.8.8.8）
		"2001:4860:4860::8888", // 公网 IPv6（Google DNS）
	}
	for _, h := range allowed {
		if err := CheckHost(h); err != nil {
			t.Errorf("%s 不应被阻断: %v", h, err)
		}
	}
}

func TestCheckURLForms(t *testing.T) {
	blocked := []string{
		"http://localhost:8080/admin",
		"http://127.0.0.1:9090/metrics",
		"http://[::1]:8080/x",
		"https://10.0.0.1/path?q=1",
		"http://metadata.google.internal/", // .internal 不是 localhost——域名放行？
	}
	for _, u := range blocked[:4] {
		if err := Check(u); err == nil {
			t.Errorf("%s 应被阻断", u)
		}
	}
	// 普通内网域名不在字面量围栏范围（无 DNS preflight，取舍见包注释）
	if err := Check(blocked[4]); err != nil {
		t.Errorf("域名不在字面量围栏范围: %v", err)
	}
	if err := Check("https://example.com/docs"); err != nil {
		t.Errorf("公网 URL 不应被阻断: %v", err)
	}
	if err := Check("http://[::ffff:192.168.0.1]:8080/"); err == nil {
		t.Error("映射私网 IPv6 应被阻断")
	}
}

func TestCheckInvalidInput(t *testing.T) {
	if err := Check("://bad url"); err == nil || !strings.Contains(err.Error(), "解析失败") {
		t.Errorf("非法 URL 应 fail-closed: %v", err)
	}
	if err := CheckHost(""); err == nil {
		t.Error("空 host 应 fail-closed")
	}
	if err := CheckHost("  "); err == nil {
		t.Error("空白 host 应 fail-closed")
	}
}
