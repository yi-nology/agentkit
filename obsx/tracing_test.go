package obsx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/oekit/logx"
)

// recordLogger 最小测试桩：记录 Info/Warn 调用（handler 只用这两个级别）。
// mu 保护 calls——流式收束在 GoSafe 后台 goroutine 落日志，与主测试 goroutine
// 的轮询读取并发（生产 logx 线程安全，桩须同款纪律）。
type recordLogger struct {
	logx.Logger // 嵌入接口（其余方法不会被调用）
	mu          sync.Mutex
	calls       []logCall
}

type logCall struct {
	level string
	msg   string
	args  []any
}

func (r *recordLogger) Info(msg string, kv ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, logCall{level: "info", msg: msg, args: kv})
}

func (r *recordLogger) Warn(msg string, kv ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, logCall{level: "warn", msg: msg, args: kv})
}

func (r *recordLogger) find(msg string) *logCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.calls {
		if r.calls[i].msg == msg {
			c := r.calls[i]
			return &c
		}
	}
	return nil
}

func (r *recordLogger) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (c *logCall) value(key string) (any, bool) {
	for i := 0; i+1 < len(c.args); i += 2 {
		if c.args[i] == key {
			return c.args[i+1], true
		}
	}
	return nil, false
}

// fireModelCallbacks 模拟 eino-ext openai 模型内部的回调触发链路：
// EnsureRunInfo → OnStart(typed input) → OnEnd(typed output)。
func fireModelCallbacks(ctx context.Context) context.Context {
	ctx = callbacks.EnsureRunInfo(ctx, "OpenAI", components.ComponentOfChatModel)
	ctx = callbacks.OnStart(ctx, &model.CallbackInput{
		Messages: []*schema.Message{schema.UserMessage("hi")},
	})
	ctx = callbacks.OnEnd(ctx, &model.CallbackOutput{
		Message: &schema.Message{Role: schema.Assistant, Content: "回答"},
		TokenUsage: &model.TokenUsage{
			PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150,
		},
	})
	return ctx
}

func TestTracingHandlerOnEnd(t *testing.T) {
	rl := &recordLogger{Logger: logx.NewSlogLogger("test")}

	ctx := InitLLMObservability(context.Background(), rl, Options{})
	ctx = WithStage(ctx, "R1")
	fireModelCallbacks(ctx)

	end := rl.find("llm.call.end")
	if end == nil {
		t.Fatalf("缺 llm.call.end，实际 %d 条: %+v", len(rl.calls), rl.calls)
	}
	for key, want := range map[string]any{
		"stage":             "R1",
		"component":         string(components.ComponentOfChatModel),
		"model":             "OpenAI",
		"prompt_tokens":     100,
		"completion_tokens": 50,
		"total_tokens":      150,
	} {
		if v, ok := end.value(key); !ok || v != want {
			t.Errorf("end.%s = %v (want %v)", key, v, want)
		}
	}
	if start := rl.find("llm.call.start"); start == nil {
		t.Fatal("缺 llm.call.start")
	}
}

