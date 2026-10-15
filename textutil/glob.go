package textutil

import "strings"

// GlobMatch 极简 glob：支持 `**`（跨目录）与 `*`/`?`（单段）。
// 语义对齐 .gitignore 常见用法：`web/**` 匹配 web/ 下一切（不含 web 自身）；
// `*.vue` 匹配任意目录下的 .vue；`?` 消耗一个 rune（多字节文件名安全）。
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
				// 尾随 `/**` 只匹配目录内部（.gitignore 语义），不含目录自身
				return len(seg) > 0
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
	// rune 级回溯：`?` 消耗一个 rune，多字节（中文等）文件名不漏配
	pat := []rune(pattern)
	str := []rune(s)
	var (
		px, sx int
		starPx = -1
		starSx int
	)
	for sx < len(str) {
		if px < len(pat) && (pat[px] == '?' || pat[px] == str[sx]) {
			px++
			sx++
			continue
		}
		if px < len(pat) && pat[px] == '*' {
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
	for px < len(pat) && pat[px] == '*' {
		px++
	}
	return px == len(pat)
}
