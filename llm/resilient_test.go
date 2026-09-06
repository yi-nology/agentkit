package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeChatModel 可编程的假 ChatModel：按脚本依次返回响应/错误。
type fakeChatModel struct {
	mu     sync.Mutex
	calls  int
	script []fakeResp // 依次消费；耗尽后重复最后一个
}

type fakeResp struct {
	err          error
	content      string
	finishReason string
	prompt       int
	completion   int
}

func (f *fakeChatModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := f.script[f.min(f.calls, len(f.script)-1)]
	f.calls++
	if resp.err != nil {
		return nil, resp.err
	}
	out := &schema.Message{Role: schema.Assistant, Content: resp.content}
	if resp.finishReason != "" || resp.prompt > 0 {
		out.ResponseMeta = &schema.ResponseMeta{}
		if resp.finishReason != "" {
			out.ResponseMeta.FinishReason = resp.finishReason
		}
		if resp.prompt > 0 {
			out.ResponseMeta.Usage = &schema.TokenUsage{
				PromptTokens: resp.prompt, CompletionTokens: resp.completion,
				TotalTokens: resp.prompt + resp.completion,
			}
		}
	}
	return out, nil
}

func (f *fakeChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream not supported")
}

func (f *fakeChatModel) min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (f *fakeChatModel) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeProvider 测试用 Provider。
type fakeProvider struct {
	name      string
	model     model.BaseChatModel
	ctxTokens int
	maxOut    int
	timeout   time.Duration
	costs     [2]float64
}

func (p *fakeProvider) Name() string                        { return p.name }
func (p *fakeProvider) Model() model.BaseChatModel          { return p.model }
func (p *fakeProvider) ModelName() string                   { return p.name + "-model" }
func (p *fakeProvider) ContextTokens() int                  { return p.ctxTokens }
func (p *fakeProvider) MaxOutputTokens() int                { return p.maxOut }
func (p *fakeProvider) CostPer1KTokens() (float64, float64) { return p.costs[0], p.costs[1] }
func (p *fakeProvider) AttemptTimeout() time.Duration       { return p.timeout }

func newFakeProvider(name string, script ...fakeResp) *fakeProvider {
	return &fakeProvider{
		name:      name,
		model:     &fakeChatModel{script: script},
		ctxTokens: 128_000,
		maxOut:    4096,
		costs:     [2]float64{0.001, 0.002},
	}
}

