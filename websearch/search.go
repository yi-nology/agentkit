// Package websearch 公开资料检索抽象：接口可插拔，缺省实现 SearXNG 自建实例
// （免 API key、零调用成本）。与 knowledge/rag 互补——本包面向外网公开资料。
package websearch

import (
	"context"
)

// Result 单条检索结果。
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Service 检索服务抽象。失败返回 error（调用方按降级收口）；无命中返回空切片 + nil error。
type Service interface {
	Search(ctx context.Context, query string, topK int) ([]Result, error)
}
