package agentrun

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

		// 流式请求（EnableStreaming 后 adk 走模型 Stream）：OpenAI SSE chunk 线缆格式——
		// reasoning/正文拆多片发增量 delta，tool_call 按 id/name 首片 + arguments 片段
		// 两段式，与真实兼容网关同构。
		if reqStream(r) {
			m.writeStreamChunks(w, resp)
			return
		}

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

// reqStream 判定请求体是否 stream:true（OpenAI 语义）。
func reqStream(r *http.Request) bool {
	var body struct {
		Stream bool `json:"stream"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	return body.Stream
}

// sseChunk 一条 OpenAI 流式 chunk 帧（delta+finish_reason）。
func sseChunk(delta map[string]any, finish any) string {
	b, _ := json.Marshal(map[string]any{
		"id": "cmpl-test", "object": "chat.completion.chunk", "created": 1, "model": "mock",
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	})
	return "data: " + string(b) + "\n\n"
}

// writeStreamChunks 把一条响应按 OpenAI 流式协议拆片输出：role 首片 → reasoning 增量
// → tool_call 两段式（id/name 片 + arguments 片）或正文增量 → 收尾片 → [DONE]。
func (m *mockOpenAI) writeStreamChunks(w http.ResponseWriter, resp mockResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	write := func(s string) {
		_, _ = io.WriteString(w, s)
		if fl != nil {
			fl.Flush()
		}
	}
	write(sseChunk(map[string]any{"role": "assistant"}, nil))
	if resp.reasoning != "" {
		for _, part := range splitChunks(resp.reasoning, 2) {
			write(sseChunk(map[string]any{"reasoning_content": part}, nil))
		}
	}
	if resp.toolCall != nil {
		write(sseChunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": resp.toolCall.id, "type": "function",
			"function": map[string]any{"name": resp.toolCall.name, "arguments": ""},
		}}}, nil))
		write(sseChunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": 0,
			"function": map[string]any{"arguments": resp.toolCall.arguments},
		}}}, nil))
		write(sseChunk(map[string]any{}, "tool_calls"))
	} else {
		for _, part := range splitChunks(resp.content, 2) {
			write(sseChunk(map[string]any{"content": part}, nil))
		}
		write(sseChunk(map[string]any{}, "stop"))
	}
	write("data: [DONE]\n\n")
}

// splitChunks 把文本按 rune 均分为 n 片（单片至少 1 rune；空串不拆）。
func splitChunks(s string, n int) []string {
	if s == "" {
		return nil
	}
	rs := []rune(s)
	if len(rs) <= n {
		return []string{s}
	}
	size := (len(rs) + n - 1) / n
	var out []string
	for i := 0; i < len(rs); i += size {
		end := i + size
		if end > len(rs) {
			end = len(rs)
		}
		out = append(out, string(rs[i:end]))
	}
	return out
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

// TestRunStreamingDeltas 流式增量通道语义（默认开流）：思考/正文逐分片先于权威快照
// 到达；分片拼接 = 快照全文；调用轮（含 tool_calls 分片）不外发正文增量——其前导正文
// 分片（无 tool_calls 的 chunk）仍外发；权威事件序列与关流时一致。
func TestRunStreamingDeltas(t *testing.T) {
	_, cm := newMockOpenAI(t,
		mockResponse{reasoning: "先想清楚再动手", toolCall: &mockToolCall{id: "c1", name: "echo", arguments: `{"text":"hi"}`}},
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

	// 思考增量：全部先于权威 reasoning；拼接 = 快照全文。
	var rDelta, tDelta strings.Builder
	authorityIdx := map[string]int{}
	for i, e := range events {
		switch e.Type {
		case EventReasoningDelta:
			if _, ok := authorityIdx[EventReasoning]; ok {
				t.Fatalf("reasoning_delta 出现在权威 reasoning 之后：%+v", events[:i+1])
			}
			rDelta.WriteString(e.Text)
		case EventReasoning:
			authorityIdx[EventReasoning] = i
			if rDelta.String() != e.Text {
				t.Fatalf("思考分片拼接 %q ≠ 快照 %q", rDelta.String(), e.Text)
			}
		case EventTextDelta:
			if _, ok := authorityIdx[EventText]; ok {
				t.Fatalf("text_delta 出现在权威 text 之后：%+v", events[:i+1])
			}
			tDelta.WriteString(e.Text)
		case EventText:
			authorityIdx[EventText] = i
			if tDelta.String() != e.Text {
				t.Fatalf("正文分片拼接 %q ≠ 快照 %q", tDelta.String(), e.Text)
			}
		}
	}
	if len(authorityIdx) != 2 {
		t.Fatalf("应各有一条权威 reasoning/text：%+v", events)
	}
	// 调用轮不外发正文增量（reasoning 增量可以有）：全部 text_delta 拼接恰为终稿。
	if tDelta.String() != "最终结论" {
		t.Fatalf("正文增量应为终稿全文（调用轮不夹带）：%q", tDelta.String())
	}
	// 增量确实在流动：至少 2 片 reasoning_delta（mock 按 2 片拆分）。
	if n := strings.Count(eventsString(events), "reasoning_delta"); n < 2 {
		t.Fatalf("reasoning_delta 应 ≥2 片，实得 %d：%+v", n, events)
	}
}

func eventsString(events []Event) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString(e.Type)
		b.WriteByte(',')
	}
	return b.String()
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
	}, "调用工具", func(e Event) {
		if e.Type == EventReasoningDelta || e.Type == EventTextDelta {
			return // 流式增量通道：本测试只对账权威事件（增量语义由 TestRunStreamingDeltas 专测）
		}
		events = append(events, e)
	})
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
	//（toolprior.LimitCalls 等有状态包装的计数按尝试重置，不跨尝试累计）
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
