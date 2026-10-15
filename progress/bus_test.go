package progress

import (
	"context"
	"runtime"
	"testing"
	"time"
)

type testEvent struct {
	Type string
	ID   string
}

func TestBusFanOut(t *testing.T) {
	b := NewBus[testEvent]()
	ctx := context.Background()
	ch1, c1 := b.Subscribe(ctx)
	ch2, c2 := b.Subscribe(ctx)
	defer c1()
	defer c2()

	b.Publish(testEvent{Type: "created", ID: "t1"})
	for _, ch := range []<-chan testEvent{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Type != "created" || ev.ID != "t1" {
				t.Fatalf("event 不符: %+v", ev)
			}
		case <-time.After(time.Second):
			t.Fatal("订阅者未收到事件")
		}
	}
}

func TestBusDropOnSlow(t *testing.T) {
	b := NewBus[testEvent]()
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()
	_ = ch // 有损丢弃：只断言发布端不阻塞

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish(testEvent{Type: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish 被慢订阅者阻塞")
	}
}

func TestBusNilSafe(t *testing.T) {
	var b *Bus[testEvent]
	b.Publish(testEvent{Type: "x"}) // 不应 panic
}

func TestSubscribeCtxCleanup(t *testing.T) {
	b := NewBus[testEvent]()
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := b.Subscribe(ctx)
	cancel()
	// ctx 取消后订阅自动注销
	time.Sleep(10 * time.Millisecond)
	// 发一个事件，不应有任何订阅者收到
	b.Publish(testEvent{Type: "x"})
	select {
	case <-ch:
		t.Fatal("ctx 取消后不应收到事件")
	default:
	}
}

func TestBusGenericTypes(t *testing.T) {
	// 验证不同泛型类型独立工作
	stringBus := NewBus[string]()
	intBus := NewBus[int]()

	strCh, c1 := stringBus.Subscribe(context.Background())
	intCh, c2 := intBus.Subscribe(context.Background())
	defer c1()
	defer c2()

	stringBus.Publish("hello")
	intBus.Publish(42)

	select {
	case v := <-strCh:
		if v != "hello" {
			t.Fatalf("string bus 收到 %q", v)
		}
	case <-time.After(time.Second):
		t.Fatal("string bus 未收到")
	}
	select {
	case v := <-intCh:
		if v != 42 {
			t.Fatalf("int bus 收到 %d", v)
		}
	case <-time.After(time.Second):
		t.Fatal("int bus 未收到")
	}
}

func TestSubscribeManualCancelReleasesGoroutine(t *testing.T) {
	// 回归：Background ctx + 手动 cancel 不得泄漏监听 goroutine
	//（此前 cancel 只删 channel，阻塞在 ctx.Done() 的 goroutine 永久残留）
	runtime.GC()
	base := runtime.NumGoroutine()

	b := NewBus[int]()
	cancels := make([]func(), 0, 100)
	for i := 0; i < 100; i++ {
		_, cancel := b.Subscribe(context.Background())
		cancels = append(cancels, cancel)
	}
	for _, c := range cancels {
		c()
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= base+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("cancel 后 goroutine 未回收: base=%d now=%d", base, runtime.NumGoroutine())
}

func TestPublishDropCounted(t *testing.T) {
	// 订阅者积压丢弃不再完全静默：Dropped() 计数可见（进度条停在中间态可归因）
	b := NewBus[int]()
	ch, cancel := b.Subscribe(context.Background())
	defer cancel()
	<-time.After(10 * time.Millisecond) // 等订阅生效

	for i := 0; i < 200; i++ { // 64 缓冲必然溢出
		b.Publish(i)
	}
	if d := b.Dropped(); d == 0 {
		t.Fatal("积压丢弃应被计数")
	}
	// 不消费 ch，直接校验计数即可
	_ = ch
}
