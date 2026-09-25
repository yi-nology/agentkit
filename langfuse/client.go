// Langfuse Public API 只读客户端（v3 self-hosted 契约面，BasicAuth pk:sk；
// 沉淀自 heimdallr internal/observe）。列表端点不含 observations，FetchBatch
// 的逐条详情合并是必要成本；顺序串行，对自托管服务友好。
package langfuse

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yi-nology/agentkit/httpx"
)

// Client Langfuse 只读客户端。
type Client struct {
	host string
	pk   string
	sk   string
	http *http.Client
}

// NewClient host 形如 https://lf.example.com（不带尾斜杠）。
func NewClient(host, publicKey, secretKey string) *Client {
	return &Client{
		host: strings.TrimRight(host, "/"),
		pk:   publicKey,
		sk:   secretKey,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// Query 选择口径（selection criteria——观测报告必须可复述选择过程）。
type Query struct {
	Limit    int    // 最多拉取的 trace 数（<=0 → 单页默认 50）
	PageSize int    // 每页条数（<=0 → 50）
	Name     string // trace name 过滤
	Session  string // sessionId 过滤
	User     string // userId 过滤
	From     string // fromTimestamp（RFC3339/ISO8601）
	To       string // toTimestamp
	Tags     []string
}

// Batch 一次拉取结果；TotalItems 是服务端过滤后的总选择集大小（含未拉取部分）。
type Batch struct {
	Traces     []Trace
	TotalItems int
}

type listResp struct {
	Data []Trace `json:"data"`
	Meta struct {
		Page       int `json:"page"`
		Limit      int `json:"limit"`
		TotalItems int `json:"totalItems"`
		TotalPages int `json:"totalPages"`
	} `json:"meta"`
}

// FetchBatch 按选择口径拉取 trace（列表分页 + 逐条详情合并 observations）。
func (c *Client) FetchBatch(ctx context.Context, q Query) (Batch, error) {
	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	limit := q.Limit
	if limit <= 0 {
		limit = pageSize
	}

	var batch Batch
	for page := 1; len(batch.Traces) < limit; page++ {
		u := fmt.Sprintf("%s/api/public/traces?%s", c.host, listQuery(q, page, pageSize).Encode())
		var resp listResp
		if err := c.get(ctx, u, &resp); err != nil {
			return batch, err
		}
		batch.TotalItems = resp.Meta.TotalItems
		for _, tr := range resp.Data {
			if len(batch.Traces) >= limit {
				break
			}
			detail, err := c.GetTrace(ctx, tr.ID)
			if err != nil {
				return batch, fmt.Errorf("langfuse: trace %s: %w", tr.ID, err)
			}
			if detail.Name == "" {
				detail.Name = tr.Name
			}
			batch.Traces = append(batch.Traces, detail)
		}
		if page >= resp.Meta.TotalPages || len(resp.Data) == 0 {
			break
		}
	}
	return batch, nil
}

// GetTrace 单条 trace 详情（含 observations）。
func (c *Client) GetTrace(ctx context.Context, id string) (Trace, error) {
	var tr Trace
	u := fmt.Sprintf("%s/api/public/traces/%s", c.host, url.PathEscape(id))
	if err := c.get(ctx, u, &tr); err != nil {
		return Trace{}, err
	}
	return tr, nil
}

func listQuery(q Query, page, pageSize int) url.Values {
	v := url.Values{}
	v.Set("page", strconv.Itoa(page))
	v.Set("limit", strconv.Itoa(pageSize))
	v.Set("orderBy", "timestamp.asc")
	if q.Name != "" {
		v.Set("name", q.Name)
	}
	if q.Session != "" {
		v.Set("sessionId", q.Session)
	}
	if q.User != "" {
		v.Set("userId", q.User)
	}
	if q.From != "" {
		v.Set("fromTimestamp", q.From)
	}
	if q.To != "" {
		v.Set("toTimestamp", q.To)
	}
	for _, t := range q.Tags {
		v.Add("tags", t)
	}
	return v
}

func (c *Client) get(ctx context.Context, u string, out any) error {
	err := httpx.DoJSON(ctx, c.http, httpx.Request{
		URL: u,
		Header: func(h http.Header) {
			h.Set("Authorization", "Basic "+basicAuth(c.pk, c.sk))
		},
	}, out)
	if err != nil {
		return fmt.Errorf("langfuse: %w", err)
	}
	return nil
}

// basicAuth RFC 7617 Basic 凭证（pk:sk，URL-safe 编码不受内容影响）。
func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
