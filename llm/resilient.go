// Package llm 弹性 LLM 客户端：多模型降级链 + 熔断 + 预算短路 + 窗口自适应。
//
// 降级决策矩阵：
//   - 429 rate limit   → 不重试同模型，立即切换（不同模型限速池独立）
//   - 5xx/网络/超时     → 同模型快速重试，仍败切换
//   - context too long → 不重试同模型，切换（大窗口备选可能救）
//   - 输出截断          → 提升 MaxTokens 重试，仍败切换
//   - 401/403          → 不重试同模型，切换（各 Provider 独立 APIKey）
//   - ctx 取消/预算耗尽 → 全链中止
package llm

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/yi-nology/agentkit/breaker"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/time/rate"
)

// Generator LLM 生成接口：*Client 与 *Resilient 都满足，调用方无感切换。
type Generator interface {
	Generate(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, error)
	GenerateJSON(ctx context.Context, stage string, msgs []*schema.Message, out any) error
	// UsedTokens 返回该客户端累计消耗的 token 数（无预算绑定时返回 0）。
	UsedTokens() int
	// RawModel 返回底层 eino 模型（供 ReAct agent 等需要裸模型的场景；
	// Resilient 返回主模型——降级仅覆盖 Generate/GenerateJSON 路径）。
	RawModel() model.BaseChatModel
}

// 编译期断言。
var (
	_ Generator = (*Client)(nil)
	_ Generator = (*Resilient)(nil)
)

// BudgetInjector 可选接口：支持运行时注入任务级预算（Dispatcher fan-out 场景）。
// 调用方通过类型断言检测；不实现则预算保持构造时的值。
type BudgetInjector interface {
	WithBudget(b TokenAccountant) Generator
}

// BudgetHolder 可选接口：暴露 Generator 绑定的预算累计器。
// 聚合方（StageRouter.UsedTokens）按其指针身份去重——各链共享同一 Budget 时
// 逐链求和会按链数倍增。
type BudgetHolder interface {
	BoundBudget() TokenAccountant
}

// WithBudget Client 的预算注入：浅拷贝（Client 只含无锁值字段，Budget 指针共享是有意为之）。
func (c *Client) WithBudget(b TokenAccountant) Generator {
	c2 := *c
	c2.Budget = b
	return &c2
}

// WithBudget Resilient 的预算注入：显式构造新实例（Resilient 含互斥字段不可浅拷贝）。
// chain/breakers 共享原实例（熔断状态跨任务全局），Budget/Tracker 指向任务级。
func (r *Resilient) WithBudget(b TokenAccountant) Generator {
	return &Resilient{
		chain:      r.chain,
		cfg:        r.cfg,
		breakers:   r.breakers,
		Budget:     b,
		Limiter:    r.Limiter,
		Tracker:    r.Tracker,
		OnUsage:    r.OnUsage,
		OnFallback: r.OnFallback,
	}
}

// Attempt 单模型尝试记录（AttemptError 的组成部分）。
type Attempt struct {
	Provider string
	Model    string
	Err      string
	Duration time.Duration
}

// AttemptError 全部模型失败的聚合错误（含每个模型的失败原因链）。
type AttemptError struct {
	Stage    string
	Attempts []Attempt
}

func (e *AttemptError) Error() string {
	var parts []string
	for _, a := range e.Attempts {
		parts = append(parts, fmt.Sprintf("%s(%s): %s", a.Provider, a.Model, a.Err))
	}
	return fmt.Sprintf("llm: %s 全部 %d 个模型失败: %s", e.Stage, len(e.Attempts), strings.Join(parts, " → "))
}

// ResilientConfig 弹性客户端配置。
type ResilientConfig struct {
	// RetriesPerModel 每模型重试次数（不含首次），默认 2。
	RetriesPerModel int
	// BreakerTrip 模型熔断阈值（连续失败次数），默认 3。
	BreakerTrip int
	// BreakerCooldown 模型熔断冷却期，默认 5 分钟。
	BreakerCooldown time.Duration
	// BaseDelay 重试退避基准，默认 2s（测试可调小）。
	BaseDelay time.Duration
	// MaxDelay 重试退避上限，默认 30s。
	MaxDelay time.Duration
}

