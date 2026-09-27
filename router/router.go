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

// SlotSpec 意图槽位定义：分类调用在选路的同时顺带提取的附加意图维度
// （如「是否要方案」）。与选路共用一次 LLM 调用（零额外延迟/费用）；
// 槽位是选路之外的正交维度——「要方案」不改变走哪条链，只改变链内行为。
type SlotSpec struct {
	// Name 槽位名（回复 JSON slots 对象的 key；短横线/字母风格，如 "plan"）。
	Name string
	// Description 取值语义（合法值与判据，值一律字符串，如 "true"/"false"）。
	Description string
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
	// Slots 意图槽位（nil = 纯选路：提示词与解析保持原样，模型多给的 slots 一律丢弃）。
	Slots []SlotSpec
}

// Decision 分类决策（可观测）。
type Decision struct {
	Route      string  `json:"route"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	// Slots 槽位提取结果（仅含已配置槽位；未配置/模型未给 = nil）。
	Slots map[string]string `json:"slots,omitempty"`
}

// Router 路由器（构建后只读，并发安全）。
type Router struct {
	client     *llm.Client
	routes     []Route
	byName     map[string]Route
	slots      []SlotSpec
	slotByName map[string]bool
	minCF      float64
	fb         func(ctx context.Context, input string, reason string) (string, error)
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
	slotByName := map[string]bool{}
	for _, s := range cfg.Slots {
		if s.Name == "" || s.Description == "" {
			return nil, fmt.Errorf("router: 槽位 %q 缺 Name 或 Description", s.Name)
		}
		if slotByName[s.Name] {
			return nil, fmt.Errorf("router: 槽位名重复 %q", s.Name)
		}
		slotByName[s.Name] = true
	}
	return &Router{
		client:     llm.NewClient(cfg.Model, cfg.ModelName, nil),
		routes:     cfg.Routes,
		byName:     byName,
		slots:      cfg.Slots,
		slotByName: slotByName,
		minCF:      cfg.MinConfidence,
		fb:         cfg.Fallback,
	}, nil
}

// Classify 意图分类（不执行）。
// 门槛自守：分类合法性（结果不在路由表，含 "none"）与 MinConfidence 置信度下限
// 在本方法统一校验，未过门槛返回携带原因的错误（Decision 仍返回，供调用方可观测）——
// 只取 Classify 的编排器与 Do 分发共用同一闸门，不会出现"配置了阈值但没人校验"的死配置。
// 配置了 Slots 时同一调用顺带提取槽位（提示词追加槽位说明；模型未给的槽位不出现在结果里，
// 未配置的槽位名一律丢弃——槽位提取失败不影响选路本身）。
func (r *Router) Classify(ctx context.Context, input string) (Decision, error) {
	var b strings.Builder
	b.WriteString("把用户输入路由到最合适的处理类别。\n\n可用类别：\n")
	for _, rt := range r.routes {
		fmt.Fprintf(&b, "- %s: %s\n", rt.Name, rt.Description)
	}
	if len(r.slots) > 0 {
		b.WriteString("\n同时从输入提取以下意图槽位，放进 slots 对象（无法判断的槽位省略，值用字符串）：\n")
		for _, s := range r.slots {
			fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
		}
		b.WriteString("\n只输出 JSON：{\"route\":\"类别名\",\"confidence\":0到1,\"reason\":\"一句话依据\",\"slots\":{\"槽位名\":\"值\"}}。" +
			"没有合适类别时 route 填 \"none\"。")
	} else {
		b.WriteString("\n只输出 JSON：{\"route\":\"类别名\",\"confidence\":0到1,\"reason\":\"一句话依据\"}。" +
			"没有合适类别时 route 填 \"none\"。")
	}

	var d Decision
	err := r.client.GenerateJSON(ctx, "router:classify", []*schema.Message{
		schema.SystemMessage(b.String()), schema.UserMessage(input),
	}, &d)
	if err != nil {
		return Decision{}, fmt.Errorf("router: 分类失败: %w", err)
	}
	d.Route = strings.ToLower(strings.TrimSpace(d.Route))
	d.Confidence = clamp01(d.Confidence) // LLM 幻觉防护：越界置信度统一钳位
	d.Slots = sanitizeSlots(d.Slots, r.slotByName)
	if _, ok := r.byName[d.Route]; !ok {
		return d, fmt.Errorf("router: 分类结果 %q 不在路由表", d.Route)
	}
	if r.minCF > 0 && d.Confidence < r.minCF {
		return d, fmt.Errorf("router: 置信度 %.2f 低于阈值 %.2f", d.Confidence, r.minCF)
	}
	return d, nil
}

// slotValueCap 单个槽位值长度上限（LLM 幻觉防护：槽位值是短枚举/布尔，不是内容字段）。
const slotValueCap = 64

// sanitizeSlots 槽位提取结果自守恒：只保留已配置槽位名，去空白、丢空值、截断超长值。
func sanitizeSlots(in map[string]string, configured map[string]bool) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if !configured[k] {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if len(v) > slotValueCap {
			v = v[:slotValueCap]
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Run 分类 + 分发执行：门槛未过（Classify 报错）时走 Fallback 处理链。
// （v0.10.11 自 Do 改名——全仓主执行方法词表统一为 Run，与 acpx.Registry.Run、
// agentrun 同词表。）
func (r *Router) Run(ctx context.Context, input string) (Decision, string, error) {
	d, err := r.Classify(ctx, input)
	if err != nil {
		if r.fb != nil {
			out, ferr := r.fb(ctx, input, err.Error())
			return d, out, ferr
		}
		return d, "", err
	}
	out, err := r.byName[d.Route].Handle(ctx, input)
	return d, out, err
}

// clamp01 把置信度钳位到 [0,1]。
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
