// 上下文窗口治理（对标 ZCode compact/microcompact + context-usage-breakdown）：
// BaseChatModel 装饰器，发送前输入自守恒，三级递进——
//
//	L1 microcompact：更早的工具结果内容置换占位（保最近 keepRecentTools 条不动；
//	  只替换内容、不动消息条数与 tool_call/tool_result 配对，语法安全）；
//	  清理量不足 minClearSavings 时放弃（不值得损失信息）；
//	L2 截最长 user 消息（与 llm.Client.fitInput 同策略，TruncNote 留痕）；
//	L3 病理态兜底：迭代截最大工具结果直至入窗。
//
// 触发线与估算口径对齐 llm fitInput：rune 计数，limit=(窗口-输出预留)×2×0.9。
// 真实 usage 校准（对标 ZCode tokenSource=provider_usage）：llm.UsageHandler 侧
// 经 RecordUsage 回灌每模型最近一次真实 prompt tokens，与发送时 rune 数配对出
// tokens/char 系数——估算系统性偏低的模型（中文为主）自动收紧有效预算。
// 观测：每次拟合结果经 OnFit 上报（passthrough 应占绝对多数），上下文构成
// 分解经 OnBreakdown 按 role 分桶上报，接入方自行接 Prometheus/日志。
package wrap

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/yi-nology/agentkit/textutil"
)

// ---------- 真实 usage 校准注册表（进程级全局单槽） ----------
//
// 为什么单槽而非按模型分桶：callbacks 侧用量记录的 Model 是组件类型（RunInfo.Type，
// 非真实模型名），与装配期配置名对不上；且拟合只发生在直调旁路（Generate 链有
// Client.fitInput 且记账走 Client.OnUsage 不进本注册表），发送 rune 数与回调
// usage 天然同源配对。多模型分桶待有真实需求再开。

var (
	calibLock  sync.Mutex
	calibUsage int // 最近一次真实 prompt tokens（UsageHandler 回灌）
	calibChars int // 最近一次发送 rune 总数（FitModel 发送时记录）
)

// RecordUsage 回灌真实 prompt tokens（接入方的 UsageHandler 回调侧调用；
// 单源不双计——只更新校准寄存器，不动记账）。model 参数预留分桶扩展，当前忽略。
func RecordUsage(model string, promptTokens int) {
	if promptTokens <= 0 {
		return
	}
	calibLock.Lock()
	defer calibLock.Unlock()
	calibUsage = promptTokens
}

func recordSentChars(chars int) {
	calibLock.Lock()
	defer calibLock.Unlock()
	calibChars = chars
}

// tokensPerChar 最近一次可配对的真实估算系数（tokens/rune）；样本不足返回 0。
// 系数异常（<0.02 或 >4）视为脏样本不采信——防止单次异常响应带偏预算。
func tokensPerChar() float64 {
	calibLock.Lock()
	defer calibLock.Unlock()
	if calibUsage <= 0 || calibChars <= 0 {
		return 0
	}
	f := float64(calibUsage) / float64(calibChars)
	if f < 0.02 || f > 4 {
		return 0
	}
	return f
}

// ---------- 拟合内核（纯函数，不改调用方切片与消息） ----------

const (
	// keepRecentRounds microcompact 保留最近 N 个 assistant 轮的工具结果组
	//（compact 同款组粒度：组 = 一个 assistant(带 ToolCalls) 之后连续的工具结果）。
	keepRecentRounds = 5
	// minClearSavings 清理量门槛（256 token 当量，口径 1 token≈2 rune）：
	// 不值得损失信息的清理不做。
	minClearSavings = 256 * 2
	// toolPlaceholder 被清理工具结果的占位文案（固定短文案，可测试可预期）。
	toolPlaceholder = "[旧工具结果内容已清理：microcompact]"
	truncNote       = "输入超出模型窗口预算，已截断留痕"
)

// isProtectedResult 错误/提醒结果保护（compact「出错结果默认保留」同款语义）：
// 携带提醒信封（含 tool-error 标记）的结果是模型自纠与规则遵循的依据，不清理。
func isProtectedResult(content string) bool {
	return strings.Contains(content, "<system-reminder>") || strings.Contains(content, "【tool-error】")
}

// toolGroups 划分工具结果轮次组：一个 assistant（带 ToolCalls）开启一轮，其后
// 连续的 Tool 消息属该组。返回每组的消息下标切片（按出现序）。孤立工具结果
// （无所属 assistant 轮）不入组——保守不清理。
func toolGroups(msgs []*schema.Message) (groups [][]int) {
	for i := 0; i < len(msgs); {
		if msgs[i].Role == schema.Assistant && len(msgs[i].ToolCalls) > 0 {
			i++
			var g []int
			for i < len(msgs) && msgs[i].Role == schema.Tool {
				g = append(g, i)
				i++
			}
			if len(g) > 0 {
				groups = append(groups, g)
			}
			continue
		}
		i++
	}
	return groups
}

