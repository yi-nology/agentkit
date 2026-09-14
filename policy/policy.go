// Package policy 操作审计门：多种会话执行策略模式（confirm/auto/plan/full）下的
// 统一操作裁决——每个操作必过、三值裁决、全程留痕。模式改变的是裁决策略，不是移除
// 审计点；红线（变异工具拒绝、高危恒人审）在矩阵层硬编码，任何模式不可绕过。
//
// 裁决分层：例外规则（Policy.Rules，首个命中即胜）→ 模式×风险矩阵 → 兜底 need_human
// （未知形态 fail-safe）。灰区可挂 Arbiter 仲裁插件，失败/非法输出一律 fail-safe 升人审
// ——「只升不降」在 fail-safe 方向恒成立。工具调用面的装饰器见 WithAuditGate。
//
// 沉淀自 bianque engine/policy（v0.9.6）：领域包 yaml 路径约定改为显式入参
// （LoadOverrides path），其余原样。
package policy

import (
	"context"
	"fmt"
)

// Mode 会话执行策略模式。
type Mode string

const (
	ModeConfirm Mode = "confirm" // 变更前确认（缺省；=存量行为）
	ModeAuto    Mode = "auto"    // 低风险（≤auto_max_risk）系统代批
	ModePlan    Mode = "plan"    // 计划模式：方案只审不执行
	ModeFull    Mode = "full"    // 完全访问：≤full_max_risk 代批（最高危恒人工双确认）
)

// Normalize 归一：空/非法一律回落 confirm。非法值在 API 入口已被 400 拦截
// （ValidMode），这里是纵深兜底——审计门不允许因模式脏值放大权限。
func Normalize(s string) Mode {
	switch Mode(s) {
	case ModeConfirm, ModeAuto, ModePlan, ModeFull:
		return Mode(s)
	}
	return ModeConfirm
}

// ValidMode API 入参严格校验（空串返回 false，由调用方走缺省档）。
func ValidMode(s string) bool {
	switch Mode(s) {
	case ModeConfirm, ModeAuto, ModePlan, ModeFull:
		return true
	}
	return false
}

// Verdict 裁决值：三值裁决 + 两个过程值（plan_only=计划模式只审不执行；
// unknown=灰区待仲裁，仅例外规则可产出）。
type Verdict string

const (
	VerdictAuto      Verdict = "auto_proceed"
	VerdictNeedHuman Verdict = "need_human"
	VerdictDeny      Verdict = "deny"
	VerdictPlanOnly  Verdict = "plan_only"
	VerdictUnknown   Verdict = "unknown"
)

// OpType 操作类型：工具调用面 / 确定性执行面 / 方案提审面。
type OpType string

const (
	OpToolCall    OpType = "tool_call"    // LLM ReAct 采集面单次工具调用
	OpExecuteStep OpType = "execute_step" // 已批准方案的确定性执行步骤（审批上游把关，审计复读）
	OpPlanSubmit  OpType = "plan_submit"  // 执行类方案提审（风险等级驱动）
)

// Decider 裁决者（审计「这次放行是谁裁的」）：human 仅审批闸固有语义，gate 不产生。
type Decider string

const (
	DeciderRule   Decider = "rule"          // 规则/矩阵
	DeciderLLM    Decider = "llm"           // 灰区仲裁插件
	DeciderHuman  Decider = "human"         // 人工审批
	DeciderSystem Decider = "system_policy" // 策略代批（审批单 decided_by 同值）
)

// Op 一次待裁决操作。
type Op struct {
	Type     OpType
	Mode     Mode
	Tool     string // 工具名（tool_call/execute_step）
	Risk     int    // 1-4（plan_submit）；0=未评级
	Mutating bool   // 工具名命中变异动词段（agentrun.IsMutatingTool）
}

// Decision 裁决结果（审计事件 payload 的语义源）。
type Decision struct {
	Verdict Verdict
	Decider Decider
	RuleID  string
	Reason  string
}

// Policy 策略表：代批阈值 + 例外规则。yaml 外置（LoadOverrides，缺文件用
// DefaultPolicy 基座；解析失败 fail-fast）。
type Policy struct {
	AutoMaxRisk int    `yaml:"auto_max_risk"`
	FullMaxRisk int    `yaml:"full_max_risk"`
	Rules       []Rule `yaml:"rules"`
}

// Rule 例外规则（首个命中即裁决，优先于矩阵层）。
type Rule struct {
	ID       string   `yaml:"id"`
	OpTypes  []string `yaml:"op_types"` // 空=全部操作类型
	Tools    []string `yaml:"tools"`    // 工具名精确匹配；空=不限
	MinRisk  int      `yaml:"min_risk"` // 含（对 plan_submit 的 Risk 生效；0=不限）
	MaxRisk  int      `yaml:"max_risk"` // 含；0=不限
	Mutating *bool    `yaml:"mutating"` // nil=不限
	Verdict  Verdict  `yaml:"verdict"`  // auto_proceed|need_human|deny|unknown
	Reason   string   `yaml:"reason"`
}

