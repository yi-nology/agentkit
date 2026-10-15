package acpx

import (
	"context"
	"strings"
	"testing"
)

// TestClaudeInitOnlyWithoutResultFallsBack 回归（v0.10.28 审计）：init 事件到达
// 而 result 事件丢失（超长行被限容丢弃/流尾截断）时，不得返回空文本成功——
// init-only 空壳 result 不具备终态语义，应走全文兜底。
func TestClaudeInitOnlyWithoutResultFallsBack(t *testing.T) {
	bin := fakeCLI(t, `cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess-1"}
{"type":"assistant","message":{"content":[{"type":"text","text":"流式文本"}]}}
EOF
echo "非事件尾行文本"
`)
	a := NewClaudeCode()
	a.Bin = bin
	res, err := a.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text == "" {
		t.Fatal("result 事件丢失应走全文兜底，而非空文本成功")
	}
	if !strings.Contains(res.Text, "流式文本") && !strings.Contains(res.Text, "非事件尾行文本") {
		t.Fatalf("兜底应取可用文本: %q", res.Text)
	}
}

// TestClaudeEmptyResultIsLegitimateSuccess 反向守卫：result 事件合法到达但文本
// 为空（纯工具调用跑）仍是空成功——sawResult 区分这两种形态。
func TestClaudeEmptyResultIsLegitimateSuccess(t *testing.T) {
	bin := fakeCLI(t, `cat <<'EOF'
{"type":"system","subtype":"init","session_id":"sess-1"}
{"type":"result","subtype":"success","result":"","session_id":"sess-1"}
EOF
`)
	a := NewClaudeCode()
	a.Bin = bin
	res, err := a.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.SessionID != "sess-1" {
		t.Fatalf("result 事件到达即终态: %+v", res)
	}
}
