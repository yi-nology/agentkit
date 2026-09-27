// FailoverModel 模型链 failover 装饰器（eino BaseChatModel/ToolCallingChatModel
// 双形态）。
//
// Resilient/FallbackChain 只覆盖 agentkit 自己的 Generator 客户端路径；ReAct 主路径
// （ADK ChatModelAgent 直调模型）拿到的就是裸 BaseChatModel，没有降级链。本装饰器
// 填补这一层：按序尝试模型，前一个失败且调用方 ctx 仍存活时切下一个重放同一次请求。
//
// 刻意保持薄：不做熔断/限速/预算/同模型重试（那是 Resilient 的职责，二者可叠加——
// Resilient.RawModelWithFailover() 即本装饰器包住整条 Provider 链）。切换决策对任何
// 错误恒真：429/5xx/网络类错误切换必然正确；401/403/上下文超限类确定性错误在模型
// 同端点时切了也白切，但异端点/异凭证时能救——误切代价仅一次模型调用。
// 调用方语义的失败（ctx 取消/超时）不切换，原样上抛。
//
// 从 bianque 生产装配提炼（v0.9.4）；v0.10.9 由主备二元泛化为 N 模型链。
package llm

import (
	"context"
	"fmt"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// FailoverModel 模型链装饰器（构建后只读；OnFailover 构建期设置）。
type FailoverModel struct {
	models []einomodel.BaseChatModel
	names  []string
	// OnFailover 切换观测回调（from/to 模型名 + reason=失败模型错误；nil=静默）。
	OnFailover func(from, to, reason string)
}

// 编译期断言：ADK 绑工具经 ToolCallingChatModel.WithTools，装饰器必须可派生。
var _ einomodel.ToolCallingChatModel = (*FailoverModel)(nil)

// ChainLink failover 链单链节：模型 + 观测名（结构化链节消平行切片错位——
// names[i] 对不上 models[i] 是装配期静默事故）。
type ChainLink struct {
	Model einomodel.BaseChatModel
	Name  string
}

// NewFailoverModel 模型链按序 failover 装饰（至少一节；主备二元传两节即可）。
// nil 模型会在被尝试到时报"failover 链含 nil 模型"错误并继续下一个。
func NewFailoverModel(links ...ChainLink) *FailoverModel {
	if len(links) == 0 {
		return &FailoverModel{names: []string{"invalid"}}
	}
	m := &FailoverModel{models: make([]einomodel.BaseChatModel, len(links)),
		names: make([]string, len(links))}
	for i, l := range links {
		m.models[i], m.names[i] = l.Model, l.Name
	}
	return m
}

// WithTools 工具绑定转发：链上各模型分别派生带工具的实例后重新包装（不可变派生，
// 并发安全语义与 eino ToolCallingChatModel 契约一致）。
func (m *FailoverModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound := make([]einomodel.BaseChatModel, len(m.models))
	for i, mm := range m.models {
		tc, ok := mm.(einomodel.ToolCallingChatModel)
		if !ok {
			return nil, fmt.Errorf("llm: failover 模型 %s 不支持工具绑定（ToolCallingChatModel）", m.names[i])
		}
		tw, err := tc.WithTools(tools)
		if err != nil {
			return nil, fmt.Errorf("llm: failover 模型 %s 绑定工具失败: %w", m.names[i], err)
		}
		bound[i] = tw
	}
	return &FailoverModel{models: bound, names: m.names, OnFailover: m.OnFailover}, nil
}

// Generate 按链序尝试；失败且 ctx 存活（非调用方取消/超时）时切下一个。
// 全部失败上抛末个模型的错误——此前各模型的失败经 OnFailover 回调可见。
func (m *FailoverModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	for i, mm := range m.models {
		resp, err := generateModel(ctx, mm, input, opts...)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil || i == len(m.models)-1 {
			return nil, err
		}
		m.notify(m.names[i], m.names[i+1], err.Error())
	}
	return nil, fmt.Errorf("llm: failover 链为空")
}

// Stream 与 Generate 同一降级语义（首块前失败才可切换；已在流中失败无法换模型重放）。
func (m *FailoverModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	for i, mm := range m.models {
		sr, err := mm.Stream(ctx, input, opts...)
		if err == nil {
			return sr, nil
		}
		if ctx.Err() != nil || i == len(m.models)-1 {
			return nil, err
		}
		m.notify(m.names[i], m.names[i+1], err.Error())
	}
	return nil, fmt.Errorf("llm: failover 链为空")
}

// generateModel nil 模型防御（装配遗漏 fail-fast 于尝试时刻而非 panic）。
func generateModel(ctx context.Context, m einomodel.BaseChatModel, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	if m == nil {
		return nil, fmt.Errorf("llm: failover 链含 nil 模型")
	}
	return m.Generate(ctx, input, opts...)
}

func (m *FailoverModel) notify(from, to, reason string) {
	if m.OnFailover != nil {
		m.OnFailover(from, to, reason)
	}
}
