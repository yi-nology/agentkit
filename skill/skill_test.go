package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileProviderResolve(t *testing.T) {
	dir := t.TempDir()

	// 创建测试 skill 文件：root/<name>/SKILL.md
	skillDir := filepath.Join(dir, "ocr-grading")
	_ = os.MkdirAll(skillDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# OCR 评分标准\n\nHigh = 安全问题"), 0o644)

	// 创建 root/<name>.md 格式
	_ = os.WriteFile(filepath.Join(dir, "team-style.md"), []byte("# 团队风格"), 0o644)

	p := NewFileProvider(dir)
	ctx := context.Background()

	// 解析 <name>/SKILL.md
	s, err := p.Resolve(ctx, Ref{Name: "ocr-grading"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Content != "# OCR 评分标准\n\nHigh = 安全问题" {
		t.Fatalf("内容不符: %q", s.Content)
	}
	if s.Checksum == "" {
		t.Fatal("缺少 checksum")
	}

	// 解析 <name>.md
	s2, err := p.Resolve(ctx, Ref{Name: "team-style"})
	if err != nil {
		t.Fatal(err)
	}
	if s2.Content != "# 团队风格" {
		t.Fatalf("内容不符: %q", s2.Content)
	}

	// 缓存：第二次 Resolve 应命中缓存
	s3, err := p.Resolve(ctx, Ref{Name: "ocr-grading"})
	if err != nil {
		t.Fatal(err)
	}
	if s3 != s {
		t.Fatal("应命中缓存（同一指针）")
	}
}

func TestFileProviderNotFound(t *testing.T) {
	dir := t.TempDir()
	p := NewFileProvider(dir)

	_, err := p.Resolve(context.Background(), Ref{Name: "nonexistent"})
	if err == nil {
		t.Fatal("不存在的 skill 应报错")
	}
}

func TestFileProviderPathTraversal(t *testing.T) {
	dir := t.TempDir()
	p := NewFileProvider(dir)

	_, err := p.Resolve(context.Background(), Ref{Name: "../../../etc/passwd"})
	if err == nil {
		t.Fatal("路径穿越应被拒绝")
	}
}

func TestFileProviderUnsupportedSource(t *testing.T) {
	dir := t.TempDir()
	p := NewFileProvider(dir)

	_, err := p.Resolve(context.Background(), Ref{Name: "x", Source: "git"})
	if err == nil {
		t.Fatal("非 file 来源应报错")
	}
}

func TestFileProviderVersionedCache(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "my-skill.md"), []byte("v1"), 0o644)

	p := NewFileProvider(dir)
	ctx := context.Background()

	s1, _ := p.Resolve(ctx, Ref{Name: "my-skill"})
	s2, _ := p.Resolve(ctx, Ref{Name: "my-skill", Version: "2"})
	// 不同版本应独立缓存
	if s1 == s2 {
		t.Fatal("不同版本不应共享缓存")
	}
}
