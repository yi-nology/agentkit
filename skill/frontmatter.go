package skill

import "strings"

// parseFrontmatter 解析 SKILL.md 的 YAML frontmatter（Agent Skills 开放标准的
// 最小子集：--- 围栏内的 name/description 行）。零依赖：不引 yaml 解析器，
// 不认识的键忽略——skill 正文才是消费主体，元数据只服务发现与决策。
func parseFrontmatter(content string) (name, desc, body string) {
	body = content
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return "", "", content
	}
	// 定位首个 ---（起始）与结束 ---
	rest := trimmed[3:]
	if !strings.HasPrefix(rest, "\n") {
		return "", "", content
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", content
	}
	block := rest[:end]
	body = strings.TrimPrefix(rest[end+4:], "\n")

	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := cut(line, ':')
		if !ok {
			continue
		}
		val = trimQuoted(strings.TrimSpace(val))
		switch strings.TrimSpace(key) {
		case "name":
			name = val
		case "description":
			desc = val
		}
	}
	return name, desc, body
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

func cut(s string, sep byte) (before, after string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
