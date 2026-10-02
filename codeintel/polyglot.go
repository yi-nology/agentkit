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

	"github.com/yi-nology/agentkit/textutil"
)

var pyFuncRe = regexp.MustCompile(`(?m)^(\s*)(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)`)

// jsFuncRe JS/TS 函数声明/赋值/导出（词法形态：function、const/let = (…) =>、
// class 方法不做——方法名与调用形态歧义大，宁缺勿滥）。
var jsFuncRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:function\s+([A-Za-z_$][A-Za-z0-9_$]*)|(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)(?::[^=
]+)?\s*=\s*(?:async\s*)?(?:\(|[A-Za-z_$][A-Za-z0-9_$]*\s*=>))`)

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
func scanPolyFiles(dir string, maxFiles int) (map[string][]PolySymbol, error) {
	out := map[string][]PolySymbol{}
	if maxFiles <= 0 {
		maxFiles = 1000
	}
	count := 0
	var walkErr error
	werr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err // 根不可达上抛（第八轮审计，与 Go 侧同纪律）
			}
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
		var syms []PolySymbol
		if lang == "py" {
			syms = scanPy(text, lang)
		} else {
			syms = scanJS(text, lang)
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
	if werr != nil {
		walkErr = werr
	}
	return out, walkErr
}

// scanPy Python def（含 docstring 首行启发：def 后首个非空行若为三引号/注释）。
func scanPy(text, lang string) []PolySymbol {
	var out []PolySymbol
	for _, m := range pyFuncRe.FindAllStringSubmatchIndex(text, -1) {
		name := text[m[4]:m[5]]
		args := textutil.TruncEllipsis(strings.TrimSpace(text[m[6]:m[7]]), 120)
		line := 1 + strings.Count(text[:m[0]], "\n")
		doc := ""
		if nl := strings.Index(text[m[1]:], "\n"); nl >= 0 {
			// def 头匹配末（m[1]）的下一**物理**行——此前对整段剩余文本
			// TrimSpace 会跨空行塌缩，且无条件取首行（"pass" 级正文也被当
			// 文档；多行参数表取到参数名；第八轮审计）。仅三引号/注释形态提取。
			first := strings.TrimSpace(strings.SplitN(text[m[1]+nl+1:], "\n", 2)[0])
			doc = extractDocLine(first)
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

// GoCallers 按名查调用形态出现位置（pkg.F( 与 F( 两种形态，定义行不计；
// 与 References 的差异：只收调用形态行，并注明词法级）。v0.12.0 起查建索引
// 期与 refs 同遍采集的 calls 表（token 对 IDENT+'('）——正则扫描、正则缓存、
// 子串预筛、逐行匹配四层全部删除：token 扫描无视空白（Serve (w) 命中）、
// 字符串/注释不产 token（假命中根除）、查询 O(1)。
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
	for _, r := range x.calls[name] {
		key := r.File + ":" + strconv.Itoa(r.Line)
		if defLines[key] {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// extractDocLine 文档首行提取：仅三引号 docstring/注释形态生效，
// 剥离定界符后截 120 rune（第八轮审计：此前无条件取首个非空行，
// "pass" 级正文与多行参数表的参数名都会被当文档）。
func extractDocLine(first string) string {
	q3d := string([]byte{'"', '"', '"'})
	q3s := string([]byte{'\'', '\'', '\''})
	if strings.HasPrefix(first, q3d) || strings.HasPrefix(first, q3s) || strings.HasPrefix(first, "#") {
		return textutil.TruncEllipsis(strings.Trim(first, "\"'# \t"), 120)
	}
	return ""
}