// FitAction 拟合动作（观测标签；passthrough 应占绝对多数）。
type FitAction string

const (
	FitPass       FitAction = "passthrough"  // 未触发
	FitMicro      FitAction = "microcompact" // 仅 L1
	FitTruncated  FitAction = "truncated"    // L2/L3（无 L1 或 L1 不足）
	FitMicroTrunc FitAction = "micro+trunc"  // L1+L2/L3
)

// FitResult 拟合结果（rune 口径）。
type FitResult struct {
	Action  FitAction
	Before  int // 拟合前总 rune
	After   int
	Limit   int // 生效字符预算
	Cleared int // L1 清理的工具结果条数
}

// FitMessages 两级拟合纯内核。limitChars ≤0 或未超限时原样返回（同一切片，
// 不复制——零开销直通路径）。
func FitMessages(msgs []*schema.Message, limitChars int) ([]*schema.Message, FitResult) {
	res := FitResult{Action: FitPass}
	if limitChars <= 0 || len(msgs) == 0 {
		return msgs, res
	}
	res.Limit = limitChars
	total := runeTotal(msgs)
	res.Before = total
	if total <= limitChars {
		res.After = total
		return msgs, res
	}

	// L1 microcompact（轮次组粒度）：按 assistant 轮分组，更早轮的
	// 工具结果清内容（保最近 keepRecentRounds 轮）；错误/提醒结果保护不清理。
	// sizes 缓存整趟消息的 rune 数，免每层重复 len([]rune()) 分配（热路径）。
	out := copyMsgs(msgs)
	sizes := msgRunes(out)
	saved, cleared := 0, 0
	groups := toolGroups(out)
	if len(groups) > keepRecentRounds {
		for _, g := range groups[:len(groups)-keepRecentRounds] {
			for _, i := range g {
				l := sizes[i]
				if l <= len(toolPlaceholder) || isProtectedResult(out[i].Content) {
					continue
				}
				cloned := *out[i] // 消息结构克隆替换：浅拷贝切片仍共享元素指针，直改会污染调用方
				cloned.Content = toolPlaceholder
				out[i] = &cloned
				sizes[i] = utf8.RuneCountInString(toolPlaceholder)
				saved += l - sizes[i]
				cleared++
			}
		}
	}
	// 清理量不足则放弃 L1（不值得损失信息）；无论走否，out 自此都是私有副本，
	// 后续 L2/L3 的写入不会污染调用方切片。
	if saved >= minClearSavings {
		total -= saved
		res.Cleared = cleared
	} else {
		out = copyMsgs(msgs)
		sizes = msgRunes(out)
	}

	// L2 截最长 user 消息（llm fitInput 同款：保头尾，TruncNote 留痕）
	truncated := false
	if total > limitChars {
		longest := -1
		for i, m := range out {
			if m.Role == schema.User && (longest < 0 || sizes[i] > sizes[longest]) {
				longest = i
			}
		}
		if longest >= 0 {
			l := sizes[longest]
			// 注记余量 32：llm fitInput 的 keep 不扣注记开销（截完仍略超限），
			// 本处两级递进需 L2 后真正入窗，否则 L3 会被这 ~20 rune 噪声无谓触发
			keep := limitChars - (total - l) - 32
			if keep < 0 {
				keep = 0
			}
			if keep < l {
				cloned := *out[longest]
				cloned.Content = "\n" + textutil.TruncNote(out[longest].Content, keep, truncNote)
				nl := utf8.RuneCountInString(cloned.Content)
				total -= l - nl
				sizes[longest] = nl
				out[longest] = &cloned
				truncated = true
			}
		}
	}

	// L3 病理态兜底：迭代截最大（非保护）工具结果直至入窗（或无工具结果可截）。
	// 保护语义全级别一致：错误/提醒结果不参与截断——极端情形宁可超限报错，
	// 也不丢模型自纠依据。
	for total > limitChars {
		big := -1
		for i, m := range out {
			if m.Role == schema.Tool && !isProtectedResult(m.Content) &&
				(big < 0 || sizes[i] > sizes[big]) {
				big = i
			}
		}
		if big < 0 {
			break
		}
		l := sizes[big]
		keep := l - (total - limitChars) - 64 // 64：截断注记余量
		cloned := *out[big]
		if keep <= 0 {
			cloned.Content = toolPlaceholder
		} else {
			cloned.Content = textutil.TruncNote(out[big].Content, keep, truncNote)
		}
		nl := utf8.RuneCountInString(cloned.Content)
		total -= l - nl
		sizes[big] = nl
		out[big] = &cloned
		truncated = true
	}

	res.After = total
	switch {
	case res.Cleared > 0 && !truncated:
		res.Action = FitMicro
	case res.Cleared > 0:
		res.Action = FitMicroTrunc
	case truncated:
		res.Action = FitTruncated
	default:
		res.Action = FitPass // 超限但无可裁对象（无 user 无工具结果），尽力直通
	}
	return out, res
}

