package hookx

import (
	"context"
	"strings"
	"testing"
	"time"
)

// shHook 构造一个经 sh 的测试 hook：吞 stdin 后输出给定 JSON。
func shHook(t *testing.T, events, out string) Hook {
	t.Helper()
	return Hook{Command: []string{"sh", "-c", "cat >/dev/null; printf '%s' '" + out + "'"}, Events: events}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		pattern string
		event   Event
		want    bool
	}{
		{"", EventPreToolUse, true},
		{"*", EventStop, true},
		{"pre_tool_use|post_tool_use", EventPreToolUse, true},
		{"pre_tool_use|post_tool_use", EventSessionStart, false},
		{"/.*tool.*/", EventPostToolUse, true},
		{"/^session_/", EventSessionStart, true},
		{"/^session_/", EventStop, false},
	}
	for _, tc := range cases {
		if got := (Hook{Events: tc.pattern}).matches(tc.event); got != tc.want {
			t.Errorf("pattern=%q event=%s: got %v", tc.pattern, tc.event, got)
		}
	}
}

func TestRunBlockDecision(t *testing.T) {
	r := &Runner{Hooks: []Hook{
		shHook(t, "pre_tool_use", `{"decision":"block","reason":"危险命令"}`),
	}}
	res := r.Run(context.Background(), EventPreToolUse, Input{Event: EventPreToolUse, Tool: "bash"})
	if !res.BlockRequested || !res.PreventContinuation || res.PermissionBehavior != "deny" {
		t.Fatalf("block 决策: %+v", res)
	}
	if res.StopReason != "危险命令" {
		t.Fatalf("stopReason: %q", res.StopReason)
	}
}

func TestRunApproveAndContext(t *testing.T) {
	r := &Runner{Hooks: []Hook{
		shHook(t, "permission_request", `{"decision":"approve","additionalContext":"已核对"}`),
	}}
	res := r.Run(context.Background(), EventPermissionRequest, Input{Event: EventPermissionRequest})
	if res.PermissionBehavior != "allow" || len(res.AdditionalContexts) != 1 || res.AdditionalContexts[0] != "已核对" {
		t.Fatalf("approve+context: %+v", res)
	}
}

func TestRunContinueFalse(t *testing.T) {
	r := &Runner{Hooks: []Hook{
		shHook(t, "*", `{"continue":false,"stopReason":"拦截"}`),
	}}
	res := r.Run(context.Background(), EventUserPromptSubmit, Input{Event: EventUserPromptSubmit})
	if !res.BlockRequested || res.StopReason != "拦截" {
		t.Fatalf("continue:false: %+v", res)
	}
	// Stop 事件上 continue:false 表示否决停止（继续跑）；continue:true 无意见
	r2 := &Runner{Hooks: []Hook{shHook(t, "stop", `{"continue":false,"stopReason":"还有活"}`)}}
	res2 := r2.Run(context.Background(), EventStop, Input{Event: EventStop})
	if !res2.StopShouldContinue || res2.StopReason != "还有活" {
		t.Fatalf("stop 事件 continue:false 应否决停止: %+v", res2)
	}
	r3 := &Runner{Hooks: []Hook{shHook(t, "stop", `{"continue":true}`)}}
	res3 := r3.Run(context.Background(), EventStop, Input{Event: EventStop})
	if res3.StopShouldContinue {
		t.Fatalf("stop 事件 continue:true 应无意见: %+v", res3)
	}
}

func TestRunUpdatedInput(t *testing.T) {
	r := &Runner{Hooks: []Hook{
		shHook(t, "pre_tool_use", `{"hookSpecificOutput":{"hookEventName":"pre_tool_use","updatedInput":{"command":"ls --color"}}}`),
	}}
	res := r.Run(context.Background(), EventPreToolUse, Input{Event: EventPreToolUse})
	m, ok := res.UpdatedInput.(map[string]any)
	if !ok || m["command"] != "ls --color" {
		t.Fatalf("改写输入: %+v", res.UpdatedInput)
	}
}