func (r Rule) matches(op Op) bool {
	if len(r.OpTypes) > 0 && !contains(r.OpTypes, string(op.Type)) {
		return false
	}
	if len(r.Tools) > 0 && !contains(r.Tools, op.Tool) {
		return false
	}
	if r.MinRisk > 0 && op.Risk < r.MinRisk {
		return false
	}
	if r.MaxRisk > 0 && op.Risk > r.MaxRisk {
		return false
	}
	if r.Mutating != nil && *r.Mutating != op.Mutating {
		return false
	}
	return true
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Arbiter 灰区仲裁插件（可选）。契约：只允许返回 auto_proceed/need_human/deny；
// 失败或非法输出由 Gate fail-safe 升人审——「只升不降」在 fail-safe 方向恒成立。
type Arbiter interface {
	Arbitrate(ctx context.Context, op Op) (Verdict, string, error)
}

// Gate 操作审计门。Decide 只读 policy，并发安全，可放工具调用热路径。
type Gate struct {
	policy  *Policy
	arbiter Arbiter
}

// NewGate 构造；p 为 nil 用内置基座。
func NewGate(p *Policy, a Arbiter) *Gate {
	if p == nil {
		p = DefaultPolicy()
	}
	return &Gate{policy: p, arbiter: a}
}

// Decide 裁决：例外规则 → 矩阵层 → 兜底 need_human（未知形态 fail-safe）。
func (g *Gate) Decide(ctx context.Context, op Op) Decision {
	for _, r := range g.policy.Rules {
		if !r.matches(op) {
			continue
		}
		if r.Verdict != VerdictUnknown {
			return Decision{Verdict: r.Verdict, Decider: DeciderRule, RuleID: r.ID, Reason: r.Reason}
		}
		if g.arbiter == nil {
			return Decision{Verdict: VerdictNeedHuman, Decider: DeciderRule, RuleID: r.ID,
				Reason: "灰区规则命中且未配置仲裁器，fail-safe 升人审: " + r.Reason}
		}
		v, reason, err := g.arbiter.Arbitrate(ctx, op)
		if err != nil {
			return Decision{Verdict: VerdictNeedHuman, Decider: DeciderRule, RuleID: r.ID,
				Reason: fmt.Sprintf("仲裁失败（%v），fail-safe 升人审", err)}
		}
		if v != VerdictAuto && v != VerdictNeedHuman && v != VerdictDeny {
			return Decision{Verdict: VerdictNeedHuman, Decider: DeciderRule, RuleID: r.ID, Reason: "仲裁输出非法，fail-safe 升人审"}
		}
		return Decision{Verdict: v, Decider: DeciderLLM, RuleID: r.ID, Reason: reason}
	}
	switch op.Type {
	case OpToolCall:
		if op.Mutating {
			return Decision{Verdict: VerdictDeny, Decider: DeciderRule, RuleID: "mutating_tool_guard",
				Reason: "采集面工具命中变异动词段：变更必须走方案审批面，LLM 自主变异调用一律拒绝（任何模式）"}
		}
		return Decision{Verdict: VerdictAuto, Decider: DeciderRule, RuleID: "readonly_allowlist", Reason: "白名单内只读采集工具"}
	case OpExecuteStep:
		return Decision{Verdict: VerdictAuto, Decider: DeciderRule, RuleID: "approved_plan",
			Reason: "已批准方案的确定性执行步骤（审批+一次性凭据上游把关，此处审计复读）"}
	case OpPlanSubmit:
		return g.decidePlanSubmit(op)
	default:
		return Decision{Verdict: VerdictNeedHuman, Decider: DeciderRule, RuleID: "unknown_op", Reason: "未知操作类型，fail-safe 升人审"}
	}
}

// decidePlanSubmit 方案提审矩阵：模式×风险的代批范围裁决。
func (g *Gate) decidePlanSubmit(op Op) Decision {
	switch op.Mode {
	case ModePlan:
		return Decision{Verdict: VerdictPlanOnly, Decider: DeciderRule, RuleID: "plan_mode", Reason: "计划模式：方案只审不执行"}
	case ModeAuto:
		if op.Risk >= 1 && op.Risk <= g.policy.AutoMaxRisk {
			return Decision{Verdict: VerdictAuto, Decider: DeciderRule, RuleID: "auto_matrix",
				Reason: fmt.Sprintf("auto 模式：风险 L%d ≤ 代批上限 L%d，系统代批", op.Risk, g.policy.AutoMaxRisk)}
		}
		return Decision{Verdict: VerdictNeedHuman, Decider: DeciderRule, RuleID: "auto_matrix",
			Reason: fmt.Sprintf("auto 模式：风险 L%d 超代批上限 L%d，升人审", op.Risk, g.policy.AutoMaxRisk)}
	case ModeFull:
		if op.Risk >= 1 && op.Risk <= g.policy.FullMaxRisk {
			return Decision{Verdict: VerdictAuto, Decider: DeciderRule, RuleID: "full_matrix",
				Reason: fmt.Sprintf("full 模式：风险 L%d ≤ 代批上限 L%d，系统代批", op.Risk, g.policy.FullMaxRisk)}
		}
		return Decision{Verdict: VerdictNeedHuman, Decider: DeciderRule, RuleID: "full_matrix",
			Reason: fmt.Sprintf("full 模式：风险 L%d 超代批上限 L%d，升人审（最高危恒人工双确认）", op.Risk, g.policy.FullMaxRisk)}
	default: // confirm 与未归一模式
		return Decision{Verdict: VerdictNeedHuman, Decider: DeciderRule, RuleID: "confirm_default", Reason: "confirm 模式（缺省）：变更前人工审批"}
	}
}
