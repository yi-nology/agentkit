// 本文件族是 LLM Provider 抽象与多后端支持（Provider 接口 + FallbackChain
// 模型降级链）。包级总览见 client.go 的唯一 package doc。
package llm

import (
	"github.com/cloudwego/eino/components/model"
)

// Provider LLM 提供者接口：抽象模型创建与配置。
type Provider interface {
	// Name 提供者名称（如 "openai", "anthropic", "ollama"）。
	Name() string
	// Model 返回 eino ChatModel 实例。
	Model() model.BaseChatModel
	// ModelName 返回模型标识（如 "gpt-4o", "deepseek-v4"）。
	ModelName() string
	// ContextTokens 返回模型上下文窗口大小（token 数）。
	ContextTokens() int
	// MaxOutputTokens 返回单次生成输出上限。
	MaxOutputTokens() int
	// CostPer1KTokens 返回每 1000 token 的成本（美元）。
	// 用于成本追踪，返回 0 表示不计费。
	CostPer1KTokens() (prompt, completion float64)
}

// FallbackChain 模型降级链：按优先级尝试多个 Provider。
type FallbackChain struct {
	Providers []Provider
}

// NewFallbackChain 创建降级链。
func NewFallbackChain(providers ...Provider) *FallbackChain {
	return &FallbackChain{Providers: providers}
}

// Primary 返回主 Provider（优先级最高）。
func (fc *FallbackChain) Primary() Provider {
	if len(fc.Providers) == 0 {
		return nil
	}
	return fc.Providers[0]
}

// Fallback 返回备选 Provider 列表（不含主 Provider）。
func (fc *FallbackChain) Fallback() []Provider {
	if len(fc.Providers) <= 1 {
		return nil
	}
	return fc.Providers[1:]
}
