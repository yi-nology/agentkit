package workcopy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.enjoye.top/enjoydream/ekit/observability/logx"
)

// makeFixtureRepo 创建本地 git fixture 仓库（file:// 协议克隆，零网络依赖）：
// 默认分支 main 提交 1 次；创建 refs/pull/1/head 供 fetch（模拟 PR head）。
func makeFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-b", "main", dir)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# fixture\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")
	// 模拟 PR head ref（gitea/github 习惯 refs/pull/<n>/head）
	run("update-ref", "refs/pull/1/head", "main")
	return dir
}

func newTestPool(t *testing.T, fixture string) *Pool {
	t.Helper()
	return NewPool(t.TempDir(), logx.NewSlogLogger("test"))
}

func TestEnsureAndRelease(t *testing.T) {
	fixture := makeFixtureRepo(t)
	p := newTestPool(t, fixture)
	p.CredentialOf = func(string) (string, string, bool) { return "file://" + fixture, "x", true }

	ctx := context.Background()
	key := WorktreeKey{Platform: "gitea", Owner: "o", Repo: "r", Number: "1",
		HeadSHA: "HEAD", DefaultBranch: "main"}

	dir, err := p.Ensure(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	// 工作树就绪：README 存在
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("工作副本不完整: %v", err)
	}

	// 引用计数：二次 Ensure 返回同一目录
	dir2, err := p.Ensure(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if dir2 != dir {
		t.Fatal("同 key 并发 Ensure 应共享目录")
	}

	// Release 一次：引用计数 1，目录保留
	p.Release(key)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("引用计数未归零不应删除")
	}

	// 再 Release：归零删除
	p.Release(key)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("引用归零后应删除沙箱目录")
	}
}

func TestEnsureMissingCredential(t *testing.T) {
	p := newTestPool(t, "")
	p.CredentialOf = func(string) (string, string, bool) { return "", "", false }

	_, err := p.Ensure(context.Background(), WorktreeKey{Platform: "gitea", HeadSHA: "x"})
	if err == nil {
		t.Fatal("缺凭证应报错")
	}
}

func TestEnsureNilCredentialFunc(t *testing.T) {
	p := newTestPool(t, "")
	_, err := p.Ensure(context.Background(), WorktreeKey{HeadSHA: "x"})
	if err == nil {
		t.Fatal("未配置 CredentialOf 应报错")
	}
}

// TestEnsureRejectsOptionBranch 锁死引用名守卫：以 "-" 开头的 DefaultBranch
// 会被 git 当选项解析（argument injection），必须在建目录之前拒绝。
func TestEnsureRejectsOptionBranch(t *testing.T) {
	p := newTestPool(t, "")
	p.CredentialOf = func(string) (string, string, bool) { return "https://git.example.com", "x", true }

	_, err := p.Ensure(context.Background(), WorktreeKey{
		Platform: "gitea", Owner: "o", Repo: "r", Number: "1", HeadSHA: "x",
		DefaultBranch: "--upload-pack=evil",
	})
	if err == nil || !strings.Contains(err.Error(), "非法引用名") {
		t.Fatalf("以 - 开头的分支应被拒绝: %v", err)
	}
	if es, _ := os.ReadDir(p.Root); len(es) != 0 {
		t.Fatalf("拒绝路径不应产生沙箱目录: %v", es)
	}
}

func TestEnsureMissingHeadSHA(t *testing.T) {
	fixture := makeFixtureRepo(t)
	p := newTestPool(t, fixture)
	p.CredentialOf = func(string) (string, string, bool) { return "file://" + fixture, "x", true }

	_, err := p.Ensure(context.Background(), WorktreeKey{Platform: "gitea", Owner: "o", Repo: "r", Number: "1"})
	if err == nil {
		t.Fatal("缺 head SHA 应报错")
	}
}

func TestCloneURLStripsAPISuffix(t *testing.T) {
	cases := []struct{ base, host string }{
		{"https://git.example.com/api/v1", "git.example.com"},
		{"https://gitlab.example.com/api/v4", "gitlab.example.com"},
		{"https://git.example.com/", "git.example.com"},
	}
	for _, c := range cases {
		got := cloneURL(c.base, "o", "r", "tok")
		want := "https://oauth2:tok@" + c.host + "/o/r.git"
		if got != want {
			t.Errorf("cloneURL(%q) = %q, want %q", c.base, got, want)
		}
	}
	// file:// 直通（e2e fixture 场景），不嵌凭证
	if got := cloneURL("file:///tmp/repo", "o", "r", "tok"); got != "file:///tmp/repo" {
		t.Errorf("file:// 应直通，得到 %q", got)
	}
}

