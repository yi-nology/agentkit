// Package lineage 装配血缘：expert → skill → MCP server 三类资产节点的
// 声明式依赖图，used_by/granted_by 反查字段的单一事实源（多个 list API 共享
// 一份反查计算，消灭双份漂移）。随 reload/Resync 重建；Diff 产出 reload 前后的
// 结构化影响清单（version/maturity 跃迁、工具面增减、引用边增减）；Focus 提供
// 焦点邻接子图（控制台血缘视图）。
//
// 本包只管图的机制（组装/diff/遍历/并发读）；「有效技能集如何确定」「清单如何
// 加载」是装配方的事——经 Expert/Skill 中性输入注入，不依赖任何具体装配实现。
package lineage

import (
	"sort"

	"github.com/yi-nology/agentkit/hotplug"
	"github.com/yi-nology/agentkit/pack"
	"github.com/yi-nology/agentkit/skill"
)

// Grant 专家对 MCP server 的工具授予（allow 空=全部工具）。
type Grant struct {
	Server string
	Allow  []string
}

// Expert 专家节点的装配边输入。Skills 须为有效技能集（挂摘覆盖 > 文件基座、
// optional 缺失已剔除——覆盖感知语义由装配方保证）。
type Expert struct {
	Slug       string
	Skills     []string
	Overridden bool // 是否存在页面/外部覆盖（版本约束未校验的提示位）
	Grants     []Grant
}

// Skill 技能节点的元数据输入（未引用的技能也要登记——须能作焦点查询）。
type Skill struct {
	Name        string
	Version     string
	Maturity    string
	Source      string
	RequiresMCP []skill.MCPDep
	Provides    []string
	Deprecated  *skill.Deprecated
}

// SkillFromMeta 把 skill.LibMeta 投影为血缘输入（字段映射的唯一事实源——
// 装配方不再手工逐字段对拷，LibMeta 演进时单点跟进）。
func SkillFromMeta(m skill.LibMeta) Skill {
	return Skill{
		Name:        m.Name,
		Version:     m.Version,
		Maturity:    m.Maturity,
		Source:      m.Source,
		RequiresMCP: m.RequiresMCP,
		Provides:    m.Provides,
		Deprecated:  m.Deprecated,
	}
}

// ExpertEdges 专家的装配边（slug → 技能/工具授予）。
type ExpertEdges struct {
	Skills     []string `json:"skills"`     // 有效技能（覆盖感知）
	Overridden bool     `json:"overridden"` // 是否存在覆盖（版本约束未校验）
	Grants     []string `json:"grants"`     // 授予的 MCP server
}

// SkillEdges 技能的血缘边（name → 被谁引用 / 依赖哪些 MCP）。
type SkillEdges struct {
	UsedBy      []string          `json:"used_by"`                // 引用该技能的专家（覆盖感知）
	RequiresMCP []skill.MCPDep    `json:"requires_mcp,omitempty"` // 声明的 MCP 依赖
	Provides    []string          `json:"provides,omitempty"`     // 能力标签
	Version     string            `json:"version,omitempty"`
	Maturity    string            `json:"maturity,omitempty"`
	Source      string            `json:"source,omitempty"`
	Deprecated  *skill.Deprecated `json:"deprecated,omitempty"`
}

// MCPEdges MCP server 的血缘边（server → 谁授予 / 谁声明依赖 / 契约清单）。
type MCPEdges struct {
	GrantedBy    []string           `json:"granted_by"`
	UsedBySkills []string           `json:"used_by_skills"`
	Tools        []string           `json:"tools,omitempty"`     // 显式授予工具并集（allow 明细）
	Unlimited    bool               `json:"unlimited,omitempty"` // 任一授予 allow 空=全部工具（无法枚举对账）
	Manifest     *pack.ToolManifest `json:"manifest,omitempty"`  // 契约事实源（无清单=nil）
}

// Lineage 全量装配图（可序列化进 API 响应）。
type Lineage struct {
	Experts map[string]ExpertEdges `json:"experts"`
	Skills  map[string]SkillEdges  `json:"skills"`
	MCPS    map[string]MCPEdges    `json:"mcps"`
}

