// Package jsonrepair LLM 输出宽容 JSON 修复骨架：栅栏剥离 → 散文抽对象 →
// 语法级修复 → 标量归一。报告/领域 schema 由调用方持有；本包只做格式宽容。
package jsonrepair

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/yi-nology/agentkit/textutil"
)

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

// Repair 轻量 JSON 修复：全角结构标点、非法转义（如 \| → |）、尾逗号、未闭合
// 括号。只处理语法级损坏；语义归一化由 Normalize / 调用方承担。
//
// 单遍字符串感知扫描（第六轮审计重构）：此前尾逗号/非法转义走全文正则、不辨
// 字符串边界——字符串里合法的 `", }"` / `"\x"` 序列会被当损坏静默改写（修复器
// 的错误成功比失败更贵）。现在三类修复共用一个 inStr/esc 状态机，与
// scanOpenBrackets/ExtractObject 同一纪律：字符串外才动结构位，字符串内只修
// 转义。
func Repair(s string) string {
	var b bytes.Buffer
	b.Grow(len(s))
	inStr, esc := false, false
	// truncAt 尾逗号候选段起点（逗号 + 其后空白）：确认紧跟 '}'/']' 才回退删除。
	truncAt := -1
	for _, r := range s {
		if inStr {
			if esc {
				esc = false
				if validEscape(r) {
					b.WriteRune('\\')
				}
				b.WriteRune(r) // 非法转义：丢反斜杠保字符（\| → |）
				continue
			}
			if r == '\\' {
				esc = true // 反斜杠的写入延到下一 rune 判定合法性
				continue
			}
			if r == '"' {
				inStr = false
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '"':
			inStr = true
			truncAt = -1 // 值开始，逗号不再是尾逗号
			b.WriteRune(r)
		case '，': // 全角结构标点（字符串外）→ ASCII；逗号语义同 ASCII
			truncAt = b.Len() // 转出的 , 同样是尾逗号候选（旧正则靠后跑补此环）
			b.WriteByte(',')
		case '：':
			truncAt = -1
			b.WriteByte(':')
		case ',':
			truncAt = b.Len()
			b.WriteRune(r)
		case '}', ']':
			if truncAt >= 0 {
				b.Truncate(truncAt) // 尾逗号（含其后空白）删除
				truncAt = -1
			}
			b.WriteRune(r)
		default:
			switch r {
			case ' ', '\t', '\n', '\r':
				// 空白不重置尾逗号候选（", }" 的空格段）
			default:
				truncAt = -1
			}
			b.WriteRune(r)
		}
	}
	repaired := b.String()
	return closeUnclosed(repaired, scanOpenBrackets(repaired))
}

// validEscape JSON 合法转义字符（\" \\ \/ \b \f \n \r \t \uXXXX）。
func validEscape(r rune) bool {
	switch r {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
		return true
	}
	return false
}

// scanOpenBrackets 括号栈扫描（字符串字面量感知），返回未闭合的开括号序列。
func scanOpenBrackets(s string) []byte {
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
	return stack
}

// closeUnclosed 按栈序补全未闭合括号（后开先闭）。
func closeUnclosed(s string, stack []byte) string {
	if len(stack) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(stack))
	b.WriteString(s)
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	}
	return b.String()
}

// fixFullWidth 的全角标点修复已并入 Repair 的单遍扫描（字符串外 ，： → ASCII；
// 字符串内原样保留）。

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
			} else if flat, ok := flattenObject(v); ok {
				m[k] = flat
			} else if flat, ok := flattenList(v); ok {
				m[k] = flat
			}
		case schema.ListKeys[k]:
			if arr, isArr := v.([]any); isArr {
				for i, item := range arr {
					if s, ok := coerceString(item); ok {
						arr[i] = s
					}
				}
			} else if s, ok := coerceString(v); ok {
				// 标量 → 单元素数组（schema 声明 array 而模型给单值的高频形态）
				if s != "" {
					m[k] = []any{s}
				} else {
					m[k] = []any{}
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
// schema 为 nil 时跳过 Normalize（空 schema 下归一仍是 Unmarshal+递归+Marshal
// 三趟纯开销，对解析结果无贡献）。
func ParseLenient(raw string, v any, schema *Schema) error {
	candidate := textutil.StripCodeFence(raw)
	if candidate == "" {
		return fmt.Errorf("jsonrepair: 空输入")
	}
	tryParse := func(c string) bool {
		if schema != nil {
			normalized, nerr := Normalize(json.RawMessage(c), schema)
			if nerr == nil {
				c = normalized
			}
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

// coerceString bool/数字 → 文本；字符串原样；其余（nil/对象/数组）不处理。
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

// flattenObject 对象形态 → 按 key 字典序取标量值拼为「v1；v2」文本。全空对象返回 false。
func flattenObject(v any) (string, bool) {
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

// flattenList 数组形态 → 「1. x；2. y」序号拼接。结构化条目取 title/detail。
func flattenList(v any) (string, bool) {
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
				s, _ = flattenObject(m)
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