func newResilient(t *testing.T, providers ...Provider) *Resilient {
	t.Helper()
	r, err := NewResilient(NewFallbackChain(providers...),
		ResilientConfig{RetriesPerModel: 2, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func msgs(content string) []*schema.Message {
	return []*schema.Message{schema.UserMessage(content)}
}

// ---------- 构造与配置 ----------

func TestNewResilientEmptyChain(t *testing.T) {
	if _, err := NewResilient(NewFallbackChain(), ResilientConfig{}); err == nil {
		t.Fatal("空 Provider 列表应报错")
	}
	if _, err := NewResilient(nil, ResilientConfig{}); err == nil {
		t.Fatal("nil chain 应报错")
	}
}

func TestResilientConfigDefaults(t *testing.T) {
	cfg := ResilientConfig{}
	cfg.fillDefaults()
	if cfg.RetriesPerModel != 2 {
		t.Fatalf("默认重试应为 2，得到 %d", cfg.RetriesPerModel)
	}
	if cfg.BreakerTrip != 3 {
		t.Fatalf("默认熔断阈值应为 3，得到 %d", cfg.BreakerTrip)
	}
}

// ---------- 成功路径 ----------

func TestResilientPrimarySuccess(t *testing.T) {
	p1 := newFakeProvider("primary", fakeResp{content: "ok"})
	p2 := newFakeProvider("fallback", fakeResp{content: "fallback-ok"})
	r := newResilient(t, p1, p2)

	out, err := r.Generate(context.Background(), "R1", msgs("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "ok" {
		t.Fatalf("应使用主模型输出，得到 %q", out.Content)
	}
	if r.LastModel() != "primary-model" {
		t.Fatalf("LastModel = %q", r.LastModel())
	}
	if p2.model.(*fakeChatModel).callCount() != 0 {
		t.Fatal("备选模型不应被调用")
	}
}

// ---------- 降级触发 ----------

func TestResilient429ImmediateSwitch(t *testing.T) {
	// 429 不重试同模型：脚本全放 429，主模型只被调 1 次
	p1 := newFakeProvider("primary", fakeResp{err: errors.New("429 rate limit exceeded")})
	p2 := newFakeProvider("fallback", fakeResp{content: "fallback-ok"})
	r := newResilient(t, p1, p2)

	out, err := r.Generate(context.Background(), "R1", msgs("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "fallback-ok" {
		t.Fatal("应降级到备选模型")
	}
	if n := p1.model.(*fakeChatModel).callCount(); n != 1 {
		t.Fatalf("429 应立即切换：主模型只调 1 次，实际 %d", n)
	}
	if r.LastModel() != "fallback-model" {
		t.Fatalf("LastModel = %q", r.LastModel())
	}
}

func TestResilientContextTooLongSwitch(t *testing.T) {
	// context too long：同模型重试必然再败 → 立即切换
	p1 := newFakeProvider("primary", fakeResp{err: errors.New("This model's maximum context length is 8192 tokens")})
	p2 := newFakeProvider("fallback", fakeResp{content: "ok"})
	r := newResilient(t, p1, p2)

	if _, err := r.Generate(context.Background(), "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}
	if n := p1.model.(*fakeChatModel).callCount(); n != 1 {
		t.Fatalf("context 超限不应同模型重试：调了 %d 次", n)
	}
}

func TestResilient401SwitchNotRetry(t *testing.T) {
	// 401：同模型不重试（确定性失败），但切换模型（各 Provider 独立 APIKey）
	p1 := newFakeProvider("primary", fakeResp{err: errors.New("401 unauthorized: invalid api key")})
	p2 := newFakeProvider("fallback", fakeResp{content: "ok"})
	r := newResilient(t, p1, p2)

	if _, err := r.Generate(context.Background(), "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}
	if n := p1.model.(*fakeChatModel).callCount(); n != 1 {
		t.Fatalf("401 不应同模型重试：调了 %d 次", n)
	}
}

func TestResilientTransientRetryThenSuccess(t *testing.T) {
	// 瞬态错误：同模型重试，第 3 次成功 → 不降级
	p1 := newFakeProvider("primary",
		fakeResp{err: errors.New("500 internal server error")},
		fakeResp{err: errors.New("connection reset")},
		fakeResp{content: "ok"},
	)
	p2 := newFakeProvider("fallback", fakeResp{content: "fallback-ok"})
	r := newResilient(t, p1, p2)

	out, err := r.Generate(context.Background(), "R1", msgs("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "ok" {
		t.Fatal("瞬态错误重试后应成功")
	}
	if p2.model.(*fakeChatModel).callCount() != 0 {
		t.Fatal("不应降级")
	}
}

// ---------- 全部失败 ----------

func TestResilientAllFailAttemptError(t *testing.T) {
	p1 := newFakeProvider("primary", fakeResp{err: errors.New("429 too many requests")})
	p2 := newFakeProvider("fallback", fakeResp{err: errors.New("500 server error")})
	r := newResilient(t, p1, p2)

	_, err := r.Generate(context.Background(), "R1", msgs("hi"))
	if err == nil {
		t.Fatal("全部失败应返回错误")
	}
	var ae *AttemptError
	if !errors.As(err, &ae) {
		t.Fatalf("应返回 *AttemptError，得到 %T: %v", err, err)
	}
	if len(ae.Attempts) != 2 {
		t.Fatalf("应有 2 条尝试记录，得到 %d", len(ae.Attempts))
	}
	if ae.Stage != "R1" {
		t.Fatalf("Stage = %q", ae.Stage)
	}
	// 错误信息包含每个模型的失败原因
	msg := ae.Error()
	if !strings.Contains(msg, "primary") || !strings.Contains(msg, "fallback") {
		t.Fatalf("错误信息应含全部模型名: %s", msg)
	}
}

// ---------- 预算与取消 ----------

func TestResilientBudgetShortCircuit(t *testing.T) {
	budget := NewBudget(10) // 极小预算
	_ = budget.Remaining()
	budget.Add(10) // 耗尽

	p1 := newFakeProvider("primary", fakeResp{content: "ok"})
	r, _ := NewResilient(NewFallbackChain(p1), ResilientConfig{})
	r.Budget = budget

	_, err := r.Generate(context.Background(), "R1", msgs("hi"))
	if err == nil {
		t.Fatal("预算耗尽应短路失败")
	}
	if !strings.Contains(err.Error(), "预算") {
		t.Fatalf("错误应说明预算耗尽: %v", err)
	}
	if p1.model.(*fakeChatModel).callCount() != 0 {
		t.Fatal("预算耗尽不应调用模型")
	}
}

func TestResilientCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p1 := newFakeProvider("primary", fakeResp{content: "ok"})
	r := newResilient(t, p1)

	_, err := r.Generate(ctx, "R1", msgs("hi"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 ctx.Canceled，得到 %v", err)
	}
}

// ---------- fitInput 隔离（边界 case：调用方切片不被污染） ----------

func TestResilientMsgsNotMutated(t *testing.T) {
	// 主模型窗口 128k，输入超限 → fitInput 截断副本；
	// 调用方的原始 msgs 必须保持原样（后续降级/重试/审计依赖原文）
	p1 := newFakeProvider("primary", fakeResp{content: "ok"})
	p1.ctxTokens = 100 // 极小窗口触发 fitInput
	r := newResilient(t, p1)

	original := msgs(strings.Repeat("你好", 500)) // 1000 runes
	snapshot := original[0].Content

	if _, err := r.Generate(context.Background(), "R1", original); err != nil {
		t.Fatal(err)
	}
	if original[0].Content != snapshot {
		t.Fatal("调用方的 msgs 不应被 fitInput 污染")
	}
}

// ---------- 熔断 ----------

func TestResilientBreakerTrips(t *testing.T) {
	// 主模型连续失败 3 次（3 次独立调用各失败 1 次=429 立即切）→ 熔断
	// 第 4 次调用：主模型被熔断跳过，直接走备选（主模型 callCount 不再增长）
	p1 := newFakeProvider("primary", fakeResp{err: errors.New("429 rate limit")})
	p2 := newFakeProvider("fallback", fakeResp{content: "ok"})
	r := newResilient(t, p1, p2)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := r.Generate(ctx, "R1", msgs("hi")); err != nil {
			t.Fatal(err)
		}
	}
	callsAfter3 := p1.model.(*fakeChatModel).callCount()
	if callsAfter3 != 3 {
		t.Fatalf("3 次调用主模型应各被尝试 1 次，实际 %d", callsAfter3)
	}

	// 第 4 次：主模型熔断中，直接跳过
	if _, err := r.Generate(ctx, "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}
	if n := p1.model.(*fakeChatModel).callCount(); n != callsAfter3 {
		t.Fatalf("熔断后主模型不应再被调用: %d → %d", callsAfter3, n)
	}
}

// ---------- 成本记账跟随实际模型 ----------

func TestResilientCostTrackerFollowsModel(t *testing.T) {
	p1 := newFakeProvider("primary", fakeResp{err: errors.New("429 rate limit")})
	p2 := newFakeProvider("fallback", fakeResp{content: "ok", prompt: 100, completion: 50})
	r := newResilient(t, p1, p2)
	tracker := NewCostTracker()
	r.Tracker = tracker

	if _, err := r.Generate(context.Background(), "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}

	summary := tracker.Summary()
	if len(summary) != 1 {
		t.Fatalf("只有备选模型产生 token，应只有 1 条汇总，得到 %d", len(summary))
	}
	s, ok := summary["fallback-model"]
	if !ok {
		t.Fatalf("成本应记到 fallback-model: %v", summary)
	}
	if s.TotalPromptTokens != 100 {
		t.Fatalf("prompt tokens = %d", s.TotalPromptTokens)
	}
}

// ---------- JSON 生成 ----------

func TestResilientGenerateJSONFallback(t *testing.T) {
	// 主模型输出非法 JSON（回喂重试仍失败）→ 切换备选模型成功
	bad := newFakeProvider("primary", fakeResp{content: "not json at all"})
	good := newFakeProvider("fallback", fakeResp{content: `{"a":1}`})
	r := newResilient(t, bad, good)

	var out struct {
		A int `json:"a"`
	}
	if err := r.GenerateJSON(context.Background(), "R1", msgs("hi"), &out); err != nil {
		t.Fatal(err)
	}
	if out.A != 1 {
		t.Fatalf("A = %d", out.A)
	}
	if r.LastModel() != "fallback-model" {
		t.Fatalf("LastModel = %q", r.LastModel())
	}
}

// ---------- 单模型链 ----------

func TestResilientSingleProvider(t *testing.T) {
	p1 := newFakeProvider("only", fakeResp{content: "ok"})
	r := newResilient(t, p1)

	out, err := r.Generate(context.Background(), "R1", msgs("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "ok" {
		t.Fatal("单模型链应正常工作")
	}
}

// ---------- nil model 防御 ----------

func TestResilientNilModelSkipped(t *testing.T) {
	broken := &fakeProvider{name: "broken", model: nil, ctxTokens: 128_000, maxOut: 4096}
	good := newFakeProvider("good", fakeResp{content: "ok"})
	r := newResilient(t, broken, good)

	out, err := r.Generate(context.Background(), "R1", msgs("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "ok" {
		t.Fatal("nil model 应被跳过")
	}
}

// ---------- OnFallback 回调 ----------

func TestResilientOnFallbackCallback(t *testing.T) {
	p1 := newFakeProvider("primary", fakeResp{err: errors.New("429 rate limit")})
	p2 := newFakeProvider("fallback", fakeResp{content: "ok"})
	r := newResilient(t, p1, p2)

	var mu sync.Mutex
	var events []string
	r.OnFallback = func(from, to, stage, reason string) {
		mu.Lock()
		events = append(events, fmt.Sprintf("%s@%s", from, stage))
		mu.Unlock()
	}

	if _, err := r.Generate(context.Background(), "R1", msgs("hi")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0] != "primary-model@R1" {
		t.Fatalf("降级事件不符: %v", events)
	}
}

// ---------- 超时降级 ----------

func TestResilientAttemptTimeoutSwitch(t *testing.T) {
	// 主模型单次尝试超时（其 AttemptTimeout=10ms，但脚本让它阻塞）→ 切换备选
	slow := newFakeProvider("slow", fakeResp{content: "slow-ok"})
	slow.timeout = 10 * time.Millisecond
	// 用一个阻塞的模型模拟慢响应
	slow.model = &blockingChatModel{release: make(chan struct{})}
	fast := newFakeProvider("fast", fakeResp{content: "fast-ok"})
	r := newResilient(t, slow, fast)
	defer close(slow.model.(*blockingChatModel).release)
	out, err := r.Generate(context.Background(), "R1", msgs("hi"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "fast-ok" {
		t.Fatalf("超时后应切换到 fast 模型，得到 %q", out.Content)
	}
}

// blockingChatModel 阻塞直到 release 关闭（模拟慢响应）。
type blockingChatModel struct {
	release chan struct{}
	once    sync.Once
}

func (b *blockingChatModel) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	select {
	case <-b.release:
		return &schema.Message{Role: schema.Assistant, Content: "unblocked"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *blockingChatModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream not supported")
}

// ---------- Generator 接口兼容 ----------

func TestGeneratorInterface(t *testing.T) {
	var g Generator = &Client{}
	_ = g
	p1 := newFakeProvider("x", fakeResp{content: "ok"})
	r := newResilient(t, p1)
	g = r
	_ = g
}
