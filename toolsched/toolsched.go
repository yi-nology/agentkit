// Package toolsched 工具并发调度器：一条模型消息里的多个工具调用按注解与依赖
// 分组调度——组内并发（上限约束）、组间有序。
//
// 对标 ZCode tool/scheduler.ts + executor/batch-runner.ts：并发判定由注解驱动
// （destructive 一票否决 → concurrentSafe 显式优先 → readOnly 次之 → 具名未声明
// 保守不并行）；注解生产面见 mcp.ToolHints（tools/list 注解归一），本包只消费
// 同构的 Hints 三态声明，不依赖 MCP。
//
// 失败不阻断后续组：前序非并发安全工具的本地失败（如 todo 写盘）不应误截断
// 后续任务的执行——后续任务的去留由其自身的执行结果与调用方取消信号决定。
// 这是 ZCode 踩坑后固化的语义（曾因失败跳过全部后续组导致误截断）。
//
// 用法：
//
//	s := &toolsched.Scheduler{MaxConcurrency: 10}
//	sched, err := s.Schedule(calls)   // 分组（可测试的纯函数）
//	results := s.Execute(ctx, tasks)  // 按调度执行
package toolsched

import (
	"context"
	"fmt"
	"sync"
)

// DefaultMaxConcurrency 组大小缺省上限（对标 ZCode DEFAULT_MAX_CONCURRENCY）。
const DefaultMaxConcurrency = 10

// Hints 工具注解（三态：nil=未声明）。与 mcp.ToolHints 字段同构——非 MCP 工具
// 按同形态声明即可入调度。
type Hints struct {
	ReadOnly    *bool // 只读（无副作用）
	Idempotent  *bool // 幂等/并发安全（显式声明优先于 ReadOnly 推定）
	Destructive *bool // 破坏性（一票否决并行）
}

// Call 单次调用声明。
type Call struct {
	ID        string   // 调用唯一标识（模型侧 tool call id）
	Name      string   // 工具名（可空；匿名调用无从按名兜底）
	DependsOn []string // 显式前置（全部完成后才可执行）
	Hints     Hints
	// StopAfter 声明「本调用成功即停轮」（对标 ZCode plan 批准类
	// turnControl.stopTurnAfterResult）：成功后剩余组取消，未执行调用以
	// Skipped 结果呈现；失败不触发（停轮是成功语义）。停轮门独占单例组——
	// 与后续调用同组并发会让取消失去意义。
	StopAfter bool
}

// Item 调度产物项（输入序，含并行判定）。
type Item struct {
	Call
	CanRunParallel bool
}

// Schedule 调度结果：分组可执行（Groups），也可仅检视（Items）。
type Schedule struct {
	Items  []Item     // 输入序
	Groups [][]string // 并行组：组内可并发、组间有序（组大小 ≤ MaxConcurrency）
}

// Scheduler 调度器。零值可用（MaxConcurrency 缺省 DefaultMaxConcurrency）。
type Scheduler struct {
	// MaxConcurrency 单组大小上限（≤0 → DefaultMaxConcurrency）。
	MaxConcurrency int
	// ReadOnlyTools 按名兜底表：未声明注解的具名工具查此表判只读
	// （如 bianque 侧内置只读工具清单）；查不到保守不并行。
	ReadOnlyTools map[string]bool
}

// Schedule 计算调度（纯函数，不执行）。ID 重复/依赖悬空/依赖环均报错——
// 调用图来自模型输出与装配配置，越界即确定性错误而非静默容错。
func (s *Scheduler) Schedule(calls []Call) (*Schedule, error) {
	if err := validate(calls); err != nil {
		return nil, err
	}
	items := make([]Item, len(calls))
	byID := make(map[string]*Item, len(calls))
	for i, c := range calls {
		items[i] = Item{Call: c, CanRunParallel: s.canRunInParallel(c)}
		byID[c.ID] = &items[i]
	}
	sorted, err := topoSort(items, byID)
	if err != nil {
		return nil, err
	}
	return &Schedule{Items: items, Groups: groupByParallel(sorted, s.maxConcurrency())}, nil
}

func (s *Scheduler) maxConcurrency() int {
	if s.MaxConcurrency > 0 {
		return s.MaxConcurrency
	}
	return DefaultMaxConcurrency
}

// canRunInParallel 并发判定（对标 ZCode canRunInParallel 决策链）：
// 无名且无任何注解声明 → 并行（匿名无从判定）；destructive → 否决；
// idempotent 显式优先（≈ZCode concurrentSafe）；readOnly → 放行；
// 具名未声明 → 查 ReadOnlyTools 兜底表，缺省保守不并行。
func (s *Scheduler) canRunInParallel(c Call) bool {
	if c.StopAfter {
		return false // 停轮门独占组
	}
	declared := c.Hints.ReadOnly != nil || c.Hints.Idempotent != nil || c.Hints.Destructive != nil
	if c.Name == "" && !declared {
		return true
	}
	if b := c.Hints.Destructive; b != nil && *b {
		return false
	}
	if b := c.Hints.Idempotent; b != nil {
		return *b
	}
	if b := c.Hints.ReadOnly; b != nil {
		return *b
	}
	return s.ReadOnlyTools[c.Name]
}

func validate(calls []Call) error {
	seen := make(map[string]bool, len(calls))
	for i, c := range calls {
		if c.ID == "" {
			return fmt.Errorf("toolsched: 调用[%d] 缺 ID", i)
		}
		if seen[c.ID] {
			return fmt.Errorf("toolsched: 调用 ID 重复 %q", c.ID)
		}
		seen[c.ID] = true
	}
	for _, c := range calls {
		for _, dep := range c.DependsOn {
			if !seen[dep] {
				return fmt.Errorf("toolsched: 调用 %q 依赖不存在的 %q", c.ID, dep)
			}
		}
	}
	return nil
}

