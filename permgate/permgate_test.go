package permgate

import (
	"context"
	"testing"
)

var ctx = context.Background()

func TestPriorityChain(t *testing.T) {
	s := &Service{
		DisallowedTools: map[string]bool{"danger": true},
		AllowedTools:    map[string]bool{"safe": true},
		ProjectRules: Ruleset{
			Deny:  []Rule{{ToolName: "bash", Content: "rm -rf:*"}},
			Ask:   []Rule{{ToolName: "bash", Content: "git push:*"}},
			Allow: []Rule{{ToolName: "webfetch", Content: "https://docs.example.com/*"}},
		},
	}
	cases := []struct {
		name string
		tool string
		in   any
		mode Mode
		cap  Capability
		want Behavior
		rule string
	}{
		{"交互型工具", "ask_user", nil, ModeDefault, Capability{RequiresUserInteraction: true}, Ask, "tool.userInteraction"},
		{"交互型被硬禁", "danger", nil, ModeDefault, Capability{RequiresUserInteraction: true}, Deny, "rule.disallowedTools"},
		{"alwaysAsk 压过 yolo", "deploy", nil, ModeYolo, Capability{AlwaysAsk: true}, Ask, "tool.alwaysAsk"},
		{"alwaysAsk 被项目 deny 压过", "bash", "rm -rf /tmp", ModeDefault, Capability{AlwaysAsk: true}, Deny, "rule.project.deny"},
		{"alwaysAsk 未授权", "deploy", nil, ModeDefault, Capability{AlwaysAsk: true}, Ask, "tool.alwaysAsk"},
		{"yolo 直通", "bash", "ls -la", ModeYolo, Capability{}, Allow, "mode.yolo"},
		{"yolo 压不过硬禁", "danger", nil, ModeYolo, Capability{}, Deny, "rule.disallowedTools"},
		{"项目 deny", "bash", "rm -rf /tmp", ModeDefault, Capability{}, Deny, "rule.project.deny"},
		{"项目 ask", "bash", "git push origin", ModeDefault, Capability{}, Ask, "rule.project.ask"},
		{"只读模式放行只读", "read", nil, ModeReadOnly, Capability{ReadOnly: true}, Allow, "mode.read_only"},
		{"只读模式拦写入", "write", map[string]any{"file_path": "a"}, ModeReadOnly, Capability{}, Deny, "mode.read_only"},
		{"项目 allow", "webfetch", map[string]any{"url": "https://docs.example.com/a"}, ModeDefault, Capability{}, Allow, "rule.project.allow"},
	}
	for _, tc := range cases {
		got := s.Check(ctx, tc.tool, tc.in, tc.mode, tc.cap)
		if got.Behavior != tc.want || got.RuleID != tc.rule {
			t.Errorf("%s: got=%s/%s want=%s/%s", tc.name, got.Behavior, got.RuleID, tc.want, tc.rule)
		}
	}
	// 逐个精确断言关键档位
	if d := s.Check(ctx, "bash", "git status", ModeDefault, Capability{}); d.Behavior != Ask || d.RuleID != "mode.default" {
		t.Fatalf("git status 应落兜底 ask: %+v", d)
	}
	if d := s.Check(ctx, "bash", "rm -rf /tmp", ModeYolo, Capability{}); d.Behavior != Deny || d.RuleID != "rule.project.deny" {
		t.Fatalf("yolo 在项目 deny 上仍应拦（且黑名单/项目拒绝先于 yolo）: %+v", d)
	}
}

func TestSessionGrantLifecycle(t *testing.T) {
	s := &Service{}
	cap := Capability{AlwaysAsk: true}
	if d := s.Check(ctx, "deploy", nil, ModeDefault, cap); d.Behavior != Ask {
		t.Fatalf("未授权应 ask: %+v", d)
	}
	s.AllowForSession("deploy", "")
	if d := s.Check(ctx, "deploy", nil, ModeDefault, cap); d.Behavior != Allow || d.RuleID != "rule.session.allow" {
		t.Fatalf("会话授权应放行: %+v", d)
	}
	// 内容限定的会话授权：别的命令仍 ask
	s.ResetSession()
	s.AllowForSession("bash", "git status:*")
	if d := s.Check(ctx, "bash", "git status", ModeDefault, Capability{}); d.Behavior != Allow {
		t.Fatalf("命中内容应放行: %+v", d)
	}
	if d := s.Check(ctx, "bash", "git push", ModeDefault, Capability{}); d.Behavior == Allow {
		t.Fatalf("未命中内容不应放行: %+v", d)
	}
}

