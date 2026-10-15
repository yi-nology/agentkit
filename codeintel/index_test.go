package codeintel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture 跨文件 Go 工程：pkg 定义 + main 调用 + vendor 噪声 + 坏语法文件。
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "api.go"), `package app

import "fmt"

// Handler 处理入口。
type Handler struct{ Name string }

func (h *Handler) Serve(w Writer) error {
	if w == nil {
		return ErrNil
	}
	fmt.Println(h.Name)
	return nil
}

var ErrNil = errNil{}

type errNil struct{}
`)
	mustWrite(t, filepath.Join(dir, "writer.go"), `package app

// Writer 输出接口。
type Writer interface{ Write(p []byte) (int, error) }
`)
	mustWrite(t, filepath.Join(dir, "main.go"), `package main

import "app"

func main() {
	h := &app.Handler{Name: "x"}
	_ = h.Serve(nil)
}
`)
	mustWrite(t, filepath.Join(dir, "util.go"), `package app
func broken( {
`)
	mustWrite(t, filepath.Join(dir, "vendor", "x.go"), `package vendored
func Helper() {}
`)
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewIndexDefinitions(t *testing.T) {
	idx, err := NewIndex(fixture(t), 100)
	if err != nil {
		t.Fatal(err)
	}
	defs := idx.Definitions("Serve")
	if len(defs) != 1 {
		t.Fatalf("Serve 定义数 = %d", len(defs))
	}
	d := defs[0]
	if d.Kind != "func" || !strings.Contains(d.Recv, "Handler") ||
		d.File != "api.go" || d.Line != 8 {
		t.Fatalf("Serve 定义错误: %+v", d)
	}
	if !strings.Contains(d.Signature, "Serve(w Writer) error") {
		t.Fatalf("签名错误: %q", d.Signature)
	}
	// 类型与变量
	if len(idx.Definitions("Handler")) != 1 || idx.Definitions("Handler")[0].Kind != "type" {
		t.Fatalf("Handler 定义错误: %+v", idx.Definitions("Handler"))
	}
	if len(idx.Definitions("Writer")) != 1 {
		t.Fatalf("跨文件类型定义丢失: %+v", idx.Definitions("Writer"))
	}
	// 大小写不敏感查找；同名不同 Kind（type errNil / var ErrNil）小写同键共存
	if len(idx.Definitions("errnil")) < 1 {
		t.Fatal("应按小写名索引")
	}
	if len(idx.Definitions("NoSuchSym")) != 0 {
		t.Fatal("未知符号应为空")
	}
}

func TestNewIndexReferences(t *testing.T) {
	idx, err := NewIndex(fixture(t), 100)
	if err != nil {
		t.Fatal(err)
	}
	refs := idx.References("Handler", 50)
	found := false
	for _, r := range refs {
		if r.File == "main.go" && r.Line == 6 {
			found = true
		}
		// 定义行自身不算引用
		if r.File == "api.go" && r.Line == 6 {
			t.Fatalf("定义行不应计为引用: %+v", r)
		}
	}
	if !found {
		t.Fatalf("应找到 main.go:6 的跨文件引用: %+v", refs)
	}
	if len(idx.References("Handler", 1)) != 1 {
		t.Fatal("limit 未生效")
	}
}

func TestTouchedByLines(t *testing.T) {
	idx, err := NewIndex(fixture(t), 100)
	if err != nil {
		t.Fatal(err)
	}
	touched := idx.TouchedByLines("api.go", 11, 15)
	found := false
	for _, s := range touched {
		if s.Name == "Serve" {
			found = true
		}
	}
	if !found {
		t.Fatalf("11-15 行变更应触达 Serve（8-14 行,按区间重叠）: %+v", touched)
	}
	if len(idx.TouchedByLines("api.go", 100, 200)) != 0 {
		t.Fatal("未触达任何符号应返回空")
	}
}

// 坏语法与 vendor 文件跳过；解析失败的文件不致命。
func TestNewIndexSkips(t *testing.T) {
	idx, err := NewIndex(fixture(t), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Definitions("Helper")) != 0 {
		t.Fatal("vendor 下不应索引")
	}
	if idx.SkippedFiles() == 0 {
		t.Fatal("坏语法文件应计入 skipped")
	}
}

// maxFiles 封顶防超大仓炸内存。
func TestNewIndexMaxFiles(t *testing.T) {
	idx, err := NewIndex(fixture(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	if idx.IndexedFiles() > 2 {
		t.Fatalf("indexed = %d, want <= 2", idx.IndexedFiles())
	}
}
