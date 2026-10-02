package skill

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// isFenceLine 闭合围栏判定（全部 frontmatter 入口的唯一事实源）：
// --- 独立成行，允许行尾空白/CRLF。「---x」不是围栏——半个词的围栏会把正文吞进元数据。
func isFenceLine(line string) bool {
	return strings.TrimSpace(line) == "---"
}

// findClosingFence 在起始围栏之后的内容里定位闭合 ---（语义见 isFenceLine）。
// 返回块终点（闭合行前的 \n 处）与正文起点（闭合行之后）；未闭合 ok=false。
func findClosingFence(rest string) (blockEnd, bodyStart int, ok bool) {
	for off := 0; ; {
		j := strings.Index(rest[off:], "\n---")
		if j < 0 {
			return 0, 0, false
		}
		pos := off + j
		after := rest[pos+4:] // "---" 之后到行尾
		lineEnd := strings.IndexByte(after, '\n')
		tail := after
		if lineEnd >= 0 {
			tail = after[:lineEnd]
		}
		if strings.TrimSpace(tail) == "" { // 行尾只有空白/CRLF：合法闭合
			if lineEnd < 0 {
				return pos, len(rest), true
			}
			return pos, pos + 4 + lineEnd + 1, true
		}
		off = pos + 1 // 「---x」伪围栏：跳过继续找
	}
}

// parseFrontmatter 解析 SKILL.md 的 YAML frontmatter（Agent Skills 开放标准的
// 最小子集：--- 围栏内的 name/description/version 行）。零依赖：不引 yaml 解析器，
// 不认识的键忽略——skill 正文才是消费主体，元数据只服务发现与决策。
func parseFrontmatter(content string) (name, desc, ver, body string) {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return "", "", "", content
	}
	// 定位首个 ---（起始）与结束 ---
	rest := trimmed[3:]
	if !strings.HasPrefix(rest, "\n") {
		return "", "", "", content
	}
	blockEnd, bodyStart, ok := findClosingFence(rest)
	if !ok {
		return "", "", "", content
	}
	block := rest[:blockEnd]
	// canonical 正文：剥壳后去首尾空白——与 ParseRichFrontmatter 同一口径，
	// 两个 Provider 的 Content（及其 Checksum）才逐字节一致。
	body = strings.TrimSpace(rest[bodyStart:])

	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = trimQuoted(strings.TrimSpace(val))
		switch strings.TrimSpace(key) {
		case "name":
			name = val
		case "description":
			desc = val
		case "version":
			ver = val
		}
	}
	return name, desc, ver, body
}

// trimQuoted 仅当值整体被成对引号包裹时剥除——值内部以引号结尾的合法内容
// （如：他说 "hello"）不受影响。
func trimQuoted(v string) string {
	for _, q := range []string{`"`, `'`} {
		if len(v) >= 2 && strings.HasPrefix(v, q) && strings.HasSuffix(v, q) {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// ParseRichFrontmatter 用 yaml 解析 SKILL.md frontmatter（name/description/mode/
// version/maturity/requires_mcp/deprecated/compatibility）。返回元数据与正文。
// frontmatter name 写入 Title（展示名）；目录名仍作规范 Name。
func ParseRichFrontmatter(dirName, content string) (LibMeta, string, error) {
	meta := LibMeta{Name: dirName, Mode: ModeStatic}
	body := content
	if !strings.HasPrefix(content, "---") {
		return meta, body, nil
	}
	inner := content[3:]
	rest := strings.TrimPrefix(inner, "\n")
	// 空 frontmatter：`---\n---` 经剥壳后 rest 以 --- 开头（无前置换行）。
	if strings.HasPrefix(rest, "---") {
		after := strings.TrimPrefix(rest, "---")
		if after == "" || after[0] == '\n' || after[0] == '\r' {
			return meta, strings.TrimSpace(strings.TrimPrefix(after, "\n")), nil
		}
	}
	// 定位独立成行的闭合 ---（围栏语义唯一事实源见 findClosingFence）。
	blockEnd, bodyStart, closed := findClosingFence(rest)
	if !closed {
		return meta, body, fmt.Errorf("skill: frontmatter 未闭合")
	}
	fmRaw := rest[:blockEnd]
	body = strings.TrimSpace(rest[bodyStart:])

	// 直接解进 LibMeta（schema 唯一事实源）；Name/Title 语义 fixup 在下方。
	var fm LibMeta
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return meta, body, fmt.Errorf("skill: frontmatter YAML 非法: %w", err)
	}
	meta = fm
	meta.Name = dirName  // 目录名作规范名
	meta.Title = fm.Name // frontmatter name 作展示名
	if meta.Mode == "" {
		meta.Mode = ModeStatic
	}
	return meta, body, nil
}
