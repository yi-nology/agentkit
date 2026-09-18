package textutil

import (
	"testing"
	"unicode/utf8"
)

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

func TestTruncRunesNegative(t *testing.T) {
	// 回归：负上限按"全部截断"处理，不 panic
	got, truncated := TruncRunes("abc", -1)
	if got != "" || !truncated {
		t.Fatalf("负上限应返回空串+true，得到 %q/%v", got, truncated)
	}
}

func TestTruncEllipsis(t *testing.T) {
	cases := []struct {
		in, want string
		n        int
	}{
		{"hello", "hello", 10},
		{"hello", "hello", 5},
		{"hello", "hel…", 3},
		{"你好世界", "你好…", 2},
		{"你好世界", "你好世界", 4},
		{"", "…", 0},
		{"abc", "…", -1},
	}
	for _, c := range cases {
		if got := TruncEllipsis(c.in, c.n); got != c.want {
			t.Errorf("TruncEllipsis(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestSplitRunes(t *testing.T) {
	// 等分：3 块 2+2+1，最后一块可不足
	got := SplitRunes("你好世界啊", 2)
	if len(got) != 3 || got[0] != "你好" || got[2] != "啊" {
		t.Fatalf("SplitRunes = %v", got)
	}
	// 不需分块 / n<=0
	if got := SplitRunes("abc", 10); len(got) != 1 || got[0] != "abc" {
		t.Fatalf("短串应单块: %v", got)
	}
	if got := SplitRunes("abc", 0); len(got) != 1 || got[0] != "abc" {
		t.Fatalf("n<=0 应整串单块: %v", got)
	}
	// 多字节不被腰斩
	for _, part := range SplitRunes("中文内容分块测试", 3) {
		if !utf8.ValidString(part) {
			t.Fatalf("分块产生非法 UTF-8: %q", part)
		}
	}
}
