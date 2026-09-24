package agentrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yi-nology/agentkit/llm/llmtest"
)

// plan/respond 工具调用脚本（eino planexecute 的 tool-calling 协议形态）。
func planCall(steps ...string) llmtest.Resp {
	var sb strings.Builder
	sb.WriteString(`{"steps":[`)
	for i, s := range steps {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`"` + s + `"`)
	}
	sb.WriteString(`]}`)
	return llmtest.Resp{ToolCalls: llmtest.ToolCall("plan", sb.String())}
}

func respondCall(text string) llmtest.Resp {
	return llmtest.Resp{ToolCalls: llmtest.ToolCall("respond", `{"response":"`+text+`"}`)}
}

// TestPlanAndExecuteEndToEnd 全链路：Planner 出计划 → Executor 执行 →
// Replanner 判定完成（respond）→ 最终答复解包为纯文本。
// 漏传 Replanner 的历史 bug（v0.8.0 起不可用）由本测试锁死修复。
func TestPlanAndExecuteEndToEnd(t *testing.T) {
	// Planner 与 Replanner 复用同一模型（未显式配置 Replanner 的兜底路径）：
	// 第 1 次（规划）出 plan，第 2 次（重规划判定）出 respond。
	planner := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: false, Script: []llmtest.Resp{
		planCall("检索资料", "汇总答复"),
		respondCall("最终答复：资料已汇总"),
	}}}
	executor := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{Content: "步骤执行完成"},
	}}}

	res, err := PlanAndExecute(context.Background(), PlanExecuteConfig{
		Planner:            planner,
		Executor:           executor,
		MaxSteps:           5,
		PlannerInstruction: "只规划检索类步骤",
	}, "调研 X")
	if err != nil {
		t.Fatalf("PlanAndExecute: %v", err)
	}
	// respond 的 {"response":...} 信封应解包为纯文本。
	if res.Text != "最终答复：资料已汇总" {
		t.Fatalf("Text = %q（respond 信封应解包）", res.Text)
	}
}

// TestPlanAndExecuteExplicitReplanner 显式配置独立 Replanner 模型。
func TestPlanAndExecuteExplicitReplanner(t *testing.T) {
	planner := &llmtest.ToolModel{Model: &llmtest.Model{Script: []llmtest.Resp{
		planCall("单步"),
	}}}
	replanner := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		respondCall("由判定模型收尾"),
	}}}
	executor := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{Content: "done"},
	}}}

	res, err := PlanAndExecute(context.Background(), PlanExecuteConfig{
		Planner:   planner,
		Replanner: replanner,
		Executor:  executor,
		MaxSteps:  3,
	}, "目标")
	if err != nil {
		t.Fatalf("PlanAndExecute: %v", err)
	}
	if res.Text != "由判定模型收尾" {
		t.Fatalf("Text = %q", res.Text)
	}
}

// TestPlanAndExecuteMaxStepsExhausted Replanner 恒重规划 → 循环上限耗尽报错。
func TestPlanAndExecuteMaxStepsExhausted(t *testing.T) {
	planner := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		planCall("步骤"),
	}}}
	executor := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{Content: "执行中"},
	}}}

	_, err := PlanAndExecute(context.Background(), PlanExecuteConfig{
		Planner:  planner,
		Executor: executor,
		MaxSteps: 2,
	}, "永不完成的任务")
	if err == nil || !strings.Contains(err.Error(), "未产出最终答复") {
		t.Fatalf("上限耗尽应报错: %v", err)
	}
}

// TestPlanAndExecuteValidation 参数校验。
func TestPlanAndExecuteValidation(t *testing.T) {
	if _, err := PlanAndExecute(context.Background(), PlanExecuteConfig{}, "g"); err == nil ||
		!strings.Contains(err.Error(), "Planner/Executor") {
		t.Fatalf("缺 Planner/Executor 应报错: %v", err)
	}
	m := &llmtest.ToolModel{Model: &llmtest.Model{}}
	if _, err := PlanAndExecute(context.Background(), PlanExecuteConfig{Executor: m}, "g"); err == nil {
		t.Fatal("缺 Planner 应报错")
	}
	if _, err := PlanAndExecute(context.Background(), PlanExecuteConfig{Planner: m}, "g"); err == nil {
		t.Fatal("缺 Executor 应报错")
	}
}

// TestMaxStepsOr 缺省与显式值。
func TestMaxStepsOr(t *testing.T) {
	if maxStepsOr(0) != 10 || maxStepsOr(-3) != 10 {
		t.Fatalf("缺省应为 10: %d %d", maxStepsOr(0), maxStepsOr(-3))
	}
	if maxStepsOr(7) != 7 {
		t.Fatal("显式值应保留")
	}
}

// TestUnwrapRespond respond 信封解包。
func TestUnwrapRespond(t *testing.T) {
	if got := unwrapRespond(`{"response":"答案"}`); got != "答案" {
		t.Fatalf("信封应解包: %q", got)
	}
	// 非信封形态原样返回。
	for _, s := range []string{"纯文本", `{"other":1}`, "", `{"response":""}`} {
		if got := unwrapRespond(s); got != s {
			t.Fatalf("%q 不应改动，得到 %q", s, got)
		}
	}
}

// TestPlanAndExecuteStreamErrBeforeFirstChunk 桩错误冒烟（Planner 流失败）。
func TestPlanAndExecuteStreamErrBeforeFirstChunk(t *testing.T) {
	planner := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{Err: errors.New("planner down")},
	}}}
	executor := &llmtest.ToolModel{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
		{Content: "x"},
	}}}
	if _, err := PlanAndExecute(context.Background(), PlanExecuteConfig{
		Planner: planner, Executor: executor, MaxSteps: 2,
	}, "g"); err == nil {
		t.Fatal("Planner 失败应上抛")
	}
}
