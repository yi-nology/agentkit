// Package agentrun ReAct agent 运行样板：封装 eino ADK 的
// ChatModelAgent + Runner 构造、事件流 drain、最终文本提取与失败重试。
//
// 从 Argus 的 R3 执行模式提炼——任何"单 agent 带工具自主循环"的场景
// 都可以直接用，不必重写 ADK 样板：
//
//	out, err := agentrun.Run(ctx, agentrun.Config{
//	    Name:        "reviewer",
//	    Instruction: instruction,
//	    Model:       chatModel,           // llm.Generator.RawModel()
//	    Tools:       tools,               // eino 工具表（mcp/rag/skill...）
//	    MaxIterations: 12,
//	}, query)
//
// ReAct 出口判定：assistant 消息且不带 tool_calls 即最终答复；
// MaxIterations 耗尽仍未出口 → 报错（调用方可决定降级）。
package agentrun

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/yi-nology/agentkit/llm"
)

// 默认迭代上限（ADK ReAct 循环轮数；防失控循环）。
const DefaultMaxIterations = 12

// Config ReAct agent 运行配置。
type Config struct {
	// Name agent 名（trace/日志标识）。
	Name string
	// Description 角色描述（进 agent 元信息）。
	Description string
	// Instruction 系统提示词（含方法论/工具策略等注入产物）。
	Instruction string
	// Model eino ChatModel（llm.Generator 的 RawModel()）。
	Model model.BaseChatModel
	// Tools 工具表（建议经 toolprior.Table.Ordered(ctx) 产出）。
	// 与 ToolsFactory 二选一；两者都设置时 ToolsFactory 优先。
	Tools []tool.BaseTool
	// ToolsFactory 工具表工厂：每次 run 调用新建一份工具表。
	// WithRetry 场景建议设置——工具表含 toolprior.LimitCalls 等
	// 有状态包装时，复用同一实例会让限流计数跨重试累计（重试继承 0 余额，
	// 每次调用立即被拒）。工厂内每次重新包装即可让预算按尝试重置。
	ToolsFactory func() []tool.BaseTool
	// MaxIterations ReAct 循环轮数上限（默认 12）。
	MaxIterations int
	// RetryAfterMutation 变更类工具已执行后仍允许整体重试（默认 false=守卫生效：
	// 首轮调过变更类工具后失败不再重跑——重复副作用风险，如实上抛交调用方降级）。
	// 只读/幂等工具场景可置 true 恢复无条件重试。
	RetryAfterMutation bool
	// DisableStreaming 关闭流式（默认 false=开）：开流时思考/正文以 *_delta 增量事件
	// 外发（真流式观测面），每轮收流后仍按完整消息走既有派发与终稿判定——消费方
	// 语义不变，只多出增量通道。模型/网关不兼容流式时的逃生开关。
	DisableStreaming bool
	// OnUsage 流式调用的每轮用量回调（可选）：开流后 eino callbacks OnEnd 拿到的是
	// StreamReader（无法在回调侧消费取 Usage），由 reactHandler 收流 concat 后从
	// ResponseMeta 提取并回调，每轮一次。非流式路径不触发（消费方沿用既有
	// callbacks 用量面板，两路不会双计）。ctx 为 run 的调用 ctx（归因取值用）。
	OnUsage func(ctx context.Context, u Usage)
}

// Usage 单轮 LLM 调用用量（流式 OnUsage 回调载荷；字段与 eino TokenUsage 同义）。
type Usage struct {
	PromptTokens     int
	CachedTokens     int
	CompletionTokens int
	ReasoningTokens  int
	TotalTokens      int
	FinishReason     string
}

// Event agent 运行过程事件（OnEvent 回调载荷，观测/进度展示用）。
type Event struct {
	Type   string // reasoning | reasoning_delta | text | text_delta | tool_call | tool_result
	Text   string // *_delta 时为增量分片（非累积快照）
	Tool   string // tool_call/tool_result 的工具名
	Args   string // tool_call 的 JSON 参数串
	CallID string // tool_call/tool_result 的原生调用 ID（声明 tc.ID / 结果 ToolCallID）——观测面精确配对依据
}

