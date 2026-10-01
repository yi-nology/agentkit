// 模式级学习环的确定性数学与键派生（收编自 argus internal/learning，v0.10.32）：
// BetaConfidence 与 WilsonCI/McNemarExact 同族——评审反馈回路的小样本统计原语；
// PatternKey/NormalizePatternText 是「同一类问题聚成同一模式」的键语义单源。
// 纯函数、零依赖、可离线复算；禁止 LLM 摘要参与键派生（不可对账）。
package reportutil

import (
	"git.enjoye.top/enjoydream/ekit/pkg/encoding"
	"regexp"
	"strings"

	"github.com/yi-nology/agentkit/textutil"
)

// PatternTextMax 模式键/人读样本的文本截断上限（rune）。
const PatternTextMax = 160

// NormalizePatternText 规范化评论文本用于模式键：
// 小写 → 剥路径/十六进制/反引号标识符/纯数字 → 折叠空白 → 截断。
// 反引号/路径/十六进制/数字先剥，避免 "错误处理不佳 in `handler.go:12`"
// 与 "错误处理不佳 in `service.go:99`" 落成不同模式。
func NormalizePatternText(comment string) string {
	s := strings.ToLower(comment)
	s = backtickRe.ReplaceAllString(s, " ")
	s = pathRe.ReplaceAllString(s, " ")
	s = hexRe.ReplaceAllString(s, " ")
	s = digitsRe.ReplaceAllString(s, " ")

	// 折叠空白；去掉标点中对模式无信息量的重复符号
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	// 再压一遍：仅保留字母数字与 CJK，其余标点变空白后再折叠
	s = punctRe.ReplaceAllString(s, " ")
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	out, _ := textutil.TruncRunes(s, PatternTextMax)
	return out
}

var (
	backtickRe = regexp.MustCompile("`[^`]*`")
	pathRe     = regexp.MustCompile(`(?:[\w.-]+/)+[\w.-]+|\b\w+\.(?:go|ts|js|py|java|rs|c|cpp|h|hpp|md|yaml|yml|json|sql|sh|proto)\b`)
	hexRe      = regexp.MustCompile(`\b[0-9a-f]{6,}\b`)
	digitsRe   = regexp.MustCompile(`\b\d+\b`)
	spaceRe    = regexp.MustCompile(`\s+`)
	punctRe    = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// PatternKey 派生仓库级模式键：sha256(plugin \x00 normalize(comment)) 前 16 hex。
// plugin 为空时用 "unknown"——缺插件归属的键仍可计算，但不应静默混入其他插件历史。
// 键不含 file/line——同一类问题在不同文件聚成同一模式；含 plugin 分量——
// 不同审查来源的信任历史不混用。
func PatternKey(pluginName, comment string) string {
	if pluginName == "" {
		pluginName = "unknown"
	}
	norm := NormalizePatternText(comment)
	return encoding.Sha256(pluginName + "\x00" + norm)[:16]
}

// PatternSample 返回入库的人读样本（规范化后截断），便于审计与提示词注入展示。
func PatternSample(comment string) string {
	return NormalizePatternText(comment)
}

// BetaConfidence 由 posted/regret/suppress/restore 计数推 Beta 后验置信度。
// 先验 (α=2, β=2) → 0.5；Posted α+1；Regret β+2；Suppress β+3；
// Restore 只回抬 β（max(β-2,1)），不加 α——此前 Restore α+2 会让
// suppress+restore 成对循环净抬置信度（α+2, β+1），API 持有者可循环洗白；
// 现在 restore 仅撤销部分负证据，成对循环净 β+1（置信度微降），无法洗白。
// samples=Posted+Regret+Suppress（PatternSamples），读时计算不落列。
func BetaConfidence(posted, regret, suppress, restore int) float64 {
	if posted < 0 {
		posted = 0
	}
	if regret < 0 {
		regret = 0
	}
	if suppress < 0 {
		suppress = 0
	}
	if restore < 0 {
		restore = 0
	}
	alpha := 2.0 + float64(posted)
	beta := 2.0 + 2*float64(regret) + 3*float64(suppress)
	if restore > 0 {
		// 撤销误杀：负证据回抬一档（不为负）
		if b := beta - 2*float64(restore); b < 1 {
			beta = 1
		} else {
			beta = b
		}
	}
	return alpha / (alpha + beta)
}

// PatternSamples 参与消费判定的样本量（小样本不消费，防抖动）。
func PatternSamples(posted, regret, suppress int) int {
	if posted < 0 {
		posted = 0
	}
	if regret < 0 {
		regret = 0
	}
	if suppress < 0 {
		suppress = 0
	}
	return posted + regret + suppress
}
