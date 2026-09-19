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
//     {"type":"error","error":{"data":{"message":"..."},"message":"..."}}（错误详情在
//     error.data.message——2026-09 实弹核实；error.message 顶层路径为兼容回退）
//   - `-m provider/model` 覆盖模型：**须 xiaomi/ 全名前缀**（短名被服务端拒绝；
//     本机 CLI 缺省模型可能被服务端下线——ultraspeed 前车之鉴，生产装配建议显式
//     配置 DefaultModel）
//   - `-s/--session ID` 续聊
//   - ⚠ 运行期错误 exit code 仍为 0——错误识别只能靠事件流，本适配器对 error
//     事件如实上抛（不因恰好有部分文本而掩盖），并经 EventError 全量转发
type Mimo struct {
	Bin string // 默认 "mimo"
	// DefaultModel 请求未指定 Model 时的缺省模型（xiaomi/ 全名）。空 = 尊重
	// 本机 CLI 配置（其缺省模型可能已被服务端下线）。
	DefaultModel string

	name string // 注册身份（构造器填充）
}

// NewMimo 创建 MiMo 适配器。
func NewMimo() *Mimo { return &Mimo{Bin: "mimo", name: "mimo"} }

func (m *Mimo) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mimo"
}

// Capabilities 支持 Model/Session（-m / -s）；沙箱/轮数/工具白名单无对应参数。
func (m *Mimo) Capabilities() Capability {
	return Capability{Model: true, Session: true}
}

type mimoEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Error     *struct {
		Message string `json:"message"`
		Data    *struct {
			Message string `json:"message"`
		} `json:"data"`
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

// errMsg 错误详情提取：error.data.message 优先（实际发入路径），顶层
// error.message 为兼容回退。
func (e *mimoEvent) errMsg() string {
	if e.Error == nil {
		return ""
	}
	if e.Error.Data != nil && e.Error.Data.Message != "" {
		return e.Error.Data.Message
	}
	return e.Error.Message
}

// Run 执行 mimo run 任务。
func (m *Mimo) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	argv := []string{m.Bin, "run", promptArg(req.Prompt), "--format", "json"}
	model := req.Model
	if model == "" {
		model = m.DefaultModel
	}
	if model != "" {
		argv = append(argv, "-m", model)
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
			// mimo 运行期错误（如模型不支持）exit code 仍为 0——只能从事件流识别。
			// 全量转发（transcript 可排障）并记录，结束时如实报错
			if msg := ev.errMsg(); msg != "" {
				errMsg = msg
				if req.OnEvent != nil {
					req.OnEvent(Event{Type: EventError, Text: msg, Raw: json.RawMessage(line)})
				}
			}
		case "text":
			if ev.Part != nil && ev.Part.Text != "" {
				text = ev.Part.Text // 增量文本：保留最后一段（完整回复以 step_finish 收尾）
				emitText(req, ev.Part.Text, line)
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
			if req.OnEvent != nil {
				req.OnEvent(Event{Type: EventResult, Text: text, Raw: json.RawMessage(line)})
			}
		}
	}

	stdout, code, err := runCLI(ctx, req, argv, onLine)
	if err != nil {
		return nil, err
	}
	// 有错如实报错：不因恰好收到部分文本而把失败跑当成功返回
	//（2026-09 huginn 实弹教训：纯错误跑曾以 stdout 全文兜底成"成功"，
	// probe 误判通过、排障被带偏）
	if errMsg != "" {
		return nil, fmt.Errorf("acpx: mimo 运行错误: %s", errMsg)
	}
	if text == "" {
		text = strings.TrimSpace(stdout) // 事件流不可用时全文兜底（含 ANSI 码的场景由调用方清理）
	}
	return &RunResult{Text: text, SessionID: sessionID, Usage: usage, ExitCode: code, Raw: []byte(stdout)}, nil
}
