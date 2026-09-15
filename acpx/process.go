package acpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// maxChildStdout 子进程 stdout 采集上限（异常 agent 刷屏防内存放大）。
const maxChildStdout = 8 << 20 // 8MB（stream-json 事件量大，比 Argus 的 1MB 放宽）

// maxLineLen lineWriter 单行缓冲上限：超限丢弃该行不再回调。partial 若无上限，
// 单行无换行洪泛会绕过 maxChildStdout 限容（claude stream-json 每个事件就是一行，
// 一个含超大 diff 的 result 事件即可冲出数百 MB 峰值）。
const maxLineLen = 1 << 20 // 1MB

// killGrace 超时/取消后 TERM 整组到 SIGKILL 的宽限期。
const killGrace = 3 * time.Second

// sentinel 错误：调用方可 errors.Is 分类（超时 / 被取消 / 启动失败）。
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

// baseEnvAllow 子进程缺省环境白名单：路径/临时目录/语言/代理/CA。
// 绝不全量继承——cli agent 在用户工作目录执行任意代码，
// 全量环境等于把宿主凭证交给待执行任务。
var baseEnvAllow = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "USER": true,
	"LANG": true, "LC_ALL": true, "TERM": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	// agent 自身的配置目录（登录态）：按名显式放行
	"CLAUDE_CONFIG_DIR": true, "OPENCODE_CONFIG": true,
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
}

// childEnv 构造子进程最小环境：白名单 + 显式透传项 + 禁交互。
// extra 两种形态：纯名（如 "HF_TOKEN"）从当前进程按名透传（不存在则丢弃）；
// 含 "="（如 "HF_TOKEN=xxx"）按 KEY=VALUE 字面注入，且同名父进程值不透传
// （每 key 唯一，避免 environ 同名两项时子进程取值依实现而异）。
func childEnv(extra []string) []string {
	keep := map[string]bool{}
	literal := map[string]string{} // name → KEY=VALUE
	var litOrder []string
	for _, k := range extra {
		if i := strings.IndexByte(k, '='); i > 0 {
			name := k[:i]
			if _, dup := literal[name]; !dup {
				litOrder = append(litOrder, name)
			}
			literal[name] = k
		} else {
			keep[k] = true
		}
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if _, isLit := literal[name]; isLit {
			continue // 字面注入项优先，不透传父进程同名值
		}
		if baseEnvAllow[name] || keep[name] {
			env = append(env, kv)
		}
	}
	for _, name := range litOrder {
		env = append(env, literal[name])
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "CI=1")
}

// execCLI 进程组执行 agent CLI（照抄 Argus adapter_cli 生产范式）：
// Setpgid 建组 → ctx 取消/超时 TERM 整组（cmd.Cancel）→ killGrace 后框架 SIGKILL
// leader（cmd.WaitDelay）→ Wait 返回后兜底 KILL 整组清残留孙进程。
// maxStdout stdout 采集上限（0 = maxChildStdout 缺省）。
func execCLI(ctx context.Context, dir string, argv []string, env []string,
	timeout time.Duration, onLine func(string), maxStdout int) (stdout, stderr string, exitCode int, err error) {

	if len(argv) == 0 {
		return "", "", -1, fmt.Errorf("acpx: 命令为空")
	}
	// argv[0] 只允许是可执行名：以 "-" 开头会被 exec 层之下的参数解析当选项
	// （argument injection）；Bin 来自装配配置，越界即配置错误，fail-fast
	if strings.HasPrefix(argv[0], "-") {
		return "", "", -1, fmt.Errorf("acpx: 非法可执行名 %q", argv[0])
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
		return "", "", -1, fmt.Errorf("acpx: 启动 %s 失败: %w", argv[0], err)
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
				fmt.Errorf("acpx: %s %w: %w", argv[0], ErrCanceled, ctx.Err())
		case errors.Is(cctx.Err(), context.DeadlineExceeded): // 本地 Timeout
			return outBuf.buf.String(), errBuf.buf.String(), exitStatus(waitErr),
				fmt.Errorf("acpx: %s %w（%s）", argv[0], ErrTimeout, timeout)
		default:
			return outBuf.buf.String(), errBuf.buf.String(), exitStatus(waitErr),
				fmt.Errorf("acpx: %s 执行失败: %w: %s", argv[0], waitErr, strings.TrimSpace(tail))
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

// ProcessRequest 独立进程执行请求：只要进程组托管纪律、不需要 Agent 解析层的
// 调用方使用（如包装外部 cli 审查器）。
type ProcessRequest struct {
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

// RunProcess 以进程组纪律执行外部命令：Setpgid 建组 → 超时/取消 TERM 整组 →
// killGrace 宽限后 SIGKILL。超时/取消可经 errors.Is(err, ErrTimeout/ErrCanceled)
// 程序化区分；非零退出返回 error（含 stderr 尾巴），exitCode 一并返回。
// 环境走白名单——待审仓库里的 cli 可执行任意代码，全量环境等于泄漏宿主凭证。
func RunProcess(ctx context.Context, req ProcessRequest) (stdout, stderr string, exitCode int, err error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return execCLI(ctx, req.Dir, req.Argv, childEnv(req.Env), timeout, req.OnLine, req.MaxStdout)
}

// exitStatus 从 Wait 错误提取退出码。
func exitStatus(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

// lineWriter 按行拆分 stdout，回调后仍写入 buf 保留全文。
// 单行超过 maxLineLen 时丢弃该行（overflow 置位后不回调，直到扫到行尾重新同步），
// buf 照常限容写入保留全文供兜底。
type lineWriter struct {
	buf      *cappedBuffer
	onLine   func(string)
	partial  []byte
	overflow bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	_, _ = w.buf.Write(p)
	if w.overflow {
		// 超限行继续流入：不再累积，只找行尾重新同步
		if i := bytes.LastIndexByte(p, '\n'); i >= 0 {
			w.overflow = false
			w.partial = append(w.partial[:0], p[i+1:]...)
		}
		return len(p), nil
	}
	if len(w.partial)+len(p) > maxLineLen {
		w.overflow = true // 整行丢弃（含已累积部分），防 partial 无限放大
		w.partial = w.partial[:0]
		return len(p), nil
	}
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		line := string(w.partial[:i])
		w.partial = w.partial[i+1:]
		if line != "" {
			w.onLine(line)
		}
	}
	return len(p), nil
}
