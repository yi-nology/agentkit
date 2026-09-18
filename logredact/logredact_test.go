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