// Build 组装装配图。manifests 可为 nil（测试/降级路径）。
// used_by 以装配方给定的有效技能集为准（覆盖感知）。
func Build(experts []Expert, skills []Skill, manifests map[string]*pack.ToolManifest) *Lineage {
	lin := &Lineage{
		Experts: map[string]ExpertEdges{},
		Skills:  map[string]SkillEdges{},
		MCPS:    map[string]MCPEdges{},
	}
	// 技能节点：全库登记（未被引用的技能也要能作焦点查询）。
	for _, s := range skills {
		lin.Skills[s.Name] = SkillEdges{
			RequiresMCP: s.RequiresMCP, Provides: s.Provides,
			Version: s.Version, Maturity: s.Maturity,
			Source: s.Source, Deprecated: s.Deprecated,
		}
	}
	for _, e := range experts {
		grants := make([]string, 0, len(e.Grants))
		for _, g := range e.Grants {
			grants = append(grants, g.Server)
			edge := lin.MCPS[g.Server]
			edge.GrantedBy = append(edge.GrantedBy, e.Slug)
			if len(g.Allow) == 0 {
				edge.Unlimited = true // allow 空=该 server 全部工具
			}
			seen := map[string]bool{}
			for _, t := range edge.Tools {
				seen[t] = true
			}
			for _, t := range g.Allow {
				if !seen[t] {
					edge.Tools = append(edge.Tools, t)
				}
			}
			lin.MCPS[g.Server] = edge
		}
		lin.Experts[e.Slug] = ExpertEdges{Skills: e.Skills, Overridden: e.Overridden, Grants: grants}
		for _, n := range e.Skills {
			edge := lin.Skills[n]
			edge.UsedBy = append(edge.UsedBy, e.Slug)
			lin.Skills[n] = edge
		}
	}
	// 技能 → MCP 依赖边。
	for n, edge := range lin.Skills {
		for _, dep := range edge.RequiresMCP {
			m := lin.MCPS[dep.Server]
			m.UsedBySkills = append(m.UsedBySkills, n)
			lin.MCPS[dep.Server] = m
		}
	}
	// 契约清单挂上 MCP 节点。
	for server, m := range manifests {
		edge := lin.MCPS[server]
		edge.Manifest = m
		lin.MCPS[server] = edge
	}
	// 稳定排序（API 展示与 diff 的确定性契约）。
	for slug, e := range lin.Experts {
		sort.Strings(e.Skills)
		sort.Strings(e.Grants)
		lin.Experts[slug] = e
	}
	for n, e := range lin.Skills {
		sort.Strings(e.UsedBy)
		lin.Skills[n] = e
	}
	for s, e := range lin.MCPS {
		sort.Strings(e.GrantedBy)
		sort.Strings(e.UsedBySkills)
		sort.Strings(e.Tools)
		lin.MCPS[s] = e
	}
	return lin
}

// Impact reload 前后的结构化影响项（进 reload 响应；warnings 仍保留人读摘要）。
type Impact struct {
	Type   string   `json:"type"` // skill_version_changed|skill_maturity_changed|deprecation|tools_changed|refs_changed
	Skill  string   `json:"skill,omitempty"`
	Expert string   `json:"expert,omitempty"`
	Server string   `json:"server,omitempty"`
	From   string   `json:"from,omitempty"`
	To     string   `json:"to,omitempty"`
	UsedBy []string `json:"used_by,omitempty"`
	// ToolsAdded/ToolsRemoved 仅 tools_changed（MCP 工具面）填充。
	ToolsAdded   []string `json:"tools_added,omitempty"`
	ToolsRemoved []string `json:"tools_removed,omitempty"`
	// SkillsAdded/SkillsRemoved 仅 refs_changed（技能引用边）填充。
	//（历史上 refs_changed 曾把技能名错装进 tools_* 字段——v0.10.14 起按
	// 仓库"无兼容层"纪律删除旧填充，消费方读本字段。）
	SkillsAdded   []string `json:"skills_added,omitempty"`
	SkillsRemoved []string `json:"skills_removed,omitempty"`
}

