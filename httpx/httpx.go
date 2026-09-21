// Package httpx HTTP+JSON 调用纪律单源：ctx 感知构造请求 → 执行 → 限容读体
// （读错误不吞）→ 状态码检查（错误体 rune 安全截断，不腰斩 UTF-8）→ JSON 解码。
// langfuse / rag.OpenAIEmbedder / websearch.Searxng 共用同一实现——此前三份
// 手写样板已漂移出真缺陷（响应体读取错误被吞、错误体按字节截断腰斩 UTF-8）。
// 错误不带包前缀返回，调用方以自己的包前缀包装一次（与全仓错误前缀纪律一致）。
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"github.com/yi-nology/agentkit/textutil"
)

// maxBody 响应体采集上限（异常服务端刷屏防内存放大）。
const maxBody = 64 << 20 // 64MB

// Request 一次 HTTP+JSON 调用的描述。
type Request struct {
	// Method HTTP 方法（空 = GET）。
	Method string
	// URL 完整地址（含 query）。
	URL string
	// Body 请求体（nil = 无请求体；JSON 调用方自行 Marshal）。
	Body []byte
	// Header 鉴权/Content-Type 等请求头注入钩子（可 nil）。
	Header func(h http.Header)
}

// DoJSON 执行请求并把 2xx 响应体解码进 out。非 2xx 返回带状态码与响应体摘要
// （rune 安全截断到 200 字符）的错误；解码失败返回包装后的解析错误。
func DoJSON(ctx context.Context, client *http.Client, req Request, out any) error {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	if req.Header != nil {
		req.Header(httpReq.Header)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &StatusError{Code: resp.StatusCode, Body: string(raw)}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("响应解析失败: %w", err)
	}
	return nil
}

// StatusError 非 2xx 响应错误：供重试判定等按状态码分类（errors.As 提取）。
// Error() 文案与历史 DoJSON 错误保持一致（HTTP %d: 响应体摘要）。
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Code, textutil.TruncEllipsis(e.Body, 200))
}

// RetryConfig 一次重试策略（零值 = 仅执行一次，不重试）。
type RetryConfig struct {
	// MaxAttempts 总尝试次数（含首次；<1 视为 1）。
	MaxAttempts int
	// BaseDelay 首次重试延迟（<=0 取 200ms），按尝试轮次指数退避。
	BaseDelay time.Duration
	// MaxDelay 单次重试延迟上限（<=0 取 2s）。
	MaxDelay time.Duration
	// Jitter 随机抖动幅度（防惊群；<0 取 0）。
	Jitter time.Duration
	// Retryable 自定义可重试判定（nil = 默认：网络错误或 HTTP 5xx/429 可重试；
	// 4xx 参数错、响应解析失败不重试）。
	Retryable func(err error) bool
}

// DoJSONWithRetry 带重试的 HTTP+JSON 调用。
//
// reqFn 每次尝试重新构造 Request（请求体流不可重读，且调用方可能希望每轮
// 刷新签名头）；reqFn 自身报错视为调用方参数问题，立即返回不重试。
// 重试间指数退避 + Jitter 抖动；ctx 取消/超时立即返回 ctx.Err()。
func DoJSONWithRetry(ctx context.Context, client *http.Client, reqFn func() (Request, error), cfg RetryConfig, out any) error {
	attempts := cfg.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	base, maxDelay, jitter := cfg.BaseDelay, cfg.MaxDelay, cfg.Jitter
	if base <= 0 {
		base = 200 * time.Millisecond
	}
	if maxDelay <= 0 {
		maxDelay = 2 * time.Second
	}
	if jitter < 0 {
		jitter = 0
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := reqFn()
		if err != nil {
			return err
		}
		err = DoJSON(ctx, client, req, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == attempts || !retryable(err, cfg.Retryable) {
			break
		}
		delay := base << (attempt - 1) // 指数退避
		if delay > maxDelay {
			delay = maxDelay
		}
		if jitter > 0 {
			delay += time.Duration(rand.Int64N(int64(jitter)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}

// retryable 判定一次错误是否值得重试：自定义判定优先；缺省网络超时/5xx/429
// 可重试（网络非超时错误按不可重试保守处理，避免对断言式失败空转）。
func retryable(err error, custom func(error) bool) bool {
	if custom != nil {
		return custom(err)
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code >= 500 || se.Code == http.StatusTooManyRequests
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
