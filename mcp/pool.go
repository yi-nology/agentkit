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
// 安全模型：stdio 子进程环境走白名单透传（绝不继承密钥，经 procx.ChildEnv
// 单一纪律）；工具白名单按 spec 收敛（未声明的 server/工具不暴露给 agent）。
package mcp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/yi-nology/agentkit/procx"
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
	// Env stdio 子进程环境白名单：条目为纯变量名（如 "GITHUB_TOKEN"）时按名从
	// 当前进程透传；含 "=" 时按 KEY=VALUE 字面透传。未列出的变量一律不继承
	// （基础集见 procx.ChildEnv），防止本进程密钥泄漏给外部 MCP server。
	Env []string
	// URL HTTP 传输（streamable http）。
	URL string
	// Headers HTTP 请求头（如 Bearer 鉴权）。
	Headers map[string]string
	// Timeout 连接 + Initialize 超时（默认 30s）。
	Timeout time.Duration
	// ProbeTimeout 探活（PingServer）独立预算（默认 5s；租约面 lease.go——
	// 探活是轻量协议 ping，不该吃连接级 30s 预算）。
	ProbeTimeout time.Duration
}

// ToolSpec 工具白名单：某 server 的哪些工具暴露给 agent。
type ToolSpec struct {
	// Server ServerConfig.Name。
	Server string
	// Allow 工具名白名单；空 = 该 server 全部工具。
	Allow []string
}

// Pool MCP 连接池：lazy 建连（首次 Tools 时）、连接缓存复用、Close 全量回收。
// 闲置回收/探活租约面见 lease.go（ReapIdle/StartIdleReaper/PingServer）。
type Pool struct {
	mu sync.Mutex
	// closed 置位后拒绝新建连接（Close 与在途建连并发时，新连接会被立即关闭，
	// 不产生泄漏）。Close 后池不可复用。
	closed  bool
	cfgs    map[string]ServerConfig
	clients map[string]client.MCPClient
	// lastUsed 连接最近使用时刻（建连/取工具/探活成功；闲置回收时钟，lease.go）。
	lastUsed map[string]time.Time
	// catalog 每连接的 tools/list 目录缓存（server → 条目，含注解；见 catalog.go）。
	// 连接被 evict 时同 server 目录一并失效。
	catalog map[string][]toolEntry
	// reapStop 闲置回收协程停止信号（StartIdleReaper 置位；Close 关闭）。
	reapStop chan struct{}
	// OnError 建连/列举失败回调（nil 安全）。返回错误不中断其余 server——
	// 部分失败容忍：可用的工具照常返回，失败的 server 由调用方经钩子观测。
	OnError func(server string, err error)
	// RequestMeta 每笔工具调用 _meta 注入来源（nil 安全；批次五十六 B，对标 ZCode
	// request-context）。metaFn 在调用方 ctx 上取值，池不感知业务键；经 metaClient
	// 装饰器注入 req.Params.Meta（见 meta.go）。返回 nil/空 = 不注入。
	RequestMeta func(ctx context.Context) map[string]any
}

// NewPool 创建连接池（建连延后到 Tools 调用时——允许启动期 MCP server 未就绪）。
// Name 为空的配置忽略；重名保留先到者（后者忽略）——配置错误尽早收敛为确定性
// 行为而非静默覆盖。
func NewPool(cfgs ...ServerConfig) *Pool {
	p := &Pool{cfgs: map[string]ServerConfig{}, clients: map[string]client.MCPClient{},
		lastUsed: map[string]time.Time{}, catalog: map[string][]toolEntry{}}
	for _, c := range cfgs {
		if c.Name == "" {
			continue
		}
		if _, dup := p.cfgs[c.Name]; !dup {
			p.cfgs[c.Name] = c
		}
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
// 后跳过（部分失败容忍）；列举失败视为该连接失效——从缓存摘除并关闭（server 崩溃
// 后下次调用自动重建），同样经 OnError 跳过。
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
		entries, err := p.catalogFor(ctx, cfg, cli)
		if err != nil {
			if p.OnError != nil {
				p.OnError(cfg.Name, err)
			}
			continue
		}
		tools := convTools(cli, entries, spec.Allow)
		// Allow 白名单全部未命中（拼写错误等）：转换层静默返回空表，
		// 这里经 OnError 告警，避免 agent 拿到空工具表却无从排查
		if len(tools) == 0 && len(spec.Allow) > 0 && p.OnError != nil {
			p.OnError(cfg.Name, fmt.Errorf("mcp: %s 白名单 %v 无一命中（返回 0 个工具）", cfg.Name, spec.Allow))
		}
		// 调用期自愈（批次五十）：自愈层在最外（传输层死亡重连重试），业务错误
		// （isError:true）仍由 errorAsObservation 层转观察回喂 LLM。
		for _, w := range WrapErrorAsObservation(tools) {
			if it, ok := w.(tool.InvokableTool); ok {
				info, ierr := it.Info(ctx)
				if ierr != nil {
					out = append(out, w)
					continue
				}
				allow := spec.Allow
				out = append(out, &selfHealTool{
					name:   info.Name,
					inner:  it,
					redial: p.selfHealRedial(ctx, cfg, allow, info.Name),
				})
				continue
			}
			out = append(out, w)
		}
	}
	return out, nil
}

