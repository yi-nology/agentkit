package llm

// Retry-After 捕获与消费（批次四十五，对标 ZCode runner-retry 的「服务端退避建议
// 优先于本地曲线」）：配额型端点在 429 响应里携带 Retry-After（秒数或 HTTP-date），
// 服务端建议比本地指数退避更准——限流窗口还剩多久只有端点知道。
//
// 管线三段：
//   1. capture：retryAfterTransport 包装 RoundTripper，429 响应读取 Retry-After
//      写入请求 ctx 中的 sink（透明；非 429 原样透传）；
//   2. 安装：WithRetryAfterSink 在重试环上游注入 sink（消费方与传输层以此相遇）；
//   3. 消费：RetryAfterFrom 读取建议，重试环按钳制规则取舍——建议 >0 且
//      （≤5min 或 < 本地曲线值）才取代本地退避（ZCode 同款「合理性钳制」）。
//
// 隐私与内存：sink 只存一个时长；未安装 sink 的请求零开销。

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxServerRetryAfter 服务端退避建议的可信上限：超过 5 分钟的建议不直接采信
// （长时间限流应交由 failover 切备模型，而非原地等）。
const MaxServerRetryAfter = 5 * time.Minute

type retryAfterHolder struct {
	mu sync.Mutex
	d  time.Duration
}

func (h *retryAfterHolder) set(d time.Duration) {
	h.mu.Lock()
	h.d = d
	h.mu.Unlock()
}

func (h *retryAfterHolder) get() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.d
}

type retryAfterKey struct{}

// WithRetryAfterSink 在 ctx 注入 sink（重试环上游调用一次；传输层据此回写）。
func WithRetryAfterSink(ctx context.Context) context.Context {
	return context.WithValue(ctx, retryAfterKey{}, &retryAfterHolder{})
}

// RetryAfterFrom 读取最近一次 429 的服务端建议（0 = 无）。重试环在计算退避时调用。
func RetryAfterFrom(ctx context.Context) time.Duration {
	if h, ok := ctx.Value(retryAfterKey{}).(*retryAfterHolder); ok {
		return h.get()
	}
	return 0
}

// SelectRetryDelay 退避取舍（ZCode runner-retry 同款合理性钳制）：服务端建议 >0
// 且（≤MaxServerRetryAfter 或 < 本地曲线值）→ 采信建议；否则本地曲线。
func SelectRetryDelay(local, hint time.Duration) time.Duration {
	if hint <= 0 {
		return local
	}
	if hint <= MaxServerRetryAfter || hint < local {
		return hint
	}
	return local
}

// retryAfterTransport 透明捕获 429 的 Retry-After 头。响应体不做任何改写——
// go-openai 侧的错误构造不受影响。
type retryAfterTransport struct {
	base http.RoundTripper
}

func (t retryAfterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil && resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		if h, ok := req.Context().Value(retryAfterKey{}).(*retryAfterHolder); ok {
			if d := parseRetryAfter(resp.Header.Get("Retry-After")); d > 0 {
				h.set(d)
			}
		}
	}
	return resp, err
}

// parseRetryAfter 解析 Retry-After：延迟秒数（整数）或 HTTP-date。无法解析返回 0。
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		d := time.Until(at)
		if d > 0 {
			return d
		}
	}
	return 0
}
