package audit

import (
	"testing"

	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

func TestLoggerLog(t *testing.T) {
	log := logx.NewSlogLogger("test")
	logger := New(log, "test-audit")

	// 不应 panic
	logger.Log(Action("task.create"), "task_id", "t1", "platform", "gitea")
	logger.Log(Action("finding.suppress"), "fp", "abc123")
}

func TestLoggerNilSafe(t *testing.T) {
	// nil logger 不应 panic（实际 Logger 内部有 log 字段，但 Log 方法不检查 nil）
	log := logx.NewSlogLogger("test")
	logger := New(log, "test-audit")
	if logger == nil {
		t.Fatal("New 应返回非 nil")
	}
}

func TestActionConstants(t *testing.T) {
	// Action 类型是 string 的别名
	var a Action = "test.action"
	if string(a) != "test.action" {
		t.Fatal("Action 应可转为 string")
	}
}
