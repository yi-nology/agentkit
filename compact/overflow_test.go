package compact

import (
	"errors"
	"strings"
	"testing"
)

func overflowErr(gap string) error { return errors.New(gap) }

func TestTokenGap(t *testing.T) {
	cases := []struct {
		msg  string
		want int
		ok   bool
	}{
		{"This model's maximum context length is 128000 tokens. However, you requested 130500 tokens. ", 2500, true},
		{"prompt is too long: 150000 tokens > 200000 maximum", 50000, true},
		{"prompt is too long: 200000 tokens > 200000 maximum", 0, false}, // 非超差
		{"some other error", 0, false},
	}
	for _, tc := range cases {
		got, ok := TokenGap(overflowErr(tc.msg))
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%q: got=%d ok=%v want=%d ok=%v", tc.msg, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := TokenGap(nil); ok {
		t.Error("nil cause 不应识别")
	}
}

func mkRounds(n int, resultLen int) []Message {
	return mkTranscript(n, resultLen)
}

func TestGroupRoundsAndSelect(t *testing.T) {
	msgs := mkRounds(4, 1)
	groups := GroupRounds(msgs)
	// 对标 ZCode groupByAssistantStartedRounds：首组前的 user 自成组，
	// 其后每组以 assistant 开头
	if len(groups) != 5 {
		t.Fatalf("应 5 组（leading user 自成组）: %d", len(groups))
	}
	if groups[0][0].Role != "user" {
		t.Fatalf("首组应为 leading user: %v", groups[0][0].Role)
	}
	for i, g := range groups[1:] {
		if g[0].Role != "assistant" {
			t.Fatalf("组[%d] 应以 assistant 开头: %v", i+1, g[0].Role)
		}
	}
	sel := Select(msgs, 1)
	if sel.GroupsPreserved != 1 || len(sel.Preserved) != len(groups[4]) {
		t.Fatalf("保留最近 1 组: %+v", sel)
	}
	if len(sel.ForSummary) == 0 {
		t.Fatal("摘要侧非空")
	}
	// 保留数上限 = 总组-1（至少一组进摘要）
	sel2 := Select(msgs, 99)
	if sel2.GroupsPreserved != 4 || len(sel2.ForSummary) == 0 {
		t.Fatalf("钳制: %+v", sel2)
	}
	// 0 = 全量摘要
	sel3 := Select(msgs, 0)
	if len(sel3.Preserved) != 0 || len(sel3.ForSummary) != len(msgs) {
		t.Fatalf("全量摘要: %+v", sel3)
	}
}

func TestSelectAfterOverflow(t *testing.T) {
	msgs := mkRounds(10, 3)
	cause := overflowErr("This model's maximum context length is 128000 tokens. However, you requested 190000 tokens. ")
	// gap 62k：每组 3×3000 字符结果 ≈ 3×1000 token + 调用开销 → 覆盖 62k 需全部组
	// → countGroupsToCover 退半量（10/2=5），从 preserve=0 上移到 5
	sel := SelectAfterOverflow(msgs, cause, 0)
	if sel == nil {
		t.Fatal("应给出收紧选择")
	}
	if sel.GroupsPreserved <= 0 {
		t.Fatalf("应上移保留组数: %d", sel.GroupsPreserved)
	}
	// 已到上限：nil
	if got := SelectAfterOverflow(msgs, cause, 9); got != nil {
		t.Fatalf("已到上限应 nil: %+v", got)
	}
	// 组数不足
	if got := SelectAfterOverflow(mkRounds(1, 1), cause, 0); got != nil {
		t.Fatal("组数不足应 nil")
	}
	// 无法识别的报文：保守移 1 组
	sel2 := SelectAfterOverflow(msgs, overflowErr("weird"), 0)
	if sel2 == nil || sel2.GroupsPreserved != 1 {
		t.Fatalf("未知报文应保守移 1 组: %+v", sel2)
	}
}

