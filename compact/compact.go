// Package compact 会话压缩策略面：auto-compact 判定（阈值/熔断/token 双轨）+
// microcompact（旧工具结果占位清除）。对标 ZCode compact/policy.ts + microcompact.ts。
//
// 两层分工：
//   - ShouldCompact 回答「该压缩了吗」——阈值 = (contextWindow − output 预留) −
//     buffer（provider 的 context window 是输入输出共享窗口，阈值分母必须先扣
//     output 侧）；token 口径双轨：优先 provider 真实用量（含 cache 读写，本地
//     估算漏计缓存），无则本地估算；连续失败 3 次熔断（压缩本身失败反复重试
//     只会原地打转）。
//   - MaybeMicrocompact 回答「不动模型就能省一点吗」——轻量先手：把旧工具结果
//     替换为占位符（保留最近 N 组与出错结果），无需摘要请求。阈值 = auto 阈值
//     的 min(90%, 阈值−2000)（先于 auto 触发才有意义）；idle 超时也触发
//     （长会话挂起后旧结果大概率已无引用价值）。
//
// 本包只做确定性判定与转写变换；摘要生成（真压缩）归调用方——模型请求、
// prompt 组装与 provider 交互不在工具箱原语层。
//
// 用法：
//
//	d := compact.ShouldCompact(msgs, compact.Config{ContextWindow: 128_000}, failures, usage)
//	if d.ShouldCompact { …调用方做摘要压缩… }
//	mr := compact.MaybeMicrocompact(msgs, compact.MicroConfig{
//	    ThresholdTokens: compact.BuildDefaultThreshold(d.Threshold),
//	}, lastDone, time.Now())
//	msgs = mr.Messages
package compact

import (
	"math"
	"time"
	"unicode/utf8"

	"github.com/yi-nology/agentkit/textutil"
)

// 策略常量（对标 ZCode policy.ts；每个值都是生产调过的，别拍脑袋改）。
const (
	// DefaultContextWindow 缺省上下文窗口。
	DefaultContextWindow = 200_000
	// DefaultOutputReserve 缺省 output 预留（正常请求输出已收敛 32K 目标）。
	DefaultOutputReserve = 32_000
	// ReserveCap output 预留上限（preflight 口径：完整预留既过早压缩又浪费窗口）。
	ReserveCap = 21_000
	// DefaultBuffer 阈值下方缓冲（压缩动作本身要花 token，贴线触发会反复穿越）。
	DefaultBuffer = 13_000
	// MaxConsecutiveFailures 连续压缩失败熔断阈值。
	MaxConsecutiveFailures = 3
)

// Config auto-compact 策略配置。零值可用（全部落缺省）。
type Config struct {
	// ContextWindow ≤0 → DefaultContextWindow。
	ContextWindow int
	// MaxOutputTokens ≤0 → DefaultOutputReserve；实际预留 = min(值, ReserveCap)。
	MaxOutputTokens int
	// BufferTokens ≤0 → DefaultBuffer。
	BufferTokens int
	// MaxConsecutiveFailures ≤0 → MaxConsecutiveFailures。
	MaxConsecutiveFailures int
	// Enabled nil = 启用；false = 整体关闭（决策面仍返回计量供观测）。
	Enabled *bool
}

// EffectiveWindow 输入侧可用窗口 = window − min(预留, window)，下限 0。
func (c Config) EffectiveWindow() int {
	w := c.contextWindow()
	reserve := min(c.outputReserve(), w)
	return max(0, w-reserve)
}

// Threshold 压缩触发阈值 = max(0, 有效窗口 − buffer)。
func (c Config) Threshold() int {
	buffer := c.buffer()
	if buffer <= 0 {
		buffer = DefaultBuffer
	}
	return max(0, c.EffectiveWindow()-buffer)
}

func (c Config) contextWindow() int {
	if c.ContextWindow > 0 {
		return c.ContextWindow
	}
	return DefaultContextWindow
}

func (c Config) outputReserve() int {
	if c.MaxOutputTokens > 0 {
		return min(c.MaxOutputTokens, ReserveCap)
	}
	return min(DefaultOutputReserve, ReserveCap)
}

func (c Config) buffer() int {
	if c.BufferTokens > 0 {
		return c.BufferTokens
	}
	return DefaultBuffer
}

func (c Config) maxFailures() int {
	if c.MaxConsecutiveFailures > 0 {
		return c.MaxConsecutiveFailures
	}
	return MaxConsecutiveFailures
}

