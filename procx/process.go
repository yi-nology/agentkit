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
	"io"
	"os"
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
// 分工：newGroupCmd 建组与杀组纪律 / startAndWait 启动写 stdin 并收割 /
// classifyWait 错误语义 / noteTruncated 截断留痕。
func execCLI(ctx context.Context, dir string, argv []string, env []string,
	timeout time.Duration, onLine func(string), maxStdout int, stdin []byte) (stdout, stderr string, exitCode int, err error) {

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

	cmd, pgidKill := newGroupCmd(cctx, dir, argv, env)

	outBuf, errBuf := newCappedBuffer(maxStdout), newCappedBuffer(0)
	var lineW *lineWriter
	if onLine == nil {
		cmd.Stdout = outBuf
	} else {
		lineW = &lineWriter{buf: outBuf, onLine: onLine}
		cmd.Stdout = lineW
	}
	cmd.Stderr = errBuf

	waitErr, err := startAndWait(cmd, stdin, pgidKill, argv[0])
	if err != nil {
		return "", "", -1, err
	}
	// 行缓冲收尾（放在错误判断之前）：失败路径的流尾事件同样要 flush——
	// mimo 的 error JSON / kimi 的最后一行可能无尾换行，claude 的 result
	// 事件到达后进程仍可能非零退出，"result 优先"策略依赖这里救回结果
	if onLine != nil && len(lineW.partial) > 0 && !lineW.overflow {
		onLine(string(lineW.partial))
		lineW.partial = nil
	}

	out, errStr := outBuf.buf.String(), errBuf.buf.String()
	if waitErr != nil {
		// ErrWaitDelay：leader 已成功退出，但遗留的后台孙进程仍握 stdout 写端，
		// WaitDelay 到点强关管道所致（os/exec 语义：Cancel 未被调用 + 退出成功时
		// 才返回）。组已被 startAndWait 兜底 SIGKILL 清理——这是一次成功执行，
		// 不是失败（npm 类包装器守护化后台进程曾把成功跑整体误判为失败；
		// 第六轮审计）。
		if errors.Is(waitErr, exec.ErrWaitDelay) && ctx.Err() == nil {
			return noteTruncated(out, outBuf, maxStdout), errStr, 0, nil
		}
		return out, errStr, exitStatus(waitErr),
			classifyWait(ctx, cctx, argv[0], waitErr, errBuf, timeout)
	}
	return noteTruncated(out, outBuf, maxStdout), errStr, 0, nil
}

// startAndWait 启动 + 异步喂 stdin + 收割（含失败兜底清组）。
// 返回的 err 仅启动期失败（未跑到 Wait）；waitErr 为 Wait 结果。
func startAndWait(cmd *exec.Cmd, stdin []byte, pgidKill func(syscall.Signal) error, name string) (waitErr error, err error) {
	// StdinPipe 必须在 Start 之前建（os/exec 守卫：进程启动后 StdinPipe 报错，
	// 后建等于把 payload 丢进 /dev/null——子进程实际读到空 stdin）
	var stdinPipe io.WriteCloser
	if stdin != nil {
		pipe, perr := cmd.StdinPipe()
		if perr != nil {
			return nil, fmt.Errorf("procx: stdin 管道建立失败: %w", perr)
		}
		stdinPipe = pipe
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("procx: 启动 %s 失败: %w", name, err)
	}
	if stdinPipe != nil {
		// 异步写：子进程不读 stdin 且长寿时，同步 Write 会满管道阻塞到超时才进
		// Wait，错误分类被拖死。进程退出/被杀后 Write 得 EPIPE 自然收口。
		go func(pipe io.WriteCloser, payload []byte) {
			_, _ = pipe.Write(payload)
			_ = pipe.Close()
		}(stdinPipe, stdin)
	}
	waitErr = cmd.Wait()
	if waitErr != nil {
		// 兜底清组：leader 已死，这里清的是 TERM 宽限后仍未退出的孙进程
		_ = pgidKill(syscall.SIGKILL)
	}
	return waitErr, nil
}

// newGroupCmd 建组 + 超时/取消 TERM 整组 + 宽限 SIGKILL 的命令装配。
// 返回的 pgidKill 供 Wait 失败后兜底清组。
func newGroupCmd(cctx context.Context, dir string, argv, env []string) (*exec.Cmd, func(syscall.Signal) error) {
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
	cmd.Cancel = func() error {
		err := pgidKill(syscall.SIGTERM)
		if err == syscall.ESRCH {
			// 子进程恰在 deadline 触发瞬间自然退出：组已不在，Kill 返回 ESRCH。
			// 按 os/exec 约定返回 ErrProcessDone——否则成功退出会被误当
			// Cancel 失败，Wait 报错（第六轮审计）
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = killGrace
	return cmd, pgidKill
}

// classifyWait 把 Wait 错误翻译成调用方可 errors.Is 的语义（取消/超时/失败+stderr 尾巴）。
func classifyWait(ctx, cctx context.Context, name string, waitErr error, errBuf *cappedBuffer, timeout time.Duration) error {
	tail := errBuf.buf.String()
	if len(tail) > 400 {
		tail = tail[len(tail)-400:]
	}
	switch {
	case ctx.Err() != nil: // 调用方主动取消（含调用方自己的 deadline）
		return fmt.Errorf("procx: %s %w: %w", name, ErrCanceled, ctx.Err())
	case errors.Is(cctx.Err(), context.DeadlineExceeded): // 本地 Timeout
		return fmt.Errorf("procx: %s %w（%s）", name, ErrTimeout, timeout)
	default:
		return fmt.Errorf("procx: %s 执行失败: %w: %s", name, waitErr, strings.TrimSpace(tail))
	}
}

// noteTruncated 超限截断时在 stdout 尾部留痕（调用方可见「有内容被丢」）。
func noteTruncated(out string, outBuf *cappedBuffer, maxStdout int) string {
	if !outBuf.truncated {
		return out
	}
	limit := maxStdout
	if limit <= 0 {
		limit = maxChildStdout
	}
	return out + fmt.Sprintf("\n…（stdout 超过 %d 字节上限，已截断）", limit)
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
	// Stdin 可选：启动后写入子进程 stdin 并关闭（hookx 等交互式协议用）。
	// 子进程不读 stdin 提前退出时写入失败静默容忍（printf 类命令无害）。
	Stdin []byte
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
	return execCLI(ctx, req.Dir, req.Argv, ChildEnv(req.Env), timeout, req.OnLine, req.MaxStdout, req.Stdin)
}

// exitStatus 从 Wait 错误提取退出码。
func exitStatus(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
