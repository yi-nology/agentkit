// 目录层（批次对标 ZCode「连接时一次 tools/list、注册面全量」adapters/src/mcp）：
// 每条连接建立后仅首次取目录走一次线上 ListTools，此后 Tools() 命中内存缓存；
// 连接被 evict（列举失败/探活死亡/自愈摘除）时目录随连接一并失效，重建连接后
// 重新列举。第三方 server 工具面在连接生命周期内视为静态——工具集变化需重连
// 生效，换来每次专家装配/直调不再全量走线（此前 einomcp.GetTools 每次 Tools()
// 都发一轮 ListTools）。
//
// 注解（annotations）随目录同源提取，供并发治理与风险分级消费（对标 ZCode
// readOnlyHint/idempotentHint/destructiveHint → 风险级 + concurrentSafe 映射）。
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// ToolHints 工具注解归一（mcp.ToolAnnotation 子集，nil 视为规范缺省）。
type ToolHints struct {
	ReadOnly    bool
	Idempotent  bool
	Destructive bool
}

// ConcurrentSafe 读读可并行（readOnly+idempotent，对标 ZCode concurrentSafe）；
// 缺省注解按非安全处理——保守。
func (h ToolHints) ConcurrentSafe() bool { return h.ReadOnly && h.Idempotent }

// RiskLevel 风险级（对标 ZCode：readOnly→low、destructive→high、其余 medium）。
func (h ToolHints) RiskLevel() string {
	switch {
	case h.ReadOnly:
		return "low"
	case h.Destructive:
		return "high"
	default:
		return "medium"
	}
}

// hintsOf mcp.Tool 注解 → 归一 hints。DestructiveHint 缺省语义按 MCP 规范为 true
// （除非声明只读）；mcp-go 服务端 NewTool 本身也缺省填 false/true。
func hintsOf(t mcp.Tool) ToolHints {
	a := t.Annotations
	var h ToolHints
	if a.ReadOnlyHint != nil {
		h.ReadOnly = *a.ReadOnlyHint
	}
	if a.IdempotentHint != nil {
		h.Idempotent = *a.IdempotentHint
	}
	switch {
	case a.DestructiveHint != nil:
		h.Destructive = *a.DestructiveHint
	case !h.ReadOnly:
		h.Destructive = true
	}
	return h
}

// toolEntry 目录条目：原始工具定义 + 归一注解。
type toolEntry struct {
	tool  mcp.Tool
	hints ToolHints
}

// listTools 线上列举一页（与 eino-ext 同不翻页——工具目录场景页大小足够）。
func listTools(ctx context.Context, cli client.MCPClient, timeout time.Duration) ([]toolEntry, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := cli.ListTools(cctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, err
	}
	entries := make([]toolEntry, 0, len(res.Tools))
	for _, t := range res.Tools {
		entries = append(entries, toolEntry{tool: t, hints: hintsOf(t)})
	}
	return entries, nil
}

// convTools 目录条目 → eino 工具（内存转换，无 IO）。allow 空 = 全部；过滤保持
// 服务端目录序（与 eino-ext GetTools 行为一致）。
func convTools(cli client.MCPClient, entries []toolEntry, allow []string) []tool.BaseTool {
	want := map[string]struct{}{}
	all := len(allow) == 0
	for _, n := range allow {
		want[n] = struct{}{}
	}
	out := make([]tool.BaseTool, 0, len(entries))
	for _, e := range entries {
		if !all {
			if _, ok := want[e.tool.Name]; !ok {
				continue
			}
		}
		out = append(out, convTool(cli, e))
	}
	return out
}

// convTool 单条目录 → eino 可调用工具。转换口径与 eino-ext components/tool/mcp
// v0.0.9 GetTools 一致：InputSchema → JSON Schema → ParamsOneOf。isError:true 的
// 降级由上层 WrapErrorAsObservation 承担——其识别锚点是错误文本
// "mcp server return error"，勿改动该文案。
func convTool(cli client.MCPClient, e toolEntry) tool.BaseTool {
	inputSchema := &jsonschema.Schema{}
	if raw, err := json.Marshal(e.tool.InputSchema); err == nil {
		_ = json.Unmarshal(raw, inputSchema)
	}
	return &invokableTool{
		cli: cli,
		info: &schema.ToolInfo{
			Name:        e.tool.Name,
			Desc:        e.tool.Description,
			ParamsOneOf: schema.NewParamsOneOfByJSONSchema(inputSchema),
		},
	}
}

// invokableTool eino 可调用工具（eino-ext toolHelper 的等价自持版；调用请求构造
// 与结果序列化口径保持一致）。
type invokableTool struct {
	cli  client.MCPClient
	info *schema.ToolInfo
}

func (m *invokableTool) Info(context.Context) (*schema.ToolInfo, error) { return m.info, nil }

func (m *invokableTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	if args == "" {
		args = "{}"
	}
	result, err := m.cli.CallTool(ctx, mcp.CallToolRequest{
		Request: mcp.Request{Method: "tools/call"},
		Params: mcp.CallToolParams{
			Name:      m.info.Name,
			Arguments: json.RawMessage(args),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to call mcp tool: %w", err)
	}
	marshaled, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal mcp tool result: %w", err)
	}
	if result.IsError {
		return "", fmt.Errorf("failed to call mcp tool, mcp server return error: %s", marshaled)
	}
	return string(marshaled), nil
}

var (
	_ tool.BaseTool      = (*invokableTool)(nil)
	_ tool.InvokableTool = (*invokableTool)(nil)
)

// catalogFor 取 server 目录（缓存优先，缺则线上列举一次并落缓存）。列举失败 =
// 连接失效：摘除连接并回错（调用方经 OnError 观测、跳过该 server）。
// 并发列举重复发起无碍——落缓存时先到者为准，后来者让位。
func (p *Pool) catalogFor(ctx context.Context, cfg ServerConfig, cli client.MCPClient) ([]toolEntry, error) {
	p.mu.Lock()
	if entries, ok := p.catalog[cfg.Name]; ok {
		p.mu.Unlock()
		return entries, nil
	}
	p.mu.Unlock()

	entries, err := listTools(ctx, cli, cfg.timeoutOr(DefaultTimeout))
	if err != nil {
		p.evict(cfg.Name, cli)
		return nil, fmt.Errorf("mcp: %s 列举工具失败: %w", cfg.Name, err)
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("mcp: 池已关闭")
	}
	if cur, ok := p.catalog[cfg.Name]; ok {
		entries = cur
	} else {
		p.catalog[cfg.Name] = entries
	}
	p.mu.Unlock()
	return entries, nil
}

// Hints 目录注解快照（server 名 → 工具名 → 注解）。server 未配置或未列举 → nil。
// 供装饰层做并发治理与风险分级；返回副本，调用方可自由持有。
func (p *Pool) Hints(server string) map[string]ToolHints {
	p.mu.Lock()
	defer p.mu.Unlock()
	entries, ok := p.catalog[server]
	if !ok {
		return nil
	}
	out := make(map[string]ToolHints, len(entries))
	for _, e := range entries {
		out[e.tool.Name] = e.hints
	}
	return out
}
