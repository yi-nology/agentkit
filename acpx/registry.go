package acpx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// toolIn eino 工具入参。
type toolIn struct {
	Agent   string `json:"agent" jsonschema:"description=编码 agent 名（claude|zcode|codex|opencode|...）"`
	Prompt  string `json:"prompt" jsonschema:"description=给编码 agent 的任务描述（要具体、含期望产出）"`
	WorkDir string `json:"work_dir,omitempty" jsonschema:"description=工作目录（agent 在此目录执行；空=当前目录）"`
}

// toolOut eino 工具出参。
type toolOut struct {
	Text      string `json:"text,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RunEvent 一次 agent 运行的观测事件（Registry.OnRun 钩子载荷）。
type RunEvent struct {
	Agent     string        // agent 名
	Duration  time.Duration // 运行耗时
	Err       error         // 非空 = 运行失败
	TextLen   int           // 产出文本长度（不落内容，防泄露）
	Usage     Usage
	SessionID string
}

// Registry agent 注册表：名字 → Agent 实例。
type Registry struct {
	agents map[string]Agent
	order  []string
	// OnRun 每次经本 Registry 发起的运行结束后的回调（nil 安全）。
	// 观测入口：接结构化日志/Prometheus/trace 均可，本包不硬依赖任何观测后端。
	OnRun func(RunEvent)
}

// NewRegistry 创建注册表并注册缺省 agent
// （claude/zcode/codex/opencode/minimax/kimi/gemini/qwen/mimo）。
func NewRegistry() *Registry {
	r := &Registry{agents: map[string]Agent{}}
	r.Register(NewClaudeCode())
	r.Register(NewZCode())
	r.Register(NewCodex())
	r.Register(NewOpenCode())
	r.Register(NewMinimax())
	r.Register(NewKimi())
	r.Register(NewGemini())
	r.Register(NewQwen())
	r.Register(NewMimo())
	return r
}

// Register 注册 agent（重名覆盖）。
func (r *Registry) Register(a Agent) {
	if _, exists := r.agents[a.Name()]; !exists {
		r.order = append(r.order, a.Name())
	}
	r.agents[a.Name()] = a
}

// Get 按名查找。
func (r *Registry) Get(name string) (Agent, bool) {
	a, ok := r.agents[name]
	return a, ok
}

// Names 返回全部注册名（注册序）。
func (r *Registry) Names() []string {
	return append([]string(nil), r.order...)
}

// Run 按名执行（结束后发 RunEvent）。
func (r *Registry) Run(ctx context.Context, name string, req RunRequest) (*RunResult, error) {
	a, ok := r.agents[name]
	if !ok {
		return nil, fmt.Errorf("acpx: 未注册的 agent %q（可用: %s）", name, strings.Join(r.order, ","))
	}
	start := time.Now()
	res, err := a.Run(ctx, req)
	if r.OnRun != nil {
		ev := RunEvent{Agent: name, Duration: time.Since(start), Err: err}
		if res != nil {
			ev.TextLen = len(res.Text)
			ev.Usage = res.Usage
			ev.SessionID = res.SessionID
		}
		r.OnRun(ev)
	}
	return res, err
}

// AsTool 把注册表包成 eino 工具（run_coding_agent），供 ReAct agent 自主调用。
// 与 rag.KnowledgeService.AsTool 同范式。
func (r *Registry) AsTool() tool.BaseTool {
	t, err := utils.InferTool("run_coding_agent",
		"调用 CLI 编码 agent（claude/zcode/codex/opencode/minimax/kimi/gemini/qwen/mimo）"+
			"执行编码任务（写代码/改代码/跑命令/审查等）。prompt 要具体明确，包含期望产出与验收标准。",
		func(ctx context.Context, in *toolIn) (*toolOut, error) {
			// 透传 eino 传入的 ctx：上层取消/超时必须能终止 CLI 子进程，
			// 否则被放弃的运行会占满超时窗口与并发槽位
			res, err := r.Run(ctx, in.Agent, RunRequest{
				Prompt:  in.Prompt,
				WorkDir: in.WorkDir,
			})
			if err != nil {
				return &toolOut{Error: err.Error()}, nil
			}
			return &toolOut{Text: res.Text, SessionID: res.SessionID}, nil
		})
	if err != nil {
		return nil
	}
	return t
}
