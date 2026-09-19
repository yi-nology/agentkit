package websearch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

func searxngJSON(results string) string {
	return `{"query":"test","results":[` + results + `]}`
}

func TestSearxngSearchParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searxngJSON(`{"title":"概念A","url":"https://a.example","content":"要点A"},` +
			`{"title":"缺 content","url":"https://x.example"},` +
			`{"title":"","url":"https://skip.example","content":"无标题应跳过"},` +
			`{"title":"指南B","url":"https://b.example","content":"要点B"}`)))
	}))
	defer srv.Close()
	s := NewSearxng(srv.URL, time.Second)
	got, err := s.Search(context.Background(), "什么是概念A", 2)
	if err != nil {
		t.Fatalf("检索不应失败: %v", err)
	}
	if len(got) != 2 || got[0].Title != "概念A" || got[0].URL != "https://a.example" {
		t.Fatalf("结果不符: %+v", got)
	}
}

func TestSearxngLanguageParam(t *testing.T) {
	var gotLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLang = r.URL.Query().Get("language")
		_, _ = w.Write([]byte(searxngJSON(`{"title":"t","url":"https://u.example","content":"c"}`)))
	}))
	defer srv.Close()

	s := NewSearxng(srv.URL, time.Second)
	if _, err := s.Search(context.Background(), "q", 1); err != nil {
		t.Fatal(err)
	}
	if gotLang != "zh-CN" {
		t.Fatalf("缺省 language = %q, want zh-CN", gotLang)
	}

	s.Language = ""
	if _, err := s.Search(context.Background(), "q", 1); err != nil {
		t.Fatal(err)
	}
	if gotLang != "" {
		t.Fatalf("Language 空串应不传参, got %q", gotLang)
	}

	s.Language = "en-US"
	if _, err := s.Search(context.Background(), "q", 1); err != nil {
		t.Fatal(err)
	}
	if gotLang != "en-US" {
		t.Fatalf("language = %q, want en-US", gotLang)
	}
}

func TestSearxngSearchEmptyAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	got, err := NewSearxng(srv.URL, time.Second).Search(context.Background(), "q", 5)
	if err != nil || len(got) != 0 {
		t.Fatalf("空结果应 nil error，得到: %v %+v", err, got)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>search page</html>"))
	}))
	defer srv2.Close()
	if _, err := NewSearxng(srv2.URL, time.Second).Search(context.Background(), "q", 5); err == nil {
		t.Fatal("HTML 响应应报错")
	}

	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv3.Close()
	if _, err := NewSearxng(srv3.URL, time.Second).Search(context.Background(), "q", 5); err == nil {
		t.Fatal("HTTP 500 应报错")
	}

	if _, err := NewSearxng(srv.URL, time.Second).Search(context.Background(), "  ", 5); err == nil {
		t.Fatal("空 query 应报错")
	}
}

func TestSearxngNilHTTPFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searxngJSON(`{"title":"t","url":"https://u.example","content":"c"}`)))
	}))
	defer srv.Close()
	s := &Searxng{BaseURL: srv.URL, Language: "zh-CN"} // 字面量构造，HTTP 为 nil
	got, err := s.Search(context.Background(), "q", 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("nil HTTP 应走缺省 client: %v %v", err, got)
	}
}

func TestSearxngAsTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("必须请求 format=json")
		}
		_, _ = w.Write([]byte(searxngJSON(`{"title":"条目A","url":"https://a.example","content":"要点A"}`)))
	}))
	defer srv.Close()
	s := NewSearxng(srv.URL, time.Second)
	tl := s.AsTool()
	if tl == nil {
		t.Fatal("AsTool 返回 nil")
	}
	info, err := tl.Info(context.Background())
	if err != nil || info == nil || info.Name != "web_search" {
		t.Fatalf("AsTool Info 不符: info=%+v err=%v", info, err)
	}
}

func TestAsToolErrorContract(t *testing.T) {
	// 执行失败=模型可读文本（agent 可自行降级），不上抛框架错误通道——
	// 与 rag.AsTool 同一契约（此前该分支覆盖 23%）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	s := NewSearxng(srv.URL, time.Second)
	it, ok := s.AsTool().(tool.InvokableTool)
	if !ok {
		t.Fatal("AsTool 应产出 InvokableTool")
	}
	out, err := it.InvokableRun(context.Background(), `{"query":"oom 排查"}`)
	if err != nil {
		t.Fatalf("执行失败不得上抛 error（契约）: %v", err)
	}
	txt := fmt.Sprint(out)
	if !strings.Contains(txt, "检索失败") || !strings.Contains(txt, "502") {
		t.Fatalf("错误文本应含原因与状态码: %q", txt)
	}
}

func TestAsToolHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("format=json 缺失")
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"OOM 指南","url":"https://x.dev/oom","content":"排查步骤"}]}`))
	}))
	defer srv.Close()

	s := NewSearxng(srv.URL, time.Second)
	s.DefaultTopK = 5
	it, ok := s.AsTool().(tool.InvokableTool)
	if !ok {
		t.Fatal("AsTool 应产出 InvokableTool")
	}
	out, err := it.InvokableRun(context.Background(), `{"query":"oom 排查"}`)
	if err != nil {
		t.Fatal(err)
	}
	if txt := fmt.Sprint(out); !strings.Contains(txt, "OOM 指南") || !strings.Contains(txt, "https://x.dev/oom") {
		t.Fatalf("结果文本应含标题与 URL: %q", txt)
	}
}
