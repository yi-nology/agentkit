// Package rag 知识检索服务。
// 提供两种后端：Local（本地 TF-IDF）和 MilvusStore（向量数据库）。
// 检索算法 = 词元重叠打分（ASCII 词 + CJK 二元组）或向量相似度，
// 零外部向量库依赖（Local）或 Milvus 向量数据库（MilvusStore）。
package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/yi-nology/agentkit/textutil"
)

// Chunk 检索片段。
type Chunk struct {
	ID       string
	Content  string
	Score    float64
	Metadata map[string]string
}

// Filter 检索过滤条件（如 repo、规范文档名）。
type Filter map[string]string

// KnowledgeService 知识检索服务接口。
// 模式一：预检索（确定性）—— workflow 阶段在拼 prompt 前取 topK 片段。
// 模式二：工具化（自主）—— 包成 search_knowledge(query) 挂进 ReAct agent。
type KnowledgeService interface {
	Retrieve(ctx context.Context, query string, topK int, filter Filter) ([]Chunk, error)
	AsTool() tool.BaseTool
}

// Noop 空实现：Retrieve 返回空、AsTool 返回 nil。
type Noop struct{}

// NewNoop 创建空实现。
func NewNoop() *Noop { return &Noop{} }

func (n *Noop) Retrieve(_ context.Context, _ string, _ int, _ Filter) ([]Chunk, error) {
	return nil, nil
}

func (n *Noop) AsTool() tool.BaseTool { return nil }

var _ KnowledgeService = (*Noop)(nil)

// buildAsTool 构建 search_knowledge eino 工具（Local 和 MilvusStore 共用）。
func buildAsTool(svc KnowledgeService) tool.BaseTool {
	t, err := utils.InferTool("search_knowledge",
		"检索团队知识库（编码规范/部署约定/历史评审结论/安全清单）。返回最相关的知识片段及出处。",
		func(ctx context.Context, in *searchIn) (*searchOut, error) {
			// 透传 eino 工具调用 ctx：上层取消/超时要能中断检索
			chunks, err := svc.Retrieve(ctx, in.Query, defaultTopK, nil)
			if err != nil {
				return &searchOut{Error: err.Error()}, nil
			}
			var b strings.Builder
			for _, c := range chunks {
				content := c.Content
				content = textutil.TruncNote(content, toolSnippetRunes, "截断")
				heading := c.Metadata["heading"]
				file := c.Metadata["file"]
				if heading != "" {
					fmt.Fprintf(&b, "【%s > %s】%s\n\n", file, heading, content)
				} else {
					fmt.Fprintf(&b, "【%s】%s\n\n", file, content)
				}
			}
			return &searchOut{Results: b.String()}, nil
		})
	if err != nil {
		panic(fmt.Sprintf("rag: 构建 search_knowledge 工具失败: %v", err)) // 静态 schema，构造失败即编程错误
	}
	return t
}

type searchIn struct {
	Query string `json:"query" jsonschema:"description=检索关键词或问题"`
}
type searchOut struct {
	Results string `json:"results,omitempty"`
	Error   string `json:"error,omitempty"`
}
