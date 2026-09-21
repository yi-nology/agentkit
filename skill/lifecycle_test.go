package skill

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// mf 构造 MapFS 文件项。
func mf(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// lifecycleFS 校验/写回夹具：_shared 技能（frozen 缺省）+ 包内技能（契约字段齐全）。
func lifecycleFS() fstest.MapFS {
	return fstest.MapFS{
		"_shared/skills/sop/SKILL.md":          mf("---\nname: SOP\ndescription: 通用诊断流程\n---\n\n流程正文"),
		"os-basics/skills/net-detect/SKILL.md": mf("---\nname: 网络检测\ndescription: 网络攻击检测方法\nmode: on_demand\nversion: 1.2.0\nmaturity: stable\nrequires_mcp:\n  - server: ask-ops\n    tools: [get_logs]\n---\n\n检测方法论正文"),
	}
}

// TestLoadValidateFailFast mode/maturity 枚举、SemVer、弃用窗口、requires_mcp 非空——全部加载期拦截。
func TestLoadValidateFailFast(t *testing.T) {
	bad := []fstest.MapFS{
		{"p/skills/s1/SKILL.md": mf("---\nname: x\nmode: magic\n---\n正文")},
		{"p/skills/s1/SKILL.md": mf("---\nname: x\nmaturity: semi\n---\n正文")},
		{"p/skills/s1/SKILL.md": mf("---\nname: x\nversion: 1.x\n---\n正文")},
		{"p/skills/s1/SKILL.md": mf("---\nname: x\nmaturity: deprecated\n---\n正文")},
		{"p/skills/s1/SKILL.md": mf("---\nname: x\nmaturity: deprecated\ndeprecated:\n  remove_after: tomorrow\n---\n正文")},
		{"p/skills/s1/SKILL.md": mf("---\nname: x\nrequires_mcp:\n  - tools: [a]\n---\n正文")},
		{"p/skills/s1/SKILL.md": mf("---\nname: [unclosed\n---\n正文")},
	}
	for i, fs := range bad {
		if _, err := LoadFromFS(fs); err == nil {
			t.Fatalf("夹具 %d 应 fail-fast", i)
		}
	}
	// 错误带文件定位。
	_, err := LoadFromFS(bad[0])
	if err != nil && !strings.Contains(err.Error(), "p/skills/s1/SKILL.md") {
		t.Fatalf("错误应带路径定位: %v", err)
	}
}

// TestLoadContractDefaults 缺省值（_shared=frozen / 包内=experimental / 0.0.0 / static）
// 与契约字段（requires_mcp.tools / provides）解析。
func TestLoadContractDefaults(t *testing.T) {
	lib, err := LoadFromFS(lifecycleFS())
	if err != nil {
		t.Fatal(err)
	}
	if m := lib.Describe("sop"); m.Maturity != MaturityFrozen || m.Mode != ModeStatic || m.Version != DefaultVersion {
		t.Fatalf("sop 缺省异常: %+v", m)
	}
	m := lib.Describe("net-detect")
	if m.Maturity != MaturityStable || m.Version != "1.2.0" || m.Mode != ModeOnDemand {
		t.Fatalf("net-detect 元数据异常: %+v", m)
	}
	if len(m.RequiresMCP) != 1 || m.RequiresMCP[0].Server != "ask-ops" ||
		len(m.RequiresMCP[0].Tools) != 1 || m.RequiresMCP[0].Tools[0] != "get_logs" {
		t.Fatalf("requires_mcp 解析异常: %+v", m.RequiresMCP)
	}
	if got := lib.Describe("sop").Source; got != "_shared" {
		t.Fatalf("source=%q", got)
	}
}

// TestDeprecatedExpiredInUse 过期且被引用 → 失败清单；未过期/无引用不进。
func TestDeprecatedExpiredInUse(t *testing.T) {
	fsys := fstest.MapFS{"p/skills/s1/SKILL.md": mf("---\nname: s1\nmaturity: deprecated\ndeprecated:\n  replaced_by: s2\n  remove_after: 2099-01-01\n---\n正文")}
	lib, err := LoadFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if m := lib.Describe("s1"); m.DeprecationExpired(time.Now()) || m.Deprecated.ReplacedBy != "s2" {
		t.Fatalf("2099 窗口不应过期/replaced_by 丢失: %+v", m)
	}
	if got := lib.DeprecatedExpiredInUse(map[string][]string{"s1": {"a"}}, time.Now()); len(got) != 0 {
		t.Fatalf("未过期不应进失败清单: %v", got)
	}
	past := fstest.MapFS{"p/skills/s1/SKILL.md": mf("---\nname: s1\nmaturity: deprecated\ndeprecated:\n  remove_after: 2020-01-01\n---\n正文")}
	lib2, _ := LoadFromFS(past)
	if got := lib2.DeprecatedExpiredInUse(map[string][]string{"s1": {"a"}}, time.Now()); len(got) != 1 {
		t.Fatalf("过期且被引用应进失败清单: %v", got)
	}
	if got := lib2.DeprecatedExpiredInUse(map[string][]string{}, time.Now()); len(got) != 0 {
		t.Fatalf("无引用不应失败: %v", got)
	}
}

// TestRewriteMode 结构化 mode 写回保留嵌套字段（requires_mcp.tools/min_version、provides）与正文。
func TestRewriteMode(t *testing.T) {
	src := strings.Join([]string{
		"---", "name: ssh-check", "description: 检测", "mode: static",
		"version: 1.1.0", "maturity: stable", "provides: [ssh-attack-detection, login-audit]",
		"requires_mcp:", "  - server: security-assistant", "    min_version: \"1.2.0\"", "    tools: [collect_security_logs]",
		"---", "", "正文段",
	}, "\n")
	out, err := RewriteMode(src, ModeOnDemand)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := LoadFromFS(fstest.MapFS{"p/skills/ssh-check/SKILL.md": mf(out)})
	if err != nil {
		t.Fatalf("写回后应仍可加载: %v\n%s", err, out)
	}
	m := lib.Describe("ssh-check")
	if m.Mode != ModeOnDemand || m.Maturity != MaturityStable || m.Version != "1.1.0" {
		t.Fatalf("写回后元数据丢失: %+v", m)
	}
	if len(m.Provides) != 2 || len(m.RequiresMCP) != 1 || m.RequiresMCP[0].MinVersion != "1.2.0" || m.RequiresMCP[0].Server != "security-assistant" {
		t.Fatalf("写回丢契约字段: provides=%v mcp=%+v", m.Provides, m.RequiresMCP)
	}
	if body, _ := lib.Body("ssh-check"); body != "正文段" {
		t.Fatalf("正文被破坏: %q", body)
	}
	if _, err := RewriteMode("无围栏", ModeStatic); err == nil {
		t.Fatal("无 frontmatter 应报错")
	}
	if _, err := RewriteMode(src, "magic"); err == nil {
		t.Fatal("非法 mode 应报错")
	}
}

// TestRewriteModeNoTitle 无 frontmatter name 的技能写回不得引入伪 name。
func TestRewriteModeNoTitle(t *testing.T) {
	src := "---\nmode: static\n---\n正文"
	out, err := RewriteMode(src, ModeOnDemand)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "name:") {
		t.Fatalf("无展示名不应写回 name 键: %q", out)
	}
}

