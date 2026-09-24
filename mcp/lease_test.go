// 连接租约面测试（批次五十三沉淀）：PingServer 探活/摘除/无连接哨兵 + 闲置回收。
// 假体只实现用到的接口面（MCPClient 大接口，embedding 兜底未用方法）。
package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
)

// fakeClient MCPClient 假体：Ping 可编排死活。
type fakeClient struct {
	client.MCPClient // embedding：未实现方法 panic 即「测试不应触达」
	dead             bool
	closed           bool
}

func (f *fakeClient) Ping(ctx context.Context) error {
	if f.dead {
		return errors.New("connection refused")
	}
	return nil
}

func (f *fakeClient) Close() error {
	f.closed = true
	return nil
}

// seedFake 直塞缓存（绕过真实 dial）。
func seedFake(p *Pool, name string, cli client.MCPClient) {
	p.mu.Lock()
	p.cfgs[name] = ServerConfig{Name: name}
	p.clients[name] = cli
	p.lastUsed[name] = time.Now()
	p.mu.Unlock()
}

func TestPingServerAliveAndDead(t *testing.T) {
	p := NewPool()
	alive := &fakeClient{}
	seedFake(p, "ok", alive)
	if err := p.PingServer(context.Background(), "ok"); err != nil {
		t.Fatalf("存活探活不应失败: %v", err)
	}
	if alive.closed {
		t.Fatal("存活连接不应被关闭")
	}

	dead := &fakeClient{dead: true}
	seedFake(p, "dead", dead)
	if err := p.PingServer(context.Background(), "dead"); err == nil {
		t.Fatal("死亡探活应报错")
	}
	if !dead.closed {
		t.Fatal("死亡连接应被摘除关闭")
	}
	p.mu.Lock()
	_, still := p.clients["dead"]
	p.mu.Unlock()
	if still {
		t.Fatal("死亡连接应出缓存")
	}
}

func TestPingServerNotConnected(t *testing.T) {
	p := NewPool(ServerConfig{Name: "ghost", URL: "http://127.0.0.1:1/mcp"})
	err := p.PingServer(context.Background(), "ghost")
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("无缓存连接应返回哨兵: %v", err)
	}
}

func TestReapIdle(t *testing.T) {
	p := NewPool()
	old := &fakeClient{}
	fresh := &fakeClient{}
	seedFake(p, "old", old)
	seedFake(p, "fresh", fresh)
	p.mu.Lock()
	p.lastUsed["old"] = time.Now().Add(-10 * time.Minute)
	p.mu.Unlock()

	n := p.ReapIdle(time.Minute)
	if n != 1 {
		t.Fatalf("应回收 1 条: %d", n)
	}
	if !old.closed {
		t.Fatal("闲置连接应被关闭")
	}
	if fresh.closed {
		t.Fatal("新鲜连接不应被回收")
	}
	p.mu.Lock()
	_, oldIn := p.clients["old"]
	_, freshIn := p.clients["fresh"]
	p.mu.Unlock()
	if oldIn || !freshIn {
		t.Fatal("缓存状态与回收结果不一致")
	}
	// 无新增闲置：再跑回收 0 条。
	if n := p.ReapIdle(time.Minute); n != 0 {
		t.Fatalf("二次回收应为 0: %d", n)
	}
}

func TestStartIdleReaperStopsOnClose(t *testing.T) {
	p := NewPool()
	p.StartIdleReaper(time.Millisecond, time.Minute)
	p.StartIdleReaper(time.Millisecond, time.Minute) // 幂等
	time.Sleep(5 * time.Millisecond)
	p.Close()
	// Close 后 reapStop 关闭，协程退出（无泄漏断言靠 race/无阻塞；这里只验证不 panic）。
}
