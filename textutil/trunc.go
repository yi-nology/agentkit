// Package textutil 文本处理公共函数。
package textutil

import "unicode/utf8"

// SplitRunes 按 rune 等分为 ≤n 字符的块（最后一块可不足）。
// n<=0 返回整串单块。大文本分块送 LLM 的公共原语。
func SplitRunes(s string, n int) []string {
	if n <= 0 {
		return []string{s}
	}
	r := []rune(s)
	var out []string
	for len(r) > 0 {
		k := n
		if len(r) < k {
			k = len(r)
		}
		out = append(out, string(r[:k]))
		r = r[k:]
	}
	return out
}

// TruncRunes 按 rune 截断，避免多字节字符被腰斩产生非法 UTF-8。
// 第二个返回值 = 是否真发生了截断。
// n<0 视为非法上限，按"全部截断"处理（返回空串），不 panic。
func TruncRunes(s string, n int) (string, bool) {
	if n < 0 {
		return "", true
	}
	// 先计数再转换：无需截断时不产生 []rune 全量分配（大 diff 场景省 4 倍峰值内存）
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	r := []rune(s)
	return string(r[:n]), true
}

// TruncEllipsis 截断并追加省略号（列表/标题/事件载荷展示面统一语义）。
// n<=0 返回省略号；无需截断时原样返回。
func TruncEllipsis(s string, n int) string {
	if n <= 0 {
		return "…"
	}
	out, truncated := TruncRunes(s, n)
	if truncated {
		return out + "…"
	}
	return out
}