// 事件类型词表。
// 注意与 acpx 包事件词表（text/tool_call/tool_result）互为平行词汇：
// 两侧有意保持同名字面量以便消费方对译；改动任一侧词表时同步检查另一侧。
//
// 流式语义（默认开流）：reasoning_delta/text_delta 在生成过程中逐分片到达
// （真流式），同轮收束后仍发完整 reasoning/text 快照（权威口径，回放/对账以它为
// 准；增量只服务实况渲染）。消费方以「同轮快照到达即吸收增量缓冲」去重——
// 中间**调用轮**无 text 快照（终稿窗口才有），其 text_delta 缓冲以该轮的
// tool_call 事件为吸收锚（调用轮前导正文不构成终稿；reasoning 逐轮有快照），
// 拼接口径=各轮窗口正文按序拼接、终稿窗口与 EventText 快照严格一致（第八轮
// 审计钉住的词表契约）。
const (
	EventReasoning      = "reasoning"
	EventText           = "text"
	EventToolCall       = "tool_call"
	EventToolResult     = "tool_result"
	EventReasoningDelta = "reasoning_delta"
	EventTextDelta      = "text_delta"
)

// Validate 校验配置必需项。
func (c Config) Validate() error {
	if c.Model == nil {
		return fmt.Errorf("agentrun: Model 不能为空")
	}
	if c.Instruction == "" {
		return fmt.Errorf("agentrun: Instruction 不能为空")
	}
	return nil
}

func (c Config) maxIterations() int {
	if c.MaxIterations > 0 {
		return c.MaxIterations
	}
	return DefaultMaxIterations
}

// RunOption Run 入口的可选行为（v0.10.38 破坏式收敛：此前 Run/RunWithEvents/
// RunWithRetry/RunWithEventsAndRetry 四个导出入口层层包裹——retryQuery 是
// 独立参数而 RetryAfterMutation 却在 Config，签名不对称；入口唯一后行为
// 正交组合，守卫逻辑只此一份）。
type RunOption func(*runOpts)

type runOpts struct {
	onEvent    func(Event)
	retryQuery string
}

// WithOnEvent 过程事件回调（可多传，末个生效；nil 回调等价不传）。
// 契约：回调在事件 drain 循环内同步执行——不得阻塞（阻塞会拖停整轮 agent）、
// 不得 panic（会击穿调用方 goroutine，与 acpx OnEvent 同契约；事件已固化，
// 回调内消费失败不影响事件面完整性，自行 recover）。
func WithOnEvent(fn func(Event)) RunOption {
	return func(o *runOpts) { o.onEvent = fn }
}

// WithRetry 失败回喂重试一次：首次失败（或产出空文本）时以 retryQuery 再跑，
// 两次均失败返回末次错误。副作用守卫：首轮已调用变更类工具（见
// IsMutatingTool）后不整体重跑，除非 Config.RetryAfterMutation=true——重跑会
// 重复副作用（脚本执行/服务操作类工具在首轮已生效）。
// ⚠ retryQuery 是**完整替换** query 而非追加（2026-09-19 bianque 实弹教训：
// 消费者若传纯提示语，重试轮将丢失全部任务材料——工单/合议上下文全空，模型
// 输出「未收到输入」类空心合规报告）。消费方应传 query+提示语的拼接串。
// 注意：两次尝试共用 cfg.Tools 实例——工具表含 toolprior.LimitCalls 等有状态
// 包装时，限流计数会跨尝试累计；需要按尝试重置预算请设置 ToolsFactory。
func WithRetry(retryQuery string) RunOption {
	return func(o *runOpts) { o.retryQuery = retryQuery }
}

// Run 执行一次 ReAct 查询，返回最终 assistant 文本；过程事件与失败重试经
// RunOption 正交组合（WithOnEvent / WithRetry）。
func Run(ctx context.Context, cfg Config, query string, opts ...RunOption) (string, error) {
	var o runOpts
	for _, opt := range opts {
		opt(&o)
	}
	if o.retryQuery == "" {
		return run(ctx, cfg, query, o.onEvent)
	}
	return runWithRetry(ctx, cfg, query, o.retryQuery, o.onEvent)
}