// topoSort Kahn 拓扑排序（输入序播种，输出确定）。
func topoSort(items []Item, byID map[string]*Item) ([]*Item, error) {
	inDegree := make(map[string]int, len(items))
	dependents := make(map[string][]string, len(items))
	for _, it := range items {
		inDegree[it.ID] = len(it.DependsOn)
		for _, dep := range it.DependsOn {
			dependents[dep] = append(dependents[dep], it.ID)
		}
	}
	queue := make([]string, 0, len(items))
	for _, it := range items {
		if inDegree[it.ID] == 0 {
			queue = append(queue, it.ID)
		}
	}
	sorted := make([]*Item, 0, len(items))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		sorted = append(sorted, byID[id])
		for _, next := range dependents[id] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(sorted) != len(items) {
		remaining := make([]string, 0)
		for _, it := range items {
			if inDegree[it.ID] > 0 {
				remaining = append(remaining, it.ID)
			}
		}
		return nil, fmt.Errorf("toolsched: 依赖环（未排入: %v）", remaining)
	}
	return sorted, nil
}

// groupByParallel 层级分组：level = max(依赖 level)+1；层内可并行者聚组
// （满 MaxConcurrency 即切），非并行者独占单例组（前后切组）。
func groupByParallel(sorted []*Item, maxConcurrency int) [][]string {
	levels := make(map[string]int, len(sorted))
	byLevel := make(map[int][]*Item)
	for _, it := range sorted {
		level := 0
		for _, dep := range it.DependsOn {
			if l := levels[dep] + 1; l > level {
				level = l
			}
		}
		levels[it.ID] = level
		byLevel[level] = append(byLevel[level], it)
	}
	var groups [][]string
	for level := 0; level <= maxLevel(byLevel); level++ {
		items := byLevel[level]
		if len(items) == 0 {
			continue
		}
		var parallel []string
		flush := func() {
			if len(parallel) > 0 {
				groups = append(groups, parallel)
				parallel = nil
			}
		}
		for _, it := range items {
			if !it.CanRunParallel {
				flush()
				groups = append(groups, []string{it.ID})
				continue
			}
			if len(parallel) >= maxConcurrency {
				flush()
			}
			parallel = append(parallel, it.ID)
		}
		flush()
	}
	return groups
}

func maxLevel(byLevel map[int][]*Item) int {
	max := -1
	for l := range byLevel {
		if l > max {
			max = l
		}
	}
	return max
}

// Task 可执行任务：声明（Call）+ 执行体。
type Task struct {
	Call
	// Run 执行体（nil 视为任务错误，不阻断其余任务）。
	Run func(ctx context.Context) (string, error)
}

// Result 单任务结果（输入序返回，与完成序无关）。
type Result struct {
	ID      string
	Name    string
	Output  string
	Err     error
	Skipped bool // 因 StopAfter/ctx 取消未执行
}

// Execute 按调度执行：组内并发、组间顺序；结果按输入序返回。
// 组内失败不阻断后续组；ctx 取消时在途任务透传取消信号、剩余组跳过（Skipped）。
func (s *Scheduler) Execute(ctx context.Context, tasks []Task) []Result {
	calls := make([]Call, len(tasks))
	for i, t := range tasks {
		calls[i] = t.Call
	}
	sched, err := s.Schedule(calls)
	if err != nil {
		res := make([]Result, len(tasks))
		for i, t := range tasks {
			res[i] = Result{ID: t.ID, Name: t.Name, Err: err}
		}
		return res
	}
	byID := make(map[string]*Task, len(tasks))
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}
	results := make(map[string]Result, len(tasks))
	var mu sync.Mutex
	record := func(r Result) {
		mu.Lock()
		results[r.ID] = r
		mu.Unlock()
	}

	// skipRest 把指定组起的所有未完成任务记为 Skipped（含嵌套展平）。
	skipRest := func(fromGroup int) {
		for _, group := range sched.Groups[fromGroup:] {
			for _, id := range group {
				if _, done := results[id]; !done {
					record(Result{ID: id, Name: byID[id].Name, Skipped: true})
				}
			}
		}
	}

exec:
	for gi, group := range sched.Groups {
		if ctx.Err() != nil {
			skipRest(gi)
			break
		}
		var wg sync.WaitGroup
		for _, id := range group {
			t := byID[id]
			if t == nil {
				continue
			}
			wg.Add(1)
			go func(t *Task) {
				defer wg.Done()
				if t.Run == nil {
					record(Result{ID: t.ID, Name: t.Name, Err: fmt.Errorf("toolsched: 任务 %q 缺执行体", t.ID)})
					return
				}
				out, err := t.Run(ctx)
				record(Result{ID: t.ID, Name: t.Name, Output: out, Err: err})
			}(t)
		}
		wg.Wait()

		// 停轮判定：本组内有 StopAfter 任务成功 → 剩余组全部跳过
		for _, id := range group {
			t := byID[id]
			if t == nil || !t.StopAfter {
				continue
			}
			if r := results[id]; r.Err == nil && !r.Skipped {
				skipRest(gi + 1)
				break exec
			}
		}
	}
	out := make([]Result, len(tasks))
	for i, t := range tasks {
		out[i] = results[t.ID]
	}
	return out
}
