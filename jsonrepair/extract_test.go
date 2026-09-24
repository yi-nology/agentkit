package jsonrepair

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain object", `{"a":1}`, `{"a":1}`},
		{"plain array", `[1,2]`, `[1,2]`},
		{"markdown fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"fence no lang", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"extra text", `some text {"a":1} more`, `{"a":1}`},
		{"nested braces", `{"a":{"b":1}}`, `{"a":{"b":1}}`},
		{"empty", ``, ``},
		{"no braces", `hello`, `hello`},
	}
	for _, c := range cases {
		got := ExtractJSON(c.input)
		if got != c.want {
			t.Errorf("%s: ExtractJSON(%q) = %q, want %q", c.name, c.input, got, c.want)
		}
	}
}

// 目标 schema（对象含数组字段 / 顶层数组两种常见宿主形态）。
type out struct {
	Items []struct {
		File    string `json:"file"`
		Comment string `json:"comment"`
	} `json:"items"`
	Note string `json:"note"`
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestUnmarshalFastPathUnchanged(t *testing.T) {
	// 历史行为不回归：合法 JSON（含围栏、散文前后缀）走快路径原样命中。
	want := out{Note: "ok"}
	cases := []string{
		mustJSON(t, want),
		"```json\n" + mustJSON(t, want) + "\n```",
		"解析结果：\n" + mustJSON(t, want) + "\n以上。",
	}
	for i, in := range cases {
		var got out
		if err := Unmarshal(in, &got); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got.Note != "ok" {
			t.Fatalf("case %d: note=%q", i, got.Note)
		}
	}
}

func TestUnmarshalRepairsCommonBreakage(t *testing.T) {
	// 高频半损坏形态逐一可回收：全角结构符、尾逗号、非法转义、未闭合/截断。
	// （对象值落 string 字段等类型级错配不在宽容范围——那是领域 schema 的活。）
	cases := []string{
		`{"note"："ok"}`,      // 全角冒号
		`{"note"："ok"，}`,     // 全角冒号 + 全角尾逗号
		"{\"note\":\"ok\",}", // ASCII 尾逗号
		`{"note":"a\|b"}`,    // 非法转义（\|）
		`{"note":"ok"`,       // 截断未闭合
		"结论如下：\n" + "`" + `{"note":"ok"}` + "`", // 散文包裹 + 反引号
		"```json\n{\"note\":\"ok\",}\n```",      // 围栏内尾逗号
	}
	for i, in := range cases {
		var got out
		if err := Unmarshal(in, &got); err != nil {
			t.Fatalf("case %d (%q): %v", i, in, err)
		}
		if got.Note == "" {
			t.Fatalf("case %d: note 为空", i)
		}
	}
}

func TestUnmarshalTruncatedWithProse(t *testing.T) {
	// 截断 + 散文：字符串位已闭合、只剩对象括号未闭合——ExtractJSON 切到片段，
	// Repair 补括号后可解析。（字符串值本身被截断超出语法修复能力，不在此列。）
	var got out
	in := `模型输出被截断，前半段如下：{"note":"部分"`
	if err := Unmarshal(in, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Note != "部分" {
		t.Fatalf("note=%q", got.Note)
	}
}

func TestUnmarshalTopLevelArray(t *testing.T) {
	// 顶层数组（findings 清单形态）：快路径切片 + 修复都应保留数组语义。
	var items []struct {
		File    string `json:"file"`
		Comment string `json:"comment"`
	}
	for _, in := range []string{
		`[{"file":"a.go","comment":"x"}]`,
		"发现如下：\n[{\"file\":\"a.go\",\"comment\":\"x\",}]",
	} {
		if err := Unmarshal(in, &items); err != nil {
			t.Fatalf("in=%q: %v", in, err)
		}
		if len(items) != 1 || items[0].File != "a.go" {
			t.Fatalf("in=%q: %+v", in, items)
		}
	}
}

func TestUnmarshalRejectsNonJSON(t *testing.T) {
	// 纯散文/空输入仍报错——宽容只救格式损坏，不把非 JSON 文本硬造成零值。
	for _, in := range []string{"", "抱歉，我无法输出该内容。", "plain text only"} {
		var got out
		if err := Unmarshal(in, &got); err == nil {
			t.Fatalf("in=%q: 期望报错", in)
		}
	}
}

func TestUnmarshalErrorCarriesBothPaths(t *testing.T) {
	// 错误信息同时携带严格与宽容两路原因，回喂 LLM 时无需调用方拼装。
	var got out
	err := Unmarshal("完全不是 JSON", &got)
	if err == nil || !strings.Contains(err.Error(), "严格") || !strings.Contains(err.Error(), "宽容") {
		t.Fatalf("err=%v", err)
	}
}
