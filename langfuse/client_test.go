package langfuse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 假 Langfuse：验证 BasicAuth、查询参数、分页循环、详情合并（官方 openapi 契约形态）。
func TestClientFetchBatch(t *testing.T) {
	mux := http.NewServeMux()
	var gotAuth string
	var gotQuery string
	pageCalls := 0

	mux.HandleFunc("/api/public/traces", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		pageCalls++
		page := r.URL.Query().Get("page")
		pageNo := 1
		fmt.Sscanf(page, "%d", &pageNo)
		resp := map[string]any{
			"meta": map[string]any{"page": pageNo, "limit": 2, "totalItems": 3, "totalPages": 2},
		}
		switch page {
		case "1":
			resp["data"] = []map[string]any{
				{"id": "tr-1", "timestamp": "2026-09-14T08:00:00Z", "name": "agent"},
				{"id": "tr-2", "timestamp": "2026-09-14T08:01:00Z", "name": "agent"},
			}
		default:
			resp["data"] = []map[string]any{
				{"id": "tr-3", "timestamp": "2026-09-14T08:02:00Z", "name": "agent"},
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	detailCalls := 0
	mux.HandleFunc("/api/public/traces/", func(w http.ResponseWriter, r *http.Request) {
		detailCalls++
		id := r.URL.Path[len("/api/public/traces/"):]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "timestamp": "2026-09-14T08:00:00Z", "latency": 1.5,
			"observations": []map[string]any{
				{"id": "o1", "type": "GENERATION", "name": "llm",
					"startTime": "2026-09-14T08:00:00Z", "level": "DEFAULT",
					"usageDetails": map[string]int{"input": 10, "output": 5, "total": 15}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "pk-test", "sk-test")
	batch, err := c.FetchBatch(context.Background(), Query{Limit: 3, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}

	// BasicAuth: base64(pk:sk)
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("pk-test:sk-test"))
	if gotAuth != wantAuth {
		t.Fatalf("basic auth pk:sk expected, got %q", gotAuth)
	}
	if pageCalls != 2 {
		t.Fatalf("must page until exhausted, got %d list calls", pageCalls)
	}
	if detailCalls != 3 {
		t.Fatalf("each trace needs detail fetch (observations), got %d", detailCalls)
	}
	if len(batch.Traces) != 3 {
		t.Fatalf("want 3 traces, got %d", len(batch.Traces))
	}
	if batch.TotalItems != 3 {
		t.Fatalf("server-side selection size must be recorded: %+v", batch)
	}
	// 详情合并：observations 已随 trace 返回
	if len(batch.Traces[0].Observations) != 1 || batch.Traces[0].Latency == nil || *batch.Traces[0].Latency != 1.5 {
		t.Fatalf("details must be merged: %+v", batch.Traces[0])
	}
	// usage 口径：新口径优先
	in, out, total := batch.Traces[0].Observations[0].UsageTokens()
	if in != 10 || out != 5 || total != 15 {
		t.Fatalf("usage details must be readable: %d/%d/%d", in, out, total)
	}
	// 过滤参数透传
	c2 := NewClient(srv.URL, "pk", "sk")
	_, _ = c2.FetchBatch(context.Background(), Query{Limit: 1, Name: "agent", Tags: []string{"prod", "v2"}, From: "2026-09-01T00:00:00Z"})
	if gotQuery == "" || !containsAll(gotQuery, "name=agent", "tags=prod", "tags=v2", "fromTimestamp=2026-09-01") {
		t.Fatalf("filters must be passed through: %q", gotQuery)
	}
}

// 服务端错误必须显式失败，不静默返回空批次。
func TestClientAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Invalid username or password"}`))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "pk", "bad").FetchBatch(context.Background(), Query{Limit: 1})
	if err == nil {
		t.Fatal("401 must surface as error")
	}
}

// limit=0 视为默认页大小拉取一页（防呆）。
func TestClientDefaultLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/public/traces", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") == "" {
			t.Error("limit param must always be set")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"page": 1, "limit": 50, "totalItems": 0, "totalPages": 0},
			"data": []any{},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	batch, err := NewClient(srv.URL, "pk", "sk").FetchBatch(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Traces) != 0 {
		t.Fatalf("empty project must yield empty batch: %+v", batch)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !stringContains(s, sub) {
			return false
		}
	}
	return true
}

func stringContains(s, sub string) bool {
	return fmt.Sprint(s) != "" && len(s) >= len(sub) && indexOfStr(s, sub) >= 0
}

func indexOfStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