func TestTracingHandlerOnError(t *testing.T) {
	rl := &recordLogger{Logger: logx.NewSlogLogger("test")}

	ctx := InitLLMObservability(context.Background(), rl, Options{})
	ctx = WithStage(ctx, "R3")
	ctx = callbacks.EnsureRunInfo(ctx, "OpenAI", components.ComponentOfChatModel)
	ctx = callbacks.OnStart(ctx, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hi")}})
	callbacks.OnError(ctx, context.DeadlineExceeded)

	e := rl.find("llm.call.error")
	if e == nil {
		t.Fatal("缺 llm.call.error")
	}
	if v, _ := e.value("stage"); v != "R3" {
		t.Errorf("error.stage = %v", v)
	}
	if v, _ := e.value("error"); !errors.Is(toErr(v), context.DeadlineExceeded) && v != "context deadline exceeded" {
		t.Errorf("error.error = %v", v)
	}
}

func TestTracingHandlerSlowCall(t *testing.T) {
	rl := &recordLogger{Logger: logx.NewSlogLogger("test")}

	ctx := InitLLMObservability(context.Background(), rl, Options{SlowThreshold: time.Millisecond})
	ctx = callbacks.EnsureRunInfo(ctx, "OpenAI", components.ComponentOfChatModel)
	ctx = callbacks.OnStart(ctx, &model.CallbackInput{})
	time.Sleep(5 * time.Millisecond)
	callbacks.OnEnd(ctx, &model.CallbackOutput{})

	if rl.find("llm.call.slow") == nil {
		t.Fatal("慢调用应落 warn")
	}
}

func TestStageFromContext(t *testing.T) {
	if StageFromContext(WithStage(context.Background(), "R2")) != "R2" {
		t.Fatal("WithStage 后应可读出阶段")
	}
	if StageFromContext(context.Background()) != "" {
		t.Fatal("未标记 ctx 应返回空串")
	}
}

func toErr(v any) error {
	e, _ := v.(error)
	return e
}

func TestTokenUsageOfResponseMetaFallback(t *testing.T) {
	// compose 图节点对裸 ChatModel 只透传 Message（无 TokenUsage 字段）——
	// v0.10.10 前obsx 侧缺 ResponseMeta 回退会静默漏采；回退路径须与 llm 侧一致。
	u := TokenUsageOf(&model.CallbackOutput{
		Message: &schema.Message{
			Role: schema.Assistant,
			ResponseMeta: &schema.ResponseMeta{
				Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		},
	})
	if u == nil || u.PromptTokens != 10 || u.CompletionTokens != 5 || u.TotalTokens != 15 {
		t.Fatalf("ResponseMeta.Usage 回退应生效: %+v", u)
	}

	// TokenUsage 字段优先，不受 Message 影响。
	u = TokenUsageOf(&model.CallbackOutput{
		Message:    &schema.Message{Role: schema.Assistant},
		TokenUsage: &model.TokenUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
	})
	if u == nil || u.PromptTokens != 100 {
		t.Fatalf("TokenUsage 字段应优先: %+v", u)
	}

	// 两者皆无 → nil（调用方静默跳过）。
	if TokenUsageOf(&model.CallbackOutput{Message: &schema.Message{Role: schema.Assistant}}) != nil {
		t.Fatal("无 usage 应返回 nil")
	}
	if TokenUsageOf(nil) != nil {
		t.Fatal("nil 输出应返回 nil")
	}
}

func TestDrainStreamUsageLastNonNil(t *testing.T) {
	// last-non-nil 语义：无 usage 的尾帧不清空已捕获值；模型名取末个非空；
	// Message 路回退 usage 同样参与捕获；类型 nil 帧跳过。
	msgUsage := &schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{
		Usage: &schema.TokenUsage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
	}}
	var typedNil *model.CallbackOutput
	sr := schema.StreamReaderFromArray([]callbacks.CallbackOutput{
		&model.CallbackOutput{Config: &model.Config{Model: "m1"}}, // 仅模型名，无 usage
		typedNil, // 类型 nil 帧：跳过不清状态
		&model.CallbackOutput{Config: &model.Config{Model: "m2"},
			TokenUsage: &model.TokenUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}},
		msgUsage, // Message 路回退 usage → 成为末个非 nil
		&model.CallbackOutput{Config: &model.Config{Model: "m3"}}, // 尾帧无 usage：不清空
	})
	usage, name := DrainStreamUsage(sr)
	if name != "m3" {
		t.Fatalf("模型名应取末个非空: %q", name)
	}
	if usage == nil || usage.PromptTokens != 7 || usage.TotalTokens != 10 {
		t.Fatalf("usage 应取末个非 nil（Message 回退路）: %+v", usage)
	}
	sr.Close() // 调用方负责 Close 契约
}

func TestTracingHandlerStreamEnd(t *testing.T) {
	rl := &recordLogger{Logger: logx.NewSlogLogger("test")}

	ctx := InitLLMObservability(context.Background(), rl, Options{})
	ctx = WithStage(ctx, "R2")
	ctx = callbacks.EnsureRunInfo(ctx, "OpenAI", components.ComponentOfChatModel)
	ctx = callbacks.OnStart(ctx, &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("hi")}})

	sr := schema.StreamReaderFromArray([]callbacks.CallbackOutput{
		&model.CallbackOutput{Config: &model.Config{Model: "gpt-x"}}, // 流内真实模型名
		&model.CallbackOutput{TokenUsage: &model.TokenUsage{PromptTokens: 11, CompletionTokens: 22, TotalTokens: 33}},
	})
	callbacks.OnEndWithStreamOutput(ctx, sr)

	// 收束是异步排空（GoSafe）：轮询等 end 落盘。
	deadline := time.Now().Add(2 * time.Second)
	var end *logCall
	for time.Now().Before(deadline) {
		if end = rl.find("llm.call.end"); end != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if end == nil {
		t.Fatalf("流式收束应落 llm.call.end，实际 %d 条", rl.count())
	}
	for key, want := range map[string]any{
		"stage":             "R2",
		"model":             "gpt-x",
		"prompt_tokens":     11,
		"completion_tokens": 22,
		"total_tokens":      33,
	} {
		if v, ok := end.value(key); !ok || v != want {
			t.Errorf("end.%s = %v (want %v)", key, v, want)
		}
	}
}

func TestTracingHandlerPreview(t *testing.T) {
	rl := &recordLogger{Logger: logx.NewSlogLogger("test")}

	ctx := InitLLMObservability(context.Background(), rl, Options{PreviewLen: 4})
	ctx = callbacks.EnsureRunInfo(ctx, "OpenAI", components.ComponentOfChatModel)
	ctx = callbacks.OnStart(ctx, &model.CallbackInput{})
	callbacks.OnEnd(ctx, &model.CallbackOutput{
		Message: &schema.Message{Role: schema.Assistant, Content: "一二三四五六七八"},
	})

	end := rl.find("llm.call.end")
	if end == nil {
		t.Fatal("缺 llm.call.end")
	}
	v, ok := end.value("output_preview")
	if !ok || v == "" {
		t.Fatalf("PreviewLen>0 应落 output_preview")
	}
	if got := v.(string); got == "一二三四五六七八" {
		t.Fatalf("预览应截断: %q", got)
	}
}
