package workcopy

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeGit 把可编程的伪造 git 放上 PATH：完整 argv 追加到 probe。
// probeFailUntil = merge-base 第 N 次调用起成功（<=0 恒成功）；failFetch =
// fetch 以带凭证的 stderr 失败（脱敏回归）。
func fakeGit(t *testing.T, probeFailUntil int, failFetch bool) string {
	t.Helper()
	bin := t.TempDir()
	probe := filepath.Join(bin, "called")
	state := filepath.Join(bin, "state")
	script := "#!/bin/sh\necho \"$@\" >> " + shq(probe) + "\n"
	if failFetch {
		script += "if [ \"$3\" = \"fetch\" ]; then echo 'fatal: unable to access https://oauth2:sup3rsecret@host/o/r.git/: some error' >&2; exit 128; fi\n"
	}
	if probeFailUntil > 0 {
		// 状态读取用 shell 内建 read（子进程 PATH 只含假 git 目录，外部命令
		// 如 cat 不可用——acpx 最小环境 + t.Setenv 的既有约束）
		script += "if [ \"$3\" = \"merge-base\" ]; then\n" +
			"  n=0\n" +
			"  if [ -f " + shq(state) + " ]; then read n < " + shq(state) + "; fi\n" +
			"  n=$((n+1)); echo $n > " + shq(state) + "\n" +
			"  if [ \"$n\" -lt " + strconv.Itoa(probeFailUntil) + " ]; then exit 1; fi\n" +
			"fi\n"
	}
	script += "exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return probe
}

func shallowDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "shallow"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readArgv(t *testing.T, probe string) string {
	t.Helper()
	b, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal("git 应被调用")
	}
	return string(b)
}

// 完整克隆：本地探测秒级通过，零 fetch 快速路径（merge-base 失败另有原因时
// 静默返回、交由后续 OCR 报真实错误——旧语义保持）。
func TestEnsureMergeBaseFullCloneFastPath(t *testing.T) {
	probe := fakeGit(t, 0, false)
	if err := EnsureMergeBase(context.Background(), t.TempDir(), "main", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if argv := readArgv(t, probe); strings.Contains(argv, "fetch") {
		t.Fatalf("探测通过不应触发 fetch: %s", argv)
	}
}

// 浅克隆但 merge-base 已可达（此前已加深过的工作副本）：零 fetch。
func TestEnsureMergeBaseShallowAlreadyReachable(t *testing.T) {
	probe := fakeGit(t, 0, false)
	if err := EnsureMergeBase(context.Background(), shallowDir(t), "main", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if argv := readArgv(t, probe); strings.Contains(argv, "fetch") {
		t.Fatalf("merge-base 已可达不应 fetch: %s", argv)
	}
}

// 浅克隆 merge-base 不可达 → --deepen 指数加深，一轮收敛后停止；refspec 必须
// 显式全量（--single-branch 克隆下 PR base 非默认分支时 origin/<base> 缺失，
// 真件实测复现——与旧 unshallow 同款纪律）。
func TestEnsureMergeBaseDeepensIncrementally(t *testing.T) {
	probe := fakeGit(t, 2, false) // 第 2 次 merge-base 起成功 → 恰好一轮 deepen
	if err := EnsureMergeBase(context.Background(), shallowDir(t), "main", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	argv := readArgv(t, probe)
	if !strings.Contains(argv, "--deepen=64") {
		t.Fatalf("应有首轮 --deepen=64: %s", argv)
	}
	if !strings.Contains(argv, "+refs/heads/*:refs/remotes/origin/*") {
		t.Fatalf("fetch 必须带显式全量 refspec: %s", argv)
	}
	if strings.Contains(argv, "--deepen=128") {
		t.Fatalf("一轮收敛后不得继续加深: %s", argv)
	}
}

// 指数递增：持续不可达时逐轮 ×2（64→128→…→4096 封顶），预算耗尽报错降级
// （错误经调用方告警，审查主链路不阻断）。
func TestEnsureMergeBaseExhaustionErrors(t *testing.T) {
	probe := fakeGit(t, 1<<20, false) // merge-base 永不成功
	err := EnsureMergeBase(context.Background(), shallowDir(t), "main", 10*time.Second)
	if err == nil {
		t.Fatal("预算耗尽应报错降级")
	}
	if !strings.Contains(err.Error(), "仍不可达") {
		t.Fatalf("错误应说明不可达: %v", err)
	}
	argv := readArgv(t, probe)
	for _, step := range []string{"--deepen=64", "--deepen=128", "--deepen=4096"} {
		if !strings.Contains(argv, step) {
			t.Fatalf("argv 缺 %s: %s", step, argv)
		}
	}
}

// fetch 失败：错误上抛且带凭证的 clone URL 必须整条脱敏——ocrshim 通道此错误
// 经 MCP 原样回传 argus，可落入日志与 PR 评论（v3.9.0 审查修复的同款纪律）。
func TestEnsureMergeBaseFetchFailureRedacts(t *testing.T) {
	fakeGit(t, 2, true)
	err := EnsureMergeBase(context.Background(), shallowDir(t), "main", 10*time.Second)
	if err == nil {
		t.Fatal("fetch 失败应上抛")
	}
	if !strings.Contains(err.Error(), "some error") {
		t.Fatalf("错误应含 stderr 尾巴: %v", err)
	}
	if strings.Contains(err.Error(), "sup3rsecret") {
		t.Fatalf("带凭证 URL 必须被脱敏: %v", err)
	}
}

// base 分支缺失守卫（调用方均先校验，此处兜底）。
func TestEnsureMergeBaseRequiresBaseBranch(t *testing.T) {
	if err := EnsureMergeBase(context.Background(), t.TempDir(), "", time.Second); err == nil {
		t.Fatal("缺少 base 分支应报错")
	}
}

// shq shell 单引号包裹（POSIX；'→'\” 转义）。
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
