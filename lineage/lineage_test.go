package lineage

import (
	"slices"
	"testing"

	"github.com/yi-nology/agentkit/pack"
	"github.com/yi-nology/agentkit/skill"
)

// fixture 专家 sec（挂摘覆盖引用 sop+pod，授予 sec server）；技能 sop requires_mcp sec；
// 契约清单 sec v2（collect/inspect）。
func fixture() (*Lineage, map[string]*pack.ToolManifest) {
	experts := []Expert{{
		Slug: "specialists/sec", Overridden: true,
		Skills: []string{"sop", "pod"},
		Grants: []Grant{{Server: "sec", Allow: []string{"collect", "undeclared-x"}}},
	}}
	skills := []Skill{
		{Name: "sop", Source: "_shared", Version: "1.1.0",
			RequiresMCP: []skill.MCPDep{{Server: "sec"}}},
		{Name: "pod", Source: "k8s", Version: "0.9.0"},
	}
	manifests := map[string]*pack.ToolManifest{
		"sec": {Server: "sec", Name: "sec", Version: "2.0.0",
			Tools: []pack.ManifestTool{{Name: "collect"}, {Name: "inspect"}}, From: "k8s"},
	}
	return Build(experts, skills, manifests), manifests
}

// TestBuild 边组装：used_by 覆盖感知、grants、requires_mcp、manifest 挂载。
func TestBuild(t *testing.T) {
	lin, _ := fixture()
	e := lin.Experts["specialists/sec"]
	if !e.Overridden || !slices.Equal(e.Skills, []string{"pod", "sop"}) ||
		!slices.Equal(e.Grants, []string{"sec"}) {
		t.Fatalf("专家边错误: %+v", e)
	}
	se := lin.Skills["sop"]
	if !slices.Equal(se.UsedBy, []string{"specialists/sec"}) || se.Source != "_shared" || se.Version != "1.1.0" {
		t.Fatalf("技能边错误: %+v", se)
	}
	m := lin.MCPS["sec"]
	if !slices.Equal(m.GrantedBy, []string{"specialists/sec"}) ||
		!slices.Equal(m.Tools, []string{"collect", "undeclared-x"}) ||
		m.Manifest == nil || m.Manifest.Version != "2.0.0" {
		t.Fatalf("MCP 边错误: %+v", m)
	}
	if !slices.Equal(m.UsedBySkills, []string{"sop"}) {
		t.Fatalf("UsedBySkills 错误: %+v", m)
	}
	// 未引用技能也登记（焦点查询可及）。
	if _, ok := lin.Skills["pod"]; !ok {
		t.Fatal("未引用技能应登记")
	}
	// nil 安全：nil 图 Focus 不炸。
	if _, ok := (*Lineage)(nil).Focus("x", 1); ok {
		t.Fatal("nil 图应返回 false")
	}
}

// TestFocus 焦点邻接子图：技能焦点含专家与 server；未知焦点 false；深度裁剪。
func TestFocus(t *testing.T) {
	lin, _ := fixture()
	res, ok := lin.Focus("sop", 1)
	if !ok {
		t.Fatal("sop 焦点应存在")
	}
	kinds := map[string]bool{}
	for _, n := range res.Nodes {
		kinds[n.Kind+"_"+n.Name] = true
	}
	if !kinds["expert_specialists/sec"] || !kinds["skill_sop"] || !kinds["server_sec"] {
		t.Fatalf("邻接节点不全: %+v", res.Nodes)
	}
	// 反向：server 焦点能回到专家与依赖技能。
	res2, _ := lin.Focus("sec", 2)
	names := map[string]bool{}
	for _, n := range res2.Nodes {
		names[n.Kind+"_"+n.Name] = true
	}
	if !names["expert_specialists/sec"] || !names["skill_sop"] {
		t.Fatalf("深度 2 邻接不全: %+v", res2.Nodes)
	}
	if _, ok := lin.Focus("nosuch", 1); ok {
		t.Fatal("未知焦点应 false")
	}
}

// TestDiff 版本/成熟度/弃用跃迁、工具面增减、引用边增减；nil 基线=首帧无 diff。
func TestDiff(t *testing.T) {
	prev, _ := fixture()
	next := Build([]Expert{{
		Slug: "specialists/sec",
		Skills: []string{"pod"},
		Grants: []Grant{{Server: "sec", Allow: []string{"collect"}}},
	}}, []Skill{
		{Name: "sop", Source: "_shared", Version: "1.2.0", Maturity: skill.MaturityDeprecated,
			Deprecated: &skill.Deprecated{RemoveAfter: "2026-01-01"}},
		{Name: "pod", Source: "k8s", Version: "0.9.0"},
	}, map[string]*pack.ToolManifest{
		"sec": {Server: "sec", Name: "sec", Version: "2.0.0",
			Tools: []pack.ManifestTool{{Name: "collect"}, {Name: "probe"}}, From: "k8s"},
	})

	impacts := Diff(prev, next)
	byType := map[string][]Impact{}
	for _, im := range impacts {
		byType[im.Type] = append(byType[im.Type], im)
	}
	if len(byType["skill_version_changed"]) != 1 || byType["skill_version_changed"][0].From != "1.1.0" || byType["skill_version_changed"][0].To != "1.2.0" {
		t.Fatalf("版本跃迁 impact 错误: %+v", byType["skill_version_changed"])
	}
	if len(byType["skill_maturity_changed"]) != 1 {
		t.Fatalf("成熟度跃迁 impact 缺失: %+v", byType)
	}
	if len(byType["deprecation"]) != 1 || byType["deprecation"][0].To != "2026-01-01" {
		t.Fatalf("弃用跃迁 impact 错误: %+v", byType["deprecation"])
	}
	tc := byType["tools_changed"]
	// 并集语义：prev 显式授予 undeclared-x 也计入（manifest∪授予），cur 侧它消失 → removed。
	if len(tc) != 1 || !slices.Equal(tc[0].ToolsAdded, []string{"probe"}) ||
		!slices.Equal(tc[0].ToolsRemoved, []string{"inspect", "undeclared-x"}) {
		t.Fatalf("工具面 impact 错误: %+v", tc)
	}
	rc := byType["refs_changed"]
	if len(rc) != 1 || rc[0].Expert != "specialists/sec" ||
		len(rc[0].ToolsAdded) != 0 || !slices.Equal(rc[0].ToolsRemoved, []string{"sop"}) {
		t.Fatalf("引用边 impact 错误: %+v", rc)
	}
	if got := Diff(prev, prev); len(got) != 0 {
		t.Fatalf("同图 diff 应为空: %+v", got)
	}
	if got := Diff(nil, next); len(got) != 0 {
		t.Fatalf("首帧应无 impact: %+v", got)
	}
}

// TestHub Set/Resync/Get/Manifests 语义与 nil 安全。
func TestHub(t *testing.T) {
	var nilHub *Hub
	nilHub.Resync(nil)            // no-op
	if nilHub.Get() != nil || nilHub.Manifests() != nil {
		t.Fatal("nil hub 应安全 no-op")
	}
	lin, manifests := fixture()
	h := NewHub()
	if got := h.Set(lin, manifests); got != lin || h.Get() != lin {
		t.Fatal("Set 应存图并返回")
	}
	// Resync 只换图，清单沿用。
	next := Build(nil, nil, nil)
	h.Resync(next)
	if h.Get() != next || h.Manifests() == nil {
		t.Fatal("Resync 应换图留清单")
	}
}
