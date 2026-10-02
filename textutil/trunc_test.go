package textutil

import "testing"

func TestTruncRunes(t *testing.T) {
	cases := []struct {
		input     string
		n         int
		want      string
		truncated bool
	}{
		{"hello", 10, "hello", false},
		{"hello", 5, "hello", false},
		{"hello", 3, "hel", true},
		{"你好世界", 2, "你好", true},
		{"你好世界", 4, "你好世界", false},
		{"", 0, "", false},
		{"abc", 0, "", true},
	}
	for _, c := range cases {
		got, tr := TruncRunes(c.input, c.n)
		if got != c.want || tr != c.truncated {
			t.Errorf("TruncRunes(%q, %d) = (%q, %v), want (%q, %v)",
				c.input, c.n, got, tr, c.want, c.truncated)
		}
	}
}

func TestTruncRunesMultibyte(t *testing.T) {
	// 多字节字符不应被腰斩
	s := "你好世界"
	got, tr := TruncRunes(s, 3)
	if !tr {
		t.Fatal("应截断")
	}
	// 验证输出是合法 UTF-8
	if got != "你好世" {
		t.Fatalf("应为 '你好世', 得到 %q", got)
	}
}
