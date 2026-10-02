// Package progress 泛型事件总线。
// 多订阅者广播，每个订阅者 64 缓冲，满了就丢（观测数据允许有损）。
// 发布永不阻塞（订阅者慢不拖慢主链路）。
package progress

import (
	"context"
	"sync"
	"sync/atomic"

	"git.enjoye.top/enjoydream/ekit/concurrency/async"
)

// Bus 泛型多订阅者广播总线。
type Bus[T any] struct {
	mu      sync.Mutex
	subs    map[chan T]struct{}
	dropped atomic.Int64 // 订阅者积压导致的丢弃事件计数（Dropped 观测）
}

// Dropped 累计丢弃的事件数（订阅者积压时）。有损是声明的设计，
// 计数让"进度条停在中间态"可归因。
func (b *Bus[T]) Dropped() int64 {
	if b == nil {
		return 0
	}
	return b.dropped.Load()
}

// NewBus 创建事件总线。
func NewBus[T any]() *Bus[T] {
	return &Bus[T]{subs: map[chan T]struct{}{}}
}

// Publish 非阻塞广播。nil Bus 安全（no-op）。
func (b *Bus[T]) Publish(e T) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default: // 订阅者积压：丢弃（计数可观测）
			b.dropped.Add(1)
		}
	}
}

// Subscribe 注册订阅者；返回取消函数（ctx 结束也会自动取消）。
// cancel 与 ctx 结束双向收口：任一发生，监听 goroutine 都会退出——
// 此前手动 cancel 只删 channel 不唤醒监听 goroutine，ctx 为 Background 时
// 每个订阅者永久泄漏一个 goroutine + 一条残留 channel。
func (b *Bus[T]) Subscribe(ctx context.Context) (<-chan T, func()) {
	ch := make(chan T, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	stop := make(chan struct{})
	unsubscribe := func() {
		once.Do(func() { close(stop) })
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
	async.GoSafe(func() {
		select {
		case <-ctx.Done():
			unsubscribe()
		case <-stop:
		}
	})
	return ch, unsubscribe
}
