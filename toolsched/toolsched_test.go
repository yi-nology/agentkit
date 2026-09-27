package toolsched

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func b(v bool) *bool { return &v }

func TestCanRunInParallelDecisionChain(t *testing.T) {
	s := &Scheduler{ReadOnlyTools: map[string]bool{"grep": true}}
	cases := []struct {
		name string
		c    Call
		want bool
	}{
		{"匿名无声明→并行", Call{ID: "1"}, true},
		{"destructive 否决", Call{ID: "1", Name: "rm", Hints: Hints{Destructive: b(true), ReadOnly: b(true)}}, false},
		{"idempotent 显式优先于 readOnly=false", Call{ID: "1", Name: "x", Hints: Hints{Idempotent: b(true), ReadOnly: b(false)}}, true},
		{"idempotent=false 不并行", Call{ID: "1", Name: "x", Hints: Hints{Idempotent: b(false), ReadOnly: b(true)}}, false},
		{"readOnly 放行", Call{ID: "1", Name: "x", Hints: Hints{ReadOnly: b(true)}}, true},
		{"readOnly=false 不并行", Call{ID: "1", Name: "x", Hints: Hints{ReadOnly: b(false)}}, false},
		{"具名未声明查兜底表命中", Call{ID: "1", Name: "grep"}, true},
		{"具名未声明兜底表未命中→保守不并行", Call{ID: "1", Name: "deploy"}, false},
	}
	for _, tc := range cases {
		if got := s.canRunInParallel(tc.c); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestScheduleDependencyOrdering(t *testing.T) {
	s := &Scheduler{}
	sched, err := s.Schedule([]Call{
		{ID: "c", DependsOn: []string{"b"}, Name: "n3"},
		{ID: "a", Name: "n1"},
		{ID: "b", DependsOn: []string{"a"}, Name: "n2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// a → b → c 三个层级；具名未声明保守不并行 → 各自单例组
	want := [][]string{{"a"}, {"b"}, {"c"}}
	if !equalGroups(sched.Groups, want) {
		t.Fatalf("依赖分层错误: %v", sched.Groups)
	}
}

func TestScheduleParallelGroupSplit(t *testing.T) {
	s := &Scheduler{MaxConcurrency: 2}
	calls := []Call{
		{ID: "1", Hints: Hints{ReadOnly: b(true)}},
		{ID: "2", Hints: Hints{ReadOnly: b(true)}},
		{ID: "3", Hints: Hints{ReadOnly: b(true)}},
		{ID: "4", Hints: Hints{ReadOnly: b(true)}},
		{ID: "5", Hints: Hints{ReadOnly: b(true)}},
	}
	sched, err := s.Schedule(calls)
	if err != nil {
		t.Fatal(err)
	}
	// 无依赖全 0 层；上限 2 → 2+2+1
	want := [][]string{{"1", "2"}, {"3", "4"}, {"5"}}
	if !equalGroups(sched.Groups, want) {
		t.Fatalf("上限切组错误: %v", sched.Groups)
	}
}

func TestScheduleNonParallelBreaksGroup(t *testing.T) {
	s := &Scheduler{MaxConcurrency: 10}
	calls := []Call{
		{ID: "1", Hints: Hints{ReadOnly: b(true)}},
		{ID: "2", Hints: Hints{Destructive: b(true)}}, // 非并行独占
		{ID: "3", Hints: Hints{ReadOnly: b(true)}},
	}
	sched, err := s.Schedule(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"1"}, {"2"}, {"3"}}
	if !equalGroups(sched.Groups, want) {
		t.Fatalf("非并行切组错误: %v", sched.Groups)
	}
}

func TestScheduleSameLevelMixed(t *testing.T) {
	// 同层内：并行者聚组，非并行者独占且不打散前后并行段
	s := &Scheduler{}
	calls := []Call{
		{ID: "r1", Hints: Hints{ReadOnly: b(true)}},
		{ID: "w1", Hints: Hints{ReadOnly: b(false)}},
		{ID: "r2", Hints: Hints{ReadOnly: b(true)}},
		{ID: "r3", Hints: Hints{ReadOnly: b(true)}},
	}
	sched, err := s.Schedule(calls)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"r1"}, {"w1"}, {"r2", "r3"}}
	if !equalGroups(sched.Groups, want) {
		t.Fatalf("同层混排错误: %v", sched.Groups)
	}
}

func TestScheduleErrors(t *testing.T) {
	s := &Scheduler{}
	if _, err := s.Schedule([]Call{{ID: ""}}); err == nil || !strings.Contains(err.Error(), "缺 ID") {
		t.Fatalf("空 ID 应报错: %v", err)
	}
	if _, err := s.Schedule([]Call{{ID: "a"}, {ID: "a"}}); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复 ID 应报错: %v", err)
	}
	if _, err := s.Schedule([]Call{{ID: "a", DependsOn: []string{"ghost"}}}); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("悬空依赖应报错: %v", err)
	}
	if _, err := s.Schedule([]Call{
		{ID: "a", DependsOn: []string{"b"}},
		{ID: "b", DependsOn: []string{"a"}},
	}); err == nil || !strings.Contains(err.Error(), "依赖环") {
		t.Fatalf("环应报错: %v", err)
	}
}

