// Package llm LLM 客户端薄封装（eino BaseChatModel）：
//   - 生成失败：指数退避 + jitter 重试，429 退避下限提高
//   - JSON 输出：解析失败带错误信息回喂重试
//   - token 记账：优先读响应 usage，缺省按 chars/4 估算
//   - 输入自守恒：fitInput 超限截断最长 user 消息留痕
//   - 限速：可选 token bucket 限速器
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/time/rate"

	"github.com/yi-nology/agentkit/obsx"
)

// TokenAccountant token 记账接口（调用方可接预算累计器或 Prometheus）。
type TokenAccountant interface {
	Add(n int)
	Used() int
	Remaining() int
}

// Client LLM 客户端（带重试、限速、预算、fitInput）。
type Client struct {
	Model     model.BaseChatModel
	ModelName string

	// 模型窗口（token，输入+输出合计）与单次输出预留。
	ContextTokens   int
	MaxOutputTokens int

	// OnUsage 每次调用后的 token 记账回调（stage, promptTokens, completionTokens）。
	OnUsage func(stage string, prompt, completion int)
	// Budget 任务 token 预算累计器（可 nil）。
	Budget TokenAccountant
	// Limiter 全局 LLM 请求限速器（token bucket）；nil = 不限速。
	Limiter *rate.Limiter

	MaxRetries int           // 最大尝试次数（含首次），默认 3
	BaseDelay  time.Duration // 指数退避基准，默认 2s
	MaxDelay   time.Duration // 退避上限，默认 30s
}

// NewClient 创建 LLM 客户端。
func NewClient(m model.BaseChatModel, modelName string, budget TokenAccountant) *Client {
	return &Client{
		Model: m, ModelName: modelName, Budget: budget,
		MaxRetries: 3, BaseDelay: 2 * time.Second, MaxDelay: 30 * time.Second,
	}
}

// RawModel 返回底层 eino 模型（Generator 接口实现）。
func (c *Client) RawModel() model.BaseChatModel { return c.Model }

// UsedTokens 返回累计消耗 token 数（无预算绑定时返回 0）。
func (c *Client) UsedTokens() int {
	if c.Budget != nil {
		return c.Budget.Used()
	}
	return 0
}

// BoundBudget 返回绑定的预算累计器（可 nil）。BudgetHolder 接口实现。
func (c *Client) BoundBudget() TokenAccountant { return c.Budget }

func estimateTokens(s string) int { return len(s) / 4 }

func (c *Client) account(stage string, in []*schema.Message, out *schema.Message) {
	var p, comp int
	if out != nil && out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		u := out.ResponseMeta.Usage
		p, comp = u.PromptTokens, u.CompletionTokens
	} else {
		for _, m := range in {
			p += estimateTokens(m.Content)
		}
		if out != nil {
			comp = estimateTokens(out.Content)
		}
	}
	if c.OnUsage != nil {
		c.OnUsage(stage, p, comp)
	}
	if c.Budget != nil {
		c.Budget.Add(p + comp)
	}
}

// Generate 带重试的普通生成（指数退避 + jitter）。
// 确定性失败（鉴权/权限/上下文超限）不重试。
// stage 同时注入 ctx（obsx.WithStage）——eino callbacks handler 可读到业务阶段。
func (c *Client) Generate(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, error) {
	ctx = obsx.WithStage(ctx, stage)
	msgs = copyMsgs(msgs) // fitInput 原地替换元素，不能污染调用方的切片
	c.fitInput(msgs)
	maxRetries := c.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	base := c.BaseDelay
	if base <= 0 {
		base = 2 * time.Second
	}
	ceil := c.MaxDelay
	if ceil <= 0 {
		ceil = 30 * time.Second
	}

	var lastErr error
	truncatedBoosted := false
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := c.backoffDelay(attempt, base, ceil, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		if c.Limiter != nil {
			if err := c.Limiter.Wait(ctx); err != nil {
				return nil, fmt.Errorf("llm: %s 限速等待取消: %w", stage, err)
			}
		}
		var opts []model.Option
		if IsTruncatedError(lastErr) && c.MaxOutputTokens > 0 && !truncatedBoosted {
			boosted := c.MaxOutputTokens * 3 / 2
			opts = append(opts, model.WithMaxTokens(boosted))
			truncatedBoosted = true
		}
		out, err := c.Model.Generate(ctx, msgs, opts...)
		if err == nil {
			if out.ResponseMeta != nil && out.ResponseMeta.FinishReason == "length" {
				c.account(stage, msgs, out)
				// Usage 是否存在取决于 Provider 实现（部分兼容端点缺失），不能裸解引用
				ct := "?"
				if out.ResponseMeta.Usage != nil {
					ct = fmt.Sprint(out.ResponseMeta.Usage.CompletionTokens)
				}
				err = fmt.Errorf("llm: %s 输出被截断（finish_reason=length, completion_tokens=%s）",
					stage, ct)
			} else {
				c.account(stage, msgs, out)
				return out, nil
			}
		}
		lastErr = err
		hint := ClassifyLLMError(err)
		if !hint.Retryable {
			break
		}
	}
	return nil, fmt.Errorf("llm: %s 调用失败（已重试 %d 次）: %w", stage, maxRetries-1, lastErr)
}

