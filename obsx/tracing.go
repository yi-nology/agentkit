// Package obsx 可观测性扩展：对齐 eino callbacks 体系的 LLM 调用追踪。
//
// 核心组件 TracingHandler 是 eino callbacks.Handler 的实现——通过
// callbacks.InitCallbacks(ctx, nil, handler) 注入后，eino 各组件
// （ChatModel 等）在调用时自动触发 OnStart/OnEnd/OnError，无需侵入业务代码：
//
//	ctx = obsx.InitLLMObservability(ctx, log, obsx.Options{}) // 一行启用
//	msg, err := client.Generate(ctx, "R1", msgs)               // 自动产出结构化 trace
//
// 日志字段对齐可审计需求：stage（业务阶段）、component/model（哪个模型）、
// duration、prompt/completion tokens（优先真实 usage）、慢调用告警。
package obsx

import (
	"context"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/yi-nology/agentkit/textutil"
	"git.enjoye.top/enjoydream/ekit/concurrency/async"
	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

// stageCtxKey 业务阶段（R1/R2/...）在 ctx 中的键。
// llm 包在每次 Generate 前注入；TracingHandler 读取后随 trace 落日志。
type stageCtxKey struct{}

// WithStage 在 ctx 上标记业务阶段。
func WithStage(ctx context.Context, stage string) context.Context {
	return context.WithValue(ctx, stageCtxKey{}, stage)
}

// StageFromContext 读取业务阶段（未标记返回空串）。
func StageFromContext(ctx context.Context) string {
	v, _ := ctx.Value(stageCtxKey{}).(string)
	return v
}

// Options TracingHandler 配置。
type Options struct {
	// SlowThreshold 慢调用阈值：超过以 Warn 级落日志（默认 30s；0 = 不告警）。
	SlowThreshold time.Duration
	// PreviewLen 输入/输出消息内容预览长度（默认 0 = 不落内容，只落长度；
	// 生产建议 0——消息可能含用户代码/凭证）。
	PreviewLen int
}

// TokenUsageOf 模型回调输出的真实 token 用量提取（全仓单源）：
// 优先 out.TokenUsage；compose 图节点对裸 ChatModel 只透传 Message 时回退读
// ResponseMeta.Usage（缺该回退会静默漏采——v0.10.11 前仅 llm/usage 侧有，
// obsx 侧漏采已修）。llm.NewUsageHandler 与 TracingHandler 共用本函数。
func TokenUsageOf(out *model.CallbackOutput) *model.TokenUsage {
	if out == nil {
		return nil
	}
	if out.TokenUsage != nil {
		return out.TokenUsage
	}
	if out.Message != nil && out.Message.ResponseMeta != nil && out.Message.ResponseMeta.Usage != nil {
		u := out.Message.ResponseMeta.Usage
		return &model.TokenUsage{
			PromptTokens: u.PromptTokens,
			PromptTokenDetails: model.PromptTokenDetails{
				CachedTokens:     u.PromptTokenDetails.CachedTokens,
				CacheWriteTokens: u.PromptTokenDetails.CacheWriteTokens,
			},
			CompletionTokens:        u.CompletionTokens,
			TotalTokens:             u.TotalTokens,
			CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens},
		}
	}
	return nil
}

// TracingHandler eino 追踪 handler 的配置（经 NewTracingHandler 构建为 callbacks.Handler）。
type TracingHandler struct {
	log logx.Logger
	opt Options
}

// NewTracingHandler 创建 eino 追踪 handler（经 HandlerBuilder 组装）。
// v0.12.1 起补 OnEndWithStreamOutput（第九轮审计 C 级：流式只触发该时机，
// 仅注册 OnEnd 的追踪在 agentrun 默认开流下 llm.call.end/slow 永不产出、
// duration/tokens 全盲）——流式分支异步排空副本流取末帧 usage 后落 end
// （同步排空会把真流式阻塞成全量批出，与 llm.NewUsageHandler 同修法）。
// 组件过滤：非 ChatModel 组件（Tool/Lambda/Graph）的回调不产 llm.call.*——
// 注入点在 run 边界时工具节点同样触发回调，此前混入 LLM 错误率与日志。
func NewTracingHandler(log logx.Logger, opt Options) callbacks.Handler {
	h := &TracingHandler{log: log, opt: opt}
	return callbacks.NewHandlerBuilder().
		OnStartFn(h.onStart).
		OnEndFn(h.onEnd).
		OnErrorFn(h.onError).
		OnEndWithStreamOutputFn(h.onStreamEnd).
		Build()
}

