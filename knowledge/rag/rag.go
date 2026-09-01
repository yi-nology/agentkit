// Package rag 知识检索服务（本地 RAG 实现）。
// 检索源 = 目录下的 markdown 文件。检索算法 = 词元重叠打分（ASCII 词 + CJK 二元组），
// 零外部向量库依赖。向量库/ES 等后端按接口换实现即可。
package rag

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
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
