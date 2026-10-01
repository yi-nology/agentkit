// Package diffx unified diff 解析纯函数（收编自 argus internal/context
// diffprep 与 runner/inject，v0.10.35）：按 "diff --git" 切分多文件 diff、
// 提取目标路径、提取 hunk 新行区间。纯函数、零依赖、不改输入。
//
// 边界：解析面只覆盖 git 风格 unified diff 的结构性标记（+++ / @@ 行），
// 不做语义级 diff（那是 go-git-platform diff 侧的领域）；路径以 +++ b/<path>
// 为准（新增/修改），删除文件回退 --- a/<path>。
//
// v0.12.0 解析强化：quoted path（`+++ b/"a b.go"`，git 对含空格/特殊字符
// 路径的 C 风格转义——去引号并反转义）、--no-prefix 形态（`+++ <path>` 无
// b/ 侧前缀，diff.noprefix 配置用户常见）、CRLF 残留剥除，三形态统一收口在
// headerPath。DiffPath 的识别止于首个 @@（hunk 体内容行不参与——正文
// `++ b/foo` 加行后即 `+++ b/foo` 的歧义形态在单块路径提取上根除）；
// HunkLines 刻意保持宽松逐行识别（兼容无 diff --git 分隔的拼接/截断形态
// ——真实消费方送来的常是这种），残留歧义（hunk 体中恰为完整 `+++ b/x`
// 形态的内容行会被当新文件头）作为宽松模式的有意取舍在此声明。
package diffx

import (
	"regexp"
	"strconv"
	"strings"
)

// sep 多文件 diff 的文件块分隔前缀。
const sep = "diff --git "

// SplitUnifiedDiff 将拼接的 unified diff 按文件路径切分为 map[path]完整块
// （块保留 "diff --git " 前缀，与常见存储/展示形态一致）。无路径的块忽略；
// 空输入返回空 map。
func SplitUnifiedDiff(raw string) map[string]string {
	result := make(map[string]string)
	if raw == "" {
		return result
	}
	chunks := SplitChunks(raw)
	for _, chunk := range chunks {
		if path := DiffPath(chunk); path != "" {
			result[path] = chunk
		}
	}
	return result
}

// SplitChunks 按 "\ndiff --git " 切块，返回保序块列表（每块补回前缀；首块
// 容忍缺失前缀，空 preamble 跳过）。需要顺序语义（同路径块后者覆盖等确定性
// 行为）的调用方用它；只需 path→块 映射时用 SplitUnifiedDiff。
func SplitChunks(raw string) []string {
	chunks := strings.Split(raw, "\n"+sep)
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		switch {
		case i == 0 && strings.HasPrefix(chunk, sep):
			// 已有前缀，保留原样
		case i == 0 && strings.TrimSpace(chunk) == "":
			continue // 空 preamble
		case i == 0:
			chunk = sep + chunk // 无前缀（罕见），补回
		default:
			chunk = sep + chunk
		}
		out = append(out, chunk)
	}
	return out
}

// DiffPath 从单文件 diff 块提取目标路径（+++ b/<path>，删除文件回退
// --- a/<path>；行尾制表符附加信息剥除，/dev/null 不是目标）。
// 识别止于**首个 @@**（v0.12.0）：hunk 体内容行不参与——正文 `++ b/x` 加行
// 后即 `+++ b/x` 的歧义形态在单块路径提取上根除。
func DiffPath(chunk string) string {
	var minus, plus string
	for _, line := range strings.Split(chunk, "\n") {
		if strings.HasPrefix(line, "@@") {
			break // 头区结束：hunk 体内容行不参与文件头识别
		}
		if p, ok := headerPath(line, "+++ "); ok {
			plus = p
		} else if p, ok := headerPath(line, "--- "); ok {
			minus = p
		}
		// /dev/null 的 +++ 不是可用目标（删除文件形态）——继续扫 --- 侧
		if plus != "" && plus != "/dev/null" {
			break
		}
	}
	if plus != "" && plus != "/dev/null" {
		return plus
	}
	if minus != "/dev/null" {
		return minus
	}
	return ""
}