func (c *Client) backoffDelay(attempt int, base, ceil time.Duration, err error) time.Duration {
	minDelay := base
	if IsRateLimitError(err) {
		minDelay = 5 * time.Second
	}
	delay := base * time.Duration(1<<uint(attempt-1))
	if delay < minDelay {
		delay = minDelay
	}
	if half := int64(base / 2); half > 0 { // base=1ns 等极小值时 Int63n(0) 会 panic
		delay += time.Duration(rand.Int63n(half))
	}
	if delay > ceil {
		delay = ceil
	}
	return delay
}

// GenerateJSON 生成并解析 JSON；解析失败把原始输出与错误回喂重试 1 次。
// out 必须是 *T。截断在 Generate 内部已转为错误返回，走到这里的 lastErr 只会是
// json.Unmarshal 错误——回喂提示只有"输出合法 JSON"一种。
func (c *Client) GenerateJSON(ctx context.Context, stage string, msgs []*schema.Message, out any) error {
	ctx = obsx.WithStage(ctx, stage)
	msgs = copyMsgs(msgs)
	var lastErr error
	lastRaw := ""
	for attempt := 0; attempt < 2; attempt++ {
		callMsgs := msgs
		if attempt > 0 {
			hint := "你上一轮的输出无法解析为合法 JSON（错误：%v）。\n请重新输出，且只输出合法 JSON：不要解释、不要 markdown 代码围栏。"
			callMsgs = append(append([]*schema.Message{}, msgs...),
				&schema.Message{Role: schema.Assistant, Content: lastRaw},
				schema.UserMessage(fmt.Sprintf(hint, lastErr)),
			)
		}
		resp, err := c.Generate(ctx, stage, callMsgs)
		if err != nil {
			return err
		}
		raw := ExtractJSON(resp.Content)
		if err := json.Unmarshal([]byte(raw), out); err != nil {
			lastErr = err
			lastRaw = resp.Content
			continue
		}
		return nil
	}
	return fmt.Errorf("llm: %s JSON 解析失败（已带错误重试 1 次）: %w", stage, lastErr)
}

// fitInput 发送前输入自守恒：总输入超 (窗口-输出预留)×2×0.9 时，
// 截断最长的 user 消息并留痕（兜底层，正常轮不到）。
func (c *Client) fitInput(msgs []*schema.Message) {
	if c.ContextTokens <= 0 {
		return
	}
	reserved := c.MaxOutputTokens
	if reserved <= 0 {
		reserved = c.ContextTokens / 20
	}
	limit := (c.ContextTokens - reserved) * 2 * 9 / 10
	if limit <= 0 {
		return
	}
	lens := make([]int, len(msgs))
	total := 0
	longest := -1
	for i, m := range msgs {
		lens[i] = len([]rune(m.Content))
		total += lens[i]
		if m.Role == schema.User && (longest < 0 || lens[i] > lens[longest]) {
			longest = i
		}
	}
	if total <= limit || longest < 0 {
		return
	}
	keep := limit - (total - lens[longest])
	if keep < 0 {
		keep = 0
	}
	r := []rune(msgs[longest].Content)
	cloned := *msgs[longest]
	cloned.Content = string(r[:keep]) + "\n…（输入超出模型窗口预算，已截断留痕）"
	msgs[longest] = &cloned
}
