package skill

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/yi-nology/agentkit/fence"
)

// ---------- 决策使用：use_skill 工具（渐进披露） ----------

// 预算缺省（两级渐进披露的预算面，批次五十三自 bianque 沉淀）：
// 清单渲染超预算确定性降级；use_skill 全文超上限截断并声明。
// 调用方显式传值覆盖（0=回落缺省，负=不限的语义在 RenderList/Cap 侧）。
const (
	DefaultListBudget = 20_000  // RenderList 清单预算（字节）
	DefaultContentCap = 100_000 // use_skill 全文上限（字节）
)

// RenderList 清单渲染 + 确定性预算降级（对标 ZCode skills.ts 的预算-降级形态：
// 降级是确定性行为而非异常——超预算的形态可测试、可预期）。
//
// budget≤0 = 不限（等价 ListPrompt）；>0 = 预算字节。降级两级：
// 全额（名称+描述，ListPrompt 同款）→ 超预算降级为纯名单（name only）→
// 仍超则按预算截断 + 末尾注明。多字节字符按完整名回退，不截半个字。
func RenderList(metas []Meta, budget int) string {
	full := ListPrompt(metas)
	if budget <= 0 || len(full) <= budget {
		return full
	}
	names := make([]string, 0, len(metas))
	for _, m := range metas {
		names = append(names, m.Name)
	}
	degraded := "可用 skill 清单（清单超预算，已降级为纯名单；按名用 use_skill 加载，描述见技能库）：\n" +
		strings.Join(names, ", ")
	if len(degraded) > budget {
		joined := strings.Join(names, ", ")
		truncated := joined
		if len(truncated) > budget {
			truncated = truncated[:budget]
			// 按字节截断可能切断多字节字符——回退到最后一个完整分隔符。
			if i := strings.LastIndex(truncated, ", "); i > 0 {
				truncated = truncated[:i]
			}
		}
		degraded = "可用 skill 清单（清单过长已截断）：\n" + truncated + " …（其余技能名见技能库）"
	}
	return degraded
}

// useSkillIn use_skill 工具入参。
type useSkillIn struct {
	Name string `json:"name" jsonschema:"description=要加载的 skill 名（须在可用清单内）"`
}

// useSkillOut use_skill 工具出参。
// 自证字段（Name/Requested/Version/Checksum）供观测守卫校验「声明加载 A、实际返回 B」——
// 身份锚点是结果内容自报的规范名+校验和，不依赖事件流相邻顺序。
type useSkillOut struct {
	Name        string `json:"name,omitempty"`      // 解析后规范引用名（allowed 校验与守卫比对的基准）
	Requested   string `json:"requested,omitempty"` // 模型原始入参（别名命中时与 Name 不同）
	Version     string `json:"version,omitempty"`   // frontmatter version（Provider 未提供时为空）
	Checksum    string `json:"checksum,omitempty"`  // 内容校验和（sha256 hex，Provider 口径）
	Content     string `json:"content,omitempty"`
	Description string `json:"description,omitempty"`
	Error       string `json:"error,omitempty"`
}