func (c *ResilientConfig) fillDefaults() {
	if c.RetriesPerModel <= 0 {
		c.RetriesPerModel = 2
	}
	if c.BreakerTrip <= 0 {
		c.BreakerTrip = breaker.DefaultTripThreshold
	}
	if c.BreakerCooldown <= 0 {
		c.BreakerCooldown = breaker.DefaultCooldown
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = 2 * time.Second
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 30 * time.Second
	}
}

// timeoutProvider 可选接口：Provider 可声明单次尝试超时。
type timeoutProvider interface {
	AttemptTimeout() time.Duration
}

// Resilient 弹性 LLM 客户端。
//
// 与 *Client 方法签名一致（满足 Generator），可作 drop-in 替换。
// 额外能力：多模型降级、按模型熔断、预算短路、每模型窗口自适应 fitInput。
type Resilient struct {
	chain    *FallbackChain
	cfg      ResilientConfig
	breakers *breaker.Breakers

	// Budget 任务 token 预算（nil = 不限）。耗尽时全链短路。
	Budget TokenAccountant
	// Limiter 全局限速器（所有模型共享；nil = 不限速）。
	Limiter *rate.Limiter
	// Tracker 成本追踪器（nil = 不追踪）。记账跟随实际执行的模型。
	Tracker *CostTracker
	// OnUsage token 使用回调（model = 实际执行的模型）。
	OnUsage func(model, stage string, prompt, completion int)
	// OnFallback 降级事件回调（from = 失败模型，to = 切换到的模型）。
	OnFallback func(from, to, stage, reason string)

	mu        sync.Mutex
	lastModel string // 最近一次成功调用使用的模型
}

// NewResilient 创建弹性客户端。空 Provider 列表报错。
func NewResilient(chain *FallbackChain, cfg ResilientConfig) (*Resilient, error) {
	if chain == nil || len(chain.Providers) == 0 {
		return nil, fmt.Errorf("llm: Resilient 需要至少一个 Provider")
	}
	cfg.fillDefaults()
	return &Resilient{
		chain:    chain,
		cfg:      cfg,
		breakers: breaker.NewBreakers(cfg.BreakerTrip, cfg.BreakerCooldown),
	}, nil
}

// LastModel 返回最近一次成功调用使用的模型名（无成功调用返回空）。
func (r *Resilient) LastModel() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastModel
}

// String 返回降级链描述（如 "openai(gpt-4o) → openai(deepseek-v4)"）。
func (r *Resilient) String() string {
	names := make([]string, len(r.chain.Providers))
	for i, p := range r.chain.Providers {
		names[i] = fmt.Sprintf("%s(%s)", p.Name(), p.ModelName())
	}
	return strings.Join(names, " → ")
}

// PrimaryModel 返回主模型的 BaseChatModel（供 eino agent 工具表使用）。
func (r *Resilient) PrimaryModel() model.BaseChatModel {
	if p := r.chain.Primary(); p != nil {
		return p.Model()
	}
	return nil
}

// RawModel Generator 接口实现：返回主模型。
func (r *Resilient) RawModel() model.BaseChatModel { return r.PrimaryModel() }

// UsedTokens Generator 接口实现：返回预算累计消耗。
func (r *Resilient) UsedTokens() int {
	if r.Budget != nil {
		return r.Budget.Used()
	}
	return 0
}

// BoundBudget 返回绑定的预算累计器（可 nil）。BudgetHolder 接口实现。
func (r *Resilient) BoundBudget() TokenAccountant { return r.Budget }

// Generate 弹性生成：按链序尝试各 Provider，全部失败返回 *AttemptError。
func (r *Resilient) Generate(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, error) {
	out, _, err := r.generateWithTrace(ctx, stage, msgs)
	return out, err
}

