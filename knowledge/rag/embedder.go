// Package rag Embedding 接口定义与 OpenAI 兼容实现。
package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yi-nology/agentkit/httpx"
)

// Embedder 文本向量化接口。
type Embedder interface {
	// Embed 将文本切片转为向量切片，返回的向量维度应一致。
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dim 返回向量维度。
	Dim() int
}

// OpenAIEmbedder OpenAI 兼容 Embedding API 适配器。
// 支持 OpenAI / 百炼 / 任何兼容 /v1/embeddings 端点。
type OpenAIEmbedder struct {
	BaseURL   string // 如 https://api.openai.com/v1 或 https://dashscope.aliyuncs.com/compatible-mode/v1
	APIKey    string
	Model     string // 如 text-embedding-3-small / text-embedding-v3
	dimension int    // 向量维度（需与模型输出一致）

	client *http.Client
}

// NewOpenAIEmbedder 创建 OpenAI 兼容 Embedding 客户端。
func NewOpenAIEmbedder(baseURL, apiKey, model string, dim int) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		BaseURL:   strings.TrimSuffix(baseURL, "/"),
		APIKey:    apiKey,
		Model:     model,
		dimension: dim,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Dim 返回向量维度。
func (e *OpenAIEmbedder) Dim() int { return e.dimension }

type embedRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed 将文本切片转为向量切片。
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// API 单次上限：每批最多 16 条（保守值，各提供商不同）
	const batchSize = 16
	var allEmbeddings [][]float32

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		embeddings, err := e.embedBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("rag: embedding batch %d-%d: %w", i, end, err)
		}
		allEmbeddings = append(allEmbeddings, embeddings...)
	}

	return allEmbeddings, nil
}

func (e *OpenAIEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(embedRequest{Input: texts, Model: e.Model})

	var er embedResponse
	err := httpx.DoJSON(ctx, e.client, httpx.Request{
		Method: http.MethodPost,
		URL:    e.BaseURL + "/embeddings",
		Body:   body,
		Header: func(h http.Header) {
			h.Set("Content-Type", "application/json")
			h.Set("Authorization", "Bearer "+e.APIKey)
		},
	}, &er)
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}

	// 按 index 排序（API 保证返回顺序，但防御性排序）
	embeddings := make([][]float32, len(texts))
	for _, d := range er.Data {
		// Index 来自远端 JSON，上下界都不可信（负数下标会 panic）；
		// 被丢弃的槽位由下方维度校验兜底报错
		if d.Index >= 0 && d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}

	// 验证维度
	for i, emb := range embeddings {
		if len(emb) != e.dimension {
			return nil, fmt.Errorf("rag: embedding[%d] 维度 %d != 期望 %d", i, len(emb), e.dimension)
		}
	}

	return embeddings, nil
}

func (e *OpenAIEmbedder) String() string {
	return fmt.Sprintf("OpenAIEmbedder(%s, %s, dim=%d)", e.BaseURL, e.Model, e.dimension)
}
