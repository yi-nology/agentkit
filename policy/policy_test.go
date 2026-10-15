package policy

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
)

func TestDecideMatrix(t *testing.T) {
	g := NewGate(nil, nil)
	cases := []struct {
		name string
		op   Op
		want Verdict
		rule string
	}{
		{"confirm L1 提审→人审", Op{Type: OpPlanSubmit, Mode: ModeConfirm, Risk: 1}, VerdictNeedHuman, "confirm_default"},
		{"confirm L4 提审→人审", Op{Type: OpPlanSubmit, Mode: ModeConfirm, Risk: 4}, VerdictNeedHuman, "confirm_default"},
		{"auto L2→代批", Op{Type: OpPlanSubmit, Mode: ModeAuto, Risk: 2}, VerdictAuto, "auto_matrix"},
		{"auto L3→人审", Op{Type: OpPlanSubmit, Mode: ModeAuto, Risk: 3}, VerdictNeedHuman, "auto_matrix"},
		{"auto L4→人审", Op{Type: OpPlanSubmit, Mode: ModeAuto, Risk: 4}, VerdictNeedHuman, "auto_matrix"},
		{"full L3→代批", Op{Type: OpPlanSubmit, Mode: ModeFull, Risk: 3}, VerdictAuto, "full_matrix"},
		{"full L4→人审（双确认不豁免）", Op{Type: OpPlanSubmit, Mode: ModeFull, Risk: 4}, VerdictNeedHuman, "full_matrix"},
		{"plan L1→只审不执行", Op{Type: OpPlanSubmit, Mode: ModePlan, Risk: 1}, VerdictPlanOnly, "plan_mode"},
		{"采集面只读→放行", Op{Type: OpToolCall, Mode: ModeConfirm, Tool: "get_load"}, VerdictAuto, "readonly_allowlist"},
		{"采集面变异→拒绝（任何模式）", Op{Type: OpToolCall, Mode: ModeFull, Tool: "shell_execute", Mutating: true}, VerdictDeny, "mutating_tool_guard"},
		{"执行面已批准步骤→放行", Op{Type: OpExecuteStep, Mode: ModeAuto, Tool: "shell_execute", Mutating: true}, VerdictAuto, "approved_plan"},
		{"未知操作类型→fail-safe 人审", Op{Type: "weird", Mode: ModeFull}, VerdictNeedHuman, "unknown_op"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := g.Decide(context.Background(), tc.op)
			if got.Verdict != tc.want || got.RuleID != tc.rule {
				t.Fatalf("got %+v, want verdict=%s rule=%s", got, tc.want, tc.rule)
			}
			if got.Decider != DeciderRule {
				t.Fatalf("矩阵/规则裁决 decider 应为 rule，got %s", got.Decider)
			}
		})
	}
}

func TestLoadOverrides(t *testing.T) {
	const path = "_shared/operation_policy.yaml"
	t.Run("缺文件回退基座", func(t *testing.T) {
		p, err := LoadOverrides(fstest.MapFS{}, path)
		if err != nil {
			t.Fatal(err)
		}
		if p.AutoMaxRisk != 2 || p.FullMaxRisk != 3 {
			t.Fatalf("基座阈值被改动: %+v", p)
		}
	})
	t.Run("阈值只能调低不能调高", func(t *testing.T) {
		fsys := fstest.MapFS{path: &fstest.MapFile{Data: []byte(
			"operation_policy:\n  auto_max_risk: 4\n  full_max_risk: 3\n")}}
		p, err := LoadOverrides(fsys, path)
		if err != nil {
			t.Fatal(err)
		}
		if p.AutoMaxRisk != 2 {
			t.Fatalf("auto_max_risk=4 应被 clamp 回 2，got %d", p.AutoMaxRisk)
		}
		fsys = fstest.MapFS{path: &fstest.MapFile{Data: []byte(
			"operation_policy:\n  auto_max_risk: 1\n  full_max_risk: 2\n")}}
		p, err = LoadOverrides(fsys, path)
		if err != nil {
			t.Fatal(err)
		}
		if p.AutoMaxRisk != 1 || p.FullMaxRisk != 2 {
			t.Fatalf("调低应生效，got %+v", p)
		}
	})
	t.Run("解析失败 fail-fast", func(t *testing.T) {
		fsys := fstest.MapFS{path: &fstest.MapFile{Data: []byte(
			"operation_policy: [broken")}}
		if _, err := LoadOverrides(fsys, path); err == nil {
			t.Fatal("解析失败应返回 error")
		}
	})
	t.Run("例外规则优先于矩阵层", func(t *testing.T) {
		fsys := fstest.MapFS{path: &fstest.MapFile{Data: []byte(
			"operation_policy:\n  rules:\n    - id: prod-host-guard\n      op_types: [plan_submit]\n      min_risk: 1\n      verdict: need_human\n      reason: 生产机变更恒人审\n")}}
		p, err := LoadOverrides(fsys, path)
		if err != nil {
			t.Fatal(err)
		}
		g := NewGate(p, nil)
		got := g.Decide(context.Background(), Op{Type: OpPlanSubmit, Mode: ModeFull, Risk: 2})
		if got.Verdict != VerdictNeedHuman || got.RuleID != "prod-host-guard" {
			t.Fatalf("例外规则应优先于矩阵层，got %+v", got)
		}
	})
}

type stubArbiter struct {
	v      Verdict
	reason string
	err    error
}

func (s stubArbiter) Arbitrate(_ context.Context, _ Op) (Verdict, string, error) {
	return s.v, s.reason, s.err
}

func TestUnknownRuleArbiter(t *testing.T) {
	pol := &Policy{AutoMaxRisk: 2, FullMaxRisk: 3, Rules: []Rule{
		{ID: "gray", Verdict: VerdictUnknown, Reason: "灰区"},
	}}
	op := Op{Type: OpToolCall, Mode: ModeConfirm, Tool: "weird_tool"}
	t.Run("无仲裁器 fail-safe 升人审", func(t *testing.T) {
		got := NewGate(pol, nil).Decide(context.Background(), op)
		if got.Verdict != VerdictNeedHuman || got.Decider != DeciderRule {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("仲裁成功", func(t *testing.T) {
		got := NewGate(pol, stubArbiter{v: VerdictAuto, reason: "语义只读"}).Decide(context.Background(), op)
		if got.Verdict != VerdictAuto || got.Decider != DeciderLLM || got.Reason != "语义只读" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("仲裁失败 fail-safe", func(t *testing.T) {
		got := NewGate(pol, stubArbiter{err: errors.New("boom")}).Decide(context.Background(), op)
		if got.Verdict != VerdictNeedHuman {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("仲裁非法输出 fail-safe", func(t *testing.T) {
		got := NewGate(pol, stubArbiter{v: VerdictPlanOnly}).Decide(context.Background(), op)
		if got.Verdict != VerdictNeedHuman {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestModeNormalize(t *testing.T) {
	if Normalize("") != ModeConfirm || Normalize("hack") != ModeConfirm {
		t.Fatal("非法/空模式应回落 confirm")
	}
	for _, m := range []string{"confirm", "auto", "plan", "full"} {
		if !ValidMode(m) {
			t.Fatalf("%s 应合法", m)
		}
	}
	if ValidMode("") || ValidMode("Auto") {
		t.Fatal("空串/大小写不一致应非法（严格校验）")
	}
}
