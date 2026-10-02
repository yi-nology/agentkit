package acpx

import (
	"context"
	"encoding/json"
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
			// 多轮任务逐轮累加（与 mimo 适配器一致，只取最后一轮会少计）
			if ev.Usage != nil {
				usage.InputTokens += ev.Usage.InputTokens
				usage.OutputTokens += ev.Usage.OutputTokens
			}
		}
	}

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), onLine, 0)
	if err != nil {
		// 与 claude 族一致：output-last-message 已有最终消息（agent 正常完成但
		// 退出码非零）时以结果为准
		if text := strings.TrimSpace(readFileTrim(lastMsg)); text != "" {
			return &RunResult{Text: text, Usage: usage, ExitCode: code, Raw: []byte(stdout)}, nil
		}
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
