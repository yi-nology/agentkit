// 连接租约面（批次五十三自 bianque 对标 ZCode pool 沉淀）：探活 + 闲置回收。
//
// 语义裁定——ZCode 原型是「引用计数归零 + 30s 惰性关闭」，本池不可照搬：工具对象
// （eino tool）持有连接且生命周期调用方不可见，refcount 无处挂钩。借用模型的等价
// 租约 = **闲置回收**（ReapIdle/StartIdleReaper）+ **调用期透明重建**（既有 evict
// + lazy 建连）。回收只动缓存连接，不追踪在途工具调用——启用回收的部署必须保证
// maxIdle 显著大于最大工具超时（工具调用中不触碰 lastUsed，调用中连接可能被回收）。
package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/client"
)

// ErrNotConnected PingServer 探活时该 server 无缓存连接——懒建连模型下属正常态
// （与「有连接但已死」区分，探活无意义不算失败）。
var ErrNotConnected = errors.New("mcp: server 无缓存连接（未建连或已被回收）")

// DefaultProbeTimeout 探活独立预算（批次五十六 B：ping 是轻量协议方法，不该吃
// 连接级 30s 预算；对标 ZCode MCP_PING_TIMEOUT_MS=5s）。
const DefaultProbeTimeout = 5 * time.Second

// PingServer 探活指定 server 的缓存连接（MCP 协议 ping）。死亡连接立即摘除出缓存
// 并关闭，下次调用透明重建——HTTP server 被停掉不派发断连回调，「无声死亡」只有
// 显式探活才能暴露（ZCode pool 同款问题与解法）。
//
// 返回 ErrNotConnected = 无缓存连接（不算失败）；其余错误 = 探活失败（连接已摘除）。
func (p *Pool) PingServer(ctx context.Context, name string) error {
	p.mu.Lock()
	cli, ok := p.clients[name]
	if !ok {
		p.mu.Unlock()
		return ErrNotConnected
	}
	cfg := p.cfgs[name]
	p.mu.Unlock()

	budget := cfg.ProbeTimeout
	if budget <= 0 {
		budget = DefaultProbeTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if err := cli.Ping(cctx); err != nil {
		p.evict(name, cli)
		return fmt.Errorf("mcp: %s 探活失败（连接已摘除，下次调用重建）: %w", name, err)
	}
	p.touch(name)
	return nil
}

// ReapIdle 关闭闲置超过 maxIdle 的缓存连接，返回回收数。闲置时钟 = 最近一次
// 建连/取工具/探活成功。被回收的 server 下次调用透明重建（evict+lazy 建连）。
// ⚠ 在途工具调用不触碰闲置时钟：maxIdle 必须显著大于部署的最大工具超时。
func (p *Pool) ReapIdle(maxIdle time.Duration) int {
	if maxIdle <= 0 {
		return 0
	}
	deadline := time.Now().Add(-maxIdle)
	p.mu.Lock()
	var victims []string
	for name, used := range p.lastUsed {
		if used.Before(deadline) {
			if _, alive := p.clients[name]; alive {
				victims = append(victims, name)
			}
		}
	}
	type victim struct {
		name string
		cli  client.MCPClient
	}
	var closing []victim
	for _, name := range victims {
		if cli, ok := p.clients[name]; ok {
			delete(p.clients, name)
			delete(p.lastUsed, name)
			closing = append(closing, victim{name, cli})
		}
	}
	p.mu.Unlock()
	for _, v := range closing {
		_ = v.cli.Close()
		if p.OnError != nil {
			p.OnError(v.name, fmt.Errorf("mcp: 闲置超过 %s，连接已回收（下次调用重建）", maxIdle))
		}
	}
	return len(closing)
}

// StartIdleReaper 启动后台闲置回收协程：每 interval 周期执行 ReapIdle(maxIdle)。
// Close 停止；重复启动幂等（只起一个）。
func (p *Pool) StartIdleReaper(interval, maxIdle time.Duration) {
	p.mu.Lock()
	if p.reapStop != nil {
		p.mu.Unlock()
		return
	}
	p.reapStop = make(chan struct{})
	stop := p.reapStop
	p.mu.Unlock()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				p.ReapIdle(maxIdle)
			}
		}
	}()
}

// touch 记录 server 连接最近使用时刻。
func (p *Pool) touch(name string) {
	p.mu.Lock()
	p.lastUsed[name] = time.Now()
	p.mu.Unlock()
}
