package textutil

import (
	"strings"
	"testing"
)

func TestNumberLines(t *testing.T) {
	got := NumberLines("package main\n\nfunc A() {}")
	lines := strings.Split(got, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "   1|") || !strings.HasPrefix(lines[2], "   3|") {
		t.Fatalf("行号格式不符: %q", got)
	}
	if NumberLines("") != "" {
		t.Fatal("空输入应空输出")
	}
	if got := NumberLines("x\n"); strings.HasSuffix(got, "\n") {
		t.Fatalf("尾随换行不应产生幽灵空行: %q", got)
	}
}

func TestSanitizeFileStem(t *testing.T) {
	cases := map[string]string{
		"acme/app#7":          "acme-app-7",
		"group/sub/owner":     "group-sub-owner",
		"feat/branch:main":    "feat-branch-main",
		"a\\b":                "a-b",
		"plain":               "plain",
		"user@host/repo#main": "user-host-repo-main",
	}
	for in, want := range cases {
		if got := SanitizeFileStem(in); got != want {
			t.Fatalf("SanitizeFileStem(%q)=%q，want %q", in, got, want)
		}
	}
}
