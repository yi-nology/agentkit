package acpx

import (
	"context"
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
