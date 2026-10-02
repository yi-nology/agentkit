// Package codeintel 轻量代码符号索引（收编自 argus internal/codegraph，v0.10.34）：
// 工作副本上以 go/parser / go/scanner 建立符号定义表与标识符出现索引，为审查/
// 问答类 agent 提供 get_symbol（定义定位）、get_references（跨文件影响面）、
// get_callers（词法调用边）的数据面。v1.5 起含 JS/TS/Python 词法符号表。
//
// 边界如实声明：全部是词法级索引（Go 侧"定义/出现"两级，多语言侧正则锚定
// 声明/调用形态），不做类型解析级的精确调用图（那是 Greptile 全库图/LSIF 的
// 领域）。浅克隆工作副本 + CGO_ENABLED=0 构建约束下，纯 Go stdlib 方案是成本
// 与收益的平衡点。误报形态（字符串/注释内命中、动态调用）由工具描述向 agent
// 声明"须回文件读取核实"。全部为确定性代码，不经 LLM。
package codeintel

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/yi-nology/agentkit/textutil"
)

// Symbol 一个顶层符号定义。
type Symbol struct {
	Name      string // 原名（查询大小写不敏感）
	Kind      string // func | type | var | const
	Recv      string // 方法 receiver 类型文本（普通函数为空）
	File      string // 相对索引根的路径
	Line      int
	EndLine   int
	Signature string // 声明源码片段（≤200 runes）
}

// Ref 一个标识符出现位置（文件:行，按行去重）。
type Ref struct {
	File string
	Line int
}

// fileEntry 参与引用查询的已解析文件（引用查询惰性扫描，不预建全量索引）。
type fileEntry struct {
	rel string
	src []byte
	tf  *token.File
}

// Index 代码图索引（只读；多 goroutine 并发查询安全——refs 与 files 均在
// NewIndex 建完后冻结，查询侧零共享可变状态）。
type Index struct {
	defs  map[string][]Symbol // 小写名 → 定义
	refs  map[string][]Ref    // 精确标识符 → 出现位置（含定义行，References 查询时剔除）
	poly  map[string][]PolySymbol
	files []fileEntry
	skip  int // 解析失败文件数（坏语法）
	idxed int
}

// skipDirs 索引跳过的目录名（噪声目录最小集）。
var skipDirs = map[string]bool{
	"vendor": true, "node_modules": true, ".git": true,
	"testdata": true, "dist": true, "third_party": true,
}

