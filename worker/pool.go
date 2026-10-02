// Package worker DB 即队列 worker pool。
// webhook/API 同步路径只落库（pending）即返回，N 个 worker 从表拉 pending（原子抢占）执行。
// 心跳续期 + 过期重置 + 两阶段优雅停机。
package worker

import (
	"context"
	"sync"
	"time"

	"git.enjoye.top/enjoydream/ekit/concurrency/async"
	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

// 默认参数。
const (
	HeartbeatInterval = 20 * time.Second
	StaleRunningAfter = 90 * time.Second
)

// TaskQueue 任务队列存储接口（调用方实现，如 GORM/MySQL/Redis）。
type TaskQueue interface {
	// ClaimNextPending 原子抢占下一个 pending 任务（pending → running）。
	ClaimNextPending(ctx context.Context) (taskID string, ok bool, err error)
	// TouchRunningHeartbeats 续期本实例 running 任务的心跳。
	TouchRunningHeartbeats(ctx context.Context) error
	// ResetRunningToPending 重置心跳已死的 running 任务回 pending。
	ResetRunningToPending(ctx context.Context, staleAfter time.Duration) (int64, error)
}

// Pool worker 池。
type Pool struct {
	Queue TaskQueue
	// Run 单任务执行体。
	Run func(ctx context.Context, taskID string) error
	N   int
	Log logx.Logger

	loopCtx    context.Context
	runCtx     context.Context
	cancelLoop context.CancelFunc
	cancelRun  context.CancelFunc
	done       chan struct{}
	once       sync.Once
}

// Start 启动 N 个常驻 worker。
func (p *Pool) Start(ctx context.Context) {
	if p.N <= 0 {
		p.N = 1
	}
	p.loopCtx, p.cancelLoop = context.WithCancel(ctx)
	p.runCtx, p.cancelRun = context.WithCancel(ctx)
	p.done = make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < p.N; i++ {
		wg.Add(1)
		async.GoSafe(func() {
			defer wg.Done()
			p.loop(p.loopCtx, p.runCtx, i)
		})
	}
	async.GoSafe(func() {
		wg.Wait()
		close(p.done)
	})
	async.GoSafe(func() { p.heartbeatLoop(p.loopCtx) })
	p.Log.Info("agentkit.worker.started", "workers", p.N)
}

func (p *Pool) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := p.Queue.TouchRunningHeartbeats(hbCtx); err != nil {
				p.Log.Warn("agentkit.worker.heartbeat_failed", "error", err.Error())
			}
			if n, err := p.Queue.ResetRunningToPending(hbCtx, StaleRunningAfter); err != nil {
				p.Log.Warn("agentkit.worker.stale_reset_failed", "error", err.Error())
			} else if n > 0 {
				p.Log.Warn("agentkit.worker.stale_running_reset", "count", n,
					"stale_after", StaleRunningAfter.String())
			}
			cancel()
		}
	}
}

// Stop 两阶段停机：取消领取循环 → 等 grace → 硬取消执行。幂等。
func (p *Pool) Stop(grace time.Duration) {
	p.once.Do(func() {
		if p.cancelLoop == nil {
			return
		}
		p.cancelLoop()
		select {
		case <-p.done:
		case <-time.After(grace):
			p.Log.Warn("agentkit.worker.stop_grace_exceeded",
				"grace", grace.String(), "action", "硬取消在途任务")
			p.cancelRun()
			select {
			case <-p.done:
			case <-time.After(10 * time.Second):
			}
		}
		p.Log.Info("agentkit.worker.stopped")
	})
}

func (p *Pool) loop(loopCtx, runCtx context.Context, id int) {
	for {
		select {
		case <-loopCtx.Done():
			return
		default:
		}
		taskID, ok, err := p.Queue.ClaimNextPending(loopCtx)
		if err != nil {
			if loopCtx.Err() != nil {
				return
			}
			p.Log.Error("agentkit.worker.claim_failed", "worker", id, "error", err.Error())
			sleepCtx(loopCtx, time.Second)
			continue
		}
		if !ok {
			sleepCtx(loopCtx, 500*time.Millisecond)
			continue
		}
		p.Log.Info("agentkit.worker.claimed", "worker", id, "task", taskID)
		if err := p.Run(runCtx, taskID); err != nil {
			p.Log.Error("agentkit.worker.run_error", "worker", id, "task", taskID, "error", err.Error())
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
