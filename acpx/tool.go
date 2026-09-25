package acpx

import (
	"context"

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

// AsTool 把注册表包成 eino 工具（run_coding_agent），供 ReAct agent 自主调用。
// 与 rag.KnowledgeService.AsTool 同范式。
// InferTool 失败（模板/schema 问题，属配置期错误）返回 nil——调用方不得把返回值
// 不判空就挂进工具表。
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
