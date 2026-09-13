package llm

import (
	"errors"
	"testing"
)

func TestClassifyLLMError(t *testing.T) {
	cases := []struct {
		err       error
		retryable bool
		rateLimit bool
		truncated bool
	}{
		{nil, true, false, false},
		{errors.New("401 unauthorized"), false, false, false},
		{errors.New("invalid api key"), false, false, false},
		{errors.New("context length exceeded"), false, false, false},
		{errors.New("too many tokens"), false, false, false},
		{errors.New("429 rate limit"), true, true, false},
		{errors.New("too many requests"), true, true, false},
		{errors.New("finish_reason=length"), true, false, true},
		{errors.New("connection reset"), true, false, false},
		// 确定性 4xx：参数错/模型不存在，重试无意义
		{errors.New("error, status code: 400, message: max_tokens too large"), false, false, false},
		{errors.New("error, status code: 404, message: model not found"), false, false, false},
		{errors.New("400 bad request: invalid parameter"), false, false, false},
		{errors.New("unknown model: gpt-99"), false, false, false},
		// 408/429 仍可重试
		{errors.New("408 request timeout"), true, false, false},
		// 词边界：状态码不得命中耗时/计数等数字文案
		{errors.New("request failed after 1429ms"), true, false, false},
		{errors.New("batch 1401 done"), true, false, false},
		{errors.New("error, status code: 429, message: rate limited"), true, true, false},
		// 截断 marker 必须最先判：Client 自产截断错误文本含 completion_tokens=<n>，
		// n 恰为 4xx/429 值时不得被数字 marker 抢先误判（否则"截断→提升
		// MaxOutputTokens 重试"机制确定性失效）
		{errors.New(`llm: R1 输出被截断（finish_reason=length, completion_tokens=401）`), true, false, true},
		{errors.New(`llm: R1 输出被截断（finish_reason=length, completion_tokens=429）`), true, false, true},
		{errors.New(`llm: R1 输出被截断（finish_reason=length, completion_tokens=404）`), true, false, true},
	}
	for _, c := range cases {
		hint := ClassifyLLMError(c.err)
		if hint.Retryable != c.retryable {
			t.Errorf("%v: retryable 应为 %v", c.err, c.retryable)
		}
		if hint.IsRateLimit != c.rateLimit {
			t.Errorf("%v: rateLimit 应为 %v", c.err, c.rateLimit)
		}
		if hint.IsTruncated != c.truncated {
			t.Errorf("%v: truncated 应为 %v", c.err, c.truncated)
		}
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain object", `{"a":1}`, `{"a":1}`},
		{"plain array", `[1,2]`, `[1,2]`},
		{"markdown fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"fence no lang", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"extra text", `some text {"a":1} more`, `{"a":1}`},
		{"nested braces", `{"a":{"b":1}}`, `{"a":{"b":1}}`},
		{"empty", ``, ``},
		{"no braces", `hello`, `hello`},
	}
	for _, c := range cases {
		got := ExtractJSON(c.input)
		if got != c.want {
			t.Errorf("%s: ExtractJSON(%q) = %q, want %q", c.name, c.input, got, c.want)
		}
	}
}

func TestBudgetConfig(t *testing.T) {
	cfg := BudgetConfig{ContextTokens: 1_000_000}

	if got := cfg.MaxDiffChars(); got != 1_400_000 {
		t.Errorf("MaxDiffChars = %d, want 1400000", got)
	}
	if got := cfg.TaskTokenBudget(); got != 600_000 {
		t.Errorf("TaskTokenBudget = %d, want 600000", got)
	}
	if got := cfg.MaxOutputTokens(); got != 50_000 {
		t.Errorf("MaxOutputTokens = %d, want 50000", got)
	}
	if got := cfg.ReqBodyMaxChars(); got != 60_000 {
		t.Errorf("ReqBodyMaxChars = %d, want 60000", got)
	}
	if got := cfg.ReqIssueMaxChars(); got != 15_000 {
		t.Errorf("ReqIssueMaxChars = %d, want 15000", got)
	}
}

func TestBudgetConfigMinimums(t *testing.T) {
	cfg := BudgetConfig{ContextTokens: 128_000}

	if got := cfg.MaxDiffChars(); got < 10_000 {
		t.Errorf("MaxDiffChars = %d, should be >= 10000", got)
	}
	if got := cfg.MaxOutputTokens(); got < 4096 {
		t.Errorf("MaxOutputTokens = %d, should be >= 4096", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens("hello world"); got != 2 {
		t.Errorf("estimateTokens('hello world') = %d, want 2", got)
	}
	// 中文按字节估算：4 runes × 3 bytes = 12 bytes / 4 = 3
	if got := estimateTokens("你好世界"); got != 3 {
		t.Errorf("estimateTokens('你好世界') = %d, want 3", got)
	}
}

func TestRetryableLLMError(t *testing.T) {
	if !RetryableLLMError(nil) {
		t.Error("nil 应可重试")
	}
	if RetryableLLMError(errors.New("401 unauthorized")) {
		t.Error("401 不应重试")
	}
	if !RetryableLLMError(errors.New("500 internal error")) {
		t.Error("500 应可重试")
	}
}
