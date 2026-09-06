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
	t.Setenv("FAKE_ARGS_FILE", argsFile)

	if _, err := k.Run(context.Background(), RunRequest{
		Prompt: "hi", Model: "k2", SessionID: "sess-7",
		Env: []string{"FAKE_ARGS_FILE"},
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsFile)
	args := string(raw)
	for _, want := range []string{"-p", "hi", "--output-format", "stream-json", "--model", "k2", "--session", "sess-7"} {
		if !strings.Contains(args, want) {
			t.Errorf("参数缺 %q: %s", want, args)
		}
	}
}

func TestGeminiJSON(t *testing.T) {
	// 官方协议：-p + --output-format json → 单 JSON 对象
	bin := fakeCLI(t, `cat <<'EOF'
{"response":"Gemini 的回答","stats":{"models":{"gemini-2.5-pro":{"tokens":{"prompt":300,"candidates":120}}}}}
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
	t.Setenv("FAKE_ARGS_FILE", argsFile)

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

func TestMimoDedicatedAdapter(t *testing.T) {
	// 专用适配器：--format json 事件流解析（text/step_finish → usage/cost/sessionID）
	bin := fakeCLI(t, `cat <<'EOF'
{"type":"text","part":{"text":"mimo 结果"}}
{"type":"step_finish","sessionID":"ses_x","part":{"tokens":{"input":100,"output":20},"cost":0.01}}
EOF
`)
	m := NewMimo()
	m.Bin = bin

	res, err := m.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "mimo 结果" {
		t.Fatalf("Text = %q", res.Text)
	}
	if res.SessionID != "ses_x" {
		t.Fatalf("SessionID = %q", res.SessionID)
	}
	if res.Usage.InputTokens != 100 || res.Usage.OutputTokens != 20 || res.Usage.CostUSD != 0.01 {
		t.Fatalf("Usage = %+v", res.Usage)
	}
}

func TestMimoArgBuilding(t *testing.T) {
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"`)
	m := NewMimo()
	m.Bin = bin

	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_ARGS_FILE", argsFile)

	if _, err := m.Run(context.Background(), RunRequest{
		Prompt: "任务", Model: "xiaomi/mimo-v2.5-pro", SessionID: "ses_1",
		Env: []string{"FAKE_ARGS_FILE"},
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsFile)
	for _, want := range []string{"run", "任务", "--format", "json", "-m", "xiaomi/mimo-v2.5-pro", "-s", "ses_1"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("参数缺 %q: %s", want, raw)
		}
	}
}

func TestGenericAgentJSONMode(t *testing.T) {
	// IsJSON=true：输出 JSON 对象时解析 text/sessionID/tokens，日志混入容错
	bin := fakeCLI(t, `echo "[log] noise"; echo '{"text":"generic 结果","sessionID":"g-1","tokens":{"input":7,"output":3}}'`)
	g := NewGenericAgent("custom", []string{bin, "{prompt}"}, true)

	res, err := g.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "generic 结果" || res.SessionID != "g-1" {
		t.Fatalf("Text/SessionID = %q/%q", res.Text, res.SessionID)
	}
	if res.Usage.InputTokens != 7 || res.Usage.OutputTokens != 3 {
		t.Fatalf("Usage = %+v", res.Usage)
	}
}

func TestGenericAgentJSONFallbackToText(t *testing.T) {
	// IsJSON=true 但输出非 JSON → 全文兜底
	bin := fakeCLI(t, `echo "not json"`)
	g := NewGenericAgent("custom", []string{bin, "{prompt}"}, true)

	res, err := g.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "not json" {
		t.Fatalf("Text = %q", res.Text)
	}
}