func runeTotal(msgs []*schema.Message) int {
	n := 0
	for _, m := range msgs {
		n += utf8.RuneCountInString(m.Content)
	}
	return n
}

// msgRunes 一次扫描得到每条消息的 rune 数（FitMessages 多层共享，免重复分配）。
func msgRunes(msgs []*schema.Message) []int {
	sizes := make([]int, len(msgs))
	for i, m := range msgs {
		sizes[i] = utf8.RuneCountInString(m.Content)
	}
	return sizes
}

// copyMsgs 浅拷贝切片（元素指针复用；修改内容时另行克隆消息结构）。
func copyMsgs(msgs []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, len(msgs))
	copy(out, msgs)
	return out
}

// ---------- BaseChatModel 装饰器 ----------

// FitModel 输入自守恒装饰器：Generate/Stream 前做两级拟合，窗口 ≤0 时直通。
// 无可变状态（校准在包级注册表），可跨任务共享。
type FitModel struct {
	einomodel.BaseChatModel
	Stage         string // 观测标签（chat/R3/QA/...）
	ModelName     string // 校准键（与 UsageHandler rec.Model 同源）
	WindowTokens  int    // 模型上下文窗口（token）；≤0 = 直通
	MaxOutputSize int    // 输出预留（token）；≤0 = 窗口/20
	// OnFit 拟合事件回调（nil 安全；仅非 passthrough 触发）。
	OnFit func(stage string, res FitResult)
	// OnBreakdown 上下文构成分解回调（nil 安全）：每次发送按 role 分桶累计
	// 本次输入量（rune）——回答「token 花在哪」。接入方自行接指标系统。
	OnBreakdown func(stage string, byRole map[string]int)
}

var _ einomodel.ToolCallingChatModel = (*FitModel)(nil)

// NewFitModel 包装 inner；WindowTokens ≤0 时保持直通（不改行为）。
func NewFitModel(inner einomodel.BaseChatModel, stage, modelName string, windowTokens, maxOutputTokens int, onFit func(string, FitResult)) *FitModel {
	return &FitModel{
		BaseChatModel: inner,
		Stage:         stage,
		ModelName:     modelName,
		WindowTokens:  windowTokens,
		MaxOutputSize: maxOutputTokens,
		OnFit:         onFit,
	}
}

func (f *FitModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	return f.BaseChatModel.Generate(ctx, f.fit(input), opts...)
}

func (f *FitModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return f.BaseChatModel.Stream(ctx, f.fit(input), opts...)
}

// WithTools 工具绑定透传：绑定产物再包一层 FitModel——ReAct 绑定工具后拟合不失效。
func (f *FitModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, ok := f.BaseChatModel.(einomodel.ToolCallingChatModel)
	if !ok {
		return nil, fmt.Errorf("ctxfit: 内层模型不支持工具绑定 stage=%s type=%T", f.Stage, f.BaseChatModel)
	}
	nb, err := bound.WithTools(tools)
	if err != nil {
		return nil, err
	}
	c := *f
	c.BaseChatModel = nb
	return &c, nil
}

// fit 发送前拟合：有效预算 = (窗口-输出预留)×2×0.9（口径同 llm fitInput），
// 真实 usage 校准系数只紧不松；同时回灌发送 rune 数供下一轮校准配对。
func (f *FitModel) fit(msgs []*schema.Message) []*schema.Message {
	if f.WindowTokens <= 0 || len(msgs) == 0 {
		return msgs
	}
	reserved := f.MaxOutputSize
	if reserved <= 0 {
		reserved = f.WindowTokens / 20
	}
	limit := (f.WindowTokens - reserved) * 2 * 9 / 10
	if limit <= 0 {
		return msgs
	}
	if fc := tokensPerChar(); fc > 0 {
		if tight := int(float64(f.WindowTokens-reserved) * 0.9 / fc); tight < limit {
			limit = tight
		}
	}
	out, res := FitMessages(msgs, limit)
	recordSentChars(res.Before)
	if f.OnBreakdown != nil {
		f.reportBreakdown(msgs)
	}
	if f.OnFit != nil && res.Action != FitPass {
		f.OnFit(f.Stage, res)
	}
	return out
}

// reportBreakdown 上下文构成分解（对标 ZCode context-usage-breakdown）：按 role
// 分桶累计本次发送量（rune）。FitModel 已在发送路径上，顺手分桶零额外遍历成本。
func (f *FitModel) reportBreakdown(msgs []*schema.Message) {
	bucket := map[string]int{}
	for _, m := range msgs {
		bucket[string(m.Role)] += len([]rune(m.Content))
	}
	f.OnBreakdown(f.Stage, bucket)
}
