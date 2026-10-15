package reportutil

import (
	"strings"
	"testing"
)

// 同文案不同行号/文件/十六进制/纯数字 → 同 key；不同 plugin 同文案 → 不同 key。
func TestPatternKeyStabilityAcrossNoise(t *testing.T) {
	base := "未检查 error 返回，可能导致后续逻辑使用空值"
	// 噪声仅限设计约定会剥离的形态：反引号内容 / 路径 / hex / 纯数字
	withNoise := "未检查 error 返回，可能导致后续逻辑使用空值 `handler.go:12` internal/foo/bar.go abcdef012345 42"
	a := PatternKey("security", base)
	b := PatternKey("security", withNoise)
	if a != b {
		t.Fatalf("噪声不应改变 pattern_key: %q vs %q (normA=%q normB=%q)",
			a, b, NormalizePatternText(base), NormalizePatternText(withNoise))
	}
}

func TestPatternKeySeparatesPlugins(t *testing.T) {
	c := "函数过长，建议拆分"
	if PatternKey("security", c) == PatternKey("style", c) {
		t.Fatal("不同 plugin 同文案不得共享 pattern_key")
	}
}

func TestPatternKeyEmptyPlugin(t *testing.T) {
	if PatternKey("", "x") == PatternKey("style", "x") {
		t.Fatal("空 plugin 应落 unknown，不得与具体插件同键")
	}
}

func TestPatternSampleReadable(t *testing.T) {
	s := PatternSample("  未处理的错误返回：`db.Ping()` 失败后继续执行 99 ")
	if s == "" {
		t.Fatal("sample 不应为空")
	}
	if strings.Contains(s, "`") || strings.Contains(s, "db.Ping") {
		t.Fatalf("反引号内容应剥离，got %q", s)
	}
	if len([]rune(s)) > PatternTextMax {
		t.Fatalf("sample 超长: %d", len([]rune(s)))
	}
}

// 置信度：仅 Posted 缓升但不过 0.5 太多；Regret/Suppress 下穿；Restore 回抬。
func TestBetaConfidenceDirection(t *testing.T) {
	base := BetaConfidence(0, 0, 0, 0)
	if base < 0.49 || base > 0.51 {
		t.Fatalf("先验应为 0.5, got %v", base)
	}
	if BetaConfidence(3, 0, 0, 0) <= base {
		t.Fatal("Posted 应抬高置信度")
	}
	if BetaConfidence(0, 2, 0, 0) >= base {
		t.Fatal("Regret 应压低置信度")
	}
	if BetaConfidence(0, 0, 1, 0) >= BetaConfidence(0, 1, 0, 0) {
		t.Fatal("Suppress 权重应不低于 Regret")
	}
	if BetaConfidence(0, 2, 1, 2) <= BetaConfidence(0, 2, 1, 0) {
		t.Fatal("Restore 应回抬置信度")
	}
}

func TestBetaConfidenceHardFloorScenario(t *testing.T) {
	// 设计稿场景：Regret×2 + Suppress×1 → 应明显低于 0.35 SoftFloor
	c := BetaConfidence(0, 2, 1, 0)
	if c >= 0.35 {
		t.Fatalf("regret2+suppress1 应低于 soft floor, got %v", c)
	}
	// samples 门槛
	if PatternSamples(1, 2, 1) != 4 {
		t.Fatalf("samples=%d want 4", PatternSamples(1, 2, 1))
	}
}

// 成对循环不洗白（v3.8.0 在案回归）：suppress+restore 反复成对，置信度必须
// 单调不升（restore 只撤销部分负证据，不产生正证据）。
func TestBetaConfidencePairLoopNoWhitewash(t *testing.T) {
	c0 := BetaConfidence(1, 0, 0, 0)
	c1 := BetaConfidence(1, 0, 1, 1)
	c2 := BetaConfidence(1, 0, 2, 2)
	if c1 >= c0 || c2 >= c1 {
		t.Fatalf("suppress+restore 成对循环应单调不升: %v %v %v", c0, c1, c2)
	}
}

func TestNormalizePatternStripsPathsAndHex(t *testing.T) {
	got := NormalizePatternText("见 internal/storage/prints.go 与 fp abcdef123456")
	if strings.Contains(got, "prints.go") || strings.Contains(got, "abcdef") {
		t.Fatalf("路径/hex 应剥离: %q", got)
	}
}

// TestBetaConfidenceRestoreNeverExceedsPrior 回归（第八轮审计）：restore 超量
// 的进参形态不得越过先验置信度——此前下限钳 1（低于先验 β=2），
// BetaConfidence(0,0,1,50) 得 0.667 > 0.5，「不可洗白」不闭合。
func TestBetaConfidenceRestoreNeverExceedsPrior(t *testing.T) {
	if c := BetaConfidence(0, 0, 1, 50); c > 0.5 {
		t.Fatalf("restore 超量不得越过先验: %f", c)
	}
	if c := BetaConfidence(0, 0, 3, 1000); c > 0.5 {
		t.Fatalf("极端 restore 同理: %f", c)
	}
}