// Diff 对比前后两份装配图，产出结构化影响清单（仅 diff 可观察字段，无变化不产出）。
// prev 为 nil（重启首帧）= 无基线 → 不产出 impact：首帧把全部被引用资产报成
// 「(新增)」是噪音，会淹没真正的变更。
func Diff(prev, cur *Lineage) []Impact {
	if cur == nil || prev == nil {
		return nil
	}
	var out []Impact
	empty := func(s []string) bool { return len(s) == 0 }
	// 技能元数据跃迁（version/maturity/弃用）。
	for name, e := range cur.Skills {
		p, had := prev.Skills[name]
		usedBy := e.UsedBy
		if !had {
			if !empty(usedBy) {
				out = append(out, Impact{Type: "skill_version_changed", Skill: name, From: "(新增)", To: e.Version, UsedBy: usedBy})
			}
			continue
		}
		if p.Version != e.Version {
			out = append(out, Impact{Type: "skill_version_changed", Skill: name, From: p.Version, To: e.Version, UsedBy: usedBy})
		}
		if p.Maturity != e.Maturity {
			out = append(out, Impact{Type: "skill_maturity_changed", Skill: name, From: p.Maturity, To: e.Maturity, UsedBy: usedBy})
		}
		if p.Maturity != skill.MaturityDeprecated && e.Maturity == skill.MaturityDeprecated {
			to := ""
			if e.Deprecated != nil {
				to = e.Deprecated.RemoveAfter
			}
			out = append(out, Impact{Type: "deprecation", Skill: name, To: to, UsedBy: usedBy})
		}
	}
	// 工具面增减 = manifest 声明 ∪ 显式授予（edge.Tools；仅 diff manifest 会让
	// 授予变更静默）。allow 空=全部工具无法枚举对账 → 该侧跳过不产出。
	toolSet := func(lin *Lineage, server string) map[string]bool {
		set := map[string]bool{}
		if m := lin.MCPS[server].Manifest; m != nil {
			for _, t := range m.Tools {
				set[t.Name] = true
			}
		}
		for _, t := range lin.MCPS[server].Tools {
			set[t] = true
		}
		return set
	}
	servers := map[string]bool{}
	for s := range prev.MCPS {
		servers[s] = true
	}
	for s := range cur.MCPS {
		servers[s] = true
	}
	for server := range servers {
		if prev.MCPS[server].Unlimited || cur.MCPS[server].Unlimited {
			continue // 任一侧为「全部工具」授予：枚举不可知，不产出工具面 diff
		}
		before, after := toolSet(prev, server), toolSet(cur, server)
		var added, removed []string
		for t := range after {
			if !before[t] {
				added = append(added, t)
			}
		}
		for t := range before {
			if !after[t] {
				removed = append(removed, t)
			}
		}
		if len(added) > 0 || len(removed) > 0 {
			sort.Strings(added)
			sort.Strings(removed)
			out = append(out, Impact{Type: "tools_changed", Server: server,
				ToolsAdded: added, ToolsRemoved: removed,
				UsedBy: cur.MCPS[server].GrantedBy})
		}
	}
	// 引用边增减（文件基座/覆盖变化；覆盖感知的边变化如实上报）。
	for slug, e := range cur.Experts {
		p, had := prev.Experts[slug]
		if !had {
			continue // 新专家属新增资产，不算引用变化
		}
		prevSet := map[string]bool{}
		for _, n := range p.Skills {
			prevSet[n] = true
		}
		curSet := map[string]bool{}
		for _, n := range e.Skills {
			curSet[n] = true
		}
		var added, removed []string
		for n := range curSet {
			if !prevSet[n] {
				added = append(added, n)
			}
		}
		for n := range prevSet {
			if !curSet[n] {
				removed = append(removed, n)
			}
		}
		if len(added) > 0 || len(removed) > 0 {
			sort.Strings(added)
			sort.Strings(removed)
			out = append(out, Impact{Type: "refs_changed", Expert: slug,
				SkillsAdded: added, SkillsRemoved: removed})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Skill+out[i].Expert+out[i].Server < out[j].Skill+out[j].Expert+out[j].Server
	})
	return out
}

// LineageNode / LineageEdge / LineageFocus 焦点查询结果（邻接子图）。
type LineageNode struct {
	Kind string `json:"kind"` // expert|skill|server
	Name string `json:"name"`
}
type LineageEdge struct {
	From     LineageNode `json:"from"`
	To       LineageNode `json:"to"`
	Relation string      `json:"relation"` // skill|grants|requires_mcp
}
type LineageFocus struct {
	Focus LineageNode   `json:"focus"`
	Nodes []LineageNode `json:"nodes"`
	Edges []LineageEdge `json:"edges"`
}

