package acpx

import (
	"context"
	"encoding/json"
)

// 编译期断言。
var _ Agent = (*Codex)(nil)

// ---------- Codex ----------

// Codex OpenAI Codex CLI 适配器（`codex exec --json --output-last-message <file>`）。
type Codex struct {
	Bin string // 默认 "codex"
}

// NewCodex 创建 Codex 适配器。
func NewCodex() *Codex { return &Codex{Bin: "codex"} }

func (c *Codex) Name() string { return "codex" }

// Capabilities 支持 Model/Sandbox（-m / --sandbox）；exec 无续聊、轮数与工具白名单无对应参数。
func (c *Codex) Capabilities() Capability {
	return Capability{Model: true, Sandbox: true}
}

// Run 执行 Codex exec 任务。
// 最终文本优先取 --output-last-message 文件（官方保证的最终消息），事件流兜底。
func (c *Codex) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	lastMsg, cleanup, err := tempFile("acpx-codex-last-*")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	st := &codexStream{req: req, lastMsg: lastMsg}
	stdout, stderr, code, err := runCLI(ctx, req, c.buildArgv(req, lastMsg), st.handleLine)
	return st.finalize(stdout, stderr, code, err)
}

// buildArgv 构造 codex CLI 参数（协议面固定：exec --json --output-last-message；argv 语义不改）。
func (c *Codex) buildArgv(req RunRequest, lastMsg string) []string {
	argv := []string{c.Bin, "exec", promptArg(req.Prompt), "--json", "--output-last-message", lastMsg}
	if req.Model != "" {
		argv = append(argv, "-m", req.Model)
	}
	if req.WorkDir != "" {
		argv = append(argv, "-C", req.WorkDir)
	}
	if v, ok := mapSandbox(req.Sandbox, "read-only", "workspace-write", "danger-full-access"); ok {
		argv = append(argv, "--sandbox", v)
	}
	return argv
}

// codexStream 事件流解析状态（handleLine 在 stdout 读取 goroutine 中同步执行）。
type codexStream struct {
	req      RunRequest
	lastMsg  string
	lastText string
	usage    Usage
	errMsg   string
}

// handleLine 单行 JSONL 事件解析（非事件行忽略）。
func (s *codexStream) handleLine(line string) {
	var ev codexEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return
	}
	switch ev.Type {
	case "error":
		// 运行期错误全量转发（transcript 可排障）并记录——codex 部分错误形态
		// exit 仍为 0，结束时如实报错（与 mimo/gemini 同纪律）
		if ev.Message != "" {
			s.errMsg = ev.Message
			emitError(s.req, ev.Message, line)
		}
	case "item.completed":
		if ev.Item != nil && ev.Item.ItemType == "assistant_message" && ev.Item.Text != "" {
			s.lastText = ev.Item.Text
			emitText(s.req, ev.Item.Text, line)
		}
	case "turn.completed":
		// 多轮任务逐轮累加（与 mimo 适配器一致，只取最后一轮会少计）
		if ev.Usage != nil {
			s.usage.InputTokens += ev.Usage.InputTokens
			s.usage.OutputTokens += ev.Usage.OutputTokens
		}
	}
}

// finalize 收尾装配：output-last-message 优先、事件流 lastText 兜底；错误如实上抛。
func (s *codexStream) finalize(stdout, stderr string, code int, err error) (*RunResult, error) {
	if err != nil {
		// 与 claude 族一致：output-last-message 已有最终消息（agent 正常完成但
		// 退出码非零）时以结果为准
		if text := readFileTrim(s.lastMsg); text != "" {
			return &RunResult{Text: text, Usage: s.usage, ExitCode: code, Raw: []byte(stdout), Stderr: stderrTail(stderr)}, nil
		}
		return nil, err
	}
	// 有错如实报错（对齐 mimo 纪律及其回归测试）：exit=0 但事件流含 error 的
	// 部分输出跑（配额耗尽/流中断后吐了些文本）不得当成功——lastText 条件
	// 曾让这类跑静默丢错
	if err := streamError("codex", "运行错误", s.errMsg); err != nil {
		return nil, err
	}
	text := readFileTrim(s.lastMsg)
	if text == "" {
		text = s.lastText
	}
	return &RunResult{Text: text, Usage: s.usage, ExitCode: code, Raw: []byte(stdout), Stderr: stderrTail(stderr)}, nil
}

type codexEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"` // error 事件详情
	Item    *struct {
		ItemType string `json:"item_type"`
		Text     string `json:"text"`
	} `json:"item"`
	Usage *tokenPair `json:"usage"`
}
