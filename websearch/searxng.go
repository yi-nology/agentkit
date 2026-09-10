package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// Searxng SearXNG 自建实例客户端（GET /search?format=json——实例 settings.yml 须启用
// formats: [html, json]）。零 API key、零调用成本；商业后端后续按同 Service 接口接入。
type Searxng struct {
	BaseURL     string
	Language    string // 缺省 "zh-CN"；空串=不传 language 参数
	DefaultTopK int    // AsTool 用；≤0 兜底 5
	HTTP        *http.Client
}

// NewSearxng 构造（timeout 为单次检索上限；≤0 用 10s 兜底）。
func NewSearxng(baseURL string, timeout time.Duration) *Searxng {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Searxng{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Language: "zh-CN",
		HTTP:     &http.Client{Timeout: timeout},
	}
}

// searxngResp SearXNG JSON API 响应（只取用到的字段）。
type searxngResp struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// Search 实现 Service。HTTP 非 200 / 响应非 JSON（实例未开 json format 时返回 HTML）一律报错。
func (s *Searxng) Search(ctx context.Context, query string, topK int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("searxng: 空 query")
	}
	if topK <= 0 {
		topK = 5
	}
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("format", "json")
	if s.Language != "" {
		q.Set("language", s.Language)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("searxng: 构造请求失败: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng: 检索失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng: 检索失败: HTTP %d", resp.StatusCode)
	}
	var parsed searxngResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("searxng: 响应解析失败（实例是否开启 format=json?）: %w", err)
	}
	out := make([]Result, 0, topK)
	for _, r := range parsed.Results {
		if r.Title == "" || r.URL == "" {
			continue
		}
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: r.Content})
		if len(out) >= topK {
			break
		}
	}
	return out, nil
}

// searchIn web_search 工具入参（utils.InferTool 自动推导 schema）。
type searchIn struct {
	Query string `json:"query" jsonschema:"检索 query，用领域/技术术语（可中英文）"`
}

// AsTool 把检索包成 web_search 内置工具（与 rag.AsTool 同形态）。
func (s *Searxng) AsTool() tool.BaseTool {
	t, err := utils.InferTool("web_search",
		"检索公开资料（SearXNG）。返回按相关度排序的结果列表（标题/URL/摘要）。用于概念、方法论类问题的事实核查。",
		func(ctx context.Context, in *searchIn) (string, error) {
			results, err := s.Search(ctx, in.Query, s.DefaultTopK)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "未检索到相关公开资料。", nil
			}
			var b strings.Builder
			for i, r := range results {
				b.WriteString(strconv.Itoa(i+1) + ". " + r.Title + "\n   " + r.URL + "\n   " + r.Snippet + "\n\n")
			}
			return b.String(), nil
		})
	if err != nil {
		panic(fmt.Sprintf("searxng: 构建 web_search 工具失败: %v", err)) // 静态 schema，构造失败即编程错误
	}
	return t
}
