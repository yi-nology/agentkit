package acpx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 编译期断言。
var _ Agent = (*ClaudeCode)(nil)

// ---------- Claude Code / ZCode（同族协议） ----------

// ClaudeCode Claude Code 适配器（headless：`claude -p --output-format stream-json`）。
type ClaudeCode struct {
	Bin string // 默认 "claude"

	name string // 注册身份（构造器填充）；空 = 按 Bin 推导兜底
}

// NewClaudeCode 创建 Claude Code 适配器。
func NewClaudeCode() *ClaudeCode { return &ClaudeCode{Bin: "claude", name: "claude"} }

// NewZCode 创建 ZCode 适配器（CLI 参数协议与 Claude Code 同族）。
func NewZCode() *ClaudeCode { return &ClaudeCode{Bin: "zcode", name: "zcode"} }

func (c *ClaudeCode) Name() string {
	if c.name != "" {
		return c.name
	}
	if c.Bin == "zcode" {
		return "zcode"
	}
	return "claude"
}

// Capabilities 全量支持：--model / --resume / --max-turns / --allowedTools / --permission-mode。
func (c *ClaudeCode) Capabilities() Capability {
	return Capability{Model: true, Session: true, MaxTurns: true, AllowedTools: true, Sandbox: true}
}

// Run 执行 headless 任务（stream-json NDJSON 事件流解析）。
func (c *ClaudeCode) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	argv := []string{c.Bin, "-p", promptArg(req.Prompt), "--output-format", "stream-json", "--verbose"}
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
		// claude 族语义：空格分隔的单参数。工具名本身含空格会静默变形——
		// 调用方需保证名字合法（工具名规范不含空格）
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
			if ev.Message != nil {
				for _, blk := range ev.Message.Content {
					switch blk.Type {
					case "text":
						emitText(req, blk.Text, line)
					case "thinking":
						if req.OnEvent != nil {
							req.OnEvent(Event{Type: EventThinking, Text: blk.Thinking, Raw: json.RawMessage(line)})
						}
					case "tool_use":
						if req.OnEvent != nil {
							req.OnEvent(Event{Type: EventToolCall, Tool: blk.Name, Raw: json.RawMessage(line)})
						}
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
				// is_error result 全量转发（transcript 可排障）；成功/失败判定不动——
				// 非 0 退出已有"result 优先"策略，0 退出 + is_error 的处置归消费方
				typ := EventResult
				if ev.IsError {
					typ = EventError
				}
				req.OnEvent(Event{Type: typ, Text: ev.Result, Raw: json.RawMessage(line)})
			}
		}
	}

	stdout, code, err := runCLI(ctx, req, argv, onLine)
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
		result = fallbackResult(stdout, code)
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
	Result       string     `json:"result"`
	IsError      bool       `json:"is_error"`
	TotalCostUSD float64    `json:"total_cost_usd"`
	Usage        *tokenPair `json:"usage"`
}
