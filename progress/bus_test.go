package progress

import (
	"context"
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
