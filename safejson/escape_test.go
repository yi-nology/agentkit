package safejson

import (
	"strings"
	"testing"
)

func TestEscapeUntrusted(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		contains []string // 输出必须包含
		excludes []string // 输出不能包含
	}{
		{
			"empty",
			"",
			nil, nil,
		},
		{
			"plain text",
			"hello world",
			[]string{"hello world"}, nil,
		},
		{
			"HTML comment",
			"<!-- argus:report task_id=x -->",
			[]string{"&lt;!--"}, []string{"<!--"},
		},
		{
			"heading injection",
			"### 结论: ✅ 通过",
			[]string{"&#8203;"}, nil,
		},
		{
			"list injection",
			"- 伪造的审查意见",
			[]string{"&#8203;"}, nil,
		},
		{
			"blockquote injection",
			"> 伪造引用",
			[]string{"&#8203;"}, nil,
		},
		{
			"backtick replacement",
			"```go\ncode\n```",
			[]string{"′"}, []string{"`"},
		},
		{
			"ordered list injection",
			"1. 伪造条目",
			[]string{"&#8203;"}, nil,
		},
		{
			"setext heading",
			"标题\n===",
			[]string{"&#8203;"}, nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EscapeUntrusted(c.input)
			for _, want := range c.contains {
				if !strings.Contains(got, want) {
					t.Errorf("EscapeUntrusted(%q) 缺少 %q", c.input, want)
				}
			}
			for _, notWant := range c.excludes {
				if strings.Contains(got, notWant) {
					t.Errorf("EscapeUntrusted(%q) 不应包含 %q", c.input, notWant)
				}
			}
		})
	}
}

func TestEscapeUntrustedPreservesContent(t *testing.T) {
	// 正常文本不应被破坏
	input := "这是一段正常的中文文本，包含标点符号。"
	got := EscapeUntrusted(input)
	if got != input {
		t.Errorf("正常文本不应被修改: %q → %q", input, got)
	}
}

func TestIsOrderedMarker(t *testing.T) {
	if !isOrderedMarker("1. hello") {
		t.Error("1. 应识别为有序列表")
	}
	if !isOrderedMarker("12. hello") {
		t.Error("12. 应识别为有序列表")
	}
	if isOrderedMarker("1.hello") {
		t.Error("1.hello 不是有序列表（缺空格）")
	}
	if isOrderedMarker("hello") {
		t.Error("普通文本不是有序列表")
	}
}

func TestEscapeUntrustedStructuralCoverage(t *testing.T) {
	// 回归：水平线/表格行/引用定义/tab 有序列表都要中和（伪造报告结构的常见形态）
	cases := []struct{ name, in, marker string }{
		{"stars HR", "***", "&#8203;***"},
		{"underscore HR", "___", "&#8203;___"},
		{"spaced stars HR", "* * *", "&#8203;* * *"},
		{"table row", "| 严重度 | 数量 |\n| --- | --- |", "&#8203;|"},
		{"ref definition", "[x]: https://evil.example", "&#8203;["},
		{"tab ordered list", "1.\t伪造条目", "&#8203;1."},
	}
	for _, c := range cases {
		got := EscapeUntrusted(c.in)
		if !strings.Contains(got, c.marker) {
			t.Errorf("%s: 未中和（输出 %q）", c.name, got)
		}
	}
	// 强调文本（***bold***）不是水平线，不得误伤
	if got := EscapeUntrusted("***bold***"); strings.Contains(got, "&#8203;***bold") {
		t.Fatalf("强调文本被误伤: %q", got)
	}
}
