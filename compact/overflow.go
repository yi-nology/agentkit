// 溢出自适应与熔断（compact 二期，对标 ZCode compact-selection.ts +
// turn-loop-state.ts）：压缩请求本身 prompt-too-long 时按 provider 报的 token
// gap 逐次上移保留组数；压缩后 <N 个工具轮次又满连续 M 次判 rapid-refill 熔断
// （某个文件/工具输出过大，继续压缩只是原地打转）。
package compact

import (
	"fmt"
	"regexp"
	"strconv"
)

// MaxOverflowRetries 压缩请求本身溢出的自适应重试上限（对标
// MAX_COMPACT_PROMPT_TOO_LONG_RETRIES）。
const MaxOverflowRetries = 3

// RapidRefillToolTurnThreshold 判 rapid-refill 的工具轮次阈值：压缩后少于
// 该轮次又满，计入连续 rapid-refill。
const RapidRefillToolTurnThreshold = 3

// MaxConsecutiveRapidRefills 连续 rapid-refill 熔断阈值。
const MaxConsecutiveRapidRefills = 3

// ErrRapidRefill rapid-refill 熔断错误（不可继续压缩——指引排查大输出源）。
type ErrRapidRefill struct {
	ConsecutiveRapidRefills int
	ToolTurnsSinceCompact   int
}

func (e *ErrRapidRefill) Error() string {
	return fmt.Sprintf("compact: rapid-refill 熔断（连续 %d 次，压缩后仅 %d 个工具轮次即满——"+
		"大概率存在单条超大文件读/工具输出，应排查输出源头而非继续压缩）",
		e.ConsecutiveRapidRefills, e.ToolTurnsSinceCompact)
}

// RapidRefillDecision rapid-refill 评估结果。
type RapidRefillDecision struct {
	ConsecutiveRapidRefills int
	ShouldBlock             bool
	ToolTurnsSinceCompact   int
}

// EvaluateRapidRefill 在每次「又触发压缩」时评估：距上次压缩不足
// RapidRefillToolTurnThreshold 个工具轮次 → 连续计数 +1；否则清零。
// tracking 传上次记录（nil = 无历史）。
func EvaluateRapidRefill(toolTurnsSinceCompact, consecutiveRapidRefills int) RapidRefillDecision {
	if toolTurnsSinceCompact < RapidRefillToolTurnThreshold {
		consecutiveRapidRefills++
	} else {
		consecutiveRapidRefills = 0
	}
	return RapidRefillDecision{
		ConsecutiveRapidRefills: consecutiveRapidRefills,
		ShouldBlock:             consecutiveRapidRefills >= MaxConsecutiveRapidRefills,
		ToolTurnsSinceCompact:   toolTurnsSinceCompact,
	}
}

// gapMatcher 单形态提取器：sub 匹配后计算超差。
type gapMatcher struct {
	re    *regexp.Regexp
	first bool // true: m[1]=maximum、m[2]=requested（gap=m2−m1）；false: 反之
}

// tokenGapMatchers provider 溢出报文的两类形态（OpenAI / Anthropic 系）。
var tokenGapMatchers = []gapMatcher{
	// OpenAI: This model's maximum context length is 128000 tokens. However, you requested 130500 tokens
	{regexp.MustCompile(`maximum context length is (\d+) tokens\D{0,40}?requested (\d+) tokens`), true},
	// Anthropic: prompt is too long: 201329 tokens > 200000 maximum（requested 在前）
	{regexp.MustCompile(`prompt is too long: (\d+) tokens > (\d+) maximum`), false},
}

// TokenGap 从 provider 的 prompt-too-long 错误报文提取超差 token 数
// （requested − maximum 或 本身 − 上限）。未识别形态返回 false——调用方
// 走保守降级（按比例丢弃）而非瞎猜。
func TokenGap(cause error) (int, bool) {
	if cause == nil {
		return 0, false
	}
	msg := cause.Error()
	for _, g := range tokenGapMatchers {
		if m := g.re.FindStringSubmatch(msg); len(m) == 3 {
			a, err1 := strconv.Atoi(m[1])
			b, err2 := strconv.Atoi(m[2])
			if err1 != nil || err2 != nil {
				continue
			}
			if g.first && b > a { // m1=maximum m2=requested
				return b - a, true
			}
			if !g.first && a > b { // m1=requested m2=maximum
				return a - b, true
			}
		}
	}
	return 0, false
}

