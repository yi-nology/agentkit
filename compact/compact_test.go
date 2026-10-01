package compact

import (
	"strings"
	"testing"
	"time"
)

func TestEstimateTokensCountsToolCallArgs(t *testing.T) {
	msgs := []Message{
		{Role: "user", Text: "abcd"}, // 4 runes
		{Role: "assistant", Text: "", ToolCalls: []ToolCall{
			{ID: "1", Name: "read_file", ArgsJSON: `{"path":"aaaaaaaaaaaaaaaa"}`}, // 10+24=34 runes
		}},
	}
	got := EstimateTokens(msgs)
	// CJK 保守口径（v0.12.1，第九轮审计）：4 runes→3；工具入参 36 runes→25
	if got != 3+25 {
		t.Fatalf("工具入参应计入估算（CJK 口径）: got %d want 28", got)
	}
}

func TestConfigEffectiveWindowAndThreshold(t *testing.T) {
	c := Config{} // 全缺省：200k 窗口，reserve=min(32k,21k)=21k，buffer=13k
	if got := c.EffectiveWindow(); got != 200_000-21_000 {
		t.Fatalf("有效窗口: %d", got)
	}
	if got := c.Threshold(); got != 200_000-21_000-13_000 {
		t.Fatalf("阈值: %d", got)
	}
	// 自定义 output 目标钳制到 ReserveCap
	c2 := Config{ContextWindow: 128_000, MaxOutputTokens: 50_000}
	if got := c2.outputReserve(); got != ReserveCap {
		t.Fatalf("预留应钳到 ReserveCap: %d", got)
	}
	// 窗口小于预留：floor 0
	c3 := Config{ContextWindow: 10_000}
	if got := c3.EffectiveWindow(); got != 0 {
		t.Fatalf("窗口小于预留应落 0: %d", got)
	}
}

func mkTranscript(assistantRounds int, toolResults int) []Message {
	var msgs []Message
	for i := 0; i < assistantRounds; i++ {
		msgs = append(msgs, Message{Role: "user", Text: "u"})
		msgs = append(msgs, Message{Role: "assistant", Text: "a", ToolCalls: []ToolCall{
			{ID: string(rune('a' + i)), Name: "read"},
		}})
		for j := 0; j < toolResults; j++ {
			msgs = append(msgs, Message{Role: "tool", ToolCallID: string(rune('a' + i)),
				ToolName: "read", Text: strings.Repeat("x", 3000)})
		}
	}
	return msgs
}

func TestShouldCompactDecisionChain(t *testing.T) {
	disabled := false
	cases := []struct {
		name    string
		msgs    []Message
		cfg     Config
		fail    int
		ov      *TokenOverride
		want    Reason
		compact bool
	}{
		{"关闭", mkTranscript(3, 1), Config{Enabled: &disabled}, 0, nil, ReasonDisabled, false},
		{"消息不足", []Message{{Role: "user", Text: "hi"}, {Role: "assistant", Text: "yo"}},
			Config{}, 0, nil, ReasonNotEnough, false},
		{"熔断", mkTranscript(3, 1), Config{}, MaxConsecutiveFailures, nil, ReasonCircuitBreaker, false},
		{"未到阈值", mkTranscript(3, 1), Config{}, 0, nil, ReasonBelowThreshold, false},
		{"超阈值", mkTranscript(40, 3), Config{ContextWindow: 128_000}, 0, nil, ReasonAboveThreshold, true},
		{"provider 用量优先", mkTranscript(3, 1), Config{}, 0,
			&TokenOverride{TokenCount: 500_000}, ReasonAboveThreshold, true},
	}
	for _, tc := range cases {
		d := ShouldCompact(tc.msgs, tc.cfg, tc.fail, tc.ov)
		if d.Reason != tc.want || d.ShouldCompact != tc.compact {
			t.Errorf("%s: reason=%s compact=%v", tc.name, d.Reason, d.ShouldCompact)
		}
	}
	// provider 口径回填
	d := ShouldCompact(mkTranscript(3, 1), Config{}, 0, &TokenOverride{TokenCount: 500_000})
	if d.TokenSource != SourceProviderUsage || d.TokenCount != 500_000 || d.EstimatedTokens == d.TokenCount {
		t.Fatalf("provider 口径应优先且保留估算值: %+v", d)
	}
	// 熔断边界：failures = max-1 不熔断（最后机会）
	d = ShouldCompact(mkTranscript(40, 3), Config{ContextWindow: 128_000}, MaxConsecutiveFailures-1, nil)
	if d.Reason != ReasonAboveThreshold {
		t.Fatalf("failures=max-1 不应熔断: %s", d.Reason)
	}
}

