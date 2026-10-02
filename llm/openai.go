package llm

import (
	"context"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// OpenAIProvider OpenAI 兼容 LLM 提供者。
// 支持 OpenAI / 百炼 / DeepSeek / 任何 OpenAI 兼容端点。
type OpenAIProvider struct {
	modelName       string
	contextTokens   int
	maxOutputTokens int
	timeout         time.Duration
	costPer1K       [2]float64 // [prompt, completion]

	chatModel model.BaseChatModel
}

// OpenAIProviderConfig OpenAI 提供者配置。
type OpenAIProviderConfig struct {
	BaseURL             string
	APIKey              string
	Model               string
	ContextTokens       int           // 模型上下文窗口（默认 128k）
	MaxOutputTokens     int           // 单次输出上限（默认从窗口派生）
	Timeout             time.Duration // 请求超时（默认 60s）
	CostPer1KPrompt     float64       // 每 1000 prompt token 成本（美元）
	CostPer1KCompletion float64       // 每 1000 completion token 成本（美元）
}

// NewOpenAIProvider 创建 OpenAI 兼容提供者。
func NewOpenAIProvider(ctx context.Context, cfg OpenAIProviderConfig) (*OpenAIProvider, error) {
	if cfg.ContextTokens <= 0 {
		cfg.ContextTokens = 128_000
	}
	// 0 = 从窗口派生缺省；<0 = 不下发 max_tokens 参数（部分推理模型要求
	// max_completion_tokens 或拒绝该参数）
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = cfg.ContextTokens * 5 / 100
		if cfg.MaxOutputTokens < 4096 {
			cfg.MaxOutputTokens = 4096
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}

	var maxTokens *int
	if cfg.MaxOutputTokens > 0 {
		t := cfg.MaxOutputTokens
		maxTokens = &t
	}
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:   cfg.BaseURL,
		APIKey:    cfg.APIKey,
		Model:     cfg.Model,
		MaxTokens: maxTokens,
		Timeout:   cfg.Timeout,
	})
	if err != nil {
		return nil, err
	}

	return &OpenAIProvider{
		modelName:       cfg.Model,
		contextTokens:   cfg.ContextTokens,
		maxOutputTokens: cfg.MaxOutputTokens,
		timeout:         cfg.Timeout,
		costPer1K:       [2]float64{cfg.CostPer1KPrompt, cfg.CostPer1KCompletion},
		chatModel:       chatModel,
	}, nil
}

func (p *OpenAIProvider) Name() string               { return "openai" }
func (p *OpenAIProvider) Model() model.BaseChatModel { return p.chatModel }
func (p *OpenAIProvider) ModelName() string          { return p.modelName }
func (p *OpenAIProvider) ContextTokens() int         { return p.contextTokens }
func (p *OpenAIProvider) MaxOutputTokens() int       { return p.maxOutputTokens }
func (p *OpenAIProvider) CostPer1KTokens() (float64, float64) {
	return p.costPer1K[0], p.costPer1K[1]
}

// AttemptTimeout 单次尝试超时（Resilient 降级链按 Provider 独立限时）。
func (p *OpenAIProvider) AttemptTimeout() time.Duration { return p.timeout }
