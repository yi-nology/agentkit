// Package progress 泛型事件总线。
// 多订阅者广播，每个订阅者 64 缓冲，满了就丢（观测数据允许有损）。
// 发布永不阻塞（订阅者慢不拖慢主链路）。
package progress

import (
	"context"
	"sync"

	"git.enjoye.top/enjoydream/ekit/concurrency/async"
)

// Bus 泛型多订阅者广播总线。
type Bus[T any] struct {
	mu   sync.Mutex
	subs map[chan T]struct{}
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
		default: // 订阅者积压：丢弃
		}
	}
}

// Subscribe 注册订阅者；返回取消函数（ctx 结束也会自动取消）。
func (b *Bus[T]) Subscribe(ctx context.Context) (<-chan T, func()) {
	ch := make(chan T, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
	async.GoSafe(func() {
		<-ctx.Done()
		cancel()
	})
	return ch, cancel
}
