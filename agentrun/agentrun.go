// Package agentrun ReAct agent 运行样板：封装 eino ADK 的
// ChatModelAgent + Runner 构造、事件流 drain、最终文本提取与失败重试。
//
// 从 Argus 的 R3 执行模式提炼——任何"单 agent 带工具自主循环"的场景
// 都可以直接用，不必重写 ADK 样板：
//
//	out, err := agentrun.Run(ctx, agentrun.Config{
//	    Name:        "reviewer",
//	    Instruction: instruction,
//	    Model:       chatModel,           // llm.Generator.RawModel()
//	    Tools:       tools,               // eino 工具表（mcp/rag/skill...）
//	    MaxIterations: 12,
//	}, query)
//
// ReAct 出口判定：assistant 消息且不带 tool_calls 即最终答复；
// MaxIterations 耗尽仍未出口 → 报错（调用方可决定降级）。
package agentrun

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// 默认迭代上限（ADK ReAct 循环轮数；防失控循环）。
const DefaultMaxIterations = 12

// Config ReAct agent 运行配置。
type Config struct {
	// Name agent 名（trace/日志标识）。
	Name string
	// Description 角色描述（进 agent 元信息）。
	Description string
	// Instruction 系统提示词（含方法论/工具策略等注入产物）。
	Instruction string
	// Model eino ChatModel（llm.Generator 的 RawModel()）。
	Model model.BaseChatModel
	// Tools 工具表（建议经 toolprior.Table.Ordered(ctx) 产出）。
	Tools []tool.BaseTool
	// MaxIterations ReAct 循环轮数上限（默认 12）。
	MaxIterations int
}

// Event agent 运行过程事件（OnEvent 回调载荷，观测/进度展示用）。
type Event struct {
	Type string // text | tool_call
	Text string
	Tool string // tool_call 的工具名
}

// 事件类型词表。
const (
	EventText     = "text"
	EventToolCall = "tool_call"
)

// Validate 校验配置必需项。
func (c Config) Validate() error {
	if c.Model == nil {
		return fmt.Errorf("agentrun: Model 不能为空")
	}
	if c.Instruction == "" {
		return fmt.Errorf("agentrun: Instruction 不能为空")
	}
	return nil
}

func (c Config) maxIterations() int {
	if c.MaxIterations > 0 {
		return c.MaxIterations
	}
	return DefaultMaxIterations
}

// Run 执行一次 ReAct 查询，返回最终 assistant 文本。
func Run(ctx context.Context, cfg Config, query string) (string, error) {
	return run(ctx, cfg, query, nil)
}

// RunWithEvents 执行并回调过程事件（nil 回调等价 Run）。
func RunWithEvents(ctx context.Context, cfg Config, query string, onEvent func(Event)) (string, error) {
	return run(ctx, cfg, query, onEvent)
}

// RunWithRetry 失败回喂重试一次：首次失败（或产出空文本）时以 retryQuery 再跑。
// retryQuery 由调用方构造（可携带首轮错误/输出摘要作为反馈上下文）。
// 两次均失败返回末次错误。
func RunWithRetry(ctx context.Context, cfg Config, query, retryQuery string) (string, error) {
	out, err := run(ctx, cfg, query, nil)
	if err == nil {
		return out, nil
	}
	return run(ctx, cfg, retryQuery, nil)
}

// run 核心：构造 ADK agent → Runner.Query → drain 事件流。
func run(ctx context.Context, cfg Config, query string, onEvent func(Event)) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          cfg.Name,
		Description:   cfg.Description,
		Instruction:   cfg.Instruction,
		Model:         cfg.Model,
		MaxIterations: cfg.maxIterations(),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: cfg.Tools},
		},
	})
	if err != nil {
		return "", fmt.Errorf("agentrun: 构造 agent 失败: %w", err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: false})
	iter := runner.Query(ctx, query)

	var finalText string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			return "", fmt.Errorf("agentrun: agent 事件错误: %w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mv := event.Output.MessageOutput
		// ReAct 出口判定：assistant 且无 tool_calls 才是最终答复
		if mv.Role == schema.Assistant && mv.Message != nil &&
			len(mv.Message.ToolCalls) == 0 && mv.Message.Content != "" {
			finalText = mv.Message.Content
			if onEvent != nil {
				onEvent(Event{Type: EventText, Text: mv.Message.Content})
			}
			continue
		}
		// 中间过程：tool_calls 声明 → 工具调用事件
		if onEvent != nil && mv.Role == schema.Assistant && mv.Message != nil {
			for _, tc := range mv.Message.ToolCalls {
				onEvent(Event{Type: EventToolCall, Tool: tc.Function.Name})
			}
		}
	}
	if finalText == "" {
		return "", fmt.Errorf("agentrun: agent 未产出最终文本（iterations=%d）", cfg.maxIterations())
	}
	return finalText, nil
}
