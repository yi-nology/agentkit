package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yi-nology/agentkit/llm/llmtest"
)

// 桩统一走 llmtest.Model（单响应恒定 + 首消息记录）。

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
	r, err := New(&Config{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: `{"route":"bug-fix","confidence":0.9,"reason":"修代码"}`}}}, Routes: testRoutes()})
	if err != nil {
		t.Fatal(err)
	}
	d, out, err := r.Run(context.Background(), "帮我修这个空指针")
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
		Model:  &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: `{"route":"bug-fix","confidence":0.3,"reason":"不确定"}`}}},
		Routes: testRoutes(), MinConfidence: 0.6,
		Fallback: func(ctx context.Context, input string, reason string) (string, error) {
			fbReason = reason
			return "fallback", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, out, err := r.Run(context.Background(), "随便问点啥")
	if err != nil || out != "fallback" {
		t.Fatalf("低置信应走兜底: %q %v", out, err)
	}
	if !strings.Contains(fbReason, "置信度") {
		t.Fatalf("兜底原因不符: %q", fbReason)
	}
}

func TestRouterUnknownRouteNoFallback(t *testing.T) {
	r, err := New(&Config{
		Model:  &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: `{"route":"no-such","confidence":1.0,"reason":"乱说"}`}}},
		Routes: testRoutes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Run(context.Background(), "x"); err == nil {
		t.Fatal("无兜底且分类非法应报错")
	}
}

func TestRouterValidation(t *testing.T) {
	if _, err := New(&Config{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: "{}"}}}}); err == nil {
		t.Fatal("空路由表应报错")
	}
	_, err := New(&Config{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: "{}"}}}, Routes: []Route{
		{Name: "a", Handle: func(context.Context, string) (string, error) { return "", nil }},
		{Name: "a", Handle: func(context.Context, string) (string, error) { return "", nil }},
	}})
	if err == nil {
		t.Fatal("重名路由应报错")
	}
}

func TestRouterClassifyError(t *testing.T) {
	r, err := New(&Config{Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: "not json"}}}, Routes: testRoutes()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Classify(context.Background(), "x"); err == nil {
		t.Fatal("非法 JSON 应报错（GenerateJSON 回喂后仍失败）")
	} else if !errors.Is(err, err) {
		t.Fatal("unreachable")
	}
}

// Classify 门槛自守：低置信直接报错（编排器只取 Classify 也受 MinConfidence 约束），
// Decision 仍返回供可观测。
func TestRouterClassifyGatesLowConfidence(t *testing.T) {
	r, err := New(&Config{
		Model:  &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: `{"route":"bug-fix","confidence":0.3,"reason":"不确定"}`}}},
		Routes: testRoutes(), MinConfidence: 0.6,
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.Classify(context.Background(), "x")
	if err == nil {
		t.Fatal("低置信分类应报错")
	}
	if !strings.Contains(err.Error(), "置信度") {
		t.Fatalf("错误应说明门槛原因: %v", err)
	}
	if d.Route != "bug-fix" {
		t.Fatalf("Decision 应随错误返回供可观测: %+v", d)
	}
}

// 未知名（含 "none"）在 Classify 即报错，不再放行给调用方。
func TestRouterClassifyGatesUnknownRoute(t *testing.T) {
	r, err := New(&Config{
		Model:  &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: `{"route":"none","confidence":1.0,"reason":"没有合适类别"}`}}},
		Routes: testRoutes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Classify(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "不在路由表") {
		t.Fatalf("未知名应报错: %v", err)
	}
}

// MinConfidence=0 = 不设门槛：低置信也放行（原始语义保留）。
func TestRouterClassifyNoGateWhenDisabled(t *testing.T) {
	r, err := New(&Config{
		Model:  &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: `{"route":"explain","confidence":0.1,"reason":"随便"}`}}},
		Routes: testRoutes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d, err := r.Classify(context.Background(), "x"); err != nil || d.Route != "explain" {
		t.Fatalf("零门槛不应报错: %+v %v", d, err)
	}
}

// 置信度越界钳位：LLM 幻觉出 1.5 不能借越界值绕过门槛，负值不被判为必拒。
func TestRouterClassifyClampsConfidence(t *testing.T) {
	r, err := New(&Config{
		Model:  &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: `{"route":"bug-fix","confidence":1.5,"reason":"幻觉"}`}}},
		Routes: testRoutes(), MinConfidence: 0.9,
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.Classify(context.Background(), "x")
	if err != nil || d.Confidence != 1.0 {
		t.Fatalf("越界置信度应钳位到 1.0: %+v %v", d, err)
	}
}

// 槽位提取：同一分类调用顺带提取已配置槽位；未配置槽位名一律丢弃（不透传模型幻觉），
// 空值丢弃、超长值截断；未配置 Slots 时提示词保持纯选路原样。
func TestRouterClassifySlots(t *testing.T) {
	r, err := New(&Config{
		Model: &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{
			{Content: `{"route":"bug-fix","confidence":0.9,"reason":"修代码","slots":{"plan":"true","hack":"x","empty":"  ","long":"` + strings.Repeat("长", 80) + `"}}`}},
		},
		Routes: testRoutes(),
		Slots: []SlotSpec{
			{Name: "plan", Description: "是否要方案（true/false）"},
			{Name: "long", Description: "超长值截断用"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.Classify(context.Background(), "修复这个空指针")
	if err != nil {
		t.Fatal(err)
	}
	if d.Slots["plan"] != "true" {
		t.Fatalf("已配置槽位应保留: %+v", d.Slots)
	}
	if _, ok := d.Slots["hack"]; ok {
		t.Fatalf("未配置槽位应丢弃: %+v", d.Slots)
	}
	if _, ok := d.Slots["empty"]; ok {
		t.Fatalf("空值槽位应丢弃: %+v", d.Slots)
	}
	if got := d.Slots["long"]; len(got) != slotValueCap {
		t.Fatalf("超长槽位值应截断到 %d: %d", slotValueCap, len(got))
	}
}

// 未配置 Slots：提示词不含槽位段，模型多给的 slots 一律丢弃（Decision.Slots=nil）。
func TestRouterClassifyNoSlotsConfigDropsReplySlots(t *testing.T) {
	m := &llmtest.Model{RepeatLast: true, Script: []llmtest.Resp{{Content: `{"route":"explain","confidence":0.9,"reason":"解释","slots":{"plan":"true"}}`}}}
	r, err := New(&Config{Model: m, Routes: testRoutes()})
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.Classify(context.Background(), "什么是空指针")
	if err != nil || d.Slots != nil {
		t.Fatalf("未配置槽位应丢弃且回复 slots 不透传: %+v %v", d.Slots, err)
	}
	if prompt := systemPromptOf(m); strings.Contains(prompt, "slots") {
		t.Fatalf("纯选路提示词不应含槽位段: %q", prompt)
	}
}

// systemPromptOf 返回分类调用的 system 消息（提示词断言用）。
func systemPromptOf(m *llmtest.Model) string { return m.FirstInput }
