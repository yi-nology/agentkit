package acpx

import (
	"context"
	"strings"
	"testing"
)

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

func TestGenericAgentFlagPairDrop(t *testing.T) {
	// flag+占位符是条件单元：缺值时连带移除紧邻 flag——
	// 悬空 "--model" 会吞掉后续位置参数（prompt），CLI 收到的 prompt 变成空。
	bin := fakeCLI(t, `echo "args: $@"`)
	g := NewGenericAgent("custom", []string{bin, "run", "--model", "{model}", "--verbose", "{prompt}"}, false)

	res, err := g.Run(context.Background(), RunRequest{Prompt: "任务描述"})
	if err != nil {
		t.Fatal(err)
	}
	// 无 Model：--model 整对消失，prompt 完整到达；--verbose 与 prompt 相邻。
	if !strings.Contains(res.Text, "--verbose 任务描述") || strings.Contains(res.Text, "--model") {
		t.Fatalf("缺值应整对移除 --model，实际: %s", res.Text)
	}
	// 有 Model：flag+值正常展开。
	res2, err := g.Run(context.Background(), RunRequest{Prompt: "任务描述", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res2.Text, "--model m1") {
		t.Fatalf("有值应展开 --model m1，实际: %s", res2.Text)
	}
}
