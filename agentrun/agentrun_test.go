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
	content  string
	toolCall *mockToolCall
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

		choice := map[string]any{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": resp.content},
		}
		if resp.toolCall != nil {
			choice = map[string]any{
				"index": 0, "finish_reason": "tool_calls",
				"message": map[string]any{
					"role": "assistant", "content": "",
					"tool_calls": []any{map[string]any{
						"id":   resp.toolCall.id,
						"type": "function",
						"function": map[string]any{
							"name":      resp.toolCall.name,
							"arguments": resp.toolCall.arguments,
						},
					}},
				},
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
