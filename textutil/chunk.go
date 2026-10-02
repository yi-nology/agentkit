package textutil

import (
	"strings"
	"unicode/utf8"
)

// ChunkLines 按行边界把文本切成 ≤target（rune）的增量片：凑片不跨行
// （markdown 表格/列表行保持完整，中途渲染不碎）；超长单行在 hard 处硬切
// （base64/长 URL 不设上限会把节奏拖成整行卡顿）。SplitAfter 语义保留行尾
// 换行——join(所有片) 严格等于原文（消费方按序归并即得全文，无需分隔符约定）。
// target/hard<=0 取缺省（160/400）；空白文本返回 nil。
func ChunkLines(s string, target, hard int) []string {
	if target <= 0 {
		target = 160
	}
	if hard <= 0 {
		hard = 400
	}
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var chunks []string
	var buf []rune
	flush := func() {
		if len(buf) > 0 {
			chunks = append(chunks, string(buf))
			buf = buf[:0]
		}
	}
	for _, line := range strings.SplitAfter(s, "\n") {
		runes := []rune(line)
		for len(runes) > hard {
			flush()
			chunks = append(chunks, string(runes[:hard]))
			runes = runes[hard:]
		}
		if len(buf)+len(runes) > target {
			flush()
		}
		buf = append(buf, runes...)
	}
	flush()
	return chunks
}

// ClampRuneBoundary 把字节下标 n 回退到所在 UTF-8 字符的起始边界（切点落进
// 多字节字符中间时会产生非法 UTF-8——头尾切分场景先经此钳制）。n>=len(s)
// 返回 len(s)；n<=0 返回 0。
func ClampRuneBoundary(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	if n <= 0 {
		return 0
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}
