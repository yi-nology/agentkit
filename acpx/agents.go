package acpx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 编译期断言：全部适配器满足 Agent 接口。
var (
	_ Agent = (*ClaudeCode)(nil)
	_ Agent = (*Codex)(nil)
	_ Agent = (*OpenCode)(nil)
	_ Agent = (*GenericAgent)(nil)
)

// ---------- Claude Code / ZCode（同族协议） ----------

// ClaudeCode Claude Code 适配器（headless：`claude -p --output-format stream-json`）。
type ClaudeCode struct {
	Bin string // 默认 "claude"
}

// NewClaudeCode 创建 Claude Code 适配器。
func NewClaudeCode() *ClaudeCode { return &ClaudeCode{Bin: "claude"} }

// NewZCode 创建 ZCode 适配器（CLI 参数协议与 Claude Code 同族）。
func NewZCode() *ClaudeCode { return &ClaudeCode{Bin: "zcode"} }

func (c *ClaudeCode) Name() string {
	if c.Bin == "zcode" {
		return "zcode"
	}
	return "claude"
}

// Run 执行 headless 任务（stream-json NDJSON 事件流解析）。
func (c *ClaudeCode) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	argv := []string{c.Bin, "-p", req.Prompt, "--output-format", "stream-json", "--verbose"}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	if req.SessionID != "" {
		argv = append(argv, "--resume", req.SessionID)
	}
	if req.MaxTurns > 0 {
		argv = append(argv, "--max-turns", fmt.Sprint(req.MaxTurns))
	}
	if len(req.AllowedTools) > 0 {
		argv = append(argv, "--allowedTools", strings.Join(req.AllowedTools, " "))
	}
	switch req.Sandbox {
	case SandboxReadonly:
		argv = append(argv, "--permission-mode", "plan")
	case SandboxWorkspace:
		argv = append(argv, "--permission-mode", "acceptEdits")
	case SandboxFull:
		argv = append(argv, "--permission-mode", "bypassPermissions")
	}

	var result *RunResult
	onLine := func(line string) {
		var ev claudeEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return // 非事件行（警告/日志）忽略
		}
		switch ev.Type {
		case "system":
			if ev.Subtype == "init" && ev.SessionID != "" && (result == nil || result.SessionID == "") {
				if result == nil {
					result = &RunResult{}
				}
				result.SessionID = ev.SessionID
			}
		case "assistant":
			if ev.Message != nil && req.OnEvent != nil {
				for _, blk := range ev.Message.Content {
					switch blk.Type {
					case "text":
						req.OnEvent(Event{Type: EventText, Text: blk.Text, Raw: json.RawMessage(line)})
					case "thinking":
						req.OnEvent(Event{Type: EventThinking, Text: blk.Thinking, Raw: json.RawMessage(line)})
					case "tool_use":
						req.OnEvent(Event{Type: EventToolCall, Tool: blk.Name, Raw: json.RawMessage(line)})
					}
				}
			}
		case "result":
			r := &RunResult{Text: ev.Result, SessionID: ev.SessionID}
			if ev.Usage != nil {
				r.Usage = Usage{InputTokens: ev.Usage.InputTokens, OutputTokens: ev.Usage.OutputTokens, CostUSD: ev.TotalCostUSD}
			}
			if r.SessionID == "" && result != nil {
				r.SessionID = result.SessionID
			}
			result = r
			if req.OnEvent != nil {
				req.OnEvent(Event{Type: EventResult, Text: ev.Result, Raw: json.RawMessage(line)})
			}
		}
	}

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), onLine)
	if err != nil {
		// result 事件已到达（agent 正常完成但进程退出码非零）：以结果为准
		if result != nil && result.Text != "" {
			result.ExitCode = code
			result.Raw = []byte(stdout)
			return result, nil
		}
		return nil, err
	}
	if result == nil {
		// 无 stream-json 输出（旧版本/输出格式变更）：全文当文本兜底
		result = &RunResult{Text: strings.TrimSpace(stdout)}
	}
	result.ExitCode = code
	result.Raw = []byte(stdout)
	return result, nil
}

