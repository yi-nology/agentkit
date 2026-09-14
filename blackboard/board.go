// Package blackboard 黑板（Blackboard）架构原语：共享状态板 + 多专家轮转读写。
//
// 与 Supervisor（eino adk prebuilt：中心调度员指派任务）不同，Blackboard 没有
// 调度员——每位专家自行观察黑板增量、判断与己相关则贡献条目；循环至无人再写
// 或达轮次上限。适用：多视角分析（多角色并行审阅同一材料、结论互相引用），
// 贡献顺序不可预知、无中心指派逻辑的场景。
//
// 并发语义：Board 线程安全；Convene 每轮串行调用各专家（专家内可自行并发）。
package blackboard

import (
	"sync"
	"time"
)

// Entry 黑板条目。
type Entry struct {
	Seq     int64     `json:"seq"`     // 单调递增（增量观察游标）
	Author  string    `json:"author"`  // 贡献者（专家名或 "user"）
	Kind    string    `json:"kind"`    // 条目类型（如 "material"/"finding"/"verdict"；词表由调用方约定）
	Content string    `json:"content"` // 内容
	At      time.Time `json:"at"`
}

// Board 共享黑板（线程安全；内存态——持久化由调用方按 Entries 快照自行落库）。
type Board struct {
	mu      sync.Mutex
	seq     int64
	entries []Entry
}

// NewBoard 创建黑板。
func NewBoard() *Board { return &Board{} }

// Seed 写入初始材料（author="user"）。
func (b *Board) Seed(kind, content string) Entry {
	return b.Write("user", kind, content)
}

// Write 追加条目，返回带序号的完整条目。
func (b *Board) Write(author, kind, content string) Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	e := Entry{Seq: b.seq, Author: author, Kind: kind, Content: content, At: time.Now().UTC()}
	b.entries = append(b.entries, e)
	return e
}

// Entries 全量快照。
func (b *Board) Entries() []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Entry(nil), b.entries...)
}

// EntriesAfter 增量观察：返回 seq > after 的条目（专家"自上次读过之后"的新内容）。
func (b *Board) EntriesAfter(after int64) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Entry
	for _, e := range b.entries {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out
}

// Latest 读某 Kind 的最新条目。
func (b *Board) Latest(kind string) (Entry, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.entries) - 1; i >= 0; i-- {
		if b.entries[i].Kind == kind {
			return b.entries[i], true
		}
	}
	return Entry{}, false
}
