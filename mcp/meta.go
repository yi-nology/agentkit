// 请求侧上下文透传（批次五十六 B，对标 ZCode request-context：请求侧
// _meta 携带 trace/workspace 上下文，adapters/src/mcp/index.ts:1767-1770）：
// Pool.RequestMeta 在每笔工具调用的 ctx 上取值，经 metaClient 装饰器注入
// req.Params.Meta（stdio 与 HTTP 同走 JSON-RPC params，server 侧通用）。
// 池不感知业务键——metaFn 由装配方提供（bianque 侧从 ctx 取 session/trace/caller）。
package mcp

import (
	"context"
	"maps"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// metaClient _meta 注入装饰器：仅覆写 CallTool，其余方法 embedding 透传。
// 已有 Meta 时按 AdditionalFields 合并，metaFn 键胜（平台命名空间 com.bianque/
// 不该被业务方占用）。
type metaClient struct {
	client.MCPClient
	metaFn func(ctx context.Context) map[string]any
}

func (c *metaClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if c.metaFn != nil {
		if kv := c.metaFn(ctx); len(kv) > 0 {
			merged := make(map[string]any, len(kv)+2)
			if req.Params.Meta != nil {
				maps.Copy(merged, req.Params.Meta.AdditionalFields)
			}
			maps.Copy(merged, kv)
			req.Params.Meta = mcp.NewMetaFromMap(merged)
		}
	}
	return c.MCPClient.CallTool(ctx, req)
}

var _ client.MCPClient = (*metaClient)(nil)
