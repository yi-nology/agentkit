package acpx

import (
	"context"
	"strings"
	"testing"
)

func TestNewAgentNames(t *testing.T) {
	cases := []struct {
		a    Agent
		want string
	}{
		{NewKimi(), "kimi"},
		{NewQwen(), "qwen"},
		{NewGemini(), "gemini"},
		{NewMimo(), "mimo"},
		{NewMinimax(), "minimax"},
	}
	for _, c := range cases {
		if c.a.Name() != c.want {
			t.Errorf("Name = %q, want %q", c.a.Name(), c.want)
		}
	}
}

func TestBuiltinCapabilities(t *testing.T) {
	// 能力矩阵回归：声明与适配器实际支持的 argv 映射一一对应——
	// 声明多于实际会漏放（fail-fast 失效），少于实际会误拒合法请求。
	cases := []struct {
		a    Agent
		want Capability
	}{
		{NewClaudeCode(), Capability{Model: true, Session: true, MaxTurns: true, AllowedTools: true, Sandbox: true}},
		{NewZCode(), Capability{Model: true, Session: true, MaxTurns: true, AllowedTools: true, Sandbox: true}},
		{NewCodex(), Capability{Model: true, Sandbox: true}},
		{NewOpenCode(), Capability{Model: true, Session: true}},
		{NewGemini(), Capability{Model: true, Sandbox: true}},
		{NewQwen(), Capability{Model: true, Sandbox: true}},
		{NewKimi(), Capability{Model: true, Session: true}},
		{NewMimo(), Capability{Model: true, Session: true}},
		{NewMinimax(), Capability{}}, // 模板只含 {prompt}
	}
	for _, c := range cases {
		if got := c.a.Capabilities(); got != c.want {
			t.Errorf("%s Capabilities = %+v, want %+v", c.a.Name(), got, c.want)
		}
	}
	// GenericAgent 模板占位推导。
	g := NewGenericAgent("x", []string{"run", "{prompt}", "--model", "{model}", "--session", "{session}"}, false)
	if got := g.Capabilities(); got != (Capability{Model: true, Session: true}) {
		t.Errorf("GenericAgent 模板推导 = %+v, want {Model,Session}", got)
	}
}

func TestRegistryRunRejectsUnsupportedFields(t *testing.T) {
	// 行为变化回归：请求了 agent 声明不支持的字段必须 fail-fast，
	// 不得静默丢弃（kimi 收到 Sandbox=readonly 实则全自主裸跑的历史教训）。
	r := NewRegistry()
	req := RunRequest{Prompt: "p", Sandbox: SandboxReadonly}
	if _, err := r.Run(context.Background(), "kimi", req); err == nil ||
		!strings.Contains(err.Error(), "Sandbox") {
		t.Fatalf("kimi + Sandbox 应 fail-fast 且点名字段: %v", err)
	}
	// 多个不支持字段全部点名。
	req2 := RunRequest{Prompt: "p", Sandbox: SandboxReadonly, MaxTurns: 3, AllowedTools: []string{"Read"}}
	_, err := r.Run(context.Background(), "kimi", req2)
	if err == nil || !strings.Contains(err.Error(), "MaxTurns") || !strings.Contains(err.Error(), "AllowedTools") {
		t.Fatalf("应同时点名 MaxTurns/AllowedTools: %v", err)
	}
	// 声明支持的字段组合不触发能力错误（执行走 stub 之外的正常路径，此处只看错误类别——
	// kimi 未安装会报执行错误而非能力错误）。
	_, err = r.Run(context.Background(), "kimi", RunRequest{Prompt: "p", Model: "m", SessionID: "s"})
	if err != nil && strings.Contains(err.Error(), "静默降级") {
		t.Fatalf("支持的字段不得被能力校验拒绝: %v", err)
	}
	// 自定义 agent 全零能力：任何控制面字段都被拒。
	r.Register(&stubAgent{name: "stub"})
	if _, err := r.Run(context.Background(), "stub", RunRequest{Prompt: "p", Model: "m"}); err == nil {
		t.Fatal("零能力 agent + Model 应被拒")
	}
}

func TestUnsupportedFieldsOrder(t *testing.T) {
	// 字段按声明序输出（错误信息稳定可断言）。
	got := unsupportedFields(NewKimi(), RunRequest{
		Prompt: "p", Model: "m", Sandbox: "full", MaxTurns: 2, AllowedTools: []string{"Bash"},
	})
	want := []string{"MaxTurns", "AllowedTools", "Sandbox"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
