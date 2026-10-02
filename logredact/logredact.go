// Package logredact 日志/审计载荷凭据脱敏：连接串/URL/头中的凭据在落日志前模式化打码。
// 与 safejson 正交——safejson 防 Markdown/HTML 注入，本包打码凭据。
package logredact

import "regexp"

type rule struct {
	re   *regexp.Regexp
	repl string
}

var rules = []rule{
	// URL 内嵌凭据：nats://user:pass@host、postgres://user:pw@db…（scheme 不区分大小写）
	{regexp.MustCompile(`(?i)\b(nats|postgres|postgresql|mysql|mongodb|http|https|redis|amqp)://([^\s:@/]+):([^\s@/]+)@`), "$1://$2:****@"},
	// 键值对：token=xxx / api_key:xxx / …webhook/send?key=xxx（值截到 & 或空白）
	{regexp.MustCompile(`(?i)\b(token|secret|password|passwd|api_?key|access_?key|key)[=:]["']?([^\s&'"]+)`), "$1=****"},
	// Authorization 头
	{regexp.MustCompile(`(?i)(authorization|www-authenticate)\s*[:=]\s*\S+`), "$1=****"},
	{regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]+`), "Bearer ****"},
}

// Redact 返回打码后的文本（无命中原样返回）。
func Redact(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// RedactValue 递归脱敏任意 JSON 形态值：map/slice 逐字段下探，字符串过 Redact，其余原样。
// 事件载荷入库前的统一出口——tool_call 参数与 tool_result 文本常含连接串、webhook key、Bearer 头。
func RedactValue(v any) any {
	switch t := v.(type) {
	case string:
		return Redact(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = RedactValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = RedactValue(vv)
		}
		return out
	default:
		return v
	}
}
