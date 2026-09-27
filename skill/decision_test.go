package skill

import (
	"context"
	"encoding/json"
	"fmt"
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
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(`---
name: OCR 评分
description: OCR 结果的三级评分标准（High/Medium/Low）
---

## High

安全漏洞与数据丢失。
`), 0o644)

	// 平铺形式 + 无 frontmatter
	_ = os.WriteFile(filepath.Join(dir, "plain.md"), []byte("# 无元数据\n\n正文内容。"), 0o644)

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
	// checksum 对正文计算（canonical 口径）——存在即可
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

func TestAsSkillToolSelfIdentifying(t *testing.T) {
	// P1 结果自证：出参必须回显解析后规范名/原始入参/校验和——
	// bianque 身份守卫（IdentityGuard）以此校验「声明 A 实得 B」
	p, _ := setupSkills(t)

	st, err := AsSkillTool(p, []string{"ocr-grading"})
	if err != nil {
		t.Fatal(err)
	}
	it := st.(tool.InvokableTool)

	// 规范名调用：Name==Requested==规范名，Checksum 非空
	res, err := it.InvokableRun(context.Background(), `{"name":"ocr-grading"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out useSkillOut
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("出参应為 JSON: %v\n%s", err, res)
	}
	if out.Name != "ocr-grading" || out.Requested != "ocr-grading" {
		t.Fatalf("自证字段不符: %+v", out)
	}
	if len(out.Checksum) != 16 {
		t.Fatalf("Checksum 应为 sha256[:16] hex: %q", out.Checksum)
	}

	// 别名调用：Requested 保留原始入参，Name 归一化为规范名
	res, err = it.InvokableRun(context.Background(), `{"name":"OCR 评分"}`)
	if err != nil {
		t.Fatal(err)
	}
	out = useSkillOut{}
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("出参应為 JSON: %v\n%s", err, res)
	}
	if out.Name != "ocr-grading" || out.Requested != "OCR 评分" {
		t.Fatalf("别名归一化自证不符: %+v", out)
	}

	// 错误路径：不出自证字段（Error 优先）
	res, err = it.InvokableRun(context.Background(), `{"name":"plain"}`)
	if err != nil {
		t.Fatal(err)
	}
	out = useSkillOut{}
	_ = json.Unmarshal([]byte(res), &out)
	if out.Error == "" || out.Name != "" || out.Checksum != "" {
		t.Fatalf("错误路径不应携带自证字段: %+v", out)
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
	name, desc, _, _ := parseFrontmatter("---\nname: \"ocr\"\ndescription: 他说 \"hello\" 结尾\n---\n正文")
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

func TestListPromptSanitizesDescription(t *testing.T) {
	// 出口消毒（v0.10.9）：恶意 Description 的 markdown 结构被中和，
	// 不依赖调用方自觉处理。
	out := ListPrompt([]Meta{{Name: "sop", Description: "正常\n## 伪造标题\n- 伪造条目 <!-- 注释 -->"}})
	if strings.Contains(out, "\n## 伪造标题") || strings.Contains(out, "<!--") {
		t.Fatalf("恶意结构未中和: %s", out)
	}
	if !strings.Contains(out, "sop") {
		t.Fatalf("正常内容应保留: %s", out)
	}
}

// RenderList 清单预算三态（批次五十三沉淀）：不限/降级纯名单/截断注明。
func TestRenderListBudget(t *testing.T) {
	metas := []Meta{
		{Name: "sop", Title: "sop", Description: strings.Repeat("处置标准要点。", 40)},
		{Name: "other", Title: "other", Description: strings.Repeat("另一条要点。", 40)},
	}
	full := RenderList(metas, 0)
	if full != ListPrompt(metas) {
		t.Fatal("budget≤0 应等价 ListPrompt 全额")
	}
	if out := RenderList(metas, 10_000); out != full {
		t.Fatal("预算充足应与全额一致")
	}
	// 超预算降级：保留全部名、丢描述（预算取「描述占位」与「纯名单」之间）。
	degraded := RenderList(metas, 200)
	if !strings.Contains(degraded, "已降级为纯名单") {
		t.Fatalf("超预算应声明降级: %q", degraded)
	}
	for _, m := range metas {
		if !strings.Contains(degraded, m.Name) {
			t.Fatalf("降级形态丢名 %s", m.Name)
		}
	}
	if strings.Contains(degraded, "处置标准") {
		t.Fatal("降级形态不应含描述")
	}
	// 极端截断：长度受控 + 截断声明。
	many := make([]Meta, 0, 500)
	for i := range 500 {
		many = append(many, Meta{Name: fmt.Sprintf("s-%04d", i), Description: "d"})
	}
	trunc := RenderList(many, 400)
	if !strings.Contains(trunc, "已截断") || len(trunc) > 800 {
		t.Fatalf("极端超限应截断且受控: len=%d %q", len(trunc), trunc[:80])
	}
}

// WithContentCap 全文上限：超限截断声明、其余字段保留、≤0 不限。
func TestAsSkillToolContentCap(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "big.md"), []byte("# 大技能\n\n"+strings.Repeat("A", 5000)), 0o644)
	p2 := NewFileProvider(dir)

	bt, err := AsSkillTool(p2, []string{"big"}, WithContentCap(1000))
	if err != nil {
		t.Fatal(err)
	}
	it, ok := bt.(tool.InvokableTool)
	if !ok {
		t.Fatal("应为 InvokableTool")
	}
	raw, err := it.InvokableRun(context.Background(), `{"name":"big"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out useSkillOut
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("出参非 JSON: %v", err)
	}
	if len(out.Content) >= 5000 || !strings.Contains(out.Content, "已截断") {
		t.Fatalf("全文应截断并声明: %d 字节", len(out.Content))
	}
	if out.Name != "big" || out.Version == "" && out.Checksum == "" {
		t.Fatalf("自证字段应保留: %+v", out.Name)
	}
	// 不限形态原样。
	bt2, _ := AsSkillTool(p2, []string{"big"}, WithContentCap(0))
	it2 := bt2.(tool.InvokableTool)
	raw2, _ := it2.InvokableRun(context.Background(), `{"name":"big"}`)
	var out2 useSkillOut
	_ = json.Unmarshal([]byte(raw2), &out2)
	if len(out2.Content) < 5000 || strings.Contains(out2.Content, "已截断") {
		t.Fatal("cap≤0 应不限且原样")
	}
}
