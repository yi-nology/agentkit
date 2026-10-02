package acpx

import (
	"context"
	"encoding/json"
	"strings"
)

// Kimi Kimi Code CLI 适配器（MoonshotAI/kimi-cli，协议已核实官方文档）。
//
// 协议要点（Print 模式，headless）：
//   - `kimi --print -p "<prompt>"` 非交互执行（隐式 --afk 全自动批准工具调用）
//   - `--output-format=stream-json` 输出 JSONL 消息流：
//     {"role":"assistant","content":"..."}
//     {"role":"tool","content":"..."}（工具调用过程）
//   - `--final-message-only` 只输出最终 assistant 消息
//   - `-m/--model NAME` 覆盖模型；`--session ID`/`--resume ID` 续聊
//
// 注：Kimi 正演进为 Kimi Code CLI（MoonshotAI/kimi-code），协议同族。
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

	argv := []string{k.Bin, "--print", "-p", req.Prompt}
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

// ---------- MiMo Code（GenericAgent 预设） ----------

// NewMimo 创建小米 MiMo Code 适配器（CLI 约定待官方稳定，当前走通用模板；
// 稳定后按实际参数收敛为专用适配器——同 Minimax 路径）。
func NewMimo() *GenericAgent {
	return NewGenericAgent("mimo", []string{"mimo", "run", "{prompt}"}, false)
}

// 编译期断言。
var (
	_ Agent = (*Kimi)(nil)
	_ Agent = (*Gemini)(nil)
	_ Agent = NewMimo()
)