// isModelCall 组件过滤：只认 ChatModel（eino 官方契约：ConvCallbackInput
// 返回 nil 即非模型调用应跳过——但 Lambda 的 []*schema.Message 输入也会被
// 包装成功，故显式判组件）。
func isModelCall(info *callbacks.RunInfo) bool {
	return info != nil && info.Component == components.ComponentOfChatModel
}

func (h *TracingHandler) onStart(ctx context.Context, info *callbacks.RunInfo,
	input callbacks.CallbackInput) context.Context {

	if !isModelCall(info) {
		return ctx
	}
	state := startState{start: time.Now(), stage: StageFromContext(ctx), model: modelOf(info)}
	// 真实模型名优先（第十轮审计落地：eino-ext 回调 Config.Model 携带构造
	// 配置的真实模型名；info.Type 只是实现标识（"OpenAI"），多模型混跑不可区分）
	if in := model.ConvCallbackInput(input); in != nil && in.Config != nil && in.Config.Model != "" {
		state.model = in.Config.Model
	}
	h.log.Info("llm.call.start",
		"stage", state.stage,
		"component", compOf(info),
		"model", state.model,
		"n_messages", nMessages(input))
	return context.WithValue(ctx, startStateKey{}, state)
}

func (h *TracingHandler) onEnd(ctx context.Context, info *callbacks.RunInfo,
	output callbacks.CallbackOutput) context.Context {

	if !isModelCall(info) {
		return ctx
	}
	state, _ := ctx.Value(startStateKey{}).(startState)
	dur := h.durationSince(state)
	if out := model.ConvCallbackOutput(output); out != nil && out.Config != nil && out.Config.Model != "" {
		state.model = out.Config.Model
	}
	fields := []any{
		"stage", state.stage,
		"component", compOf(info),
		"model", state.model,
		"duration_ms", dur.Milliseconds(),
	}
	if out := model.ConvCallbackOutput(output); out != nil {
		if usage := TokenUsageOf(out); usage != nil {
			fields = append(fields,
				"prompt_tokens", usage.PromptTokens,
				"completion_tokens", usage.CompletionTokens,
				"total_tokens", usage.TotalTokens,
				"reasoning_tokens", usage.CompletionTokensDetails.ReasoningTokens)
		}
		if out.Message != nil && h.opt.PreviewLen > 0 {
			fields = append(fields, "output_preview", preview(out.Message.Content, h.opt.PreviewLen))
		}
	}
	if h.opt.SlowThreshold > 0 && dur > h.opt.SlowThreshold {
		h.log.Warn("llm.call.slow", fields...)
		return ctx
	}
	h.log.Info("llm.call.end", fields...)
	return ctx
}

func (h *TracingHandler) onError(ctx context.Context, info *callbacks.RunInfo,
	err error) context.Context {

	if !isModelCall(info) {
		return ctx
	}
	state, _ := ctx.Value(startStateKey{}).(startState)
	if state.model == "" {
		state.model = modelOf(info) // OnStart 缺失的解耦注入形态兜底
	}
	h.log.Warn("llm.call.error",
		"stage", state.stage,
		"component", compOf(info),
		"model", state.model,
		"duration_ms", h.durationSince(state).Milliseconds(),
		"error", err.Error())
	return ctx
}