// runWithRetry 失败回喂重试核心（Run + WithRetry 的执行体；重试过程可观测——
// 观测事件照常全量回调，副作用守卫见 WithRetry）。
func runWithRetry(ctx context.Context, cfg Config, query, retryQuery string, onEvent func(Event)) (string, error) {
	// Retry-After sink（批次四十五）：传输层捕获 429 服务端建议（Retry-After 头），
	// 供重跑前的等待决策。
	ctx = llm.WithRetryAfterSink(ctx)
	out, mutating, err := runWithMeta(ctx, cfg, query, onEvent)
	if err == nil {
		return out, nil
	}
	if mutating != "" && !cfg.RetryAfterMutation {
		return "", mutationSkipErr(mutating, err)
	}
	// 服务端退避建议优先（钳制 ≤5min，ctx 取消即止）：429 时端点知道限流窗口还剩
	// 多久——对仍在限流窗口的端点立即重跑只会再吃一个 429（对标 ZCode runner-retry
	// 的服务端建议优先语义）。建议超出钳制（>5min 且长于本地曲线）= 超长限流窗口，
	// 原地等待与立即重跑都没有意义——返回首轮错误交调用方/failover 处置（此前
	// SelectRetryDelay 返回 0 时跳过等待但照常立即重跑，恰是注释论证过要避免的
	// 动作；第六轮审计）。
	if hint := llm.RetryAfterFrom(ctx); hint > 0 {
		if wait := llm.SelectRetryDelay(0, hint); wait > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}
		} else {
			return "", err // 建议超钳制：超长限流窗口，不原地重跑
		}
	}
	return run(ctx, cfg, retryQuery, onEvent)
}

// run 核心：构造 ADK agent → Runner.Query → drain 事件流。
func run(ctx context.Context, cfg Config, query string, onEvent func(Event)) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	tools := cfg.Tools
	if cfg.ToolsFactory != nil {
		tools = cfg.ToolsFactory()
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          cfg.Name,
		Description:   cfg.Description,
		Instruction:   cfg.Instruction,
		Model:         cfg.Model,
		MaxIterations: cfg.maxIterations(),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
		},
	})
	if err != nil {
		return "", fmt.Errorf("agentrun: 构造 agent 失败: %w", err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: !cfg.DisableStreaming})
	iter := runner.Query(ctx, query)

	h := &reactHandler{ctx: ctx, onEvent: onEvent, onUsage: cfg.OnUsage, toolCalled: map[string]string{}}
	err = drainEvents(iter, "agent", h.handle)
	if err != nil {
		return "", err
	}
	if !h.sawFinal {
		return "", fmt.Errorf("agentrun: agent 未产出最终文本（iterations=%d）", cfg.maxIterations())
	}
	if strings.TrimSpace(h.finalText) == "" {
		return "", fmt.Errorf("agentrun: agent 最终答复为空（iterations=%d）", cfg.maxIterations())
	}
	return h.finalText, nil
}

// reactHandler ReAct 事件流消费：工具声明/结果/思考/最终答复分路派发。
type reactHandler struct {
	ctx        context.Context
	onEvent    func(Event)
	onUsage    func(context.Context, Usage)
	toolCalled map[string]string // tool_call id → 工具名（回填 tool_result）
	finalText  string
	sawFinal   bool
}

