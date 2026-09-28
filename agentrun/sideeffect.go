// 副作用感知的重试守卫：变更类（非幂等）工具一旦执行，整体重跑 = 重复副作用
// （脚本执行/服务操作类工具在首轮已真机生效）。重试入口据此跳过整体重试，
// 宁可如实失败交上层降级/人工，不做「重跑一次赌成功」。
//
// 变更类判定是保守的动词段启发式：按下划线分段**精确**匹配动词表——子串匹配会把
// use_skill（含 kill）、get_running_config（含 run）误判成变更类，白白放弃合法重试；
// 偏保守原则保留在词表侧——拿不准的执行动词都收进来。
package agentrun

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MutatingVerbs 变更类工具动词段表（可扩展：调用方按领域补充后 IsMutatingTool 即生效；
// 并发读取安全——构建期扩展、运行期只读）。
var MutatingVerbs = map[string]bool{
	"exec": true, "execute": true, "run": true, "restart": true, "kill": true,
	"reboot": true, "shutdown": true, "halt": true, "start": true, "stop": true,
	"install": true, "remove": true, "delete": true, "del": true, "create": true,
	"write": true, "set": true, "update": true, "apply": true, "deploy": true,
	"enable": true, "disable": true, "flush": true, "truncate": true, "mount": true,
	"umount": true, "unmount": true, "killall": true, "chmod": true, "chown": true,
}

// IsMutatingTool 工具名是否变更类（非幂等）：小写后按下划线分段精确匹配动词表。
func IsMutatingTool(name string) bool {
	n := strings.ToLower(name)
	for _, seg := range strings.Split(n, "_") {
		if MutatingVerbs[seg] {
			return true
		}
	}
	return false
}

// SideEffectTracker 观测一次 agent 运行中是否调用过变更类工具（事件回调包装器，
// 观测不截断：Wrap 后事件照常透传给原回调）。
type SideEffectTracker struct {
	mu     sync.Mutex
	called string // 首个变更类工具名；""=未调用
}

// Observe 处理一个运行事件（接 OnEvent 回调）。
func (t *SideEffectTracker) Observe(ev Event) {
	if ev.Type == EventToolCall && IsMutatingTool(ev.Tool) {
		t.mu.Lock()
		if t.called == "" {
			t.called = ev.Tool
		}
		t.mu.Unlock()
	}
}

// Wrap 包装事件回调：记录变更类工具调用并透传事件（onEvent 可为 nil）。
func (t *SideEffectTracker) Wrap(onEvent func(Event)) func(Event) {
	return func(ev Event) {
		t.Observe(ev)
		if onEvent != nil {
			onEvent(ev)
		}
	}
}

// Called 是否已调用过变更类工具。
func (t *SideEffectTracker) Called() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.called != ""
}

// MutatingTool 首个被调用的变更类工具名（未调用返回 ""）。
func (t *SideEffectTracker) MutatingTool() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.called
}

// runWithMeta run 的内部形态：额外返回首轮运行中首个被调用的变更类工具名。
func runWithMeta(ctx context.Context, cfg Config, query string, onEvent func(Event)) (string, string, error) {
	var tracker SideEffectTracker
	out, err := run(ctx, cfg, query, tracker.Wrap(onEvent))
	return out, tracker.MutatingTool(), err
}

// mutationSkipErr 重试守卫触发：首轮已执行变更类工具，整体重跑会重复副作用。
func mutationSkipErr(tool string, err error) error {
	return fmt.Errorf("agentrun: 首轮已调用变更类工具 %q，跳过重试防副作用重复（确需重试设 Config.RetryAfterMutation=true）: %w", tool, err)
}
