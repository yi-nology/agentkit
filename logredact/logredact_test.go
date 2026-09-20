package logredact

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []struct{ in, want, mustNotContain string }{
		{
			in:             "NATS 已连接: nats://ops:s3cret@10.0.0.5:4222",
			want:           "nats://ops:****@10.0.0.5:4222",
			mustNotContain: "s3cret",
		},
		{
			in:             "dsn=postgres://user:pass@127.0.0.1:5432/db?sslmode=disable",
			want:           "postgres://user:****@127.0.0.1:5432/db?sslmode=disable",
			mustNotContain: ":pass@",
		},
		{
			in:             "webhook https://example.com/send?key=ABC123-xyz sent",
			want:           "key=****",
			mustNotContain: "ABC123",
		},
		{
			in:             "auth=Bearer eyJhbGciOiJIUzI1NiJ9.e30.sig",
			want:           "Bearer ****",
			mustNotContain: "eyJhbGciOiJIUzI1NiJ9",
		},
		{
			in:             "password=hunter2 user=bob",
			want:           "password=**** user=bob",
			mustNotContain: "hunter2",
		},
		{
			// 凭证词键值对容忍 ": " 空格形态（kubeconfig/私钥 YAML 日志常见）
			in:             "private-key: MIIEvQIBADANBgkq",
			want:           "private-key=****",
			mustNotContain: "MIIEvQ",
		},
		{
			in:             "kubeconfig = /home/u/.kube/config with secret-key: abc123",
			want:           "secret-key=****",
			mustNotContain: "abc123",
		},
		{
			// PEM 私钥整段打码
			in:             "-----BEGIN RSA PRIVATE KEY-----\nMIIEow...\nasdf\n-----END RSA PRIVATE KEY-----",
			want:           "----PRIVATE KEY ****",
			mustNotContain: "MIIEow",
		},
		{
			// 散文中的普通键值不受凭证词表误伤
			in:   "user-keynote: 演讲主题",
			want: "user-keynote: 演讲主题",
		},
	}
	for _, c := range cases {
		got := Redact(c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("Redact(%q) = %q, 期望含 %q", c.in, got, c.want)
		}
		if c.mustNotContain != "" && strings.Contains(got, c.mustNotContain) {
			t.Errorf("Redact(%q) = %q, 泄漏 %q", c.in, got, c.mustNotContain)
		}
	}

	for _, plain := range []string{
		"会话 sess-0908-abc succeeded",
		"知识库已索引: dir=knowledge",
		"地址 http://127.0.0.1:8900",
	} {
		if got := Redact(plain); got != plain {
			t.Errorf("无凭据文本不应改动: %q → %q", plain, got)
		}
	}
}

func TestRedactValue(t *testing.T) {
	in := map[string]any{
		"dsn":    "postgres://u:p@h/db",
		"nested": map[string]any{"auth": "Bearer tok.en.sig"},
		"list":   []any{"token=abc", 42},
		"ok":     "safe",
	}
	out, _ := RedactValue(in).(map[string]any)
	if s, _ := out["dsn"].(string); strings.Contains(s, ":p@") {
		t.Errorf("dsn 未脱敏: %q", s)
	}
	nested, _ := out["nested"].(map[string]any)
	if s, _ := nested["auth"].(string); strings.Contains(s, "tok.en") {
		t.Errorf("nested auth 未脱敏: %q", s)
	}
	list, _ := out["list"].([]any)
	if s, _ := list[0].(string); strings.Contains(s, "abc") {
		t.Errorf("list[0] 未脱敏: %q", s)
	}
	if list[1] != 42 {
		t.Errorf("非字符串应原样保留: %v", list[1])
	}
	if out["ok"] != "safe" {
		t.Errorf("无凭据字段不应改动: %v", out["ok"])
	}
}

func TestRedactSecrets(t *testing.T) {
	// 精确串抹除（高敏感已知名单——与模式化 Redact 互补，与 Masker 相反不回填）。
	got := RedactSecrets("clone https://oauth2:tok123@host/r.git 失败: tok123", "tok123")
	if strings.Contains(got, "tok123") {
		t.Fatalf("秘密未抹除: %s", got)
	}
	if !strings.Contains(got, "oauth2:***@") {
		t.Fatalf("URL 形态应保持可读: %s", got)
	}
	// 长秘密优先：短串是长串前缀时，先替短串会留下尾段泄漏。
	got = RedactSecrets("k=abcdef k2=abc", "abc", "abcdef")
	if strings.Contains(got, "def") || strings.Contains(got, "abc") {
		t.Fatalf("长串优先替换防尾段泄漏: %s", got)
	}
	// 空串忽略、无命中原样返回。
	if got := RedactSecrets("plain", "", "nohit"); got != "plain" {
		t.Fatalf("无命中应原样返回: %s", got)
	}
}

// 平台 API token 特征前缀（裸形态）打码——2026-09 review-service 收敛评估反哺：
// 旧规则只覆盖键值对/连接串形态，裸 token 散文形态（sk-/ghp_/JWT）会泄漏。
func TestRedactProviderTokenPrefixes(t *testing.T) {
	cases := []struct{ name, in, mustNotContain string }{
		{"OpenAI 风格", "key is sk-abc123def456ghi789jklmnop", "sk-abc123"},
		{"Anthropic 风格", "sk-ant-api03-abcdef1234567890abcdef", "sk-ant-api03"},
		{"GitHub PAT", "push with ghp_0123456789abcdefghij", "ghp_0123456789"},
		{"Slack", "xoxb-123456789012-abcdef", "xoxb-123456789012"},
		{"AWS AKIA", "aws key AKIAIOSFODNN7EXAMPLE in log", "AKIAIOSFODNN7EXAMPLE"},
		{"JWT", "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJVadQssw5c leaked", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Redact(c.in)
			if strings.Contains(got, c.mustNotContain) {
				t.Fatalf("泄漏未打码: %q -> %q", c.in, got)
			}
		})
	}
}

func TestRedactKeepsNormalText(t *testing.T) {
	// 防误伤：普通文本/路径/短词不受新规则影响。
	normal := "task done in 1.25s, see design.md and http://example.com/docs"
	if got := Redact(normal); got != normal {
		t.Fatalf("普通文本被误改: %q -> %q", normal, got)
	}
}
