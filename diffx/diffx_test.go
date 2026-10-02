package diffx

import (
	"strings"
	"testing"
)

func TestSplitUnifiedDiff(t *testing.T) {
	raw := "diff --git a/a.go b/a.go\n+++ b/a.go\n@@ +1 @@\n+line1\ndiff --git b/b.go b/b.go\n+++ b/b.go\n@@ +1 @@\n+line2\n"
	m := SplitUnifiedDiff(raw)
	if len(m) != 2 {
		t.Fatalf("应解析出 2 个文件，got %d", len(m))
	}
	// 块保留 "diff --git " 前缀（存储值与上游 ChangedFile.Diff 形态一致）
	if !strings.HasPrefix(m["a.go"], "diff --git ") {
		t.Fatalf("块应保留前缀: %q", m["a.go"])
	}
	if !strings.Contains(m["a.go"], "+line1") || strings.Contains(m["a.go"], "+line2") {
		t.Fatalf("a.go 块内容错位: %q", m["a.go"])
	}
	if m2 := SplitUnifiedDiff(""); len(m2) != 0 {
		t.Fatalf("空输入应返回空 map，got %d", len(m2))
	}
	// 无前缀首块（罕见形态）补回
	m3 := SplitUnifiedDiff("a/a.go b/a.go\n+++ b/a.go\n+x")
	if len(m3) != 1 || !strings.HasPrefix(m3["a.go"], "diff --git ") {
		t.Fatalf("无前缀首块应补回: %v", m3)
	}
}

func TestDiffPath(t *testing.T) {
	cases := map[string]string{
		"diff --git a/x.go b/x.go\n+++ b/x.go\n@@\n":            "x.go", // 新增/修改
		"diff --git a/d.go b/d.go\n--- a/d.go\n":                "d.go", // 删除文件回退 ---
		"diff --git a/n.go b/n.go\n+++ /dev/null\n--- a/n.go\n": "n.go", // /dev/null 回退
		"+++ b/t.go\t2026-01-02\n":                              "t.go", // 行尾制表符附加信息剥除
		"no markers here\n":                                     "",     // 无标记为空
	}
	for chunk, want := range cases {
		if got := DiffPath(chunk); got != want {
			t.Errorf("DiffPath(%q)=%q want %q", chunk, got, want)
		}
	}
}

func TestHunkLines(t *testing.T) {
	text := "+++ b/a.go\n@@ -1,3 +1,4 @@\n ctx\n+new\n ctx\n+++ b/b.go\n@@ -10,2 +10,2 @@\n-del\n+add\n+++ c.go\n@@ -5,6 +5,0 @@\n-gone\n"
	m := HunkLines(text)
	if len(m["a.go"]) != 1 || m["a.go"][0] != (LineRange{1, 4}) {
		t.Fatalf("a.go 区间错: %v", m["a.go"])
	}
	if len(m["b.go"]) != 1 || m["b.go"][0] != (LineRange{10, 11}) {
		t.Fatalf("b.go 区间错: %v", m["b.go"])
	}
	if _, ok := m["c.go"]; ok {
		t.Fatal("纯删除块新文件侧无行，不应入表")
	}
}
