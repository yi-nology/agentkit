package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

// mockQueue 实现 TaskQueue 接口用于测试。
type mockQueue struct {
	mu      sync.Mutex
	pending []string
	claimed []string
	heartbeatCalls int32
}

func newMockQueue(taskIDs ...string) *mockQueue {
	return &mockQueue{pending: taskIDs}
}

func (q *mockQueue) ClaimNextPending(_ context.Context) (string, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return "", false, nil
	}
	id := q.pending[0]
	q.pending = q.pending[1:]
	q.claimed = append(q.claimed, id)
	return id, true, nil
}

func (q *mockQueue) TouchRunningHeartbeats(_ context.Context) error {
	atomic.AddInt32(&q.heartbeatCalls, 1)
	return nil
}

func (q *mockQueue) ResetRunningToPending(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func TestPoolClaimsAndRuns(t *testing.T) {
	q := newMockQueue("task-1", "task-2", "task-3")
	var mu sync.Mutex
	var ran []string

	p := &Pool{
		Queue: q,
		Run: func(_ context.Context, taskID string) error {
			mu.Lock()
			ran = append(ran, taskID)
			mu.Unlock()
			return nil
		},
		N:   1,
		Log: logx.NewSlogLogger("test"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	// 等待所有任务执行完
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		done := len(ran)
		mu.Unlock()
		if done >= 3 {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("超时：只完成了 %d/3 个任务", done)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	cancel()
	p.Stop(time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 3 {
		t.Fatalf("应完成 3 个任务，实际 %d", len(ran))
	}
}

func TestPoolMultipleWorkers(t *testing.T) {
	// 5 个任务，3 个 worker
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = "task-" + string(rune('A'+i))
	}
	q := newMockQueue(ids...)
	var count atomic.Int32

	p := &Pool{
		Queue: q,
		Run: func(_ context.Context, _ string) error {
			time.Sleep(50 * time.Millisecond)
			count.Add(1)
			return nil
		},
		N:   3,
		Log: logx.NewSlogLogger("test"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	deadline := time.After(5 * time.Second)
	for count.Load() < 5 {
		select {
		case <-deadline:
			cancel()
			t.Fatalf("超时：完成 %d/5", count.Load())
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	cancel()
	p.Stop(time.Second)
}

func TestPoolStopGraceful(t *testing.T) {
	q := newMockQueue("task-1")
	started := make(chan struct{})
	var done atomic.Bool

	p := &Pool{
		Queue: q,
		Run: func(ctx context.Context, _ string) error {
			close(started)
			select {
			case <-ctx.Done():
				done.Store(true)
			case <-time.After(10 * time.Second):
				done.Store(true)
			}
			return nil
		},
		N:   1,
		Log: logx.NewSlogLogger("test"),
	}
	ctx := context.Background()
	p.Start(ctx)

	<-started
	// 短 grace：不应取消（任务自然完成前 grace 到期）
	p.Stop(10 * time.Millisecond)

	// 硬取消后任务应收到 ctx 取消
	time.Sleep(100 * time.Millisecond)
	if !done.Load() {
		t.Fatal("Stop 应最终取消在途任务")
	}
}

func TestPoolEmptyQueue(t *testing.T) {
	q := newMockQueue() // 空队列
	var count atomic.Int32

	p := &Pool{
		Queue: q,
		Run: func(_ context.Context, _ string) error {
			count.Add(1)
			return nil
		},
		N:   1,
		Log: logx.NewSlogLogger("test"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	time.Sleep(200 * time.Millisecond) // 空转一段时间
	cancel()
	p.Stop(time.Second)

	if count.Load() != 0 {
		t.Fatal("空队列不应执行任何任务")
	}
}

func TestPoolZeroWorkersDefaults(t *testing.T) {
	q := newMockQueue("task-1")
	p := &Pool{
		Queue: q,
		Run:   func(_ context.Context, _ string) error { return nil },
		N:     0, // 应默认为 1
		Log:   logx.NewSlogLogger("test"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	cancel()
	p.Stop(time.Second)
	// 不应 panic
}
