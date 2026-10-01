// 字符 bigram 集合 + Jaccard 相似度的近重复检测原语（沉淀自 heimdallr 挖掘管道）。
// 可解释、小文本下精确、零依赖；候选规模（百~千条）下直接比对足够快，不必上向量库。
package textutil

import "strings"

// BigramSet 字符 bigram 集合：小写化、去空白（空白差异不算差异）；
// 单字文本也有指纹（该字本身）。
// 零值/空串返回非 nil 空集，调用方可直接 range。
func BigramSet(s string) map[string]bool {
	s = strings.Join(strings.Fields(strings.ToLower(s)), "")
	runes := []rune(s)
	out := make(map[string]bool, max(0, len(runes)-1))
	for i := 0; i+1 < len(runes); i++ {
		// string(runes[i:i+2]) 一次物化两字符，免两次 string(rune)+拼接
		out[string(runes[i:i+2])] = true
	}
	if len(runes) == 1 {
		out[s] = true
	}
	return out
}

// Jaccard 两个集合的 Jaccard 系数 [0,1]；两者皆空视为完全相同（返回 1）。
// 迭代较小集合，map 探测次数取 min(|a|,|b|)。
func Jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	// 保证 a 是较小的一侧，减少 range 次数
	if len(a) > len(b) {
		a, b = b, a
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// Similarity 两段文本的相似度 = 字符 bigram 集合的 Jaccard 系数 [0,1]。
// 全等短路（含双双归一为空的等价情形交给 BigramSet/Jaccard，不在此误判）。
func Similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	return Jaccard(BigramSet(a), BigramSet(b))
}

// NearDuplicate 相似度 ≥ threshold 视为近重复。
func NearDuplicate(a, b string, threshold float64) bool {
	return Similarity(a, b) >= threshold
}
