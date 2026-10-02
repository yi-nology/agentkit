package reflection

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeModel 脚本化假模型：按调用序返回脚本内容（Generate 计数）。
type fakeModel struct {
	responses []string
	calls     int
	prompts   []string
}

func (f *fakeModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("reflection 测试桩不支持流式")
}

func (f *fakeModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.prompts = append(f.prompts, in[len(in)-1].Content)
	if f.calls >= len(f.responses) {
		return nil, fmt.Errorf("脚本耗尽（第 %d 次调用）", f.calls+1)
	}
	resp := f.responses[f.calls]
	f.calls++
	return &schema.Message{Role: schema.Assistant, Content: resp}, nil
}

func TestRefineConvergesSecondRound(t *testing.T) {
	// 调用序：draft → critique(不通过) → revise → critique(通过)
	fm := &fakeModel{responses: []string{
		"初稿代码", // draft
		`{"pass":false,"issues":["缺少错误处理","命名不清"]}`, // critique 1
		"修订稿代码",                     // revise
		`{"pass":true,"issues":[]}`, // critique 2
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
	if !res.Converged || res.Output != "修订稿代码" {
		t.Fatalf("应收敛到修订稿: converged=%v output=%q", res.Converged, res.Output)
	}
	if len(res.Rounds) != 2 {
		t.Fatalf("应有两轮记录: %d", len(res.Rounds))
	}
	if len(res.Rounds[0].Issues) != 2 || res.Rounds[1].Pass != true {
		t.Fatalf("轮次记录不符: %+v", res.Rounds)
	}
	// 修订提示词应携带上一稿与全量 issues
	if !strings.Contains(fm.prompts[2], "初稿代码") || !strings.Contains(fm.prompts[2], "缺少错误处理") {
		t.Fatalf("修订输入应含草稿与问题: %q", fm.prompts[2])
	}
}

func TestRefineMaxIterations(t *testing.T) {
	fm := &fakeModel{responses: []string{
		"d1", `{"pass":false,"issues":["x"]}`,
		"d2", `{"pass":false,"issues":["y"]}`,
		"d3", `{"pass":false,"issues":["z"]}`,
	}}
	res, err := Refine(context.Background(), &Config{
		Model: fm, Task: "t", Rubric: "r", MaxIterations: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Converged || res.Output != "d3" || len(res.Rounds) != 3 {
		t.Fatalf("达上限应返回末稿: converged=%v output=%q rounds=%d", res.Converged, res.Output, len(res.Rounds))
	}
}

func TestRefineSelfContradictoryCritique(t *testing.T) {
	// pass=true 但带 issues → 以 issues 为准（不自洽评审不可信）
	fm := &fakeModel{responses: []string{
		"d1", `{"pass":true,"issues":["仍有问题"]}`,
		"d2", `{"pass":true,"issues":[]}`,
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
	if _, err := Refine(context.Background(), &Config{Model: &fakeModel{}}); err == nil {
		t.Fatal("缺 Task 应报错")
	}
	if _, err := Refine(context.Background(), &Config{Model: &fakeModel{}, Task: "t"}); err == nil {
		t.Fatal("缺 Rubric 应报错")
	}
}
