// StageRouter 分阶段模型路由：不同业务阶段（stage）用不同的 Generator——
// 大窗口模型吃长输入（R1）、快模型跑判定（R3）、强模型保质量（R4）。
//
// 匹配语义：精确命中优先，其次最长前缀（注册 "R1" 可命中 "R1"/"R1a"）；
// 未命中走缺省 Generator。装配示例：
//
//	sr := llm.NewStageRouter(defaultGen)      // 缺省 = 主链（含预算注入）
//	sr.Use("R1", r1Gen)                        // 前缀路由，命中 "R1"/"R1a"
//	sr.Use("qa", qaGen)                        // 精确路由
//	// 之后 sr.Generate(ctx, "R1a", ...) 走 r1Gen
//
// 预算：路由不改变预算语义——各 Generator 应由调用方完成预算注入
// （BudgetInjector.WithBudget）后再注册；UsedTokens 聚合全部已注册链。
package llm

import (
	"context"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// stageRoute 一条阶段路由。
type stageRoute struct {
	prefix string
	gen    Generator
}

// StageRouter 按 stage 分发的 Generator（Generator 接口实现，可无感嵌入现有装配）。
type StageRouter struct {
	mu     sync.RWMutex
	def    Generator
	routes []stageRoute // 注册序；匹配时精确优先，其后最长前缀优先
}

// NewStageRouter 创建阶段路由器（def = 未命中阶段的缺省 Generator）。
// def 为 nil panic——构造期配置错误尽早暴露（与 toolprior.Table.Add 同纪律），
// 运行期才炸的 nil 难排查。
func NewStageRouter(def Generator) *StageRouter {
	if def == nil {
		panic("llm: NewStageRouter def 不能为 nil")
	}
	return &StageRouter{def: def}
}

// Use 注册阶段路由。stage 为精确名（"qa"）或前缀（"R1" 命中 "R1a"）；
// 重名注册覆盖旧路由。
func (r *StageRouter) Use(stage string, gen Generator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.routes {
		if r.routes[i].prefix == stage {
			r.routes[i].gen = gen
			return
		}
	}
	r.routes = append(r.routes, stageRoute{prefix: stage, gen: gen})
}

// pick 精确优先、最长前缀其次。
func (r *StageRouter) pick(stage string) Generator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var best *stageRoute
	for i := range r.routes {
		rt := &r.routes[i]
		if rt.prefix == stage {
			return rt.gen // 精确命中
		}
		if strings.HasPrefix(stage, rt.prefix) {
			if best == nil || len(rt.prefix) > len(best.prefix) {
				best = rt
			}
		}
	}
	if best != nil {
		return best.gen
	}
	return r.def
}

func (r *StageRouter) Generate(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, error) {
	return r.pick(stage).Generate(ctx, stage, msgs)
}

func (r *StageRouter) GenerateJSON(ctx context.Context, stage string, msgs []*schema.Message, out any) error {
	return r.pick(stage).GenerateJSON(ctx, stage, msgs, out)
}

// RawModel 缺省链的底层模型（StageRouter 本身不做 ReAct——ReAct 需要哪个模型
// 由调用方显式指定）。
func (r *StageRouter) RawModel() model.BaseChatModel { return r.def.RawModel() }

// UsedTokens 全部链（缺省 + 已注册）的预算累计和——任务预算口径下各链共享
// 同一 Budget 时即任务总消耗。暴露 BudgetHolder 的链按预算指针身份去重，
// 避免共享预算被按链数倍增；未暴露的链退回逐链求和。
func (r *StageRouter) UsedTokens() int {
	r.mu.RLock()
	gens := make([]Generator, 0, len(r.routes)+1)
	gens = append(gens, r.def)
	for i := range r.routes {
		gens = append(gens, r.routes[i].gen)
	}
	r.mu.RUnlock()

	seen := map[TokenAccountant]struct{}{}
	total := 0
	for _, g := range gens {
		bh, ok := g.(BudgetHolder)
		if !ok {
			total += g.UsedTokens()
			continue
		}
		acc := bh.BoundBudget()
		if acc == nil {
			continue
		}
		if _, dup := seen[acc]; dup { // 共享同一预算只计一次
			continue
		}
		seen[acc] = struct{}{}
		total += acc.Used()
	}
	return total
}
