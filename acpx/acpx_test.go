package acpx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCLI 创建可执行的假 CLI 脚本（脚本内容 = shell 代码），返回其路径。
// 让解析逻辑在无真实 agent 环境下全链路验证。
func fakeCLI(t *testing.T, code string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+code), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeCodeStreamJSON(t *testing.T) {
	bin := fakeCLI(t, `cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess-123"}
{"type":"assistant","message":{"content":[{"type":"text","text":"分析中…"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}
{"type":"result","subtype":"success","result":"任务完成","session_id":"sess-123","total_cost_usd":0.05,"usage":{"input_tokens":100,"output_tokens":50}}
EOF
`)
	a := NewClaudeCode()
	a.Bin = bin

	var events []Event
	res, err := a.Run(context.Background(), RunRequest{
		Prompt:  "do something",
		WorkDir: t.TempDir(),
		OnEvent: func(e Event) { events = append(events, e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "任务完成" {
		t.Fatalf("Text = %q", res.Text)
	}
	if res.SessionID != "sess-123" {
		t.Fatalf("SessionID = %q", res.SessionID)
	}
	if res.Usage.InputTokens != 100 || res.Usage.OutputTokens != 50 {
		t.Fatalf("Usage = %+v", res.Usage)
	}
	if res.Usage.CostUSD != 0.05 {
		t.Fatalf("CostUSD = %v", res.Usage.CostUSD)
	}
	// 事件：text + tool_call + result
	if len(events) != 3 {
		t.Fatalf("事件数 = %d (%+v)", len(events), events)
	}
	if events[0].Type != EventText || events[1].Type != EventToolCall || events[2].Type != EventResult {
		t.Fatalf("事件序不符: %v", events)
	}
}

func TestClaudeCodeExitNonZeroWithResult(t *testing.T) {
	// agent 正常产出 result 但退出码非零（部分失败的常见形态）：以 result 为准
	bin := fakeCLI(t, `cat <<'EOF'
{"type":"result","result":"部分完成，1 项失败","session_id":"s1"}
EOF
exit 1
`)
	a := NewClaudeCode()
	a.Bin = bin

	res, err := a.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "部分完成，1 项失败" {
		t.Fatalf("Text = %q", res.Text)
	}
	if res.ExitCode != 1 {
		t.Fatalf("ExitCode = %d", res.ExitCode)
	}
}

func TestClaudeCodeFailureNoResult(t *testing.T) {
	bin := fakeCLI(t, `echo "fatal: not logged in" >&2; exit 2`)
	a := NewClaudeCode()
	a.Bin = bin

	_, err := a.Run(context.Background(), RunRequest{Prompt: "x"})
	if err == nil {
		t.Fatal("无 result 且非零退出应报错")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("错误应含 stderr 尾巴: %v", err)
	}
}

func TestClaudeCodeArgBuilding(t *testing.T) {
	// 用假 CLI 把参数回显出来，验证参数组装
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"; echo '{"type":"result","result":"ok"}'`)
	a := NewClaudeCode()
	a.Bin = bin

	argsFile := filepath.Join(t.TempDir(), "args")
	workDir := t.TempDir()
	_ = os.Setenv("FAKE_ARGS_FILE", argsFile)
	defer os.Unsetenv("FAKE_ARGS_FILE")

	_, err := a.Run(context.Background(), RunRequest{
		Prompt: "hi", WorkDir: workDir, Model: "sonnet", Env: []string{"FAKE_ARGS_FILE"},
		SessionID: "sess-9", MaxTurns: 5,
		AllowedTools: []string{"Read", "Edit"},
		Sandbox:      SandboxWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsFile)
	args := string(raw)
	for _, want := range []string{
		"-p", "hi", "--model", "sonnet", "--resume", "sess-9",
		"--max-turns", "5", "--allowedTools", "Read Edit",
		"--permission-mode", "acceptEdits",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("参数缺 %q: %s", want, args)
		}
	}
}

func TestClaudeCodeTimeout(t *testing.T) {
	bin := fakeCLI(t, `sleep 30`)
	a := NewClaudeCode()
	a.Bin = bin

	start := time.Now()
	_, err := a.Run(context.Background(), RunRequest{Prompt: "x", Timeout: 200 * time.Millisecond})
	if err == nil {
		t.Fatal("超时应报错")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("超时未被尊重（TERM→KILL 未生效）")
	}
}

func TestCodexLastMessage(t *testing.T) {
	// --output-last-message <file>：假 CLI 解析最后一个参数写文件
	bin := fakeCLI(t, `
last=""
for a in "$@"; do last="$a"; done
prev=""
for a in "$@"; do
  if [ "$prev" = "--output-last-message" ]; then printf %s "codex 最终输出" > "$a"; fi
  prev="$a"
done
echo '{"type":"item.completed","item":{"item_type":"assistant_message","text":"中间消息"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":200,"output_tokens":80}}'
`)
	a := NewCodex()
	a.Bin = bin

	var events []Event
	res, err := a.Run(context.Background(), RunRequest{
		Prompt: "fix bug", OnEvent: func(e Event) { events = append(events, e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "codex 最终输出" {
		t.Fatalf("最终文本应取 output-last-message 文件: %q", res.Text)
	}
	if res.Usage.InputTokens != 200 {
		t.Fatalf("Usage = %+v", res.Usage)
	}
	if len(events) != 1 || events[0].Type != EventText {
		t.Fatalf("事件 = %+v", events)
	}
}

func TestCodexSandboxArgs(t *testing.T) {
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"`)
	a := NewCodex()
	a.Bin = bin

	argsFile := filepath.Join(t.TempDir(), "args")
	workDir := t.TempDir()
	_ = os.Setenv("FAKE_ARGS_FILE", argsFile)
	defer os.Unsetenv("FAKE_ARGS_FILE")

	if _, err := a.Run(context.Background(), RunRequest{
		Prompt: "x", WorkDir: workDir, Model: "o3", Env: []string{"FAKE_ARGS_FILE"},
		Sandbox: SandboxFull,
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsFile)
	args := string(raw)
	for _, want := range []string{"exec", "x", "--json", "-m", "o3", "-C", workDir, "--sandbox", "danger-full-access"} {
		if !strings.Contains(args, want) {
			t.Errorf("参数缺 %q: %s", want, args)
		}
	}
}

func TestOpenCodeJSON(t *testing.T) {
	bin := fakeCLI(t, `echo '[log] loading'; echo '{"text":"opencode 结果","sessionID":"oc-1","tokens":{"input":10,"output":5}}'`)
	a := NewOpenCode()
	a.Bin = bin

	res, err := a.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "opencode 结果" {
		t.Fatalf("Text = %q", res.Text)
	}
	if res.SessionID != "oc-1" {
		t.Fatalf("SessionID = %q", res.SessionID)
	}
	if res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 5 {
		t.Fatalf("Usage = %+v", res.Usage)
	}
}

func TestOpenCodeNonJSONFallback(t *testing.T) {
	// 非 JSON 输出（旧版本）：全文当文本
	bin := fakeCLI(t, `echo "plain text output"`)
	a := NewOpenCode()
	a.Bin = bin

	res, err := a.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "plain text output" {
		t.Fatalf("Text = %q", res.Text)
	}
}

func TestGenericAgentTemplate(t *testing.T) {
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"; echo "done"`)
	g := NewGenericAgent("mini", []string{bin, "run", "{prompt}", "--model", "{model}"}, false)
	_ = bin

	argsFile := filepath.Join(t.TempDir(), "args")
	_ = os.Setenv("FAKE_ARGS_FILE", argsFile)
	defer os.Unsetenv("FAKE_ARGS_FILE")

	res, err := g.Run(context.Background(), RunRequest{Prompt: "任务", Model: "m1", Env: []string{"FAKE_ARGS_FILE"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" {
		t.Fatalf("Text = %q", res.Text)
	}
	raw, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(raw), "任务") || !strings.Contains(string(raw), "m1") {
		t.Fatalf("模板替换失败: %s", raw)
	}
	// {session} 无值时应被省略
	if strings.Contains(string(raw), "{session}") {
		t.Fatalf("未消费的占位符不应残留: %s", raw)
	}
}

func TestZCodeName(t *testing.T) {
	z := NewZCode()
	if z.Name() != "zcode" {
		t.Fatalf("Name = %q", z.Name())
	}
	if z.Bin != "zcode" {
		t.Fatalf("Bin = %q", z.Bin)
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	for _, want := range []string{"claude", "zcode", "codex", "opencode", "minimax"} {
		if _, ok := r.Get(want); !ok {
			t.Errorf("缺省注册缺 %q", want)
		}
	}
	// 未知名
	if _, err := r.Run(context.Background(), "nope", RunRequest{Prompt: "x"}); err == nil {
		t.Fatal("未注册 agent 应报错")
	}
	// 覆盖注册
	r.Register(&stubAgent{name: "claude"})
	a, _ := r.Get("claude")
	if a.Name() != "claude" {
		t.Fatal("覆盖注册失败")
	}
	// AsTool
	if tool := r.AsTool(); tool == nil {
		t.Fatal("AsTool 应返回非 nil")
	}
}

func TestRequestValidation(t *testing.T) {
	// 空 prompt
	err := (&RunRequest{}).validate()
	if err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("空 prompt 应报错: %v", err)
	}
	// 非法 sandbox
	err = (&RunRequest{Prompt: "x", Sandbox: "yolo"}).validate()
	if err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("非法 sandbox 应报错: %v", err)
	}
	// 合法
	if err := (&RunRequest{Prompt: "x", Sandbox: SandboxFull}).validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEnvAllowlist(t *testing.T) {
	_ = os.Setenv("SECRET_TOKEN", "leak-me")
	_ = os.Setenv("PATH", os.Getenv("PATH"))
	defer os.Unsetenv("SECRET_TOKEN")

	env := childEnv(nil)
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "SECRET_TOKEN") {
		t.Fatal("SECRET_TOKEN 不应透传给子进程")
	}
	if !strings.Contains(joined, "PATH=") {
		t.Fatal("PATH 应保留")
	}
}

// stubAgent 测试桩。
type stubAgent struct{ name string }

func (s *stubAgent) Name() string { return s.name }
func (s *stubAgent) Run(_ context.Context, _ RunRequest) (*RunResult, error) {
	return nil, errors.New("stub")
}

func TestLastLineWithoutNewlineFlushed(t *testing.T) {
	// 回归：流末尾无换行符的事件不能丢（mimo error JSON 恰在流尾的真实场景）
	// 末行（result 事件）无尾换行——正是 flush 要救的场景
	bin := fakeCLI(t, `printf '%s\n%s' '{"type":"assistant","message":{"content":[{"type":"text","text":"first"}]}}' '{"type":"result","result":"last-no-newline","session_id":"s1"}'`)
	a := NewClaudeCode()
	a.Bin = bin

	var texts []string
	res, err := a.Run(context.Background(), RunRequest{
		Prompt: "x", OnEvent: func(e Event) { texts = append(texts, e.Text) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 2 {
		t.Fatalf("两条消息都应回调（含无尾换行的末行），实际 %d: %v", len(texts), texts)
	}
	if texts[1] != "last-no-newline" {
		t.Fatalf("末行内容不符: %q", texts[1])
	}
	if res.Text != "last-no-newline" {
		t.Fatalf("res.Text = %q", res.Text)
	}
}

func TestSuccessExitCodeZero(t *testing.T) {
	// 回归：成功路径 ExitCode 应为 0（此前误标 -1）
	bin := fakeCLI(t, `echo '{"role":"assistant","content":"ok"}'`)
	a := NewClaudeCode()
	a.Bin = bin

	res, err := a.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("成功退出码应为 0，实际 %d", res.ExitCode)
	}
}
