package wrap

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// seqModel 按脚本逐次返回（耗尽后重复最后一条）。
type seqModel struct {
	mu     sync.Mutex
	resps  []*schema.Message
	errs   []error
	calls  int
	inputs [][]*schema.Message
}

func seqResp(content, finishReason string, toolCalls int) *schema.Message {
	m := &schema.Message{Role: schema.Assistant, Content: content}
	if fr := finishReason; fr != "" {
		m.ResponseMeta = &schema.ResponseMeta{FinishReason: fr}
	}
	for i := 0; i < toolCalls; i++ {
		m.ToolCalls = append(m.ToolCalls, schema.ToolCall{ID: "t1", Function: schema.FunctionCall{Name: "x"}})
	}
	return m
}

func (s *seqModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputs = append(s.inputs, input)
	i := s.calls
	if i >= len(s.resps) {
		i = len(s.resps) - 1
	}
	s.calls++
	if i < 0 {
		return nil, errors.New("seq: 空脚本")
	}
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	return s.resps[i], nil
}

func (s *seqModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("seq: stream 未实现")
}

func (s *seqModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return s, nil }

func (s *seqModel) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestContinueModelMergesTruncated(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{
		seqResp("part-", "length", 0),
		seqResp("rest", "stop", 0),
	}}
	var rounds []int
	cm := NewContinueModel(inner, "R35", func(stage string, round, added int) {
		rounds = append(rounds, round)
	})
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out.Content != "part-rest" {
		t.Fatalf("应顺序拼接，got %q", out.Content)
	}
	if out.ResponseMeta.FinishReason != "stop" {
		t.Fatalf("finish reason 应取最新，got %q", out.ResponseMeta.FinishReason)
	}
	if len(rounds) != 1 || rounds[0] != 1 {
		t.Fatalf("续写轮次不符: %v", rounds)
	}
	if inner.callCount() != 2 {
		t.Fatalf("应恰好 2 次调用，got %d", inner.callCount())
	}
	// 续写请求应携带部分输出 + 续写指令
	second := inner.inputs[1]
	if second[len(second)-1].Role != schema.User || !strings.Contains(second[len(second)-1].Content, "截断处") {
		t.Fatal("续写指令缺失")
	}
	if second[len(second)-2].Content != "part-" {
		t.Fatal("部分输出未随续写请求回传")
	}
}

func TestContinueModelMaxRounds(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{
		seqResp("a", "length", 0),
		seqResp("b", "length", 0),
		seqResp("c", "length", 0),
		seqResp("d", "stop", 0),
	}}
	cm := NewContinueModel(inner, "R3", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out.Content != "abcd" || inner.callCount() != 4 {
		t.Fatalf("三轮续写后拼接 %q（%d 次调用）", out.Content, inner.callCount())
	}
}

func TestContinueModelToolCallsNotContinued(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{
		seqResp("", "length", 2), // 带工具调用的截断：不续写
	}}
	cm := NewContinueModel(inner, "chat", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.ToolCalls) != 2 || inner.callCount() != 1 {
		t.Fatal("带工具调用的截断消息不得续写")
	}
}

func TestContinueModelErrorKeepsPartial(t *testing.T) {
	inner := &seqModel{
		resps: []*schema.Message{
			seqResp("partial", "length", 0),
			seqResp("", "", 0),
		},
		errs: []error{nil, errors.New("续写炸了")},
	}
	cm := NewContinueModel(inner, "qa", nil)
	out, err := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil {
		t.Fatalf("续写失败应返回部分而非报错: %v", err)
	}
	if out.Content != "partial" {
		t.Fatalf("应保留部分输出，got %q", out.Content)
	}
}

func TestContinueModelPassthroughNormal(t *testing.T) {
	inner := &seqModel{resps: []*schema.Message{seqResp("done", "stop", 0)}}
	cm := NewContinueModel(inner, "router", nil)
	out, _ := cm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if out.Content != "done" || inner.callCount() != 1 {
		t.Fatal("正常结束不应触发续写")
	}
}
