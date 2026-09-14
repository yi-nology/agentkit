package obsx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

// recordLogger 最小测试桩：记录 Info/Warn 调用（handler 只用这两个级别）。
type recordLogger struct {
	logx.Logger // 嵌入接口（其余方法不会被调用）
	calls       []logCall
}

type logCall struct {
	level string
	msg   string
	args  []any
}

func (r *recordLogger) Info(msg string, kv ...any) {
	r.calls = append(r.calls, logCall{level: "info", msg: msg, args: kv})
}

func (r *recordLogger) Warn(msg string, kv ...any) {
	r.calls = append(r.calls, logCall{level: "warn", msg: msg, args: kv})
}

func (r *recordLogger) find(msg string) *logCall {
	for i := range r.calls {
		if r.calls[i].msg == msg {
			return &r.calls[i]
		}
	}
	return nil
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
	ctx = fireModelCallbacks(ctx)

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
	ctx = callbacks.OnError(ctx, context.DeadlineExceeded)

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
	ctx = callbacks.OnEnd(ctx, &model.CallbackOutput{})

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

func TestOnUsageCallback(t *testing.T) {
	// v0.7.2：OnUsage 回调回收真实 usage（RawModel 旁路记账入口），stage 取自 ctx 标记
	rl := &recordLogger{Logger: logx.NewSlogLogger("test")}
	got := struct {
		model, stage string
		p, c         int
	}{}
	h := NewTracingHandler(rl, Options{
		OnUsage: func(component, model, stage string, prompt, completion int) {
			got.model, got.stage, got.p, got.c = model, stage, prompt, completion
		},
	})
	ctx := callbacks.InitCallbacks(context.Background(), nil, h)
	ctx = WithStage(ctx, "R3")
	_ = fireModelCallbacks(ctx)
	if got.p != 100 || got.c != 50 {
		t.Fatalf("OnUsage 应回收真实 usage: %+v", got)
	}
	if got.stage != "R3" || got.model == "" {
		t.Fatalf("stage/model 不应缺失: %+v", got)
	}
}