func TestPrRefspec(t *testing.T) {
	if got := prRefspec("gitlab", "12"); got != "refs/merge-requests/12/head" {
		t.Fatalf("gitlab refspec = %q", got)
	}
	if got := prRefspec("gitea", "12"); got != "refs/pull/12/head" {
		t.Fatalf("gitea refspec = %q", got)
	}
}

func TestScrub(t *testing.T) {
	msg := "fatal: unable to access 'https://oauth2:secret123@host/repo.git/'"
	out := scrub(msg, "secret123")
	if containsStr(out, "secret123") {
		t.Fatalf("token 未脱敏: %s", out)
	}
}

func TestSweepRemovesOrphans(t *testing.T) {
	p := newTestPool(t, "")
	// 模拟孤儿目录：旧 mtime
	orphan := filepath.Join(p.Root, "wc-orphan")
	_ = os.MkdirAll(orphan, 0o755)
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(orphan, old, old)

	// 活跃目录（新 mtime）不应被清
	live := filepath.Join(p.Root, "wc-live")
	_ = os.MkdirAll(live, 0o755)

	p.Sweep(time.Hour)

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("过期孤儿目录应被清理")
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatal("新目录不应被清理")
	}
}

func TestWorktreeKeyString(t *testing.T) {
	k := WorktreeKey{Platform: "gitea", Owner: "o", Repo: "r", Number: "1", HeadSHA: "abc"}
	if k.String() != "gitea/o/r#1@abc" {
		t.Fatalf("String() = %q", k.String())
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestSweepKeepsInUseWorktree(t *testing.T) {
	// 回归：rc>0 的在用目录即使超 TTL 也不得被 Sweep 删除（长任务保护）
	fixture := makeFixtureRepo(t)
	p := newTestPool(t, fixture)
	p.CredentialOf = func(string) (string, string, bool) { return "file://" + fixture, "x", true }

	key := WorktreeKey{Platform: "gitea", Owner: "o", Repo: "r", Number: "1",
		HeadSHA: "HEAD", DefaultBranch: "main"}
	dir, err := p.Ensure(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}

	p.Sweep(-time.Hour) // TTL 为负 = 一切都算过期
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal("rc>0 的在用工作副本不得被 Sweep 删除")
	}

	// 释放后（rc=0）再次 Sweep：此时才可回收
	p.Release(key)
	p.Sweep(-time.Hour)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("rc=0 且超 TTL 的目录应被 Sweep 回收")
	}
}

func TestScrubRedactsTokenInPrepareError(t *testing.T) {
	// 回归：prepare 失败路径的错误必须携带 git 详情且不含 token（scrub 参数顺序）
	fixture := makeFixtureRepo(t)
	p := newTestPool(t, fixture)
	const token = "sup3rs3cret-token"
	p.CredentialOf = func(string) (string, string, bool) { return "file://" + fixture, token, true }
	// 不存在的分支 → clone 失败
	key := WorktreeKey{Platform: "gitea", Owner: "o", Repo: "r", Number: "1",
		HeadSHA: "HEAD", DefaultBranch: "no-such-branch"}

	_, err := p.Ensure(context.Background(), key)
	if err == nil {
		t.Fatal("clone 失败应报错")
	}
	if !strings.Contains(err.Error(), "no-such-branch") && !strings.Contains(err.Error(), "not found") &&
		!strings.Contains(err.Error(), "could not") {
		t.Fatalf("错误应保留 git 失败详情: %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("错误不得泄漏 token: %v", err)
	}
}

func TestCloneURLPreservesScheme(t *testing.T) {
	// 回归：内网 http:// 地址不得被强制升级 https
	got := cloneURL("http://gitea.internal:3000", "o", "r", "tok")
	if !strings.HasPrefix(got, "http://oauth2:tok@gitea.internal:3000/o/r.git") {
		t.Fatalf("http scheme 应保留: %q", got)
	}
	got = cloneURL("gitea.internal", "o", "r", "tok")
	if !strings.HasPrefix(got, "https://oauth2:tok@gitea.internal/o/r.git") {
		t.Fatalf("无 scheme 应补 https: %q", got)
	}
}
