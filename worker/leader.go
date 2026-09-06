// worker 包选主原语：多副本部署时"只能跑一份"的组件（出站轮询、定时清理、
// 通知汇总等）经租约竞争，与 DB 即队列的 worker 池组成完整的横向扩展故事——
// 任务面靠 ClaimNextPending 原子抢占天然分片，控制面靠租约选主收敛为单份。
//
// 选主语义：任期 = ttl；持有者失联（进程崩溃）后 ≤ttl 自动换主；
// 优雅停机主动让位（Release）；脑裂窗口 ≤ 一个轮询间隔，调用方组件需容忍
// 短暂双主（Argus 轮询任务源幂等双查，双跑无害只浪费）。
package worker

import (
	"context"
	"sync"
	"time"

	"git.enjoye.top/enjoydream/ekit/concurrency/async"
	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

// LeaseStore 租约存储接口（调用方按存储实现：SQL 一行条件 UPSERT 即可）。
// 语义必须原子：TryAcquire 的"读-判-写"不允许并发穿插（SQL 用
// `UPDATE ... WHERE key=? AND (holder=? OR expires_at<?)` 条件更新实现）。
type LeaseStore interface {
	// TryAcquire 原子获取或续约租约。key 空闲/已过期/持有者就是 holder →
	// 置 holder 并顺延 ttl，返回 true；他人持有未过期 → 返回 false。
	TryAcquire(ctx context.Context, key, holder string, ttl time.Duration) (bool, error)
	// Release 释放租约（仅持有者本人生效；优雅停机让位）。
	// 非持有者或 key 不存在 → no-op 返回 nil。
	Release(ctx context.Context, key, holder string) error
}

// LeaderElector 租约选主器：周期 TryAcquire 竞争，IsLeader 反映最近一次结果，
// 角色翻转时触发 onGained/onLost 回调。
type LeaderElector struct {
	store    LeaseStore
	key      string
	holder   string
	ttl      time.Duration
	interval time.Duration
	onGained func()
	onLost   func()
	log      logx.Logger

	mu     sync.Mutex
	leader bool
	stop   chan struct{}
	once   sync.Once
}

// LeaderOption 选主器可选项。
type LeaderOption func(*LeaderElector)

// WithOnGained 获得领导权回调（首次当选与失而复得均触发）。
func WithOnGained(f func()) LeaderOption { return func(e *LeaderElector) { e.onGained = f } }

// WithOnLost 失去领导权回调（续约失败；Stop 主动让位不触发——那是计划内交接）。
func WithOnLost(f func()) LeaderOption { return func(e *LeaderElector) { e.onLost = f } }

// WithLeaderLogger 日志（nil 安全，缺省静默）。
func WithLeaderLogger(log logx.Logger) LeaderOption {
	return func(e *LeaderElector) {
		if log != nil {
			e.log = log
		}
	}
}

// NewLeaderElector 创建选主器。interval 建议 ttl/3（失主换主延迟 ≈ ttl + interval）。
func NewLeaderElector(store LeaseStore, key, holder string, ttl, interval time.Duration,
	opts ...LeaderOption) *LeaderElector {
	e := &LeaderElector{
		store:    store,
		key:      key,
		holder:   holder,
		ttl:      ttl,
		interval: interval,
		log:      logx.NewSlogLogger("agentkit-leader"),
		stop:     make(chan struct{}),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Start 启动竞选循环：立即竞争一次（缩短冷启动等待），此后按 interval 续约。
// ctx 取消或 Stop() 退出，退出时主动让位。
func (e *LeaderElector) Start(ctx context.Context) {
	async.GoSafe(func() {
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		e.tick(ctx)
		for {
			select {
			case <-ctx.Done():
				e.Stop()
				return
			case <-e.stop:
				return
			case <-ticker.C:
				e.tick(ctx)
			}
		}
	})
}

// IsLeader 当前是否持有领导权（最近一次续约结果）。
func (e *LeaderElector) IsLeader() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.leader
}

// Stop 停止竞选并主动让位（幂等；优雅停机的计划内交接，不触发 onLost）。
func (e *LeaderElector) Stop() {
	e.once.Do(func() {
		close(e.stop)
		e.mu.Lock()
		wasLeader := e.leader
		e.leader = false
		e.mu.Unlock()
		if wasLeader {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := e.store.Release(ctx, e.key, e.holder); err != nil && e.log != nil {
				e.log.Warn("agentkit.leader.release_failed", "key", e.key, "error", err.Error())
			}
			e.log.Info("agentkit.leader.yielded", "key", e.key, "holder", e.holder)
		}
	})
}

func (e *LeaderElector) tick(ctx context.Context) {
	ok, err := e.store.TryAcquire(ctx, e.key, e.holder, e.ttl)
	if err != nil {
		// 存储抖动：保守认为失主（回调方组件按非 leader 收敛），下个周期重试
		e.setLeader(false)
		e.log.Warn("agentkit.leader.acquire_failed", "key", e.key, "error", err.Error())
		return
	}
	e.setLeader(ok)
}

func (e *LeaderElector) setLeader(v bool) {
	e.mu.Lock()
	prev := e.leader
	e.leader = v
	e.mu.Unlock()
	if v && !prev {
		e.log.Info("agentkit.leader.gained", "key", e.key, "holder", e.holder, "ttl", e.ttl.String())
		if e.onGained != nil {
			e.onGained()
		}
	} else if !v && prev {
		e.log.Warn("agentkit.leader.lost", "key", e.key, "holder", e.holder)
		if e.onLost != nil {
			e.onLost()
		}
	}
}
