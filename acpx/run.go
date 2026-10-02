package acpx

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yi-nology/agentkit/jsonrepair"
)

// runCLI 适配器统一执行骨架：validate → childEnv 白名单 → timeout → 进程组执行。
// stdout 走缺省限容（maxChildStdout），各适配器只负责 argv 构造与事件流解析。
func runCLI(ctx context.Context, req RunRequest, argv []string, onLine func(string)) (stdout string, code int, err error) {
	if err := req.validate(); err != nil {
		return "", -1, err
	}
	stdout, _, code, err = execCLI(ctx, req.WorkDir, argv, childEnv(req.Env), req.timeout(), onLine, 0)
	return stdout, code, err
}

// fallbackResult 全文兜底结果：JSON/事件流不可用时把 stdout 整体当文本。
func fallbackResult(stdout string, code int) *RunResult {
	return &RunResult{Text: strings.TrimSpace(stdout), ExitCode: code, Raw: []byte(stdout)}
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

// emitText 流式文本事件的 nil 安全发送（回调在 stdout 读取 goroutine 中同步执行，
// 不得阻塞/panic——见 RunRequest.OnEvent 契约）。
func emitText(req RunRequest, text, line string) {
	if req.OnEvent != nil {
		req.OnEvent(Event{Type: EventText, Text: text, Raw: json.RawMessage(line)})
	}
}
