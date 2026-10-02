// Package textutil 文本处理公共函数。
package textutil

import (
	"unicode/utf8"

	"git.enjoye.top/enjoydream/ekit/pkg/stringx"
)

// 截断三件（TruncRunes/TruncBytes/TruncEllipsis）的实现已下沉
// ekit/pkg/stringx（ekit v0.33.0，agentkit v0.10.41 起单源在彼）——下方为
// 公开面薄委托（agentkit 内大量调用点与外部消费方免迁移）。

// TokenCharDivisor 字符→token 估算除数（与 ZCode ESTIMATED_TOKEN_CHAR_DIVISOR
// 同值——中英混合语料的经验系数；精确计量归 provider usage 回传）。
const TokenCharDivisor = 3

// EstTokensOf 字符串 token 估算（rune 数 / TokenCharDivisor 向上取整）。
func EstTokensOf(s string) int {
	return EstTokens(utf8.RuneCountInString(s))
}

// EstTokens 已知 rune 数时的 token 估算（向上取整）。
func EstTokens(runes int) int {
	if runes <= 0 {
		return 0
	}
	return (runes + TokenCharDivisor - 1) / TokenCharDivisor
}

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
func TruncRunes(s string, n int) (string, bool) { return stringx.TruncRunes(s, n) }

// TruncBytes 按字节预算截断（rune 对齐：预算落点切进多字节字符时回退到
// RuneStart 边界，不产生非法 UTF-8 尾字节）。第二个返回值 = 是否真发生了
// 截断。budget<0 视为非法上限，按"全部截断"处理（返回空串），不 panic。
// 字节预算面的唯一原语（skill 清单降级/全文上限共用——此前各写一份回退逻辑）。
func TruncBytes(s string, budget int) (string, bool) { return stringx.TruncBytes(s, budget) }

// TruncEllipsis 截断并追加省略号（列表/标题/事件载荷展示面统一语义）。
// n<=0 返回省略号；无需截断时原样返回。
func TruncEllipsis(s string, n int) string { return stringx.TruncEllipsis(s, n) }

// TruncNote 截断并追加中文注记后缀（截断留痕语义单源：超限截断的输出带
// 「…（注记）」尾巴，调用方/模型可感知截断发生）。无需截断时原样返回；
// 注记以「…（」开头自行拼接，调用方传纯文字（如 "超长行截断"）。
func TruncNote(s string, n int, note string) string {
	out, truncated := TruncRunes(s, n)
	if truncated {
		return out + "…（" + note + "）"
	}
	return out
}
