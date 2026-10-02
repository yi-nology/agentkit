package sast

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yi-nology/agentkit/reportutil"
)

type nopLogger struct{}

func (nopLogger) Info(string, ...any) {}
func (nopLogger) Warn(string, ...any) {}

// fakeBin 写一个可执行 shell 脚本（测试专用 fake 工具）。
func fakeBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Fatal("全空应关闭")
	}
	if !(Config{GitleaksBin: "/x/gitleaks"}).Enabled() {
		t.Fatal("配 gitleaks 应开启")
	}
	if !(Config{SemgrepBin: "/x/semgrep", SemgrepConfig: "p/go"}).Enabled() {
		t.Fatal("配 semgrep 应开启")
	}
}

func TestGitleaksScan(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "gitleaks", `
while [ $# -gt 0 ]; do
  if [ "$1" = "--report-path" ]; then
    printf '%s' '[{"RuleID":"aws-access-key-id","Description":"AWS Access Key","File":"cmd/main.go","StartLine":42,"Secret":"AKIA..."}]' > "$2"
  fi
  shift
done
exit 0
`)
	results := Scan(context.Background(),
		Config{GitleaksBin: bin, Timeout: 30 * time.Second}, dir, "main", nil, nopLogger{})
	if len(results) != 1 {
		t.Fatalf("应产出 1 个结果: %d", len(results))
	}
	res := results[0]
	if res.Err != nil {
		t.Fatalf("不应失败: %v", res.Err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	f := res.Findings[0]
	if f.Tool != "gitleaks" || f.Severity != reportutil.High ||
		f.File != "cmd/main.go" || f.Line != 42 || f.RuleID != "aws-access-key-id" {
		t.Fatalf("归一错误: %+v", f)
	}
	// 红线：密钥原文绝不进评论（防泄漏进下游分发面）
	if strings.Contains(f.Comment, "AKIA") {
		t.Fatalf("密钥原文不得出现在意见中: %q", f.Comment)
	}
}

func TestSemgrepScan(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "semgrep", `printf '%s' '{"results":[{"check_id":"go.lang.correct","path":"internal/x/a.go","start":{"line":7,"col":1},"extra":{"severity":"WARNING","message":"err not checked"}}]}'`)
	results := Scan(context.Background(),
		Config{SemgrepBin: bin, SemgrepConfig: "p/go", Timeout: 30 * time.Second},
		dir, "", []string{"internal/x/a.go", "internal/x/b.go"}, nopLogger{})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v", results)
	}
	f := results[0].Findings[0]
	if f.File != "internal/x/a.go" || f.Line != 7 || f.Severity != reportutil.Medium {
		t.Fatalf("归一错误: %+v", f)
	}
	if !strings.Contains(f.Comment, "err not checked") {
		t.Fatalf("评论应含 semgrep message: %q", f.Comment)
	}
}

// FocusPaths 聚焦路径整形：去重/去空/防路径穿越/超限回退全仓。
func TestFocusPaths(t *testing.T) {
	focused := FocusPaths([]string{"a.go", " a.go ", "", "b.go", "../escape.go"})
	if len(focused) != 2 || focused[0] != "a.go" || focused[1] != "b.go" {
		t.Fatalf("整形错误: %v", focused)
	}
	if FocusPaths(nil) != nil {
		t.Fatal("空清单应回退全仓")
	}
	many := make([]string, semgrepMaxPaths+1)
	for i := range many {
		many[i] = "f.go"
	}
	if FocusPaths(many) != nil {
		t.Fatal("超限应回退全仓")
	}
}

// semgrep bin 配了但 config 未配：不产结果（不算覆盖缺口），只告警。
func TestSemgrepNoConfigSkipped(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "semgrep", `exit 0`)
	results := Scan(context.Background(),
		Config{SemgrepBin: bin, Timeout: 30 * time.Second}, dir, "", nil, nopLogger{})
	if len(results) != 0 {
		t.Fatalf("未配 config 不应产出结果: %+v", results)
	}
}

func TestToolFailure(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "gitleaks", `echo "boom" >&2; exit 1`)
	results := Scan(context.Background(),
		Config{GitleaksBin: bin, Timeout: 30 * time.Second}, dir, "main", nil, nopLogger{})
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("失败应产出带 Err 的结果: %+v", results)
	}
}

func TestFindingsCapped(t *testing.T) {
	dir := t.TempDir()
	// 生成 60 条 findings 的 JSON
	var items []string
	for i := 0; i < 60; i++ {
		items = append(items, `{"RuleID":"r","Description":"d","File":"a.go","StartLine":1}`)
	}
	json := `[` + strings.Join(items, ",") + `]`
	bin := fakeBin(t, dir, "gitleaks", strings.ReplaceAll(`
while [ $# -gt 0 ]; do
  if [ "$1" = "--report-path" ]; then printf '%s' '@JSON@' > "$2"; fi
  shift
done
exit 0
`, "@JSON@", json))
	results := Scan(context.Background(),
		Config{GitleaksBin: bin, Timeout: 30 * time.Second}, dir, "", nil, nopLogger{})
	if len(results) != 1 {
		t.Fatalf("results = %d", len(results))
	}
	if len(results[0].Findings) != maxFindingsPerTool {
		t.Fatalf("findings 应封顶 %d, got %d", maxFindingsPerTool, len(results[0].Findings))
	}
	if !strings.Contains(results[0].Notes, "截断") {
		t.Fatalf("封顶应留痕 Notes: %q", results[0].Notes)
	}
}

// GitleaksArgs 命令行构造：带 baseRef 传 --log-opts（diff 范围扫描）+ redact/exit-code 防线。
func TestGitleaksArgs(t *testing.T) {
	withBase := strings.Join(GitleaksArgs("/repo", "origin/main..HEAD"), " ")
	if !strings.Contains(withBase, "--log-opts origin/main..HEAD") {
		t.Fatalf("缺 --log-opts: %s", withBase)
	}
	if !strings.Contains(withBase, "--redact") || !strings.Contains(withBase, "--exit-code 0") {
		t.Fatalf("缺 redact/exit-code 防线: %s", withBase)
	}
	withoutBase := strings.Join(GitleaksArgs("/repo", ""), " ")
	if strings.Contains(withoutBase, "--log-opts") {
		t.Fatal("无 baseRef 不应传 --log-opts")
	}
}

// 空数组报告：零 findings 不算失败。
func TestGitleaksCleanReport(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "gitleaks", `
while [ $# -gt 0 ]; do
  if [ "$1" = "--report-path" ]; then printf '%s' '[]' > "$2"; fi
  shift
done
exit 0
`)
	results := Scan(context.Background(),
		Config{GitleaksBin: bin, Timeout: 30 * time.Second}, dir, "", nil, nopLogger{})
	if len(results) != 1 || results[0].Err != nil || len(results[0].Findings) != 0 {
		t.Fatalf("干净报告应产出空 findings: %+v", results)
	}
}

func TestSemgrepSeverity(t *testing.T) {
	cases := map[string]string{
		"ERROR":   reportutil.High,
		"warning": reportutil.Medium,
		"INFO":    reportutil.Low,
		"":        reportutil.Low,
	}
	for in, want := range cases {
		if got := SemgrepSeverity(in); got != want {
			t.Errorf("SemgrepSeverity(%q)=%q want %q", in, got, want)
		}
	}
}
