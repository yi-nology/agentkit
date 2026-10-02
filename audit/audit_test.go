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
	// nil receiver 与 nil logger 都不应 panic（审计留痕缺失好过进程崩溃）
	var l *Logger
	l.Log("test.action", "k", "v")

	logger := New(nil, "test-audit") // nil logger → 回退缺省 logger
	if logger == nil {
		t.Fatal("New 应返回非 nil")
	}
	logger.Log("test.action", "k", "v")
}

func TestActionConstants(t *testing.T) {
	// Action 类型是 string 的别名
	var a Action = "test.action"
	if string(a) != "test.action" {
		t.Fatal("Action 应可转为 string")
	}
}
