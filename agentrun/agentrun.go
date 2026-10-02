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
	"strings"

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
	// 与 ToolsFactory 二选一；两者都设置时 ToolsFactory 优先。
	Tools []tool.BaseTool
	// ToolsFactory 工具表工厂：每次 run 调用新建一份工具表。
	// RunWithRetry 场景建议设置——工具表含 toolprior.WithCallLimit 等
	// 有状态包装时，复用同一实例会让限流计数跨重试累计（重试继承 0 余额，
	// 每次调用立即被拒）。工厂内每次重新包装即可让预算按尝试重置。
	ToolsFactory func() []tool.BaseTool
	// MaxIterations ReAct 循环轮数上限（默认 12）。
	MaxIterations int
	// RetryAfterMutation 变更类工具已执行后仍允许整体重试（默认 false=守卫生效：
	// 首轮调过变更类工具后失败不再重跑——重复副作用风险，如实上抛交调用方降级）。
	// 只读/幂等工具场景可置 true 恢复无条件重试。
	RetryAfterMutation bool
}

// Event agent 运行过程事件（OnEvent 回调载荷，观测/进度展示用）。
type Event struct {
	Type   string // reasoning | text | tool_call | tool_result
	Text   string
	Tool   string // tool_call/tool_result 的工具名
	Args   string // tool_call 的 JSON 参数串
	CallID string // tool_call/tool_result 的原生调用 ID（声明 tc.ID / 结果 ToolCallID）——观测面精确配对依据
}

// 事件类型词表。
const (
	EventReasoning  = "reasoning"
	EventText       = "text"
	EventToolCall   = "tool_call"
	EventToolResult = "tool_result"
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
// 副作用守卫：首轮已调用变更类工具（见 IsMutatingTool）后不整体重跑，除非
// Config.RetryAfterMutation=true——重跑会重复副作用（脚本执行/服务操作类工具
// 在首轮已生效）。
// 注意：两次尝试共用 cfg.Tools 实例——工具表含 toolprior.WithCallLimit 等
// 有状态包装时，限流计数会跨尝试累计；需要按尝试重置预算请设置 ToolsFactory。
func RunWithRetry(ctx context.Context, cfg Config, query, retryQuery string) (string, error) {
	out, mutating, err := runWithMeta(ctx, cfg, query, nil)
	if err == nil {
		return out, nil
	}
	if mutating != "" && !cfg.RetryAfterMutation {
		return "", mutationSkipErr(mutating, err)
	}
	return run(ctx, cfg, retryQuery, nil)
}

// RunWithEventsAndRetry 带 过程事件回调 的失败回喂重试（重试过程可观测）。
// 副作用守卫与 RunWithRetry 一致：首轮调过变更类工具后不整体重跑（观测事件照常全量回调）。
func RunWithEventsAndRetry(ctx context.Context, cfg Config, query, retryQuery string, onEvent func(Event)) (string, error) {
	out, mutating, err := runWithMeta(ctx, cfg, query, onEvent)
	if err == nil {
		return out, nil
	}
	if mutating != "" && !cfg.RetryAfterMutation {
		return "", mutationSkipErr(mutating, err)
	}
	return run(ctx, cfg, retryQuery, onEvent)
}

// run 核心：构造 ADK agent → Runner.Query → drain 事件流。
func run(ctx context.Context, cfg Config, query string, onEvent func(Event)) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	tools := cfg.Tools
	if cfg.ToolsFactory != nil {
		tools = cfg.ToolsFactory()
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          cfg.Name,
		Description:   cfg.Description,
		Instruction:   cfg.Instruction,
		Model:         cfg.Model,
		MaxIterations: cfg.maxIterations(),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
		},
	})
	if err != nil {
		return "", fmt.Errorf("agentrun: 构造 agent 失败: %w", err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: false})
	iter := runner.Query(ctx, query)

	var (
		finalText  string
		sawFinal   bool
		toolCalled = map[string]string{} // tool_call id → 工具名（回填 tool_result 事件）
	)
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			// 底层事件通道是 UnboundedChan（生产者不阻塞），提前返回不会泄漏
			return "", fmt.Errorf("agentrun: agent 事件错误: %w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		mv := event.Output.MessageOutput
		if mv.Message != nil {
			for _, tc := range mv.Message.ToolCalls {
				toolCalled[tc.ID] = tc.Function.Name
			}
		}
		// 工具结果消息：回填 tool_result 事件（观测/进度展示需要工具返回）
		if onEvent != nil && mv.Role == schema.Tool && mv.Message != nil {
			name := toolCalled[mv.Message.ToolCallID]
			onEvent(Event{Type: EventToolResult, CallID: mv.Message.ToolCallID, Tool: name, Text: mv.Message.Content})
			continue
		}
		if mv.Role == schema.Assistant && mv.Message != nil {
			// 思考过程先于动作/答复（同一 assistant 消息内 reasoning_content 先产出）
			if onEvent != nil && mv.Message.ReasoningContent != "" {
				onEvent(Event{Type: EventReasoning, Text: mv.Message.ReasoningContent})
			}
			// ReAct 出口判定：assistant 且无 tool_calls 即最终答复——
			// 空内容也记录（部分推理型模型会有空最终消息），错误文案区分"空答复"与"没答复"
			if len(mv.Message.ToolCalls) == 0 {
				sawFinal = true
				finalText = mv.Message.Content
				if onEvent != nil && finalText != "" {
					onEvent(Event{Type: EventText, Text: finalText})
				}
				continue
			}
			// 中间过程：tool_calls 声明 → 工具调用事件（含参数与原生 ID，观测面需要看到调用命令并精确配对）
			if onEvent != nil {
				for _, tc := range mv.Message.ToolCalls {
					onEvent(Event{Type: EventToolCall, CallID: tc.ID, Tool: tc.Function.Name, Args: tc.Function.Arguments})
				}
			}
		}
	}
	if !sawFinal {
		return "", fmt.Errorf("agentrun: agent 未产出最终文本（iterations=%d）", cfg.maxIterations())
	}
	if strings.TrimSpace(finalText) == "" {
		return "", fmt.Errorf("agentrun: agent 最终答复为空（iterations=%d）", cfg.maxIterations())
	}
	return finalText, nil
}
