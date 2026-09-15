// FailoverModel 主备 failover 装饰器（eino BaseChatModel/ToolCallingChatModel 双形态）。
//
// Resilient/FallbackChain 只覆盖 agentkit 自己的 Generator 客户端路径；ReAct 主路径
// （ADK ChatModelAgent 直调模型）拿到的就是裸 BaseChatModel，没有降级链。本装饰器
// 填补这一层：主模型请求失败且调用方 ctx 仍存活时，切备模型重放同一次请求。
//
// 刻意保持薄：不做熔断/限速/预算（那是 Resilient 的职责，二者可叠加——
// Resilient.RawModel() 外面再包一层 FailoverModel）。切换决策对任何错误恒真：
// 429/5xx/网络类错误切换必然正确；401/403/上下文超限类确定性错误在主备同端点时
// 切了也白切，但主备异端点/异凭证时能救——误切代价仅一次备模型调用。
// 调用方语义的失败（ctx 取消/超时）不切换，原样上抛。
//
// 从 bianque 生产装配提炼（v0.9.4）。
package llm

import (
	"context"
	"fmt"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// FailoverModel 主备模型装饰器（构建后只读；OnFailover 构建期设置）。
type FailoverModel struct {
	primary      einomodel.BaseChatModel
	fallback     einomodel.BaseChatModel
	primaryName  string
	fallbackName string
	// OnFailover 切换观测回调（from/to 模型名 + reason=主模型错误；nil=静默）。
	OnFailover func(from, to, reason string)
}

// 编译期断言：ADK 绑工具经 ToolCallingChatModel.WithTools，装饰器必须可派生。
var _ einomodel.ToolCallingChatModel = (*FailoverModel)(nil)

// NewFailoverModel 主备模型 failover 装饰。
func NewFailoverModel(primary, fallback einomodel.BaseChatModel, primaryName, fallbackName string) *FailoverModel {
	return &FailoverModel{primary: primary, fallback: fallback, primaryName: primaryName, fallbackName: fallbackName}
}

// WithTools 工具绑定转发：主备分别派生带工具的实例后重新包装（不可变派生，并发安全
// 语义与 eino ToolCallingChatModel 契约一致）。
func (m *FailoverModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	p, ok1 := m.primary.(einomodel.ToolCallingChatModel)
	f, ok2 := m.fallback.(einomodel.ToolCallingChatModel)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("llm: failover 主/备模型不支持工具绑定（ToolCallingChatModel）")
	}
	pw, err := p.WithTools(tools)
	if err != nil {
		return nil, fmt.Errorf("llm: failover 主模型绑定工具失败: %w", err)
	}
	fw, err := f.WithTools(tools)
	if err != nil {
		return nil, fmt.Errorf("llm: failover 备模型绑定工具失败: %w", err)
	}
	return &FailoverModel{primary: pw, fallback: fw, primaryName: m.primaryName, fallbackName: m.fallbackName, OnFailover: m.OnFailover}, nil
}

// Generate 主模型优先；失败且 ctx 存活（非调用方取消/超时）时切备模型。
func (m *FailoverModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	resp, err := m.primary.Generate(ctx, input, opts...)
	if err == nil {
		return resp, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	m.notify(err.Error())
	return m.fallback.Generate(ctx, input, opts...)
}

// Stream 与 Generate 同一降级语义（首块前失败才可切换；已在流中失败无法换模型重放）。
func (m *FailoverModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := m.primary.Stream(ctx, input, opts...)
	if err == nil {
		return sr, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	m.notify(err.Error())
	return m.fallback.Stream(ctx, input, opts...)
}

func (m *FailoverModel) notify(reason string) {
	if m.OnFailover != nil {
		m.OnFailover(m.primaryName, m.fallbackName, reason)
	}
}