// headerPath 解析 +++ / --- 文件头行：剥 b/ a/ 侧前缀（--no-prefix 裸路径
// 原样通过）、quoted path（"…"，C 风格转义）、行尾 \t 附加信息与 \r 残留。
// prefix 为 "+++ "（侧前缀 b/）或 "--- "（侧前缀 a/）；ok=false 表示非该类头行。
func headerPath(line, prefix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	p := stripTab(strings.TrimPrefix(line, prefix))
	// 侧前缀剥离（+++ → b/，--- → a/；quoted 形态下前缀在引号**外**：
	// `+++ b/"a b.go"`）；无该前缀 = --no-prefix 形态，原样通过
	side := "b/"
	if prefix == "--- " {
		side = "a/"
	}
	p = strings.TrimPrefix(p, side)
	// quoted path：git 对含空格/特殊字符路径的 C 风格转义形态（引号包住
	// 剥前缀后的路径本体）
	if strings.HasPrefix(p, `"`) && strings.HasSuffix(p, `"`) && len(p) >= 2 {
		return unquoteC(p[1 : len(p)-1]), true
	}
	return p, true
}

// unquoteC 反转义 git quoted path 的 C 风格转义（\" \\ \t \n 与 \八进制）。
// 无法识别的转义序列原样保留（保守——路径宁可带残渣不可静默丢字符）。
func unquoteC(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '"', '\\':
			b.WriteByte(s[i])
		default:
			// \八进制（最多三位）
			if s[i] >= '0' && s[i] <= '7' && i+2 < len(s) &&
				s[i+1] >= '0' && s[i+1] <= '7' && s[i+2] >= '0' && s[i+2] <= '7' {
				n := int(s[i]-'0')<<6 | int(s[i+1]-'0')<<3 | int(s[i+2]-'0')
				if n < 256 {
					b.WriteByte(byte(n))
					i += 2
					continue
				}
			}
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// stripTab 剥路径尾部制表符附加信息与 CRLF 残留。
func stripTab(p string) string {
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	// CRLF diff（Windows 编辑器/Web 面板搬运常态）行尾 \r 残留会进路径，
	// map 查询按真实仓库路径必落空=整块静默丢失（第八轮审计）
	return strings.TrimSuffix(p, "\r")
}

// hunkRe unified diff hunk 头：@@ -oldStart,oldLines +newStart,newLines @@。
var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// LineRange 新文件侧行区间（闭区间）；[2]int 别名——嵌入方既有签名零改动。
type LineRange = [2]int

// HunkLines 从 diff 文本提取每个文件的新行区间（闭区间 [start,end]）。
// 纯删除块（新增行数 0）不计；@@ 行前的 +++ 头决定归属文件。
// v0.12.0 状态机化：文件头只在头区识别，hunk 体的内容行不干扰归属。
func HunkLines(diffText string) map[string][]LineRange {
	out := map[string][]LineRange{}
	curFile := ""
	for _, line := range strings.Split(diffText, "\n") {
		if strings.HasPrefix(line, "@@") {
			m := hunkRe.FindStringSubmatch(line)
			if m == nil || curFile == "" {
				continue
			}
			start, err1 := strconv.Atoi(m[1])
			cnt := 1
			if m[2] != "" {
				cnt, _ = strconv.Atoi(m[2])
			}
			if err1 != nil || cnt <= 0 {
				continue // 纯删除块：新文件侧无行
			}
			out[curFile] = append(out[curFile], LineRange{start, start + cnt - 1})
			continue
		}
		// 宽松识别（见包注释的取舍声明）：+++ / --- / diff --git 行在任意位置
		// 都重置文件归属——兼容无分隔拼接与截断形态
		if strings.HasPrefix(line, "diff --git ") {
			curFile = ""
			continue
		}
		if p, ok := headerPath(line, "+++ "); ok && p != "/dev/null" {
			curFile = p
		}
	}
	return out
}
