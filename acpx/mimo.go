package acpx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 编译期断言。
var _ Agent = (*Mimo)(nil)

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

	argv := []string{m.Bin, "run", promptArg(req.Prompt), "--format", "json"}
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

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), onLine, 0)
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
