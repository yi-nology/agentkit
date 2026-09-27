package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"_shared/agents.yaml":                 {Data: []byte("api_version: 1\n")},
		"_shared/skills/checklist/SKILL.md":   {Data: []byte("---\nname: 巡检清单\ndescription: 平台巡检\ndeprecated:\n  remove_after: 2099-01-01\n---\n# 巡检\n步骤一\n")},
		"linux/skills/oom-diagnosis/SKILL.md": {Data: []byte("---\nname: OOM 诊断\ndescription: 内存排查\nmode: on_demand\nmaturity: stable\nversion: 1.2.0\nrequires_mcp:\n  - server: node-exporter\n---\nOOM 排查步骤\n")},
		"linux/pack.yaml":                     {Data: []byte("api_version: 1\n")},
		"not-a-pack/skills/broken/SKILL.md":   {Data: []byte("no frontmatter body")},
	}
}

func TestLoadFromFS(t *testing.T) {
	lib, err := LoadFromFS(testFS())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, n := range lib.Names() {
		names[n] = true
	}
	if !names["checklist"] || !names["oom-diagnosis"] || !names["broken"] {
		t.Fatalf("names = %v", lib.Names())
	}

	m := lib.Describe("checklist")
	if m.Title != "巡检清单" || m.Source != "_shared" || m.Maturity != MaturityFrozen {
		t.Fatalf("checklist meta = %+v", m)
	}
	if m.Mode != ModeStatic {
		t.Fatalf("checklist mode = %q", m.Mode)
	}

	m2 := lib.Describe("oom-diagnosis")
	if m2.Mode != ModeOnDemand || m2.Maturity != MaturityStable || m2.Version != "1.2.0" {
		t.Fatalf("oom meta = %+v", m2)
	}
	if len(m2.RequiresMCP) != 1 || m2.RequiresMCP[0].Server != "node-exporter" {
		t.Fatalf("requires_mcp = %+v", m2.RequiresMCP)
	}
	if m2.Source != "linux" {
		t.Fatalf("source = %q", m2.Source)
	}

	full, ok := lib.Get("oom-diagnosis")
	if !ok || full == "" {
		t.Fatal("Get 失败")
	}
	body, ok := lib.Body("oom-diagnosis")
	if !ok || body != "OOM 排查步骤" {
		t.Fatalf("Body = %q", body)
	}
}

func TestLoadFromFSDuplicate(t *testing.T) {
	fsys := fstest.MapFS{
		"_shared/skills/dup/SKILL.md": {Data: []byte("---\nname: A\n---\nx\n")},
		"pkg/skills/dup/SKILL.md":     {Data: []byte("---\nname: B\n---\ny\n")},
	}
	if _, err := LoadFromFS(fsys); err == nil {
		t.Fatal("重名应报错")
	}
}

func TestLibraryProvider(t *testing.T) {
	lib, err := LoadFromFS(testFS())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	sk, err := lib.Resolve(ctx, Ref{Name: "oom-diagnosis"})
	if err != nil {
		t.Fatal(err)
	}
	if sk.Content != "OOM 排查步骤" || sk.Checksum == "" {
		t.Fatalf("Resolve = %+v", sk)
	}
	if _, err := lib.Resolve(ctx, Ref{Name: "missing"}); err == nil {
		t.Fatal("未知名应报错")
	}

	metas, err := lib.ListSkills(ctx)
	if err != nil || len(metas) != 3 {
		t.Fatalf("ListSkills = %v %v", metas, err)
	}

	if n, ok := lib.CanonicalName(context.Background(), "oom-diagnosis"); !ok || n != "oom-diagnosis" {
		t.Fatalf("规范名归一失败: %q %v", n, ok)
	}
	if n, ok := lib.CanonicalName(context.Background(), "OOM 诊断"); !ok || n != "oom-diagnosis" {
		t.Fatalf("展示名归一失败: %q %v", n, ok)
	}
	if _, ok := lib.CanonicalName(context.Background(), "nope"); ok {
		t.Fatal("未知名不应命中")
	}
}

func TestParseRichFrontmatterEmpty(t *testing.T) {
	meta, body, err := ParseRichFrontmatter("x", "---\n---\nbody text\n")
	if err != nil {
		t.Fatalf("空 frontmatter 不应报错: %v", err)
	}
	if body != "body text" {
		t.Fatalf("body = %q", body)
	}
	if meta.Name != "x" || meta.Mode != ModeStatic {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestDeprecationExpired(t *testing.T) {
	m := LibMeta{
		Maturity:   MaturityDeprecated,
		Deprecated: &Deprecated{RemoveAfter: "2020-01-01"},
	}
	after, _ := time.ParseInLocation("2006-01-02", "2026-09-10", time.Local)
	sameDay, _ := time.ParseInLocation("2006-01-02", "2020-01-01", time.Local)
	if !m.DeprecationExpired(after) {
		t.Fatal("过期窗口应 true")
	}
	if m.DeprecationExpired(sameDay) {
		t.Fatal("remove_after 当日应未过期")
	}
	m2 := LibMeta{Maturity: MaturityStable}
	if m2.DeprecationExpired(after) {
		t.Fatal("非 deprecated 应 false")
	}
}

func TestChecksumConsistentAcrossProviders(t *testing.T) {
	// canonical 口径回归：同一份 SKILL.md 经 FileProvider 与 Library 解析，
	// Checksum 必须一致（绑定正文，与 Provider 无关）；Version 取 frontmatter 声明。
	content := "---\nname: 展示名\nversion: 1.2.3\ndescription: d\n---\n\n正文内容 A。\n"
	body := "正文内容 A。"

	fp := NewFileProvider(t.TempDir())
	if err := os.MkdirAll(filepath.Join(fp.Root, "sop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fp.Root, "sop", "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s1, err := fp.Resolve(context.Background(), Ref{Name: "sop", Version: "9.9.9"})
	if err != nil {
		t.Fatal(err)
	}

	lib, err := LoadFromFS(fstest.MapFS{"_shared/skills/sop/SKILL.md": &fstest.MapFile{Data: []byte(content)}})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := lib.Resolve(context.Background(), Ref{Name: "sop"})
	if err != nil {
		t.Fatal(err)
	}

	if s1.Checksum != s2.Checksum {
		t.Fatalf("两 Provider Checksum 应一致: %q vs %q", s1.Checksum, s2.Checksum)
	}
	sum := sha256.Sum256([]byte(body))
	if want := hex.EncodeToString(sum[:])[:16]; s1.Checksum != want {
		t.Fatalf("Checksum 应为 sha256(正文)[:16]=%s，实际 %s", want, s1.Checksum)
	}
	// Version 取 frontmatter 声明，不再回显请求约束。
	if s1.Version != "1.2.3" || s2.Version != "1.2.3" {
		t.Fatalf("Version 应为 frontmatter 声明 1.2.3: %q / %q", s1.Version, s2.Version)
	}
}
