package severity

import "strings"

// GlobMatch 极简 glob：支持 `**`（跨目录）与 `*`/`?`（单段）。
// 语义对齐 .gitignore 常见用法：`web/**` 匹配 web/ 下一切；`*.vue` 匹配任意目录下的 .vue。
func GlobMatch(pattern, path string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "**" {
		return true
	}
	if !strings.Contains(pattern, "/") {
		return segmentMatch(pattern, path[strings.LastIndexByte(path, '/')+1:])
	}
	return globMatch(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func globMatch(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(seg); i++ {
				if globMatch(pat[1:], seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		if !segmentMatch(pat[0], seg[0]) {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

func segmentMatch(pattern, s string) bool {
	var (
		px, sx int
		starPx = -1
		starSx int
	)
	for sx < len(s) {
		if px < len(pattern) && (pattern[px] == '?' || pattern[px] == s[sx]) {
			px++
			sx++
			continue
		}
		if px < len(pattern) && pattern[px] == '*' {
			starPx = px
			starSx = sx
			px++
			continue
		}
		if starPx >= 0 {
			px = starPx + 1
			starSx++
			sx = starSx
			continue
		}
		return false
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}
