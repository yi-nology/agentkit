package agentrun

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ---------- mock OpenAI ----------

// mockResponse 一次响应脚本。
type mockResponse struct {
	content   string
	reasoning string // reasoning_content（推理型模型的思考过程，可选）
	toolCall  *mockToolCall
}

type mockToolCall struct {
	id        string
	name      string
	arguments string
}

// mockOpenAI 最小 OpenAI 兼容 server：responses 按调用次数依次返回（耗尽后重复最后一条）。
type mockOpenAI struct {
	srv       *httptest.Server
	responses []mockResponse
	calls     atomic.Int32
}

func newMockOpenAI(t *testing.T, responses ...mockResponse) (*mockOpenAI, *openai.ChatModel) {
	t.Helper()
	m := &mockOpenAI{responses: responses}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(m.calls.Add(1)) - 1
		if i >= len(m.responses) {
			i = len(m.responses) - 1
		}
		resp := m.responses[i]

		msg := map[string]any{"role": "assistant", "content": resp.content}
		if resp.reasoning != "" {
			msg["reasoning_content"] = resp.reasoning
		}
		choice := map[string]any{
			"index": 0, "finish_reason": "stop", "message": msg,
		}
		if resp.toolCall != nil {
			msg["tool_calls"] = []any{map[string]any{
				"id":   resp.toolCall.id,
				"type": "function",
				"function": map[string]any{
					"name":      resp.toolCall.name,
					"arguments": resp.toolCall.arguments,
				},
			}}
			choice = map[string]any{
				"index": 0, "finish_reason": "tool_calls", "message": msg,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{
			"id": "cmpl-test", "object": "chat.completion", "created": 1, "model": "mock",
			"choices": []any{choice},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	t.Cleanup(m.srv.Close)

	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: m.srv.URL + "/v1", APIKey: "k", Model: "mock",
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, cm
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// newEchoTool 测试工具：回显输入文本。
func newEchoTool(t *testing.T) tool.BaseTool {
	t.Helper()
	st, err := utils.InferTool("echo", "回显输入",
		func(_ context.Context, in *echoIn) (*echoOut, error) {
			return &echoOut{Echo: in.Text}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

type echoIn struct {
	Text string `json:"text" jsonschema:"description=要回显的文本"`
}
type echoOut struct {
	Echo string `json:"echo"`
}

// ---------- 用例 ----------

func TestRunSimpleReply(t *testing.T) {
	_, cm := newMockOpenAI(t, mockResponse{content: "最终结论"})

	out, err := Run(context.Background(), Config{
		Name: "test", Instruction: "你是测试 agent", Model: cm,
	}, "做点什么")
	if err != nil {
		t.Fatal(err)
	}
	if out != "最终结论" {
		t.Fatalf("out = %q", out)
	}
}

func TestRunReActLoopWithToolCall(t *testing.T) {
	// 第一轮 tool_calls（ReAct 循环）→ 第二轮拿到工具结果产出最终文本
	_, cm := newMockOpenAI(t,
		mockResponse{toolCall: &mockToolCall{id: "c1", name: "echo", arguments: `{"text":"hi"}`}},
		mockResponse{content: "工具结果已消化，最终结论"},
	)

	echoTool := newEchoTool(t)
	var toolEvents []string
	out, err := RunWithEvents(context.Background(), Config{
		Name: "test", Instruction: "使用 echo 工具后给出结论", Model: cm,
		Tools: []tool.BaseTool{echoTool},
	}, "调用工具", func(e Event) {
		if e.Type == EventToolCall {
			toolEvents = append(toolEvents, e.Tool)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "工具结果已消化，最终结论" {
		t.Fatalf("out = %q", out)
	}
	if len(toolEvents) != 1 || toolEvents[0] != "echo" {
		t.Fatalf("tool_call 事件不符: %v", toolEvents)
	}
}

func TestRunEventsReasoningAndToolCallArgs(t *testing.T) {
	// 回归：推理型模型的 reasoning_content 要以 reasoning 事件先行外发；
	// tool_call 事件携带原始 JSON 参数串（观测面需要看到调用命令）
	_, cm := newMockOpenAI(t,
		mockResponse{
			reasoning: "先想清楚再动手",
			toolCall:  &mockToolCall{id: "c1", name: "echo", arguments: `{"text":"hi"}`},
		},
		mockResponse{content: "最终结论"},
	)

	var events []Event
	out, err := RunWithEvents(context.Background(), Config{
		Name: "test", Instruction: "inst", Model: cm,
		Tools: []tool.BaseTool{newEchoTool(t)},
	}, "调用工具", func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "最终结论" {
		t.Fatalf("out = %q", out)
	}

	// 期望事件序列：reasoning → tool_call(带 Args) → tool_result → text
	want := []Event{
		{Type: EventReasoning, Text: "先想清楚再动手"},
		{Type: EventToolCall, Tool: "echo", Args: `{"text":"hi"}`},
		{Type: EventToolResult, Tool: "echo"},
		{Type: EventText, Text: "最终结论"},
	}
	if len(events) != len(want) {
		t.Fatalf("事件数 = %d，期望 %d：%+v", len(events), len(want), events)
	}
	for i, w := range want {
		got := events[i]
		// tool_result 文本来自工具回显，只断言类型/工具名
		if got.Type != w.Type || got.Tool != w.Tool || got.Args != w.Args {
			t.Fatalf("事件[%d] = %+v，期望 %+v", i, got, w)
		}
		if w.Type == EventReasoning && got.Text != w.Text {
			t.Fatalf("事件[%d].Text = %q，期望 %q", i, got.Text, w.Text)
		}
		if w.Type == EventText && got.Text != w.Text {
			t.Fatalf("事件[%d].Text = %q，期望 %q", i, got.Text, w.Text)
		}
	}
}

func TestRunWithRetry(t *testing.T) {
	// 第一次空内容（未产出最终文本）→ 重试成功
	_, cm := newMockOpenAI(t,
		mockResponse{content: ""},
		mockResponse{content: "重试成功"},
	)

	out, err := RunWithRetry(context.Background(), Config{
		Name: "test", Instruction: "inst", Model: cm,
	}, "第一问", "第二问（带反馈）")
	if err != nil {
		t.Fatal(err)
	}
	if out != "重试成功" {
		t.Fatalf("out = %q", out)
	}
}

func TestRunMaxIterationsExhausted(t *testing.T) {
	// 持续 tool_calls 永不出口 → MaxIterations 耗尽报错
	tc := &mockToolCall{id: "c1", name: "echo", arguments: "{}"}
	_, cm := newMockOpenAI(t,
		mockResponse{toolCall: tc}, mockResponse{toolCall: tc},
		mockResponse{toolCall: tc}, mockResponse{toolCall: tc},
		mockResponse{toolCall: tc},
	)

	echo := newEchoTool(t)
	_, err := Run(context.Background(), Config{
		Name: "test", Instruction: "inst", Model: cm,
		Tools:         []tool.BaseTool{echo},
		MaxIterations: 3,
	}, "x")
	if err == nil {
		t.Fatal("迭代耗尽应报错")
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{Instruction: "x"}).Validate(); err == nil {
		t.Fatal("nil Model 应报错")
	}
	if err := (Config{Model: nil}).Validate(); err == nil {
		t.Fatal("空 Instruction 应报错")
	}
}

func TestMaxIterationsDefault(t *testing.T) {
	if (Config{}).maxIterations() != DefaultMaxIterations {
		t.Fatalf("默认迭代 = %d", (Config{}).maxIterations())
	}
}

func TestRunWithRetryToolsFactoryFreshPerAttempt(t *testing.T) {
	// A1 回归：RunWithRetry + ToolsFactory 时，每次尝试应拿到新建的工具表
	//（toolprior.WithCallLimit 等有状态包装的计数按尝试重置，不跨尝试累计）
	_, cm := newMockOpenAI(t,
		mockResponse{content: ""}, // 首轮失败（空最终文本）
		mockResponse{content: "重试成功"},
	)

	var builds atomic.Int32
	cfg := Config{
		Name: "test", Instruction: "inst", Model: cm,
		ToolsFactory: func() []tool.BaseTool {
			builds.Add(1)
			return nil // 本用例无工具调用，只验证工厂按尝试调用
		},
	}

	out, err := RunWithRetry(context.Background(), cfg, "第一问", "第二问")
	if err != nil {
		t.Fatal(err)
	}
	if out != "重试成功" {
		t.Fatalf("out = %q", out)
	}
	if builds.Load() != 2 {
		t.Fatalf("ToolsFactory 应按尝试各建一次（2 次），实际 %d", builds.Load())
	}
}

func TestToolsFactoryPreferredOverTools(t *testing.T) {
	// 两者都设置时 ToolsFactory 优先
	_, cm := newMockOpenAI(t, mockResponse{content: "ok"})

	var used bool
	out, err := Run(context.Background(), Config{
		Name: "test", Instruction: "inst", Model: cm,
		Tools: []tool.BaseTool{newEchoTool(t)},
		ToolsFactory: func() []tool.BaseTool {
			used = true
			return nil
		},
	}, "q")
	if err != nil || out != "ok" {
		t.Fatalf("run 失败: %v %q", err, out)
	}
	if !used {
		t.Fatal("ToolsFactory 设置时应优先使用工厂")
	}
}

func TestRunEventsCarryNativeCallID(t *testing.T) {
	// P0 身份透传：tool_call/tool_result 事件必须携带原生调用 ID——
	// bianque 观测面靠它做声明↔结果精确配对（取代按名 FIFO 猜配对）。
	_, cm := newMockOpenAI(t,
		mockResponse{toolCall: &mockToolCall{id: "call_abc", name: "echo", arguments: `{"text":"hi"}`}},
		mockResponse{content: "最终结论"},
	)

	var callIDs []string
	_, err := RunWithEvents(context.Background(), Config{
		Name: "test", Instruction: "inst", Model: cm,
		Tools: []tool.BaseTool{newEchoTool(t)},
	}, "调用工具", func(e Event) {
		if e.Type == EventToolCall || e.Type == EventToolResult {
			callIDs = append(callIDs, e.Type+":"+e.CallID)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(callIDs) != 2 || callIDs[0] != "tool_call:call_abc" || callIDs[1] != "tool_result:call_abc" {
		t.Fatalf("原生 CallID 透传不符: %v", callIDs)
	}
}
