package reflection

import (
	"context"
	"strings"
	"testing"

	"github.com/yi-nology/agentkit/llm/llmtest"
)

// 桩统一走 llmtest.Model（耗尽报错语义与原桩一致）。

func TestRefineConvergesSecondRound(t *testing.T) {
	// 调用序：draft → critique(不通过) → revise → critique(通过)
	fm := &llmtest.Model{Script: []llmtest.Resp{
		{Content: "初稿代码"}, // draft
		{Content: `{"pass":false,"issues":["缺少错误处理","命名不清"]}`}, // critique 1
		{Content: "修订稿代码"},                     // revise
		{Content: `{"pass":true,"issues":[]}`}, // critique 2
	}}
	res, err := Refine(context.Background(), &Config{
		Model: fm, ModelName: "fake",
		Task:   "实现函数",
		Input:  "需求素材",
		Rubric: "1. 有错误处理 2. 命名清晰",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Converged || res.Text != "修订稿代码" {
		t.Fatalf("应收敛到修订稿: converged=%v output=%q", res.Converged, res.Text)
	}
	if len(res.Rounds) != 2 {
		t.Fatalf("应有两轮记录: %d", len(res.Rounds))
	}
	if len(res.Rounds[0].Issues) != 2 || res.Rounds[1].Pass != true {
		t.Fatalf("轮次记录不符: %+v", res.Rounds)
	}
	// 修订提示词应携带上一稿与全量 issues
	if !strings.Contains(fm.Inputs[2], "初稿代码") || !strings.Contains(fm.Inputs[2], "缺少错误处理") {
		t.Fatalf("修订输入应含草稿与问题: %q", fm.Inputs[2])
	}
}

func TestRefineMaxIterations(t *testing.T) {
	fm := &llmtest.Model{Script: []llmtest.Resp{
		{Content: "d1"},
		{Content: `{"pass":false,"issues":["x"]}`},
		{Content: "d2"},
		{Content: `{"pass":false,"issues":["y"]}`},
		{Content: "d3"},
		{Content: `{"pass":false,"issues":["z"]}`},
	}}
	res, err := Refine(context.Background(), &Config{
		Model: fm, Task: "t", Rubric: "r", MaxIterations: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Converged || res.Text != "d3" || len(res.Rounds) != 3 {
		t.Fatalf("达上限应返回末稿: converged=%v output=%q rounds=%d", res.Converged, res.Text, len(res.Rounds))
	}
}

func TestRefineSelfContradictoryCritique(t *testing.T) {
	// pass=true 但带 issues → 以 issues 为准（不自洽评审不可信）
	fm := &llmtest.Model{Script: []llmtest.Resp{
		{Content: "d1"},
		{Content: `{"pass":true,"issues":["仍有问题"]}`},
		{Content: "d2"},
		{Content: `{"pass":true,"issues":[]}`},
	}}
	res, err := Refine(context.Background(), &Config{Model: fm, Task: "t", Rubric: "r", MaxIterations: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds[0].Pass {
		t.Fatal("pass=true 且有 issues 应判不通过")
	}
	if !res.Converged {
		t.Fatal("第二轮应收敛")
	}
}

func TestRefineValidation(t *testing.T) {
	if _, err := Refine(context.Background(), &Config{Rubric: "r"}); err == nil {
		t.Fatal("缺 Model 应报错")
	}
	if _, err := Refine(context.Background(), &Config{Model: &llmtest.Model{}}); err == nil {
		t.Fatal("缺 Task 应报错")
	}
	if _, err := Refine(context.Background(), &Config{Model: &llmtest.Model{}, Task: "t"}); err == nil {
		t.Fatal("缺 Rubric 应报错")
	}
}