// onStreamEnd 流式收束：异步排空框架给的副本流（MUST Close），取末帧 usage
// 与 finish 后复用非流式 onEnd 的落日志路径。start 计时点为 OnStart——
// 排空完成时刻即全量生成完成时刻，duration 语义与非流式一致。
func (h *TracingHandler) onStreamEnd(ctx context.Context, info *callbacks.RunInfo,
	output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {

	if !isModelCall(info) {
		output.Close()
		return ctx
	}
	// 异步：eino 分发同步 + Stream() 在本时机返回后才交流给调用方——同步
	// 排空会把真流式阻塞成批出（第九轮审计 C 级，与 llm.NewUsageHandler 同修）
	async.GoSafe(func() {
		defer output.Close()
		var usage *model.TokenUsage // last-non-nil（第十轮审计：无 usage 的尾帧
		// 会把已捕获的 usage 覆写成 nil——观测面与记账面矛盾）
		var modelName string
		for {
			v, err := output.Recv()
			if err != nil {
				break
			}
			switch t := v.(type) {
			case *model.CallbackOutput:
				if t != nil {
					if u := TokenUsageOf(t); u != nil {
						usage = u
					}
					if t.Config != nil && t.Config.Model != "" {
						modelName = t.Config.Model
					}
				}
			case *schema.Message:
				if t == nil {
					continue
				}
				if u := TokenUsageOf(&model.CallbackOutput{Message: t}); u != nil {
					usage = u
				}
			}
		}
		// usage 缺失（未开 IncludeUsage 的 provider）也落 end——与非流式 onEnd
		// 行为一致，start/end 配对不破（第十轮审计）
		h.logUsageEnd(ctx, info, usage, modelName)
	})
	return ctx
}

// logUsageEnd 落 llm.call.end / llm.call.slow（流式/非流式共用）。usage 可
// nil（provider 未回传时缺 token 字段，但 end/slow 与 duration 仍产出）。
func (h *TracingHandler) logUsageEnd(ctx context.Context, info *callbacks.RunInfo, usage *model.TokenUsage, modelName string) {
	state, _ := ctx.Value(startStateKey{}).(startState)
	if modelName == "" {
		modelName = state.model
	}
	if modelName == "" {
		modelName = modelOf(info)
	}
	dur := h.durationSince(state)
	fields := []any{
		"stage", state.stage,
		"component", compOf(info),
		"model", modelName,
		"duration_ms", dur.Milliseconds(),
	}
	if usage != nil {
		fields = append(fields,
			"prompt_tokens", usage.PromptTokens,
			"completion_tokens", usage.CompletionTokens,
			"total_tokens", usage.TotalTokens,
			"reasoning_tokens", usage.CompletionTokensDetails.ReasoningTokens)
	}
	if h.opt.SlowThreshold > 0 && dur > h.opt.SlowThreshold {
		h.log.Warn("llm.call.slow", fields...)
		return
	}
	h.log.Info("llm.call.end", fields...)
}

// durationSince 计算自 OnStart 以来的耗时；OnStart 缺失（handler 在调用链中段
// 注入导致无配对）时 state.start 为零值——直接 time.Since 会产出 1.7 万年的
// 天文数字并误触慢调用告警，这里跳过计时。
func (h *TracingHandler) durationSince(state startState) time.Duration {
	if state.start.IsZero() {
		return 0
	}
	return time.Since(state.start)
}

// startState OnStart → OnEnd/OnError 的调用内状态。
type startState struct {
	start time.Time
	stage string
	model string // onStart 解析的真实模型名（Config.Model > info.Type 回退）
}

type startStateKey struct{}

// InitLLMObservability 一步启用：注入 TracingHandler 到 ctx。
// 之后该 ctx 链上的所有 eino 组件调用（ChatModel Generate 等）自动产出 trace。
func InitLLMObservability(ctx context.Context, log logx.Logger, opt Options) context.Context {
	return callbacks.InitCallbacks(ctx, nil, NewTracingHandler(log, opt))
}

func compOf(info *callbacks.RunInfo) string {
	if info == nil {
		return ""
	}
	return string(info.Component)
}

func modelOf(info *callbacks.RunInfo) string {
	if info == nil {
		return ""
	}
	return info.Type
}

func nMessages(input callbacks.CallbackInput) int {
	if in := model.ConvCallbackInput(input); in != nil {
		return len(in.Messages)
	}
	return 0
}

// preview 内容预览（textutil.TruncEllipsis 单源）。
func preview(s string, n int) string {
	return textutil.TruncEllipsis(s, n)
}
