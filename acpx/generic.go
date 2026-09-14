package acpx

import (
	"context"
	"encoding/json"
	"strings"
)

// 编译期断言。
var _ Agent = (*GenericAgent)(nil)

// ---------- GenericAgent（ZCode/MiniMax/任意 CLI 的通用出口） ----------

// GenericAgent 通用 CLI agent：以显式 argv 模板驱动任意 agent。
// 模板占位符：{prompt}（原样单参传入，无 shell 注入面）、{model}、{session}。
// IsJSON=true 时按 {"text","sessionID","tokens":{"input","output"}} 解析输出。
//
// 用途：CLI 参数协议未稳定（MiniMax）或私有 agent 的快速接入；
// 协议稳定后应收敛为专用适配器。
type GenericAgent struct {
	AgentName string   // agent 标识（Name() 返回值）
	Argv      []string // argv 模板
	IsJSON    bool
}

// NewGenericAgent 创建通用适配器。
func NewGenericAgent(name string, argv []string, isJSON bool) *GenericAgent {
	return &GenericAgent{AgentName: name, Argv: argv, IsJSON: isJSON}
}

// NewMinimax MiniMax 编码 agent 预设（CLI 约定待官方稳定，当前走通用模板）。
func NewMinimax() *GenericAgent {
	return NewGenericAgent("minimax", []string{"minimax-code", "run", "{prompt}"}, false)
}

func (g *GenericAgent) Name() string {
	if g.AgentName != "" {
		return g.AgentName
	}
	return "generic"
}

// Run 执行通用 CLI agent。
func (g *GenericAgent) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	var argv []string
	for _, a := range g.Argv {
		switch a {
		case "{prompt}":
			argv = append(argv, promptArg(req.Prompt))
		case "{model}":
			if req.Model != "" {
				argv = append(argv, req.Model)
			}
		case "{session}":
			if req.SessionID != "" {
				argv = append(argv, req.SessionID)
			}
		default:
			argv = append(argv, a)
		}
	}
	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), nil, 0)
	if err != nil {
		return nil, err
	}
	if !g.IsJSON {
		return &RunResult{Text: strings.TrimSpace(stdout), ExitCode: code, Raw: []byte(stdout)}, nil
	}
	var out openCodeOut
	if err := json.Unmarshal([]byte(extractJSONObj(stdout)), &out); err != nil || out.Text == "" {
		return &RunResult{Text: strings.TrimSpace(stdout), ExitCode: code, Raw: []byte(stdout)}, nil
	}
	r := &RunResult{Text: out.Text, SessionID: out.SessionID, ExitCode: code, Raw: []byte(stdout)}
	if out.Tokens != nil {
		r.Usage = Usage{InputTokens: out.Tokens.Input, OutputTokens: out.Tokens.Output}
	}
	return r, nil
}