// AsSkillTool 把"决策使用"包成 use_skill eino 工具：提示词只注入
// 可用 skill 清单（名称+描述，经 ListSkills），agent 按任务相关性自主
// 决定加载哪个全文——skill 多/大时不撑爆系统提示词。
//
// allowed 限定可加载的 skill 名（空 = 目录全量）；与静态注入互斥使用。
// 与 rag.KnowledgeService.AsTool 同范式。
//
// opts 可选：WithContentCap 限定单次返回全文上限（超限截断 + 尾部声明——
// LLM 可感知的诚实形态，不静默吞内容；缺省 DefaultContentCap，≤0 = 不限）。
func AsSkillTool(p Provider, allowed []string, opts ...ToolOption) (tool.BaseTool, error) {
	if _, ok := p.(Lister); !ok {
		return nil, fmt.Errorf("skill: provider 不支持发现（未实现 Lister），无法做决策使用")
	}
	set := map[string]bool{}
	for _, a := range allowed {
		set[a] = true
	}
	cfg := skillToolCfg{contentCap: DefaultContentCap}
	for _, o := range opts {
		o(&cfg)
	}

	t, err := utils.InferTool("use_skill",
		"加载指定 skill 的完整方法论内容（评分标准/检查清单/规范摘要）。"+
			"仅当任务与可用清单中某条 skill 的描述相关时调用。",
		func(ctx context.Context, in *useSkillIn) (*useSkillOut, error) {
			// 别名归一化：模型可能用清单里的展示名（frontmatter name）回填，
			// 加载与 allowed 校验一律以规范引用名为准
			name := in.Name
			if ar, ok := p.(AliasResolver); ok {
				if n, hit := ar.CanonicalName(ctx, in.Name); hit {
					name = n
				}
			}
			if len(set) > 0 && !set[name] {
				return &useSkillOut{Error: fmt.Sprintf("skill %q 不在允许清单内", name)}, nil
			}
			s, err := p.Resolve(ctx, Ref{Name: name})
			if err != nil {
				return &useSkillOut{Error: err.Error()}, nil
			}
			out := &useSkillOut{Name: name, Requested: in.Name, Version: s.Version,
				Checksum: s.Checksum, Content: s.Content, Description: s.Description}
			capContent(out, cfg.contentCap)
			return out, nil
		})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// skillToolCfg use_skill 工具装配配置（ToolOption 注入）。
type skillToolCfg struct {
	contentCap int // 全文上限字节；≤0 = 不限
}

// ToolOption AsSkillTool 装配选项。
type ToolOption func(*skillToolCfg)

// WithContentCap 限定 use_skill 单次返回全文上限（字节）。超限截断并在尾部
// 追加声明（模型可感知，不静默吞内容）；≤0 = 不限。缺省 DefaultContentCap。
func WithContentCap(n int) ToolOption {
	return func(c *skillToolCfg) { c.contentCap = n }
}

// capContent 就地施加全文上限（截断 + 尾部声明；≤0 = 不限）。
func capContent(out *useSkillOut, capBytes int) {
	if capBytes <= 0 || len(out.Content) <= capBytes {
		return
	}
	out.Content = out.Content[:capBytes] +
		"\n\n[系统提示] 技能全文超过上限已截断；以上为前缀内容，请基于已有信息继续，必要时申请平台侧拆分技能。"
}

// ListPrompt 渲染可用 skill 清单（注入提示词的决策依据，不含正文）。
// Description 经 fence.EscapeUntrusted 中和（markdown 结构/HTML 注释边界）——
// Name/Title/Description 来自仓库内 markdown，属半可信内容，恶意描述可夹带
// 提示词注入指令；消毒在出口统一兜底，不依赖调用方自觉（目录写权限仍应收敛）。
// 无 skill 返回空串；有则渲染为 markdown 列表。
// 加载名（Name）作为加粗主词——模型回填 use_skill 的名称必须与它一致；
// 展示名（Title，frontmatter name）仅作括注。
func ListPrompt(metas []Meta) string {
	if len(metas) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("可用 skill 清单（如任务相关，用 use_skill 工具加载全文）：\n")
	for _, m := range metas {
		desc := fence.EscapeUntrusted(m.Description)
		if desc == "" {
			desc = "（无描述）"
		}
		if m.Title != "" && m.Title != m.Name {
			fmt.Fprintf(&b, "- **%s**（%s）：%s\n", m.Name, m.Title, desc)
		} else {
			fmt.Fprintf(&b, "- **%s**：%s\n", m.Name, desc)
		}
	}
	return b.String()
}

// ---------- 变量渲染（对齐 eino FString） ----------

// Format 按 eino FString（pyfmt，{var} 占位）渲染 skill 内容。
// 底层复用 schema.Message 的 MessagesTemplate 实现——与 eino 提示词模板同语义：
//
//	content := "以 {language} 审查，输出不超过 {max_items} 条。"
//	out, err := skill.Format(ctx, content, map[string]any{"language": "Go", "max_items": 5})
func Format(ctx context.Context, content string, vars map[string]any) (string, error) {
	tpl := &schema.Message{Content: content}
	msgs, err := tpl.Format(ctx, vars, schema.FString)
	if err != nil {
		return "", fmt.Errorf("skill: 模板渲染失败: %w", err)
	}
	if len(msgs) != 1 {
		return "", fmt.Errorf("skill: 模板渲染出 %d 条消息，应为 1", len(msgs))
	}
	return msgs[0].Content, nil
}
