package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

// memLeaseStore 内存租约存储（单测；生产实现 = SQL 条件 UPSERT）。
type memLeaseStore struct {
	mu        sync.Mutex
	holder    map[string]string
	expiresAt map[string]time.Time
	now       func() time.Time
}

func newMemLeaseStore() *memLeaseStore {
	return &memLeaseStore{holder: map[string]string{}, expiresAt: map[string]time.Time{}, now: time.Now}
}

func (m *memLeaseStore) TryAcquire(_ context.Context, key, holder string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if h, ok := m.holder[key]; ok && h != holder && now.Before(m.expiresAt[key]) {
		return false, nil
	}
	m.holder[key] = holder
	m.expiresAt[key] = now.Add(ttl)
	return true, nil
}

func (m *memLeaseStore) Release(_ context.Context, key, holder string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.holder[key] == holder {
		delete(m.holder, key)
		delete(m.expiresAt, key)
	}
	return nil
}

func TestLeaderElectorGainedAndHandover(t *testing.T) {
	store := newMemLeaseStore()
	var gainedA, lostA int
	a := NewLeaderElector(store, "poller", "A", 200*time.Millisecond, 50*time.Millisecond,
		WithOnGained(func() { gainedA++ }), WithOnLost(func() { lostA++ }),
		WithLeaderLogger(logx.NewSlogLogger("test")))
	ctx, cancel := context.WithCancel(context.Background())
	a.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !a.IsLeader() {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.IsLeader() {
		t.Fatal("A 应获得领导权")
	}

	// B 加入：租约未过期不得抢走
	b := NewLeaderElector(store, "poller", "B", 200*time.Millisecond, 20*time.Millisecond,
		WithLeaderLogger(logx.NewSlogLogger("test")))
	b.Start(ctx)
	defer b.Stop()
	time.Sleep(150 * time.Millisecond)
	if b.IsLeader() || !a.IsLeader() {
		t.Fatalf("租约在途 B 不得当选: a=%v b=%v", a.IsLeader(), b.IsLeader())
	}

	// A 停止让位 → B 应当选
	a.Stop()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !b.IsLeader() {
		time.Sleep(10 * time.Millisecond)
	}
	if !b.IsLeader() {
		t.Fatal("A 让位后 B 应当选")
	}
	if lostA != 0 {
		t.Fatalf("Stop 主动让位不应触发 onLost: %d", lostA)
	}
	cancel()
}

func TestLeaderElectorExpiryHandover(t *testing.T) {
	store := newMemLeaseStore()
	now := time.Now()
	store.now = func() time.Time { return now } // 冻结时钟，手动推进

	a := NewLeaderElector(store, "poller", "A", time.Minute, 10*time.Millisecond,
		WithLeaderLogger(logx.NewSlogLogger("test")))
	ctx := context.Background()
	a.Start(ctx)
	defer a.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !a.IsLeader() {
		time.Sleep(5 * time.Millisecond)
	}
	if !a.IsLeader() {
		t.Fatal("A 应当选")
	}

	// A 失联：时钟跳过 ttl，B 竞争应换主（A 不再续约后其 IsLeader 在下次 tick 翻转）
	now = now.Add(2 * time.Minute)
	b := NewLeaderElector(store, "poller", "B", time.Minute, 10*time.Millisecond,
		WithLeaderLogger(logx.NewSlogLogger("test")))
	b.Start(ctx)
	defer b.Stop()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !b.IsLeader() {
		time.Sleep(5 * time.Millisecond)
	}
	if !b.IsLeader() {
		t.Fatal("租约过期后 B 应换主当选")
	}
}
