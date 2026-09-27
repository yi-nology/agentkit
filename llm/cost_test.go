package llm

import "testing"

// PriceOf：精确匹配优先、最长前缀回落、未配价归零（bianque 生产语义）。
func TestPriceOf(t *testing.T) {
	pricing := map[string]Price{
		"deepseek-chat": {InputPerM: 0.27, OutputPerM: 1.1},
		"deepseek":      {InputPerM: 1.0, OutputPerM: 2.0},
		"qwen-max":      {InputPerM: 1.6, OutputPerM: 6.4},
	}
	cost := PriceOf(pricing, "deepseek-chat", 1_000_000, 1_000_000)
	if cost != 1.37 {
		t.Fatalf("精确匹配: cost=%v want 1.37", cost)
	}
	cost = PriceOf(pricing, "deepseek-chat-20260901", 1_000_000, 0)
	if cost != 0.27 {
		t.Fatalf("最长前缀回落应命中 deepseek-chat: %v", cost)
	}
	if PriceOf(pricing, "unknown-model", 1_000_000, 1_000_000) != 0 {
		t.Fatal("未配价应返回 0")
	}
	if PriceOf(nil, "deepseek-chat", 1, 1) != 0 {
		t.Fatal("空定价表应返回 0")
	}
}
