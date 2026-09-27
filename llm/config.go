package llm

// BudgetConfig 模型窗口派生配置（"单一基准"哲学：窗口是唯一基准，全部上限按比例派生）。
type BudgetConfig struct {
	ContextTokens int // 模型上下文窗口（token 数，输入+输出合计）
}

// MaxDiffChars diff 总量上限（字符）：窗口 35% 折算字符，min 10k。
// chars/token 按 4 估算（中文按 2 保守，见 fitInput）。
func (c BudgetConfig) MaxDiffChars() int {
	n := c.ContextTokens * 35 / 100 * 4
	if n < 10_000 {
		n = 10_000
	}
	return n
}

// TaskTokenBudget 任务 token 预算：窗口 60%。
func (c BudgetConfig) TaskTokenBudget() int {
	return c.ContextTokens * 60 / 100
}

// MaxOutputTokens 单次生成上限：max(4096, 窗口 5%)。
func (c BudgetConfig) MaxOutputTokens() int {
	n := c.ContextTokens * 5 / 100
	if n < 4096 {
		n = 4096
	}
	return n
}

// ReqBodyMaxChars 需求文本上限：窗口 6%。
func (c BudgetConfig) ReqBodyMaxChars() int {
	return c.ContextTokens * 6 / 100
}

// ReqIssueMaxChars issue 文本上限：窗口 1.5%。
func (c BudgetConfig) ReqIssueMaxChars() int {
	return c.ContextTokens * 15 / 1000
}
