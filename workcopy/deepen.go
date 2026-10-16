// deepen.go 浅克隆工作副本的按需历史补全（收编自 argus internal/gitdeepen，
// v0.10.39）。
//
// 背景：依赖 merge-base 的 range 审查在 workcopy 的"默认分支浅克隆 + head
// checkout"下必挂（"cannot find merge-base"）。全量 `git fetch --unshallow`
// 把完整历史拉进关键路径，慢且重。本文件先本地探测 merge-base 可达性，
// 不可达再 `--deepen` 指数加深（64 起步逐轮 ×2）：多数 PR 一两轮即收敛，
// 代价从"全仓历史"降到"base↔head 的实际距离"。
//
// refspec 保持显式全量：clone --depth 1 隐含 --single-branch，裸 fetch 只补
// 默认分支的远端引用，PR base 非默认分支时 origin/<base> 不存在（真件实测
// 复现）。deepen 不移除 .git/shallow 标记——守卫因此从"标记存在"改为
// "merge-base 可达探测"，已补全的工作副本（标记可能仍在）探测通过即零开销。
package workcopy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yi-nology/agentkit/logredact"
	"github.com/yi-nology/agentkit/procx"
)

// fullRefspec 显式全量 refspec（--single-branch 克隆下唯一可靠的补全入口）。
const fullRefspec = "+refs/heads/*:refs/remotes/origin/*"

// deepenStart / maxDeepenRounds 指数加深参数：64 起步逐轮 ×2，8 轮覆盖 8k+
// 提交；真实上限由时间预算兜底——大仓耗尽预算即报错降级（审查主链路不阻断）。
const (
	deepenStart     = 64
	maxDeepenRounds = 8
	probeTimeout    = 30 * time.Second
)

// EnsureMergeBase 确保 origin/<baseBranch> 与 HEAD 的 merge-base 可达。
//   - 探测通过（完整克隆、已补全、或 base 与 head 重合等价场景）零 fetch 返回；
//   - 探测失败且 .git/shallow 存在 → deepen 指数加深循环（每轮后重探）；
//   - 探测失败但非浅克隆 → 静默返回：merge-base 失败另有原因（base 分支缺失、
//     非 git 目录等），交由后续 OCR 执行报真实错误——与旧"无 shallow 标记即
//     跳过"语义一致；
//   - 加深轮失败/预算耗尽仍不可达 → 报错降级。整条消息过 logredact（git 传输
//     失败回显带凭证 clone URL——ocrshim 通道此错误经 MCP 原样回传 argus）。
func EnsureMergeBase(ctx context.Context, dir, baseBranch string, timeout time.Duration) error {
	return ensureMergeBase(ctx, dir, baseBranch, timeout, false)
}

// EnsureMergeBaseTLS EnsureMergeBase 的 TLS 形态：insecureTLS=true 时对 deepen
// fetch 注入 GIT_SSL_NO_VERIFY（与 Pool.runGit 同款纪律——自签 https 平台的
// clone/PR fetch 均走该环境，deepen fetch 漏注会让补全在此类平台确定性失败
// 且静默降级丢失 range 审查；第八轮审计 C 级修复）。
func EnsureMergeBaseTLS(ctx context.Context, dir, baseBranch string, timeout time.Duration, insecureTLS bool) error {
	return ensureMergeBase(ctx, dir, baseBranch, timeout, insecureTLS)
}

func ensureMergeBase(ctx context.Context, dir, baseBranch string, timeout time.Duration, insecureTLS bool) error {
	if baseBranch == "" {
		return errors.New("gitdeepen: 缺少 base 分支")
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	base := "origin/" + baseBranch

	if probeMergeBase(ctx, dir, base) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "shallow")); err != nil {
		return nil // 完整克隆：merge-base 失败非浅历史问题
	}
	for i := 0; i < maxDeepenRounds; i++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		fetchReq := procx.RunRequest{
			Argv: []string{
				"git", "-C", dir, "fetch",
				"--deepen=" + strconv.Itoa(deepenStart<<i),
				"origin", fullRefspec,
			},
			Timeout: remaining,
		}
		if insecureTLS {
			fetchReq.Env = []string{"GIT_SSL_NO_VERIFY=true"}
		}
		if _, stderr, _, err := procx.Run(ctx, fetchReq); err != nil {
			return errors.New(logredact.Redact(fmt.Sprintf(
				"浅克隆加深失败（git fetch --deepen）: %v；stderr: %s", err, tail(stderr, 400))))
		}
		if probeMergeBase(ctx, dir, base) {
			return nil
		}
	}
	return errors.New(logredact.Redact(fmt.Sprintf(
		"浅克隆加深后 merge-base 仍不可达（origin/%s ↔ HEAD，%s 内 deepen %d 轮）——OCR range 审查降级",
		baseBranch, timeout.Round(time.Second), maxDeepenRounds)))
}

// probeMergeBase 本地探测 merge-base 可达（无网络 IO，秒级）。exit 非零一律视为
// 不可达（含 base 远端引用不存在——由后续 deepen 轮经全量 refspec 创建）。
func probeMergeBase(ctx context.Context, dir, base string) bool {
	_, _, _, err := procx.Run(ctx, procx.RunRequest{
		Argv:    []string{"git", "-C", dir, "merge-base", base, "HEAD"},
		Timeout: probeTimeout,
	})
	return err == nil
}

// tail 取 stderr 尾部（诊断用，限长防爆）。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