func TestTruncateForRetry(t *testing.T) {
	msgs := mkRounds(10, 3)
	// 无 gap：按 20% 丢 2 组
	got := TruncateForRetry(msgs, overflowErr("weird"), 1)
	if got == nil || len(got) >= len(msgs) {
		t.Fatalf("应丢最旧组: %d → %d", len(msgs), len(got))
	}
	// attempt 超限
	if got := TruncateForRetry(msgs, nil, MaxOverflowRetries); got != nil {
		t.Fatal("超限应 nil")
	}
	// 单组无从丢弃（leading-user 规则下 user+assistant+tool 是 2 组，此处构造真单组）
	if got := TruncateForRetry([]Message{{Role: "assistant"}, {Role: "tool"}}, nil, 1); got != nil {
		t.Fatal("单组应 nil")
	}
	// 有 gap：按覆盖丢
	got2 := TruncateForRetry(msgs, overflowErr(
		"This model's maximum context length is 1000 tokens. However, you requested 10000 tokens. "), 1)
	if got2 == nil || len(got2) >= len(msgs) {
		t.Fatalf("按 gap 丢组: %d → %d", len(msgs), len(got2))
	}
}

func TestEvaluateRapidRefill(t *testing.T) {
	// 距上次压缩 0 轮又满 → 计数 +1
	d := EvaluateRapidRefill(0, 0)
	if d.ConsecutiveRapidRefills != 1 || d.ShouldBlock {
		t.Fatalf("首次 rapid: %+v", d)
	}
	// 距上次压缩 5 轮（≥3）→ 清零
	d = EvaluateRapidRefill(5, 2)
	if d.ConsecutiveRapidRefills != 0 || d.ShouldBlock {
		t.Fatalf("正常间隔应清零: %+v", d)
	}
	// 连续 3 次熔断
	d = EvaluateRapidRefill(0, MaxConsecutiveRapidRefills-1)
	if !d.ShouldBlock {
		t.Fatalf("连续到阈值应熔断: %+v", d)
	}
	// 熔断错误形态
	err := &ErrRapidRefill{ConsecutiveRapidRefills: 3, ToolTurnsSinceCompact: 1}
	if !strings.Contains(err.Error(), "超大") {
		t.Fatalf("错误文案应指引排查输出源: %s", err)
	}
}

func TestSummaryPromptAndFormat(t *testing.T) {
	p := SummaryPrompt("")
	if !strings.Contains(p, "CRITICAL: Respond with TEXT ONLY") ||
		!strings.Contains(p, "9. Optional Next Step") ||
		!strings.Contains(p, "REMINDER: Do NOT call any tools") {
		t.Fatal("模板应含前后围栏与 9 段结构")
	}
	if !strings.Contains(p, "preserved verbatim") {
		t.Fatal("安全约束逐字保留指令应在")
	}
	p2 := SummaryPrompt("重点保留测试输出")
	if !strings.Contains(p2, "Additional Instructions:\n重点保留测试输出") {
		t.Fatal("附加指令应拼入")
	}

	// 格式化：剥 analysis、展开 summary
	out := FormatSummary("<analysis>思考…</analysis>\n<summary>\n正文 A\n\n\n\nB\n</summary>")
	if strings.Contains(out, "analysis") || strings.Contains(out, "<summary>") {
		t.Fatalf("标签应剥除: %q", out)
	}
	if !strings.Contains(out, "Summary:\n正文 A") || strings.Contains(out, "\n\n\n") {
		t.Fatalf("summary 展开与空行压缩: %q", out)
	}
	if FormatSummary("  ") != "" {
		t.Fatal("空输出应空串")
	}
}

func TestSummaryMessage(t *testing.T) {
	m := SummaryMessage("摘要正文", SummaryOptions{})
	if !strings.Contains(m, "摘要正文") || !strings.Contains(m, "continued from a previous conversation") {
		t.Fatalf("基础形态: %q", m)
	}
	m2 := SummaryMessage("s", SummaryOptions{RecentPreserved: true, SuppressFollowup: true, TranscriptPath: "/tmp/t.jsonl"})
	for _, want := range []string{"preserved verbatim", "Continue the conversation", "/tmp/t.jsonl"} {
		if !strings.Contains(m2, want) {
			t.Fatalf("可选段缺 %q: %q", want, m2)
		}
	}
}
