// polyglot.go —— 多语言词法符号表（JS/TS/Python）。
//
// 边界如实声明：JS/TS/Python 与 Go 的 get_callers 全部是**词法级**索引
// （正则锚定声明/调用形态），不做类型解析——与 Go 侧 go/scanner 同级质量，
// 零新增依赖、零 cgo（CGO_ENABLED=0 兼容）。误报形态（字符串/注释内命中、
// 动态调用）与 Go 词法索引一致，由工具描述向 agent 声明"须回 get_file 核实"。
package codeintel

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/yi-nology/agentkit/textutil"
)

// skipExts 索引的语言扩展（v1.5 起 JS/TS/Python 与 Go 并列）。
var pyFuncRe = regexp.MustCompile(`(?m)^(\s*)def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)`)

// jsFuncRe JS/TS 函数声明/赋值/导出（词法形态：function、const/let = (…) =>、
// class 方法不做——方法名与调用形态歧义大，宁缺勿滥）。
var jsFuncRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?(?:function\s+([A-Za-z_$][A-Za-z0-9_$]*)|(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s*)?\()`)

// goCallRe Go 调用形态（pkg.Name( / Name(），供 get_callers 惰性扫描。
var goCallRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.]*\.[A-Za-z_][A-Za-z0-9_]*\(|[A-Za-z_][A-Za-z0-9_]*\(`)

// PolySymbol 非 Go 语言的词法符号。
type PolySymbol struct {
	Name string
	Lang string // py | js
	File string // 相对索引根
	Line int
	Args string // 参数表原文（≤120 runes）
	Doc  string // 前置 docstring/注释首行（≤120 runes，可空）
}

// scanPolyFiles 扫描 dir 下 .py/.js/.ts/.tsx 文件建词法符号表（上限与 Go 共享
// maxFiles 语义由调用方控制；这里独立 cap 防大仓重复遍历）。
func scanPolyFiles(dir string, maxFiles int) map[string][]PolySymbol {
	out := map[string][]PolySymbol{}
	if maxFiles <= 0 {
		maxFiles = 1000
	}
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if count >= maxFiles {
			return filepath.SkipAll
		}
		ext := filepath.Ext(path)
		var lang string
		switch ext {
		case ".py":
			lang = "py"
		case ".js", ".ts", ".tsx", ".jsx":
			lang = "js"
		default:
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return nil
		}
		count++
		text := string(src)
		syms := scanPy(text, lang)
		if lang == "js" {
			syms = append(syms, scanJS(text, lang)...)
		}
		for i := range syms {
			syms[i].File = filepath.ToSlash(rel)
		}
		for _, s := range syms {
			key := strings.ToLower(s.Name)
			out[key] = append(out[key], s)
		}
		return nil
	})
	return out
}

// scanPy Python def（含 docstring 首行启发：def 后首个非空行若为三引号/注释）。
func scanPy(text, lang string) []PolySymbol {
	var out []PolySymbol
	for _, m := range pyFuncRe.FindAllStringSubmatchIndex(text, -1) {
		name := text[m[4]:m[5]]
		args := textutil.TruncEllipsis(strings.TrimSpace(text[m[6]:m[7]]), 120)
		line := 1 + strings.Count(text[:m[0]], "\n")
		doc := ""
		if i := strings.Index(text[m[2]:], "\n"); i >= 0 {
			rest := strings.TrimSpace(text[m[2]+i+1:])
			doc = textutil.TruncEllipsis(strings.Split(rest, "\n")[0], 120)
		}
		out = append(out, PolySymbol{Name: name, Lang: lang, Line: line, Args: args, Doc: doc})
	}
	return out
}

// scanJS JS/TS 词法函数声明（组 2=function 名，组 4=const/let/var 赋值名）。
func scanJS(text, lang string) []PolySymbol {
	var out []PolySymbol
	for _, m := range jsFuncRe.FindAllStringSubmatchIndex(text, -1) {
		s, e := m[2], m[3]
		if s < 0 {
			s, e = m[4], m[5]
		}
		if s < 0 {
			continue
		}
		line := 1 + strings.Count(text[:m[0]], "\n")
		out = append(out, PolySymbol{Name: text[s:e], Lang: lang, Line: line})
	}
	return out
}

// PolyDefinitions 按名查非 Go 符号（大小写不敏感；Go 侧命中优先——同名冲突
// 时 Go 索引是权威）。
func (x *Index) PolyDefinitions(name string) []PolySymbol {
	if x == nil {
		return nil
	}
	return x.poly[strings.ToLower(name)]
}

// goCallersRes 单次查询的调用形态正则缓存（pattern 依赖查询名，按名复用编译
// 结果——LLM 对同一符号可能连问多次）。
var goCallersRes sync.Map // name → *regexp.Regexp

// GoCallers 惰性扫描 Go 标识符的调用形态出现位置（pkg.F( 与 F( 两种形态，
// 定义行不计；与 References 的差异：只收调用形态行，并注明词法级）。
func (x *Index) GoCallers(name string, limit int) []Ref {
	if x == nil || name == "" {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	defLines := map[string]bool{}
	for _, s := range x.defs[strings.ToLower(name)] {
		defLines[s.File+":"+strconv.Itoa(s.Line)] = true
	}
	var out []Ref
	seen := map[string]bool{}
	callRe, _ := goCallersRes.LoadOrStore(name, regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\s*\(`))
	re := callRe.(*regexp.Regexp)
	for _, f := range x.files {
		if !strings.Contains(string(f.src), name) {
			continue // 子串预筛：正则要求名字面出现，不含者整文件跳过
		}
		for i, line := range strings.Split(string(f.src), "\n") {
			key := f.rel + ":" + strconv.Itoa(i+1)
			if defLines[key] || seen[key] {
				continue
			}
			if re.MatchString(line) && goCallRe.MatchString(line) {
				seen[key] = true
				out = append(out, Ref{File: f.rel, Line: i + 1})
				if len(out) >= limit {
					return out
				}
			}
		}
	}
	return out
}
