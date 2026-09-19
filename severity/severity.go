// Package severity 严重级别词表与归一：high/medium/low 词表 + 外部审查专家
// 常用别名（P0/fatal/nit…）折叠。零外部依赖。
// （v0.10.9 职责收敛：GlobMatch 迁 textutil、Fingerprint/NormalizeComment 迁
// sampling——本包只回答「严重级别是什么、多严重」。）
package severity

import "strings"

// 严重级别词表。
const (
	High   = "high"
	Medium = "medium"
	Low    = "low"
)

// aliases 外部审查专家的常见严重度词表：cli/acpx 类外部 agent 常用 P0/P1/
// nit 等通用研发词表而非 high/medium/low，缺别名会让外部专家的 P0 整批落
// low（高危意见永远触发不了高严重度裁决）。
var aliases = map[string]string{
	"p0":      High,
	"fatal":   High,
	"urgent":  High,
	"p1":      Medium,
	"major":   Medium,
	"p2":      Low,
	"p3":      Low,
	"trivial": Low,
	"nit":     Low,
	"nits":    Low,
}

// Normalize 把任意写法归一到词表。
// 第二个返回值=false 表示原值越界（已归为 low，调用方应记录告警）。
func Normalize(s string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(s))
	if level, ok := aliases[v]; ok {
		return level, true
	}
	switch v {
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
