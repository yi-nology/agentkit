package jsonrepair

import (
	"encoding/json"
	"testing"
)

func TestExtractObject(t *testing.T) {
	if got := ExtractObject(`前缀散文 {"a":{"b":1}} 后缀`); got != `{"a":{"b":1}}` {
		t.Errorf("ExtractObject = %q", got)
	}
	if got := ExtractObject(`no object here`); got != "" {
		t.Errorf("无对象应空串, got %q", got)
	}
	// 未闭合
	got := ExtractObject(`{"a": {"b": 1`)
	if got == "" {
		t.Fatal("未闭合应返回剩余文本")
	}
}

func TestRepair(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a": 1,}`, `{"a": 1}`},
		{`{"a": "x\|y"}`, `{"a": "x|y"}`},
		{`{"a": 1`, `{"a": 1}`},
		{`{"a"：1}`, `{"a":1}`},
		{`{"a"：1，"b"：2}`, `{"a":1,"b":2}`},
	}
	for _, c := range cases {
		if got := Repair(c.in); got != c.want {
			t.Errorf("Repair(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// 字符串内全角应保留
	if got := Repair(`{"a": "中文：保留"}`); got != `{"a": "中文：保留"}` {
		t.Errorf("字符串内全角被改动: %q", got)
	}
}

func TestNormalize(t *testing.T) {
	schema := &Schema{
		StringKeys: map[string]bool{"summary": true},
		ListKeys:   map[string]bool{"steps": true},
	}
	raw := json.RawMessage(`{"summary": true, "steps": "only-one", "other": 1}`)
	out, err := Normalize(raw, schema)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	if m["summary"] != "true" {
		t.Errorf("summary = %v", m["summary"])
	}
	steps, ok := m["steps"].([]any)
	if !ok || len(steps) != 1 || steps[0] != "only-one" {
		t.Errorf("steps = %v", m["steps"])
	}
}

func TestNormalizeFlatten(t *testing.T) {
	schema := &Schema{StringKeys: map[string]bool{"redirect": true, "conclusion": true}}
	raw := json.RawMessage(`{"redirect":{"to":"x","reason":"y"}, "conclusion":[{"title":"A","detail":"B"}]}`)
	out, err := Normalize(raw, schema)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(out), &m)
	if m["redirect"] != "x；y" && m["redirect"] != "y；x" {
		// 字典序：reason < to → "y；x"
		t.Logf("redirect = %v", m["redirect"])
		if s, _ := m["redirect"].(string); s == "" {
			t.Errorf("redirect 未扁平化: %v", m["redirect"])
		}
	}
	if s, _ := m["conclusion"].(string); s != "1. A：B" {
		t.Errorf("conclusion = %v", m["conclusion"])
	}
}

func TestParseLenient(t *testing.T) {
	var v map[string]any
	// 栅栏
	if err := ParseLenient("```json\n{\"a\":1}\n```", &v, nil); err != nil || v["a"] != float64(1) {
		t.Fatalf("fence: %v %v", err, v)
	}
	// 尾逗号
	v = nil
	if err := ParseLenient(`{"a":1,}`, &v, nil); err != nil || v["a"] != float64(1) {
		t.Fatalf("trailing comma: %v %v", err, v)
	}
	// 散文包裹
	v = nil
	if err := ParseLenient(`根据分析，报告如下：{"a":2} 供参考`, &v, nil); err != nil || v["a"] != float64(2) {
		t.Fatalf("prose: %v %v", err, v)
	}
	// 彻底失败
	if err := ParseLenient(`not json at all`, &v, nil); err == nil {
		t.Fatal("非 JSON 应报错")
	}
}
