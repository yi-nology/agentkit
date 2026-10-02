package acpx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

	argv := []string{c.Bin, "exec", promptArg(req.Prompt), "--json", "--output-last-message", lastMsg}
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
	var errMsg string
	onLine := func(line string) {
		var ev codexEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return
		}
		switch ev.Type {
		case "error":
			// 运行期错误全量转发（transcript 可排障）并记录——codex 部分错误形态
			// exit 仍为 0，结束时如实报错（与 mimo/gemini 同纪律）
			if ev.Message != "" {
				errMsg = ev.Message
				emitError(req, ev.Message, line)
			}
		case "item.completed":
			if ev.Item != nil && ev.Item.ItemType == "assistant_message" && ev.Item.Text != "" {
				lastText = ev.Item.Text
				emitText(req, ev.Item.Text, line)
			}
		case "turn.completed":
			// 多轮任务逐轮累加（与 mimo 适配器一致，只取最后一轮会少计）
			if ev.Usage != nil {
				usage.InputTokens += ev.Usage.InputTokens
				usage.OutputTokens += ev.Usage.OutputTokens
			}
		}
	}

	stdout, code, err := runCLI(ctx, req, argv, onLine)
	if err != nil {
		// 与 claude 族一致：output-last-message 已有最终消息（agent 正常完成但
		// 退出码非零）时以结果为准
		if text := strings.TrimSpace(readFileTrim(lastMsg)); text != "" {
			return &RunResult{Text: text, Usage: usage, ExitCode: code, Raw: []byte(stdout)}, nil
		}
		return nil, err
	}
	if errMsg != "" && lastText == "" {
		return nil, fmt.Errorf("acpx: codex 运行错误: %s", errMsg)
	}
	text := strings.TrimSpace(readFileTrim(lastMsg))
	if text == "" {
		text = lastText
	}
	return &RunResult{Text: text, Usage: usage, ExitCode: code, Raw: []byte(stdout)}, nil
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
