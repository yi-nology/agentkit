package llm

import (
	"context"
	"testing"
	"time"
)

func TestNewOpenAIProviderDefaults(t *testing.T) {
	p, err := NewOpenAIProvider(context.Background(), OpenAIProviderConfig{
		BaseURL: "https://api.example.com/v1",
		APIKey:  "key",
		Model:   "gpt-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 缺省窗口 128k
	if p.ContextTokens() != 128_000 {
		t.Fatalf("默认窗口 = %d", p.ContextTokens())
	}
	// 输出上限 = max(4096, 128k*5%) = 6400
	if p.MaxOutputTokens() != 6400 {
		t.Fatalf("默认输出上限 = %d", p.MaxOutputTokens())
	}
	// 超时缺省 60s
	if p.AttemptTimeout() != 60*time.Second {
		t.Fatalf("默认超时 = %v", p.AttemptTimeout())
	}
	if p.Name() != "openai" || p.ModelName() != "gpt-test" {
		t.Fatalf("Name/ModelName = %s/%s", p.Name(), p.ModelName())
	}
	if p.Model() == nil {
		t.Fatal("Model 应返回非 nil（构造不发网络请求）")
	}
}

func TestNewOpenAIProviderExplicitConfig(t *testing.T) {
	p, err := NewOpenAIProvider(context.Background(), OpenAIProviderConfig{
		BaseURL: "https://api.example.com/v1", APIKey: "k", Model: "m",
		ContextTokens: 1_000_000, MaxOutputTokens: 50_000,
		Timeout:             120 * time.Second,
		CostPer1KPrompt:     0.001,
		CostPer1KCompletion: 0.002,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ContextTokens() != 1_000_000 || p.MaxOutputTokens() != 50_000 {
		t.Fatalf("显式配置未生效: %d/%d", p.ContextTokens(), p.MaxOutputTokens())
	}
	if p.AttemptTimeout() != 120*time.Second {
		t.Fatalf("Timeout = %v", p.AttemptTimeout())
	}
	pr, co := p.CostPer1KTokens()
	if pr != 0.001 || co != 0.002 {
		t.Fatalf("CostPer1K = %v/%v", pr, co)
	}
}

func TestNewOpenAIProviderMaxOutputFloor(t *testing.T) {
	// 小窗口下输出上限不低于 4096
	p, err := NewOpenAIProvider(context.Background(), OpenAIProviderConfig{
		BaseURL: "x", APIKey: "k", Model: "m", ContextTokens: 32_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 32k*5% = 1600 → 钳到 4096
	if p.MaxOutputTokens() != 4096 {
		t.Fatalf("输出上限应钳到 4096，实际 %d", p.MaxOutputTokens())
	}
}

// OpenAIProvider 实现 Provider 接口（编译期断言在运行时验证字段完整性）。
func TestOpenAIProviderSatisfiesInterface(t *testing.T) {
	p, err := NewOpenAIProvider(context.Background(), OpenAIProviderConfig{
		BaseURL: "x", APIKey: "k", Model: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	var _ Provider = p
}
