package llm

// Retry-After 的 parse/Select/Transport 单测已随实现下沉 ekit/pkg/httpjson；
// 本文件保留 llm 全链端到端回归（OpenAIProvider→Client→generateRetry，
// CaptureRetryAfter=true）。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestGenerateRetryHonorsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"requests","code":"1305"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"恢复后作答"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer srv.Close()

	provider, err := NewOpenAIProvider(context.Background(), OpenAIProviderConfig{
		BaseURL: srv.URL, APIKey: "k", Model: "test-model",
		ContextTokens: 4096, MaxOutputTokens: 512,
		Timeout: 5 * time.Second, CaptureRetryAfter: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Model: provider.Model(), ModelName: "test-model", MaxRetries: 3, BaseDelay: 50 * time.Millisecond, MaxDelay: 200 * time.Millisecond}
	start := time.Now()
	out, err := c.Generate(context.Background(), "retryafter-stage", []*schema.Message{{Role: schema.User, Content: "q"}})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if out.Content != "恢复后作答" {
		t.Fatalf("内容异常: %q", out.Content)
	}
	if elapsed < time.Second {
		t.Fatalf("应等待服务端建议（≥1s）: %v", elapsed)
	}
}
