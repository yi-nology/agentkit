package agentrun

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// newShellExecTool 名字含变更类动词段的测试工具（执行成功，便于让首轮在工具生效后
// 以「空最终答复」失败——真实场景里副作用已在首轮落地）。
func newShellExecTool(t *testing.T) tool.BaseTool {
	t.Helper()
	st, err := utils.InferTool("shell_exec", "执行命令",
		func(_ context.Context, in *echoIn) (*echoOut, error) {
			return &echoOut{Echo: in.Text}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestIsMutatingTool 下划线分段精确匹配：动词段命中即变更类；子串误报不发生。
func TestIsMutatingTool(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"shell_exec", true},
		{"service_restart", true},
		{"kill", true},
		{"use_skill", false},          // 子串含 kill 但分段不匹配——不误判
		{"get_running_config", false}, // 子串含 run 但分段不匹配
		{"read_file", false},
		{"KILL_ALL", true}, // 大小写归一
	}
	for _, tc := range cases {
		if got := IsMutatingTool(tc.name); got != tc.want {
			t.Errorf("IsMutatingTool(%q) = %v, 期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestSideEffectTracker 观测器：tool_call 事件置位，其余事件不动；Wrap 透传原回调。
func TestSideEffectTracker(t *testing.T) {
	var tracker SideEffectTracker
	var passed []Event
	wrapped := tracker.Wrap(func(ev Event) { passed = append(passed, ev) })

	wrapped(Event{Type: EventToolCall, Tool: "echo"})
	wrapped(Event{Type: EventToolCall, Tool: "shell_exec"})
	wrapped(Event{Type: EventToolResult, Tool: "shell_exec"})

	if !tracker.Called() {
		t.Fatal("变更类工具调用后 Called 应为 true")
	}
	if tracker.MutatingTool() != "shell_exec" {
		t.Fatalf("MutatingTool = %q，期望 shell_exec（首个变更类工具）", tracker.MutatingTool())
	}
	if len(passed) != 3 {
		t.Fatalf("事件应全量透传: %d", len(passed))
	}

	var idle SideEffectTracker
	idle.Observe(Event{Type: EventToolCall, Tool: "read_file"})
	if idle.Called() {
		t.Fatal("只读工具不应置位")
	}
}

// TestRunWithRetrySkipsAfterMutation 首轮变更类工具已生效后失败 → 不整体重跑
// （只打 1 次请求），错误说明跳过原因并包裹原始错误。
func TestRunWithRetrySkipsAfterMutation(t *testing.T) {
	// 首轮：shell_exec 真实执行（副作用落地）→ 空最终答复 → 运行失败。
	m, cm := newMockOpenAI(t,
		mockResponse{toolCall: &mockToolCall{id: "c1", name: "shell_exec", arguments: `{"text":"reboot -h"}`}},
		mockResponse{content: ""},
	)

	_, err := RunWithRetry(context.Background(), Config{
		Name: "test", Instruction: "inst", Model: cm,
		Tools: []tool.BaseTool{newShellExecTool(t)},
	}, "执行重启", "带反馈重跑")
	if err == nil {
		t.Fatal("应失败")
	}
	if !strings.Contains(err.Error(), "跳过重试") || !strings.Contains(err.Error(), "shell_exec") {
		t.Fatalf("错误应说明副作用守卫: %v", err)
	}
	if n := m.calls.Load(); n != 2 {
		t.Fatalf("守卫生效不应发起第二次运行（2 次请求=首轮 ReAct 的工具轮+答复轮）: calls = %d", n)
	}
	if !strings.Contains(err.Error(), "未产出最终文本") && !strings.Contains(err.Error(), "最终答复为空") {
		t.Fatalf("应保留原始失败原因（%%w 可解包）: %v", err)
	}
}

// TestRunWithEventsAndRetrySkipsAfterMutation 事件回调路径同一守卫；观测事件照常全量回调。
func TestRunWithEventsAndRetrySkipsAfterMutation(t *testing.T) {
	m, cm := newMockOpenAI(t,
		mockResponse{toolCall: &mockToolCall{id: "c1", name: "shell_exec", arguments: `{"text":"x"}`}},
		mockResponse{content: ""},
	)

	var toolCalls []string
	_, err := RunWithEventsAndRetry(context.Background(), Config{
		Name: "test", Instruction: "inst", Model: cm,
		Tools: []tool.BaseTool{newShellExecTool(t)},
	}, "执行", "重跑", func(ev Event) {
		if ev.Type == EventToolCall {
			toolCalls = append(toolCalls, ev.Tool)
		}
	})
	if err == nil || !strings.Contains(err.Error(), "跳过重试") {
		t.Fatalf("应触发副作用守卫: %v", err)
	}
	if len(toolCalls) != 1 || toolCalls[0] != "shell_exec" {
		t.Fatalf("首轮工具调用事件应照常回调: %v", toolCalls)
	}
	if n := m.calls.Load(); n != 2 {
		t.Fatalf("不应发起第二次运行: calls = %d", n)
	}
}

// TestRunWithRetryAfterMutationAllowed RetryAfterMutation=true：恢复无条件重试。
func TestRunWithRetryAfterMutationAllowed(t *testing.T) {
	m, cm := newMockOpenAI(t,
		mockResponse{toolCall: &mockToolCall{id: "c1", name: "shell_exec", arguments: `{"text":"x"}`}},
		mockResponse{content: ""},
		mockResponse{content: "重试成功"},
	)

	out, err := RunWithRetry(context.Background(), Config{
		Name: "test", Instruction: "inst", Model: cm,
		Tools:              []tool.BaseTool{newShellExecTool(t)},
		RetryAfterMutation: true,
	}, "执行", "带反馈重跑")
	if err != nil || out != "重试成功" {
		t.Fatalf("显式允许后应重试成功: %v %q", err, out)
	}
	if n := m.calls.Load(); n < 2 {
		t.Fatalf("应发起第二次运行: calls = %d", n)
	}
}

// TestRunWithRetryReadOnlyRetries 只读工具失败仍照常重试（守卫不误伤）。
func TestRunWithRetryReadOnlyRetries(t *testing.T) {
	_, cm := newMockOpenAI(t,
		mockResponse{content: ""},
		mockResponse{content: "重试成功"},
	)

	out, err := RunWithRetry(context.Background(), Config{
		Name: "test", Instruction: "inst", Model: cm,
	}, "第一问", "第二问")
	if err != nil || out != "重试成功" {
		t.Fatalf("无变更类工具应照常重试: %v %q", err, out)
	}
}