// NewIndex 建立目录索引（dir 为工作副本根；maxFiles 封顶防超大仓，0 = 1000）。
// 单文件解析失败跳过计数，不致命。
func NewIndex(dir string, maxFiles int) (*Index, error) {
	if maxFiles <= 0 {
		maxFiles = 1000
	}
	idx := &Index{defs: map[string][]Symbol{}, poly: map[string][]PolySymbol{}}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err // 根不可达：静默返回空索引会误导调用方（第八轮审计）
			}
			return nil // 不可读子树跳过（沙箱竞态容错）
		}
		if d.IsDir() {
			if path != dir && skipDirs[d.Name()] {
				// SkipDir（非 SkipAll）：只跳过该目录内容，兄弟文件继续遍历
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if idx.idxed >= maxFiles {
			return filepath.SkipAll // 封顶即全停（SkipDir 会继续遍历兄弟目录白跑）
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		af, perr := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if perr != nil {
			idx.skip++
			return nil
		}
		idx.idxed++
		tf := fset.File(af.Pos())
		if tf != nil {
			idx.files = append(idx.files, fileEntry{rel: rel, src: src, tf: tf})
		}
		idx.collectDefs(fset, af, src, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	idx.buildRefs()
	poly, perr := scanPolyFiles(dir, maxFiles)
	if perr != nil {
		return nil, fmt.Errorf("codeintel: 扫描 %s: %w", dir, perr)
	}
	idx.poly = poly
	return idx, nil
}

// buildRefs 一次性扫描全部已解析文件，建立 精确标识符 → 出现位置 反向索引。
// References 原先每次查询都全量 go/scanner 重扫（ReAct 反复查同名时 O(files×tokens)）；
// 建索引期一次付清，查询侧 O(1) 查表 + 过滤定义行。含定义行，查询时剔除。
func (x *Index) buildRefs() {
	x.refs = make(map[string][]Ref, 64)
	var sc scanner.Scanner
	for _, f := range x.files {
		// 同名同行去重用 name→line 单层 map（第八轮审计：此前 map-of-maps +
		// 每 token 一次 "rel:line" 拼接，千文件级数百万 token 的构建期内存/
		// 分配放大 2~3 倍）
		lastLine := map[string]int{}
		sc.Init(f.tf, f.src, nil, 0)
		for {
			pos, tok, lit := sc.Scan()
			if tok == token.EOF {
				break
			}
			if tok != token.IDENT {
				continue
			}
			line := f.tf.Line(pos)
			if lastLine[lit] == line {
				continue
			}
			lastLine[lit] = line
			x.refs[lit] = append(x.refs[lit], Ref{File: f.rel, Line: line})
		}
	}
}

// collectDefs 提取顶层 FuncDecl/GenDecl(type/var/const) 定义。
func (x *Index) collectDefs(fset *token.FileSet, af *ast.File, src []byte, rel string) {
	line := func(p token.Pos) int { return fset.Position(p).Line }
	srcSeg := func(from, to token.Pos) string {
		s := src[fset.Position(from).Offset:fset.Position(to).Offset]
		return textutil.TruncEllipsis(strings.TrimSpace(string(s)), 200)
	}
	add := func(name string, s Symbol) {
		s.Name = name
		x.defs[strings.ToLower(name)] = append(x.defs[strings.ToLower(name)], s)
	}
	for _, decl := range af.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			s := Symbol{
				Kind: "func", File: rel,
				Line: line(d.Pos()), EndLine: line(d.End()),
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				s.Recv = srcSeg(d.Recv.List[0].Type.Pos(), d.Recv.List[0].Type.End())
			}
			// 函数签名 = 声明头（到函数体左括号前）
			if d.Body != nil {
				s.Signature = srcSeg(d.Pos(), d.Body.Lbrace)
			} else {
				s.Signature = srcSeg(d.Pos(), d.End())
			}
			if d.Name != nil {
				add(d.Name.Name, s)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					add(sp.Name.Name, Symbol{
						Kind: "type", File: rel,
						Line: line(sp.Pos()), EndLine: line(sp.End()),
						Signature: srcSeg(d.Pos(), d.End()),
					})
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						add(n.Name, Symbol{
							Kind: d.Tok.String(), File: rel,
							Line: line(sp.Pos()), EndLine: line(sp.End()),
							Signature: srcSeg(d.Pos(), d.End()),
						})
					}
				}
			}
		}
	}
}

// Definitions 按名查定义（大小写不敏感，词法索引惯例）。
func (x *Index) Definitions(name string) []Symbol {
	if x == nil {
		return nil
	}
	return x.defs[strings.ToLower(name)]
}

// References 查标识符出现位置（精确大小写——Go 标识符大小写敏感）。
// 定义行自身不计入；按行去重；limit 封顶（<=0 = 50）。
// 走 buildRefs 预建的反向索引：查询 O(1) 查表，不再全量重扫。
func (x *Index) References(name string, limit int) []Ref {
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
	for _, r := range x.refs[name] {
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

// TouchedByLines 返回与给定行区间重叠的符号（变更触达提示的依据）。
// 区间语义为 overlap（改动行落在符号 [Line,EndLine] 内即触达）。
func (x *Index) TouchedByLines(file string, from, to int) []Symbol {
	if x == nil {
		return nil
	}
	var out []Symbol
	for _, syms := range x.defs {
		for _, s := range syms {
			if s.File == file && s.Line <= to && s.EndLine >= from {
				out = append(out, s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { // map 遍历序随机，输出按 (File,Line) 确定化
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// SkippedFiles 解析失败文件数（覆盖可见性）。
func (x *Index) SkippedFiles() int { return x.skip }

// IndexedFiles 成功索引文件数。
func (x *Index) IndexedFiles() int { return x.idxed }
