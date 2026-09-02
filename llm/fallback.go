package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/time/rate"
)

// FallbackClient 支持模型降级链的 LLM 客户端。
// 主模型失败时自动切换到备选模型（确定性失败如 401/上下文超限不触发降级）。
type FallbackClient struct {
	chain    *FallbackChain
	budget   TokenAccountant
	limiter  *rate.Limiter
	onUsage  func(model, stage string, prompt, completion int)
	tracker  *CostTracker

	MaxRetries int // 每个模型的最大重试次数（默认 3）
}

// NewFallbackClient 创建降级客户端。
func NewFallbackClient(chain *FallbackChain, budget TokenAccountant) *FallbackClient {
	return &FallbackClient{
		chain:      chain,
		budget:     budget,
		MaxRetries: 3,
	}
}

// WithLimiter 设置限速器。
func (fc *FallbackClient) WithLimiter(l *rate.Limiter) *FallbackClient {
	fc.limiter = l
	return fc
}

// WithUsageCallback 设置 token 使用回调。
func (fc *FallbackClient) WithUsageCallback(fn func(model, stage string, prompt, completion int)) *FallbackClient {
	fc.onUsage = fn
	return fc
}

// WithCostTracker 设置成本追踪器。
func (fc *FallbackClient) WithCostTracker(ct *CostTracker) *FallbackClient {
	fc.tracker = ct
	return fc
}

// Generate 带降级的生成：主模型重试失败后自动切换备选模型。
func (fc *FallbackClient) Generate(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, error) {
	providers := fc.chain.Providers
	if len(providers) == 0 {
		return nil, fmt.Errorf("llm: 无可用 provider")
	}

	var lastErr error
	for i, p := range providers {
		client := fc.buildClient(p)
		out, err := client.Generate(ctx, stage, msgs)
		if err == nil {
			return out, nil
		}
		lastErr = err

		// 确定性失败（鉴权/上下文超限）不触发降级，直接上抛
		if !ClassifyLLMError(err).Retryable {
			return nil, err
		}

		// 还有备选模型时，记录降级日志后继续
		if i < len(providers)-1 {
			// 降级到下一个模型
			continue
		}
	}

	return nil, fmt.Errorf("llm: 所有 provider 均失败，最后错误: %w", lastErr)
}

// GenerateJSON 带降级的 JSON 生成。
func (fc *FallbackClient) GenerateJSON(ctx context.Context, stage string, msgs []*schema.Message, out any) error {
	providers := fc.chain.Providers
	if len(providers) == 0 {
		return fmt.Errorf("llm: 无可用 provider")
	}

	var lastErr error
	for i, p := range providers {
		client := fc.buildClient(p)
		err := client.GenerateJSON(ctx, stage, msgs, out)
		if err == nil {
			return nil
		}
		lastErr = err

		if !ClassifyLLMError(err).Retryable {
			return err
		}

		if i < len(providers)-1 {
			continue
		}
	}

	return fmt.Errorf("llm: 所有 provider 均失败，最后错误: %w", lastErr)
}

// PrimaryModel 返回主模型的 BaseChatModel（供 eino agent 使用）。
func (fc *FallbackClient) PrimaryModel() model.BaseChatModel {
	if p := fc.chain.Primary(); p != nil {
		return p.Model()
	}
	return nil
}

// PrimaryProvider 返回主 Provider。
func (fc *FallbackClient) PrimaryProvider() Provider {
	return fc.chain.Primary()
}

func (fc *FallbackClient) buildClient(p Provider) *Client {
	c := NewClient(p.Model(), p.ModelName(), fc.budget)
	c.MaxRetries = fc.MaxRetries
	c.ContextTokens = p.ContextTokens()
	c.MaxOutputTokens = p.MaxOutputTokens()
	c.Limiter = fc.limiter
	costPer1K := [2]float64{}
	costPer1K[0], costPer1K[1] = p.CostPer1KTokens()
	c.OnUsage = func(stage string, prompt, completion int) {
		if fc.onUsage != nil {
			fc.onUsage(p.ModelName(), stage, prompt, completion)
		}
		if fc.tracker != nil {
			fc.tracker.Record(p.ModelName(), stage, prompt, completion, costPer1K)
		}
	}
	return c
}

// ProviderNames 返回所有 provider 名称（用于日志/报告）。
func (fc *FallbackClient) ProviderNames() []string {
	names := make([]string, len(fc.chain.Providers))
	for i, p := range fc.chain.Providers {
		names[i] = fmt.Sprintf("%s(%s)", p.Name(), p.ModelName())
	}
	return names
}

// String 返回降级链描述。
func (fc *FallbackClient) String() string {
	return strings.Join(fc.ProviderNames(), " → ")
}