func TestRuleContentMatching(t *testing.T) {
	cases := []struct {
		content string
		sub     string
		want    bool
	}{
		{"", "anything", true},                       // 空模式=全匹配
		{"git status", "git status", true},           // 精确
		{"git status", "git statusx", false},         // 不等
		{"git status:*", "git status", true},         // 前缀=本体
		{"git status:*", "git status --short", true}, // 前缀+空格
		{"git status:*", "git statusx", false},       // 前缀须边界
		{"npm run *", "npm run build", true},         // 通配
		{"npm run *", "npm test", false},             // 通配不中
		{"web/**/*.vue", "web/src/a.vue", true},      // 跨段通配
		{"git status", "", false},                    // 无匹配对象
	}
	for _, tc := range cases {
		subs := []string{tc.sub}
		if tc.sub == "" {
			subs = nil
		}
		if got := matchRuleContent(tc.content, subs); got != tc.want {
			t.Errorf("content=%q sub=%q: got %v want %v", tc.content, tc.sub, got, tc.want)
		}
	}
}

func TestSubjectsExtraction(t *testing.T) {
	if got := subjects("raw command", "x"); len(got) != 1 || got[0] != "raw command" {
		t.Fatalf("字符串输入: %v", got)
	}
	m := map[string]any{"command": "ls", "file_path": "a"}
	if got := subjects(m, "bash"); got[0] != "ls" {
		t.Fatalf("command 优先: %v", got)
	}
	m2 := map[string]any{"file_path": "/tmp/x"}
	if got := subjects(m2, "read"); got[0] != "/tmp/x" {
		t.Fatalf("file_path: %v", got)
	}
	if got := subjects(42, "x"); got != nil {
		t.Fatalf("未知形态: %v", got)
	}
}

func TestEditRuleMatchesWrite(t *testing.T) {
	s := &Service{ProjectRules: Ruleset{Allow: []Rule{{ToolName: "Edit", Content: "/src/*"}}}}
	if d := s.Check(ctx, "Write", map[string]any{"file_path": "/src/a.go"}, ModeDefault, Capability{}); d.Behavior != Allow {
		t.Fatalf("Edit 规则应匹配 Write: %+v", d)
	}
	if d := s.Check(ctx, "Write", map[string]any{"file_path": "/etc/passwd"}, ModeDefault, Capability{}); d.Behavior == Allow {
		t.Fatalf("内容不匹配: %+v", d)
	}
}

func TestPreapproveHook(t *testing.T) {
	s := &Service{Preapprove: func(ctx context.Context, tool string, input any) (string, bool) {
		if u, ok := input.(map[string]any)["url"].(string); ok && u == "https://docs.example.com" {
			return "tool.webfetch.preapproved", true
		}
		return "", false
	}}
	if d := s.Check(ctx, "webfetch", map[string]any{"url": "https://docs.example.com"}, ModeDefault, Capability{}); d.Behavior != Allow || d.RuleID != "tool.webfetch.preapproved" {
		t.Fatalf("预批应放行: %+v", d)
	}
	if d := s.Check(ctx, "webfetch", map[string]any{"url": "https://other.com"}, ModeDefault, Capability{}); d.Behavior != Ask {
		t.Fatalf("未预批应落兜底: %+v", d)
	}
	// 只读模式压过预批（写入类预批也不行——位次设计）
	s2 := &Service{Preapprove: s.Preapprove}
	if d := s2.Check(ctx, "webfetch", map[string]any{"url": "https://docs.example.com"}, ModeReadOnly, Capability{ReadOnly: true}); d.Behavior != Allow {
		t.Fatalf("只读模式+只读工具应放行: %+v", d)
	}
	if d := s2.Check(ctx, "webfetch", map[string]any{"url": "https://docs.example.com"}, ModeReadOnly, Capability{}); d.Behavior != Deny {
		t.Fatalf("只读模式压过预批（非只读工具拦下）: %+v", d)
	}
}
