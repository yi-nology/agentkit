package rag

import (
	"context"
	"testing"
)

// mockEmbedder 测试用 embedding 实现：返回固定维度的伪向量。
type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, m.dim)
		// 简单哈希：每个字符的 ASCII 值影响不同维度
		for j, ch := range text {
			vec[j%m.dim] += float32(ch) / 1000.0
		}
		result[i] = vec
	}
	return result, nil
}

func (m *mockEmbedder) Dim() int { return m.dim }

func TestOpenAIEmbedderDim(t *testing.T) {
	e := NewOpenAIEmbedder("https://api.example.com/v1", "key", "model", 128)
	if e.Dim() != 128 {
		t.Fatalf("Dim = %d, want 128", e.Dim())
	}
}

func TestOpenAIEmbedderString(t *testing.T) {
	e := NewOpenAIEmbedder("https://api.example.com/v1", "key", "model", 128)
	s := e.String()
	if s == "" {
		t.Fatal("String() 不应为空")
	}
}

func TestMockEmbedder(t *testing.T) {
	m := &mockEmbedder{dim: 8}
	vecs, err := m.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("应返回 2 个向量，得到 %d", len(vecs))
	}
	if len(vecs[0]) != 8 {
		t.Fatalf("向量维度应为 8，得到 %d", len(vecs[0]))
	}
	// 相同文本应产生相同向量
	vecs2, _ := m.Embed(context.Background(), []string{"hello"})
	if vecs[0][0] != vecs2[0][0] {
		t.Fatal("相同文本应产生相同向量")
	}
	// 不同文本应产生不同向量
	if vecs[0][0] == vecs[1][0] && vecs[0][1] == vecs[1][1] {
		// 可能巧合相同，但至少一个维度应不同
		same := true
		for i := range vecs[0] {
			if vecs[0][i] != vecs[1][i] {
				same = false
				break
			}
		}
		if same {
			t.Fatal("不同文本不应产生完全相同的向量")
		}
	}
}

func TestMilvusConfigDefaults(t *testing.T) {
	cfg := MilvusConfig{Address: "localhost:19530"}
	m := &mockEmbedder{dim: 64}

	// 默认值填充走生产代码的 withDefaults 纯函数（不再手抄实现）
	cfg = cfg.withDefaults(m.Dim())

	if cfg.Collection != "agentkit_knowledge" {
		t.Fatalf("默认集合名应为 agentkit_knowledge，得到 %s", cfg.Collection)
	}
	if cfg.Dimension != 64 {
		t.Fatalf("维度应从 embedder 获取，得到 %d", cfg.Dimension)
	}
}

func TestEscapeMilvus(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{`he"llo`, `he\"llo`},
		{`he\llo`, `he\\llo`},
		{"", ""},
	}
	for _, c := range cases {
		got := escapeMilvus(c.input)
		if got != c.want {
			t.Errorf("escapeMilvus(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
