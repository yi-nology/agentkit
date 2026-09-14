package acpx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
