// Package hotplug 运行时插拔与原子热替换：启停视图（Plugboard）+ 泛型快照持有点（Holder）。
// 适用「文件管定义、页面管状态」的可插拔组件——reload/启停构建新快照后整体 Store，
// 运行中请求继续用旧快照跑完，新请求即时用新快照。
package hotplug

import (
	"sort"
	"sync/atomic"
)

// Plugboard 启停视图（登记 id → 是否启用）。并发安全；nil 指针=全启用
// （未接插拔的测试/装配路径零成本兼容）。未登记 id 视为启用——注册表校验才是
// 错误路径，插拔视图不越权拦截。
type Plugboard struct {
	v atomic.Pointer[map[string]bool] // value: id → enabled
}

// NewPlugboard 构造：all=登记 id 全集；disabled=被禁集合（含已不在册 id 时忽略）。
func NewPlugboard(all []string, disabled map[string]bool) *Plugboard {
	m := make(map[string]bool, len(all))
	for _, s := range all {
		m[s] = true
	}
	for s := range disabled {
		if _, ok := m[s]; ok {
			m[s] = false
		}
	}
	p := &Plugboard{}
	p.v.Store(&m)
	return p
}

// Enabled 是否启用。
func (p *Plugboard) Enabled(id string) bool {
	if p == nil {
		return true
	}
	if m := p.v.Load(); m != nil {
		if en, ok := (*m)[id]; ok {
			return en
		}
	}
	return true
}

// Disabled 被禁 id 列表（字典序——可观测面输出稳定，不随 map 迭代漂移）。
func (p *Plugboard) Disabled() []string {
	if p == nil {
		return nil
	}
	var out []string
	if m := p.v.Load(); m != nil {
		for s, en := range *m {
			if !en {
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Holder 泛型原子快照持有点：读无锁，换整体原子。
type Holder[T any] struct {
	v atomic.Pointer[T]
}

// NewHolder 构造空持有点（Load 返回 nil，直到首次 Store）。
func NewHolder[T any]() *Holder[T] {
	return &Holder[T]{}
}

// Load 当前快照（可能为 nil——装配完成前）。
func (h *Holder[T]) Load() *T { return h.v.Load() }

// Store 原子换新。
func (h *Holder[T]) Store(v *T) { h.v.Store(v) }
