package rag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalRetrieve(t *testing.T) {
	dir := t.TempDir()
	// 创建测试 markdown 文件
	os.WriteFile(filepath.Join(dir, "coding-style.md"), []byte(`# 编码规范

## Go 代码风格

每个函数不超过 50 行。
错误必须显式处理，不得用 _ 忽略。
变量命名使用驼峰式。

## Python 代码风格

使用 snake_case 命名。
每行不超过 120 字符。
`), 0o644)

	os.WriteFile(filepath.Join(dir, "deploy.md"), []byte(`# 部署规范

## Nacos 配置

所有服务配置必须通过 Nacos 下发。
禁止在代码中硬编码配置项。

## ArgoCD 部署

使用 ArgoCD 进行 GitOps 部署。
镜像 tag 必须使用 git commit SHA。
`), 0o644)

	local, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 检索 "Go 代码风格"
	chunks, err := local.Retrieve(ctx, "Go 代码风格", 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) == 0 {
		t.Fatal("应检索到结果")
	}
	// 结果应包含编码规范相关内容（"函数"、"命名"、"代码"等）
	found := false
	for _, c := range chunks {
		if contains(c.Content, "函数") || contains(c.Content, "命名") || contains(c.Content, "编码") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Top 结果应包含编码规范相关内容，得到: %q", chunks[0].Content[:min(80, len(chunks[0].Content))])
	}

	// 检索 "ArgoCD 部署"
	chunks2, err := local.Retrieve(ctx, "ArgoCD 部署", 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks2) == 0 {
		t.Fatal("应检索到 ArgoCD 结果")
	}
	found2 := false
	for _, c := range chunks2 {
		if contains(c.Content, "ArgoCD") || contains(c.Content, "GitOps") {
			found2 = true
			break
		}
	}
	if !found2 {
		t.Fatal("Top 结果应包含 ArgoCD 相关内容")
	}
}

func TestLocalRetrieveTopK(t *testing.T) {
	dir := t.TempDir()
	// 创建多个文件
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(dir, "doc"+string(rune('0'+i))+".md"),
			[]byte("# 文档 "+string(rune('0'+i))+"\n内容关于测试和规范。"), 0o644)
	}

	local, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}

	chunks, _ := local.Retrieve(context.Background(), "测试规范", 3, nil)
	if len(chunks) > 3 {
		t.Fatalf("topK=3 应最多返回 3 个结果，得到 %d", len(chunks))
	}
}

func TestLocalRetrieveFilter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.md"), []byte("# Go 规范\n使用 errcheck。"), 0o644)
	os.WriteFile(filepath.Join(dir, "py.md"), []byte("# Python 规范\n使用 pylint。"), 0o644)

	local, _ := NewLocal(dir)

	// 过滤只看 go.md
	chunks, _ := local.Retrieve(context.Background(), "规范", 5, Filter{"file": "go.md"})
	for _, c := range chunks {
		if c.Metadata["file"] != "go.md" {
			t.Fatalf("过滤后应只返回 go.md，得到 %s", c.Metadata["file"])
		}
	}
}

func TestLocalRetrieverEmptyQuery(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.md"), []byte("# X"), 0o644)
	local, _ := NewLocal(dir)

	chunks, _ := local.Retrieve(context.Background(), "", 5, nil)
	if len(chunks) != 0 {
		t.Fatal("空查询应返回空结果")
	}
}

func TestLocalRescan(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "v1.md"), []byte("# 版本1\n初始内容。"), 0o644)

	local, _ := NewLocal(dir)
	chunks1, _ := local.Retrieve(context.Background(), "初始内容", 5, nil)
	if len(chunks1) == 0 {
		t.Fatal("v1 应有结果")
	}

	// 添加新文件后强制重扫描
	local.mu.Lock()
	local.scannedAt = time.Time{} // 清除扫描时间，触发重扫
	local.mu.Unlock()

	os.WriteFile(filepath.Join(dir, "v2.md"), []byte("# 版本2\n新增内容关于部署。"), 0o644)
	chunks2, _ := local.Retrieve(context.Background(), "部署", 5, nil)
	found := false
	for _, c := range chunks2 {
		if contains(c.Content, "部署") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("重扫描后应能找到新文件内容")
	}
}

func TestLocalAsTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.md"), []byte("# 测试\n工具化检索。"), 0o644)
	local, _ := NewLocal(dir)

	tool := local.AsTool()
	if tool == nil {
		t.Fatal("AsTool 应返回非 nil")
	}
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "search_knowledge" {
		t.Fatalf("工具名应为 search_knowledge，得到 %s", info.Name)
	}
}

func TestNewLocalNotDir(t *testing.T) {
	f, _ := os.CreateTemp("", "test-*.md")
	f.Close()
	defer os.Remove(f.Name())

	_, err := NewLocal(f.Name())
	if err == nil {
		t.Fatal("传入文件路径应报错")
	}
}

func TestNewLocalNotExist(t *testing.T) {
	_, err := NewLocal("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("不存在的路径应报错")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
