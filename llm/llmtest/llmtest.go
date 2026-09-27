// Package llmtest eino ChatModel/Provider 的脚本化测试桩——llm/reflection/router
// 等包的测试共用（此前四份手写桩已漂移出两种耗尽语义与多种记录口径）。
// 只进测试二进制，不进任何生产依赖图。
package llmtest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Resp 单次调用的脚本条目（Generate 与 Stream 共按序消费）。
type Resp struct {
	Content      string // 响应文本（Err 非空时忽略）
	Err          error  // 非空时本次调用返回该错误
	FinishReason string // 非空时填入 ResponseMeta（截断重试等场景断言用）
	// Prompt/Completion 非零时填入 ResponseMeta.Usage（真实 usage 记账断言用）。
	Prompt     int
	Completion int
	// ToolCalls 附带的工具调用（tool-calling 协议桩：adk planexecute 的
	// plan/respond 工具调用形态）。
	ToolCalls []schema.ToolCall
}

// ToolCall 便捷构造单工具调用条目（name/arguments JSON）。
func ToolCall(name, arguments string) []schema.ToolCall {
	return []schema.ToolCall{{
		ID:       "call_" + name,
		Type:     "function",
		Function: schema.FunctionCall{Name: name, Arguments: arguments},
	}}
}

// Model 脚本化 BaseChatModel 测试桩（并发安全）。
//
//   - Script 按调用序消费；RepeatLast=true 时耗尽后重复末条（重试/降级链测试：
//     持续错误才能触发切换），false 时耗尽报错（防桩被多调——脚本写漏立即可见）；
//   - FirstInput/LastInput 记录每次 Generate 的首/末消息文本（提示词断言用）；
//   - StreamContent 非空时 Stream 返回该内容的单块流，空则报错（多数测试桩
//     不需要流式路径）；
//   - Calls 为 Generate+Stream 总调用数。
type Model struct {
	mu sync.Mutex

	Script     []Resp
	RepeatLast bool
	// StreamContent Stream 路径返回的单块内容（空 = "桩不支持流式"）。
	StreamContent string

	// 断言面（测试读取）。
	FirstInput string   // 最近一次 Generate 的首条消息文本
	LastInput  string   // 最近一次 Generate 的末条消息文本
	Inputs     []string // 每次 Generate 的末条消息文本（调用序历史）
	Calls      int
}

// Generate 按 Script 依次响应。
func (m *Model) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(in) > 0 {
		m.FirstInput = in[0].Content
		m.LastInput = in[len(in)-1].Content
		m.Inputs = append(m.Inputs, m.LastInput)
	}
	resp, err := m.next()
	if err != nil {
		return nil, err
	}
	m.Calls++
	if resp.Err != nil {
		return nil, resp.Err
	}
	out := &schema.Message{Role: schema.Assistant, Content: resp.Content, ToolCalls: resp.ToolCalls}
	if resp.FinishReason != "" || resp.Prompt > 0 {
		out.ResponseMeta = &schema.ResponseMeta{}
		if resp.FinishReason != "" {
			out.ResponseMeta.FinishReason = resp.FinishReason
		}
		if resp.Prompt > 0 {
			out.ResponseMeta.Usage = &schema.TokenUsage{
				PromptTokens: resp.Prompt, CompletionTokens: resp.Completion,
				TotalTokens: resp.Prompt + resp.Completion,
			}
		}
	}
	return out, nil
}

// Stream 按同一脚本响应（Generate/Stream 共享调用序）：条目带 ToolCalls 时
// 发带工具调用的消息，否则发 Content。StreamContent 非空时优先恒定内容
// （简洁的单内容流场景）。
func (m *Model) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.StreamContent != "" {
		m.Calls++
		return singleMsgStream(m.StreamContent, nil), nil
	}
	resp, err := m.next()
	if err != nil {
		return nil, err
	}
	m.Calls++
	if resp.Err != nil {
		return nil, resp.Err
	}
	return singleMsgStream(resp.Content, resp.ToolCalls), nil
}

// singleMsgStream 单块消息流。
func singleMsgStream(content string, toolCalls []schema.ToolCall) *schema.StreamReader[*schema.Message] {
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(&schema.Message{Role: schema.Assistant, Content: content, ToolCalls: toolCalls}, nil)
	sw.Close()
	return sr
}

// next 取下一条脚本（内部已持锁）。
func (m *Model) next() (Resp, error) {
	if len(m.Script) == 0 {
		return Resp{}, fmt.Errorf("llmtest: 脚本为空（第 %d 次调用）", m.Calls+1)
	}
	if m.Calls >= len(m.Script) {
		if m.RepeatLast {
			return m.Script[len(m.Script)-1], nil
		}
		return Resp{}, fmt.Errorf("llmtest: 脚本耗尽（第 %d 次调用）", m.Calls+1)
	}
	return m.Script[m.Calls], nil
}

// ToolModel 带 WithTools 派生能力的桩（ADK 绑工具路径的降级语义测试用；
// Model 本身不实现 ToolCallingChatModel——不支持绑定的场景直接用 Model）。
type ToolModel struct {
	*Model
	// ToolTag WithTools 派生副本的 Content 后缀（区分派生前后的响应）。
	ToolTag string
}

var _ model.ToolCallingChatModel = (*ToolModel)(nil)

// WithTools 返回带 ToolTag 标记的派生副本（脚本与状态共享父桩）。
func (t *ToolModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &ToolModel{Model: t.Model, ToolTag: t.ToolTag}, nil
}

// Provider llm.Provider 接口的测试实现（Resilient 降级链测试用）。
type Provider struct {
	// Label Name() 返回值（字段不能与接口方法 Name() 同名）。
	Label      string
	ModelValue model.BaseChatModel
	CtxTokens  int
	MaxOut     int
	// Costs CostPer1KTokens 返回值 [prompt, completion]。
	Costs [2]float64
	// Timeout AttemptTimeout 返回值（>0 时生效；Resilient 单次尝试限时测试用）。
	Timeout time.Duration
}

var _ interface {
	Name() string
	Model() model.BaseChatModel
	ModelName() string
	ContextTokens() int
	MaxOutputTokens() int
	CostPer1KTokens() (float64, float64)
} = (*Provider)(nil)

func (p *Provider) Name() string                        { return p.Label }
func (p *Provider) Model() model.BaseChatModel          { return p.ModelValue }
func (p *Provider) ModelName() string                   { return p.Label + "-model" }
func (p *Provider) ContextTokens() int                  { return p.CtxTokens }
func (p *Provider) MaxOutputTokens() int                { return p.MaxOut }
func (p *Provider) CostPer1KTokens() (float64, float64) { return p.Costs[0], p.Costs[1] }

// AttemptTimeout 可选能力（llm.timeoutProvider 接口；0=不限）。
func (p *Provider) AttemptTimeout() time.Duration { return p.Timeout }

// NewScriptedProvider 便捷构造：name + 脚本（耗尽重复末条——降级链测试依赖
// 持续错误触发切换），窗口 128k / 输出 4096 / 成本 0.001|0.002。
func NewScriptedProvider(name string, script ...Resp) *Provider {
	return &Provider{
		Label:      name,
		ModelValue: &Model{Script: script, RepeatLast: true},
		CtxTokens:  128_000,
		MaxOut:     4096,
		Costs:      [2]float64{0.001, 0.002},
	}
}