// claudeEvent stream-json 事件（容错解析：只取需要的字段）。
type claudeEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Message   *struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
			Name     string `json:"name"`
		} `json:"content"`
	} `json:"message"`
	Result       string  `json:"result"`
	IsError      bool    `json:"is_error"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ---------- Codex ----------

// Codex OpenAI Codex CLI 适配器（`codex exec --json --output-last-message <file>`）。
type Codex struct {
	Bin string // 默认 "codex"
}

// NewCodex 创建 Codex 适配器。
func NewCodex() *Codex { return &Codex{Bin: "codex"} }

func (c *Codex) Name() string { return "codex" }

// Run 执行 Codex exec 任务。
// 最终文本优先取 --output-last-message 文件（官方保证的最终消息），事件流兜底。
func (c *Codex) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	lastMsg, cleanup, err := tempFile("acpx-codex-last-*")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	argv := []string{c.Bin, "exec", req.Prompt, "--json", "--output-last-message", lastMsg}
	if req.Model != "" {
		argv = append(argv, "-m", req.Model)
	}
	if req.WorkDir != "" {
		argv = append(argv, "-C", req.WorkDir)
	}
	switch req.Sandbox {
	case SandboxReadonly:
		argv = append(argv, "--sandbox", "read-only")
	case SandboxWorkspace:
		argv = append(argv, "--sandbox", "workspace-write")
	case SandboxFull:
		argv = append(argv, "--sandbox", "danger-full-access")
	}

	var lastText string
	var usage Usage
	onLine := func(line string) {
		var ev codexEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return
		}
		switch ev.Type {
		case "item.completed":
			if ev.Item != nil && ev.Item.ItemType == "assistant_message" && ev.Item.Text != "" {
				lastText = ev.Item.Text
				if req.OnEvent != nil {
					req.OnEvent(Event{Type: EventText, Text: ev.Item.Text, Raw: json.RawMessage(line)})
				}
			}
		case "turn.completed":
			if ev.Usage != nil {
				usage = Usage{InputTokens: ev.Usage.InputTokens, OutputTokens: ev.Usage.OutputTokens}
			}
		}
	}

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), onLine)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(readFileTrim(lastMsg))
	if text == "" {
		text = lastText
	}
	return &RunResult{Text: text, Usage: usage, ExitCode: code, Raw: []byte(stdout)}, nil
}

type codexEvent struct {
	Type string `json:"type"`
	Item *struct {
		ItemType string `json:"item_type"`
		Text     string `json:"text"`
	} `json:"item"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ---------- OpenCode ----------

// OpenCode opencode 适配器（`opencode run --json`）。
type OpenCode struct {
	Bin string // 默认 "opencode"
}

// NewOpenCode 创建 opencode 适配器。
func NewOpenCode() *OpenCode { return &OpenCode{Bin: "opencode"} }

func (c *OpenCode) Name() string { return "opencode" }

// Run 执行 opencode run 任务（--session 续聊、-m provider/model）。
func (c *OpenCode) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	argv := []string{c.Bin, "run", req.Prompt, "--json"}
	if req.Model != "" {
		argv = append(argv, "-m", req.Model)
	}
	if req.SessionID != "" {
		argv = append(argv, "--session", req.SessionID)
	}

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), nil)
	if err != nil {
		return nil, err
	}
	var out openCodeOut
	if err := json.Unmarshal([]byte(extractJSONObj(stdout)), &out); err != nil || out.Text == "" {
		// 非 JSON 输出（版本差异/日志混入）：全文当文本兜底
		return &RunResult{Text: strings.TrimSpace(stdout), ExitCode: code, Raw: []byte(stdout)}, nil
	}
	r := &RunResult{Text: out.Text, SessionID: out.SessionID, ExitCode: code, Raw: []byte(stdout)}
	if out.Tokens != nil {
		r.Usage = Usage{InputTokens: out.Tokens.Input, OutputTokens: out.Tokens.Output}
	}
	return r, nil
}

// openCodeOut opencode --json 输出结构（同时被 GenericAgent JSON 模式复用）。
type openCodeOut struct {
	Text      string `json:"text"`
	SessionID string `json:"sessionID"`
	Tokens    *struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	} `json:"tokens"`
}

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
			argv = append(argv, req.Prompt)
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
	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), nil)
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
