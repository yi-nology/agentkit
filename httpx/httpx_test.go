package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoJSONSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
	body := make([]byte, r.ContentLength)
	if _, err := r.Body.Read(body); err != nil && len(body) == 0 {
		t.Errorf("读请求体: %v", err)
	}
	if string(body) != `"hi"` {
		t.Errorf("Body = %q", body)
	}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out struct {
		OK bool `json:"ok"`
	}
	err := DoJSON(context.Background(), srv.Client(), Request{
		Method: http.MethodPost,
		URL:    srv.URL,
		Body:   []byte(`"hi"`),
		Header: func(h http.Header) {
			h.Set("Authorization", "Bearer tok")
			h.Set("Content-Type", "application/json")
		},
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatal("out.OK = false")
	}
}

func TestDoJSONErrorStatusCarriesRuneSafeSnippet(t *testing.T) {
	// 错误体摘要须 rune 安全（此前 embedder 按字节截断会腰斩 UTF-8），且超长截到 200。
	multibyte := strings.Repeat("错", 150) // 300 字节、150 rune
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(multibyte))
	}))
	defer srv.Close()

	var out map[string]any
	err := DoJSON(context.Background(), srv.Client(), Request{URL: srv.URL}, &out)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("非 2xx 应带状态码报错: %v", err)
	}
	if got := err.Error(); strings.Count(got, "错") > 200 {
		t.Fatalf("错误体摘要应截断: %d", strings.Count(got, "错"))
	}
}

func TestDoJSONDecodeAndTransportErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	var out map[string]any
	if err := DoJSON(context.Background(), srv.Client(), Request{URL: srv.URL}, &out); err == nil ||
		!strings.Contains(err.Error(), "响应解析失败") {
		t.Fatalf("坏 JSON 应报解析错误: %v", err)
	}

	// 不可达端点 → 传输错误
	if err := DoJSON(context.Background(), srv.Client(), Request{URL: "http://127.0.0.1:1/x"}, &out); err == nil {
		t.Fatal("传输错误应上抛")
	}
}
