package codeintel

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// v3.22.0 codegraph v1.5：Python/JS/TS 词法符号表 + Go 词法调用边（get_callers）。
func TestScanPolyFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "util.py"), "def calc(a, b):\n    \"\"\"求和\"\"\"\n    return a + b\n\ndef _hidden():\n    pass\n")
	mustWrite(t, filepath.Join(dir, "app.js"), "function handler(req) { return req; }\nconst run = () => 1;\n")
	mustWrite(t, filepath.Join(dir, "node_modules", "skip.js"), "function nope() {}\n")
	idx, err := NewIndex(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	defs := idx.PolyDefinitions("calc")
	if len(defs) != 1 || defs[0].Lang != "py" || defs[0].File != "util.py" || defs[0].Line != 1 {
		t.Fatalf("py 定义错误: %+v", defs)
	}
	if !strings.Contains(defs[0].Args, "a, b") {
		t.Fatalf("参数表错误: %q", defs[0].Args)
	}
	if len(idx.PolyDefinitions("handler")) != 1 || idx.PolyDefinitions("handler")[0].Lang != "js" {
		t.Fatalf("js 定义错误: %+v", idx.PolyDefinitions("handler"))
	}
	if len(idx.PolyDefinitions("run")) != 1 {
		t.Fatalf("const 箭头函数应命中: %+v", idx.PolyDefinitions("run"))
	}
	if len(idx.PolyDefinitions("nope")) != 0 {
		t.Fatal("node_modules 不应索引")
	}
	if idx.PolyDefinitions("NoSuch") != nil {
		t.Fatal("未知符号应为空")
	}
}

func TestGoCallers(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package app\n\nfunc Serve() error { return nil }\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nimport \"app\"\n\nfunc main() {\n\t_ = app.Serve()\n\t_ = Serve()\n\t// Serve() 注释里的调用\n\t_ = \"Serve() 字符串\"\n}\n")
	idx, err := NewIndex(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	refs := idx.GoCallers("Serve", 50)
	found := map[string]bool{}
	for _, r := range refs {
		found[r.File+":"+strconv.Itoa(r.Line)] = true
	}
	if !found["main.go:6"] || !found["main.go:7"] {
		t.Fatalf("调用形态应命中 pkg.F( 与 F(: %v", refs)
	}
	// 定义行不计；注释/字符串行按词法口径如实计入（工具描述声明须核实）
	if found["a.go:3"] {
		t.Fatalf("定义行不应计入: %v", refs)
	}
	if len(idx.GoCallers("Serve", 1)) != 1 {
		t.Fatal("limit 未生效")
	}
}
