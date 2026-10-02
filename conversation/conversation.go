// Package conversation 多轮会话历史的通用原语：滚动窗口切分、历史渲染、
// 摘要拼接。面向「模型无状态、连续性归运行时」的会话形态——verbatim 注入
// 只保留最近 N 轮，更早轮次交由调用方滚动压缩成摘要（摘要生成策略归调用方，
// 本包只做确定性切分与文本拼装）。
package conversation

import (
	"strings"

	"github.com/yi-nology/agentkit/textutil"
)

// Turn 单轮问答（问答形态覆盖审查追问/客服/助手类会话；statement 型对话
// 可把 Question 留空）。
type Turn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	At       string `json:"at,omitempty"` // RFC3339 或任意时间标注（可观测用）
}

// 渲染截断上限（rune）：单轮注入不吞噬上下文预算。
const (
	QuestionCap = 200
	AnswerCap   = 400
)

// Split 按 verbatim 窗口切分：recent=最近 keep 条（原样注入）；evicted=其余
// （应滚动压缩进摘要）。keep<=0 视为全 verbatim、无淘汰。
func Split(history []Turn, keep int) (recent, evicted []Turn) {
	if keep <= 0 || len(history) <= keep {
		return history, nil
	}
	return history[len(history)-keep:], history[:len(history)-keep]
}

// Render 会话历史 → 注入文本（"用户:/助手:" 行 + 单轮截断）。
// 历史内容来自用户输入（攻击者可控），调用方必须把返回值放入数据区围栏。
func Render(history []Turn) string {
	var b strings.Builder
	for _, t := range history {
		b.WriteString("用户: " + truncateLine(t.Question, QuestionCap) + "\n")
		b.WriteString("助手: " + truncateLine(t.Answer, AnswerCap) + "\n\n")
	}
	return strings.TrimSpace(b.String())
}

// Combine 滚动摘要 + verbatim 轮次 → 注入文本。summary 为空且无 recent 时
// 返回空串（调用方跳过注入）。
func Combine(summary string, recent []Turn) string {
	var parts []string
	if s := strings.TrimSpace(summary); s != "" {
		parts = append(parts, "更早对话摘要：\n"+s)
	}
	if h := Render(recent); h != "" {
		parts = append(parts, h)
	}
	return strings.Join(parts, "\n\n")
}

func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	t, tr := textutil.TruncRunes(s, n)
	if tr {
		return t + "…"
	}
	return s
}
