package llm

import (
	"errors"
	"strings"

	openai "github.com/meguminnnnnnnnn/go-openai"
)

// RetryHint 错误分类结果。
type RetryHint struct {
	Retryable   bool // 是否值得重试
	IsRateLimit bool // 是否 429/rate limit
	IsTruncated bool // 是否输出截断
}

// nonRetryable4xx 确定性失败的 HTTP 状态码（408/429 除外——超时/限速值得重试）。
var nonRetryable4xx = []string{"400", "401", "402", "403", "404", "405", "413", "422"}

// ClassifyLLMError 判定是否值得重试及错误类型。
// 分类优先级：结构化 HTTP 状态码（go-openai RequestError）→ 文本 marker。
// 确定性失败（鉴权/参数错/模型不存在/上下文超限等 4xx）不重试——重试纯浪费
// 钱和延迟；429 可重试 + 退避下限提高；5xx/网络错误默认可重试。
func ClassifyLLMError(err error) RetryHint {
	if err == nil {
		return RetryHint{Retryable: true}
	}
	// 结构化状态码优先：不受错误文案措辞影响
	if status := httpStatus(err); status >= 400 && status < 500 {
		switch status {
		case 408:
			return RetryHint{Retryable: true}
		case 429:
			return RetryHint{Retryable: true, IsRateLimit: true}
		default:
			return RetryHint{}
		}
	}

	msg := strings.ToLower(err.Error())
	// 截断 marker 必须最先判：Client 生成的截断错误文本含 "completion_tokens=<n>"
	// 数字，若先走下方数字状态码匹配，n 恰为 401/429 等值时会误判为鉴权失败/
	// 限速，"截断→提升 MaxOutputTokens 重试"机制确定性失效。
	for _, marker := range []string{"finish_reason=length", "输出被截断", "output truncated"} {
		if strings.Contains(msg, marker) {
			return RetryHint{Retryable: true, IsTruncated: true}
		}
	}
	// 鉴权/权限（部分端点不带状态码）
	for _, marker := range []string{"401", "403", "unauthorized", "invalid api key", "invalid_api_key", "forbidden"} {
		if containsMarker(msg, marker) {
			return RetryHint{}
		}
	}
	// 其余确定性 4xx：参数错/模型不存在等（重试无意义）
	for _, marker := range []string{
		"bad request", "invalid request", "invalid parameter", "invalid_parameter",
		"model not found", "unknown model", "no such model", "does not exist",
	} {
		if strings.Contains(msg, marker) {
			return RetryHint{}
		}
	}
	for _, code := range nonRetryable4xx {
		if containsMarker(msg, code) {
			return RetryHint{}
		}
	}
	for _, marker := range []string{
		"context length", "context_length", "maximum context", "max context",
		"too long", "too many tokens", "reduce the length", "input length",
	} {
		if strings.Contains(msg, marker) {
			return RetryHint{}
		}
	}
	for _, marker := range []string{"429", "rate limit", "ratelimit", "too many requests", "throttl"} {
		if containsMarker(msg, marker) {
			return RetryHint{Retryable: true, IsRateLimit: true}
		}
	}
	return RetryHint{Retryable: true}
}

// httpStatus 提取结构化 HTTP 状态码（0 = 未知）。
// 优先 errors.As 提取 go-openai 的 RequestError；错误链经 eino/网关包装后仍可穿透。
func httpStatus(err error) int {
	var re *openai.RequestError
	if errors.As(err, &re) {
		return re.HTTPStatusCode
	}
	return 0
}

// containsMarker 文本 marker 匹配。数字状态码用词边界匹配——"429" 不得命中
// "failed after 1429ms" 之类的耗时文案。
func containsMarker(msg, marker string) bool {
	if !isDigitToken(marker) {
		return strings.Contains(msg, marker)
	}
	for i := 0; i+len(marker) <= len(msg); i++ {
		if msg[i:i+len(marker)] != marker {
			continue
		}
		before, after := byte(' '), byte(' ')
		if i > 0 {
			before = msg[i-1]
		}
		if i+len(marker) < len(msg) {
			after = msg[i+len(marker)]
		}
		if !isDigitByte(before) && !isDigitByte(after) {
			return true
		}
	}
	return false
}

func isDigitToken(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }

// IsRateLimitError 快速判定是否 429 类错误。
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	return ClassifyLLMError(err).IsRateLimit
}

// IsTruncatedError 快速判定是否输出截断错误。
func IsTruncatedError(err error) bool {
	if err == nil {
		return false
	}
	return ClassifyLLMError(err).IsTruncated
}

// RetryableLLMError 判定错误是否值得重试。
func RetryableLLMError(err error) bool {
	return ClassifyLLMError(err).Retryable
}

// ExtractJSON 从模型输出中提取 JSON 文本：剥 ``` 围栏、截取首个 {/[ 到末个 }/]。
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return s
	}
	openCh := s[start]
	closeCh := byte('}')
	if openCh == '[' {
		closeCh = ']'
	}
	rest := s[start:]
	end := strings.LastIndexByte(rest, closeCh)
	if end < 0 {
		return rest
	}
	return rest[:end+1]
}
