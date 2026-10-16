// Package logredact 日志/审计载荷凭据脱敏：连接串/URL/头中的凭据在落日志前模式化打码。
// 与 fence（Markdown/HTML 注入卫生）正交——本包打码凭据。
package logredact

import (
	"regexp"
	"sort"
	"strings"
)

type rule struct {
	re   *regexp.Regexp
	repl string
}

var rules = []rule{
	// URL 内嵌凭据：nats://user:pass@host、postgres://user:pw@db…（scheme 不区分大小写）
	{regexp.MustCompile(`(?i)\b(nats|postgres|postgresql|mysql|mongodb\+srv|mongodb|http|https|redis|amqps|amqp)://([^\s:@/]+):([^\s@/]+)@`), "$1://$2:****@"},
	// 键值对：key=xxx / …webhook/send?key=xxx（值截到 & 或空白）。
	// key 单独留紧邻形态（[=:] 后无空白）——"user-keynote" 类散文防误伤
	{regexp.MustCompile(`(?i)\bkey[=:]["']?([^\s&'"]+)`), "key=****"},
	// 凭证词键值对（容忍 ": " 空格形态——YAML/缩进 JSON 日志最高频的
	// `password: xxx` 形态；第九轮审计：password/token 此前漏在紧邻词表，
	// 冒号后带空格即整体漏网）
	{regexp.MustCompile(`(?i)([A-Za-z0-9]*(?:[-_][A-Za-z0-9]*)*(?:token|secret|password|passwd|passphrase|api_?key|access_?key|private[-_]?key|secret[-_]?key|client[-_]?key|kubeconfig))["']?\s*[=:]\s*(?:"[^"\n&]*"|'[^'\n&]*'|[^\s&'"]+)`), "$1=****"},
	// PEM 私钥整段打码（载荷/日志防泄漏的最后兜底）；第二条为截断兜底——
	// 日志超长截断落在 PEM 中段时无 END，私钥体无前缀特征会原文泄漏
	{regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), "----PRIVATE KEY ****"},
	{regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*$`), "----PRIVATE KEY ****（截断）"},
	// Authorization 头：值吃到行尾——第九轮审计 C 级修复：\S+ 只吃 scheme
	// 名（Basic/Bearer），凭据本体残留且后续 Bearer 规则失去锚点兜不住
	{regexp.MustCompile(`(?i)(authorization|www-authenticate)\s*[:=]\s*[^\r\n]*`), "$1=****"},
	{regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]+`), "Bearer ****"},
	// 平台 API token 特征前缀（裸 token 不带 key= 形态出现在散文/URL 路径时的兜底；
	// 键值对形态已由上方规则先行打码，本组只负责无键形态）。
	{regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9_-]{16,}|sk-(?:proj|svcacct|None)-[A-Za-z0-9_-]{20,}|sk-[A-Za-z0-9]{20,}|ghp_[A-Za-z0-9]{20,}|gho_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})\b`), "****"},
	// JWT（三段式， eyJ 头 + 两段 base64url）：出现在日志散文里的裸 JWT。
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), "****JWT****"},
}

// Redact 返回打码后的文本（无命中原样返回）。
func Redact(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// RedactSecrets 抹除调用方已知的确切秘密（clone URL 内嵌 token、动态签发的临时
// 凭据等）：与模式化 Redact 互补——Redact 靠规则打码未知形态的凭据，本函数抹除
// 已知值。与 Masker 相反，高敏感秘密不做令牌、不回填（Masker 只收低敏感拓扑标识）。
// 长秘密优先替换（防短串是长串前缀时留下尾段泄漏），空串忽略。
func RedactSecrets(s string, secrets ...string) string {
	sorted := append([]string(nil), secrets...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, sec := range sorted {
		if sec == "" {
			continue
		}
		s = strings.ReplaceAll(s, sec, "***")
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
