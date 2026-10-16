package acpx

import (
	"context"
	"fmt"
)

// 编译期断言。
var _ Agent = (*Mcode)(nil)

// ---------- MiniMax Code（mcode，真实 CLI 核实 2026-10-04 / 0.6.2） ----------

// Mcode MiniMax Code CLI 适配器（`mcode exec <prompt> --output-format json`，
// 参数与信封经本机真实 CLI 核实）。注册名沿历史键 "minimax"（bin 名为 mcode）。
//
// 协议要点（0.6.2 实弹）：
//   - exec 单 JSON 信封输出（stdout 单行）：
//     {"schemaVersion","type":"exec.result","sessionId":"mvs_...","status":
//     "succeeded","output","model":{...},"usage":{"inputTokens","outputTokens",
//     "cacheReadTokens","totalTokens"},"usageIncomplete"}
//   - ⚠ 运行期失败 exit code 仍为 0（假模型名实弹核实）——失败识别只能靠信封
//     status=failed + error{category,message}，本适配器如实上抛并经 EventError
//     全量转发（不因 exit 0 或有部分文本而当成功——mimo 同纪律）
//   - `--model provider/model` 覆盖模型；不传用 CLI 缺省（实测
//     MiniMax-M3.1-Flash-Preview）
//   - `--session mvs_...` 续聊（实测跨轮记忆成立，sessionId 稳定不变）
//   - `--max-steps N` 轮数预算（对应 MaxTurns）
//   - ⚠ 无沙箱可映射：--permission smart 在 headless 下工作区外写也放行
//     （实测于 $HOME 建文件成功），无"工作区内放行/外禁止"的运行时硬边界——
//     Capabilities 不声明 Sandbox，工作区硬条款仅剩 prompt 纪律（mimo 同款）
//   - 非 JSON 输出（版本差异/日志混入）走全文兜底，不崩不误报
type Mcode struct {
	Bin string // 默认 "mcode"

	namedAgent
}

// NewMcode 创建 MiniMax Code 适配器（注册名 minimax）。
func NewMcode() *Mcode {
	return &Mcode{Bin: "mcode", namedAgent: namedAgent{name: "minimax", fallback: "mcode"}}
}

// Capabilities 支持 Model/Session/MaxTurns（--model / --session / --max-steps）；
// 沙箱与工具白名单无对应参数面（见类型注释 ⚠ 条目）。
func (m *Mcode) Capabilities() Capability {
	return Capability{Model: true, Session: true, MaxTurns: true}
}

// mcodeEnvelope exec.result 单 JSON 信封（字段名与通用信封 output/sessionId
// 驼峰异构，故收专用结构）。
type mcodeEnvelope struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"` // "exec.result"
	Status        string `json:"status"`
	SessionID     string `json:"sessionId"`
	Output        string `json:"output"`
	Error         *struct {
		Category string `json:"category"`
		Message  string `json:"message"`
	} `json:"error"`
	Usage *struct {
		InputTokens     int `json:"inputTokens"`
		OutputTokens    int `json:"outputTokens"`
		CacheReadTokens int `json:"cacheReadTokens"`
		TotalTokens     int `json:"totalTokens"`
	} `json:"usage"`
}

// Run 执行 mcode exec 任务。
func (m *Mcode) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	stdout, stderr, code, err := runCLI(ctx, req, m.buildArgv(req), nil)
	if err != nil {
		return nil, err // procx 层失败：启动失败/超时/非零退出（stderr 已嵌入 error）
	}
	return m.finalize(req, stdout, stderr, code)
}

// buildArgv 构造 mcode CLI 参数（协议面固定：exec --output-format json；
// model/session/max-steps 为条件单元，缺值整体移除）。
func (m *Mcode) buildArgv(req RunRequest) []string {
	argv := []string{m.exec(m.Bin), "exec", promptArg(req.Prompt), "--output-format", "json"}
	if req.Model != "" {
		argv = append(argv, "--model", req.Model)
	}
	if req.SessionID != "" {
		argv = append(argv, "--session", req.SessionID)
	}
	if req.MaxTurns > 0 {
		argv = append(argv, "--max-steps", fmt.Sprint(req.MaxTurns))
	}
	return argv
}

// finalize 信封裁决：非信封输出全文兜底；status=failed 如实上抛（exit=0 不构成
// 成功）；succeeded 装配 output/sessionId/usage。未知 status 一律按失败处置——
// 信封协议演进时宁可误报也不静默当成功。
func (m *Mcode) finalize(req RunRequest, stdout, stderr string, code int) (*RunResult, error) {
	var env mcodeEnvelope
	if parseJSONOut(stdout, &env) != nil || env.Type != "exec.result" {
		return fallbackResult(stdout, stderr, code), nil
	}
	if env.Status != "succeeded" {
		msg := ""
		if env.Error != nil && env.Error.Message != "" {
			msg = env.Error.Message
		} else {
			msg = fmt.Sprintf("status=%s(信封无 error.message)", env.Status)
		}
		emitError(req, msg, stdout)
		return nil, streamError("minimax", "运行错误", msg)
	}
	r := &RunResult{Text: env.Output, SessionID: env.SessionID, ExitCode: code, Raw: []byte(stdout), Stderr: stderrTail(stderr)}
	if env.Usage != nil {
		r.Usage = Usage{InputTokens: env.Usage.InputTokens, OutputTokens: env.Usage.OutputTokens}
	}
	return r, nil
}
