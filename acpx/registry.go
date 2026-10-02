package acpx

import (
	"context"
	"fmt"
	"strings"

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

// Registry agent 注册表：名字 → Agent 实例。
type Registry struct {
	agents map[string]Agent
	order  []string
}

// NewRegistry 创建注册表并注册缺省 agent（claude/zcode/codex/opencode/minimax）。
func NewRegistry() *Registry {
	r := &Registry{agents: map[string]Agent{}}
	r.Register(NewClaudeCode())
	r.Register(NewZCode())
	r.Register(NewCodex())
	r.Register(NewOpenCode())
	r.Register(NewMinimax())
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

// Run 按名执行。
func (r *Registry) Run(ctx context.Context, name string, req RunRequest) (*RunResult, error) {
	a, ok := r.agents[name]
	if !ok {
		return nil, fmt.Errorf("acpx: 未注册的 agent %q（可用: %s）", name, strings.Join(r.order, ","))
	}
	return a.Run(ctx, req)
}

// AsTool 把注册表包成 eino 工具（run_coding_agent），供 ReAct agent 自主调用。
// 与 rag.KnowledgeService.AsTool 同范式。
func (r *Registry) AsTool() tool.BaseTool {
	t, err := utils.InferTool("run_coding_agent",
		"调用 CLI 编码 agent（claude/zcode/codex/opencode/minimax）执行编码任务（写代码/改代码/跑命令/审查等）。"+
			"prompt 要具体明确，包含期望产出与验收标准。",
		func(_ context.Context, in *toolIn) (*toolOut, error) {
			res, err := r.Run(context.Background(), in.Agent, RunRequest{
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