// client 获取（lazy 建连 + 缓存）。失败时不缓存——下次调用重试。
func (p *Pool) client(ctx context.Context, cfg ServerConfig) (client.MCPClient, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("mcp: 池已关闭")
	}
	if cli, ok := p.clients[cfg.Name]; ok {
		p.lastUsed[cfg.Name] = time.Now()
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
	// 双检：并发建连时保留先到者，关闭后来者；池已关闭则立即回收新建连接
	if p.closed {
		p.mu.Unlock()
		_ = cli.Close()
		return nil, errors.New("mcp: 池已关闭")
	}
	if existing, ok := p.clients[cfg.Name]; ok {
		p.mu.Unlock()
		_ = cli.Close()
		return existing, nil
	}
	if p.RequestMeta != nil {
		// _meta 请求上下文注入（meta.go）：装饰器在 client() 单点包装——初次建连
		// 与自愈 redial 同走此路，evict 指针比对基于装饰器一致。
		cli = &metaClient{MCPClient: cli, metaFn: p.RequestMeta}
	}
	p.clients[cfg.Name] = cli
	p.lastUsed[cfg.Name] = time.Now()
	p.mu.Unlock()
	return cli, nil
}

// evict 连接失效时从缓存摘除并关闭（仅当缓存中的仍是该连接），下次调用走重建；
// 同 server 目录缓存一并失效（重建连接后重新列举）。
func (p *Pool) evict(name string, cli client.MCPClient) {
	p.mu.Lock()
	cached, ok := p.clients[name]
	if ok && cached == cli {
		delete(p.clients, name)
		delete(p.catalog, name)
	} else {
		cli = nil
	}
	p.mu.Unlock()
	if cli != nil {
		_ = cli.Close()
	}
}

// dial 按配置构造 MCP client（stdio 或 streamable http）。
func dial(ctx context.Context, cfg ServerConfig) (client.MCPClient, error) {
	switch {
	case len(cfg.Command) > 0:
		// argv[0] 只允许是可执行名：以 "-" 开头会落入下游参数解析当选项
		// （argument injection）；Command 来自装配配置，越界即配置错误，fail-fast
		if cfg.Command[0] == "" || strings.HasPrefix(cfg.Command[0], "-") {
			return nil, fmt.Errorf("mcp: server %s 非法 command %q", cfg.Name, cfg.Command[0])
		}
		// 经 CommandFunc 接管 exec.Cmd：环境只给白名单（procx.ChildEnv，
		// 全仓库子进程环境纪律单一事实源），绝不继承全量 os.Environ()
		return client.NewStdioMCPClientWithOptions(cfg.Command[0], cfg.Env, cfg.Command[1:],
			transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Env = procx.ChildEnv(env)
				return cmd, nil
			}))
	case cfg.URL != "":
		return client.NewStreamableHttpClient(cfg.URL,
			transport.WithHTTPHeaders(cfg.Headers),
			transport.WithHTTPTimeout(cfg.timeoutOr(DefaultTimeout)))
	default:
		return nil, fmt.Errorf("mcp: server %s stdio（command）与 http（url）均未配置", cfg.Name)
	}
}

// timeoutOr cfg.Timeout 的非零回退。
func (c ServerConfig) timeoutOr(d time.Duration) time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return d
}

// Close 关闭全部缓存连接并拒绝后续建连（幂等）；停止闲置回收协程。Close 后池不可复用。
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	clients := p.clients
	p.clients = map[string]client.MCPClient{}
	p.catalog = map[string][]toolEntry{}
	stop := p.reapStop
	p.reapStop = nil
	p.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	for _, cli := range clients {
		_ = cli.Close()
	}
}
