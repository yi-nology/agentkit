// usage.go — LLM 用量采集（eino callbacks）——**全仓唯一的 callbacks 侧记账
// 出口**：拿得到 Cached/Reasoning tokens、FinishReason、Duration、Iteration。
// 归因经 Labels（map）透传，领域键由调用方决定。
// （v0.10.12 前 obsx.Options.OnUsage 是并行的五数字出口，防重护栏跨包放在
// obsx——现护栏归位于本包，obsx 回归纯 trace，ReAct/RawModel 旁路记账统一
// 走 NewUsageHandler 注入 callbacks。）
package llm

import (
	"context"
	"git.enjoye.top/enjoydream/ekit/concurrency/async"
	"github.com/cloudwego/eino/schema"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"

	"github.com/yi-nology/agentkit/obsx"
)

// UsageRecord 单次 LLM 调用的用量记录。
type UsageRecord struct {
	Model            string
	PromptTokens     int
	CachedTokens     int
	CompletionTokens int
	ReasoningTokens  int
	TotalTokens      int
	DurationMS       int64
	FinishReason     string
	Iteration        int64             // 本作用域第几次 LLM 调用（1 起；无计数器为 0）
	Labels           map[string]string // 归因标签（session/case/step/agent 等，调用方自定）
}

// Sink 用量出口。
type Sink func(UsageRecord)

type labelsKey struct{}

// WithUsageLabels ctx 注入归因标签；不注入或无标签时 UsageRecord.Labels 为 nil。
func WithUsageLabels(ctx context.Context, labels map[string]string) context.Context {
	if len(labels) == 0 {
		return ctx
	}
	// 拷贝防调用方后续修改。
	cp := make(map[string]string, len(labels))
	for k, v := range labels {
		cp[k] = v
	}
	return context.WithValue(ctx, labelsKey{}, cp)
}

func labelsFrom(ctx context.Context) map[string]string {
	m, _ := ctx.Value(labelsKey{}).(map[string]string)
	return m
}

// UsageCallCounter 单作用域 LLM 调用轮次计数（原子递增）。
type UsageCallCounter struct{ n atomic.Int64 }

type counterKey struct{}

// WithCallCounter ctx 挂轮次计数器（每个 agent 运行作用域一个）。
func WithCallCounter(ctx context.Context) context.Context {
	return context.WithValue(ctx, counterKey{}, &UsageCallCounter{})
}

type startKey struct{}

// clientAccountingCtxKey 标记「本次模型调用的 token 记账由 Client 侧负责」
// （Client.OnUsage/Budget 已配置，generateRetry 打点）：NewUsageHandler 检测到
// 标记跳过发射——同一物理调用的 Generate 路径与 callbacks 路径只记一次账。
type clientAccountingCtxKey struct{}

func withClientAccounting(ctx context.Context) context.Context {
	return context.WithValue(ctx, clientAccountingCtxKey{}, true)
}

func clientAccounted(ctx context.Context) bool {
	v, _ := ctx.Value(clientAccountingCtxKey{}).(bool)
	return v
}

// NewUsageHandler 构建 eino callbacks.Handler（非流式 OnEnd + 流式
// OnEndWithStreamOutput——流式分支在后台 goroutine 排空副本流后回调，
// ⚠ onRecord 可能被多 goroutine 并发调用，须并发安全（第十轮审计钉约）。
// OnEndWithStreamOutput——第八轮审计补齐：eino-ext 流式只触发后者，仅注册
// OnEnd 的 handler 在开流（agentrun v0.10.32 起默认）下一条记录都不产生，
// 静默失明。流式分支消费框架给的副本流取末个非 nil usage，末帧即汇总帧）。
// 无 TokenUsage、无 RunInfo 的调用静默跳过；Labels 缺省不阻断采集。
// 防重：Client 自身配置了 OnUsage/Budget 时（Client 记账标记已在 ctx），
// 本 handler 对同一次物理调用自动跳过——两侧不会双倍记账，可同时启用。
// ⚠ agentrun 场景：Config.OnUsage 与本 handler 记同一笔——**择一装配**
// （两者没有共享防重标记，同时挂会双计）。
func NewUsageHandler(onRecord Sink) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
			// 已有起点（嵌套包装二次触发 OnStart）不覆盖：保留最外层起点。
			if _, ok := ctx.Value(startKey{}).(time.Time); ok {
				return ctx
			}
			return context.WithValue(ctx, startKey{}, time.Now())
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if rec, ok := usageRecordFrom(ctx, info, output); ok && onRecord != nil {
				onRecord(rec)
			}
			return ctx
		}).
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
			if onRecord == nil || clientAccounted(ctx) || info == nil || info.Type == "" {
				// 不消费也必须关副本（eino 契约：handler 副本不关整条管道泄漏）
				output.Close()
				return ctx
			}
			// **异步消费**（第九轮审计 C 级）：eino 的 handler 分发是同步的、
			// Stream() 在 OnEndWithStreamOutput 返回后才把流交给调用方——
			// 此前同步排空副本流会把真流式阻塞成「全量生成完才返回」（首分片
			// 延迟=全程生成时间，v0.10.32 起默认开流的 text_delta 全部积压）。
			// ctx/info 为只读值，goroutine 内读取安全；Iteration 为 atomic。
			async.GoSafe(func() {
				defer output.Close()
				usage, _ := obsx.DrainStreamUsage(output)
				if usage == nil {
					return
				}
				onRecord(recordFromUsage(ctx, info, usage, ""))
			})
			return ctx
		}).
		Build()
}

// recordFromUsage 已知 usage 的记录构造（非流式/流式两路共用单源）。
func recordFromUsage(ctx context.Context, info *callbacks.RunInfo, usage *model.TokenUsage, finish string) UsageRecord {
	rec := UsageRecord{
		Model:            info.Type,
		PromptTokens:     usage.PromptTokens,
		CachedTokens:     usage.PromptTokenDetails.CachedTokens,
		CompletionTokens: usage.CompletionTokens,
		ReasoningTokens:  usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:      usage.TotalTokens,
		Labels:           labelsFrom(ctx),
	}
	if start, ok := ctx.Value(startKey{}).(time.Time); ok && !start.IsZero() {
		rec.DurationMS = time.Since(start).Milliseconds()
	}
	if c, ok := ctx.Value(counterKey{}).(*UsageCallCounter); ok {
		rec.Iteration = c.n.Add(1)
	}
	rec.FinishReason = finish
	return rec
}

// usageRecordFrom 从 OnEnd 上下文与回调输出提取用量记录。
// ok=false 表示应静默跳过（Client 已记账 / 无 RunInfo / 无真实用量）。
func usageRecordFrom(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) (UsageRecord, bool) {
	// Client 侧已记账（OnUsage/Budget 配置时 generateRetry 打点）→ 跳过。
	if clientAccounted(ctx) {
		return UsageRecord{}, false
	}
	// 只认带 RunInfo 的 OnEnd，否则 token 双计、iteration 虚增。
	if info == nil || info.Type == "" {
		return UsageRecord{}, false
	}
	out := model.ConvCallbackOutput(output)
	if out == nil {
		return UsageRecord{}, false
	}
	// 真实用量提取（含 compose 只透传 Message 的回退）单源 obsx.TokenUsageOf。
	usage := obsx.TokenUsageOf(out)
	if usage == nil {
		return UsageRecord{}, false
	}
	finish := ""
	if out.Message != nil && out.Message.ResponseMeta != nil {
		finish = out.Message.ResponseMeta.FinishReason
	}
	return recordFromUsage(ctx, info, usage, finish), true
}
