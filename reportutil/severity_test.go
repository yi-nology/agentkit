package reportutil

import (
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"high", High, true},
		{"HIGH", High, true},
		{"critical", High, true},
		{"严重", High, true},
		{"medium", Medium, true},
		{"MEDIUM", Medium, true},
		{"warning", Medium, true},
		{"low", Low, true},
		{"info", Low, true},
		{"unknown", Low, false},
		{"", Low, false},
	}
	for _, c := range cases {
		got, ok := Normalize(c.input)
		if got != c.want || ok != c.ok {
			t.Errorf("Normalize(%q) = (%q, %v), want (%q, %v)", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestRank(t *testing.T) {
	if Rank(High) <= Rank(Medium) {
		t.Error("high 应比 medium 严重")
	}
	if Rank(Medium) <= Rank(Low) {
		t.Error("medium 应比 low 严重")
	}
}

func TestHigher(t *testing.T) {
	if Higher(High, Medium) != High {
		t.Error("Higher(high, medium) 应为 high")
	}
	if Higher(Low, High) != High {
		t.Error("Higher(low, high) 应为 high")
	}
	if Higher(Medium, Medium) != Medium {
		t.Error("Higher(medium, medium) 应为 medium")
	}
}

func TestValid(t *testing.T) {
	if !Valid(High) || !Valid(Medium) || !Valid(Low) {
		t.Error("high/medium/low 应有效")
	}
	if Valid("critical") || Valid("") {
		t.Error("critical/空 应无效")
	}
}

// 别名词表（v0.10.5）：外部审查专家常用 P0/nit 等通用研发词表。
func TestNormalizeAliases(t *testing.T) {
	high := map[string]string{"P0": High, "fatal": High, "URGENT": High}
	for in, want := range high {
		if got, ok := Normalize(in); got != want || !ok {
			t.Fatalf("Normalize(%q)=%q,%v，want %q,true", in, got, ok, want)
		}
	}
	if got, ok := Normalize("p1"); got != Medium || !ok {
		t.Fatalf("p1 应 medium: %q %v", got, ok)
	}
	for _, in := range []string{"p2", "nit", "nits", "trivial"} {
		if got, ok := Normalize(in); got != Low || !ok {
			t.Fatalf("%q 应 low（合法词）: %q %v", in, got, ok)
		}
	}
	// 原词表行为不变
	if got, ok := Normalize("critical"); got != High || !ok {
		t.Fatalf("critical 应 high: %q %v", got, ok)
	}
	if _, ok := Normalize("gibberish"); ok {
		t.Fatal("越界词仍应 false")
	}
}
