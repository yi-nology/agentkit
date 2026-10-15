package acpx

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yi-nology/agentkit/textutil"
)

// 编译期断言。
var _ Agent = (*Pi)(nil)

// ---------- Pi（pi-coding-agent，真实 CLI 核实） ----------

// Pi pi-coding-agent 适配器（`pi --mode json -p --no-session`，参数经本机真实
// CLI 核实，2026-09-28 实弹采样）。
//
// 协议要点：
//   - `pi --mode json -p "<prompt>"` 输出 JSONL 事件流：
//     {"type":"message_start","message":{"role":...,"provider":...,"model":...}}
//     {"type":"message_update","assistantMessageEvent":{"type":"text_delta",...}}
//     {"type":"message_end","message":{"role":"assistant","content":[{"type":
//     "thinking",...},{"type":"text","text":"..."}],"usage":{"input","output",
//     "totalTokens","cost":{...}},"stopReason":...}}
//     {"type":"turn_end"| "agent_end" | "agent_settled" | "session" | ...}
//     文本取 assistant message_end 的 "text" 内容块（thinking 块不计入正文）；
//     usage 含 input/output/reasoning 与 cost（多轮 message_end 后者覆盖前者）。
//   - `-p/--print` 非交互执行；`--no-session` 不落会话（acpx 语义：每次 Run =
//     独立任务，不在 ~/.pi 留状态）；扩展/技能/AGENTS.md 发现保持本机默认
//     （编码任务需要这些环境）。
//   - `--model provider/id[:thinking]` 覆盖模型（支持 provider/id 形态）。
//   - 中止形态（turn_abort 等）无 assistant message_end：有 exit!=0 时以退出错
//     误为准；exit=0 且事件流含 abort 类事件时如实报错（不静默空成功）。
type Pi struct {
	Bin string // 默认 "pi"
	// DefaultModel 请求未指定 Model 时的缺省（provider/id 形态）。空 = 尊重
	// 本机 CLI 配置。
	DefaultModel string

	namedAgent
}

// NewPi 创建 Pi 适配器。
func NewPi() *Pi { return &Pi{Bin: "pi", namedAgent: namedAgent{name: "pi", fallback: "pi"}} }

// Capabilities 支持 Model（--model）与 Session（--session-id 精确续聊，
// 缺省则 --no-session 一次性）；沙箱/轮数/工具白名单无对应参数。
func (p *Pi) Capabilities() Capability {
	return Capability{Model: true, Session: true}
}

type piEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"id"`
	Message   *struct {
		Role    string `json:"role"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *struct {
			Input  int `json:"input"`
			Output int `json:"output"`
			Cost   *struct {
				Total float64 `json:"total"`
			} `json:"cost"`
		} `json:"usage"`
	} `json:"message"`
	Error json.RawMessage `json:"error"`
}

// Run 执行 pi（--mode json 事件流解析）。
func (p *Pi) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	st := &piStream{req: req}
	stdout, stderr, code, err := runCLI(ctx, req, p.buildArgv(req), st.handleLine)
	return st.finalize(stdout, stderr, code, err)
}

// buildArgv 构造 pi CLI 参数（协议面固定：--mode json -p 事件流；argv 语义不改）。
func (p *Pi) buildArgv(req RunRequest) []string {
	argv := []string{p.exec(p.Bin), "--mode", "json", "-p"}
	if req.SessionID != "" {
		// 精确续聊：--session-id 按项目会话 ID 续（不存在则创建）
		argv = append(argv, "--session-id", req.SessionID)
	} else {
		// 一次性任务：不落会话（不在 ~/.pi 留状态）
		argv = append(argv, "--no-session")
	}
	if m := req.Model; m != "" {
		argv = append(argv, "--model", m)
	} else if p.DefaultModel != "" {
		argv = append(argv, "--model", p.DefaultModel)
	}
	return append(argv, promptArg(req.Prompt))
}

// piStream 事件流解析状态（handleLine 在 stdout 读取 goroutine 中同步执行）。
type piStream struct {
	req          RunRequest
	usage        Usage
	cost         float64
	lastText     string
	sessionID    string
	sawAssistant bool
	abortLine    string
}

// handleLine 单行 JSONL 事件解析（非事件行忽略）。
func (s *piStream) handleLine(line string) {
	var ev piEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return // 非事件行（插件告警等到 stderr，stdout 内杂行忽略）
	}
	if ev.Type == "session" && ev.SessionID != "" {
		s.sessionID = ev.SessionID
	}
	switch {
	case ev.Type == "message_end" && ev.Message != nil && ev.Message.Role == "assistant":
		s.onAssistantEnd(&ev, line)
	case strings.Contains(ev.Type, "abort") || ev.Type == "error":
		// 中止/错误事件全量转发（transcript 排障）并记录——pi 中止时
		// 可能 exit 仍为 0，结束时如实报错
		if s.abortLine == "" {
			s.abortLine = line
		}
		// "error":null 的事件不转发（字面 "null" 文案对 transcript 是噪音）
		if msg := strings.TrimSpace(string(ev.Error)); msg != "" && msg != "null" {
			emitError(s.req, msg, line)
		}
	}
}

// onAssistantEnd assistant 终态：text 块拼正文、usage/cost 落状态并转发。
func (s *piStream) onAssistantEnd(ev *piEvent, line string) {
	s.sawAssistant = true
	var b strings.Builder
	for _, blk := range ev.Message.Content {
		if blk.Type == "text" && blk.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(blk.Text)
		}
	}
	s.lastText = b.String()
	if u := ev.Message.Usage; u != nil {
		s.usage.InputTokens = u.Input
		s.usage.OutputTokens = u.Output
		if u.Cost != nil {
			s.cost = u.Cost.Total
		}
	}
	if s.lastText != "" {
		emitText(s.req, s.lastText, line)
	}
}

// finalize 收尾装配（三段式骨架）：中止仅在无终态时如实报错（pi 中止形态
// 可能 exit 仍为 0）；assistant message_end 到达即权威——文本可为空（纯
// thinking 收尾/末轮以工具调用结束不是空壳，曾以 lastText=="" 误触兜底把
// 整段 JSONL 事件流当正文返回）；无终态全文兜底。
func (s *piStream) finalize(stdout, stderr string, code int, err error) (*RunResult, error) {
	var streamErr error
	if s.abortLine != "" && !s.sawAssistant {
		streamErr = streamError("pi", "中止", textutil.TruncEllipsis(s.abortLine, 400))
	}
	s.usage.CostUSD = s.cost
	return finalizeStream(stdout, stderr, code, err,
		&RunResult{Text: s.lastText, SessionID: s.sessionID, Usage: s.usage},
		s.sawAssistant, streamErr)
}
