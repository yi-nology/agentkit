package acpx

import (
	"context"
	"encoding/json"
	"strings"
)

// 编译期断言。
var _ Agent = (*Kimi)(nil)

// Kimi Kimi Code CLI 适配器（MoonshotAI/kimi-code，参数经本机真实 CLI --help 核实）。
//
// 协议要点（非交互模式，headless）：
//   - `kimi -p "<prompt>"` 非交互执行（本身即全自主；不可与 --auto 组合——真实 CLI 实测报错）
//   - `--output-format stream-json` 输出 JSONL 消息流：{"role":"assistant","content":"..."}
//   - `-m/--model NAME` 覆盖模型；`-S/--session ID` 续聊
//
// 注：文档站描述的旧版 kimi-cli 用 `--print`；新一代 kimi-code 已改为 `-p` 直跑。
type Kimi struct {
	Bin string // 默认 "kimi"
}

// NewKimi 创建 Kimi 适配器。
func NewKimi() *Kimi { return &Kimi{Bin: "kimi"} }

func (k *Kimi) Name() string { return "kimi" }

// Capabilities 支持 Model/Session（--model / --session）；沙箱/轮数/工具白名单无对应参数
// （非交互模式本身即全自主——传 Sandbox 会被拒绝而非裸跑）。
func (k *Kimi) Capabilities() Capability {
	return Capability{Model: true, Session: true}
}

// Run 执行 Kimi print 模式任务。
func (k *Kimi) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	argv := []string{k.Bin, "-p", promptArg(req.Prompt), "--output-format", "stream-json"}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	if req.SessionID != "" {
		argv = append(argv, "--session", req.SessionID)
	}

	var lastAssistant string
	onLine := func(line string) {
		var msg kimiMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return // 非消息行（日志/警告）忽略
		}
		if msg.Role == "assistant" && msg.Content != "" {
			lastAssistant = msg.Content
			emitText(req, msg.Content, line)
		}
	}

	stdout, code, err := runCLI(ctx, req, argv, onLine)
	if err != nil {
		return nil, err
	}
	if lastAssistant == "" {
		// stream-json 不可用（旧版本/文本模式）：全文兜底
		lastAssistant = strings.TrimSpace(stdout)
	}
	return &RunResult{Text: lastAssistant, ExitCode: code, Raw: []byte(stdout)}, nil
}

// kimiMessage stream-json 消息行。
type kimiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
