// Package router 路由（Router）架构原语：LLM 意图分类 → 选路 → 分发执行。
//
// 与 skill 包"agent 自主 use_skill"（隐式路由，决策权在执行 agent）不同，
// router 是显式路由：先用一次廉价分类调用决定走哪条处理链，再交给对应 handler
// （handler 可以是 ReAct agent、Plan-and-Execute、单轮生成——任意编排）。
// 适用：入口流量可枚举为有限意图类别、各类别处理链差异大的场景。
package router

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/yi-nology/agentkit/llm"
)

// Route 一条路由：意图类别 + 处理链。
type Route struct {
	// Name 路由名（分类目标词，须唯一；小写短横线风格，如 "bug-fix"）。
	Name string
	// Description 意图描述（分类依据——写清"什么样的问题走这条"）。
	Description string
	// Handle 处理链（任意编排：单轮生成 / ReAct / P&E / 纯函数）。
	Handle func(ctx context.Context, input string) (string, error)
}

// Config 路由器配置。
type Config struct {
	// Model 分类模型（一次 GenerateJSON 调用；用快模型即可）。
	Model model.BaseChatModel
	// ModelName 分类调用记账标识。
	ModelName string
	// Routes 路由表（≥1 条；Name 唯一）。
	Routes []Route
	// MinConfidence 置信度下限（0~1；低于则走 Fallback。0 = 不设门槛）。
	MinConfidence float64
	// Fallback 兜底处理链（nil = 低置信/无法分类时报错）。
	Fallback func(ctx context.Context, input string, reason string) (string, error)
}

// Decision 分类决策（可观测）。
type Decision struct {
	Route      string  `json:"route"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// Router 路由器（构建后只读，并发安全）。
type Router struct {
	client *llm.Client
	routes []Route
	byName map[string]Route
	minCF  float64
	fb     func(ctx context.Context, input string, reason string) (string, error)
}

// New 创建路由器。
func New(cfg *Config) (*Router, error) {
	if cfg == nil || cfg.Model == nil {
		return nil, fmt.Errorf("router: Model 不能为空")
	}
	if len(cfg.Routes) == 0 {
		return nil, fmt.Errorf("router: Routes 不能为空")
	}
	byName := map[string]Route{}
	for _, r := range cfg.Routes {
		if r.Name == "" || r.Handle == nil {
			return nil, fmt.Errorf("router: 路由 %q 缺 Name 或 Handle", r.Name)
		}
		if _, dup := byName[r.Name]; dup {
			return nil, fmt.Errorf("router: 路由名重复 %q", r.Name)
		}
		byName[r.Name] = r
	}
	return &Router{
		client: llm.NewClient(cfg.Model, cfg.ModelName, nil),
		routes: cfg.Routes,
		byName: byName,
		minCF:  cfg.MinConfidence,
		fb:     cfg.Fallback,
	}, nil
}

// Classify 意图分类（不执行）。
func (r *Router) Classify(ctx context.Context, input string) (Decision, error) {
	var b strings.Builder
	b.WriteString("把用户输入路由到最合适的处理类别。\n\n可用类别：\n")
	for _, rt := range r.routes {
		fmt.Fprintf(&b, "- %s: %s\n", rt.Name, rt.Description)
	}
	b.WriteString("\n只输出 JSON：{\"route\":\"类别名\",\"confidence\":0到1,\"reason\":\"一句话依据\"}。" +
		"没有合适类别时 route 填 \"none\"。")

	var d Decision
	err := r.client.GenerateJSON(ctx, "router:classify", []*schema.Message{
		schema.SystemMessage(b.String()), schema.UserMessage(input),
	}, &d)
	if err != nil {
		return Decision{}, fmt.Errorf("router: 分类失败: %w", err)
	}
	d.Route = strings.ToLower(strings.TrimSpace(d.Route))
	return d, nil
}

// Do 分类 + 分发执行。
func (r *Router) Do(ctx context.Context, input string) (Decision, string, error) {
	d, err := r.Classify(ctx, input)
	if err != nil {
		return d, "", err
	}
	rt, ok := r.byName[d.Route]
	if !ok || (r.minCF > 0 && d.Confidence < r.minCF) {
		reason := "无法分类"
		if !ok {
			reason = fmt.Sprintf("分类结果 %q 不在路由表", d.Route)
		} else {
			reason = fmt.Sprintf("置信度 %.2f 低于阈值 %.2f", d.Confidence, r.minCF)
		}
		if r.fb != nil {
			out, ferr := r.fb(ctx, input, reason)
			return d, out, ferr
		}
		return d, "", fmt.Errorf("router: %s", reason)
	}
	out, err := rt.Handle(ctx, input)
	return d, out, err
}
