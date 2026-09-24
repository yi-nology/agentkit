package acpx

import (
	"context"
	"encoding/json"
	"fmt"
)

// 编译期断言。
var _ Agent = (*Gemini)(nil)

// ---------- Gemini CLI / Qwen Code（同族协议） ----------

// Gemini Gemini CLI 适配器（google-gemini/gemini-cli；Qwen Code 为其 fork，参数同族）。
//
// 协议要点（非交互模式）：
//   - `gemini -p "<prompt>" --output-format json` 输出单 JSON 对象：
//     {"response":"...","stats":{"models":{"<model>":{"tokens":{"prompt":N,"candidates":N}}}}}
//   - `-m/--model`；`--approval-mode default|auto_edit|yolo`（沙箱映射）
type Gemini struct {
	Bin string // 默认 "gemini"

	name string // 注册身份（构造器填充）；空 = 按 Bin 推导兜底
}

// NewGemini 创建 Gemini CLI 适配器。
func NewGemini() *Gemini { return &Gemini{Bin: "gemini", name: "gemini"} }

// NewQwen 创建 Qwen Code 适配器（gemini-cli fork，参数同族）。
func NewQwen() *Gemini { return &Gemini{Bin: "qwen", name: "qwen"} }

func (g *Gemini) Name() string {
	if g.name != "" {
		return g.name
	}
	if g.Bin == "qwen" {
		return "qwen"
	}
	return "gemini"
}

// Capabilities 支持 Model/Sandbox（-m / --approval-mode）；非交互模式无续聊、轮数与工具白名单无对应参数。
func (g *Gemini) Capabilities() Capability {
	return Capability{Model: true, Sandbox: true}
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
	argv := []string{g.Bin, "-p", promptArg(req.Prompt), "--output-format", "json"}
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

	stdout, code, err := runCLI(ctx, req, argv, nil)
	if err != nil {
		return nil, err
	}

	var out geminiOut
	if err := parseJSONOut(stdout, &out); err != nil || (out.Response == "" && out.Error == nil) {
		// 非 JSON 输出：全文兜底
		return fallbackResult(stdout, code), nil
	}
	// 运行期错误如实上抛（部分错误形态 exit 仍为 0，只能从信封识别——与 mimo 同纪律）
	if out.Error != nil && out.Error.Message != "" {
		if req.OnEvent != nil {
			req.OnEvent(Event{Type: EventError, Text: out.Error.Message, Raw: json.RawMessage(stdout)})
		}
		return nil, fmt.Errorf("acpx: gemini 运行错误: %s", out.Error.Message)
	}
	if out.Response == "" {
		return fallbackResult(stdout, code), nil
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
