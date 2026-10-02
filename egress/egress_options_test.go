package egress

import "testing"

func TestCheckHostWithAllowLoopback(t *testing.T) {
	o := Options{AllowLoopback: true}
	for _, host := range []string{"127.0.0.1", "::1", "0.0.0.0", "::"} {
		if err := CheckHostWith(host, o); err != nil {
			t.Fatalf("%s 应被 AllowLoopback 放行：%v", host, err)
		}
	}
	// 缺省零值仍拒
	if err := CheckHost("127.0.0.1"); err == nil {
		t.Fatal("缺省应拒绝回环")
	}
}

func TestCheckHostWithAllowPrivate(t *testing.T) {
	o := Options{AllowPrivate: true}
	for _, host := range []string{"10.44.1.5", "192.168.1.10", "172.25.0.1", "fd00::8"} {
		if err := CheckHostWith(host, o); err != nil {
			t.Fatalf("%s 应被 AllowPrivate 放行：%v", host, err)
		}
	}
	// 链路本地（含云元数据）不因 AllowPrivate 放行
	if err := CheckHostWith("169.254.169.254", o); err == nil {
		t.Fatal("云元数据地址不应被放行")
	}
	// CGNAT/保留段不因 AllowPrivate 放行（放行面只覆盖 RFC1918/ULA）
	if err := CheckHostWith("100.64.0.1", o); err == nil {
		t.Fatal("CGNAT 段不应被放行")
	}
	if err := CheckHostWith("198.18.0.1", o); err == nil {
		t.Fatal("benchmark 段不应被放行")
	}
}

func TestCheckWithParsesURL(t *testing.T) {
	o := Options{AllowPrivate: true, AllowLoopback: true}
	if err := CheckWith("http://127.0.0.1:8943/health", o); err != nil {
		t.Fatalf("本机健康探测应放行：%v", err)
	}
	if err := CheckWith("https://10.44.1.5/api", o); err != nil {
		t.Fatalf("内网探测应放行：%v", err)
	}
	if err := CheckWith("http://169.254.169.254/latest/meta-data", o); err == nil {
		t.Fatal("元数据地址应拒绝")
	}
	if err := CheckWith("://bad", o); err == nil {
		t.Fatal("解析失败应 fail-closed")
	}
}

func TestCheckHostWithDefaultsParity(t *testing.T) {
	// CheckHost == CheckHostWith(零值)：公开面语义不漂移
	for _, host := range []string{"8.8.8.8", "127.0.0.1", "10.0.0.1", "fe80::1", "example.com"} {
		e1, e2 := CheckHost(host), CheckHostWith(host, Options{})
		if (e1 == nil) != (e2 == nil) {
			t.Fatalf("%s 两入口语义不一致：%v vs %v", host, e1, e2)
		}
	}
}

// TestAllowLoopbackCoversLocalhostDomain 回归（第八轮审计）：localhost 域即
// 回环的域形态——AllowLoopback 的文档场景（探测本机自建服务）不得拒掉
// http://localhost:port/health 这最典型形态。
func TestAllowLoopbackCoversLocalhostDomain(t *testing.T) {
	if err := CheckWith("http://localhost:8943/health", Options{AllowLoopback: true}); err != nil {
		t.Fatalf("AllowLoopback 应放行 localhost 域: %v", err)
	}
	if err := CheckWith("http://localhost:8943/health", Options{}); err == nil {
		t.Fatal("零值仍应拒绝 localhost 域")
	}
}
