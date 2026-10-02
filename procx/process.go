// Package procx 子进程托管纪律单源：进程组执行（Setpgid 建组 → 超时/取消
// TERM 整组 → 宽限 SIGKILL）、限容采集（stdout/单行双上限）、stdout 按行拆分、
// 子进程环境白名单（绝不继承宿主全量环境）。
// acpx 各 CLI agent 适配器与 mcp/workcopy 等自行 spawn 外部命令的包共用本包，
// 纪律只有一份。
package procx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// maxChildStdout 子进程 stdout 采集上限（异常命令刷屏防内存放大）。
const maxChildStdout = 8 << 20 // 8MB（stream-json 事件量大，比 Argus 的 1MB 放宽）

// killGrace 超时/取消后 TERM 整组到 SIGKILL 的宽限期。
const killGrace = 3 * time.Second

// sentinel 错误：调用方可 errors.Is 分类（超时 / 被取消）。
var (
	// ErrTimeout 运行超过 Timeout 被终止。
	ErrTimeout = errors.New("执行超时")
	// ErrCanceled 运行被调用方 context 取消。
	ErrCanceled = errors.New("执行被取消")
)

// cappedBuffer 限容写入器：超限后丢弃后续写入并标记截断。
type cappedBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	if limit <= 0 {
		limit = maxChildStdout
	}
	return &cappedBuffer{limit: limit}
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.limit {
		room := c.limit - c.buf.Len()
		if room > 0 {
			c.buf.Write(p[:room])
		}
		c.truncated = true
		return len(p), nil // 吸收写入让子进程继续跑（管道不阻塞）
	}
	c.buf.Write(p)
	return len(p), nil
}

// execCLI 进程组执行外部命令（照抄 Argus adapter_cli 生产范式）：
// Setpgid 建组 → ctx 取消/超时 TERM 整组（cmd.Cancel）→ killGrace 后框架 SIGKILL
// leader（cmd.WaitDelay）→ Wait 返回后兜底 KILL 整组清残留孙进程。
// maxStdout stdout 采集上限（0 = maxChildStdout 缺省）。
func execCLI(ctx context.Context, dir string, argv []string, env []string,
	timeout time.Duration, onLine func(string), maxStdout int) (stdout, stderr string, exitCode int, err error) {

	if len(argv) == 0 {
		return "", "", -1, fmt.Errorf("procx: 命令为空")
	}
	// argv[0] 只允许是可执行名：以 "-" 开头会被 exec 层之下的参数解析当选项
	// （argument injection）；来自装配配置的越界即配置错误，fail-fast
	if strings.HasPrefix(argv[0], "-") {
		return "", "", -1, fmt.Errorf("procx: 非法可执行名 %q", argv[0])
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// CommandContext 缺省在 ctx done 时直接 SIGKILL leader，会抢在宽限前面——
	// 用 cmd.Cancel 接管为 TERM 整组，宽限内的 SIGKILL 由 WaitDelay 兜底
	pgidKill := func(sig syscall.Signal) error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, sig)
	}
	cmd.Cancel = func() error { return pgidKill(syscall.SIGTERM) }
	cmd.WaitDelay = killGrace

	outBuf, errBuf := newCappedBuffer(maxStdout), newCappedBuffer(0)
	var lineW *lineWriter
	if onLine == nil {
		cmd.Stdout = outBuf
	} else {
		lineW = &lineWriter{buf: outBuf, onLine: onLine}
		cmd.Stdout = lineW
	}
	cmd.Stderr = errBuf

	if err := cmd.Start(); err != nil {
		return "", "", -1, fmt.Errorf("procx: 启动 %s 失败: %w", argv[0], err)
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		// 兜底清组：leader 已死，这里清的是 TERM 宽限后仍未退出的孙进程
		_ = pgidKill(syscall.SIGKILL)
	}
	// 行缓冲收尾（放在错误判断之前）：失败路径的流尾事件同样要 flush——
	// mimo 的 error JSON / kimi 的最后一行可能无尾换行，claude 的 result
	// 事件到达后进程仍可能非零退出，"result 优先"策略依赖这里救回结果
	if onLine != nil && len(lineW.partial) > 0 && !lineW.overflow {
		onLine(string(lineW.partial))
		lineW.partial = nil
	}

	if waitErr != nil {
		tail := errBuf.buf.String()
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		switch {
		case ctx.Err() != nil: // 调用方主动取消（含调用方自己的 deadline）
			return outBuf.buf.String(), errBuf.buf.String(), exitStatus(waitErr),
				fmt.Errorf("procx: %s %w: %w", argv[0], ErrCanceled, ctx.Err())
		case errors.Is(cctx.Err(), context.DeadlineExceeded): // 本地 Timeout
			return outBuf.buf.String(), errBuf.buf.String(), exitStatus(waitErr),
				fmt.Errorf("procx: %s %w（%s）", argv[0], ErrTimeout, timeout)
		default:
			return outBuf.buf.String(), errBuf.buf.String(), exitStatus(waitErr),
				fmt.Errorf("procx: %s 执行失败: %w: %s", argv[0], waitErr, strings.TrimSpace(tail))
		}
	}
	out := outBuf.buf.String()
	if outBuf.truncated {
		limit := maxStdout
		if limit <= 0 {
			limit = maxChildStdout
		}
		out += fmt.Sprintf("\n…（stdout 超过 %d 字节上限，已截断）", limit)
	}
	return out, errBuf.buf.String(), 0, nil // waitErr == nil → 退出码 0
}

// RunRequest 独立进程执行请求：只要进程组托管纪律的调用方使用
// （acpx 各 CLI agent 适配器经 acpx.runCLI 复用同一纪律）。
type RunRequest struct {
	// Argv 可执行 + 参数（argv 直传，无 shell、无注入面）。
	Argv []string
	// Dir 工作目录（空 = 继承当前进程）。
	Dir string
	// Env 子进程环境白名单（按名从当前进程透传；含 "=" 按 KEY=VALUE 字面），
	// 叠加在 baseEnvAllow 基础集之上——绝不全量继承。
	Env []string
	// Timeout 单次执行超时（0 = 10 分钟缺省）。
	Timeout time.Duration
	// OnLine 可选 stdout 按行回调（nil = 只缓冲；回调在读取 goroutine 中执行，
	// 不得阻塞/panic；单行超 maxLineLen 会被丢弃）。
	OnLine func(string)
	// MaxStdout stdout 采集上限（0 = 8MB 缺省）。
	MaxStdout int
}

// defaultTimeout 单次执行缺省超时。
const defaultTimeout = 10 * time.Minute

// Run 以进程组纪律执行外部命令：Setpgid 建组 → 超时/取消 TERM 整组 →
// killGrace 宽限后 SIGKILL。超时/取消可经 errors.Is(err, ErrTimeout/ErrCanceled)
// 程序化区分；非零退出返回 error（含 stderr 尾巴），exitCode 一并返回。
// 环境走白名单——待执行目录里的命令可执行任意代码，全量环境等于泄漏宿主凭证。
func Run(ctx context.Context, req RunRequest) (stdout, stderr string, exitCode int, err error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return execCLI(ctx, req.Dir, req.Argv, ChildEnv(req.Env), timeout, req.OnLine, req.MaxStdout)
}

// exitStatus 从 Wait 错误提取退出码。
func exitStatus(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