func TestRunMergeDenyMonotonic(t *testing.T) {
	r := &Runner{Hooks: []Hook{
		shHook(t, "pre_tool_use", `{"decision":"approve"}`),
		shHook(t, "pre_tool_use", `{"decision":"block","reason":"后者否决"}`),
		shHook(t, "pre_tool_use", `{"additional_context":"补充材料"}`),
	}}
	res := r.Run(context.Background(), EventPreToolUse, Input{Event: EventPreToolUse})
	if res.PermissionBehavior != "deny" || !res.BlockRequested {
		t.Fatalf("deny 单调: %+v", res)
	}
	if len(res.AdditionalContexts) != 1 {
		t.Fatalf("上下文累积: %v", res.AdditionalContexts)
	}
}

func TestRunWrongEventNameIgnored(t *testing.T) {
	var errs []error
	r := &Runner{
		Hooks:   []Hook{shHook(t, "pre_tool_use", `{"hookSpecificOutput":{"hookEventName":"post_tool_use"}}`)},
		OnError: func(h Hook, err error) { errs = append(errs, err) },
	}
	res := r.Run(context.Background(), EventPreToolUse, Input{Event: EventPreToolUse})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "事件名") {
		t.Fatalf("事件名不符应报错: %v", errs)
	}
	if res.BlockRequested {
		t.Fatal("坏输出不应产生决策")
	}
}

func TestRunFailureTolerated(t *testing.T) {
	var errs []error
	r := &Runner{
		Hooks: []Hook{
			{Command: []string{"/nonexistent/hook"}, Events: "*"},
			shHook(t, "*", `not json`),
			shHook(t, "*", `{"decision":"block","reason":"好 hook"}`),
		},
		OnError: func(h Hook, err error) { errs = append(errs, err) },
	}
	res := r.Run(context.Background(), EventPreToolUse, Input{Event: EventPreToolUse})
	if len(errs) != 2 {
		t.Fatalf("两个坏 hook 应上报: %d", len(errs))
	}
	if !res.BlockRequested || res.StopReason != "好 hook" {
		t.Fatalf("坏 hook 不应拖累好 hook: %+v", res)
	}
}

func TestRunTimeout(t *testing.T) {
	var errs []error
	r := &Runner{
		Hooks:   []Hook{{Command: []string{"sh", "-c", "cat >/dev/null; sleep 5"}, Events: "*", Timeout: 100 * time.Millisecond}},
		OnError: func(h Hook, err error) { errs = append(errs, err) },
	}
	start := time.Now()
	res := r.Run(context.Background(), EventPreToolUse, Input{Event: EventPreToolUse})
	if len(errs) != 1 {
		t.Fatalf("超时应报错: %v", errs)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("超时预算应生效")
	}
	if res.BlockRequested {
		t.Fatal("超时 hook 无决策")
	}
}

// TestStdinPayloadDelivered 回归（procx StdinPipe 曾在 Start 后建——payload
// 永远送不进子进程）：hook 读取 stdin 并按内容决策，stdin 断言真实可达。
func TestStdinPayloadDelivered(t *testing.T) {
	h := Hook{Command: []string{"sh", "-c",
		`v=$(cat); case "$v" in *bash*) printf '%s' '{"decision":"approve"}';; esac`}, Events: "*"}
	r := &Runner{Hooks: []Hook{h}}
	res := r.Run(context.Background(), EventPreToolUse, Input{Event: EventPreToolUse, Tool: "bash"})
	if res.PermissionBehavior != "allow" {
		t.Fatalf("stdin 载荷未送达（hook 读不到 tool 名）: %+v", res)
	}
}

func TestRunEmpty(t *testing.T) {
	r := &Runner{}
	res := r.Run(context.Background(), EventStop, Input{Event: EventStop})
	if res.BlockRequested || len(res.AdditionalContexts) != 0 {
		t.Fatalf("无 hook 空结果: %+v", res)
	}
	// 空输出 = 无意见
	r2 := &Runner{Hooks: []Hook{shHook(t, "*", "")}}
	res2 := r2.Run(context.Background(), EventStop, Input{Event: EventStop})
	if res2.BlockRequested || res2.StopShouldContinue {
		t.Fatalf("空输出=无意见: %+v", res2)
	}
}
