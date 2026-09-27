package mcp

// 调用期自愈（批次五十，对标 ZCode callTool 自愈：先查 disconnected 再重连、SDK 裸
// "Not connected" 竞态重试一次）：MCP 工具调用遇到传输层死亡类错误（连接关闭/未
// 连接/管道断裂）时，摘除死连接→重连→解析同名工具重试一次。业务类错误（工具真的
// 执行了但失败，isError:true）不触发自愈——那是 errorAsObservation 层的语义。
//
// 与 evict 的关系：evict 在目录列举失败时触发（连接摘除+目录失效）；本层覆盖「目录
// 拿到后、调用时连接才死」的窗口（长会话里 tools 子进程中途死亡的真形态）。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// isTransportDead 传输层死亡类错误判定（自愈可救）：连接关闭/未连接/管道断裂类；
// 业务类错误（MCP isError:true 已被 errorAsObservation 转观察）不在此列。
func isTransportDead(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"not connected", "connection closed", "connection refused",
		"broken pipe", "transport closed", "client is not initialized",
		"use of closed network connection",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// selfHealTool 调用期自愈包装：传输层死亡时经 redial 重连并解析同名工具重试一次。
// redial 由 Pool 提供闭包（摘死连接→重连→GetTools→同名解析），包装层不感知池内部。
type selfHealTool struct {
	name   string // 工具名（重连后按名解析同名工具）
	inner  tool.InvokableTool
	redial func(ctx context.Context) (tool.InvokableTool, error)
}

func (t *selfHealTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.inner.Info(ctx)
}

func (t *selfHealTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	out, err := t.inner.InvokableRun(ctx, args, opts...)
	if err == nil || !isTransportDead(err) {
		return out, err
	}
	fresh, ferr := t.redial(ctx)
	if ferr != nil {
		return "", fmt.Errorf("mcp: %s 自愈重连失败: %v（原始错误: %v）", t.name, ferr, err)
	}
	out2, err2 := fresh.InvokableRun(ctx, args, opts...)
	if err2 != nil {
		return "", fmt.Errorf("mcp: %s 自愈重试仍失败: %v（原始错误: %v）", t.name, err2, err)
	}
	return out2, nil
}

var _ tool.InvokableTool = (*selfHealTool)(nil)

// selfHealRedial 生产侧重连闭包（Pool 提供装配）：摘死连接 → 重连 → GetTools →
// 按名解析同名工具（errorAsObservation 包装保持与其他工具同形态）。
func (p *Pool) selfHealRedial(ctx context.Context, cfg ServerConfig, allow []string, name string) func(context.Context) (tool.InvokableTool, error) {
	return func(ctx context.Context) (tool.InvokableTool, error) {
		p.mu.Lock()
		cached, ok := p.clients[cfg.Name]
		p.mu.Unlock()
		if ok {
			p.evict(cfg.Name, cached)
		}
		cli, err := p.client(ctx, cfg)
		if err != nil {
			return nil, err
		}
		entries, err := p.catalogFor(ctx, cfg, cli)
		if err != nil {
			return nil, err
		}
		for _, t := range WrapErrorAsObservation(convTools(cli, entries, allow)) {
			if it, ok := t.(tool.InvokableTool); ok {
				if info, ierr := it.Info(ctx); ierr == nil && info.Name == name {
					return it, nil
				}
			}
		}
		return nil, fmt.Errorf("mcp: 重连后未找到工具 %q", name)
	}
}
