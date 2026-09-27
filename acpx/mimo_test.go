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
	bin := fakeCLI(t, `cat <<'JSONL'
{"type":"text","part":{"text":"mimo 结果"}}
{"type":"step_finish","sessionID":"ses_x","part":{"tokens":{"input":100,"output":20},"cost":0.01}}
JSONL
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

func TestMimoErrorDataMessagePath(t *testing.T) {
	// 实弹回归（2026-09 huginn）：mimo 错误详情在 error.data.message——
	// 此前只读顶层 error.message 导致错误被静默吞掉。
	bin := fakeCLI(t, `cat <<'JSONL'
{"type":"error","error":{"data":{"message":"model ultraspeed has been decommissioned"},"message":"fallback-msg"}}
{"type":"step_finish","sessionID":"ses_err","part":{"tokens":{"input":1,"output":0},"cost":0}}
JSONL
`)
	m := NewMimo()
	m.Bin = bin

	var events []Event
	_, err := m.Run(context.Background(), RunRequest{Prompt: "x", OnEvent: func(e Event) { events = append(events, e) }})
	if err == nil || !strings.Contains(err.Error(), "ultraspeed has been decommissioned") {
		t.Fatalf("error.data.message 应被提取并如实报错: %v", err)
	}
	// 纯错误跑不得全文兜底成"成功"（此前 stdout 错误事件原文被当 Text 返回）。
	// error 与 step_finish 事件须全量转发（transcript 可排障——此前只转 text，
	// 纯错误跑 transcript 0 字节）。
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	if len(types) != 2 || types[0] != EventError || types[1] != EventResult {
		t.Fatalf("事件转发应含 error+result: %v", types)
	}
}

func TestMimoErrorOverridesPartialText(t *testing.T) {
	// 有错如实报错：不因恰好收到部分文本而把失败跑当成功。
	bin := fakeCLI(t, `cat <<'JSONL'
{"type":"text","part":{"text":"部分输出"}}
{"type":"error","error":{"data":{"message":"rate limited"}}}
JSONL
`)
	m := NewMimo()
	m.Bin = bin
	if _, err := m.Run(context.Background(), RunRequest{Prompt: "x"}); err == nil ||
		!strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error 应优先于部分文本如实报错: %v", err)
	}
}

func TestMimoDefaultModel(t *testing.T) {
	// 缺省模型配置化：req.Model 优先，空时回退 DefaultModel（-m 须 xiaomi/ 全名）。
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"`)
	m := NewMimo()
	m.Bin = bin
	m.DefaultModel = "xiaomi/mimo-v2.5-pro"

	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_ARGS_FILE", argsFile)

	if _, err := m.Run(context.Background(), RunRequest{Prompt: "x", Env: []string{"FAKE_ARGS_FILE"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(raw), "-m xiaomi/mimo-v2.5-pro") {
		t.Fatalf("未指定 Model 应用 DefaultModel: %s", raw)
	}

	// req.Model 显式指定时优先。
	if _, err := m.Run(context.Background(), RunRequest{Prompt: "x", Model: "xiaomi/other",
		Env: []string{"FAKE_ARGS_FILE"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(argsFile)
	if strings.Contains(string(raw), "mimo-v2.5-pro") || !strings.Contains(string(raw), "xiaomi/other") {
		t.Fatalf("req.Model 应优先于 DefaultModel: %s", raw)
	}
}
