// Package acpx 统一调用 CLI Coding Agent（Claude Code / Codex / OpenCode / ZCode / MiniMax 等）。
//
// 核心抽象：Agent 接口把各家 headless 模式收敛为统一契约——
//
//	Run(Prompt/WorkDir/Model/Session/AllowedTools/Sandbox) → Result(Text/SessionID/Usage)
//	+ OnEvent 流式回调（文本增量/工具调用事件透传）
//
// 设计对齐 eino：AsTool() 把任意 Agent 包成 tool.BaseTool，挂进 ReAct agent 工具表
// （与 rag.KnowledgeService.AsTool 同范式），让 LLM 自主决定调用哪个编码 agent。
//
// 安全模型（继承 Argus adapter_cli 实战纪律）：
//   - 进程组执行：Setpgid + 超时 TERM → grace 后 KILL 整组（防孤儿）
//   - 环境变量白名单：绝不继承 ARGUS_*/OPENAI_* 等凭证给子进程
//   - stdout 限容：异常 agent 刷屏防内存放大
package acpx

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Agent CLI 编码 agent 统一接口。
type Agent interface {
	// Name agent 标识（claude / codex / opencode / zcode / minimax）。
	Name() string
	// Run 执行一次任务（headless 模式）。
	Run(ctx context.Context, req RunRequest) (*RunResult, error)
}

// RunRequest 统一运行请求。
type RunRequest struct {
	Prompt   string // 任务描述
	WorkDir  string // 工作目录（agent 在此目录下执行）
	Model    string // 模型覆盖（空 = agent 默认）
	// SessionID 续接会话（Claude --resume / opencode --session；Codex 暂不支持续聊）。
	SessionID string
	// MaxTurns 最大 agent 轮数上限（防失控循环；0 = agent 默认）。
	MaxTurns int
	// AllowedTools 工具白名单（claude 族语义："Bash(git:*)" "Read" "Edit"；
	// 空 = agent 默认）。
	AllowedTools []string
	// Sandbox 沙箱级别：readonly | workspace | full（Codex 原生支持；
	// claude 族映射 permission-mode）。
	Sandbox string
	// Env 额外环境变量白名单（按名从当前进程透传）。
	Env []string
	// Timeout 单次运行超时（0 = 10 分钟缺省）。
	Timeout time.Duration
	// OnEvent 流式事件回调（nil = 只取最终结果）。
	OnEvent func(Event)
}

// sandbox 词表。
const (
	SandboxReadonly  = "readonly"
	SandboxWorkspace = "workspace"
	SandboxFull      = "full"
)

// Event 流式事件。
type Event struct {
	Type string // text | thinking | tool_call | tool_result | error | result
	Text string
	Tool string // tool_call/tool_result 的工具名
	Raw  json.RawMessage
}

// 事件词表。
const (
	EventText       = "text"
	EventThinking   = "thinking"
	EventToolCall   = "tool_call"
	EventToolResult = "tool_result"
	EventError      = "error"
	EventResult     = "result"
)

// Usage token 用量与成本（agent 输出缺失时为 0）。
type Usage struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// RunResult 统一运行结果。
type RunResult struct {
	Text      string // 最终 assistant 文本
	SessionID string // 会话 ID（供续聊）
	Usage     Usage
	Duration  time.Duration
	ExitCode  int
	Raw       []byte // 原始输出（调试用）
}

// validate 校验请求并填充缺省值。
func (r *RunRequest) validate() error {
	if r.Prompt == "" {
		return fmt.Errorf("acpx: prompt 不能为空")
	}
	switch r.Sandbox {
	case "", SandboxReadonly, SandboxWorkspace, SandboxFull:
	default:
		return fmt.Errorf("acpx: 未知 sandbox %q（readonly|workspace|full）", r.Sandbox)
	}
	return nil
}

func (r *RunRequest) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 10 * time.Minute
}
