// Hybrid 向量+关键词双路混合检索（R10，RRF 融合）：语义路（MilvusStore，embedding
// 近邻）与词料路（Local，TF-IDF）各取候选，倒数排名融合（Reciprocal Rank Fusion）——
// 跨路命中的文档得分累加（双路都认为相关=真相关的最强信号），单路独有的按名次计分。
// 降级哲学与装配侧一致：任一路失败降级为另一路纯检索（检索面永不因一路缺失禁用），
// 双路全败才回错。
package rag

import (
	"context"
	"sort"
	"sync"

	"github.com/cloudwego/eino/components/tool"
)

// rrfK RRF 常数（惯例 60）：越大排名差异对得分的影响越平缓。
const rrfK = 60

// Hybrid 组合检索器：vector=语义路（必选），keyword=词料路（Local 的 TF-IDF 索引，
// 纯内存随目录重建）。
type Hybrid struct {
	vector  KnowledgeService
	keyword *Local
}

// NewHybrid 构造混合检索器。
func NewHybrid(vector KnowledgeService, keyword *Local) *Hybrid {
	return &Hybrid{vector: vector, keyword: keyword}
}

// Retrieve 双路并行检索 → RRF 融合 → topK。两路各取 topK*2 候选（融合后截断，
// 候选池放大让跨路重叠有归并空间）；单路失败降级单路，双路全败回 first error。
func (h *Hybrid) Retrieve(ctx context.Context, query string, topK int, filter Filter) ([]Chunk, error) {
	type lane struct {
		chunks []Chunk
		err    error
	}
	res := make([]lane, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		cs, err := h.vector.Retrieve(ctx, query, topK*2, filter)
		res[0] = lane{chunks: cs, err: err}
	}()
	go func() {
		defer wg.Done()
		cs, err := h.keyword.Retrieve(ctx, query, topK*2, filter)
		res[1] = lane{chunks: cs, err: err}
	}()
	wg.Wait()

	var lists [][]Chunk
	var firstErr error
	for _, l := range res {
		if l.err != nil {
			if firstErr == nil {
				firstErr = l.err
			}
			continue
		}
		if len(l.chunks) > 0 {
			lists = append(lists, l.chunks)
		}
	}
	if len(lists) == 0 {
		return nil, firstErr
	}
	return FuseRRF(topK, lists...), nil
}

// AsTool 工具化透传语义路（检索语义对 LLM 不可见——混合是检索器内部实现细节）。
func (h *Hybrid) AsTool() tool.BaseTool { return h.vector.AsTool() }

// FuseRRF 倒数排名融合：score=Σ 1/(k+rank_i)（rank 从 1 起），同 ID 跨路累加
// （双路共识加权=混合检索的本意）；输出按得分降序、同分按 ID 字典序（确定性可重放），
// 截 topK。lists 为空返回空。
func FuseRRF(topK int, lists ...[]Chunk) []Chunk {
	score := map[string]float64{}
	best := map[string]Chunk{}
	for _, chunks := range lists {
		for i, c := range chunks {
			key := c.ID
			if key == "" {
				key = c.Content
			}
			score[key] += 1 / float64(rrfK+i+1)
			if prev, ok := best[key]; !ok || len(c.Content) > len(prev.Content) {
				best[key] = c // 跨路同 ID 保留信息量最大的代表副本
			}
		}
	}
	keys := make([]string, 0, len(score))
	for k := range score {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if score[keys[i]] != score[keys[j]] {
			return score[keys[i]] > score[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > topK {
		keys = keys[:topK]
	}
	out := make([]Chunk, 0, len(keys))
	for _, k := range keys {
		c := best[k]
		c.Score = score[k] // 融合分回填（检索观测面：跨路共识值 > 单路值）
		out = append(out, c)
	}
	return out
}
