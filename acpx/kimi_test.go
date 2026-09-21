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
