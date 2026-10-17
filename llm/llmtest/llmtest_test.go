package llmtest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestModelScriptSemantics(t *testing.T) {
	// 耗尽报错（默认）：脚本写漏立即可见。
	m := &Model{Script: []Resp{{Content: "only"}}}
	if _, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")}); err == nil ||
		!strings.Contains(err.Error(), "脚本耗尽") {
		t.Fatalf("耗尽应报错: %v", err)
	}

	// RepeatLast：末条重复（重试/降级链测试依赖持续错误）。
	m2 := &Model{RepeatLast: true, Script: []Resp{{Err: errors.New("down")}}}
	for i := 0; i < 3; i++ {
		if _, err := m2.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")}); err == nil {
			t.Fatal("RepeatLast 应持续返回末条错误")
		}
	}

	// Inputs 历史 + ResponseMeta 填充。
	m3 := &Model{Script: []Resp{{Content: "x", FinishReason: "length", Prompt: 10, Completion: 5}}, RepeatLast: true}
	msgs := []*schema.Message{
		{Role: schema.System, Content: "sys"},
		{Role: schema.User, Content: "hello"},
	}
	out, err := m3.Generate(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if out.ResponseMeta == nil || out.ResponseMeta.FinishReason != "length" ||
		out.ResponseMeta.Usage.PromptTokens != 10 || out.ResponseMeta.Usage.TotalTokens != 15 {
		t.Fatalf("ResponseMeta/Usage 填充不符: %+v", out.ResponseMeta)
	}
	if m3.FirstInput != "sys" || m3.LastInput != "hello" || len(m3.Inputs) != 1 || m3.Inputs[0] != "hello" {
		t.Fatalf("输入记录不符: %q %q %v", m3.FirstInput, m3.LastInput, m3.Inputs)
	}
}

func TestModelStream(t *testing.T) {
	m := &Model{}
	if _, err := m.Stream(context.Background(), nil); err == nil {
		t.Fatal("未配置 StreamContent 应报错")
	}
	m.StreamContent = "chunk"
	sr, err := m.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := sr.Recv()
	if err != nil || msg.Content != "chunk" {
		t.Fatalf("Stream 内容不符: %v %q", err, msg.Content)
	}
}

func TestToolModelWithTools(t *testing.T) {
	// WithTools 派生共享父桩状态（脚本继续按序消费）。
	base := &Model{RepeatLast: true, Script: []Resp{{Content: "ok"}}}
	tm := &ToolModel{Model: base}
	derived, err := tm.WithTools(nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := derived.Generate(context.Background(), []*schema.Message{schema.UserMessage("q")})
	if err != nil || out.Content != "ok" {
		t.Fatalf("派生实例应共享脚本: %v %q", err, out.Content)
	}
}

func TestProviderDefaults(t *testing.T) {
	p := NewScriptedProvider("p1", Resp{Content: "hi"})
	if p.Name() != "p1" || p.ModelName() != "p1-model" {
		t.Fatalf("Name/ModelName = %q/%q", p.Name(), p.ModelName())
	}
	if p.ContextTokens() != 128_000 || p.MaxOutputTokens() != 4096 {
		t.Fatal("窗口缺省不符")
	}
	pc, cc := p.CostPer1KTokens()
	if pc != 0.001 || cc != 0.002 {
		t.Fatal("成本缺省不符")
	}
}
