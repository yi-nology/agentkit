package acpx

import (
	"context"
	"strings"
	"testing"
)

// TestCodexErrorOverridesPartialText 回归（v0.10.28 审计）：error 事件与部分
// assistant 文本并存时必须报错——lastText 条件曾让这类跑静默丢错（部分输出后
// 配额耗尽/流中断的常见实弹形态，mimo 纪律同款）。
func TestCodexErrorOverridesPartialText(t *testing.T) {
	bin := fakeCLI(t, `cat <<'JSONL'
{"type":"item.completed","item":{"item_type":"assistant_message","text":"部分输出"}}
{"type":"error","message":"stream interrupted"}
JSONL
`)
	c := NewCodex()
	c.Bin = bin
	_, err := c.Run(context.Background(), RunRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "stream interrupted") {
		t.Fatalf("error 应优先于部分文本如实报错: %v", err)
	}
}

// TestCodexHappyPath 基线：正常流（文本 + usage）不受错误收紧影响。
func TestCodexHappyPath(t *testing.T) {
	bin := fakeCLI(t, `cat <<'JSONL'
{"type":"item.completed","item":{"item_type":"assistant_message","text":"全部输出"}}
{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}
JSONL
`)
	c := NewCodex()
	c.Bin = bin
	res, err := c.Run(context.Background(), RunRequest{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text == "" || res.Usage.InputTokens != 10 {
		t.Fatalf("正常流结果: %+v", res)
	}
}
