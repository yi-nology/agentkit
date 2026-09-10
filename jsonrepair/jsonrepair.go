// Package jsonrepair LLM 输出宽容 JSON 修复骨架：栅栏剥离 → 散文抽对象 →
// 语法级修复 → 标量归一。报告/领域 schema 由调用方持有；本包只做格式宽容。
package jsonrepair

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	fenceRe         = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
	invalidEscapeRe = regexp.MustCompile(`\\([^"\\/bfnrtu])`)
	trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)
)

// StripFence 剥离 ```json 栅栏；无栅栏原样 TrimSpace 返回。
func StripFence(s string) string {
	trimmed := strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(trimmed); m != nil {
		return strings.TrimSpace(m[1])
	}
	return trimmed
}

// ExtractObject 提取文本中第一个平衡的 JSON 对象（字符串字面量感知；
// 未闭合则返回剩余文本由 Repair 补全括号）。无对象返回 ""。
func ExtractObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	// 未闭合（截断）：剪掉最后一个 '}' 之后的垃圾，剩余缺口交 Repair 补全。
	tail := s[start:]
	if i := strings.LastIndex(tail, "}"); i >= 0 {
		tail = tail[:i+1]
	}
	return tail
}

// Repair 轻量 JSON 修复：全角结构标点、非法转义（如 \| → |）、尾逗号、未闭合括号。
// 只处理语法级损坏；语义归一化由 Normalize / 调用方承担。
func Repair(s string) string {
	s = fixFullWidth(s)
	s = invalidEscapeRe.ReplaceAllString(s, "$1")
	s = trailingCommaRe.ReplaceAllString(s, "$1")
	var stack []byte
	inStr, esc := false, false
	for _, c := range s {
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case !inStr && (c == '{' || c == '['):
			stack = append(stack, byte(c))
		case !inStr && (c == '}' || c == ']'):
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			s += "}"
		} else {
			s += "]"
		}
	}
	return s
}

// fixFullWidth 结构位全角标点修复（字符串感知）：JSON 结构位置的 ，： 替换为 ASCII；
// 字符串字面量内部原样保留。
func fixFullWidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inStr, esc := false, false
	for _, r := range s {
		if inStr {
			b.WriteRune(r)
			switch {
			case esc:
				esc = false
			case r == '\\':
				esc = true
			case r == '"':
				inStr = false
			}
			continue
		}
		switch r {
		case '"':
			inStr = true
			b.WriteRune(r)
		case '，':
			b.WriteByte(',')
		case '：':
			b.WriteByte(':')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Schema 标量归一化作用范围（领域键集合由调用方提供）。
type Schema struct {
	// StringKeys 期望为字符串的键：bool/数字 → 文本；对象/数组 → 扁平化文本。
	StringKeys map[string]bool
	// ListKeys 期望为数组的键：标量 → 单元素数组（或空数组）；元素内标量 → 文本。
	ListKeys map[string]bool
	// OnMap 每个 map 在其子节点归一完成后的领域钩子（可 nil）。可增删改键。
	OnMap func(m map[string]any)
}

// Normalize 把字符串位的 bool/数字统一转成文本（宽容留给格式，拦截留给白名单）。
// raw 非法 JSON 时返回 error。
func Normalize(raw json.RawMessage, schema *Schema) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	if schema == nil {
		schema = &Schema{}
	}
	walkNormalize(m, schema)
	out, err := json.Marshal(m)
	if err != nil {
		return string(raw), nil
	}
	return string(out), nil
}

func walkNormalize(m map[string]any, schema *Schema) {
	for k, v := range m {
		switch {
		case schema.StringKeys[k]:
			if s, ok := coerceString(v); ok {
				m[k] = s
			} else if flat, ok := FlattenObject(v); ok {
				m[k] = flat
			} else if flat, ok := FlattenList(v); ok {
				m[k] = flat
			}
		case schema.ListKeys[k]:
			if _, isArr := v.([]any); !isArr {
				if s, ok := coerceString(v); ok {
					if s != "" {
						m[k] = []any{s}
					} else {
						m[k] = []any{}
					}
				}
				continue
			}
			if arr, ok := v.([]any); ok {
				for i, item := range arr {
					if s, ok := coerceString(item); ok {
						arr[i] = s
					}
				}
			}
		}
		// 子节点先归一，OnMap 才能看到已规范化的嵌套结构。
		if sub, ok := m[k].(map[string]any); ok {
			walkNormalize(sub, schema)
		}
		if arr, ok := m[k].([]any); ok {
			for _, item := range arr {
				if sub, ok := item.(map[string]any); ok {
					walkNormalize(sub, schema)
				}
			}
		}
	}
	if schema.OnMap != nil {
		schema.OnMap(m)
	}
}

// ParseLenient 栅栏剥离 → 直接解析 → Repair 重试 → ExtractObject+Repair 重试。
func ParseLenient(raw string, v any, schema *Schema) error {
	candidate := StripFence(raw)
	if candidate == "" {
		return fmt.Errorf("jsonrepair: 空输入")
	}
	tryParse := func(c string) bool {
		normalized, nerr := Normalize(json.RawMessage(c), schema)
		if nerr == nil {
			c = normalized
		}
		return json.Unmarshal([]byte(c), v) == nil
	}
	if tryParse(candidate) {
		return nil
	}
	if repaired := Repair(candidate); repaired != candidate && tryParse(repaired) {
		return nil
	}
	if extracted := ExtractObject(candidate); extracted != "" {
		if tryParse(extracted) {
			return nil
		}
		if tryParse(Repair(extracted)) {
			return nil
		}
	}
	return fmt.Errorf("jsonrepair: 无法解析为 JSON")
}

// CoerceString bool/数字 → 文本；字符串原样；其余（nil/对象/数组）不处理。
func CoerceString(v any) (string, bool) {
	return coerceString(v)
}

func coerceString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return fmt.Sprintf("%t", t), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	}
	return "", false
}

// FlattenObject 对象形态 → 按 key 字典序取标量值拼为「v1；v2」文本。全空对象返回 false。
func FlattenObject(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if s, ok := coerceString(m[k]); ok && strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "；"), true
}

// FlattenList 数组形态 → 「1. x；2. y」序号拼接。结构化条目取 title/detail。
func FlattenList(v any) (string, bool) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(arr))
	for _, item := range arr {
		var s string
		if m, isMap := item.(map[string]any); isMap {
			title, _ := coerceString(m["title"])
			detail, _ := coerceString(m["detail"])
			switch {
			case title != "" && detail != "":
				s = title + "：" + detail
			case title != "":
				s = title
			case detail != "":
				s = detail
			default:
				s, _ = FlattenObject(m)
			}
		} else {
			s, _ = coerceString(item)
		}
		if strings.TrimSpace(s) != "" {
			parts = append(parts, fmt.Sprintf("%d. %s", len(parts)+1, s))
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "；"), true
}
