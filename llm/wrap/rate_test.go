package wrap

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/time/rate"
)

func TestRateModelNilPassthrough(t *testing.T) {
	inner := &fakeChatModel{}
	if got := NewRateModel(inner, nil); got != model.BaseChatModel(inner) {
		t.Fatal("nil limiter 应返回原对象（零开销直通）")
	}
}

func TestRateModelGenerateForwards(t *testing.T) {
	inner := &fakeChatModel{}
	rm := NewRateModel(inner, rate.NewLimiter(rate.Inf, 1)).(*RateModel)
	out, err := rm.Generate(context.Background(), []*schema.Message{bigTool(schema.User, 10)})
	if err != nil || out == nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestRateModelRespectsCancel(t *testing.T) {
	inner := &fakeChatModel{}
	// 0 配额限速器 + 已取消 ctx：Wait 立即返回错误，请求不出网
	rm := NewRateModel(inner, rate.NewLimiter(rate.Limit(0.0001), 0)).(*RateModel)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := rm.Generate(ctx, []*schema.Message{bigTool(schema.User, 10)}); err == nil {
		t.Fatal("取消的 ctx 应立即返回错误")
	}
	inner.mu.Lock()
	called := inner.lastIn != nil
	inner.mu.Unlock()
	if called {
		t.Fatal("限速等待失败不应触达内层模型")
	}
}

func TestRateModelWithToolsKeepsLimiter(t *testing.T) {
	inner := &fakeChatModel{}
	lim := rate.NewLimiter(rate.Inf, 1)
	rm := NewRateModel(inner, lim).(*RateModel)
	bound, err := rm.WithTools([]*schema.ToolInfo{})
	if err != nil {
		t.Fatalf("WithTools: %v", err)
	}
	bm, ok := bound.(*RateModel)
	if !ok || bm.Limiter != lim {
		t.Fatal("绑定产物应保持限速器")
	}
}
