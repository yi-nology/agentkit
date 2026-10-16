package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/yi-nology/agentkit/llm/llmtest"
)

// 桩统一走 llmtest（Model/ToolModel）——此前本文件手写 failoverStub/withToolsStub。

// TestFailoverGenerate 主模型失败 → 备模型接管；主模型健康 → 备模型零调用。
func TestFailoverGenerate(t *testing.T) {
	// 脚本序：第一次 429 失败 → 切备；第二次"恢复"（末条重复语义）→ 主模型直出。
	primary := &llmtest.Model{RepeatLast: true, StreamContent: "primary",
		Script: []llmtest.Resp{{Err: errors.New("429 rate limited")}, {Content: "primary"}}}
	fallback := &llmtest.Model{RepeatLast: true, StreamContent: "fallback", Script: []llmtest.Resp{{Content: "fallback"}}}
	m := NewFailoverModel(ChainLink{Model: primary, Name: "main"}, ChainLink{Model: fallback, Name: "backup"})

	var hooked [][2]string
	m.OnFailover = func(from, to, _ string) { hooked = append(hooked, [2]string{from, to}) }

	resp, err := m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil || resp.Content != "fallback" {
		t.Fatalf("主模型失败应切备模型: %v %q", err, resp.Content)
	}
	if len(hooked) != 1 || hooked[0] != [2]string{"main", "backup"} {
		t.Fatalf("OnFailover 应记录切换: %v", hooked)
	}

	// 主模型恢复（脚本第二条）：不再切换。
	hooked = nil
	resp, err = m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil || resp.Content != "primary" {
		t.Fatalf("主模型健康应直接返回: %v %q", err, resp.Content)
	}
	if len(hooked) != 0 {
		t.Fatalf("健康路径不应触发切换: %v", hooked)
	}
}

// TestFailoverStream 首块前失败同样切备模型。
func TestFailoverStream(t *testing.T) {
	primary := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Err: errors.New("conn refused")}}}
	fallback := &llmtest.Model{RepeatLast: true, StreamContent: "backup-stream", Script: []llmtest.Resp{{Content: "backup-stream"}}}
	m := NewFailoverModel(ChainLink{Model: primary, Name: "main"}, ChainLink{Model: fallback, Name: "backup"})

	sr, err := m.Stream(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("Stream 首块前失败应切备: %v", err)
	}
	msg, err := sr.Recv()
	if err != nil || msg.Content != "backup-stream" {
		t.Fatalf("备模型流内容不符: %v %q", err, msg.Content)
	}
}

// TestFailoverCtxCancelled 调用方取消/超时是调用语义：不切换，原样上抛。
func TestFailoverCtxCancelled(t *testing.T) {
	primary := &llmtest.Model{RepeatLast: true, StreamContent: "primary", Script: []llmtest.Resp{{Err: context.Canceled, Content: "primary"}}}
	fallback := &llmtest.Model{RepeatLast: true, StreamContent: "fallback", Script: []llmtest.Resp{{Content: "fallback"}}}
	m := NewFailoverModel(ChainLink{Model: primary, Name: "main"}, ChainLink{Model: fallback, Name: "backup"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Generate(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 取消应原样上抛: %v", err)
	}
}

// TestFailoverBothFail 主备皆失败：备模型错误如实上抛（不吞错）。
func TestFailoverBothFail(t *testing.T) {
	primary := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Err: errors.New("boom")}}}
	fallback := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Err: errors.New("backup also down")}}}
	m := NewFailoverModel(ChainLink{Model: primary, Name: "main"}, ChainLink{Model: fallback, Name: "backup"})

	_, err := m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err == nil || err.Error() != "backup also down" {
		t.Fatalf("备模型失败应如实上抛: %v", err)
	}
}

// TestFailoverWithTools WithTools 派生后仍保持 failover 语义（ADK 绑工具路径）。
func TestFailoverWithTools(t *testing.T) {
	p := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Err: errors.New("down")}}}}
	f := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: "fb"}}}}

	m := NewFailoverModel(ChainLink{Model: p, Name: "main"}, ChainLink{Model: f, Name: "backup"})
	w, err := m.WithTools(nil)
	if err != nil {
		t.Fatalf("WithTools 不应报错: %v", err)
	}
	resp, err := w.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil || resp.Content != "fb" {
		t.Fatalf("派生实例应保留 failover 语义: %v %q", err, resp.Content)
	}
}

// TestFailoverWithToolsUnsupported 主/备不支持工具绑定时如实报错。
func TestFailoverWithToolsUnsupported(t *testing.T) {
	m := NewFailoverModel(ChainLink{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{}}}, Name: "main"}, ChainLink{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{}}}, Name: "backup"})
	if _, err := m.WithTools(nil); err == nil {
		t.Fatal("非 ToolCallingChatModel 主/备应报错")
	}
}
