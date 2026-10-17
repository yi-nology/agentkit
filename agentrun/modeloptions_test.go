package agentrun

// 契约测试（批次 extra_body 透传缝）：Config.ModelOptions 经 adk.WithChatModelOptions
// 注入 → 装饰链/ADK 图下钻 → openai.ChatModel 真实 HTTP 请求体。用真 ChatModel 打
// httptest 网关（非 llmtest 桩——桩会吞 opts 证明不了下钻），断言 WithExtraFields 的
// 键值原样出现在 /chat/completions 请求体顶层。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

func TestModelOptionsReachesWire(t *testing.T) {
	var mu sync.Mutex
	var lastBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		parsed := map[string]any{}
		_ = json.Unmarshal(b, &parsed)
		mu.Lock()
		lastBody = parsed
		mu.Unlock()
		writeJSON(w, map[string]any{
			"id": "cmpl", "object": "chat.completion", "created": 1, "model": "mock",
			"choices": []any{map[string]any{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "ok"},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	t.Cleanup(srv.Close)

	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: srv.URL + "/v1", APIKey: "k", Model: "mock",
	})
	if err != nil {
		t.Fatal(err)
	}

	opts := []model.Option{openai.WithExtraFields(map[string]any{"enable_thinking": true, "thinking_budget": 4096})}
	out, err := Run(context.Background(), Config{Name: "t", Instruction: "sys", Model: cm, ModelOptions: opts, DisableStreaming: true}, "q")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok" {
		t.Fatalf("out=%q", out)
	}
	mu.Lock()
	body := lastBody
	mu.Unlock()
	if body == nil {
		t.Fatal("网关未收到请求")
	}
	if body["enable_thinking"] != true {
		t.Fatalf("enable_thinking 未达请求体: %v", body)
	}
	if tb, ok := body["thinking_budget"].(float64); !ok || tb != 4096 {
		t.Fatalf("thinking_budget 未达请求体: %v", body["thinking_budget"])
	}
}

// 对照：不传 ModelOptions 时请求体不含扩展键（缺省零漂移）。
func TestModelOptionsAbsentNoLeak(t *testing.T) {
	var mu sync.Mutex
	var lastBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		parsed := map[string]any{}
		_ = json.Unmarshal(b, &parsed)
		mu.Lock()
		lastBody = parsed
		mu.Unlock()
		writeJSON(w, map[string]any{
			"id": "cmpl", "object": "chat.completion", "created": 1, "model": "mock",
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "ok"}}},
		})
	}))
	defer srv.Close()
	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: srv.URL + "/v1", APIKey: "k", Model: "mock",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Config{Name: "t", Instruction: "sys", Model: cm, DisableStreaming: true}, "q"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, bad := lastBody["enable_thinking"]; bad {
		t.Fatal("未传 ModelOptions 不应有扩展字段")
	}
}
