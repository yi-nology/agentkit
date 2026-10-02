package skill

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// ---------- 决策使用：use_skill 工具（渐进披露） ----------

// useSkillIn use_skill 工具入参。
type useSkillIn struct {
	Name string `json:"name" jsonschema:"description=要加载的 skill 名（须在可用清单内）"`
}

// useSkillOut use_skill 工具出参。
type useSkillOut struct {
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
func AsSkillTool(p Provider, allowed []string) (tool.BaseTool, error) {
	if _, ok := p.(Lister); !ok {
		return nil, fmt.Errorf("skill: provider 不支持发现（未实现 Lister），无法做决策使用")
	}
	set := map[string]bool{}
	for _, a := range allowed {
		set[a] = true
	}

	t, err := utils.InferTool("use_skill",
		"加载指定 skill 的完整方法论内容（评分标准/检查清单/规范摘要）。"+
			"仅当任务与可用清单中某条 skill 的描述相关时调用。",
		func(_ context.Context, in *useSkillIn) (*useSkillOut, error) {
			if len(set) > 0 && !set[in.Name] {
				return &useSkillOut{Error: fmt.Sprintf("skill %q 不在允许清单内", in.Name)}, nil
			}
			s, err := p.Resolve(context.Background(), Ref{Name: in.Name})
			if err != nil {
				return &useSkillOut{Error: err.Error()}, nil
			}
			return &useSkillOut{Content: s.Content, Description: s.Description}, nil
		})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ListPrompt 渲染可用 skill 清单（注入提示词的决策依据，不含正文）。
// 无 skill 返回空串；有则渲染为 markdown 列表。
func ListPrompt(metas []Meta) string {
	if len(metas) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("可用 skill 清单（如任务相关，用 use_skill 工具加载全文）：\n")
	for _, m := range metas {
		desc := m.Description
		if desc == "" {
			desc = "（无描述）"
		}
		fmt.Fprintf(&b, "- **%s**：%s\n", m.Name, desc)
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
