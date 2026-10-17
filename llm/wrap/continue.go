// 输出截断续写（对标 ZCode turn-output-token-continuation）：finish reason 为
// length/max_tokens 且消息无工具调用时，保留部分输出并以「直接续写」指令再调，
// 内容顺序拼接，最多 maxContinuations 轮。被截断的评审结论不再整体作废重跑——
// 续写成本远低于整轮重跑，且拼接补全 JSON 恰好是 jsonrepair 的前置救济。
//
// 纪律：
//   - 带 ToolCalls 的截断消息不续写（ReAct 中间步的续写会污染工具调用状态）；
//   - 续写失败返回已得部分（部分结果优于整体失败）；
//   - Stream 直通不续写（当前无流式消费方，装饰器如实声明）。
package wrap

import (
	"context"
	"errors"
	"strings"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// maxContinuations 续写轮数上限（ZCode 同款 3）。
const maxContinuations = 3

// continueDirective 续写指令（ZCode "Resume directly — no apology, no recap" 同义）。
const continueDirective = "直接从截断处继续输出剩余内容：不要重复已有内容，不要道歉或复述。"

// ContinueModel 输出截断续写装饰器；无可变状态，可跨任务共享。
type ContinueModel struct {
	einomodel.BaseChatModel
	Stage      string
	MaxRounds  int // ≤0 = maxContinuations
	OnContinue func(stage string, round, addedRunes int)
}

var _ einomodel.ToolCallingChatModel = (*ContinueModel)(nil)

// NewContinueModel 包装 inner；轮数上限 ≤0 时取缺省 3。
func NewContinueModel(inner einomodel.BaseChatModel, stage string, onContinue func(string, int, int)) *ContinueModel {
	return &ContinueModel{BaseChatModel: inner, Stage: stage, OnContinue: onContinue}
}

func (m *ContinueModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	msg, err := m.BaseChatModel.Generate(ctx, input, opts...)
	if err != nil || !isTruncated(msg) {
		return msg, err
	}
	max := m.MaxRounds
	if max <= 0 {
		max = maxContinuations
	}
	for round := 1; round <= max && isTruncated(msg); round++ {
		cont := make([]*schema.Message, 0, len(input)+2)
		cont = append(cont, input...)
		cont = append(cont,
			&schema.Message{Role: schema.Assistant, Content: msg.Content},
			&schema.Message{Role: schema.User, Content: continueDirective})
		next, err := m.BaseChatModel.Generate(ctx, cont, opts...)
		if err != nil || next == nil {
			// 调用方取消/超时预算耗尽不是「续写失败」，必须原样上抛——吞掉会让
			// 上层把腰斩的半截输出当正常完成品（第八轮审计）；其余错误走部分返回
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return msg, ctx.Err()
			}
			return msg, nil // 续写失败：返回已得部分
		}
		merged := *msg
		merged.Content = msg.Content + next.Content
		merged.ResponseMeta = next.ResponseMeta // 以最新 finish reason 供下轮判定
		added := len([]rune(next.Content))
		msg = &merged
		if m.OnContinue != nil {
			m.OnContinue(m.Stage, round, added)
		}
	}
	return msg, nil
}

// isTruncated 截断判定：finish reason 为 length/max_tokens 且无工具调用。
func isTruncated(msg *schema.Message) bool {
	if msg == nil || len(msg.ToolCalls) > 0 {
		return false
	}
	if msg.ResponseMeta == nil {
		return false
	}
	switch strings.ToLower(msg.ResponseMeta.FinishReason) {
	case "length", "max_tokens":
		return true
	}
	return false
}

// WithTools 工具绑定透传：绑定产物再包一层 ContinueModel（与 FitModel 同纪律）。
func (m *ContinueModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	bound, ok := m.BaseChatModel.(einomodel.ToolCallingChatModel)
	if !ok {
		return nil, errors.New("ctxcontinue: 内层模型不支持工具绑定 stage=" + m.Stage)
	}
	nb, err := bound.WithTools(tools)
	if err != nil {
		return nil, err
	}
	c := *m
	c.BaseChatModel = nb
	return &c, nil
}
