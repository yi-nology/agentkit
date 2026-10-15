package textutil

import "testing"

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
