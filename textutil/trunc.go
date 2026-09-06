// Package textutil 文本处理公共函数。
package textutil

import "unicode/utf8"

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
