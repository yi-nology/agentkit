// Package toolsched 工具并发调度器：一条模型消息里的多个工具调用按注解与依赖
// 调度——依赖就绪即跑、并发受上限约束、独占任务不与他人并发。
//
// 对标 ZCode tool/scheduler.ts + executor/batch-runner.ts：并发判定由注解驱动
// （destructive 一票否决 → concurrentSafe 显式优先 → readOnly 次之 → 具名未声明
// 保守不并行）；注解生产面见 mcp.ToolHints（tools/list 注解归一），本包只消费
// 同构的 Hints 三态声明，不依赖 MCP。
//
// 失败不阻断后续：前序非并发安全工具的本地失败（如 todo 写盘）不应误截断
// 后续任务的执行——后续任务的去留由其自身的执行结果与调用方取消信号决定。
// 这是 ZCode 踩坑后固化的语义（曾因失败跳过全部后续组导致误截断）。
//
// 执行模型（v0.10.32 起）：Execute 按 DependsOn 就绪队列调度 + MaxConcurrency
// 信号量限流，不再按「组」做全屏障——同层可并行任务被切成多组时原先组间串行
// （15 个安全任务 / 上限 10 会跑成 10+5 两段串行）。Schedule.Groups 保留为
// 静态规划视图（分层/切组/独占，可测试可检视），不再驱动执行序。
//
// 用法：
//
//	s := &toolsched.Scheduler{MaxConcurrency: 10}
//	sched, err := s.Schedule(calls)   // 规划分组（可测试的纯函数）
//	results := s.Execute(ctx, tasks)  // 按依赖就绪执行
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

// Schedule 调度结果：静态规划视图（Groups）+ 输入序判定（Items）。
type Schedule struct {
	Items []Item // 输入序（含 CanRunParallel）
	// Groups 规划分组：依赖分层 + 并行切组（≤ MaxConcurrency）+ 非并行独占。
	// 仅供检视/测试/审计；Execute 不按组屏障执行（见包注释「执行模型」）。
	Groups [][]string
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
	items, sorted, err := s.plan(calls)
	if err != nil {
		return nil, err
	}
	return &Schedule{Items: items, Groups: groupByParallel(sorted, s.maxConcurrency())}, nil
}

