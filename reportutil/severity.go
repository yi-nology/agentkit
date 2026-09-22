// Package reportutil 评审/评测报告后处理原语（沉淀自 heimdallr 报告层与
// bianque 评审流水线），一个包覆盖同一条消费链的三段：
//
//   - 严重度归一（severity.go）：high/medium/low 词表 + 外部审查专家常用
//     别名（P0/fatal/nit…）折叠——「严重级别是什么、多严重」；
//   - 采样聚簇（cluster.go）：best-of-N 输出按签名通道聚簇计数（含
//     file+规范化文本精确指纹），「哪些发现被独立复现」；
//   - 对比统计（stats.go）：小样本通过率 Wilson 区间 + 配对 McNemar 精确
//     检验——「两个版本的差异是否显著」。
//
// 三段原为 severity/sampling/stats 三包，消费者画像相同且边界已漂移过一轮
// （v0.10.9 severity→sampling 指纹迁移），v0.10.11 合并为单包。零外部依赖。
package reportutil

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
