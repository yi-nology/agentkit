// Package textutil 文本处理公共函数。
package textutil

import (
	"unicode"
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

// EstTokensCJK CJK 感知的 token 估算：Han/Hangul/Kana/全角形式计 2 单位，
// 其余计 1，tokens ≈ units/3 + 1（非空下限 1）。纯 rune 计数口径对中文系统性
// 低估（经验上中文 ≈ 1 字 0.67 token、英文 ≈ 4 字符 1 token）——中文为主的
// 文本（提示词/会话历史/报告）用本函数；精确计量仍归 provider usage 回传。
func EstTokensCJK(s string) int {
	if s == "" {
		return 0
	}
	units := 0
	for _, r := range s {
		if runeUnitsCJK(r) == 2 {
			units += 2
		} else {
			units++
		}
	}
	return units/3 + 1
}

// EstTokensCJKRunes 已知 rune 数且内容以 CJK 为主时的保守计量
// （units=2n → tokens=2n/3+1）——compact 等已有 rune 计数中间量的调用方
// 复用 CJK 口径，免字符串物化。
func EstTokensCJKRunes(n int) int {
	if n <= 0 {
		return 0
	}
	return 2*n/3 + 1
}

// RuneUnitsCJK 文本的 CJK 单位总量（CJK/全角计 2、其余计 1）。
func RuneUnitsCJK(s string) int {
	n := 0
	for _, r := range s {
		n += runeUnitsCJK(r)
	}
	return n
}

// UnitsToTokensCJK 单位量 → token（units/3 向上取整，非空下限 1）。
func UnitsToTokensCJK(units int) int {
	if units <= 0 {
		return 0
	}
	return (units + 2) / 3
}

func runeUnitsCJK(r rune) int {
	// 漏段显式补齐（第八轮审计）：部首补充/康熙部首、CJK 笔画、竖排/兼容/
	// 小形式变体——Han/Hangul/Kana 的 unicode.Is 覆盖不到的 CJK 符号区段
	if (r >= 0x2E80 && r <= 0x2FDF) || (r >= 0x31C0 && r <= 0x31EF) ||
		(r >= 0xFE10 && r <= 0xFE19) || (r >= 0xFE30 && r <= 0xFE4F) ||
		(r >= 0xFE50 && r <= 0xFE6F) {
		return 2
	}
	if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		(r >= 0x3000 && r <= 0x303F) || // CJK 标点
		(r >= 0xFF00 && r <= 0xFFEF) { // 全角形式
		return 2
	}
	return 1
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

// FitInputNote 窗口预算截断的统一注记文案（llm.fitInput 与 llm/wrap 共用
// 单源——此前两处字面量漂移无编译期信号）。
const FitInputNote = "输入超出模型窗口预算，已截断留痕"

// FitCharsLimit 模型窗口的 rune 预算：(窗口-预留)×3/2×0.9（CJK 感知系数
// 1.5 token/rune 上界——v0.12.1 起两处共用；×0.9 留 10% 余量）。窗口无效
// 或预留缺省（<=0 取 窗口/20）不可算时返回 0（调用方跳过拟合）。
func FitCharsLimit(windowTokens, reserveTokens int) int {
	if windowTokens <= 0 {
		return 0
	}
	if reserveTokens <= 0 {
		reserveTokens = windowTokens / 20
	}
	limit := (windowTokens - reserveTokens) * 3 / 2 * 9 / 10
	if limit <= 0 {
		return 0
	}
	return limit
}
