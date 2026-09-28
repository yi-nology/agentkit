// 跨包组合验证：ZCode 移植五件套拼成完整的「工具调用治理」故事——
// bashguard 静态解析 → permgate 许可判定 → hookx PreToolUse 改写后复检 →
// toolsched 并发调度 → compact 压力面。验证的不是单件正确性（各包单测已覆盖），
// 而是件与件的接缝语义（改写后重走许可链、注解直转调度、只读放行链一致）。
package agentkit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yi-nology/agentkit/bashguard"
	"github.com/yi-nology/agentkit/compact"
	"github.com/yi-nology/agentkit/hookx"
	"github.com/yi-nology/agentkit/permgate"
	"github.com/yi-nology/agentkit/toolsched"
)

func b(v bool) *bool { return &v }

// TestCompositionPermissionStory 一条 bash 命令从静态解析到调度的全链。
func TestCompositionPermissionStory(t *testing.T) {
	ctx := context.Background()

	// 1. 静态解析：ls 可判只读；rm 不可
	if d := bashguard.Analyze("ls -la", nil); !d.ReadOnly {
		t.Fatalf("ls 应只读: %s", d.Reason)
	}
	if d := bashguard.Analyze("rm -rf /tmp/x", nil); d.ReadOnly {
		t.Fatal("rm 不可只读")
	}

	// 2. 许可判定：bashguard 结论作为 permgate 的只读能力来源
	gate := &permgate.Service{}
	roDecision := gate.Check(ctx, "bash", map[string]any{"command": "ls -la"},
		permgate.ModeDefault, permgate.Capability{
			ReadOnly:    bashguard.Analyze("ls -la", nil).ReadOnly,
			AlwaysAsk:   !bashguard.Analyze("ls -la", nil).ReadOnly, // 非只读 bash 逐次确认
		})
	if roDecision.Behavior != permgate.Allow {
		t.Fatalf("只读 bash 应放行: %+v", roDecision)
	}
	rwDecision := gate.Check(ctx, "bash", map[string]any{"command": "rm -rf /tmp"},
		permgate.ModeYolo, permgate.Capability{AlwaysAsk: true})
	if rwDecision.Behavior != permgate.Ask {
		t.Fatalf("写类 bash 在 yolo 也应确认（alwaysAsk）: %+v", rwDecision)
	}

	// 3. hook 改写输入后重走许可链：hook 把 rm 改写为 ls，改写后放行
	//    （hook 注入的输入不绕过判定——重走的是同一条链）
	runner := &hookx.Runner{Hooks: []hookx.Hook{{
		Command: []string{"sh", "-c", `cat >/dev/null; printf '%s' '{"decision":"approve","hookSpecificOutput":{"hookEventName":"pre_tool_use","updatedInput":{"command":"ls -la"}}}'`},
		Events:  "pre_tool_use",
	}}}
	hr := runner.Run(ctx, hookx.EventPreToolUse, hookx.Input{Event: hookx.EventPreToolUse, Tool: "bash", ToolInput: map[string]any{"command": "rm -rf /tmp"}})
	if hr.PermissionBehavior != "allow" || hr.UpdatedInput == nil {
		t.Fatalf("hook 应改写并升格: %+v", hr)
	}
	rewritten := hr.UpdatedInput.(map[string]any)
	// 改写后以新输入重走 bashguard+permgate（消费方纪律）
	recheckRO := bashguard.Analyze(rewritten["command"].(string), nil).ReadOnly
	recheck := gate.Check(ctx, "bash", rewritten, permgate.ModeDefault, permgate.Capability{ReadOnly: recheckRO, AlwaysAsk: !recheckRO})
	if recheck.Behavior != permgate.Allow {
		t.Fatalf("改写后的只读命令应放行: %+v", recheck)
	}

	// 4. 调度：bashguard 注解直转 toolsched.Hints——只读并行、写独占
	s := &toolsched.Scheduler{MaxConcurrency: 10}
	calls := []toolsched.Call{
		{ID: "r1", Name: "bash", Hints: toolsched.Hints{ReadOnly: b(true)}},
		{ID: "r2", Name: "bash", Hints: toolsched.Hints{ReadOnly: b(true)}},
		{ID: "w1", Name: "bash", Hints: toolsched.Hints{ReadOnly: b(false), Idempotent: b(false)}},
	}
	sched, err := s.Schedule(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"r1", "r2"}, {"w1"}}
	if len(sched.Groups) != len(want) {
		t.Fatalf("分组: %v", sched.Groups)
	}
	for i, g := range sched.Groups {
		if strings.Join(g, ",") != strings.Join(want[i], ",") {
			t.Fatalf("组[%d]: %v want %v", i, g, want[i])
		}
	}

	// 5. 压力面：多轮工具转写接 compact 判定（只读链路的观测续存）
	msgs := make([]compact.Message, 0, 40)
	for i := 0; i < 40; i++ {
		msgs = append(msgs,
			compact.Message{Role: "user", Text: "u"},
			compact.Message{Role: "assistant", ToolCalls: []compact.ToolCall{{ID: string(rune('a' + i%26)), Name: "bash"}}},
			compact.Message{Role: "tool", ToolCallID: string(rune('a' + i%26)), ToolName: "bash", Text: strings.Repeat("x", 3000)},
		)
	}
	d := compact.ShouldCompact(msgs, compact.Config{ContextWindow: 50_000}, 0, nil)
	if !d.ShouldCompact {
		t.Fatal("40 轮大结果应触发压缩")
	}
	mr := compact.MaybeMicrocompact(msgs, compact.MicroConfig{
		ThresholdTokens: compact.BuildDefaultThreshold(d.Threshold),
	}, time.Now(), time.Now())
	if mr.Reason != compact.MicroApplied {
		t.Fatalf("microcompact 应生效: %s", mr.Reason)
	}
}
