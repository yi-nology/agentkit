package llm

import (
	"context"
	"time"

	"git.enjoye.top/enjoydream/ekit/pkg/httpjson"
)

// Retry-After 捕获/解析/取舍的传输与钳制原语已下沉 ekit/pkg/httpjson
//（ekit v0.33.0，agentkit v0.10.41 起单源在彼）；本文件保留 llm 包公开面的
// 薄委托（agentrun/resilient 等消费 llm.* 门面）——何时采信建议的策略门控
//（仅限流失败采信）仍归本包 generateRetry 的重试策略。

// MaxServerRetryAfter 服务端退避建议的可信上限：超过 5 分钟的建议不直接
// 采信（长时间限流应交由 failover/降级链处置，而非原地等）。
const MaxServerRetryAfter = httpjson.MaxServerRetryAfter

// WithRetryAfterSink 在 ctx 注入 sink（重试环上游调用一次；传输层据此回写）。
func WithRetryAfterSink(ctx context.Context) context.Context {
	return httpjson.WithRetryAfterSink(ctx)
}

// RetryAfterFrom 读取最近一次 429 的服务端建议（0 = 无）。
func RetryAfterFrom(ctx context.Context) time.Duration {
	return httpjson.RetryAfterFrom(ctx)
}

// SelectRetryDelay 退避取舍（钳制：建议 ≤5min 或 < 本地曲线才采信）。
func SelectRetryDelay(local, hint time.Duration) time.Duration {
	return httpjson.SelectRetryDelay(local, hint)
}
