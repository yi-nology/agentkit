package audit

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/oekit/logx"
)

// TestFallbackLoggerImmuneToLogLevel 审计兜底 logger 不随 LOG_LEVEL 丢留痕：
// logx v0.3.0 起 NewSlogLogger 尊重 LOG_LEVEL 环境变量，生产常见 LOG_LEVEL=error
// 会吞掉 Info 级事件——New(nil, ...) 回退 logx.NewAuditLogger（级别锁 Info，v0.5.1
// 起门面承诺）。对照组证明裸 NewSlogLogger 确实被吞、守卫是必要的。
func TestFallbackLoggerImmuneToLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "error")
	out := captureStdout(t, func() {
		New(nil, "test-audit").Log("task.create", "task_id", "t1")
	})
	if !strings.Contains(out, "audit.event") {
		t.Fatalf("LOG_LEVEL=error 下审计事件应仍落盘，实际输出: %q", out)
	}
	ctl := captureStdout(t, func() {
		logx.NewSlogLogger("ctl").Info("plain.info")
	})
	if strings.Contains(ctl, "plain.info") {
		t.Fatalf("对照组：LOG_LEVEL=error 下未钉级别的裸 logger 应吞 Info，实际输出: %q", ctl)
	}
}

// captureStdout 截获 fn 执行期间写入 os.Stdout 的内容（logx slog 后端写时取 os.Stdout）。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = orig
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return buf.String()
}

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
