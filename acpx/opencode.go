package acpx

import (
	"context"
	"encoding/json"
	"strings"
)

// 编译期断言。
var _ Agent = (*OpenCode)(nil)

// ---------- OpenCode ----------

// OpenCode opencode 适配器（`opencode run --json`）。
type OpenCode struct {
	Bin string // 默认 "opencode"
}

// NewOpenCode 创建 opencode 适配器。
func NewOpenCode() *OpenCode { return &OpenCode{Bin: "opencode"} }

func (c *OpenCode) Name() string { return "opencode" }

// Run 执行 opencode run 任务（--session 续聊、-m provider/model）。
func (c *OpenCode) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	argv := []string{c.Bin, "run", promptArg(req.Prompt), "--json"}
	if req.Model != "" {
		argv = append(argv, "-m", req.Model)
	}
	if req.SessionID != "" {
		argv = append(argv, "--session", req.SessionID)
	}

	stdout, _, code, err := execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), nil, 0)
	if err != nil {
		return nil, err
	}
	var out openCodeOut
	if err := json.Unmarshal([]byte(extractJSONObj(stdout)), &out); err != nil || out.Text == "" {
		// 非 JSON 输出（版本差异/日志混入）：全文当文本兜底
		return &RunResult{Text: strings.TrimSpace(stdout), ExitCode: code, Raw: []byte(stdout)}, nil
	}
	r := &RunResult{Text: out.Text, SessionID: out.SessionID, ExitCode: code, Raw: []byte(stdout)}
	if out.Tokens != nil {
		r.Usage = Usage{InputTokens: out.Tokens.Input, OutputTokens: out.Tokens.Output}
	}
	return r, nil
}

// openCodeOut opencode --json 输出结构（同时被 GenericAgent JSON 模式复用）。
type openCodeOut struct {
	Text      string `json:"text"`
	SessionID string `json:"sessionID"`
	Tokens    *struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	} `json:"tokens"`
}
