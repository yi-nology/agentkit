// 流恢复原语（对标 ZCode streaming-recovery.ts + model-errors.ts）：turn 级
// 流式请求的瞬态错误分类、空终态检测、上下文溢出识别与 busy 准入退避。
// 单请求重试骨架见 errors.go（ClassifyLLMError）与 retryafter.go；本文件补
// 「流断在哪、什么算瞬态、什么时候放弃」的 turn 级语义。
package llm

import (
	"context"
	"errors"
	"strings"
	"time"
)

// MaxStreamRecoveryRetries 流断点恢复预算（对标 STREAM_RECOVERY_MAX_RETRIES：
// 跨过单请求重试边界后只恢复 1 次与用户预期差距过大，10 次是生产调过的上限）。
const MaxStreamRecoveryRetries = 10

// transientErrorReasons 瞬态失败原因码（统一小写：匹配前 err 文案会 ToLower）。
var transientErrorReasons = map[string]bool{
	"model_request_timeout": true, "model_rate_limited": true,
	"model_server_error": true, "model_network_error": true,
	"stream_idle_timeout": true, "rate_limited": true,
	"server_error": true, "network_error": true, "timeout": true,
}

// IsTransientStreamError 流式 turn 中的错误是否瞬态（值得从安全锚点重开流）。
// 与 ClassifyLLMError 的分工：后者面向单请求 HTTP 重试；本判定面向 turn 级
// 恢复——额外覆盖流空闲超时与原因码形态（错误链上任意一层的文案都参与匹配）。
// 调用方主动取消不算瞬态（与降级矩阵「ctx 取消 → 全链中止」一致）：若判为
// 瞬态，取消后的恢复循环会空转 MaxStreamRecoveryRetries 轮、每轮立即再失败。
func IsTransientStreamError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if h := ClassifyLLMError(err); h.Retryable {
		return true
	}
	msg := strings.ToLower(err.Error())
	for reason := range transientErrorReasons {
		if strings.Contains(msg, reason) {
			return true
		}
	}
	return false
}

// SuspiciousEmptyResult 空终态检测：零文本 + 零工具调用 + 非 stop 终止 +
// 零用量 = 可疑空响应（各家 provider 都会偶发；按可重试错误处理而非当正常
// 完结吞掉）。usageTotal 为 provider 报告的总 token（≤0 视为零用量/未知）。
func SuspiciousEmptyResult(finishReason string, responseLen, toolCallCount, usageTotal int) bool {
	if responseLen != 0 || toolCallCount != 0 {
		return false
	}
	fr := strings.TrimSpace(strings.ToLower(finishReason))
	if fr == "stop" || fr == "tool-calls" || fr == "tool_calls" {
		return false
	}
	return usageTotal <= 0
}

// contextExceededMarkers finishReason/错误码层识别上下文溢出（触发 reactive
// 压缩的锚点——见 compact 包）。
var contextExceededMarkers = map[string]bool{
	"context_exceeded": true, "context_length_exceeded": true,
	"context_window_exceeded": true, "model_context_window_exceeded": true,
	"prompt_too_long": true,
}

// IsContextExceededFinish finishReason 层识别上下文溢出。
func IsContextExceededFinish(finishReason, rawFinishReason string) bool {
	return isContextMarker(finishReason) || isContextMarker(rawFinishReason)
}

// IsContextExceededError 错误文案层识别上下文溢出（provider 报文形态）。
func IsContextExceededError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for marker := range contextExceededMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func isContextMarker(v string) bool {
	return contextExceededMarkers[strings.TrimSpace(strings.ToLower(v))]
}

// busyProviderCodes provider 繁忙码（zcode-plan 形态：3008/3009/3010——
// 入口排队满，与限流不同类）。
var busyProviderCodes = map[string]bool{"3008": true, "3009": true, "3010": true}

// BusyAdmissionDelays busy 准入重试的两档退避（对标
// START_PLAN_BUSY_MAIN_TURN_ADMISSION_RETRY_DELAYS_MS：1s/2s）。
var BusyAdmissionDelays = []time.Duration{time.Second, 2 * time.Second}

// IsProviderBusyCode provider 业务码是否「繁忙」类（准入级退避重试的判定；
// 耗尽后调用方应合成不可重试错误，别无限排队）。
func IsProviderBusyCode(code string) bool {
	return busyProviderCodes[strings.TrimSpace(code)]
}

// BusyAdmissionDelay 按 retry 序号（0 起）取准入退避；越界取最后一档。
func BusyAdmissionDelay(retry int) time.Duration {
	if retry < 0 {
		retry = 0
	}
	if retry >= len(BusyAdmissionDelays) {
		retry = len(BusyAdmissionDelays) - 1
	}
	return BusyAdmissionDelays[retry]
}
