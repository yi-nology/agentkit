package acpx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKimiStreamJSON(t *testing.T) {
	// 官方协议：--print -p + --output-format=stream-json → JSONL 消息流
	bin := fakeCLI(t, `cat <<'EOF'
{"role":"assistant","content":"先看一下目录结构"}
{"role":"tool","content":"$ ls"}
{"role":"assistant","content":"共有 3 个 Python 文件"}
EOF
`)
	k := NewKimi()
	k.Bin = bin

	var events []Event
	res, err := k.Run(context.Background(), RunRequest{
		Prompt:  "列出 Python 文件",
		OnEvent: func(e Event) { events = append(events, e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	// 最终文本 = 最后一条 assistant 消息（跳过中间 tool 消息）
	if res.Text != "共有 3 个 Python 文件" {
		t.Fatalf("Text = %q", res.Text)
	}
	// 事件只有 assistant 消息（tool 消息不回调）
	if len(events) != 2 {
		t.Fatalf("事件数 = %d", len(events))
	}
}

func TestKimiNonJSONFallback(t *testing.T) {
	bin := fakeCLI(t, `echo "纯文本输出"`)
	k := NewKimi()
	k.Bin = bin

	res, err := k.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "纯文本输出" {
		t.Fatalf("Text = %q", res.Text)
	}
}

func TestKimiArgBuilding(t *testing.T) {
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"; echo '{"role":"assistant","content":"ok"}'`)
	k := NewKimi()
	k.Bin = bin

	argsFile := filepath.Join(t.TempDir(), "args")
	_ = os.Setenv("FAKE_ARGS_FILE", argsFile)
	defer os.Unsetenv("FAKE_ARGS_FILE")

	if _, err := k.Run(context.Background(), RunRequest{
		Prompt: "hi", Model: "k2", SessionID: "sess-7",
		Env: []string{"FAKE_ARGS_FILE"},
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsFile)
	args := string(raw)
	for _, want := range []string{"--print", "-p", "hi", "--model", "k2", "--session", "sess-7"} {
		if !strings.Contains(args, want) {
			t.Errorf("参数缺 %q: %s", want, args)
		}
	}
}

func TestGeminiJSON(t *testing.T) {
	// 官方协议：-p + --output-format json → 单 JSON 对象
	bin := fakeCLI(t, `cat <<'EOF'
{"response":"Gemini 的回答","stats":{"models":{"gemini-2.5-pro":{"tokens":{"prompt":300,"candidates":120}}},"models":{"gemini-2.5-pro":{"tokens":{"prompt":300,"candidates":120}}}}}
EOF
`)
	g := NewGemini()
	g.Bin = bin

	res, err := g.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Gemini 的回答" {
		t.Fatalf("Text = %q", res.Text)
	}
	if res.Usage.InputTokens != 300 || res.Usage.OutputTokens != 120 {
		t.Fatalf("Usage = %+v", res.Usage)
	}
}

func TestGeminiArgBuilding(t *testing.T) {
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"; echo '{"response":"ok"}'`)
	g := NewGemini()
	g.Bin = bin

	argsFile := filepath.Join(t.TempDir(), "args")
	_ = os.Setenv("FAKE_ARGS_FILE", argsFile)
	defer os.Unsetenv("FAKE_ARGS_FILE")

	if _, err := g.Run(context.Background(), RunRequest{
		Prompt: "x", Model: "gemini-3", Sandbox: SandboxFull,
		Env: []string{"FAKE_ARGS_FILE"},
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsFile)
	args := string(raw)
	for _, want := range []string{"-p", "x", "--output-format", "json", "-m", "gemini-3", "--approval-mode", "yolo"} {
		if !strings.Contains(args, want) {
			t.Errorf("参数缺 %q: %s", want, args)
		}
	}
}

func TestGeminiNonJSONFallback(t *testing.T) {
	bin := fakeCLI(t, `echo "plain"`)
	g := NewGemini()
	g.Bin = bin

	res, err := g.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "plain" {
		t.Fatalf("Text = %q", res.Text)
	}
}

func TestNewAgentNames(t *testing.T) {
	cases := []struct {
		a    Agent
		want string
	}{
		{NewKimi(), "kimi"},
		{NewQwen(), "qwen"},
		{NewGemini(), "gemini"},
		{NewMimo(), "mimo"},
		{NewMinimax(), "minimax"},
	}
	for _, c := range cases {
		if c.a.Name() != c.want {
			t.Errorf("Name = %q, want %q", c.a.Name(), c.want)
		}
	}
}

func TestMimoIsGenericAgent(t *testing.T) {
	// MiMo 走通用模板（CLI 约定未稳定）：验证模板执行
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"; echo "mimo done"`)
	g := NewMimo()
	// 替换二进制为假 CLI（保留模板其余部分）
	g.Argv = append([]string{bin}, g.Argv[1:]...)

	argsFile := filepath.Join(t.TempDir(), "args")
	_ = os.Setenv("FAKE_ARGS_FILE", argsFile)
	defer os.Unsetenv("FAKE_ARGS_FILE")

	res, err := g.Run(context.Background(), RunRequest{
		Prompt: "任务", Env: []string{"FAKE_ARGS_FILE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "mimo done" {
		t.Fatalf("Text = %q", res.Text)
	}
	raw, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(raw), "run") || !strings.Contains(string(raw), "任务") {
		t.Fatalf("模板不符: %s", raw)
	}
}
