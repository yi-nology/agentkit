package codeintel

// 热路径基准（v3.25.1）：索引构建（每 PR 一次）与 GoCallers 查询（LLM 每次工具
// 调用一次）的成本立尺——GoCallers 含 v3.24.1 的按名正则缓存 + 子串预筛。

import (
	"os"
	"path/filepath"
	"testing"
)

// benchIndex 合成 Go 仓：n 个文件 × 每文件 f 个函数（互有调用形态引用）。
func benchIndex(b *testing.B, n, f int) *Index {
	b.Helper()
	dir := b.TempDir()
	for i := 0; i < n; i++ {
		body := "package bench\n\n"
		for j := 0; j < f; j++ {
			body += "func fn" + itoaB(i) + "_" + itoaB(j) + "() int {\n" +
				"\tx := fn" + itoaB((i+1)%n) + "_0()\n" +
				"\treturn x + " + itoaB(j) + "\n}\n\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "f"+itoaB(i)+".go"), []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	idx, err := NewIndex(dir, n+10)
	if err != nil {
		b.Fatal(err)
	}
	return idx
}

func itoaB(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// BenchmarkIndexBuild 索引构建（100 文件 × 20 函数）：go/parser 全量 AST 走查。
func BenchmarkIndexBuild(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 100; i++ {
		body := "package bench\n\n"
		for j := 0; j < 20; j++ {
			body += "func fn" + itoaB(i) + "_" + itoaB(j) + "() int {\n\treturn fn" + itoaB((i+1)%100) + "_0()\n}\n\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "f"+itoaB(i)+".go"), []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := NewIndex(dir, 200); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGoCallers 调用点查询（100 文件索引上查高频符号）：预筛命中率中等形态。
func BenchmarkGoCallers(b *testing.B) {
	idx := benchIndex(b, 100, 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = idx.GoCallers("fn5_0", 50)
	}
}
