package router

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeModel struct{ resp string }

func (f *fakeModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("桩不支持流式")
}
func (f *fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: f.resp}, nil
}

func testRoutes() []Route {
	return []Route{
		{Name: "bug-fix", Description: "修代码类请求", Handle: func(ctx context.Context, input string) (string, error) {
			return "handled-by-bug-fix:" + input, nil
		}},
		{Name: "explain", Description: "解释类请求", Handle: func(ctx context.Context, input string) (string, error) {
			return "handled-by-explain", nil
		}},
	}
}

func TestRouterDoDispatches(t *testing.T) {
	r, err := New(&Config{Model: &fakeModel{resp: `{"route":"bug-fix","confidence":0.9,"reason":"修代码"}`}, Routes: testRoutes()})
	if err != nil {
		t.Fatal(err)
	}
	d, out, err := r.Do(context.Background(), "帮我修这个空指针")
	if err != nil {
		t.Fatal(err)
	}
	if d.Route != "bug-fix" || out != "handled-by-bug-fix:帮我修这个空指针" {
		t.Fatalf("分发错误: %+v %q", d, out)
	}
}

func TestRouterFallbackOnLowConfidence(t *testing.T) {
	var fbReason string
	r, err := New(&Config{
		Model:  &fakeModel{resp: `{"route":"bug-fix","confidence":0.3,"reason":"不确定"}`},
		Routes: testRoutes(), MinConfidence: 0.6,
		Fallback: func(ctx context.Context, input string, reason string) (string, error) {
			fbReason = reason
			return "fallback", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, out, err := r.Do(context.Background(), "随便问点啥")
	if err != nil || out != "fallback" {
		t.Fatalf("低置信应走兜底: %q %v", out, err)
	}
	if !strings.Contains(fbReason, "置信度") {
		t.Fatalf("兜底原因不符: %q", fbReason)
	}
}

func TestRouterUnknownRouteNoFallback(t *testing.T) {
	r, err := New(&Config{
		Model:  &fakeModel{resp: `{"route":"no-such","confidence":1.0,"reason":"乱说"}`},
		Routes: testRoutes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Do(context.Background(), "x"); err == nil {
		t.Fatal("无兜底且分类非法应报错")
	}
}

func TestRouterValidation(t *testing.T) {
	if _, err := New(&Config{Model: &fakeModel{resp: "{}"}}); err == nil {
		t.Fatal("空路由表应报错")
	}
	_, err := New(&Config{Model: &fakeModel{resp: "{}"}, Routes: []Route{
		{Name: "a", Handle: func(context.Context, string) (string, error) { return "", nil }},
		{Name: "a", Handle: func(context.Context, string) (string, error) { return "", nil }},
	}})
	if err == nil {
		t.Fatal("重名路由应报错")
	}
}

func TestRouterClassifyError(t *testing.T) {
	r, err := New(&Config{Model: &fakeModel{resp: "not json"}, Routes: testRoutes()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Classify(context.Background(), "x"); err == nil {
		t.Fatal("非法 JSON 应报错（GenerateJSON 回喂后仍失败）")
	} else if !errors.Is(err, err) {
		t.Fatal("unreachable")
	}
}
