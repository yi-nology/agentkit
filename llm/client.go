// Package llm LLM 客户端栈（eino BaseChatModel 之上），本包唯一 package doc。
// 文件族分工：
//   - client/errors/config：客户端薄封装——生成失败指数退避+jitter 重试（429 退避
//     下限提高）、JSON 输出解析失败带错误回喂重试、token 记账（优先响应 usage，
//     缺省 chars/4 估算）、fitInput 输入自守恒、可选 token bucket 限速
//   - provider/openai：Provider 抽象与多后端 + FallbackChain 降级链
//   - resilient/failover：弹性客户端（降级矩阵/熔断/预算短路）+ 裸模型路径的
//     FailoverModel 装饰器
//   - budget/cost/usage：预算、成本、完整用量采集三套记账
//   - stage_router：按业务阶段路由模型
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/time/rate"

	"github.com/yi-nology/agentkit/jsonrepair"
	"github.com/yi-nology/agentkit/obsx"
	"github.com/yi-nology/agentkit/textutil"
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
// 确定性失败（鉴权/权限/上下文超限）不重试；429 退避下限提高后值得等
// （单客户端无备选可切）。stage 同时注入 ctx（obsx.WithStage）——eino
// callbacks handler 可读到业务阶段。
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
	out, err := c.generateRetry(ctx, stage, msgs, retryPolicy{
		maxAttempts:   maxRetries,
		base:          base,
		ceil:          ceil,
		chainFallback: false, // 单客户端无备选模型，429 值得退避等待
	})
	if err != nil {
		return nil, fmt.Errorf("llm: %s 调用失败（已重试 %d 次）: %w", stage, maxRetries-1, err)
	}
	return out, nil
}

// retryPolicy 单模型重试策略（Client.Generate 与 Resilient.tryOneProvider 共用
// 骨架的参数——重试核心只有一份实现，文案/策略不再漂移）。
type retryPolicy struct {
	maxAttempts int // 总尝试次数（含首次）
	base, ceil  time.Duration
	// chainFallback true=429 与确定性失败直接跳出（上层降级链有备选模型可切）；
	// false=仅确定性失败跳出，429 值得退避等待（单客户端无备选）。
	chainFallback bool
	// attemptTimeout 单次尝试独立限时（0=不限）。不能覆盖整个重试循环——
	// 首次尝试耗满 deadline 后重试全部形同虚设。
	attemptTimeout time.Duration
}

// generateRetry 单模型重试核心（全包唯一实现）：限速 → 截断后提升 MaxTokens
// （一次）→ Generate → finish_reason=length 转错误并记账 → 按 ClassifyLLMError
// 决定重试。ctx 取消原样上抛。
func (c *Client) generateRetry(ctx context.Context, stage string, msgs []*schema.Message, pol retryPolicy) (*schema.Message, error) {
	// Client 侧已配置记账（OnUsage/Budget）时打标记：NewUsageHandler 检测到
	// 标记跳过发射——同一次物理调用的 Generate 路径与 callbacks 路径只记一次账
	if c.OnUsage != nil || c.Budget != nil {
		ctx = withClientAccounting(ctx)
	}
	var lastErr error
	truncatedBoosted := false
	for attempt := 0; attempt < pol.maxAttempts; attempt++ {
		if attempt > 0 {
			if pol.chainFallback {
				// 同模型重试仅限"值得在同模型上重试"的错误：
				// 429（限速池独立）与 context 超限/401（确定性失败）直接跳出，切换下一模型
				if !sameModelRetryable(lastErr) {
					break
				}
			} else if !ClassifyLLMError(lastErr).Retryable {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffDelay(attempt, pol.base, pol.ceil, lastErr)):
			}
		}
		if c.Limiter != nil {
			if err := c.Limiter.Wait(ctx); err != nil {
				return nil, fmt.Errorf("llm: %s 限速等待取消: %w", stage, err)
			}
		}
		attemptCtx := ctx
		if pol.attemptTimeout > 0 {
			var cancel context.CancelFunc
			attemptCtx, cancel = context.WithTimeout(ctx, pol.attemptTimeout)
			defer cancel()
		}
		var opts []model.Option
		if IsTruncatedError(lastErr) && c.MaxOutputTokens > 0 && !truncatedBoosted {
			opts = append(opts, model.WithMaxTokens(c.MaxOutputTokens*3/2))
			truncatedBoosted = true
		}
		out, err := c.Model.Generate(attemptCtx, msgs, opts...)
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
	}
	return nil, lastErr
}

// backoffDelay 指数退避 + jitter（Client 与 Resilient 共用）；429 下限 5s，
// 下限本身仍封顶 ceil——ceil<5s 的测试场景下最终钳到 ceil 的结果不变。
func backoffDelay(attempt int, base, ceil time.Duration, err error) time.Duration {
	minDelay := base
	if IsRateLimitError(err) {
		minDelay = 5 * time.Second
		if minDelay > ceil {
			minDelay = ceil
		}
	}
	delay := base * time.Duration(1<<uint(attempt-1))
	if delay < minDelay {
		delay = minDelay
	}
	delay += time.Duration(fastRand(int64(base / 2)))
	if delay > ceil {
		delay = ceil
	}
	return delay
}

// GenerateJSON 生成并解析 JSON；解析失败把原始输出与错误回喂重试 1 次。
// out 必须是 *T。截断在 Generate 内部已转为错误返回，走到这里的 lastErr 只会是
// json.Unmarshal 错误——回喂提示只有"输出合法 JSON"一种。
// 刻意走 ExtractJSON+严格 Unmarshal 而非 jsonrepair.Unmarshal 宽容链：宽容修复
// 会改变输出内容，而本方法的失败路径要回喂重试，lastErr 须是纯解析错误。
// 宽容回收半损坏输出归 jsonrepair.Unmarshal（无重试回喂的场景用）。
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
		raw := jsonrepair.ExtractJSON(resp.Content)
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
	cloned := *msgs[longest]
	cloned.Content = "\n" + textutil.TruncNote(msgs[longest].Content, keep, "输入超出模型窗口预算，已截断留痕")
	msgs[longest] = &cloned
}
