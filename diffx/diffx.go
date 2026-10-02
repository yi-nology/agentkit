// Package diffx unified diff 解析纯函数（收编自 argus internal/context
// diffprep 与 runner/inject，v0.10.35）：按 "diff --git" 切分多文件 diff、
// 提取目标路径、提取 hunk 新行区间。纯函数、零依赖、不改输入。
//
// 边界：解析面只覆盖 git 风格 unified diff 的结构性标记（+++ / @@ 行），
// 不做语义级 diff（那是 go-git-platform diff 侧的领域）；路径以 +++ b/<path>
// 为准（新增/修改），删除文件回退 --- a/<path>。
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
func DiffPath(chunk string) string {
	var minus, plus string
	for _, line := range strings.Split(chunk, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			plus = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "--- a/"):
			minus = strings.TrimPrefix(line, "--- a/")
		}
		if plus != "" {
			break
		}
	}
	if p := stripTab(plus); p != "" && p != "/dev/null" {
		return p
	}
	return stripTab(minus)
}

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
// 纯删除块（新增行数 0）不计；@@ 行前的 +++ b/<path> 决定归属文件。
func HunkLines(diffText string) map[string][]LineRange {
	out := map[string][]LineRange{}
	curFile := ""
	for _, line := range strings.Split(diffText, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			curFile = strings.TrimPrefix(line, "+++ b/")
			curFile = stripTab(curFile)
		case curFile != "" && strings.HasPrefix(line, "@@"):
			m := hunkRe.FindStringSubmatch(line)
			if m == nil {
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
		}
	}
	return out
}
