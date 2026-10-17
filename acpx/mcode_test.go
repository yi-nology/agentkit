package acpx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 成功信封:output/sessionId/usage.* 三通道解析(2026-10-04 mcode 0.6.2 实弹形态)。
func TestMcodeDedicatedAdapter(t *testing.T) {
	bin := fakeCLI(t, `cat <<'JSON'
{"schemaVersion":1,"type":"exec.result","runId":"exec_turn_x","sessionId":"mvs_01d","turnId":"turn_x","status":"succeeded","output":"OK","model":{"providerId":"minimax","modelId":"MiniMax-M3.1-Flash-Preview"},"usage":{"inputTokens":18012,"outputTokens":2,"cacheReadTokens":4256,"totalTokens":18014},"usageSource":"completed_responses","usageIncomplete":false,"durationMs":6920}
JSON
`)
	m := NewMcode()
	m.Bin = bin

	res, err := m.Run(context.Background(), RunRequest{Prompt: "只回复两个字母: OK"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "OK" {
		t.Fatalf("Text = %q", res.Text)
	}
	if res.SessionID != "mvs_01d" {
		t.Fatalf("SessionID = %q", res.SessionID)
	}
	if res.Usage.InputTokens != 18012 || res.Usage.OutputTokens != 2 {
		t.Fatalf("Usage = %+v", res.Usage)
	}
}

// 失败信封:status=failed 且 exit=0(实弹核实:假模型名 exit 仍 0,失败只能从
// 信封识别)——必须如实上抛,不得当成功返回(2026-09 huginn 实弹教训同纪律)。
func TestMcodeFailedStatusEnvelope(t *testing.T) {
	bin := fakeCLI(t, `cat <<'JSON'
{"schemaVersion":1,"type":"exec.result","sessionId":"mvs_e34","status":"failed","error":{"category":"runtime","message":"Model \"bogus/x\" is not available for the \"configured_provider\" route."},"usageSource":"unavailable","usageIncomplete":true}
JSON
`)
	m := NewMcode()
	m.Bin = bin

	sawError := false
	_, err := m.Run(context.Background(), RunRequest{
		Prompt: "x",
		OnEvent: func(ev Event) {
			if ev.Type == EventError {
				sawError = true
			}
		},
	})
	if err == nil {
		t.Fatal("status=failed 信封应如实报错(exit=0 不构成成功)")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("错误应含信封 message 原文: %v", err)
	}
	if !sawError {
		t.Fatal("失败应经 EventError 全量转发(transcript 排障面)")
	}
}

// argv 构造:exec + --output-format json 固定面;--model/--session/--max-steps
// 条件单元(缺值整体移除,不产生悬空 flag)。
func TestMcodeArgBuilding(t *testing.T) {
	bin := fakeCLI(t, `echo "$@" > "$FAKE_ARGS_FILE"`)
	m := NewMcode()
	m.Bin = bin

	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_ARGS_FILE", argsFile)

	if _, err := m.Run(context.Background(), RunRequest{
		Prompt: "任务", Model: "minimax/MiniMax-M2.1", SessionID: "mvs_1", MaxTurns: 45,
		Env: []string{"FAKE_ARGS_FILE"},
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsFile)
	for _, want := range []string{"exec", "任务", "--output-format", "json", "--model", "minimax/MiniMax-M2.1", "--session", "mvs_1", "--max-steps", "45"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("参数缺 %q: %s", want, raw)
		}
	}

	// 缺值:Model/SessionID/MaxTurns 零值时对应 flag 对整体不出现
	argsFile2 := filepath.Join(t.TempDir(), "args2")
	t.Setenv("FAKE_ARGS_FILE", argsFile2)
	if _, err := m.Run(context.Background(), RunRequest{
		Prompt: "任务", Env: []string{"FAKE_ARGS_FILE"},
	}); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(argsFile2)
	for _, banned := range []string{"--model", "--session", "--max-steps"} {
		if strings.Contains(string(raw2), banned) {
			t.Errorf("零值请求不应出现 %q: %s", banned, raw2)
		}
	}
}

// 非 JSON 输出(版本差异/日志混入):全文兜底,不当成功解析也不崩。
func TestMcodeEnvelopeFallback(t *testing.T) {
	bin := fakeCLI(t, `echo "一段人类可读的最终答复"`)
	m := NewMcode()
	m.Bin = bin

	res, err := m.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "人类可读") {
		t.Fatalf("非信封输出应全文兜底, got %q", res.Text)
	}
}

// 能力面:Model/Session/MaxTurns 在列;Sandbox 不声明——smart 策略在 headless 下
// 工作区外写也放行(2026-10-04 实测建文件成功),无"工作区内放行/外禁止"的
// 运行时硬边界可映射,与 mimo 同款只留 prompt 纪律。
func TestMcodeCapabilities(t *testing.T) {
	caps := NewMcode().Capabilities()
	if !caps.Model || !caps.Session || !caps.MaxTurns {
		t.Fatalf("应声明 model,session,max_turns, got %+v", caps)
	}
	if caps.Sandbox || caps.AllowedTools {
		t.Fatalf("sandbox/allowed_tools 无对应参数面,不应声明, got %+v", caps)
	}
}
