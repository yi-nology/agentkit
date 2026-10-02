package acpx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestGeminiErrorEnvelope(t *testing.T) {
	// 实弹回归（与 mimo 同病）：error 信封此前解析但未检查——错误被吞成空响应。
	bin := fakeCLI(t, `echo '{"error":{"message":"API key not valid"},"response":""}'`)
	g := NewGemini()
	g.Bin = bin

	var events []Event
	_, err := g.Run(context.Background(), RunRequest{Prompt: "x", OnEvent: func(e Event) { events = append(events, e) }})
	if err == nil || !strings.Contains(err.Error(), "API key not valid") {
		t.Fatalf("error 信封应如实报错: %v", err)
	}
	if len(events) != 1 || events[0].Type != EventError {
		t.Fatalf("应转发 EventError: %v", events)
	}
}
