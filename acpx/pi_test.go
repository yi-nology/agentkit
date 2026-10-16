package acpx

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// piJSONL 构造 pi 事件流（assistant message_end 带 text 块与 usage）。
func piJSONL(text string, in, out int) string {
	inS, outS := strconv.Itoa(in), strconv.Itoa(out)
	return `{"type":"session","id":"s1"}
{"type":"message_start","message":{"role":"assistant","model":"mimo-v2.5-pro"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"流式增量"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"思考"},{"type":"text","text":"` + text + `"}],"usage":{"input":` + inS + `,"output":` + outS + `,"cost":{"total":0.01}},"stopReason":"stop"}}
{"type":"turn_end"}
{"type":"agent_settled"}`
}

func TestPiParsesAssistantMessageEnd(t *testing.T) {
	bin := fakeCLI(t, "cat <<'JSONL'\n"+piJSONL("最终答复", 22, 15)+"\nJSONL\n")
	p := NewPi()
	p.Bin = bin
	res, err := p.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "最终答复" {
		t.Fatalf("text = %q（thinking 块不计入正文）", res.Text)
	}
	if res.Usage.InputTokens != 22 || res.Usage.OutputTokens != 15 || res.Usage.CostUSD != 0.01 {
		t.Fatalf("usage: %+v", res.Usage)
	}
}

func TestPiModelFlag(t *testing.T) {
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"
cat <<'JSONL'
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}
JSONL`)
	p := NewPi()
	p.Bin = bin
	argsFile := t.TempDir() + "/args"
	t.Setenv("FAKE_ARGS_FILE", argsFile)
	if _, err := p.Run(context.Background(), RunRequest{Prompt: "x", Model: "google/gemini-2.5-pro",
		Env: []string{"FAKE_ARGS_FILE"}}); err != nil {
		t.Fatal(err)
	}
	raw := readFileTrim(argsFile)
	if !strings.Contains(raw, "--model google/gemini-2.5-pro") || !strings.Contains(raw, "--no-session") || !strings.Contains(raw, "--mode json") {
		t.Fatalf("argv: %s", raw)
	}
	// DefaultModel 回退
	p2 := NewPi()
	p2.Bin = bin
	p2.DefaultModel = "anthropic/claude-sonnet-4"
	if _, err := p2.Run(context.Background(), RunRequest{Prompt: "x", Env: []string{"FAKE_ARGS_FILE"}}); err != nil {
		t.Fatal(err)
	}
	raw2 := readFileTrim(argsFile)
	if !strings.Contains(raw2, "--model anthropic/claude-sonnet-4") {
		t.Fatalf("default model: %s", raw2)
	}
}

func TestPiAbortWithoutAssistant(t *testing.T) {
	bin := fakeCLI(t, `cat <<'JSONL'
{"type":"session"}
{"type":"turn_abort","reason":"provider down"}
JSONL
`)
	p := NewPi()
	p.Bin = bin
	if _, err := p.Run(context.Background(), RunRequest{Prompt: "x"}); err == nil ||
		!strings.Contains(err.Error(), "turn_abort") {
		t.Fatalf("中止应如实报错: %v", err)
	}
}

func TestPiNoEventsFallsBack(t *testing.T) {
	bin := fakeCLI(t, `echo "纯文本输出"`)
	p := NewPi()
	p.Bin = bin
	res, err := p.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "纯文本输出") {
		t.Fatalf("无事件流走全文兜底: %q", res.Text)
	}
}

func TestDshPreset(t *testing.T) {
	bin := fakeCLI(t, `echo "dsh 答复"`)
	d := NewDsh()
	if d.Name() != "dsh" {
		t.Fatal("name")
	}
	d.Argv[0] = bin // 头部可执行替换为本桩（模板机制）
	res, err := d.Run(context.Background(), RunRequest{Prompt: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "dsh 答复") {
		t.Fatalf("text: %q", res.Text)
	}
	// 注册面：NewRegistry 含 11 家
	r := NewRegistry()
	names := strings.Join(r.Names(), ",")
	for _, want := range []string{"pi", "dsh"} {
		if !strings.Contains(names, want) {
			t.Fatalf("注册缺 %s: %s", want, names)
		}
	}
}

// TestPiSessionResume 会话续聊：req.SessionID → --session-id（去掉 --no-session）；
// session 事件的 id 回填 RunResult.SessionID（新会话首跑→调用方拿 id 续聊）。
func TestPiSessionResume(t *testing.T) {
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"
cat <<'JSONL'
{"type":"session","id":"sess-abc"}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"续聊答复"}]}}
JSONL`)
	p := NewPi()
	p.Bin = bin
	argsFile := t.TempDir() + "/args"
	t.Setenv("FAKE_ARGS_FILE", argsFile)

	res, err := p.Run(context.Background(), RunRequest{Prompt: "x", SessionID: "sess-abc",
		Env: []string{"FAKE_ARGS_FILE"}})
	if err != nil {
		t.Fatal(err)
	}
	raw := readFileTrim(argsFile)
	if !strings.Contains(raw, "--session-id sess-abc") || strings.Contains(raw, "--no-session") {
		t.Fatalf("续聊 argv: %s", raw)
	}
	if res.SessionID != "sess-abc" {
		t.Fatalf("session id 回填: %q", res.SessionID)
	}
	if res.Text != "续聊答复" {
		t.Fatalf("text: %q", res.Text)
	}

	// 首跑（无 SessionID）：--no-session 在场
	if _, err := p.Run(context.Background(), RunRequest{Prompt: "x", Env: []string{"FAKE_ARGS_FILE"}}); err != nil {
		t.Fatal(err)
	}
	if raw := readFileTrim(argsFile); !strings.Contains(raw, "--no-session") {
		t.Fatalf("首跑应一次性: %s", raw)
	}
}

// TestPiThinkingOnlyFinalMessageIsEmptySuccess 终态只含 thinking 块（推理模型
// 纯思考收尾/末轮以工具调用结束）：message_end 到达即权威，空文本是合法成功
// ——不得退回全文兜底把 JSONL 事件流原文当正文返回（第六轮审计 C 级回归）。
func TestPiThinkingOnlyFinalMessageIsEmptySuccess(t *testing.T) {
	bin := fakeCLI(t, `cat <<'JSONL'
{"type":"session","id":"s1"}
{"type":"message_start","message":{"role":"assistant"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"纯思考收尾"}]}}
{"type":"agent_settled"}
JSONL
`)
	p := NewPi()
	p.Bin = bin
	res, err := p.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "" {
		t.Fatalf("空文本终态应返回空文本，而非事件流兜底: %q", res.Text)
	}
	if res.SessionID != "s1" {
		t.Fatalf("SessionID = %q（兜底路径保留会话 ID）", res.SessionID)
	}
}
