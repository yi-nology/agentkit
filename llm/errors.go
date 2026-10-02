package llm

import "strings"

// RetryHint 错误分类结果。
type RetryHint struct {
	Retryable   bool // 是否值得重试
	IsRateLimit bool // 是否 429/rate limit
	IsTruncated bool // 是否输出截断
}

// ClassifyLLMError 判定是否值得重试及错误类型。
// 确定性失败（鉴权/上下文超限）不重试；429 可重试 + 退避下限提高；其余可重试。
func ClassifyLLMError(err error) RetryHint {
	if err == nil {
		return RetryHint{Retryable: true}
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"401", "403", "unauthorized", "invalid api key", "invalid_api_key", "forbidden"} {
		if strings.Contains(msg, marker) {
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
		if strings.Contains(msg, marker) {
			return RetryHint{Retryable: true, IsRateLimit: true}
		}
	}
	for _, marker := range []string{"finish_reason=length", "输出被截断", "output truncated"} {
		if strings.Contains(msg, marker) {
			return RetryHint{Retryable: true, IsTruncated: true}
		}
	}
	return RetryHint{Retryable: true}
}

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
