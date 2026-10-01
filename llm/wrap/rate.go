// 限速装饰器：BaseChatModel 直调旁路的速率治理。直调旁路（ReAct/RawModel）
// 绕过 Generator 链（llm.Client/Resilient）的限速是历史缺口；RateModel 挂进
// 包装链后，直调出口每次物理调用先经全局 token bucket 等待——与 Generator
// 链同桶，真全局。
package wrap

import (
	"context"
	"errors"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"golang.org/x/time/rate"
)

// RateModel 限速装饰器；Limiter nil = 直通。
type RateModel struct {
	einomodel.BaseChatModel
	Limiter *rate.Limiter
}

var _ einomodel.ToolCallingChatModel = (*RateModel)(nil)

// NewRateModel 包装 inner；limiter 为 nil 时返回原对象（零开销直通）。
func NewRateModel(inner einomodel.BaseChatModel, limiter *rate.Limiter) einomodel.BaseChatModel {
	if limiter == nil {
		return inner
	}
	return &RateModel{BaseChatModel: inner, Limiter: limiter}
}

func (m *RateModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	if err := m.wait(ctx); err != nil {
		return nil, err
	}
	return m.BaseChatModel.Generate(ctx, input, opts...)
}

func (m *RateModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	if err := m.wait(ctx); err != nil {
		return nil, err
	}
	return m.BaseChatModel.Stream(ctx, input, opts...)
}

func (m *RateModel) wait(ctx context.Context) error {
	if m.Limiter == nil {
		return nil
	}
	return m.Limiter.Wait(ctx)
}

// WithTools 工具绑定透传：绑定产物保持限速（与 Fit/Continue 同纪律）。
func (m *RateModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, ok := m.BaseChatModel.(einomodel.ToolCallingChatModel)
	if !ok {
		return nil, errors.New("ctxrate: 内层模型不支持工具绑定")
	}
	nb, err := bound.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &RateModel{BaseChatModel: nb, Limiter: m.Limiter}, nil
}