func TestBuildDefaultThreshold(t *testing.T) {
	cases := []struct{ thr, want int }{
		{200_000, min(180_000, 198_000)}, // ratio 胜
		{10_000, min(9_000, 8_000)},      // buffer 胜
		{2_000, 0},                       // floor
	}
	for _, tc := range cases {
		if got := BuildDefaultThreshold(tc.thr); got != tc.want {
			t.Errorf("BuildDefaultThreshold(%d)=%d want %d", tc.thr, got, tc.want)
		}
	}
}

func TestMicrocompactNotTriggered(t *testing.T) {
	msgs := mkTranscript(3, 1)
	now := time.Now()
	// 无阈值无空闲
	r := MaybeMicrocompact(msgs, MicroConfig{}, time.Time{}, now)
	if r.Reason != MicroNotTriggered {
		t.Fatalf("应未触发: %s", r.Reason)
	}
	// 未到阈值、不空闲
	r = MaybeMicrocompact(msgs, MicroConfig{ThresholdTokens: 10_000_000}, now.Add(-time.Minute), now)
	if r.Reason != MicroNotTriggered {
		t.Fatalf("未到阈值不应触发: %s", r.Reason)
	}
	// 原样返回
	if &r.Messages[0] != &msgs[0] || len(r.Messages) != len(msgs) {
		t.Fatal("未触发应原样返回")
	}
}

func TestMicrocompactIdleTrigger(t *testing.T) {
	msgs := mkTranscript(8, 1) // 8 组，保 5 清 3
	now := time.Now()
	r := MaybeMicrocompact(msgs, MicroConfig{}, now.Add(-2*time.Hour), now)
	if r.Reason != MicroApplied {
		t.Fatalf("空闲应触发清除: %s", r.Reason)
	}
	if r.Trigger != TriggerIdle {
		t.Fatalf("触发源: %s", r.Trigger)
	}
	// 最早 3 轮的工具结果被占位，最近 5 轮保留
	cleared := 0
	for _, m := range r.Messages {
		if m.Role == "tool" && m.Text == ClearedPlaceholder {
			cleared++
		}
	}
	if cleared != 3 {
		t.Fatalf("应清除 3 个结果: %d", cleared)
	}
	if len(r.ClearedIDs) != 3 {
		t.Fatalf("清除明细: %v", r.ClearedIDs)
	}
	if r.TokensSaved <= 0 || r.TokensAfter >= r.TokensBefore {
		t.Fatalf("节省计量: before=%d after=%d saved=%d", r.TokensBefore, r.TokensAfter, r.TokensSaved)
	}
}

func TestMicrocompactTokenTriggerAndKeepRecent(t *testing.T) {
	msgs := mkTranscript(6, 2) // 6 组×2 结果，保 5 清 1 组（2 个结果）
	now := time.Now()
	r := MaybeMicrocompact(msgs, MicroConfig{ThresholdTokens: 1}, now, now)
	if r.Reason != MicroApplied || r.Trigger != TriggerTokens {
		t.Fatalf("token 压力应触发: %s %s", r.Reason, r.Trigger)
	}
	cleared := 0
	for _, m := range r.Messages {
		if m.Role == "tool" && m.Text == ClearedPlaceholder {
			cleared++
		}
	}
	if cleared != 2 {
		t.Fatalf("应清除最早一组 2 个结果: %d", cleared)
	}
}

func TestMicrocompactNothingToClear(t *testing.T) {
	msgs := mkTranscript(3, 1) // 3 组 ≤ 保 5
	now := time.Now()
	r := MaybeMicrocompact(msgs, MicroConfig{ThresholdTokens: 1}, now, now)
	if r.Reason != MicroNothingToClear {
		t.Fatalf("组数不足应 nothing_to_clear: %s", r.Reason)
	}
}

func TestMicrocompactNoCandidates(t *testing.T) {
	msgs := []Message{
		{Role: "user", Text: strings.Repeat("x", 5000)},
		{Role: "assistant", Text: strings.Repeat("y", 5000)},
		{Role: "user", Text: strings.Repeat("z", 5000)},
	}
	now := time.Now()
	r := MaybeMicrocompact(msgs, MicroConfig{ThresholdTokens: 1}, now, now)
	if r.Reason != MicroNoCandidates {
		t.Fatalf("无工具结果应 no_candidates: %s", r.Reason)
	}
}

