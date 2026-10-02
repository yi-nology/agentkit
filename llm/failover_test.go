package llm

import (
	"context"
	"errors"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type failoverStub struct {
	err error
	tag string
}

func (s *failoverStub) Generate(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &schema.Message{Role: schema.Assistant, Content: s.tag}, nil
}

func (s *failoverStub) Stream(_ context.Context, _ []*schema.Message, _ ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	if s.err != nil {
		return nil, s.err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(&schema.Message{Role: schema.Assistant, Content: s.tag}, nil)
	sw.Close()
	return sr, nil
}

// TestFailoverGenerate 主模型失败 → 备模型接管；主模型健康 → 备模型零调用。
func TestFailoverGenerate(t *testing.T) {
	primary := &failoverStub{err: errors.New("429 rate limited"), tag: "primary"}
	fallback := &failoverStub{tag: "fallback"}
	m := NewFailoverModel(primary, fallback, "main", "backup")

	var hooked [][2]string
	m.OnFailover = func(from, to, _ string) { hooked = append(hooked, [2]string{from, to}) }

	resp, err := m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil || resp.Content != "fallback" {
		t.Fatalf("主模型失败应切备模型: %v %q", err, resp.Content)
	}
	if len(hooked) != 1 || hooked[0] != [2]string{"main", "backup"} {
		t.Fatalf("OnFailover 应记录切换: %v", hooked)
	}

	// 主模型恢复：不再切换。
	primary.err = nil
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
	primary := &failoverStub{err: errors.New("conn refused")}
	fallback := &failoverStub{tag: "backup-stream"}
	m := NewFailoverModel(primary, fallback, "main", "backup")

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
	primary := &failoverStub{err: context.Canceled, tag: "primary"}
	fallback := &failoverStub{tag: "fallback"}
	m := NewFailoverModel(primary, fallback, "main", "backup")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Generate(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 取消应原样上抛: %v", err)
	}
}

// TestFailoverBothFail 主备皆失败：备模型错误如实上抛（不吞错）。
func TestFailoverBothFail(t *testing.T) {
	primary := &failoverStub{err: errors.New("boom")}
	fallback := &failoverStub{err: errors.New("backup also down")}
	m := NewFailoverModel(primary, fallback, "main", "backup")

	_, err := m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err == nil || err.Error() != "backup also down" {
		t.Fatalf("备模型失败应如实上抛: %v", err)
	}
}

// withToolsStub 记录 WithTools 派生（验证装饰器对 ADK 绑工具契约的可派生性）。
type withToolsStub struct {
	failoverStub
	toolTag string
}

func (s *withToolsStub) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return &withToolsStub{toolTag: s.tag + "-withtools", failoverStub: failoverStub{err: s.err, tag: s.tag}}, nil
}

// TestFailoverWithTools WithTools 派生后仍保持 failover 语义（ADK 绑工具路径）。
func TestFailoverWithTools(t *testing.T) {
	p := &withToolsStub{failoverStub: failoverStub{err: errors.New("down")}, toolTag: "p"}
	f := &withToolsStub{failoverStub: failoverStub{tag: "fb"}}

	m := NewFailoverModel(p, f, "main", "backup")
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
	m := NewFailoverModel(&failoverStub{}, &failoverStub{}, "main", "backup")
	if _, err := m.WithTools(nil); err == nil {
		t.Fatal("非 ToolCallingChatModel 主/备应报错")
	}
}
