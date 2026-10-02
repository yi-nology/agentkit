package acpx

import (
	"context"
	"encoding/json"
	"strings"
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

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), nil, 0)
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
