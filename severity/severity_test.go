package severity

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

func TestFingerprint(t *testing.T) {
	fp1 := Fingerprint("main.go", "未处理错误返回值")
	fp2 := Fingerprint("main.go", "未处理错误返回值")
	fp3 := Fingerprint("main.go", "未处理错误返回值！")
	fp4 := Fingerprint("other.go", "未处理错误返回值")

	if fp1 != fp2 {
		t.Error("同输入应产生同指纹")
	}
	if fp1 != fp3 {
		t.Error("标点差异不应影响指纹")
	}
	if fp1 == fp4 {
		t.Error("不同文件应产生不同指纹")
	}
	if len(fp1) != 32 {
		t.Errorf("指纹长度应为 32（SHA256 前 16 字节 hex），得到 %d", len(fp1))
	}
}

func TestNormalizeComment(t *testing.T) {
	if NormalizeComment("Hello World!") != NormalizeComment("hello world") {
		t.Error("大小写和标点不应影响归一化")
	}
	if NormalizeComment("  spaces  ") != NormalizeComment("spaces") {
		t.Error("空白不应影响归一化")
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**", "anything/at/all", true},
		{"*.go", "main.go", true},
		{"*.go", "src/main.go", true}, // basename 匹配
		{"*.vue", "Foo.vue", true},
		{"web/**", "web/src/App.vue", true},
		{"web/**", "web/a/b/c/d.go", true},
		{"web/*", "web/a/b", false},    // * 不跨目录
		{"web/*", "web/App.vue", true}, // * 匹配单段
		{"", "anything", false},
		{"*.go", "main.py", false},
		{"src/**/*.go", "src/a/b/c.go", true},
		{"src/**/*.go", "src/c.go", true},
	}
	for _, c := range cases {
		got := GlobMatch(c.pattern, c.path)
		if got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestGlobMatchGitignoreSemantics(t *testing.T) {
	// 回归：尾随 `/**` 只匹配目录内部，不含目录自身（.gitignore 语义）
	if GlobMatch("web/**", "web") {
		t.Fatal("web/** 不应匹配 web 自身")
	}
	if !GlobMatch("web/**", "web/a.go") {
		t.Fatal("web/** 应匹配目录内部")
	}
	if !GlobMatch("web/**", "web/a/b.go") {
		t.Fatal("web/** 应匹配深层路径")
	}
	// `?` 按 rune 消耗：多字节文件名不漏配
	if !GlobMatch("?.go", "你.go") {
		t.Fatal("? 应消耗一个 rune（多字节文件名）")
	}
	if GlobMatch("??.go", "你.go") {
		t.Fatal("两个 ? 不应被单个 rune 满足")
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
