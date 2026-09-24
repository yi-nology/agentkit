// Package dispatch 通用派发守卫：allow 矩阵 + 深度上限 + 自派发拒绝。
// 拓扑边由宿主经 EdgeSource 抽象提供（编译期校验过的 allow 矩阵），守卫运行期只读；
// 被拒绝的派发返回结构化 *DenyError——调用方不得转述、不得降级，按失败兜底如实上报。
// 沉淀自 bianque engine/dispatch（v0.9.5）：注册表耦合改为 EdgeSource 接口。
package dispatch

import (
	"fmt"
)

// 拒绝原因词表（DenyError.Reason 的取值全集，审计消费方按常量比对而非裸字符串）。
const (
	ReasonNotAllowed    = "not_allowed"    // caller→callee 不在 allow 矩阵
	ReasonDepthExceeded = "depth_exceeded" // 派发深度超限
	ReasonSelfDispatch  = "self_dispatch"  // 自派发
)

// DenyError 结构化拒绝（宿主可原样落审计事件载荷）。
type DenyError struct {
	Caller string
	Callee string
	Depth  int
	Reason string // ReasonNotAllowed | ReasonDepthExceeded | ReasonSelfDispatch
}

func (e *DenyError) Error() string {
	return fmt.Sprintf("派发被拒绝: %s → %s (depth=%d, reason=%s)", e.Caller, e.Callee, e.Depth, e.Reason)
}

// EdgeSource 拓扑边来源：宿主注册表的最小投影（slug 全集 + 各 slug 允许派发的目标）。
type EdgeSource interface {
	Slugs() []string
	// DispatchAllow 返回 slug 允许派发的目标列表（slug 未登记返回 nil）。
	DispatchAllow(slug string) []string
}

// Guard 派发守卫：构建期物化 allow 矩阵，运行期只读。
type Guard struct {
	allow    map[string]map[string]struct{}
	maxDepth int
}

// NewGuard 从边来源构建守卫。
func NewGuard(src EdgeSource, maxDepth int) *Guard {
	g := &Guard{allow: make(map[string]map[string]struct{}), maxDepth: maxDepth}
	for _, slug := range src.Slugs() {
		edges := make(map[string]struct{}, 8)
		for _, target := range src.DispatchAllow(slug) {
			edges[target] = struct{}{}
		}
		g.allow[slug] = edges
	}
	return g
}

// Assert 校验 caller→callee 派发；不合法返回 *DenyError。
func (g *Guard) Assert(caller, callee string, depth int) error {
	if caller == callee {
		return &DenyError{Caller: caller, Callee: callee, Depth: depth, Reason: ReasonSelfDispatch}
	}
	if depth >= g.maxDepth {
		return &DenyError{Caller: caller, Callee: callee, Depth: depth, Reason: ReasonDepthExceeded}
	}
	if _, ok := g.allow[caller][callee]; !ok {
		return &DenyError{Caller: caller, Callee: callee, Depth: depth, Reason: ReasonNotAllowed}
	}
	return nil
}

// AssertDepth 仅校验深度（引擎内部环节推进用）。
func (g *Guard) AssertDepth(depth int) error {
	if depth >= g.maxDepth {
		return &DenyError{Depth: depth, Reason: ReasonDepthExceeded}
	}
	return nil
}

// Allowed 只读查询（供事件载荷/诊断面展示可达性）。
func (g *Guard) Allowed(caller, callee string) bool {
	_, ok := g.allow[caller][callee]
	return ok
}
