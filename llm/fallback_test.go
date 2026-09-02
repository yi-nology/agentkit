package llm

import (
	"testing"

	"github.com/cloudwego/eino/components/model"
)

func TestFallbackChain(t *testing.T) {
	p1 := &mockProvider{name: "primary", modelName: "gpt-4o"}
	p2 := &mockProvider{name: "fallback", modelName: "deepseek-v4"}

	fc := NewFallbackChain(p1, p2)

	if fc.Primary() != p1 {
		t.Fatal("Primary 应返回第一个 provider")
	}
	if len(fc.Fallback()) != 1 {
		t.Fatalf("Fallback 应返回 1 个 provider，得到 %d", len(fc.Fallback()))
	}
	if fc.Fallback()[0] != p2 {
		t.Fatal("Fallback[0] 应返回第二个 provider")
	}
}

func TestFallbackChainSingle(t *testing.T) {
	p1 := &mockProvider{name: "only", modelName: "gpt-4o"}
	fc := NewFallbackChain(p1)

	if fc.Primary() != p1 {
		t.Fatal("Primary 应返回唯一 provider")
	}
	if fc.Fallback() != nil {
		t.Fatal("单 provider 时 Fallback 应返回 nil")
	}
}

func TestFallbackChainEmpty(t *testing.T) {
	fc := NewFallbackChain()
	if fc.Primary() != nil {
		t.Fatal("空链 Primary 应返回 nil")
	}
}

func TestFallbackClientString(t *testing.T) {
	p1 := &mockProvider{name: "openai", modelName: "gpt-4o"}
	p2 := &mockProvider{name: "deepseek", modelName: "deepseek-v4"}
	fc := NewFallbackClient(NewFallbackChain(p1, p2), nil)

	s := fc.String()
	if s != "openai(gpt-4o) → deepseek(deepseek-v4)" {
		t.Fatalf("String() = %q", s)
	}
}

func TestFallbackClientProviderNames(t *testing.T) {
	p1 := &mockProvider{name: "openai", modelName: "gpt-4o"}
	fc := NewFallbackClient(NewFallbackChain(p1), nil)

	names := fc.ProviderNames()
	if len(names) != 1 || names[0] != "openai(gpt-4o)" {
		t.Fatalf("ProviderNames() = %v", names)
	}
}

func TestCostTracker(t *testing.T) {
	ct := NewCostTracker()

	ct.Record("gpt-4o", "R1", 1000, 500, [2]float64{0.005, 0.015})
	ct.Record("gpt-4o", "R3", 2000, 1000, [2]float64{0.005, 0.015})
	ct.Record("deepseek-v4", "R1", 800, 400, [2]float64{0.001, 0.002})

	records := ct.Records()
	if len(records) != 3 {
		t.Fatalf("应有 3 条记录，得到 %d", len(records))
	}

	summary := ct.Summary()
	if len(summary) != 2 {
		t.Fatalf("应有 2 个模型汇总，得到 %d", len(summary))
	}

	gpt := summary["gpt-4o"]
	if gpt.CallCount != 2 {
		t.Fatalf("gpt-4o 调用次数应为 2，得到 %d", gpt.CallCount)
	}
	if gpt.TotalPromptTokens != 3000 {
		t.Fatalf("gpt-4o 总 prompt tokens 应为 3000，得到 %d", gpt.TotalPromptTokens)
	}
	expectedCost := (1000.0*0.005 + 500.0*0.015 + 2000.0*0.005 + 1000.0*0.015) / 1000.0
	if abs64(gpt.TotalCostUSD-expectedCost) > 0.0001 {
		t.Fatalf("gpt-4o 成本应为 %f，得到 %f", expectedCost, gpt.TotalCostUSD)
	}
}

func TestCostTrackerSummary(t *testing.T) {
	ct := NewCostTracker()
	ct.Record("m1", "R1", 100, 50, [2]float64{0, 0})
	ct.Record("m1", "R2", 200, 100, [2]float64{0, 0})

	summary := ct.Summary()
	m1 := summary["m1"]
	if m1.TotalTokens() != 450 {
		t.Fatalf("TotalTokens = %d, want 450", m1.TotalTokens())
	}
	if m1.TotalCostUSD != 0 {
		t.Fatal("免费模型成本应为 0")
	}
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// mockProvider 测试用 Provider。
type mockProvider struct {
	name      string
	modelName string
}

func (m *mockProvider) Name() string                    { return m.name }
func (m *mockProvider) Model() model.BaseChatModel      { return nil }
func (m *mockProvider) ModelName() string               { return m.modelName }
func (m *mockProvider) ContextTokens() int              { return 128000 }
func (m *mockProvider) MaxOutputTokens() int            { return 4096 }
func (m *mockProvider) CostPer1KTokens() (float64, float64) { return 0, 0 }
