// errorAsObservation MCP isError 降级器（2026-09-21 k8s 106 审计 P1）：eino-ext 把
// MCP isError:true 的工具结果当调用 error 上抛，ReAct 直接以 NodeRunError 炸掉整个
// agent 步骤——LLM 没有机会看到错误并修正参数（实弹：k8sgpt get-resource 猜错 pod 名
// → 专家整步 degraded）。本装饰器把这类错误降级为文本观察回传 LLM（是否修正参数/
// 换路径由 LLM 自行决定）；传输层等其他错误原样上抛。仅 mcp.Pool.Tools() 出口启用。
package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// errorAsObservation 包装可调用工具（MCP 工具均为 InvokableTool 形态）。
type errorAsObservation struct {
	inner tool.InvokableTool
}

func (w *errorAsObservation) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *errorAsObservation) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	out, err := w.inner.InvokableRun(ctx, args, opts...)
	if err == nil {
		return out, nil
	}
	if obs, ok := mcpErrorObservation(err); ok {
		return obs, nil
	}
	return out, err
}

// WrapErrorAsObservation 出口统一包装（幂等：已包装不重复包；非 InvokableTool 原样透传）。
func WrapErrorAsObservation(tools []tool.BaseTool) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, len(tools))
	for _, t := range tools {
		if _, ok := t.(*errorAsObservation); ok {
			out = append(out, t)
			continue
		}
		if it, ok := t.(tool.InvokableTool); ok {
			out = append(out, &errorAsObservation{inner: it})
			continue
		}
		out = append(out, t)
	}
	return out
}

// mcpErrorObservation 识别「MCP 工具执行了但业务失败」（isError:true）并剥出错误文本
// 作为观察；传输层错误（不可达/超时）不在此列。
func mcpErrorObservation(err error) (string, bool) {
	msg := err.Error()
	if !strings.Contains(msg, "mcp server return error") {
		return "", false
	}
	if i := strings.Index(msg, "{"); i >= 0 {
		var m struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal([]byte(msg[i:]), &m) == nil && len(m.Content) > 0 {
			return "MCP 工具执行失败（真实工具反馈，请据此修正参数、先列出存在的资源再取详情，或改用其他工具；不要重复同一调用）：\n" +
				m.Content[0].Text, true
		}
	}
	return "MCP 工具执行失败（真实工具反馈）：\n" + msg, true
}
