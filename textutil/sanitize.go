// sanitize.go —— 落盘文件名安全化（textutil 通用文本/标识处理）。
package textutil

import "strings"

// SanitizeFileStem 文件名安全化：标识串（owner/repo/number 等外部标识）里的
// 路径分隔与引用语法字符归一为 "-"。外部标识常含 "/"（GitLab 嵌套组）、"#"、
// ":"（分支名）、甚至 ".."——未消毒直接拼进留档文件名会导致写入失败甚至
// 目录穿越。只处理文件名主干（不含扩展名），调用方自行拼接后缀。
func SanitizeFileStem(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '#', '@', ':', '\\':
			return '-'
		}
		return r
	}, s)
}