func TestMicrocompactWhitelistAndErrors(t *testing.T) {
	// 两组：read（白名单）+ deploy（不在）+ 出错结果
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "1", Name: "read"}}},
		{Role: "tool", ToolCallID: "1", ToolName: "read", Text: strings.Repeat("x", 3000)},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "2", Name: "deploy"}}},
		{Role: "tool", ToolCallID: "2", ToolName: "deploy", Text: strings.Repeat("y", 3000)},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "3", Name: "read"}}},
		{Role: "tool", ToolCallID: "3", ToolName: "read", Text: strings.Repeat("z", 3000)},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "4", Name: "read"}}},
		{Role: "tool", ToolCallID: "4", ToolName: "read", Text: strings.Repeat("w", 3000), IsError: true},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "5", Name: "read"}}},
		{Role: "tool", ToolCallID: "5", ToolName: "read", Text: strings.Repeat("v", 3000)},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "6", Name: "read"}}},
		{Role: "tool", ToolCallID: "6", ToolName: "read", Text: strings.Repeat("u", 3000)},
	}
	now := time.Now()
	// 白名单 [read]，保 3 组：deploy 结果与出错结果不构成组，可清组=[x,z,v,u]
	// → 清最早 1 组（x）
	r := MaybeMicrocompact(msgs, MicroConfig{ThresholdTokens: 1, KeepRecent: 3,
		CompactableTools: []string{"read"}}, now, now)
	if r.Reason != MicroApplied {
		t.Fatalf("应清除: %s", r.Reason)
	}
	if r.Messages[1].Text != ClearedPlaceholder {
		t.Fatal("最早 read 组应被清除")
	}
	if r.Messages[3].Text == ClearedPlaceholder {
		t.Fatal("非白名单工具不应被清除")
	}
	if r.Messages[7].Text == ClearedPlaceholder {
		t.Fatal("出错结果默认保留")
	}
	// ClearErrorResults=true 时错误结果也可清
	// ClearErrorResults=true：出错结果也成组，可清组=[x,z,w,v,u] → 清最早 2 组
	r2 := MaybeMicrocompact(msgs, MicroConfig{ThresholdTokens: 1, KeepRecent: 3,
		CompactableTools: []string{"read"}, ClearErrorResults: true}, now, now)
	if r2.Reason != MicroApplied {
		t.Fatalf("开错误清除应通过: %s", r2.Reason)
	}
}

func TestMicrocompactBelowSavings(t *testing.T) {
	// 极小的工具结果：清了也省不了几个 token
	msgs := mkTranscript(6, 1)
	for i := range msgs {
		if msgs[i].Role == "tool" {
			msgs[i].Text = "ok" // 2 chars
		}
	}
	now := time.Now()
	r := MaybeMicrocompact(msgs, MicroConfig{ThresholdTokens: 1}, now, now)
	if r.Reason != MicroBelowSavings {
		t.Fatalf("节省不足应 below_min_savings: %s", r.Reason)
	}
	for _, m := range r.Messages {
		if m.Text == ClearedPlaceholder {
			t.Fatal("不足门槛应返回原转写")
		}
	}
}

func TestMicrocompactDisabled(t *testing.T) {
	disabled := false
	now := time.Now()
	r := MaybeMicrocompact(mkTranscript(8, 1), MicroConfig{Enabled: &disabled}, now.Add(-2*time.Hour), now)
	if r.Reason != MicroDisabled {
		t.Fatalf("应 disabled: %s", r.Reason)
	}
}

func TestMicrocompactIdempotent(t *testing.T) {
	msgs := mkTranscript(8, 1)
	now := time.Now()
	first := MaybeMicrocompact(msgs, MicroConfig{ThresholdTokens: 1}, now, now)
	second := MaybeMicrocompact(first.Messages, MicroConfig{ThresholdTokens: 1}, now, now)
	// 第二轮：已清除的占位结果被跳过，可清组减少
	var cleared int
	for _, m := range second.Messages {
		if m.Role == "tool" && m.Text == ClearedPlaceholder {
			cleared++
		}
	}
	if cleared != len(first.ClearedIDs) {
		t.Fatalf("已清除结果不应重复清除: %d vs %d", cleared, len(first.ClearedIDs))
	}
}
