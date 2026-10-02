// Package worker DB 即队列 worker pool。
// webhook/API 同步路径只落库（pending）即返回，N 个 worker 从表拉 pending（原子抢占）执行。
// 心跳续期 + 过期重置 + 两阶段优雅停机。
package worker

import (
	"context"
	"fmt"
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

	mu         sync.Mutex // 保护 start/stop 生命周期状态
	started    bool
	loopCtx    context.Context
	runCtx     context.Context
	cancelLoop context.CancelFunc
	cancelRun  context.CancelFunc
	done       chan struct{}
	stopOnce   sync.Once
}

func (p *Pool) logger() logx.Logger {
	if p.Log == nil {
		return logx.NewSlogLogger("agentkit-worker")
	}
	return p.Log
}

// Start 启动 N 个常驻 worker（重复调用为 no-op——字段不重置、旧 goroutine 不泄漏）。
func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
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
	// 心跳与 worker 同组等待：Stop 返回后不会再有心跳写库的尾巴
	wg.Add(1)
	async.GoSafe(func() {
		defer wg.Done()
		p.heartbeatLoop(p.loopCtx)
	})
	async.GoSafe(func() {
		wg.Wait()
		close(p.done)
	})
	p.mu.Unlock()
	p.logger().Info("agentkit.worker.started", "workers", p.N)
}

func (p *Pool) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 两个调用各自限时，互不挤占；cancel 经 defer 保证释放
			hbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := p.Queue.TouchRunningHeartbeats(hbCtx); err != nil {
				p.logger().Warn("agentkit.worker.heartbeat_failed", "error", err.Error())
			}
			cancel()
			rsCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
			if n, err := p.Queue.ResetRunningToPending(rsCtx, StaleRunningAfter); err != nil {
				p.logger().Warn("agentkit.worker.stale_reset_failed", "error", err.Error())
			} else if n > 0 {
				p.logger().Warn("agentkit.worker.stale_running_reset", "count", n,
					"stale_after", StaleRunningAfter.String())
			}
			cancel2()
		}
	}
}

// Stop 两阶段停机：取消领取循环 → 等 grace → 硬取消执行。幂等。
// 未启动时调用为 no-op（不消耗停机状态——Stop 在 Start 之前误调用后，
// 真正的 Stop 依然有效）。
func (p *Pool) Stop(grace time.Duration) {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return
	}
	cancelLoop, cancelRun, done, log := p.cancelLoop, p.cancelRun, p.done, p.logger()
	p.mu.Unlock()

	p.stopOnce.Do(func() {
		cancelLoop()
		select {
		case <-done:
		case <-time.After(grace):
			log.Warn("agentkit.worker.stop_grace_exceeded",
				"grace", grace.String(), "action", "硬取消在途任务")
			cancelRun()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
		}
		log.Info("agentkit.worker.stopped")
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
			p.logger().Error("agentkit.worker.claim_failed", "worker", id, "error", err.Error())
			sleepCtx(loopCtx, time.Second)
			continue
		}
		if !ok {
			sleepCtx(loopCtx, 500*time.Millisecond)
			continue
		}
		p.logger().Info("agentkit.worker.claimed", "worker", id, "task", taskID)
		p.runTask(runCtx, id, taskID)
	}
}

// runTask 单任务执行（panic 隔离）：任务 panic 只损失该任务——worker 存活继续
// 拉取，任务由 StaleRunningAfter 心跳过期机制复位重跑；不隔离则 panic 杀死
// 整个 loop goroutine，池静默减员。
func (p *Pool) runTask(runCtx context.Context, id int, taskID string) {
	defer func() {
		if r := recover(); r != nil {
			p.logger().Error("agentkit.worker.task_panic", "worker", id, "task", taskID,
				"panic", fmt.Sprint(r))
		}
	}()
	if err := p.Run(runCtx, taskID); err != nil {
		p.logger().Error("agentkit.worker.run_error", "worker", id, "task", taskID, "error", err.Error())
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
