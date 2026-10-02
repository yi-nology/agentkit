package acpx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yi-nology/agentkit/jsonrepair"
	"github.com/yi-nology/agentkit/procx"
	"github.com/yi-nology/agentkit/textutil"
)

// runCLI 适配器统一执行骨架：validate → 进程组执行（procx：环境白名单 +
// timeout + stdout 限容/按行拆分单源）。各适配器只负责 argv 构造与事件流解析。
// stderr 一并透传（RunResult.Stderr 的排障通道——此前整条丢弃）。
func runCLI(ctx context.Context, req RunRequest, argv []string, onLine func(string)) (stdout, stderr string, code int, err error) {
	if err := req.validate(); err != nil {
		return "", "", -1, err
	}
	stdout, stderr, code, err = procx.Run(ctx, procx.RunRequest{
		Argv: argv, Dir: req.WorkDir, Env: req.Env, Timeout: req.Timeout, OnLine: onLine,
	})
	return stdout, stderr, code, err
}

// stderrTail 子进程 stderr 尾巴（≤400 rune，rune 对齐）。错误路径 procx 已把
// 尾巴嵌进 error 文本，这里服务结果面（RunResult.Stderr）的 transcript 排障。
func stderrTail(stderr string) string {
	if stderr == "" {
		return ""
	}
	return textutil.TruncEllipsis(strings.TrimRight(stderr, "\n"), 400)
}

// fallbackResult 全文兜底结果：JSON/事件流不可用时把 stdout 整体当文本。
func fallbackResult(stdout, stderr string, code int) *RunResult {
	return &RunResult{Text: strings.TrimSpace(stdout), ExitCode: code, Raw: []byte(stdout), Stderr: stderrTail(stderr)}
}

// parseJSONOut 容错解析 agent 的 JSON 输出：平衡括号感知地截取首个 JSON 对象
// （日志混入/尾随噪音均可容忍）后 Unmarshal。失败时调用方走 fallbackResult。
func parseJSONOut(stdout string, out any) error {
	candidate := jsonrepair.ExtractObject(stdout)
	if candidate == "" {
		candidate = stdout
	}
	return json.Unmarshal([]byte(candidate), out)
}

// streamError 事件流失败的统一收尾：exit=0 但流内含 error/abort 时如实报错——
// 部分失败形态（配额耗尽/模型不支持/中止）进程退出码仍为 0，不得因恰好收到
// 部分文本而当成功返回（2026-09 huginn 实弹教训）。detail 为空 = 无失败。
func streamError(who, kind, detail string) error {
	if detail == "" {
		return nil
	}
	return fmt.Errorf("acpx: %s %s: %s", who, kind, detail)
}

// envelopeOut 通用 JSON 信封输出（{"text","sessionID","tokens","error"}）。
type envelopeOut struct {
	Text      string `json:"text"`
	SessionID string `json:"sessionID"`
	// Error 信封错误（"error":null 或缺省 = 无失败）。字符串与 {"message":...}
	// 两种形态都收——各家 CLI 约定不一（opencode 实测形态待真机核实，宽容解析）。
	Error  json.RawMessage `json:"error"`
	Tokens *struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	} `json:"tokens"`
}

// envelopeErrMsg 信封错误文案提取：null/空 = 无失败；字符串直取；
// 对象取 message；其余形态回退原文。
func envelopeErrMsg(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return strings.TrimSpace(str)
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.TrimSpace(obj.Message)
	}
	return s
}

// envelopeResult 信封结果装配：信封 error 如实上抛（部分错误形态 exit 仍为 0，
// 只能从信封识别——与 mimo/gemini 同纪律，曾把错误信封解析成功后以空 text
// 触发全文兜底当成功返回）；解析失败或 text 为空时全文兜底（版本差异/日志混入）。
func envelopeResult(req RunRequest, who, stdout, stderr string, code int) (*RunResult, error) {
	var out envelopeOut
	if err := parseJSONOut(stdout, &out); err != nil {
		return fallbackResult(stdout, stderr, code), nil
	}
	if msg := envelopeErrMsg(out.Error); msg != "" {
		emitError(req, msg, stdout)
		return nil, streamError(who, "运行错误", msg)
	}
	if out.Text == "" {
		return fallbackResult(stdout, stderr, code), nil
	}
	r := &RunResult{Text: out.Text, SessionID: out.SessionID, ExitCode: code, Raw: []byte(stdout), Stderr: stderrTail(stderr)}
	if out.Tokens != nil {
		r.Usage = Usage{InputTokens: out.Tokens.Input, OutputTokens: out.Tokens.Output}
	}
	return r, nil
}

// tokenPair 事件流 usage 的 input/output token 对（claude/codex 同一 JSON 形态；
// mimo/opencode 键名不同，各自内联）。
type tokenPair struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// emit 流式事件的 nil 安全发送（回调在 stdout 读取 goroutine 中同步执行，
// 不得阻塞/panic——见 RunRequest.OnEvent 契约）。emitText/emitError 是其
// 常用形态的便捷包装；thinking/tool_call 等事件直接构造 Event 后经 emit 发送。
func emit(req RunRequest, ev Event) {
	if req.OnEvent != nil {
		req.OnEvent(ev)
	}
}

// emitText 流式文本事件的 nil 安全发送。
func emitText(req RunRequest, text, line string) {
	emit(req, Event{Type: EventText, Text: text, Raw: json.RawMessage(line)})
}

// emitError 流式错误事件的 nil 安全发送。各适配器对运行期 error 事件
// 全量转发（transcript 可排障——此前只转 text，纯错误跑 transcript 0 字节）。
func emitError(req RunRequest, text, line string) {
	emit(req, Event{Type: EventError, Text: text, Raw: json.RawMessage(line)})
}

// finalizeStream 事件流适配器统一的三段式收尾骨架（claude/pi/mimo 等 JSONL
// 流共用——各家手写曾反复漂移：终态优先漏迁移、空文本终态被当空壳触发兜底）：
//  1. 执行错（超时/信号/非零退出）且终态已到达 → 终态优先：agent 正常完成但
//     进程退出码非零时以结果为准；终态到达即权威，文本可为空（空文本终态
//     ≠ 空壳，不得退回 stdout 全文兜底把事件流原文当正文）
//  2. exit=0 但流内 error/abort → 如实报错：部分失败不得因恰好收到部分文本
//     而当成功返回（2026-09 huginn 实弹教训）
//  3. 终态 → 返回；无终态 → stdout 全文兜底（版本差异/日志混入/流尾截断）
//
// term 由调用方在协议终态事件到达时装配；hasTerm=false 时 term 被忽略。
// streamErr 为流内失败（streamError 产物），nil = 无失败。
func finalizeStream(stdout, stderr string, code int, execErr error, term *RunResult, hasTerm bool, streamErr error) (*RunResult, error) {
	if execErr != nil {
		if hasTerm && term != nil {
			term.ExitCode = code
			term.Raw = []byte(stdout)
			term.Stderr = stderrTail(stderr)
			return term, nil
		}
		return nil, execErr
	}
	if streamErr != nil {
		return nil, streamErr
	}
	if hasTerm && term != nil {
		term.ExitCode = code
		term.Raw = []byte(stdout)
		term.Stderr = stderrTail(stderr)
		return term, nil
	}
	// 会话 ID 与终态正交（来自 session/init 事件）：无终态兜底时仍保留，
	// 调用方拿它续聊不受输出格式异常影响
	r := fallbackResult(stdout, stderr, code)
	if term != nil {
		r.SessionID = term.SessionID
	}
	return r, nil
}