// Focus 焦点邻接子图（无向遍历 depth 跳；depth 上限 2）。未知焦点返回 false。
func (l *Lineage) Focus(focus string, depth int) (*LineageFocus, bool) {
	if l == nil {
		return nil, false
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 2 {
		depth = 2
	}
	kindOf := func(name string) (string, bool) {
		if _, ok := l.Experts[name]; ok {
			return "expert", true
		}
		if _, ok := l.Skills[name]; ok {
			return "skill", true
		}
		if _, ok := l.MCPS[name]; ok {
			return "server", true
		}
		return "", false
	}
	kind, ok := kindOf(focus)
	if !ok {
		return nil, false
	}
	res := &LineageFocus{Focus: LineageNode{Kind: kind, Name: focus}}
	seen := map[LineageNode]bool{res.Focus: true}
	res.Nodes = append(res.Nodes, res.Focus)
	frontier := []LineageNode{res.Focus}
	neighbors := func(n LineageNode) []struct {
		to       LineageNode
		relation string
	} {
		var out []struct {
			to       LineageNode
			relation string
		}
		switch n.Kind {
		case "expert":
			for _, s := range l.Experts[n.Name].Skills {
				out = append(out, struct {
					to       LineageNode
					relation string
				}{LineageNode{"skill", s}, "skill"})
			}
			for _, s := range l.Experts[n.Name].Grants {
				out = append(out, struct {
					to       LineageNode
					relation string
				}{LineageNode{"server", s}, "grants"})
			}
		case "skill":
			for _, u := range l.Skills[n.Name].UsedBy {
				out = append(out, struct {
					to       LineageNode
					relation string
				}{LineageNode{"expert", u}, "skill"})
			}
			for _, dep := range l.Skills[n.Name].RequiresMCP {
				out = append(out, struct {
					to       LineageNode
					relation string
				}{LineageNode{"server", dep.Server}, "requires_mcp"})
			}
		case "server":
			for _, g := range l.MCPS[n.Name].GrantedBy {
				out = append(out, struct {
					to       LineageNode
					relation string
				}{LineageNode{"expert", g}, "grants"})
			}
			for _, s := range l.MCPS[n.Name].UsedBySkills {
				out = append(out, struct {
					to       LineageNode
					relation string
				}{LineageNode{"skill", s}, "requires_mcp"})
			}
		}
		return out
	}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []LineageNode
		for _, n := range frontier {
			for _, nb := range neighbors(n) {
				res.Edges = append(res.Edges, LineageEdge{From: n, To: nb.to, Relation: nb.relation})
				if !seen[nb.to] {
					seen[nb.to] = true
					res.Nodes = append(res.Nodes, nb.to)
					next = append(next, nb.to)
				}
			}
		}
		frontier = next
	}
	sort.Slice(res.Nodes, func(i, j int) bool {
		if res.Nodes[i].Kind != res.Nodes[j].Kind {
			return res.Nodes[i].Kind < res.Nodes[j].Kind
		}
		return res.Nodes[i].Name < res.Nodes[j].Name
	})
	sort.Slice(res.Edges, func(i, j int) bool {
		a, b := res.Edges[i], res.Edges[j]
		if a.From.Kind != b.From.Kind {
			return a.From.Kind < b.From.Kind
		}
		if a.From.Name != b.From.Name {
			return a.From.Name < b.From.Name
		}
		return a.To.Name < b.To.Name
	})
	return res, true
}

// Hub 血缘中枢：reload/Resync 时重建（Set），list API 与 lineage 端点共享读。
// 快照持有点复用 hotplug.Holder（读无锁整体原子换——与 Plugboard 同一并发纪律）。
// 零值可用；nil *Hub 的全部方法安全 no-op（测试/未接线路径）。
type Hub struct {
	st hotplug.Holder[hubState]
}

// hubState 一次装配快照：血缘图 + 契约清单（Set 整体换，消双字段中间态）。
type hubState struct {
	lin       *Lineage
	manifests map[string]*pack.ToolManifest
}

// NewHub 构造。
func NewHub() *Hub { return &Hub{} }

// Set 更新血缘与契约清单（返回新图供 diff 前挂载）。
func (h *Hub) Set(lin *Lineage, manifests map[string]*pack.ToolManifest) *Lineage {
	if h == nil {
		return lin
	}
	h.st.Store(&hubState{lin: lin, manifests: manifests})
	return lin
}

// Resync 仅替换血缘图（清单沿用上次 Set 的——挂摘/启停类变化不改契约面）。
func (h *Hub) Resync(lin *Lineage) {
	if h == nil {
		return
	}
	cur := h.st.Load()
	m := map[string]*pack.ToolManifest(nil)
	if cur != nil {
		m = cur.manifests
	}
	h.Set(lin, m)
}

// Get 当前血缘（未 Set 过为 nil，调用方回退现场构建）。
func (h *Hub) Get() *Lineage {
	if h == nil {
		return nil
	}
	if st := h.st.Load(); st != nil {
		return st.lin
	}
	return nil
}

// Manifests 最近一次 Set 的契约清单（未 Set 过为 nil）。
func (h *Hub) Manifests() map[string]*pack.ToolManifest {
	if h == nil {
		return nil
	}
	if st := h.st.Load(); st != nil {
		return st.manifests
	}
	return nil
}
