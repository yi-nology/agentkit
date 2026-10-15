package logredact

import (
	"strings"
	"sync"
	"testing"
)

func TestMaskerExactAndIPv4(t *testing.T) {
	m := NewMasker()
	tok := m.AddSecret("web-prod-01")
	if tok != "«T1»" {
		t.Fatalf("首个令牌应为 «T1»，得 %q", tok)
	}
	in := "host web-prod-01 (192.168.1.10) unreachable, retry 192.168.1.10"
	masked := m.Mask(in)
	if strings.Contains(masked, "web-prod-01") || strings.Contains(masked, "192.168.1.10") {
		t.Fatalf("掩码泄漏: %q", masked)
	}
	// 同 IP 恒同令牌（两处 IP 都应是 «T2»）
	if strings.Count(masked, "«T2»") != 2 {
		t.Fatalf("两处 IP 应同令牌 «T2»: %q", masked)
	}
	back := m.Restore(masked)
	if back != in {
		t.Fatalf("回填应无损:\n got %q\nwant %q", back, in)
	}
}

func TestMaskerUnknownTokenPreserved(t *testing.T) {
	m := NewMasker()
	in := "报告引用 «T99» 与 «T1x»"
	if got := m.Restore(in); got != in {
		t.Fatalf("未知令牌应原样保留: %q", got)
	}
}

func TestMaskerConcurrent(t *testing.T) {
	m := NewMasker()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.AddSecret("host-" + string(rune('a'+i)))
			_ = m.Mask("10.0.0." + string(rune('1'+i)) + " down")
			_ = m.Restore("«T1» ok")
		}(i)
	}
	wg.Wait()
}