// GenerateJSON 弹性 JSON 生成：每模型内部含解析失败回喂重试，仍败切换下一模型。
func (r *Resilient) GenerateJSON(ctx context.Context, stage string, msgs []*schema.Message, out any) error {
	_, err := r.generateJSONWithTrace(ctx, stage, msgs, out)
	return err
}

// generateWithTrace 带尝试链的生成核心。
func (r *Resilient) generateWithTrace(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, []Attempt, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if r.Budget != nil && r.Budget.Remaining() <= 0 {
		return nil, nil, fmt.Errorf("llm: %s 任务 token 预算已耗尽（%d）", stage, r.Budget.Used())
	}

	var attempts []Attempt
	breakerSkipped := 0
	for i, p := range r.chain.Providers {
		// 预算逐 provider 复查：截断记账后本链内仍会继续烧，越早短路越省钱
		if r.Budget != nil && r.Budget.Remaining() <= 0 {
			return nil, attempts, fmt.Errorf("llm: %s 任务 token 预算已耗尽（%d）", stage, r.Budget.Used())
		}
		// 熔断中的模型直接跳过（不记录为 attempt——没真正尝试）
		if !r.breakers.Allow(p.ModelName()) {
			breakerSkipped++
			continue
		}
		// nil model 防御：Allow 已放行（可能占用半开探测配额），必须配对 Failure
		if isNilModel(p.Model()) {
			r.breakers.Failure(p.ModelName())
			attempts = append(attempts, Attempt{Provider: p.Name(), Model: p.ModelName(), Err: "provider.Model() 为 nil"})
			continue
		}

		start := time.Now()
		out, err := r.tryOneProvider(ctx, p, stage, msgs)
		if err == nil {
			r.breakers.Success(p.ModelName())
			r.rememberModel(p.ModelName())
			return out, attempts, nil
		}
		// ctx 取消：用户主动中止，不再尝试其他模型。
		// 这次尝试确实失败了，仍要配对 Failure——否则半开探测被提前返回吃掉，
		// 该模型被永久逐出降级链
		r.breakers.Failure(p.ModelName())
		if ctx.Err() != nil {
			return nil, attempts, ctx.Err()
		}
		attempts = append(attempts, Attempt{
			Provider: p.Name(), Model: p.ModelName(),
			Err: err.Error(), Duration: time.Since(start),
		})
		if r.OnFallback != nil {
			// to = 降级链上的下一个模型（链尾为空串）
			to := ""
			if i+1 < len(r.chain.Providers) {
				to = r.chain.Providers[i+1].ModelName()
			}
			r.OnFallback(p.ModelName(), to, stage, err.Error())
		}
	}

	// 全部被熔断跳过时 attempts 为空——"全部 0 个模型失败"极具误导性
	if len(attempts) == 0 {
		if breakerSkipped > 0 {
			return nil, nil, fmt.Errorf("llm: %s 全部 %d 个模型均处于熔断冷却中，未发起任何尝试", stage, breakerSkipped)
		}
		return nil, nil, fmt.Errorf("llm: %s 降级链为空", stage)
	}
	return nil, attempts, &AttemptError{Stage: stage, Attempts: attempts}
}

