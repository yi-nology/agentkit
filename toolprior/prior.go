// Package toolprior 工具优先级决策层：给 ReAct agent 的工具表加上
// 提示词指引（软）、结构顺序（隐式）、调用限制（硬）三层约束。
//
// 问题背景：扁平工具表里所有工具平等竞争模型注意力——模型可能跳过
// 核心证据工具（get_file）直接刷外部 MCP 工具，且外部调用无止损。
//
// 三层机制：
//  1. StrategyPrompt()——把"何时用哪个、成本多高"渲染成提示词段，注入 instruction
//  2. Ordered()——工具表按优先级稳定排序（模型对表顺序有注意力偏好）
//  3. LimitCalls()——调用次数硬上限，超限返回固定提示文本软止损（模型可见，
//     防 runaway 循环）
package toolprior

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// 优先级档位（数值越小越优先；自定义档位可插在中间）。
const (
	// PriorityCore 核心证据工具：判定必需，必须最先使用（get_file 等）。
	PriorityCore = 0
	// PrioritySupport 支撑工具：核心证据不足时补充（knowledge/skill）。
	PrioritySupport = 1
	// PriorityExternal 外部工具：有网络/进程开销，本地证据穷尽后再用（MCP）。
	PriorityExternal = 2
)

// Entry 一条工具注册项。
type Entry struct {
	Tool tool.BaseTool
	// Priority 优先级（越小越先；同值保持注册序）。
	Priority int
	// When 何时使用（决策指引，注入提示词；空 = 只排序不渲染指引）。
	When string
	// Cost 成本提示（low/medium/high；空 = 不标注）。
	Cost string
}

// Table 工具优先级表。构建期写入（Add）、构建后只读（Ordered/StrategyPrompt）——
// 非并发安全，调用方需保证"先构建后共享"。
type Table struct {
	entries []Entry
}

// NewTable 创建空表。
func NewTable() *Table { return &Table{} }

// Add 注册一条（链式）。Tool 为 nil 时 panic——注册期配置错误应尽早暴露，
// 运行期才炸的 nil Tool 难排查。
func (t *Table) Add(e Entry) *Table {
	if e.Tool == nil {
		panic("toolprior: Entry.Tool 不能为 nil")
	}
	t.entries = append(t.entries, e)
	return t
}

// Len 当前条目数。
func (t *Table) Len() int { return len(t.entries) }

// resolvedEntry 一条经 Info 解析的注册项（排序视图元素）。
type resolvedEntry struct {
	e       Entry
	idx     int    // 注册序（同优先级的稳定 tiebreak）
	name    string // Info 失败为空串
	missing bool   // Info 失败/无名：排末尾，不遮挡可用工具
}

// sortedEntries 统一排序视图（Ordered 与 StrategyPrompt 共用）：priority 升序 →
// Info 失败的排末尾 → 同档保持注册序。两处共用同一视图，提示词宣称的顺序
// 与真实工具表顺序才不会在工具元数据缺失时互相矛盾。
func (t *Table) sortedEntries(ctx context.Context) []resolvedEntry {
	rs := make([]resolvedEntry, len(t.entries))
	for i, e := range t.entries {
		name := ""
		if info, err := e.Tool.Info(ctx); err == nil && info.Name != "" {
			name = info.Name
		}
		rs[i] = resolvedEntry{e: e, idx: i, name: name, missing: name == ""}
	}
	sort.SliceStable(rs, func(a, b int) bool {
		if rs[a].e.Priority != rs[b].e.Priority {
			return rs[a].e.Priority < rs[b].e.Priority
		}
		if rs[a].missing != rs[b].missing {
			return rs[b].missing // Info 失败的排末尾，不遮挡可用工具
		}
		return rs[a].idx < rs[b].idx
	})
	return rs
}

// Ordered 按优先级稳定排序返回工具表（同优先级保持注册序）。
// Info 失败的条目排在末尾（排序键不可得时不遮挡可用工具）。
func (t *Table) Ordered(ctx context.Context) []tool.BaseTool {
	rs := t.sortedEntries(ctx)
	out := make([]tool.BaseTool, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.e.Tool)
	}
	return out
}

// StrategyPrompt 渲染"工具使用策略"提示词段（按优先级序，与 Ordered 同一视图）。
// 空表返回空串。调用方拼进 agent instruction。
func (t *Table) StrategyPrompt(ctx context.Context) string {
	if len(t.entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("===【工具使用策略（按优先级决策）】===\n")
	for _, r := range t.sortedEntries(ctx) {
		name := r.name
		if name == "" {
			name = fmt.Sprintf("tool#%d", r.idx)
		}
		var parts []string
		parts = append(parts, fmt.Sprintf("priority=%d", r.e.Priority))
		if r.e.Cost != "" {
			parts = append(parts, "cost="+r.e.Cost)
		}
		if r.e.When != "" {
			parts = append(parts, r.e.When)
		}
		fmt.Fprintf(&b, "- %s（%s）\n", name, strings.Join(parts, "；"))
	}
	b.WriteString("原则：先用优先级数值小的工具取得基础证据；优先级数值大的外部工具（有网络/进程开销）仅在本地信息不足时调用，避免不必要的外部开销。")
	return b.String()
}

// limitedTool 调用次数受限的工具包装（原子计数，并发安全）。
type limitedTool struct {
	inner tool.InvokableTool
	max   int64
	calls atomic.Int64
}

// LimitCalls 包装工具：调用超过 max 次后返回固定提示文本（nil error）软止损。
// 必须返回文本而非 Go error——eino ToolsNode 会把工具 error 直接上抛中止整个
// agent 运行，模型永远看不到提示、此前轮次的部分结论全部丢弃；返回文本则模型
// 可见，可基于已有信息收尾（软止损 + 保留部分进展）。
// max<=0 = 不限制（原样返回）。包装每次新建——计数不跨任务共享。
// 入参需 InvokableTool（嵌入 BaseTool，Info 由内层透出）。
func LimitCalls(t tool.InvokableTool, max int) tool.BaseTool {
	if max <= 0 {
		return t
	}
	return &limitedTool{inner: t, max: int64(max)}
}

func (l *limitedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return l.inner.Info(ctx)
}

func (l *limitedTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	if l.calls.Add(1) > l.max {
		return fmt.Sprintf(
			"LIMIT_REACHED: 本工具调用已达上限（%d 次）。不要再调用本工具；请基于已获取的信息直接给出结论。",
			l.max), nil
	}
	return l.inner.InvokableRun(ctx, args, opts...)
}
