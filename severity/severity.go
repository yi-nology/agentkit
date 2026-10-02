// Package severity 严重级别归一化、指纹、glob 匹配等通用工具函数。零外部依赖。
package severity

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// 严重级别词表。
const (
	High   = "high"
	Medium = "medium"
	Low    = "low"
)

// Normalize 把任意写法归一到词表。
// 第二个返回值=false 表示原值越界（已归为 low，调用方应记录告警）。
func Normalize(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high", "critical", "blocker", "error", "严重", "高危":
		return High, true
	case "medium", "moderate", "warning", "warn", "中", "中等":
		return Medium, true
	case "low", "info", "minor", "hint", "低", "提示":
		return Low, true
	default:
		return Low, false
	}
}

// Rank 越大越严重。
func Rank(s string) int {
	switch s {
	case High:
		return 3
	case Medium:
		return 2
	default:
		return 1
	}
}

// Higher 取更严重的一方。
func Higher(a, b string) string {
	if Rank(a) >= Rank(b) {
		return a
	}
	return b
}

// Valid 是否属于钉死词表。
func Valid(s string) bool {
	return s == High || s == Medium || s == Low
}

// Fingerprint 精确指纹：规范化评论文本哈希 + file。
// 行号不入指纹（±2 容差仅作匹配辅助）；同一指纹跨轮次即"同一问题"。
func Fingerprint(file, comment string) string {
	sum := sha256.Sum256([]byte(file + "\x1f" + NormalizeComment(comment)))
	return hex.EncodeToString(sum[:16])
}

// NormalizeComment 规范化：全小写、去所有空白与标点/符号差异。
func NormalizeComment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
