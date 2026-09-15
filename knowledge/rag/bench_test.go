package rag

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// benchDir 生成 n 篇 markdown 语料（每篇 8 个标题段 × 每段 ~600 rune）。
func benchDir(b *testing.B, n int) string {
	b.Helper()
	dir := b.TempDir()
	for i := 0; i < n; i++ {
		var doc bytes.Buffer
		fmt.Fprintf(&doc, "# 文档 %d\n\n", i)
		for s := 0; s < 8; s++ {
			fmt.Fprintf(&doc, "## 第 %d 篇第 %d 节：编码规范与部署约定\n\n", i, s)
			fmt.Fprintf(&doc, "本节描述 Go 服务的错误处理规范，所有 error 返回值必须显式处理，"+
				"禁止用下划线忽略；并发场景共享状态必须用互斥锁保护，goroutine 必须有退出机制；"+
				"配置统一从 Nacos 下发，禁止硬编码；部署使用 ArgoCD GitOps 流水线，镜像 tag 使用 commit SHA。\n\n")
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("doc-%03d.md", i)), doc.Bytes(), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return dir
}

func BenchmarkLocalRetrieve(b *testing.B) {
	cases := []struct {
		docs int
	}{
		{docs: 10},   // ~80 chunks
		{docs: 100},  // ~800 chunks
		{docs: 1000}, // ~8000 chunks
	}
	for _, c := range cases {
		dir := benchDir(b, c.docs)
		local, err := NewLocal(dir)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("docs=%d", c.docs), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := local.Retrieve(context.Background(), "错误处理 互斥锁 ArgoCD", 5, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLocalFirstRetrieveAfterRescan(b *testing.B) {
	// 重扫成本（模拟启动/过期后首次查询的延迟上界）
	dir := benchDir(b, 100)
	local, err := NewLocal(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		local.Rescan()
		if _, err := local.Retrieve(context.Background(), "规范", 5, nil); err != nil {
			b.Fatal(err)
		}
	}
}