func TestExecuteConcurrentParallelGroup(t *testing.T) {
	// 3 个并行任务各等齐另两个（barrier 语义）——串行执行会死锁超时
	var running, peak int32
	var mu sync.Mutex
	release := make(chan struct{})
	s := &Scheduler{}
	tasks := make([]Task, 3)
	for i := range tasks {
		tasks[i] = Task{Call: Call{ID: string(rune('a' + i))}, Run: func(ctx context.Context) (string, error) {
			cur := atomic.AddInt32(&running, 1)
			mu.Lock()
			if cur > peak {
				peak = cur
			}
			mu.Unlock()
			select {
			case <-release:
				return "ok", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}}
	}
	done := make(chan []Result, 1)
	go func() { done <- s.Execute(context.Background(), tasks) }()
	// 等三任务并发在途后放行
	deadline := time.After(5 * time.Second)
	for atomic.LoadInt32(&running) < 3 {
		select {
		case <-deadline:
			t.Fatal("未达成 3 并发（组内串行）")
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(release)
	for _, r := range <-done {
		if r.Err != nil || r.Output != "ok" {
			t.Fatalf("结果异常: %+v", r)
		}
	}
	if peak != 3 {
		t.Fatalf("峰值并发应为 3，得 %d", peak)
	}
}

func TestExecuteResultInputOrder(t *testing.T) {
	s := &Scheduler{}
	slow := Task{Call: Call{ID: "slow"}, Run: func(ctx context.Context) (string, error) {
		time.Sleep(80 * time.Millisecond)
		return "slow", nil
	}}
	fast := Task{Call: Call{ID: "fast"}, Run: func(ctx context.Context) (string, error) {
		return "fast", nil
	}}
	res := s.Execute(context.Background(), []Task{slow, fast})
	if res[0].ID != "slow" || res[0].Output != "slow" || res[1].ID != "fast" {
		t.Fatalf("结果应按输入序: %+v", res)
	}
}

func TestExecuteFailureDoesNotBlockLaterGroups(t *testing.T) {
	s := &Scheduler{}
	var order []string
	var mu sync.Mutex
	fail := Task{Call: Call{ID: "fail"}, Run: func(ctx context.Context) (string, error) {
		return "", errors.New("本地失败")
	}}
	next := Task{Call: Call{ID: "next", DependsOn: []string{"fail"}}, Run: func(ctx context.Context) (string, error) {
		mu.Lock()
		order = append(order, "next")
		mu.Unlock()
		return "done", nil
	}}
	res := s.Execute(context.Background(), []Task{fail, next})
	if res[0].Err == nil {
		t.Fatal("fail 应有错误")
	}
	if res[1].Err != nil || res[1].Output != "done" {
		t.Fatalf("后续组不应被前序失败截断: %+v", res[1])
	}
	if len(order) != 1 {
		t.Fatal("next 未执行")
	}
}

func TestExecuteStopAfterCancelsRest(t *testing.T) {
	s := &Scheduler{}
	var executed []string
	var mu sync.Mutex
	mk := func(id string, stop bool) Task {
		return Task{Call: Call{ID: id, StopAfter: stop}, Run: func(ctx context.Context) (string, error) {
			mu.Lock()
			executed = append(executed, id)
			mu.Unlock()
			return id, nil
		}}
	}
	// plan 批准（StopAfter）→ deploy 依赖 plan → 应跳过
	res := s.Execute(context.Background(), []Task{mk("plan", true), mk("deploy", false)})
	if len(executed) != 1 || executed[0] != "plan" {
		t.Fatalf("StopAfter 后不应执行后续: %v", executed)
	}
	if !res[1].Skipped || res[1].Output != "" {
		t.Fatalf("后续应以 Skipped 呈现: %+v", res[1])
	}
}

func TestExecuteStopAfterFailureDoesNotStop(t *testing.T) {
	s := &Scheduler{}
	plan := Task{Call: Call{ID: "plan", StopAfter: true}, Run: func(ctx context.Context) (string, error) {
		return "", errors.New("批准失败")
	}}
	next := Task{Call: Call{ID: "next", DependsOn: []string{"plan"}}, Run: func(ctx context.Context) (string, error) {
		return "done", nil
	}}
	res := s.Execute(context.Background(), []Task{plan, next})
	if res[1].Skipped {
		t.Fatal("StopAfter 失败不应停轮")
	}
}

func TestExecuteCtxCancelSkipsRest(t *testing.T) {
	s := &Scheduler{}
	ctx, cancel := context.WithCancel(context.Background())
	block := Task{Call: Call{ID: "block"}, Run: func(ctx context.Context) (string, error) {
		cancel() // 在途任务触发取消
		<-ctx.Done()
		return "", ctx.Err()
	}}
	next := Task{Call: Call{ID: "next", DependsOn: []string{"block"}}, Run: func(ctx context.Context) (string, error) {
		return "should-not-run", nil
	}}
	res := s.Execute(ctx, []Task{block, next})
	if !res[1].Skipped {
		t.Fatalf("ctx 取消后剩余组应跳过: %+v", res[1])
	}
}

func TestExecuteScheduleErrorSurfaces(t *testing.T) {
	s := &Scheduler{}
	res := s.Execute(context.Background(), []Task{
		{Call: Call{ID: "a", DependsOn: []string{"ghost"}}, Run: func(ctx context.Context) (string, error) { return "", nil }},
	})
	if res[0].Err == nil {
		t.Fatal("调度错误应呈现为任务错误")
	}
}

func TestExecuteNilRun(t *testing.T) {
	s := &Scheduler{}
	res := s.Execute(context.Background(), []Task{{Call: Call{ID: "a"}}})
	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "缺执行体") {
		t.Fatalf("缺执行体应报错: %v", res[0].Err)
	}
}

func equalGroups(got, want [][]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if len(got[i]) != len(want[i]) {
			return false
		}
		for j := range got[i] {
			if got[i][j] != want[i][j] {
				return false
			}
		}
	}
	return true
}
