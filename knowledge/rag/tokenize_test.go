package rag

import (
	"reflect"
	"testing"
)

// TestTokenize 锁死分词语义：ASCII 词小写、每个 Han 字与后邻成二元组
// （后邻非 Han 时补单字）、非字母数字为边界。含 CJK 扩展区 4 字节字符与全角/假名（非 Han）边界。
func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"hello World 123", []string{"hello", "world", "123"}},
		{"错误处理", []string{"错误", "误处", "处理", "理"}},
		{"单字", []string{"单字", "字"}},
		{"中", []string{"中"}},
		{"测试，中文标点。分隔！", []string{"测试", "试", "中文", "文标", "标点", "点", "分隔", "隔"}},
		{"Go 代码风格 go.md v0.8.2", []string{"go", "代码", "码风", "风格", "格", "go", "md", "v0", "8", "2"}},
		{"𠀀𠀁扩展B四字节", []string{"𠀀𠀁", "𠀁扩", "扩展", "展", "b", "四字", "字节", "节"}},
		{"𠀀a𠀀", []string{"𠀀", "a", "𠀀"}},
		{"emoji 😀😃 与汉字中文字符", []string{"emoji", "与汉", "汉字", "字中", "中文", "文字", "字符", "符"}},
		{"ＡＢＣ全角字母ＸＹＺ", []string{"全角", "角字", "字母", "母"}}, // 全角字母非 ASCII，作边界
		{"日本語のテキストひらがな", []string{"日本", "本語", "語"}}, // 假名非 Han
		{"한국어 텍스트", nil},                                      // 谚文非 Han
	}
	for _, c := range cases {
		got := tokenize(c.in)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("tokenize(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