func (h *reactHandler) handle(mv *adk.MessageVariant) error {
	if mv == nil {
		return nil
	}
	// 流式消息：先消费分片流发增量事件（真流式观测面），收流后以完整消息走下方
	// 既有派发逻辑——tool_calls 派发/终稿判定/最终文本事件语义与关流时完全一致。
	if mv.IsStreaming && mv.MessageStream != nil {
		full, err := h.streamDeltas(mv)
		if err != nil {
			return err
		}
		mv.Message = full
		mv.IsStreaming = false
		mv.MessageStream = nil
		// 流式用量（OnUsage）：callbacks OnEnd 在流式下拿不到 TokenUsage（StreamReader
		// 无法在回调侧消费），concat 后 ResponseMeta 自带——每轮一次，非流式不触发。
		if h.onUsage != nil && full.ResponseMeta != nil && full.ResponseMeta.Usage != nil {
			u := full.ResponseMeta.Usage
			h.onUsage(h.ctx, Usage{
				PromptTokens:     u.PromptTokens,
				CachedTokens:     u.PromptTokenDetails.CachedTokens,
				CompletionTokens: u.CompletionTokens,
				ReasoningTokens:  u.CompletionTokensDetails.ReasoningTokens,
				TotalTokens:      u.TotalTokens,
				FinishReason:     full.ResponseMeta.FinishReason,
			})
		}
	}
	if mv.Message == nil {
		return nil
	}
	for _, tc := range mv.Message.ToolCalls {
		h.toolCalled[tc.ID] = tc.Function.Name
	}
	// 工具结果消息：回填 tool_result 事件（观测/进度展示需要工具返回）
	if mv.Role == schema.Tool {
		if h.onEvent != nil {
			h.onEvent(Event{Type: EventToolResult, CallID: mv.Message.ToolCallID,
				Tool: h.toolCalled[mv.Message.ToolCallID], Text: mv.Message.Content})
		}
		return nil
	}
	if mv.Role != schema.Assistant {
		return nil
	}
	// 思考过程先于动作/答复（同一 assistant 消息内 reasoning_content 先产出）
	if h.onEvent != nil && mv.Message.ReasoningContent != "" {
		h.onEvent(Event{Type: EventReasoning, Text: mv.Message.ReasoningContent})
	}
	// ReAct 出口判定：assistant 且无 tool_calls 即最终答复——
	// 空内容也记录（部分推理型模型会有空最终消息），错误文案区分"空答复"与"没答复"
	if len(mv.Message.ToolCalls) == 0 {
		h.sawFinal = true
		h.finalText = mv.Message.Content
		if h.onEvent != nil && h.finalText != "" {
			h.onEvent(Event{Type: EventText, Text: h.finalText})
		}
		return nil
	}
	// 中间过程：tool_calls 声明 → 工具调用事件（含参数与原生 ID，观测面需要看到调用命令并精确配对）
	if h.onEvent != nil {
		for _, tc := range mv.Message.ToolCalls {
			h.onEvent(Event{Type: EventToolCall, CallID: tc.ID, Tool: tc.Function.Name, Args: tc.Function.Arguments})
		}
	}
	return nil
}

// streamDeltas 消费流式消息：assistant 的思考/正文以 *_delta 增量事件逐分片外发；
// 携带 tool_calls 分片的正文不外发（那是调用轮的片段拼接，非可读答复；其前导正文
// 分片不携带 tool_calls，会先行外发）。tool 角色结果流不外发增量，静默收流。
// 返回 concat 后的完整消息，交回既有完整消息逻辑。
func (h *reactHandler) streamDeltas(mv *adk.MessageVariant) (*schema.Message, error) {
	var chunks []*schema.Message
	for {
		chunk, err := mv.MessageStream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			mv.MessageStream.Close()
			return nil, fmt.Errorf("agentrun: 流式消息接收失败: %w", err)
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		if h.onEvent == nil || mv.Role != schema.Assistant {
			continue
		}
		if chunk.ReasoningContent != "" {
			h.onEvent(Event{Type: EventReasoningDelta, Text: chunk.ReasoningContent})
		}
		if chunk.Content != "" && len(chunk.ToolCalls) == 0 {
			h.onEvent(Event{Type: EventTextDelta, Text: chunk.Content})
		}
	}
	if len(chunks) == 0 {
		return &schema.Message{Role: mv.Role}, nil
	}
	full, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("agentrun: 流式消息合并失败: %w", err)
	}
	return full, nil
}

// drainEvents ADK 事件流消费骨架（run 与 PlanAndExecute 共用）：迭代 → 空事件跳过 →
// 事件错误包装中止 → 非消息输出跳过 → 业务处理交给 handle（返回错误同样中止上抛）。
// 底层事件通道是 UnboundedChan（生产者不阻塞），提前返回不会泄漏。
func drainEvents(iter *adk.AsyncIterator[*adk.AgentEvent], errKind string, handle func(mv *adk.MessageVariant) error) error {
	return drainEventsFull(iter, errKind, func(event *adk.AgentEvent) error {
		if event.Output == nil || event.Output.MessageOutput == nil {
			return nil
		}
		return handle(event.Output.MessageOutput)
	})
}

// drainEventsFull drainEvents 的整事件形态（P&E 消费 BreakLoop 等非消息事件）。
func drainEventsFull(iter *adk.AsyncIterator[*adk.AgentEvent], errKind string, handle func(event *adk.AgentEvent) error) error {
	for {
		event, ok := iter.Next()
		if !ok {
			return nil
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			return fmt.Errorf("agentrun: %s 事件错误: %w", errKind, event.Err)
		}
		if err := handle(event); err != nil {
			return err
		}
	}
}
