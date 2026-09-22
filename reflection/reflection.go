// Package reflection 反思（Reflection）架构原语：生成 → 自我批判 → 修订的收敛循环。
//
// 流程：按 Task 对 Input 产出初稿 → Critic 按 Rubric 评审（结构化 JSON：
// pass/issues）→ 未通过则带 issues 修订重写 → 收敛（pass）或达轮次上限返回末稿。
//
// 适用：对产出质量有硬标准、且标准可被模型自评的任务（代码实现、报告撰写、
// 翻译润色）；标准主观或需要外部事实核验的场景，Critic 不可靠，不适用。
package reflection

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/yi-nology/agentkit/llm"
)

// Config 反思循环配置。
type Config struct {
	// Model 生成/评审共用的模型（内部包 llm.Client：带重试与 JSON 解析回喂）。
	Model model.BaseChatModel
	// ModelName 模型名（记账/日志标识）。
	ModelName string
	// Task 任务指令：要生成什么（如"实现该需求的 Go 函数"）。
	Task string
	// Input 待处理的素材（需求文本/diff/问题；可空）。
	Input string
	// Rubric 评审标准：Critic 据此判定 pass/issues（尽可能可判定、可执行）。
	Rubric string
	// MaxIterations 最大"批判→修订"轮数（默认 3；1 = 只批判一次不修订也要跑）。
	MaxIterations int
}

// Round 一轮（初稿或修订稿 + 该稿的批判结论）。
type Round struct {
	Draft    string
	Pass     bool
	Issues   []string
	Critique string // Critic 原始 JSON（审计留痕）
}

// Result 反思循环结果。
type Result struct {
	// Text 末稿（收敛稿或达上限时的最佳稿）——与 acpx.RunResult.Text、
	// agentrun.Event.Text 同词表（Text=模型/agent 最终产出文本）。
	Text string
	// Rounds 每轮记录（Round[0] = 初稿）。
	Rounds []Round
	// Converged 是否通过 Rubric 收敛（false = 达到轮次上限返回末稿）。
	Converged bool
}

// Refine 运行反思循环。
func Refine(ctx context.Context, cfg *Config) (*Result, error) {
	if cfg == nil || cfg.Model == nil {
		return nil, fmt.Errorf("reflection: Model 不能为空")
	}
	if cfg.Task == "" {
		return nil, fmt.Errorf("reflection: Task 不能为空")
	}
	if cfg.Rubric == "" {
		return nil, fmt.Errorf("reflection: Rubric 不能为空——无标准的自我批判是空转")
	}
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 3
	}

	client := llm.NewClient(cfg.Model, cfg.ModelName, nil)

	res := &Result{}
	draft, err := generateDraft(ctx, client, cfg, "", nil)
	if err != nil {
		return nil, fmt.Errorf("reflection: 初稿生成失败: %w", err)
	}

	for i := 0; i < maxIter; i++ {
		verdict, err := critique(ctx, client, cfg, draft)
		if err != nil {
			return nil, fmt.Errorf("reflection: 第 %d 轮批判失败: %w", i+1, err)
		}
		res.Rounds = append(res.Rounds, Round{
			Draft: draft, Pass: verdict.Pass, Issues: verdict.Issues, Critique: verdict.Raw,
		})
		if verdict.Pass {
			res.Converged = true
			res.Text = draft
			return res, nil
		}
		if i == maxIter-1 {
			break // 无修订机会了
		}
		// 修订：上一稿 + 全量 issues（不只增量——合并评审口径更稳）
		draft, err = generateDraft(ctx, client, cfg, draft, verdict.Issues)
		if err != nil {
			return nil, fmt.Errorf("reflection: 第 %d 轮修订失败: %w", i+1, err)
		}
	}
	res.Text = draft
	return res, nil
}

// generateDraft 生成初稿（previousIssues 为空）或修订稿。
func generateDraft(ctx context.Context, client *llm.Client, cfg *Config, prev string, issues []string) (string, error) {
	sys := "你是严谨的执行者。" + cfg.Task
	user := ""
	if cfg.Input != "" {
		user += "【素材】\n" + cfg.Input + "\n\n"
	}
	if prev != "" {
		user += "【你此前的草稿】\n" + prev + "\n\n【评审发现的问题（全部修复）】\n"
		for i, is := range issues {
			user += fmt.Sprintf("%d. %s\n", i+1, is)
		}
		user += "\n请输出修复所有问题后的完整成品（不是增量 diff）。\n"
	}
	user += "直接输出成品本身，不要解释。"

	resp, err := client.Generate(ctx, "reflection:draft", []*schema.Message{
		schema.SystemMessage(sys), schema.UserMessage(user),
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// critiqueVerdict Critic 的结构化结论。
type critiqueVerdict struct {
	Pass   bool     `json:"pass"`
	Issues []string `json:"issues"`
	Raw    string   `json:"-"`
}

// critique 按 Rubric 评审草稿。
func critique(ctx context.Context, client *llm.Client, cfg *Config, draft string) (*critiqueVerdict, error) {
	sys := "你是苛刻但公正的评审。只依据评审标准判定，不臆测标准之外的问题。"
	user := fmt.Sprintf("【评审标准】\n%s\n\n【待评审草稿】\n%s\n\n"+
		"按标准逐条评审。只输出 JSON：{\"pass\":bool,\"issues\":[\"问题描述\",...]}——"+
		"全部达标 pass=true 且 issues 为空数组；否则 pass=false 且 issues 逐条列出问题。",
		cfg.Rubric, draft)
	var v critiqueVerdict
	if err := client.GenerateJSON(ctx, "reflection:critique", []*schema.Message{
		schema.SystemMessage(sys), schema.UserMessage(user),
	}, &v); err != nil {
		return nil, err
	}
	// 防御：pass=true 但仍有 issues → 以 issues 为准（不自洽的评审不可信）
	if v.Pass && len(v.Issues) > 0 {
		v.Pass = false
	}
	return &v, nil
}