// GroupRounds 按 assistant 轮次分组：每个 assistant 消息开启新组，组前消息
// （用户输入/上一轮工具结果）归入本组；首组之前的头部消息自成一组的开头。
// 分组是压缩/保留的最小单位——半组保留会让工具调用与结果断链。
func GroupRounds(msgs []Message) [][]Message {
	var groups [][]Message
	var current []Message
	for _, m := range msgs {
		if m.Role == "assistant" {
			if len(current) > 0 {
				groups = append(groups, current)
			}
			current = []Message{m}
			continue
		}
		current = append(current, m)
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// Selection 压缩切分产物：ForSummary 交摘要（更早），Preserved 原样保留（最近）。
type Selection struct {
	ForSummary []Message
	Preserved  []Message
	// GroupsPreserved 保留的组数（0 = 全量摘要）。
	GroupsPreserved int
	TotalGroups     int
}

// Select 切分：保留最近 preserveGroups 组原文（下限 0、上限 len(groups)−1——
// 至少一组进摘要，否则无物可压）。preserveGroups≤0 = 全量摘要。
func Select(msgs []Message, preserveGroups int) Selection {
	groups := GroupRounds(msgs)
	total := len(groups)
	preserve := max(0, min(preserveGroups, total-1))
	keep := groups[total-preserve:]
	var sum, pres []Message
	for _, g := range groups[:total-preserve] {
		sum = append(sum, g...)
	}
	for _, g := range keep {
		pres = append(pres, g...)
	}
	return Selection{ForSummary: sum, Preserved: pres, GroupsPreserved: preserve, TotalGroups: total}
}

// SelectAfterOverflow 压缩请求本身 prompt-too-long 后的自适应重选：按 token
// gap 估算还要多保留几组（保留得越多、进摘要的越少、摘要请求越小），逐次上移。
// currentPreserved 为本次失败选择已保留的组数。无法再收紧（已到上限/组数不足/
// 摘要侧不足 2 组）返回 nil——调用方走 TruncateForRetry 或放弃。
func SelectAfterOverflow(msgs []Message, cause error, currentPreserved int) *Selection {
	groups := GroupRounds(msgs)
	if len(groups) < 2 {
		return nil
	}
	maxPreserve := len(groups) - 1
	if currentPreserved >= maxPreserve {
		return nil
	}
	// 摘要侧的组（要覆盖 gap 的是这些组——把它们移去保留即缩小摘要请求）
	summaryGroups := groups[:len(groups)-currentPreserved]
	move := 1 // 无 gap 信息时保守移 1 组
	if gap, ok := TokenGap(cause); ok {
		move = countGroupsToCover(summaryGroups, gap)
	}
	next := min(maxPreserve, currentPreserved+move)
	if next <= currentPreserved {
		return nil
	}
	sel := Select(msgs, next)
	if !HasEnoughToCompact(sel.ForSummary) {
		return nil
	}
	return &sel
}

// TruncateForRetry 摘要素材的直接丢弃重试（SelectAfterOverflow 也到头时）：
// 从摘要素材头部按 gap 覆盖丢最旧组（无 gap 按 20% 丢），保底留 1 组。
// attempt 从 1 起，超过 MaxOverflowRetries 返回 nil（放弃）。
func TruncateForRetry(entries []Message, cause error, attempt int) []Message {
	if attempt > MaxOverflowRetries {
		return nil
	}
	groups := GroupRounds(entries)
	if len(groups) < 2 {
		return nil
	}
	drop := max(1, len(groups)/5) // 20%
	if gap, ok := TokenGap(cause); ok {
		covered, n := 0, 0
		for _, g := range groups {
			covered += EstimateTokens(g)
			n++
			if covered >= gap {
				break
			}
		}
		drop = max(1, n)
	}
	drop = min(drop, len(groups)-1)
	if drop < 1 {
		return nil
	}
	var out []Message
	for _, g := range groups[drop:] {
		out = append(out, g...)
	}
	return out
}

// countGroupsToCover 从最新往旧数需要几组才能覆盖 gap（对标
// countRecentGroupsToCoverTokenGap：覆盖不完退半量，保底 1）。
func countGroupsToCover(groups [][]Message, gap int) int {
	if gap <= 0 || len(groups) == 0 {
		return 0
	}
	covered, n := 0, 0
	for i := len(groups) - 1; i >= 0; i-- {
		covered += EstimateTokens(groups[i])
		n++
		if covered >= gap {
			break
		}
	}
	if n >= len(groups)-1 {
		return max(1, len(groups)/2)
	}
	return max(1, n)
}
