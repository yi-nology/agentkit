package fence

import "strings"

// EscapeUntrusted 中和不可信文本（LLM 产出、用户输入）中的 markdown 结构与
// HTML 注释边界，防止攻击者借 diff/PR 描述注入伪造报告标题、列表条目等。
// 与 Data（数据区围栏）同属本包的注入卫生面：Data 圈住不可信内容的边界、
// EscapeUntrusted 中和不可信内容自身的 markdown 语义。
// 策略：HTML 注释开/闭序列实体化；行首标题/引用/代码围栏/列表标记/水平线/
// 表格行/引用定义前插零宽空格；反引号替换为同类引号防打断代码段。
// 前提：下游渲染器仍需自行 sanitize 裸 HTML（本包不处理 <img>/<script> 等标签）。
func EscapeUntrusted(s string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, "<!--", "&lt;!--")
	s = strings.ReplaceAll(s, "-->", "--&gt;")
	s = strings.ReplaceAll(s, "`", "′")
	var b strings.Builder
	b.Grow(len(s))
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		trimmed := strings.TrimLeft(line, " \t")
		inject := false
		switch {
		case strings.HasPrefix(trimmed, "#"), strings.HasPrefix(trimmed, ">"), strings.HasPrefix(trimmed, "~~~"):
			inject = true
		case len(trimmed) >= 2 && strings.Trim(trimmed, "=-") == "":
			inject = true
		case len(trimmed) >= 3 && strings.Trim(trimmed, "*_- ") == "":
			// 水平线 *** / ___ / * * *（--- 已由上一条覆盖）
			inject = true
		case strings.HasPrefix(trimmed, "|"):
			// 表格行（| --- | 伪造"汇总表"是常见注入形态）
			inject = true
		case strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "]:"):
			// 引用定义 [ref]: url，可劫持后文 [text][ref] 的渲染
			inject = true
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "+ "), strings.HasPrefix(trimmed, "* "):
			inject = true
		case isOrderedMarker(trimmed):
			inject = true
		}
		if inject {
			b.WriteString("&#8203;")
		}
		b.WriteString(line)
	}
	return b.String()
}

// isOrderedMarker 行首是否为有序列表标记（"12. xxx"）。
func isOrderedMarker(trimmed string) bool {
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(trimmed) && trimmed[i] == '.' &&
		(trimmed[i+1] == ' ' || trimmed[i+1] == '\t') // CommonMark 允许 tab
}