func (c Config) enabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// Role 消息角色词表（Message.Role 的合法值——此前魔法字符串散落两文件，
// 拼错即静默改变分组行为；收口轮常量化，值不变零迁移）。
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message 转写消息（对话 + 工具调用形态的最小通用模型）。
type Message struct {
	Role string // RoleUser | RoleAssistant | RoleTool（词表常量见下）
	Text string
	// ToolCalls assistant 发起的工具调用（token 估算必须计入——大型入参若只计
	// 正文，auto-compact 与预算口径都会低估，ZCode 踩过的漏计面）。
	ToolCalls []ToolCall
	// ToolCallID / ToolName role=tool 时标识对应调用与工具名。
	ToolCallID string
	ToolName   string
	// IsError 工具结果失败（microcompact 默认保留——出错结果是排障证据）。
	IsError bool
}

// ToolCall 工具调用记录（ArgsJSON 为参数原文，仅用于 token 估算与展示）。
type ToolCall struct {
	ID       string
	Name     string
	ArgsJSON string
}

// EstimateTokens 本地 token 估算：Σ ceil(字符数/3)，字符数含正文与工具调用
// 入参（name + args JSON）。
func EstimateTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += msgTokens(m)
	}
	return total
}

// msgTokens 单条消息的 token 估算（与 EstimateTokens 同口径）——增量改写时
// 只算被触碰条目，免整表二次扫描。
// CJK 口径（第九轮审计：rune/3 对中文系统性低估约半——压缩阈值到点时
// 真实 token 已近两倍超窗，provider 硬失败先于压缩发生）
func msgTokens(m Message) int {
	n := utf8.RuneCountInString(m.Text)
	for _, tc := range m.ToolCalls {
		n += utf8.RuneCountInString(tc.Name) + utf8.RuneCountInString(tc.ArgsJSON)
	}
	return textutil.EstTokensCJKRunes(n)
}

// HasEnoughToCompact 消息量是否值得压缩（<2 个 assistant 轮次压缩无意义——
// 摘要请求的开销可能超过省下的窗口）。
func HasEnoughToCompact(msgs []Message) bool {
	assistant := 0
	for _, m := range msgs {
		if m.Role == RoleAssistant {
			assistant++
		}
	}
	return assistant >= 2
}

// TokenSource token 口径来源。
type TokenSource string

const (
	SourceEstimate      TokenSource = "estimate"       // 本地估算
	SourceProviderUsage TokenSource = "provider_usage" // provider 真实用量
)

// TokenOverride provider 真实用量（ShouldCompact 优先于本地估算——provider 口径
// 覆盖 cache 读写与嵌入内容，本地估算必然低估）。
type TokenOverride struct {
	TokenCount int // 必填：provider 报告的输入侧总量
	CacheRead  int // 可观测用
	CacheWrite int
	Output     int
}

// Reason 决策原因（可观测；语义对标 ZCode AutoCompactDecision.reason）。
type Reason string

const (
	ReasonDisabled       Reason = "disabled"        // 策略关闭
	ReasonNotEnough      Reason = "not_enough"      // 消息量不足
	ReasonCircuitBreaker Reason = "circuit_breaker" // 连续失败熔断
	ReasonBelowThreshold Reason = "below_threshold" // 未到阈值
	ReasonAboveThreshold Reason = "above_threshold" // 触发压缩
)

// Decision 压缩判定（含全量计量供观测与审计）。
type Decision struct {
	ShouldCompact   bool
	Reason          Reason
	TokenCount      int
	TokenSource     TokenSource
	EstimatedTokens int
	Threshold       int
	ContextWindow   int
	EffectiveWindow int
	OutputReserve   int
}

// ShouldCompact auto-compact 判定。consecutiveFailures 为此前连续压缩失败次数
// （成功后调用方清零）；ov 非 nil 时 token 口径取 provider 真实用量。
func ShouldCompact(msgs []Message, cfg Config, consecutiveFailures int, ov *TokenOverride) Decision {
	estimated := EstimateTokens(msgs)
	tokenCount, source := estimated, SourceEstimate
	if ov != nil && ov.TokenCount > 0 {
		tokenCount, source = ov.TokenCount, SourceProviderUsage
	}
	d := Decision{
		TokenCount:      tokenCount,
		TokenSource:     source,
		EstimatedTokens: estimated,
		Threshold:       cfg.Threshold(),
		ContextWindow:   cfg.contextWindow(),
		EffectiveWindow: cfg.EffectiveWindow(),
		OutputReserve:   cfg.outputReserve(),
	}
	switch {
	case !cfg.enabled():
		d.Reason = ReasonDisabled
	case !HasEnoughToCompact(msgs):
		d.Reason = ReasonNotEnough
	case consecutiveFailures >= cfg.maxFailures():
		d.Reason = ReasonCircuitBreaker
	case tokenCount < d.Threshold:
		d.Reason = ReasonBelowThreshold
	default:
		d.Reason = ReasonAboveThreshold
		d.ShouldCompact = true
	}
	return d
}

