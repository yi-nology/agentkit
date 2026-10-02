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
	"fmt"
	"io"
	"net/http"

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
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, textutil.TruncEllipsis(string(raw), 200))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("响应解析失败: %w", err)
	}
	return nil
}
