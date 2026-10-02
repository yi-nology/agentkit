package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// setupSkills 建临时 skill 目录：frontmatter 版 + 无 frontmatter 版。
func setupSkills(t *testing.T) (*FileProvider, string) {
	t.Helper()
	dir := t.TempDir()

	// 目录形式 + frontmatter
	d := filepath.Join(dir, "ocr-grading")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(`---
name: OCR 评分
description: OCR 结果的三级评分标准（High/Medium/Low）
---

## High

安全漏洞与数据丢失。
`), 0o644)

	// 平铺形式 + 无 frontmatter
	os.WriteFile(filepath.Join(dir, "plain.md"), []byte("# 无元数据\n\n正文内容。"), 0o644)

	return NewFileProvider(dir), dir
}

func TestFrontmatterParsed(t *testing.T) {
	p, _ := setupSkills(t)

	s, err := p.Resolve(context.Background(), Ref{Name: "ocr-grading"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "OCR 评分" {
		t.Fatalf("frontmatter name 应覆盖 ref 名: %q", s.Name)
	}
	if s.Description != "OCR 结果的三级评分标准（High/Medium/Low）" {
		t.Fatalf("Description = %q", s.Description)
	}
	// 正文不含 frontmatter 围栏
	if strings.Contains(s.Content, "---") || strings.Contains(s.Content, "name:") {
		t.Fatalf("正文应剥离 frontmatter: %q", s.Content)
	}
	if !strings.Contains(s.Content, "安全漏洞") {
		t.Fatalf("正文缺失: %q", s.Content)
	}
	// checksum 对全文（含 frontmatter）计算——存在即可
	if s.Checksum == "" {
		t.Fatal("缺 checksum")
	}
}

func TestNoFrontmatter(t *testing.T) {
	p, _ := setupSkills(t)

	s, err := p.Resolve(context.Background(), Ref{Name: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Description != "" {
		t.Fatalf("无 frontmatter 应无描述: %q", s.Description)
	}
	if !strings.Contains(s.Content, "正文内容") {
		t.Fatalf("正文应完整保留: %q", s.Content)
	}
}

func TestListSkills(t *testing.T) {
	p, _ := setupSkills(t)

	metas, err := p.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("应发现 2 个 skill，得到 %d", len(metas))
	}
	byName := map[string]Meta{}
	for _, m := range metas {
		byName[m.Name] = m
	}
	// frontmatter 的 name 优先
	if m, ok := byName["OCR 评分"]; !ok || m.Description == "" {
		t.Fatalf("OCR 评分元数据缺失: %+v", byName)
	}
	if _, ok := byName["plain"]; !ok {
		t.Fatalf("plain 缺失: %+v", byName)
	}
}

func TestAsSkillTool(t *testing.T) {
	p, _ := setupSkills(t)

	st, err := AsSkillTool(p, []string{"ocr-grading"})
	if err != nil {
		t.Fatal(err)
	}
	it, ok := st.(tool.InvokableTool)
	if !ok {
		t.Fatal("应实现 tool.InvokableTool")
	}
	// 决策使用全链路：allowed 清单内 → 返回全文
	res, err := it.InvokableRun(context.Background(), `{"name":"ocr-grading"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "安全漏洞") {
		t.Fatalf("use_skill 应返回全文: %s", res)
	}
	// 清单外 → 拒绝
	res, err = it.InvokableRun(context.Background(), `{"name":"plain"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "不在允许清单内") {
		t.Fatalf("清单外应拒绝: %s", res)
	}
}

func TestListPrompt(t *testing.T) {
	out := ListPrompt([]Meta{
		{Name: "ocr-grading", Description: "评分标准"},
		{Name: "no-desc"},
	})
	if !strings.Contains(out, "use_skill") || !strings.Contains(out, "评分标准") ||
		!strings.Contains(out, "（无描述）") {
		t.Fatalf("清单渲染不符:\n%s", out)
	}
	if ListPrompt(nil) != "" {
		t.Fatal("空清单应返回空串")
	}
}

func TestFormatFString(t *testing.T) {
	ctx := context.Background()
	out, err := Format(ctx, "以 {language} 审查，最多 {n} 条。", map[string]any{
		"language": "Go", "n": 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "以 Go 审查，最多 5 条。" {
		t.Fatalf("Format = %q", out)
	}

	// 缺变量：pyfmt 行为——验证不 panic（错误可接受）
	if _, err := Format(ctx, "{missing}", nil); err != nil {
		t.Logf("缺变量返回错误（可接受）: %v", err)
	}
}