// TestRewriteBody 保留 frontmatter 原文，仅替换正文。
func TestRewriteBody(t *testing.T) {
	src := "---\nname: s1\nmode: static\n---\n\n旧正文"
	out, err := RewriteBody(src, "新正文")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "name: s1") || !strings.Contains(out, "新正文") || strings.Contains(out, "旧正文") {
		t.Fatalf("RewriteBody 异常: %q", out)
	}
	if _, err := RewriteBody("无围栏", "x"); err == nil {
		t.Fatal("无 frontmatter 应报错")
	}
}

// TestVersionInRange 区间求解：空区间恒真；空格=AND；非法区间/版本报错。
func TestVersionInRange(t *testing.T) {
	cases := []struct {
		version, expr string
		want          bool
		wantErr       bool
	}{
		{"1.2.0", "", true, false},
		{"1.2.0", ">=1.0.0 <2.0.0", true, false},
		{"2.0.0", ">=1.0.0 <2.0.0", false, false},
		{"1.0.0", ">=1.0.0", true, false},
		{"0.0.0", "^0.1.0", false, false},
		{"1.2.0", "非法区间", false, true},
		{"not-a-version", ">=1.0.0", false, true},
	}
	for _, c := range cases {
		got, err := VersionInRange(c.version, c.expr)
		if c.wantErr {
			if err == nil {
				t.Fatalf("VersionInRange(%q,%q) 应报错", c.version, c.expr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("VersionInRange(%q,%q): %v", c.version, c.expr, err)
		}
		if got != c.want {
			t.Fatalf("VersionInRange(%q,%q)=%v, want %v", c.version, c.expr, got, c.want)
		}
	}
}
