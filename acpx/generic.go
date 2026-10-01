package acpx

import (
	"context"
	"strings"
)

// 编译期断言。
var _ Agent = (*GenericAgent)(nil)

// ---------- GenericAgent（ZCode/MiniMax/任意 CLI 的通用出口） ----------

// GenericAgent 通用 CLI agent：以显式 argv 模板驱动任意 agent。
// 模板占位符：{prompt}（原样单参传入，无 shell 注入面）、{model}、{session}。
// flag+占位符是**条件单元**：{model}/{session} 缺值时整体移除——连带移除紧邻的
// 前一个以 "-" 开头的参数（如 ["run","--model","{model}"] 在无 Model 时展开为
// ["run"]，绝不产生吞掉后续位置参数的悬空 flag）。
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

// NewDsh dsh（DeepSeek Harness）预设：headless profile 单任务出口——
// `dsh --profile headless "<task>"` 打印最终 assistant 消息后退出（2026-09-28
// 本机真实 CLI 核实：stdout 即最终答复、exit 0；无 JSON/会话/模型参数面）。
func NewDsh() *GenericAgent {
	return NewGenericAgent("dsh", []string{"dsh", "--profile", "headless", "{prompt}"}, false)
}

func (g *GenericAgent) Name() string {
	if g.AgentName != "" {
		return g.AgentName
	}
	return "generic"
}

// Capabilities 由 argv 模板推导：含 {model} 占位 → 支持 Model，含 {session} →
// 支持 Session；模板机制无法表达轮数/工具白名单/沙箱，一律不支持。
func (g *GenericAgent) Capabilities() Capability {
	var caps Capability
	for _, a := range g.Argv {
		switch a {
		case "{model}":
			caps.Model = true
		case "{session}":
			caps.Session = true
		}
	}
	return caps
}

// Run 执行通用 CLI agent。
func (g *GenericAgent) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	var argv []string
	for _, a := range g.Argv {
		switch a {
		case "{prompt}":
			argv = append(argv, promptArg(req.Prompt))
		case "{model}":
			if req.Model == "" {
				argv = dropFlagPair(argv)
				continue
			}
			argv = append(argv, req.Model)
		case "{session}":
			if req.SessionID == "" {
				argv = dropFlagPair(argv)
				continue
			}
			argv = append(argv, req.SessionID)
		default:
			argv = append(argv, a)
		}
	}
	stdout, code, err := runCLI(ctx, req, argv, nil)
	if err != nil {
		return nil, err
	}
	if !g.IsJSON {
		return fallbackResult(stdout, code), nil
	}
	return envelopeResult(req, g.Name(), stdout, code)
}

// dropFlagPair 占位符缺值时连带移除紧邻的 flag 参数：模板中 "--model {model}"
// 是一对条件单元，只删值留 flag 会产生悬空 flag 吞掉后续位置参数（prompt）。
func dropFlagPair(argv []string) []string {
	if len(argv) > 0 && strings.HasPrefix(argv[len(argv)-1], "-") {
		return argv[:len(argv)-1]
	}
	return argv
}
