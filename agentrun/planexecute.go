// Plan-and-Execute 编排样板（架构模式 3）：封装 eino adk prebuilt/planexecute 的
// 三件套（Planner / Executor / Replanner）——Planner 把目标拆解为分步计划，
// Executor 带工具执行单步，Replanner 评估进度决定"完成任务"或"修订计划"，
// "执行→重规划"循环直至收敛。
//
// 与 ReAct（agentrun.Run）的分工：ReAct 适合"边想边做"的短链路任务；P&E 适合
// 目标明确、步骤可预规划的长链路任务（计划先行，每步执行结果可审计）。
// 需要定制 Planner/Replanner 提示词或输入整形时，直接使用 eino planexecute 原语。
package agentrun

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	planexecute "github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// PlanExecuteConfig Plan-and-Execute 运行配置。
type PlanExecuteConfig struct {
	// Planner 规划模型（须支持 tool calling——eino-ext openai ChatModel 均满足）。
	// 计划结构经默认 Plan ToolInfo 以 tool-calling 形态强制产出。
	Planner model.ToolCallingChatModel
	// Executor 执行模型（带工具时同样须支持 tool calling）。
	Executor model.ToolCallingChatModel
	// Tools 执行器可用工具（nil = 纯推理执行）。
	Tools []tool.BaseTool
	// MaxSteps "执行-重规划"循环上限（默认 10）。
	MaxSteps int
	// PlannerInstruction 追加给规划器的约束/背景（可空；经输入整形注入，
	// 不覆盖 planexecute 默认的计划格式提示词）。
	PlannerInstruction string
}

// PlanExecuteResult 运行结果。
type PlanExecuteResult struct {
	// Answer 最终答复（Replanner 判定任务完成时产出）。
	Answer string
}

// PlanAndExecute 运行 Plan-and-Execute。
func PlanAndExecute(ctx context.Context, cfg PlanExecuteConfig, goal string) (*PlanExecuteResult, error) {
	if cfg.Planner == nil || cfg.Executor == nil {
		return nil, fmt.Errorf("agentrun: Planner/Executor 不能为空")
	}
	maxSteps := maxStepsOr(cfg.MaxSteps)

	planner, err := planexecute.NewPlanner(ctx, &planexecute.PlannerConfig{
		ToolCallingChatModel: cfg.Planner,
		GenInputFn: func(ctx context.Context, userInput []adk.Message) ([]adk.Message, error) {
			if cfg.PlannerInstruction == "" {
				return userInput, nil
			}
			return append([]adk.Message{schema.SystemMessage(cfg.PlannerInstruction)}, userInput...), nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("agentrun: 构造 Planner 失败: %w", err)
	}
	executor, err := planexecute.NewExecutor(ctx, &planexecute.ExecutorConfig{
		Model: cfg.Executor,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: cfg.Tools},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("agentrun: 构造 Executor 失败: %w", err)
	}

	pe, err := planexecute.New(ctx, &planexecute.Config{
		Planner:       planner,
		Executor:      executor,
		MaxIterations: maxSteps,
	})
	if err != nil {
		return nil, fmt.Errorf("agentrun: 组合 Plan-and-Execute 失败: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: pe, EnableStreaming: false})
	iter := runner.Query(ctx, goal)

	var answer string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			return nil, fmt.Errorf("agentrun: plan-execute 事件错误: %w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mv := event.Output.MessageOutput
		// Replanner 判定完成时输出最终答复（assistant 无 tool_calls）
		if mv.Role == schema.Assistant && mv.Message != nil &&
			len(mv.Message.ToolCalls) == 0 && mv.Message.Content != "" {
			answer = mv.Message.Content
		}
	}
	if answer == "" {
		return nil, fmt.Errorf("agentrun: plan-execute 未产出最终答复（max_steps=%d）", maxSteps)
	}
	return &PlanExecuteResult{Answer: answer}, nil
}

func maxStepsOr(n int) int {
	if n > 0 {
		return n
	}
	return 10
}
