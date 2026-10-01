package llm

import (
	"context"
	"net/http"
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
	BaseURL         string
	APIKey          string
	Model           string
	ContextTokens   int           // 模型上下文窗口（默认 128k）
	MaxOutputTokens int           // 单次输出上限（默认从窗口派生）
	Timeout         time.Duration // 请求超时（默认 60s）
	// CaptureRetryAfter 捕获 429 响应的 Retry-After 头（批次四十五，对标 ZCode
	// runner-retry 的服务端退避建议）：经 ctx sink 透出给重试环的退避取舍。
	// 需要 http.Client 注入（Timeout 语义由注入的 Client 自带保留）。
	CaptureRetryAfter   bool
	CostPer1KPrompt     float64 // 每 1000 prompt token 成本（美元）
	CostPer1KCompletion float64 // 每 1000 completion token 成本（美元）
}

// NewOpenAIProvider 创建 OpenAI 兼容提供者。
func NewOpenAIProvider(ctx context.Context, cfg OpenAIProviderConfig) (*OpenAIProvider, error) {
	cfg.fillDefaults()
	httpClient := cfg.httpClient()
	chatModel, err := openai.NewChatModel(ctx, cfg.chatModelConfig(httpClient))
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

// fillDefaults 配置缺省落位：窗口 128k、输出 5%（下限 4096）、超时 60s。
// MaxOutputTokens 0 = 从窗口派生缺省；<0 = 不下发 max_tokens 参数（部分推理
// 模型要求 max_completion_tokens 或拒绝该参数）。
func (c *OpenAIProviderConfig) fillDefaults() {
	if c.ContextTokens <= 0 {
		c.ContextTokens = 128_000
	}
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = c.ContextTokens * 5 / 100
		if c.MaxOutputTokens < 4096 {
			c.MaxOutputTokens = 4096
		}
	}
	if c.Timeout <= 0 {
		c.Timeout = 60 * time.Second
	}
}

// httpClient 需要捕获 Retry-After 时注入带 retryAfterTransport 的 Client
// （Timeout 语义由注入的 Client 自带保留）；否则走 openai 包内部缺省。
func (c *OpenAIProviderConfig) httpClient() *http.Client {
	if !c.CaptureRetryAfter {
		return nil
	}
	return &http.Client{
		Timeout:   c.Timeout,
		Transport: retryAfterTransport{base: http.DefaultTransport},
	}
}

// chatModelConfig 组装 eino openai.ChatModelConfig（maxTokens 指针化）。
func (c *OpenAIProviderConfig) chatModelConfig(httpClient *http.Client) *openai.ChatModelConfig {
	var maxTokens *int
	if c.MaxOutputTokens > 0 {
		t := c.MaxOutputTokens
		maxTokens = &t
	}
	return &openai.ChatModelConfig{
		BaseURL:    c.BaseURL,
		APIKey:     c.APIKey,
		Model:      c.Model,
		MaxTokens:  maxTokens,
		Timeout:    c.Timeout,
		HTTPClient: httpClient,
	}
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
