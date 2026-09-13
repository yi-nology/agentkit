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
	// S1 契约：Name = 规范引用名（use_skill/allowed 依据），frontmatter name 降为展示别名 Title
	if m, ok := byName["ocr-grading"]; !ok || m.Description == "" {
		t.Fatalf("ocr-grading 元数据缺失: %+v", byName)
	} else if m.Title != "OCR 评分" {
		t.Fatalf("frontmatter name 应作为展示别名 Title: %+v", m)
	}
	if m, ok := byName["plain"]; !ok || m.Title != "" {
		t.Fatalf("plain 应无别名: %+v", m)
	}
}

func TestAsSkillToolAliasResolution(t *testing.T) {
	// S1 回归：模型用清单展示名（frontmatter name）回填 use_skill 也必须能闭环
	p, _ := setupSkills(t)

	st, err := AsSkillTool(p, []string{"ocr-grading"})
	if err != nil {
		t.Fatal(err)
	}
	it := st.(tool.InvokableTool)
	res, err := it.InvokableRun(context.Background(), `{"name":"OCR 评分"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res, "不在允许清单内") || strings.Contains(res, "未找到") {
		t.Fatalf("展示名应被归一化为规范名后加载: %s", res)
	}
	if !strings.Contains(res, "安全漏洞") {
		t.Fatalf("别名调用应返回全文: %s", res)
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

func TestFrontmatterQuotePairTrim(t *testing.T) {
	// 值整体被引号包裹才剥除；内部合法引号保留
	name, desc, _ := parseFrontmatter("---\nname: \"ocr\"\ndescription: 他说 \"hello\" 结尾\n---\n正文")
	if name != "ocr" {
		t.Fatalf("成对引号应剥除: %q", name)
	}
	if desc != `他说 "hello" 结尾` {
		t.Fatalf("内部合法引号不得被剥: %q", desc)
	}
}

func TestResolveRejectsDotDotSegmentOnly(t *testing.T) {
	p := NewFileProvider(t.TempDir())
	// 段级 .. 仍拒绝
	if _, err := p.Resolve(context.Background(), Ref{Name: "../etc"}); err == nil {
		t.Fatal("段级 .. 应拒绝")
	}
	// 含 .. 的合法名不再被误伤（目录不存在时错误是"未找到"而非"非法引用"）
	_, err := p.Resolve(context.Background(), Ref{Name: "v1..2"})
	if err == nil || strings.Contains(err.Error(), "非法引用") {
		t.Fatalf("foo..bar 形态的合法名不应被拒: %v", err)
	}
}
