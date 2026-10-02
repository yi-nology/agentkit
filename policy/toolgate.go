package policy

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
)

// auditTool 操作审计门工具装饰器：每次工具调用先过 Gate 裁决、回调留痕，deny 即
// 返回错误（沿工具结果通道如实降级，与派发拒绝同语义——agent 拿到工具错误自行降级，
// 不中断整个环节）。裁决是纯内存操作，无需超时保护，应装在宿主超时装饰器之外。
type auditTool struct {
	gate    *Gate
	op      Op // 构造时固化（工具名/模式/变异标记按次静态可判）
	onAudit func(Op, Decision)
	tool.InvokableTool
}

func (t auditTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	dec := t.gate.Decide(ctx, t.op)
	if t.onAudit != nil {
		t.onAudit(t.op, dec)
	}
	if dec.Verdict == VerdictDeny {
		return "", fmt.Errorf("操作审计门拒绝该工具调用（%s: %s）", dec.RuleID, dec.Reason)
	}
	return t.InvokableTool.InvokableRun(ctx, args, opts...)
}

// WithAuditGate 给工具套操作审计门；gate 为 nil 原样返回（未装配审计门=存量行为）。
func WithAuditGate(g *Gate, op Op, onAudit func(Op, Decision), t tool.InvokableTool) tool.InvokableTool {
	if g == nil {
		return t
	}
	return auditTool{gate: g, op: op, onAudit: onAudit, InvokableTool: t}
}
