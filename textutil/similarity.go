// 字符 bigram 集合 + Jaccard 相似度的近重复检测原语（沉淀自 heimdallr 挖掘管道）。
// 可解释、小文本下精确、零依赖；候选规模（百~千条）下直接比对足够快，不必上向量库。
package textutil

import "strings"

// BigramSet 字符 bigram 集合：小写化、去空白（空白差异不算差异）；
// 单字文本也有指纹（该字本身）。
func BigramSet(s string) map[string]bool {
	s = strings.Join(strings.Fields(strings.ToLower(s)), "")
	runes := []rune(s)
	out := make(map[string]bool, len(runes))
	for i := 0; i < len(runes)-1; i++ {
		out[string(runes[i])+string(runes[i+1])] = true
	}
	if len(runes) == 1 {
		out[s] = true
	}
	return out
}

// Jaccard 两个集合的 Jaccard 系数 [0,1]；两者皆空视为完全相同（返回 1）。
func Jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
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
func Similarity(a, b string) float64 {
	return Jaccard(BigramSet(a), BigramSet(b))
}

// NearDuplicate 相似度 ≥ threshold 视为近重复。
func NearDuplicate(a, b string, threshold float64) bool {
	return Similarity(a, b) >= threshold
}
