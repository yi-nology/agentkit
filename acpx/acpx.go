// Package acpx 统一调用 CLI Coding Agent（claude / zcode / codex / opencode /
// minimax / kimi / gemini / qwen / mimo 共 9 家，另有 GenericAgent 通用出口）。
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
//   - 进程组执行：Setpgid + 超时/取消 TERM 整组 → killGrace 宽限后 SIGKILL（cmd.Cancel/WaitDelay）
//   - 环境变量白名单：绝不继承 ARGUS_*/OPENAI_* 等凭证给子进程
//   - stdout 限容：异常 agent 刷屏防内存放大（单行另设 maxLineLen 上限防无换行洪泛）
//   - prompt 注入防线：位置参数传递的 prompt 经 promptArg 防护，"-"/"--" 开头的
//     内容不会被 CLI flag 解析器消费（否则 LLM 自填 prompt 可注入沙箱旁路开关）
package acpx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	Prompt  string // 任务描述
	WorkDir string // 工作目录（agent 在此目录下执行）
	Model   string // 模型覆盖（空 = agent 默认）
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
	// 注意：回调在 stdout 读取 goroutine 中同步执行——不得阻塞（拖慢子进程
	// 输出消费），不得 panic（会击穿整个进程）。
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

// promptArg prompt 传参防注入：以 "-" 开头的 prompt 传给位置参数或 flag 值时，
// 主流 CLI flag 解析器（clap/commander 等）可能把它当 flag 消费——LLM 自主填写
// prompt 时，攻击者可借评审内容注入 "--dangerously-bypass-approvals-and-sandbox"
// 之类的开关实现沙箱逃逸。前置换行符使 token 不再以 "-" 开头，对模型语义无影响。
func promptArg(p string) string {
	if strings.HasPrefix(p, "-") {
		return "\n" + p
	}
	return p
}
