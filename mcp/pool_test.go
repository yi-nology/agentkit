package mcp

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// newTestServer 起 in-process MCP server：注册 calculate + other 两个工具。
func newTestServer(t *testing.T) *server.MCPServer {
	t.Helper()
	srv := server.NewMCPServer("test-mcp", "1.0.0",
		server.WithToolCapabilities(false))

	calc := mcp.NewTool("calculate",
		mcp.WithDescription("Perform the add operation"),
		mcp.WithString("operation", mcp.Required(), mcp.Enum("add")),
		mcp.WithNumber("x", mcp.Required()),
		mcp.WithNumber("y", mcp.Required()),
	)
	srv.AddTool(calc, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		x, _ := req.RequireFloat("x")
		y, _ := req.RequireFloat("y")
		return mcp.NewToolResultText(intToStr(x + y)), nil
	})

	other := mcp.NewTool("other", mcp.WithDescription("another tool"))
	srv.AddTool(other, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("other"), nil
	})
	return srv
}

// injectClient 把已 Initialize 的 in-process client 直灌池缓存（白盒测试路径）。
func injectClient(t *testing.T, p *Pool, name string, cli client.MCPClient) {
	t.Helper()
	if _, err := cli.Initialize(context.Background(), newInitRequest()); err != nil {
		t.Fatalf("in-process client Initialize 失败: %v", err)
	}
	p.mu.Lock()
	p.clients[name] = cli
	p.mu.Unlock()
}

func newInitRequest() mcp.InitializeRequest {
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "agentkit-test", Version: "1.0"}
	return req
}

func TestToolsWhitelist(t *testing.T) {
	srv := newTestServer(t)
	cli, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPool(ServerConfig{Name: "calc"})
	injectClient(t, p, "calc", cli)
	defer p.Close()

	// 白名单只暴露 calculate（other 不给 agent）
	tools, err := p.Tools(context.Background(), []ToolSpec{
		{Server: "calc", Allow: []string{"calculate"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("白名单应只出 1 个工具，得到 %d", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "calculate" {
		t.Fatalf("工具名 = %q", info.Name)
	}

	// 调用链路：eino tool → MCP server
	it, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatal("返回的工具应实现 tool.InvokableTool")
	}
	res, err := it.InvokableRun(context.Background(), `{"operation":"add","x":2,"y":3}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "5") {
		t.Fatalf("calculate(2,3) 应为 5，得到 %s", res)
	}
}

func TestToolsEmptyAllowAll(t *testing.T) {
	srv := newTestServer(t)
	cli, _ := client.NewInProcessClient(srv)
	p := NewPool(ServerConfig{Name: "calc"})
	injectClient(t, p, "calc", cli)
	defer p.Close()

	// 空 Allow = 全部工具
	tools, err := p.Tools(context.Background(), []ToolSpec{{Server: "calc"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("空白名单应出全部 2 个工具，得到 %d", len(tools))
	}
}

func TestToolsUnknownServer(t *testing.T) {
	p := NewPool(ServerConfig{Name: "a"})
	_, err := p.Tools(context.Background(), []ToolSpec{{Server: "nope"}})
	if err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("引用未配置 server 应报错: %v", err)
	}
}

func TestToolsPartialFailureTolerated(t *testing.T) {
	// server-a 的 command 不存在（建连失败），server-b in-process 正常
	srv := newTestServer(t)
	cli, _ := client.NewInProcessClient(srv)

	var errCount atomic.Int32
	p := NewPool(
		ServerConfig{Name: "broken", Command: []string{"/nonexistent/mcp-server"}},
		ServerConfig{Name: "good"},
	)
	injectClient(t, p, "good", cli)
	p.OnError = func(server string, err error) {
		if server == "broken" {
			errCount.Add(1)
		}
	}
	defer p.Close()

	tools, err := p.Tools(context.Background(), []ToolSpec{
		{Server: "broken"},
		{Server: "good", Allow: []string{"other"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if errCount.Load() != 1 {
		t.Fatalf("broken server 应触发一次 OnError，实际 %d", errCount.Load())
	}
	if len(tools) != 1 {
		t.Fatalf("部分失败容忍：应返回 good 的 1 个工具，得到 %d", len(tools))
	}
}

func TestStdioDialFailure(t *testing.T) {
	p := NewPool(ServerConfig{Name: "x", Command: []string{"/nonexistent/mcp"}, Timeout: 2 * time.Second})
	_, err := p.client(context.Background(), ServerConfig{Name: "x", Command: []string{"/nonexistent/mcp"}})
	if err == nil {
		t.Fatal("不存在的命令应建连失败")
	}
}

func TestDialBothEmpty(t *testing.T) {
	if _, err := dial(context.Background(), ServerConfig{Name: "x"}); err == nil ||
		!strings.Contains(err.Error(), "均未配置") {
		t.Fatalf("stdio/http 均空应报错: %v", err)
	}
}

func TestPoolClose(t *testing.T) {
	srv := newTestServer(t)
	cli, _ := client.NewInProcessClient(srv)
	p := NewPool(ServerConfig{Name: "calc"})
	injectClient(t, p, "calc", cli)

	p.Close()
	p.Close() // 幂等
	if len(p.clients) != 0 {
		t.Fatal("Close 后缓存应清空")
	}
}

func intToStr(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64) // 整数值场景输出 "5"
}
