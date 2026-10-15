package textutil

import "testing"

// 相似度：字符 bigram 集合的 Jaccard（可解释、小文本下精确；
// SimHash 在 ~10 个特征的小文本上噪声过大——尾部加一个字就能推离阈值）。
func TestSimilarity(t *testing.T) {
	a := "帮我查询北京今天的天气并给出穿衣建议"
	if Similarity(a, a) != 1 {
		t.Fatal("identical text must be similarity 1")
	}
	// 尾部追加一字：10/11 bigram 共享 → 高相似（近重复）
	if got := Similarity(a, a+"啊"); got < 0.9 {
		t.Fatalf("one-char append must stay ≥0.9, got %v", got)
	}
	// 语义变化（今天→明天）：bigram 重叠明显下降，不应按近重复拒绝
	if got := Similarity(a, "帮我查询北京明天的天气并给出穿衣建议"); got >= 0.9 {
		t.Fatalf("semantically different text must fall below threshold, got %v", got)
	}
	// 完全无关
	if got := Similarity(a, "删除生产数据库里的所有用户表"); got > 0.2 {
		t.Fatalf("unrelated text must be far apart, got %v", got)
	}
	if Similarity("", "") != 1 {
		t.Fatal("empty vs empty is identical")
	}
}

// 归一化：空白差异不算差异。
func TestSimilarityNormalized(t *testing.T) {
	if Similarity("  查询 北京 天气 ", "查询北京天气") != 1 {
		t.Fatal("whitespace must be normalized")
	}
}

func TestBigramSet(t *testing.T) {
	b := BigramSet("查询天气")
	if len(b) != 3 { // 查询 询天 天气
		t.Fatalf("bigram set: %v", b)
	}
	if !BigramSet("查")["查"] {
		t.Fatal("single rune text must have a fingerprint")
	}
}

func TestJaccardAndNearDuplicate(t *testing.T) {
	a := map[string]bool{"a": true, "b": true}
	b := map[string]bool{"b": true, "c": true}
	if got := Jaccard(a, b); got < 0.33 || got > 0.34 { // 1/3
		t.Fatalf("Jaccard({a,b},{b,c})=1/3, got %v", got)
	}
	if !NearDuplicate("查询北京天气", "查询北京天气", 0.9) {
		t.Fatal("identical text must be near duplicate")
	}
	if NearDuplicate("查询北京天气", "删除所有用户表", 0.9) {
		t.Fatal("unrelated text must not be near duplicate")
	}
}
