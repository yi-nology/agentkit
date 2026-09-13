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

// 凭据字段值必须打码，普通业务字段/非字符串保留。
func TestRedactFields(t *testing.T) {
	in := []any{
		"url", "postgres://user:pw@db/x",
		"auth", "Bearer sk-abc123secret",
		"n", 42,
		"ok", "plain",
		"pem", "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADAN\n-----END PRIVATE KEY-----",
	}
	out := redactFields(in)
	if out[1] != "postgres://user:****@db/x" {
		t.Fatalf("URL 凭据未打码: %v", out[1])
	}
	if s, _ := out[3].(string); s == "" || s == "Bearer sk-abc123secret" {
		t.Fatalf("Bearer 未打码: %v", out[3])
	}
	if out[5] != 42 {
		t.Fatalf("非字符串字段被改动: %v", out[5])
	}
	if out[7] != "plain" {
		t.Fatalf("普通串被误伤: %v", out[7])
	}
	if s, _ := out[9].(string); len(s) == 0 || s == in[9] {
		t.Fatalf("PEM 私钥未打码: %v", out[9])
	}
}

// Log 路径不 panic 且会走 redactFields（字符串含 token= 时被替换）。
func TestLoggerLogWithSecret(t *testing.T) {
	logger := New(logx.NewSlogLogger("test"), "test-audit")
	logger.Log(Action("qa.reply"), "question", "见 token=sk-should-not-appear", "task_id", "t-1")
}
