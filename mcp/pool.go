// Package mcp MCP 工具池：MCP server 连接管理 → eino tool.BaseTool（对齐 eino-ext
// components/tool/mcp 的能力面，补连接生命周期与白名单池化）。
//
// 定位（对应 Argus design §9.2 方向 A）：R3/builtin agent 需要 diff 之外的上下文
// 时，把外部 MCP server 的工具挂进 agent 工具表（工单系统/内部文档/linter 等）。
//
// 用法：
//
//	pool := mcp.NewPool(
//	    mcp.ServerConfig{Name: "docs", URL: "http://docs.svc/mcp",
//	        Headers: map[string]string{"Authorization": "Bearer " + tok}},
//	    mcp.ServerConfig{Name: "lint", Command: []string{"lint-mcp", "serve"}},
//	)
//	defer pool.Close()
//	tools, err := pool.Tools(ctx, []mcp.ToolSpec{{Server: "docs", Allow: []string{"search_docs"}}})
//
// 安全模型：stdio 子进程环境走白名单透传（绝不继承密钥）；工具白名单按 spec
// 收敛（未声明的 server/工具不暴露给 agent）。
package mcp

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// DefaultTimeout 连接与初始化的缺省超时。
const DefaultTimeout = 30 * time.Second

// ServerConfig 单个 MCP server 连接声明。
// stdio（Command 非空）与 HTTP（URL 非空）二选一；都空视为无效配置（跳过并告警）。
type ServerConfig struct {
	// Name 逻辑名：ToolSpec.Server 引用。
	Name string
	// Command stdio 传输：可执行 + 参数（子进程由本包启动与回收）。
	Command []string
	// Env stdio 子进程额外环境变量白名单（按名从当前进程透传）。
	Env []string
	// URL HTTP 传输（streamable http）。
	URL string
	// Headers HTTP 请求头（如 Bearer 鉴权）。
	Headers map[string]string
	// Timeout 连接 + Initialize 超时（默认 30s）。
	Timeout time.Duration
}

// ToolSpec 工具白名单：某 server 的哪些工具暴露给 agent。
type ToolSpec struct {
	// Server ServerConfig.Name。
	Server string
	// Allow 工具名白名单；空 = 该 server 全部工具。
	Allow []string
}

// Pool MCP 连接池：lazy 建连（首次 Tools 时）、连接缓存复用、Close 全量回收。
type Pool struct {
	mu      sync.Mutex
	cfgs    map[string]ServerConfig
	clients map[string]client.MCPClient
	// OnError 建连/列举失败回调（nil 安全）。返回错误不中断其余 server——
	// 部分失败容忍：可用的工具照常返回，失败的 server 由调用方经钩子观测。
	OnError func(server string, err error)
}

// NewPool 创建连接池（配置校验延后到 Tools 调用时——允许启动期 MCP server 未就绪）。
func NewPool(cfgs ...ServerConfig) *Pool {
	p := &Pool{cfgs: map[string]ServerConfig{}, clients: map[string]client.MCPClient{}}
	for _, c := range cfgs {
		p.cfgs[c.Name] = c
	}
	return p
}

// Servers 返回全部配置的 server 名（字母序，可观测用）。
func (p *Pool) Servers() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.cfgs))
	for n := range p.cfgs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Tools 按 spec 白名单返回 eino 工具。
// spec.Server 未配置 → 报错（配置错误应显式暴露）；server 建连失败 → OnError 钩子
// 后跳过（部分失败容忍）。
func (p *Pool) Tools(ctx context.Context, specs []ToolSpec) ([]tool.BaseTool, error) {
	var out []tool.BaseTool
	for _, spec := range specs {
		cfg, ok := p.cfgs[spec.Server]
		if !ok {
			return nil, fmt.Errorf("mcp: ToolSpec 引用未配置的 server %q（已配置: %v）",
				spec.Server, p.Servers())
		}
		cli, err := p.client(ctx, cfg)
		if err != nil {
			if p.OnError != nil {
				p.OnError(cfg.Name, err)
			}
			continue // 部分失败容忍
		}
		tools, err := einomcp.GetTools(ctx, &einomcp.Config{
			Cli:          cli,
			ToolNameList: spec.Allow,
		})
		if err != nil {
			if p.OnError != nil {
				p.OnError(cfg.Name, err)
			}
			continue
		}
		out = append(out, tools...)
	}
	return out, nil
}

// client 获取（lazy 建连 + 缓存）。失败时不缓存——下次调用重试。
func (p *Pool) client(ctx context.Context, cfg ServerConfig) (client.MCPClient, error) {
	p.mu.Lock()
	if cli, ok := p.clients[cfg.Name]; ok {
		p.mu.Unlock()
		return cli, nil
	}
	p.mu.Unlock()

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cli, err := dial(cctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s 建连失败: %w", cfg.Name, err)
	}

	// MCP 协议要求先 Initialize 才能调用
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "agentkit", Version: "1.0"}
	if _, err := cli.Initialize(cctx, initReq); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("mcp: %s Initialize 失败: %w", cfg.Name, err)
	}

	p.mu.Lock()
	// 双检：并发建连时保留先到者，关闭后来者
	if existing, ok := p.clients[cfg.Name]; ok {
		p.mu.Unlock()
		_ = cli.Close()
		return existing, nil
	}
	p.clients[cfg.Name] = cli
	p.mu.Unlock()
	return cli, nil
}

// dial 按配置构造 MCP client（stdio 或 streamable http）。
func dial(ctx context.Context, cfg ServerConfig) (client.MCPClient, error) {
	switch {
	case len(cfg.Command) > 0:
		// NewStdioMCPClient 自动启动子进程
		return client.NewStdioMCPClient(cfg.Command[0], cfg.Env, cfg.Command[1:]...)
	case cfg.URL != "":
		return client.NewStreamableHttpClient(cfg.URL,
			transport.WithHTTPHeaders(cfg.Headers),
			transport.WithHTTPTimeout(cfg.TimeoutOr(DefaultTimeout)))
	default:
		return nil, fmt.Errorf("stdio（command）与 http（url）均未配置")
	}
}

// TimeoutOr cfg.Timeout 的非零回退。
func (c ServerConfig) TimeoutOr(d time.Duration) time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return d
}

// Close 关闭全部缓存连接（幂等）。
func (p *Pool) Close() {
	p.mu.Lock()
	clients := p.clients
	p.clients = map[string]client.MCPClient{}
	p.mu.Unlock()
	for _, cli := range clients {
		_ = cli.Close()
	}
}
