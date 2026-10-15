package toolprior

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// fakeTool 假工具。
type fakeTool struct {
	name string
	desc string
	err  error
}

func (f *fakeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &schema.ToolInfo{Name: f.name, Desc: f.desc}, nil
}

func (f *fakeTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "ran:" + f.name, nil
}

func TestOrderedPrioritySort(t *testing.T) {
	ctx := context.Background()
	table := NewTable().
		Add(Entry{Tool: &fakeTool{name: "mcp"}, Priority: PriorityExternal}).
		Add(Entry{Tool: &fakeTool{name: "get_file"}, Priority: PriorityCore}).
		Add(Entry{Tool: &fakeTool{name: "knowledge"}, Priority: PrioritySupport})

	ordered := table.Ordered(ctx)
	want := []string{"get_file", "knowledge", "mcp"}
	for i, w := range want {
		info, _ := ordered[i].Info(ctx)
		if info.Name != w {
			t.Fatalf("位置 %d = %s, want %s", i, info.Name, w)
		}
	}
}

func TestOrderedStableSamePriority(t *testing.T) {
	ctx := context.Background()
	table := NewTable().
		Add(Entry{Tool: &fakeTool{name: "a"}, Priority: PriorityCore}).
		Add(Entry{Tool: &fakeTool{name: "b"}, Priority: PriorityCore})

	ordered := table.Ordered(ctx)
	// 同优先级保持注册序
	names := make([]string, 0, 2)
	for _, t2 := range ordered {
		info, _ := t2.Info(ctx)
		names = append(names, info.Name)
	}
	if names[0] != "a" || names[1] != "b" {
		t.Fatalf("同优先级应保持注册序: %v", names)
	}
}

func TestStrategyPrompt(t *testing.T) {
	ctx := context.Background()
	table := NewTable().
		Add(Entry{Tool: &fakeTool{name: "get_file"}, Priority: PriorityCore,
			When: "核实证据必须先用", Cost: "low"}).
		Add(Entry{Tool: &fakeTool{name: "mcp"}, Priority: PriorityExternal,
			When: "外部查询", Cost: "high"})

	prompt := table.StrategyPrompt(ctx)
	for _, want := range []string{
		"工具使用策略", "priority=0", "priority=2",
		"get_file", "核实证据必须先用", "cost=high",
		"避免不必要的外部开销",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("策略提示缺 %q", want)
		}
	}
	// 策略提示按优先级排序：get_file 在 mcp 之前
	if strings.Index(prompt, "get_file") > strings.Index(prompt, "mcp") {
		t.Fatal("策略提示顺序应为优先级序")
	}
}

func TestStrategyPromptEmpty(t *testing.T) {
	if s := NewTable().StrategyPrompt(context.Background()); s != "" {
		t.Fatalf("空表策略应为空串，得到 %q", s)
	}
}

func TestWithCallLimit(t *testing.T) {
	ft := &fakeTool{name: "mcp", desc: "external"}
	limited := LimitCalls(ft, 2)

	// 前两次正常（LimitCalls 返回 BaseTool，调用经 InvokableTool 断言）
	lt, ok := limited.(tool.InvokableTool)
	if !ok {
		t.Fatal("应实现 tool.InvokableTool")
	}
	for i := 0; i < 2; i++ {
		out, err := lt.InvokableRun(context.Background(), "{}")
		if err != nil {
			t.Fatalf("第 %d 次不应受限: %v", i+1, err)
		}
		if out != "ran:mcp" {
			t.Fatalf("透传失败: %q", out)
		}
	}
	// 第三次软止损：返回固定提示文本（nil error）——error 会被 eino ToolsNode
	// 上抛中止整个 agent 运行，模型永远看不到；文本则模型可见可收尾
	out, err := lt.InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatalf("超限应软止损（nil error）: %v", err)
	}
	if !strings.Contains(out, "LIMIT_REACHED") || !strings.Contains(out, "上限") {
		t.Fatalf("超限应返回模型可见的提示文本: %q", out)
	}
	// 第四次同样拒绝（连续拒绝不透传内层）
	out, err = lt.InvokableRun(context.Background(), "{}")
	if err != nil || !strings.Contains(out, "LIMIT_REACHED") {
		t.Fatalf("持续超限应持续拒绝: %q %v", out, err)
	}
	// Info 透传不受限
	info, err := limited.Info(context.Background())
	if err != nil || info.Name != "mcp" {
		t.Fatalf("Info 应透传: %v %v", info, err)
	}
}

func TestWithCallLimitZero(t *testing.T) {
	ft := &fakeTool{name: "x"}
	limited := LimitCalls(ft, 0) // 0 = 不限（原样返回）
	lt := limited.(tool.InvokableTool)
	for i := 0; i < 10; i++ {
		if _, err := lt.InvokableRun(context.Background(), "{}"); err != nil {
			t.Fatalf("不限制时不应报错: %v", err)
		}
	}
}

func TestOrderedInfoErrorStillReturned(t *testing.T) {
	// Info 失败的工具不应被静默丢弃
	ctx := context.Background()
	table := NewTable().Add(Entry{Tool: &fakeTool{name: "broken", err: errors.New("info boom")}, Priority: PriorityCore})
	ordered := table.Ordered(ctx)
	if len(ordered) != 1 {
		t.Fatalf("Info 失败的工具仍应出现在表中，得到 %d", len(ordered))
	}
}

func TestStrategyPromptInfoErrorFallback(t *testing.T) {
	ctx := context.Background()
	table := NewTable().Add(Entry{Tool: &fakeTool{name: "", err: errors.New("boom")}, Priority: PriorityCore, When: "测试"})
	prompt := table.StrategyPrompt(ctx)
	if !strings.Contains(prompt, "tool#0") {
		t.Fatalf("Info 失败应有回退名: %s", prompt)
	}
}