// ---- microcompact ----

// microcompact 常量（对标 ZCode microcompact.ts）。
const (
	// ClearedPlaceholder 被清除工具结果的占位文本（LLM 可感知的诚实形态：
	// 明示内容已清除而非静默吞）。
	ClearedPlaceholder = "[Old tool result content cleared]"
	// DefaultKeepRecent 保留最近 N 组工具结果。
	DefaultKeepRecent = 5
	// DefaultIdle 空闲触发阈值（长会话挂起后旧结果大概率已无引用价值）。
	DefaultIdle = 60 * time.Minute
	// DefaultMinTokenSavings 最小节省门槛（省不了几个 token 就不动转写——
	// 避免为微小收益让会话形态抖动）。
	DefaultMinTokenSavings = 256

	thresholdRatio        = 0.9
	thresholdBufferTokens = 2_000
)

// BuildDefaultThreshold 由 auto-compact 阈值推导 microcompact 阈值：
// min(阈值×0.9, 阈值−2000)，下限 0（先于 auto 触发才有意义）。
func BuildDefaultThreshold(autoCompactThreshold int) int {
	ratio := int(math.Floor(float64(autoCompactThreshold) * thresholdRatio))
	return max(0, min(ratio, autoCompactThreshold-thresholdBufferTokens))
}

// MicroTrigger 触发来源。
type MicroTrigger string

const (
	TriggerIdle   MicroTrigger = "idle"            // 空闲超时
	TriggerTokens MicroTrigger = "token_threshold" // token 压力
)

// MicroReason microcompact 决策原因。
type MicroReason string

const (
	MicroDisabled       MicroReason = "disabled"          // 关闭
	MicroNotTriggered   MicroReason = "not_triggered"     // 无触发（未到阈值且不空闲）
	MicroNoCandidates   MicroReason = "no_candidates"     // 无可清除工具结果
	MicroNothingToClear MicroReason = "nothing_to_clear"  // 组数 ≤ 保留数
	MicroBelowSavings   MicroReason = "below_min_savings" // 节省不足门槛
	MicroApplied        MicroReason = "applied"           // 已清除
)

// MicroConfig microcompact 配置。零值字段落缺省。
type MicroConfig struct {
	// ThresholdTokens ≤0 = 不按 token 触发（仍可 idle 触发）。
	ThresholdTokens int
	// Idle 空闲触发阈值：0 → DefaultIdle；负值禁用 idle 触发。
	Idle time.Duration
	// KeepRecent 保留最近 N 组工具结果（≤0 → DefaultKeepRecent，下限 1）。
	KeepRecent int
	// CompactableTools 可清除的工具名集合：nil = 不限（全部工具结果可清除）。
	// agentkit 无 ZCode 的内置工具名缺省表，生产装配应传实际名单收紧面。
	CompactableTools []string
	// ClearErrorResults 是否清除出错结果（缺省 false——出错结果是排障证据）。
	ClearErrorResults bool
	// MinTokenSavings ≤0 → DefaultMinTokenSavings。
	MinTokenSavings int
	// Enabled nil = 启用。
	Enabled *bool
}

// MicroResult microcompact 结果：Messages 恒为完整转写（未触发时原样返回），
// 计量与清除明细供观测。
type MicroResult struct {
	Reason          MicroReason
	Trigger         MicroTrigger
	Messages        []Message
	TokensBefore    int
	TokensAfter     int
	TokensSaved     int
	ClearedIDs      []string
	ThresholdTokens int
}

