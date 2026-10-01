package sysprompt

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBuildOrderingAndBlocks(t *testing.T) {
	b := &Builder{}
	b.Add(NewSection("dyn-a", "", "", "动态A"))                         // system/dynamic（缺省）
	b.Add(IdentitySection("你是审查助手"))                                  // system/stable
	b.Add(NewSection("dyn-b", "", "", "动态B"))                         // system/dynamic
	b.Add(DateSection(time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC))) // user_context/dynamic
	res := b.Build()

	if len(res.System) != 2 {
		t.Fatalf("应 2 块: %d", len(res.System))
	}
	if res.System[0].Boundary != Stable || !strings.Contains(res.System[0].Content, "审查助手") {
		t.Fatalf("首块应为 stable 身份: %+v", res.System[0])
	}
	if res.System[1].Boundary != Dynamic {
		t.Fatalf("次块应为 dynamic: %+v", res.System[1])
	}
	if !strings.Contains(res.System[1].Content, "动态A") || !strings.Contains(res.System[1].Content, "动态B") {
		t.Fatal("dynamic 段应按插入序拼入")
	}
	// 排序面：stable 段在 Sections 首位
	if res.Sections[0].Name != "Identity" {
		t.Fatalf("stable 应排首位: %v", res.Sections[0].Name)
	}
	// user_context 含日期 + 免责
	if !strings.Contains(res.UserContext, "Today's date is 2026-09-27") ||
		!strings.Contains(res.UserContext, DefaultUserContextDisclaimer) {
		t.Fatalf("user_context 拼装错误: %q", res.UserContext)
	}
	// 计量 = 各段之和
	var chars int
	for _, s := range res.Sections {
		chars += s.Chars
	}
	if res.TotalChars != chars || res.TotalTokens == 0 {
		t.Fatalf("计量: chars=%d total=%d", chars, res.TotalChars)
	}
}

func TestBuildDeterministicAndFrozen(t *testing.T) {
	b := &Builder{}
	b.Add(IdentitySection("身份"))
	b.Add(NewSection("env", "", "", "env 内容"))
	r1 := b.Build()
	b.Add(NewSection("late", "", "", "后加段"))
	r2 := b.Build()
	if strings.Contains(r1.UserContext+r1.System[1].Content, "后加段") {
		t.Fatal("Build 产物应冻结")
	}
	if !strings.Contains(r2.System[1].Content, "后加段") {
		t.Fatal("新 Build 应含后加段")
	}
}

func TestBuildEmpty(t *testing.T) {
	res := (&Builder{}).Build()
	if len(res.System) != 0 || res.UserContext != "" || res.TotalChars != 0 {
		t.Fatalf("空构建: %+v", res)
	}
}

func TestSectionMetering(t *testing.T) {
	s := NewSection("x", "", "", "abc") // 3 chars → 1 token
	if s.Chars != 3 || s.Tokens != 1 {
		t.Fatalf("计量: %d/%d", s.Chars, s.Tokens)
	}
	s2 := NewSection("y", "", "", "abcd") // 4 chars → ceil(4/3)=2
	if s2.Tokens != 2 {
		t.Fatalf("向上取整: %d", s2.Tokens)
	}
	// 缺省 target/boundary
	if s.Target != TargetSystem || s.Boundary != Dynamic {
		t.Fatal("缺省应 system/dynamic")
	}
}

func TestEnvSection(t *testing.T) {
	s := EnvSection(EnvInfo{
		Cwd: "/repo", IsGitRepo: true, Platform: "darwin",
		Shell: "/bin/zsh", OSVersion: "Darwin 27.0.0", ModelID: "bigmodel/glm-5.3",
	})
	for _, want := range []string{"Primary working directory: /repo", "Is a git repository: yes",
		"Platform: darwin", "Shell: /bin/zsh", "OS Version: Darwin 27.0.0", "bigmodel/glm-5.3"} {
		if !strings.Contains(s.Content, want) {
			t.Errorf("env 段缺 %q:\n%s", want, s.Content)
		}
	}
	if s.Boundary != Dynamic || s.Target != TargetSystem {
		t.Fatal("env 段应 system/dynamic")
	}
}

func TestGitSection(t *testing.T) {
	s := GitSection(GitInfo{
		Branch: "main", MainBranch: "main", User: "MurphyYi",
		StatusLines:   []string{" M a.go", "?? b.go"},
		RecentCommits: []string{"abc123 fix", "def456 feat"},
	})
	for _, want := range []string{"snapshot in time", "Current branch: main",
		"Main branch (you will usually use this for PRs): main", "Git user: MurphyYi",
		" M a.go", "abc123 fix"} {
		if !strings.Contains(s.Content, want) {
			t.Errorf("git 段缺 %q:\n%s", want, s.Content)
		}
	}
	// clean 形态
	s2 := GitSection(GitInfo{Branch: "main"})
	if !strings.Contains(s2.Content, "(clean)") {
		t.Fatalf("无状态行应 clean:\n%s", s2.Content)
	}
	// unknown 形态
	s3 := GitSection(GitInfo{Branch: "main", StatusUnknown: true})
	if !strings.Contains(s3.Content, "(unknown)") {
		t.Fatalf("采集失败应 unknown:\n%s", s3.Content)
	}
}

func TestDetectEnvInGitRepo(t *testing.T) {
	// agentkit 仓库自身就是 git 仓——采集应得出分支与提交
	env, git, err := DetectEnv(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	if !env.IsGitRepo || git == nil {
		t.Fatalf("应识别 git 仓: %+v %+v", env, git)
	}
	if env.Platform == "" || env.Shell == "" {
		t.Fatalf("平台/shell 应有值: %+v", env)
	}
	if git.Branch == "" || len(git.RecentCommits) == 0 {
		t.Fatalf("分支与提交应采集到: %+v", git)
	}
}

func TestDetectEnvNotARepo(t *testing.T) {
	env, git, err := DetectEnv(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if env.IsGitRepo || git != nil {
		t.Fatalf("临时目录非 git 仓: %+v %+v", env, git)
	}
}

func TestUserContextCustomWording(t *testing.T) {
	b := &Builder{UserContextIntro: "参考材料：", UserContextDisclaimer: "免责"}
	b.Add(DateSection(time.Now()))
	res := b.Build()
	if !strings.HasPrefix(res.UserContext, "参考材料：") || !strings.HasSuffix(res.UserContext, "免责") {
		t.Fatalf("覆写措辞未生效: %q", res.UserContext)
	}
}