// plan 校验 + 装配 + 拓扑排序（Schedule/Execute 的共用前半）。层级分组是
// Schedule 的展示面；Execute 的 runner 用依赖计数放行、不消费 Groups——
// 此前 Execute 内部整跑一遍 Schedule 白算分组（收口轮拆分）。
func (s *Scheduler) plan(calls []Call) (items []Item, sorted []*Item, err error) {
	if err := validate(calls); err != nil {
		return nil, nil, err
	}
	items = make([]Item, len(calls))
	byID := make(map[string]*Item, len(calls))
	for i, c := range calls {
		items[i] = Item{Call: c, CanRunParallel: s.canRunInParallel(c)}
		byID[c.ID] = &items[i]
	}
	if sorted, err = topoSort(items, byID); err != nil {
		return nil, nil, err
	}
	return items, sorted, nil
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
	// 层上界一次算好：原先每层循环内 maxLevel 全扫 map，O(L²)
	top := maxLevel(byLevel)
	for level := 0; level <= top; level++ {
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
	// 契约：不得阻塞不返回（长操作应尊重 ctx）；不得 panic——执行体来自外部
	//（MCP 工具适配层等），panic 由调度器隔离转为该任务的 Err（与 worker.Pool
	// 的外部代码隔离同纪律），不击穿进程。
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

// Execute 按依赖就绪执行（引擎见 runner）。结果按输入序返回。
// 前序失败不阻断后续；ctx 取消或 StopAfter 成功后，未启动任务记 Skipped。
func (s *Scheduler) Execute(ctx context.Context, tasks []Task) []Result {
	calls := make([]Call, len(tasks))
	for i, t := range tasks {
		calls[i] = t.Call
	}
	items, _, err := s.plan(calls)
	if err != nil {
		res := make([]Result, len(tasks))
		for i, t := range tasks {
			res[i] = Result{ID: t.ID, Name: t.Name, Err: err}
		}
		return res
	}
	results := make([]Result, len(tasks))
	if len(tasks) == 0 {
		return results
	}
	r := newRunner(ctx, s.maxConcurrency(), tasks, items, results)
	r.run()
	for i := range tasks {
		if !r.filled[i] {
			results[i] = Result{ID: tasks[i].ID, Name: tasks[i].Name, Skipped: true}
		}
	}
	return results
}

// runner 就绪队列执行引擎：依赖计数放行 + 信号量限流 + 独占写锁 + 停轮门。
// 结果按索引独槽写入（validate 保证 ID 唯一），无需结果表互斥。
type runner struct {
	ctx     context.Context
	tasks   []Task
	items   []Item
	results []Result
	filled  []bool

	sem  chan struct{}
	gate sync.RWMutex // 并行持读锁；独占持写锁

	mu         sync.Mutex
	remaining  []int // 未完成前置计数
	dependents [][]int
	ready      []int
	inFlight   int
	stopped    bool
	// 收口状态：inFlight==0 && len(ready)==0 即全部完成（曾并存一个从未 Wait
	// 的 wg——纯死代码，第六轮审计移除）。
	allDone  chan struct{}
	doneOnce sync.Once
}

func newRunner(ctx context.Context, maxC int, tasks []Task, items []Item, results []Result) *runner {
	r := &runner{
		ctx:        ctx,
		tasks:      tasks,
		items:      items,
		results:    results,
		filled:     make([]bool, len(tasks)),
		sem:        make(chan struct{}, maxC),
		remaining:  make([]int, len(tasks)),
		dependents: make([][]int, len(tasks)),
		allDone:    make(chan struct{}),
	}
	// 依赖图（就绪 = 未完成前置计数归零）
	index := make(map[string]int, len(tasks))
	for i := range tasks {
		index[tasks[i].ID] = i
	}
	for i := range tasks {
		for _, dep := range tasks[i].DependsOn {
			j := index[dep]
			r.remaining[i]++
			r.dependents[j] = append(r.dependents[j], i)
		}
	}
	return r
}

func (r *runner) record(i int, res Result) {
	r.results[i] = res
	r.filled[i] = true
}

// launchMore 在 mu 内放行就绪任务。停轮门/独占优先且一次只放一个——
// 同轮就绪的并行任务必须等关门结果，否则 StopAfter 失去截断意义。
func (r *runner) launchMore() {
	if !r.stopped && r.ctx.Err() == nil {
		for k, i := range r.ready {
			if !r.items[i].CanRunParallel || r.tasks[i].StopAfter {
				r.ready = append(r.ready[:k], r.ready[k+1:]...)
				r.inFlight++
				go r.runOne(i)
				return
			}
		}
	}
	for len(r.ready) > 0 {
		i := r.ready[0]
		r.ready = r.ready[1:]
		r.inFlight++
		go r.runOne(i) // 停轮/取消路径由 runOne 记 Skipped，保证依赖链能收口
	}
}

// runOne 单任务：跳过判定 → 并发门 → 执行 → 停轮登记 → 唤醒后继。
func (r *runner) runOne(i int) {
	defer func() {
		r.mu.Lock()
		r.inFlight--
		for _, d := range r.dependents[i] {
			r.remaining[d]--
			if r.remaining[d] == 0 {
				r.ready = append(r.ready, d)
			}
		}
		if r.inFlight == 0 && len(r.ready) == 0 {
			r.doneOnce.Do(func() { close(r.allDone) })
		} else {
			r.launchMore()
		}
		r.mu.Unlock()
	}()

	t := &r.tasks[i]
	r.mu.Lock()
	skip := r.stopped || r.ctx.Err() != nil
	r.mu.Unlock()
	if skip {
		r.record(i, Result{ID: t.ID, Name: t.Name, Skipped: true})
		return
	}
	if t.Run == nil {
		r.record(i, Result{ID: t.ID, Name: t.Name, Err: fmt.Errorf("toolsched: 任务 %q 缺执行体", t.ID)})
		return
	}

	// 并发门：独占先抢写锁（等在途读者收尾并挡住新读者），并行限流+读锁
	if r.items[i].CanRunParallel {
		r.sem <- struct{}{}
		r.gate.RLock()
		defer func() { r.gate.RUnlock(); <-r.sem }()
	} else {
		r.gate.Lock()
		defer r.gate.Unlock()
	}

	out, err := runGuarded(r.ctx, t)
	r.record(i, Result{ID: t.ID, Name: t.Name, Output: out, Err: err})
	if t.StopAfter && err == nil {
		r.mu.Lock()
		r.stopped = true
		r.mu.Unlock()
	}
}

// runGuarded 执行任务并隔离 panic：Task.Run 契约禁止 panic，但执行体来自外部
// （MCP 工具适配层等）不可全信——panic 转为该任务的 Err，不阻断其余任务、
// 不击穿进程（与 worker.Pool 的外部代码隔离同纪律；第六轮审计）。
func runGuarded(ctx context.Context, t *Task) (out string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("toolsched: 任务 %q 执行体 panic: %v", t.ID, p)
		}
	}()
	return t.Run(ctx)
}

// run 点火就绪集并等全部收口。
func (r *runner) run() {
	r.mu.Lock()
	for i := range r.tasks {
		if r.remaining[i] == 0 {
			r.ready = append(r.ready, i)
		}
	}
	if len(r.ready) == 0 {
		// 全部有前置却无就绪 = 理论上被 topoSort 拒掉；防御性收口防挂死
		r.doneOnce.Do(func() { close(r.allDone) })
	} else {
		r.launchMore()
	}
	r.mu.Unlock()
	<-r.allDone
}
