package llm

import (
	"strings"
	"sync"
)

// CostTracker token 成本追踪器。
// 按模型+阶段统计 token 消耗和成本。
type CostTracker struct {
	mu      sync.Mutex
	records []CostRecord
}

// CostRecord 单次调用成本记录。
type CostRecord struct {
	Model            string // 模型名
	Stage            string // 阶段（R1/R2/R3/R4/R5/plugin:xxx）
	PromptTokens     int
	CompletionTokens int
	CostUSD          float64 // 本次成本（美元）
}

// NewCostTracker 创建成本追踪器。
func NewCostTracker() *CostTracker {
	return &CostTracker{}
}

// maxCostRecords 单进程保留的最大记录数（长驻进程防无界增长；超限丢最旧，
// Summary 汇总数字随之只反映窗口内记录）。
const maxCostRecords = 10_000

// Record 记录一次调用。
func (ct *CostTracker) Record(model, stage string, promptTokens, completionTokens int, costPer1K [2]float64) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if len(ct.records) >= maxCostRecords {
		ct.records = ct.records[1:]
	}
	cost := float64(promptTokens)/1000*costPer1K[0] + float64(completionTokens)/1000*costPer1K[1]
	ct.records = append(ct.records, CostRecord{
		Model:            model,
		Stage:            stage,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CostUSD:          cost,
	})
}

// Records 返回所有记录的副本。
func (ct *CostTracker) Records() []CostRecord {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	out := make([]CostRecord, len(ct.records))
	copy(out, ct.records)
	return out
}

// Summary 按模型汇总。
func (ct *CostTracker) Summary() map[string]ModelSummary {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	m := map[string]ModelSummary{}
	for _, r := range ct.records {
		s := m[r.Model]
		s.Model = r.Model
		s.TotalPromptTokens += r.PromptTokens
		s.TotalCompletionTokens += r.CompletionTokens
		s.TotalCostUSD += r.CostUSD
		s.CallCount++
		m[r.Model] = s
	}
	return m
}

// ModelSummary 单模型汇总。
type ModelSummary struct {
	Model                 string
	TotalPromptTokens     int
	TotalCompletionTokens int
	TotalCostUSD          float64
	CallCount             int
}

// TotalTokens 总 token 消耗。
func (s ModelSummary) TotalTokens() int {
	return s.TotalPromptTokens + s.TotalCompletionTokens
}

// Price 单模型定价（USD / 1M tokens）。
type Price struct {
	InputPerM  float64
	OutputPerM float64
}

// PriceOf 按模型名匹配定价表估算单次调用成本（USD；未配价返回 0，不阻塞记账）。
// 精确匹配优先，未命中回落最长前缀（模型名带版本/日期后缀时定价键可只写主干）。
// 宿主只需提供 map[模型名]Price；来自 bianque scheduler/usage.go 生产路径（v0.9.5 沉淀）。
func PriceOf(pricing map[string]Price, model string, prompt, completion int) float64 {
	p, ok := pricing[model]
	if !ok {
		bestLen := 0
		for prefix, cand := range pricing {
			if len(prefix) > bestLen && strings.HasPrefix(model, prefix) {
				p, bestLen = cand, len(prefix)
			}
		}
		if bestLen == 0 {
			return 0
		}
	}
	return (float64(prompt)*p.InputPerM + float64(completion)*p.OutputPerM) / 1e6
}
