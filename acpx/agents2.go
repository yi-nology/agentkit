package acpx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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

// Run 执行 Kimi print 模式任务。
func (k *Kimi) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	argv := []string{k.Bin, "-p", req.Prompt, "--output-format", "stream-json"}
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
			if req.OnEvent != nil {
				req.OnEvent(Event{Type: EventText, Text: msg.Content, Raw: json.RawMessage(line)})
			}
		}
	}

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), onLine)
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

// ---------- Gemini CLI / Qwen Code（同族协议） ----------

// Gemini Gemini CLI 适配器（google-gemini/gemini-cli；Qwen Code 为其 fork，参数同族）。
//
// 协议要点（非交互模式）：
//   - `gemini -p "<prompt>" --output-format json` 输出单 JSON 对象：
//     {"response":"...","stats":{"models":{"<model>":{"tokens":{"prompt":N,"candidates":N}}}}}
//   - `-m/--model`；`--approval-mode default|auto_edit|yolo`（沙箱映射）
type Gemini struct {
	Bin string // 默认 "gemini"
}

// NewGemini 创建 Gemini CLI 适配器。
func NewGemini() *Gemini { return &Gemini{Bin: "gemini"} }

// NewQwen 创建 Qwen Code 适配器（gemini-cli fork，参数同族）。
func NewQwen() *Gemini { return &Gemini{Bin: "qwen"} }

func (g *Gemini) Name() string {
	if g.Bin == "qwen" {
		return "qwen"
	}
	return "gemini"
}

// geminiOut --output-format json 结构（容错解析：只取需要的字段）。
type geminiOut struct {
	Response string `json:"response"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
	Stats *struct {
		Models map[string]struct {
			Tokens struct {
				Prompt     int `json:"prompt"`
				Candidates int `json:"candidates"`
			} `json:"tokens"`
		} `json:"models"`
	} `json:"stats"`
}

// Run 执行 Gemini CLI 非交互任务。
func (g *Gemini) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	argv := []string{g.Bin, "-p", req.Prompt, "--output-format", "json"}
	if req.Model != "" {
		argv = append(argv, "-m", req.Model)
	}
	switch req.Sandbox {
	case SandboxReadonly:
		argv = append(argv, "--approval-mode", "default")
	case SandboxWorkspace:
		argv = append(argv, "--approval-mode", "auto_edit")
	case SandboxFull:
		argv = append(argv, "--approval-mode", "yolo")
	}

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), nil)
	if err != nil {
		return nil, err
	}

	var out geminiOut
	if err := json.Unmarshal([]byte(extractJSONObj(stdout)), &out); err != nil || out.Response == "" {
		// 非 JSON 输出：全文兜底
		return &RunResult{Text: strings.TrimSpace(stdout), ExitCode: code, Raw: []byte(stdout)}, nil
	}
	r := &RunResult{Text: out.Response, ExitCode: code, Raw: []byte(stdout)}
	if out.Stats != nil {
		for _, m := range out.Stats.Models {
			r.Usage.InputTokens += m.Tokens.Prompt
			r.Usage.OutputTokens += m.Tokens.Candidates
		}
	}
	return r, nil
}

// ---------- MiMo Code（mimocode，真实 CLI 核实） ----------

// Mimo 小米 MiMo Code 适配器（`mimo run --format json`，参数经本机真实 CLI 核实）。
//
// 协议要点：
//   - `mimo run "<prompt>" --format json` 输出 JSONL 事件：
//     {"type":"text","part":{"text":"..."}}" 增量文本
//     {"type":"step_finish","sessionID":"...","part":{"tokens":{"input","output"},"cost":N}}
//   - `-m provider/model` 覆盖模型（本地默认模型配置可能不被服务端支持，建议显式传）
//   - `-s/--session ID` 续聊
type Mimo struct {
	Bin string // 默认 "mimo"
}

// NewMimo 创建 MiMo 适配器。
func NewMimo() *Mimo { return &Mimo{Bin: "mimo"} }

func (m *Mimo) Name() string { return "mimo" }

type mimoEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error"`
	Part *struct {
		Text   string `json:"text"`
		Tokens *struct {
			Input  int `json:"input"`
			Output int `json:"output"`
		} `json:"tokens"`
		Cost float64 `json:"cost"`
	} `json:"part"`
}

// Run 执行 mimo run 任务。
func (m *Mimo) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	argv := []string{m.Bin, "run", req.Prompt, "--format", "json"}
	if req.Model != "" {
		argv = append(argv, "-m", req.Model)
	}
	if req.SessionID != "" {
		argv = append(argv, "-s", req.SessionID)
	}

	var (
		text      string
		sessionID string
		usage     Usage
		errMsg    string
	)
	onLine := func(line string) {
		var ev mimoEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return
		}
		switch ev.Type {
		case "error":
			// mimo 运行期错误（如模型不支持）exit code 仍为 0——只能从事件流识别
			if ev.Error != nil && ev.Error.Message != "" {
				errMsg = ev.Error.Message
			}
		case "text":
			if ev.Part != nil && ev.Part.Text != "" {
				text = ev.Part.Text // 增量文本：保留最后一段（完整回复以 step_finish 收尾）
				if req.OnEvent != nil {
					req.OnEvent(Event{Type: EventText, Text: ev.Part.Text, Raw: json.RawMessage(line)})
				}
			}
		case "step_finish":
			if ev.SessionID != "" {
				sessionID = ev.SessionID
			}
			if ev.Part != nil {
				if ev.Part.Tokens != nil {
					usage.InputTokens += ev.Part.Tokens.Input
					usage.OutputTokens += ev.Part.Tokens.Output
				}
				usage.CostUSD += ev.Part.Cost
			}
		}
	}

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), onLine)
	if err != nil {
		return nil, err
	}
	if errMsg != "" && text == "" {
		return nil, fmt.Errorf("acpx: mimo 运行错误: %s", errMsg)
	}
	if text == "" {
		text = strings.TrimSpace(stdout) // 事件流不可用时全文兜底（含 ANSI 码的场景由调用方清理）
	}
	return &RunResult{Text: text, SessionID: sessionID, Usage: usage, ExitCode: code, Raw: []byte(stdout)}, nil
}

// 编译期断言。
var (
	_ Agent = (*Kimi)(nil)
	_ Agent = (*Gemini)(nil)
	_ Agent = (*Mimo)(nil)
)