// generateJSONWithTrace 带尝试链的 JSON 生成核心。
func (r *Resilient) generateJSONWithTrace(ctx context.Context, stage string, msgs []*schema.Message, out any) ([]Attempt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.Budget != nil && r.Budget.Remaining() <= 0 {
		return nil, fmt.Errorf("llm: %s 任务 token 预算已耗尽（%d）", stage, r.Budget.Used())
	}

	var attempts []Attempt
	breakerSkipped := 0
	for i, p := range r.chain.Providers {
		// 预算逐 provider 复查（同 generateWithTrace）
		if r.Budget != nil && r.Budget.Remaining() <= 0 {
			return attempts, fmt.Errorf("llm: %s 任务 token 预算已耗尽（%d）", stage, r.Budget.Used())
		}
		if !r.breakers.Allow(p.ModelName()) {
			breakerSkipped++
			continue
		}
		// nil model 防御：Allow 已放行（可能占用半开探测配额），必须配对 Failure
		if isNilModel(p.Model()) {
			r.breakers.Failure(p.ModelName())
			attempts = append(attempts, Attempt{Provider: p.Name(), Model: p.ModelName(), Err: "provider.Model() 为 nil"})
			continue
		}

		start := time.Now()
		err := r.tryOneProviderJSON(ctx, p, stage, msgs, out)
		if err == nil {
			r.breakers.Success(p.ModelName())
			r.rememberModel(p.ModelName())
			return attempts, nil
		}
		// ctx 取消路径同样配对 Failure（防止半开探测被吃掉，见 generateWithTrace）
		r.breakers.Failure(p.ModelName())
		if ctx.Err() != nil {
			return attempts, ctx.Err()
		}
		attempts = append(attempts, Attempt{Provider: p.Name(), Model: p.ModelName(), Err: err.Error(), Duration: time.Since(start)})
		if r.OnFallback != nil {
			to := ""
			if i+1 < len(r.chain.Providers) {
				to = r.chain.Providers[i+1].ModelName()
			}
			r.OnFallback(p.ModelName(), to, stage, err.Error())
		}
	}

	if len(attempts) == 0 {
		if breakerSkipped > 0 {
			return nil, fmt.Errorf("llm: %s 全部 %d 个模型均处于熔断冷却中，未发起任何尝试", stage, breakerSkipped)
		}
		return nil, fmt.Errorf("llm: %s 降级链为空", stage)
	}
	return attempts, &AttemptError{Stage: stage, Attempts: attempts}
}

// tryOneProvider 单 Provider 内部：复制 msgs（防 fitInput 污染调用方）→
// 按该 Provider 窗口裁剪 → 重试循环（每次尝试独立受 AttemptTimeout 约束）。
func (r *Resilient) tryOneProvider(ctx context.Context, p Provider, stage string, msgs []*schema.Message) (*schema.Message, error) {
	// 复制切片：fitInput 会原地替换元素，不能污染调用方的消息
	msgsCopy := copyMsgs(msgs)

	c := r.buildClient(p)
	c.fitInput(msgsCopy)

	// 单次尝试超时：每次尝试独立计时。不能在循环外创建一次——否则 deadline
	// 覆盖全部重试 + 退避睡眠，首次尝试耗满后重试全部形同虚设
	attemptTimeout := time.Duration(0)
	if tp, ok := p.(timeoutProvider); ok {
		attemptTimeout = tp.AttemptTimeout()
	}

	maxRetries := r.cfg.RetriesPerModel + 1
	var lastErr error
	truncatedBoosted := false
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// 同模型重试仅限"值得在同模型上重试"的错误：
			// 429（限速池独立）与 context 超限/401（确定性失败）直接跳出，切换下一模型
			if !sameModelRetryable(lastErr) {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(r.backoff(attempt, lastErr)):
			}
		}
		if r.Limiter != nil {
			if err := r.Limiter.Wait(ctx); err != nil {
				return nil, fmt.Errorf("llm: %s 限速等待取消: %w", stage, err)
			}
		}
		attemptCtx := ctx
		if attemptTimeout > 0 {
			var cancel context.CancelFunc
			attemptCtx, cancel = context.WithTimeout(ctx, attemptTimeout)
			defer cancel()
		}
		var opts []model.Option
		if IsTruncatedError(lastErr) && c.MaxOutputTokens > 0 && !truncatedBoosted {
			opts = append(opts, model.WithMaxTokens(c.MaxOutputTokens*3/2))
			truncatedBoosted = true
		}
		out, err := c.Model.Generate(attemptCtx, msgsCopy, opts...)
		if err == nil {
			if out.ResponseMeta != nil && out.ResponseMeta.FinishReason == "length" {
				c.account(stage, msgsCopy, out)
				err = fmt.Errorf("llm: %s 输出被截断（finish_reason=length）", stage)
			} else {
				c.account(stage, msgsCopy, out)
				return out, nil
			}
		}
		lastErr = err
	}
	return nil, lastErr
}

