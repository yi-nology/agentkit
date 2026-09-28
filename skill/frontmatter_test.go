package skill

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestFileProviderBOMRegression 回归（v0.10.30 第四轮审计）：parseFrontmatter
// 也必须剥 BOM——FileProvider.Resolve 主加载路径曾整份当正文、元数据静默丢失。
func TestFileProviderBOMRegression(t *testing.T) {
	dir := t.TempDir()
	skillDir := dir + "/bom-skill"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "\xef\xbb\xbf---\nname: bom-skill\ndescription: 带 BOM 的技能\n---\n\n正文内容\n"
	if err := os.WriteFile(skillDir+"/SKILL.md", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewFileProvider(dir)
	metas, err := p.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Name != "bom-skill" || metas[0].Description != "带 BOM 的技能" {
		t.Fatalf("BOM 路径元数据丢失: %+v", metas)
	}
	sk, err := p.Resolve(context.Background(), Ref{Name: "bom-skill"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sk.Content, "---") || !strings.Contains(sk.Content, "正文内容") {
		t.Fatalf("正文不应含 frontmatter 围栏: %q", sk.Content)
	}
}

// TestFileProviderCRLFRegression 回归（第五轮审计）：CRLF 行尾的起始围栏——
// parseFrontmatter 曾要求围栏后紧跟 \n 而 \r\n 直接 bail，整份当正文、元数据
// 丢失且与 Library 路径的 Content/Checksum 分歧。
func TestFileProviderCRLFRegression(t *testing.T) {
	dir := t.TempDir()
	skillDir := dir + "/crlf-skill"
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\r\nname: crlf-skill\r\ndescription: CRLF 技能\r\n---\r\n\r\n正文内容\r\n"
	if err := os.WriteFile(skillDir+"/SKILL.md", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewFileProvider(dir)
	metas, err := p.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Name != "crlf-skill" || metas[0].Description != "CRLF 技能" {
		t.Fatalf("CRLF 路径元数据丢失: %+v", metas)
	}
	sk, err := p.Resolve(context.Background(), Ref{Name: "crlf-skill"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sk.Content, "---") || !strings.Contains(sk.Content, "正文内容") {
		t.Fatalf("正文不应含围栏: %q", sk.Content)
	}
}
