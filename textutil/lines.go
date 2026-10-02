// lines.go —— 行号标注（文本行 → 带 4 位宽行号前缀）。
package textutil

import (
	"fmt"
	"strings"
)

// NumberLines 逐行加 4 位宽行号前缀（"   1|"）。
// 用途：给 agent 的文件内容原文加行号——无行号原文会逼模型凭空编造
// file:line 证据；行号前缀必须先于任何截断。尾随换行不产生幽灵空行。
func NumberLines(s string) string {
	if s == "" {
		return ""
	}
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i, ln := range lines {
		fmt.Fprintf(&b, "%4d|%s\n", i+1, ln)
	}
	return strings.TrimRight(b.String(), "\n")
}