// MaybeMicrocompact 轻量压缩：把旧工具结果替换为占位符。lastAssistantDone
// 为最近一次 assistant 完成时刻（零值 = 不评估 idle 触发）。
func MaybeMicrocompact(msgs []Message, cfg MicroConfig, lastAssistantDone, now time.Time) MicroResult {
	before := EstimateTokens(msgs)
	res := MicroResult{TokensBefore: before, Messages: msgs, ThresholdTokens: cfg.ThresholdTokens}
	if cfg.Enabled != nil && !*cfg.Enabled {
		res.Reason = MicroDisabled
		return res
	}
	res.Trigger = microTrigger(cfg, lastAssistantDone, now, before)
	if res.Trigger == "" {
		res.Reason = MicroNotTriggered
		return res
	}

	groups := collectClearableGroups(msgs, cfg)
	clearCount := clearableCount(groups, cfg.KeepRecent)
	if clearCount <= 0 {
		res.Reason = pickEmptyClearReason(groups)
		return res
	}

	out, clearedIDs, saved := applyClears(msgs, groups[:clearCount])
	res.ClearedIDs = clearedIDs
	if saved < minSavingsOf(cfg) {
		// 节省不足门槛：返回原转写，不让会话形态为微小收益抖动
		res.Reason = MicroBelowSavings
		return res
	}
	res.Reason = MicroApplied
	res.Messages = out
	res.TokensAfter = before - saved
	res.TokensSaved = saved
	return res
}

// clearableCount 本轮可清除的组数（总数 − 保留数；≤0 = 不动）。
func clearableCount(groups [][]int, keepRecent int) int {
	keep := keepRecent
	if keep <= 0 {
		keep = DefaultKeepRecent
	}
	return len(groups) - max(1, keep)
}

// pickEmptyClearReason 无可清除时的细分原因：无候选 vs 组数不足保留数。
func pickEmptyClearReason(groups [][]int) MicroReason {
	if len(groups) == 0 {
		return MicroNoCandidates
	}
	return MicroNothingToClear
}

// minSavingsOf 最小节省门槛缺省落位。
func minSavingsOf(cfg MicroConfig) int {
	if cfg.MinTokenSavings <= 0 {
		return DefaultMinTokenSavings
	}
	return cfg.MinTokenSavings
}

// microTrigger 触发判定：idle 优先于 token 压力（空闲场景即使 token 不高也值得清）。
func microTrigger(cfg MicroConfig, lastAssistantDone, now time.Time, tokens int) MicroTrigger {
	if !lastAssistantDone.IsZero() {
		idle := cfg.Idle
		if idle == 0 {
			idle = DefaultIdle
		}
		if idle > 0 && now.Sub(lastAssistantDone) > idle {
			return TriggerIdle
		}
	}
	if cfg.ThresholdTokens > 0 && tokens >= cfg.ThresholdTokens {
		return TriggerTokens
	}
	return ""
}

// applyClears 把待清组的工具结果替换为占位符；节省按被改写条目的前后差增量
// 累计（ΣΔ ≡ before−after，免整表二次估算）。
func applyClears(msgs []Message, clearGroups [][]int) (out []Message, clearedIDs []string, saved int) {
	out = make([]Message, len(msgs))
	copy(out, msgs)
	for _, g := range clearGroups {
		for _, idx := range g {
			was := msgTokens(msgs[idx])
			out[idx].Text = ClearedPlaceholder
			saved += was - msgTokens(out[idx])
			clearedIDs = append(clearedIDs, msgs[idx].ToolCallID)
		}
	}
	return out, clearedIDs, max(0, saved)
}

// collectClearableGroups 收集可清除工具结果的分组：组 = 同一 assistant 轮次
// 内的连续工具结果（随轮次整体清除）；孤儿结果（前文无带调用的 assistant）
// 单独成组。跳过：非白名单工具（白名单非空时）、出错结果（未开清除）、已清除。
func collectClearableGroups(msgs []Message, cfg MicroConfig) [][]int {
	allow := map[string]bool{}
	for _, n := range cfg.CompactableTools {
		allow[n] = true
	}
	var groups [][]int
	current := []int(nil)
	flush := func() {
		if len(current) > 0 {
			groups = append(groups, current)
		}
		current = nil
	}
	for i, m := range msgs {
		if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
			flush()
			current = []int{}
			continue
		}
		if m.Role != RoleTool || m.ToolCallID == "" || m.ToolName == "" {
			continue
		}
		if len(allow) > 0 && !allow[m.ToolName] {
			continue
		}
		if m.IsError && !cfg.ClearErrorResults {
			continue
		}
		if m.Text == ClearedPlaceholder {
			continue
		}
		if current == nil {
			groups = append(groups, []int{i}) // 孤儿结果单独成组
			continue
		}
		current = append(current, i)
	}
	flush()
	return groups
}
