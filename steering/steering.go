// Package steering 运行中转向队列：turn 执行中用户插入的新输入按投递语义
// 分流——guide（下一个模型请求边界前注入，不打断当前工具执行）与 queue
// （本 turn 结束后作为下一轮输入）。对标 ZCode steering.ts 的 pendingInputs
// 形态；不含 turn 机集成（那是运行时的事），只提供线程安全的队列原语。
//
// 关键语义（ZCode 踩坑固化的）：
//   - guide 输入不丢：模型执行窗口已关闭时（Drain 后未消费）fallback 为
//     queue 语义，由调用方在 turn 末尾统一取走；
//   - 同一 turn 内 guide 注入后续跑而非新开 turn，保持上下文连续。
package steering

import "sync"

// Delivery 投递语义。
type Delivery string

const (
	// Guide 下一模型请求边界注入（不中断当前工具执行）。
	Guide Delivery = "guide"
	// Queued 本 turn 结束后作为下一轮输入。
	Queued Delivery = "queue"
)

// Input 一条待投递的用户输入。
type Input struct {
	Text     string
	Delivery Delivery
}

// Queue 待投递输入队列（并发安全）。零值可用。
type Queue struct {
	mu      sync.Mutex
	pending []Input
}

// Steer 投递 guide 输入（模型请求边界注入）。
func (q *Queue) Steer(text string) {
	q.push(Input{Text: text, Delivery: Guide})
}

// Enqueue 投递 queued 输入（turn 结束后消费）。
func (q *Queue) Enqueue(text string) {
	q.push(Input{Text: text, Delivery: Queued})
}

func (q *Queue) push(in Input) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, in)
}

// DrainGuides 取走全部 guide 输入（模型请求边界调用）。模型执行窗口关闭时
// guide 降级为 queued——不丢输入。
func (q *Queue) DrainGuides() []Input {
	q.mu.Lock()
	defer q.mu.Unlock()
	var guides []Input
	for _, in := range q.pending {
		if in.Delivery == Guide {
			guides = append(guides, in)
		}
	}
	q.pending = filterPending(q.pending, func(in Input) bool { return in.Delivery != Guide })
	return guides
}

// DequeueQueued 取一条 queued 输入（FIFO）。队列空返回 ok=false。
func (q *Queue) DequeueQueued() (Input, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, in := range q.pending {
		if in.Delivery == Queued {
			q.pending = append(q.pending[:i:i], q.pending[i+1:]...)
			return in, true
		}
	}
	return Input{}, false
}

// DemoteGuidesToQueued 把全部 guide 降级为 queued（turn 提前收尾/执行窗口
// 关闭时调用——guide 不丢，转排队）。
func (q *Queue) DemoteGuidesToQueued() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, in := range q.pending {
		if in.Delivery == Guide {
			q.pending[i].Delivery = Queued
		}
	}
}

// Len 当前待投递总数（观测用）。
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

func filterPending(pending []Input, keep func(Input) bool) []Input {
	out := pending[:0:0]
	for _, in := range pending {
		if keep(in) {
			out = append(out, in)
		}
	}
	return out
}
