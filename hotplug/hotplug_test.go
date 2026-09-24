package hotplug

import (
	"sync"
	"testing"
)

func TestPlugboardNilIsAllEnabled(t *testing.T) {
	var p *Plugboard
	if !p.Enabled("anything") {
		t.Fatal("nil Plugboard 应全启用")
	}
	if len(p.Disabled()) != 0 {
		t.Fatal("nil Plugboard Disabled 应为空")
	}
}

func TestPlugboardEnableDisable(t *testing.T) {
	p := NewPlugboard([]string{"a", "b", "c"}, map[string]bool{"b": true})
	if !p.Enabled("a") || !p.Enabled("c") {
		t.Fatal("未禁用项应启用")
	}
	if p.Enabled("b") {
		t.Fatal("b 应禁用")
	}
	if !p.Enabled("unknown") {
		t.Fatal("未登记 id 应视为启用")
	}
	dis := p.Disabled()
	if len(dis) != 1 || dis[0] != "b" {
		t.Fatalf("Disabled = %v", dis)
	}

	// disabled 含已不在册 id：忽略
	p2 := NewPlugboard([]string{"a"}, map[string]bool{"ghost": true})
	if !p2.Enabled("a") || len(p2.Disabled()) != 0 {
		t.Fatal("不在册 disabled 应忽略")
	}
}

func TestHolderStoreLoad(t *testing.T) {
	h := NewHolder[int]()
	if h.Load() != nil {
		t.Fatal("初始应为 nil")
	}
	v1 := 1
	h.Store(&v1)
	if got := h.Load(); got == nil || *got != 1 {
		t.Fatalf("Load = %v", got)
	}
	v2 := 2
	h.Store(&v2)
	if got := h.Load(); got == nil || *got != 2 {
		t.Fatalf("Load after store = %v", got)
	}
}

func TestHolderConcurrent(t *testing.T) {
	h := NewHolder[int]()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			v := n
			h.Store(&v)
		}(i)
		go func() {
			defer wg.Done()
			_ = h.Load()
		}()
	}
	wg.Wait()
	if h.Load() == nil {
		t.Fatal("并发后不应为 nil")
	}
}