// tryOneProviderJSON 单 Provider 的 JSON 生成（复用 Client.GenerateJSON 的回喂重试）。
func (r *Resilient) tryOneProviderJSON(ctx context.Context, p Provider, stage string, msgs []*schema.Message, out any) error {
	msgsCopy := copyMsgs(msgs)
	c := r.buildClient(p)
	c.fitInput(msgsCopy)

	// 同 tryOneProvider：超时约束单次尝试，由 Client.GenerateJSON 内部的
	// 回喂重试循环各自继承该 deadline——语义为"单次完整生成调用"的时限
	attemptCtx := ctx
	if tp, ok := p.(timeoutProvider); ok {
		if d := tp.AttemptTimeout(); d > 0 {
			var cancel context.CancelFunc
			attemptCtx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}

	return c.GenerateJSON(attemptCtx, stage, msgsCopy, out)
}

// buildClient 为指定 Provider 构造单模型 Client（记账跟随该模型）。
func (r *Resilient) buildClient(p Provider) *Client {
	c := NewClient(p.Model(), p.ModelName(), r.Budget)
	c.ContextTokens = p.ContextTokens()
	c.MaxOutputTokens = p.MaxOutputTokens()
	c.Limiter = r.Limiter
	c.MaxRetries = 1 // 重试由 Resilient 统一控制，Client 内不再重试
	costPer1K := [2]float64{}
	costPer1K[0], costPer1K[1] = p.CostPer1KTokens()
	c.OnUsage = func(stage string, prompt, completion int) {
		if r.OnUsage != nil {
			r.OnUsage(p.ModelName(), stage, prompt, completion)
		}
		if r.Tracker != nil {
			r.Tracker.Record(p.ModelName(), stage, prompt, completion, costPer1K)
		}
	}
	return c
}

// sameModelRetryable 判断错误是否值得在同一模型上重试。
// 429（不同模型限速池独立，等待浪费时间）、context 超限与 401/403（确定性失败，
// 但换模型/换凭证可能解决）→ 不重试同模型，交由降级链切换。
func sameModelRetryable(err error) bool {
	if err == nil {
		return true
	}
	hint := ClassifyLLMError(err)
	if hint.IsRateLimit {
		return false
	}
	return hint.Retryable
}

// isNilModel 判断模型是否为 nil（含包装在接口里的 typed nil 指针）。
func isNilModel(m model.BaseChatModel) bool {
	if m == nil {
		return true
	}
	v := reflect.ValueOf(m)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func:
		return v.IsNil()
	}
	return false
}

// backoff 指数退避 + jitter；429 下限 5s。
func (r *Resilient) backoff(attempt int, err error) time.Duration {
	base := r.cfg.BaseDelay
	minDelay := base
	if IsRateLimitError(err) {
		minDelay = 5 * time.Second
		if minDelay > r.cfg.MaxDelay {
			minDelay = r.cfg.MaxDelay
		}
	}
	delay := base * time.Duration(1<<uint(attempt-1))
	if delay < minDelay {
		delay = minDelay
	}
	delay += time.Duration(fastRand(int64(base / 2)))
	if delay > r.cfg.MaxDelay {
		delay = r.cfg.MaxDelay
	}
	return delay
}

func (r *Resilient) rememberModel(name string) {
	r.mu.Lock()
	r.lastModel = name
	r.mu.Unlock()
}

// copyMsgs 浅拷贝消息切片（fitInput 替换元素时只影响副本）。
func copyMsgs(msgs []*schema.Message) []*schema.Message {
	return append([]*schema.Message(nil), msgs...)
}

// fastRand 抖动随机数。math/rand 全局函数自 Go 1.20 起为 per-thread 源（无全局锁热点），
// 且带并发安全保证——优于手写无锁 xorshift（那是数据竞争）。
func fastRand(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return rand.Int63n(n)
}
