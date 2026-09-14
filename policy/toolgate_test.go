package policy

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// fakeTool 记录式工具替身：记录是否被真实调用。
type fakeTool struct {
	name   string
	called bool
}

func (f *fakeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name}, nil
}

func (f *fakeTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	f.called = true
	return "ok", nil
}

// 编译期确认实现 InvokableTool（Info+InvokableRun）。
var _ tool.InvokableTool = (*fakeTool)(nil)

func TestWithAuditGateDeniesMutatingCall(t *testing.T) {
	inner := &fakeTool{name: "shell_execute"}
	var got Decision
	wrapped := WithAuditGate(NewGate(nil, nil),
		Op{Type: OpToolCall, Mode: ModeConfirm, Tool: "shell_execute", Mutating: true},
		func(_ Op, dec Decision) { got = dec }, inner)
	if _, err := wrapped.InvokableRun(context.Background(), "{}"); err == nil {
		t.Fatal("采集面变异工具调用应被拒绝")
	}
	if inner.called {
		t.Fatal("被拒绝的调用不应触达底层工具")
	}
	if got.Verdict != VerdictDeny || got.Decider != DeciderRule {
		t.Fatalf("留痕回调应收到 deny 裁决，got %+v", got)
	}
}

func TestWithAuditGatePassesReadonlyCall(t *testing.T) {
	inner := &fakeTool{name: "get_load"}
	audits := 0
	wrapped := WithAuditGate(NewGate(nil, nil),
		Op{Type: OpToolCall, Mode: ModeAuto, Tool: "get_load"},
		func(_ Op, _ Decision) { audits++ }, inner)
	if out, err := wrapped.InvokableRun(context.Background(), "{}"); err != nil || out != "ok" {
		t.Fatalf("只读调用应透传: out=%q err=%v", out, err)
	}
	if !inner.called || audits != 1 {
		t.Fatalf("底层应被调用且留痕一次: called=%v audits=%d", inner.called, audits)
	}
}

func TestWithAuditGateNilGatePassthrough(t *testing.T) {
	inner := &fakeTool{name: "shell_execute"}
	wrapped := WithAuditGate(nil,
		Op{Type: OpToolCall, Mode: ModeConfirm, Tool: "shell_execute", Mutating: true},
		nil, inner)
	if _, err := wrapped.InvokableRun(context.Background(), "{}"); err != nil {
		t.Fatalf("未装配审计门应保持存量行为: %v", err)
	}
	if !inner.called {
		t.Fatal("底层应被调用")
	}
}
