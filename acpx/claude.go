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
	st := &claudeStream{req: req}
	stdout, code, err := runCLI(ctx, req, c.buildArgv(req), st.handleLine)
	return st.finalize(stdout, code, err)
}

// buildArgv 构造 claude 族 CLI 参数（协议面固定：-p + stream-json 事件流）。
func (c *ClaudeCode) buildArgv(req RunRequest) []string {
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
	if v, ok := mapSandbox(req.Sandbox, "plan", "acceptEdits", "bypassPermissions"); ok {
		argv = append(argv, "--permission-mode", v)
	}
	return argv
}

// claudeStream 事件流解析状态（handleLine 在 stdout 读取 goroutine 中同步执行）。
type claudeStream struct {
	req       RunRequest
	result    *RunResult
	sawResult bool // result 事件是否到达（init-only 空壳不具备终态语义）
}

// handleLine 单行 stream-json 事件解析（非事件行忽略）。
func (s *claudeStream) handleLine(line string) {
	var ev claudeEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return // 非事件行（警告/日志）忽略
	}
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" && ev.SessionID != "" && (s.result == nil || s.result.SessionID == "") {
			if s.result == nil {
				s.result = &RunResult{}
			}
			s.result.SessionID = ev.SessionID
		}
	case "assistant":
		s.onAssistant(&ev, line)
	case "result":
		s.onResult(&ev, line)
	}
}

// onAssistant assistant 消息：text/thinking/tool_use 三类内容块转发。
func (s *claudeStream) onAssistant(ev *claudeEvent, line string) {
	if ev.Message == nil {
		return
	}
	for _, blk := range ev.Message.Content {
		switch blk.Type {
		case "text":
			emitText(s.req, blk.Text, line)
		case "thinking":
			emit(s.req, Event{Type: EventThinking, Text: blk.Thinking, Raw: json.RawMessage(line)})
		case "tool_use":
			emit(s.req, Event{Type: EventToolCall, Tool: blk.Name, Raw: json.RawMessage(line)})
		}
	}
}

// onResult 终态事件：结果/用量落状态机并全量转发（is_error → EventError）。
func (s *claudeStream) onResult(ev *claudeEvent, line string) {
	s.sawResult = true // 终态事件真的到达过（区分 init-only 空壳）
	r := &RunResult{Text: ev.Result, SessionID: ev.SessionID}
	if ev.Usage != nil {
		r.Usage = Usage{InputTokens: ev.Usage.InputTokens, OutputTokens: ev.Usage.OutputTokens, CostUSD: ev.TotalCostUSD}
	}
	if r.SessionID == "" && s.result != nil {
		r.SessionID = s.result.SessionID
	}
	s.result = r
	// is_error result 全量转发（transcript 可排障）；成功/失败判定不动——
	// 非 0 退出已有"result 优先"策略，0 退出 + is_error 的处置归消费方
	typ := EventResult
	if ev.IsError {
		typ = EventError
	}
	emit(s.req, Event{Type: typ, Text: ev.Result, Raw: json.RawMessage(line)})
}

// finalize 收尾装配（三段式骨架）：终态 = result 事件真的到达（init-only
// 空壳不具备终态语义）；终态优先无条件于文本非空（空 result 文本是合法成功
// ——is_error 的处置归消费方，经 EventError 转发）；无终态全文兜底。
func (s *claudeStream) finalize(stdout string, code int, err error) (*RunResult, error) {
	return finalizeStream(stdout, code, err, s.result, s.sawResult && s.result != nil, nil)
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
