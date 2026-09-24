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

	"github.com/yi-nology/agentkit/textutil"
	"git.enjoye.top/enjoydream/ekit/observability/logx"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
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
			PromptTokens:            u.PromptTokens,
			PromptTokenDetails:      model.PromptTokenDetails{CachedTokens: u.PromptTokenDetails.CachedTokens},
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

// NewTracingHandler 创建 eino 追踪 handler（经 HandlerBuilder 组装——
// 未注册流式时机由 builder 置空，Needed() 声明只消费非流式三时机）。
func NewTracingHandler(log logx.Logger, opt Options) callbacks.Handler {
	h := &TracingHandler{log: log, opt: opt}
	return callbacks.NewHandlerBuilder().
		OnStartFn(h.onStart).
		OnEndFn(h.onEnd).
		OnErrorFn(h.onError).
		Build()
}

func (h *TracingHandler) onStart(ctx context.Context, info *callbacks.RunInfo,
	input callbacks.CallbackInput) context.Context {

	state := startState{start: time.Now(), stage: StageFromContext(ctx)}
	h.log.Info("llm.call.start",
		"stage", state.stage,
		"component", compOf(info),
		"model", modelOf(info),
		"n_messages", nMessages(input))
	return context.WithValue(ctx, startStateKey{}, state)
}

func (h *TracingHandler) onEnd(ctx context.Context, info *callbacks.RunInfo,
	output callbacks.CallbackOutput) context.Context {

	state, _ := ctx.Value(startStateKey{}).(startState)
	dur := h.durationSince(state)
	fields := []any{
		"stage", state.stage,
		"component", compOf(info),
		"model", modelOf(info),
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

	state, _ := ctx.Value(startStateKey{}).(startState)
	h.log.Warn("llm.call.error",
		"stage", state.stage,
		"component", compOf(info),
		"model", modelOf(info),
		"duration_ms", h.durationSince(state).Milliseconds(),
		"error", err.Error())
	return ctx
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
