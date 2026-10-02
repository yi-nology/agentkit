package acpx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// maxChildStdout 子进程 stdout 采集上限（异常 agent 刷屏防内存放大）。
const maxChildStdout = 8 << 20 // 8MB（stream-json 事件量大，比 Argus 的 1MB 放宽）

// cappedBuffer 限容写入器：超限后丢弃后续写入并标记截断。
type cappedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > maxChildStdout {
		room := maxChildStdout - c.buf.Len()
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
func childEnv(extra []string) []string {
	keep := map[string]bool{}
	for _, k := range extra {
		if i := strings.IndexByte(k, '='); i > 0 {
			keep[k[:i]] = true
		} else {
			keep[k] = true
		}
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if baseEnvAllow[name] || keep[name] {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "CI=1")
}

// execCLI 进程组执行 agent CLI（照抄 Argus adapter_cli 生产范式）：
// Setpgid 建组 → ctx 取消/超时 TERM 整组 → 3s grace 后 KILL 整组防孤儿。
// onLine 回调按行消费 stdout（nil = 只缓冲）。
func execCLI(ctx context.Context, dir string, argv []string, env []string,
	timeout time.Duration, onLine func(string)) (stdout, stderr string, exitCode int, err error) {

	if len(argv) == 0 {
		return "", "", -1, fmt.Errorf("acpx: 命令为空")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	outBuf, errBuf := &cappedBuffer{}, &cappedBuffer{}
	var lineW *lineWriter
	if onLine == nil {
		cmd.Stdout = outBuf
	} else {
		lineW = &lineWriter{buf: outBuf, onLine: onLine}
		cmd.Stdout = lineW
	}
	cmd.Stderr = errBuf

	if err := cmd.Start(); err != nil {
		return "", "", -1, fmt.Errorf("启动 %s 失败: %w", argv[0], err)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-cctx.Done(): // 超时/取消：TERM 整组 → grace 后 KILL 整组
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				time.Sleep(3 * time.Second)
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()
	waitErr := cmd.Wait()
	close(done)
	if waitErr != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // 兜底清组
		tail := errBuf.buf.String()
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		if cctx.Err() != nil {
			return outBuf.buf.String(), errBuf.buf.String(), exitStatus(waitErr),
				fmt.Errorf("acpx: %s 执行超时（%s）", argv[0], timeout)
		}
		return outBuf.buf.String(), errBuf.buf.String(), exitStatus(waitErr),
			fmt.Errorf("acpx: %s 执行失败: %v: %s", argv[0], waitErr, strings.TrimSpace(tail))
	}
	out := outBuf.buf.String()
	if outBuf.truncated {
		out += "\n…（stdout 超过 8MB 上限，已截断）"
	}
	// 行缓冲收尾：最后一行无换行符时 partial 不会触发 onLine，事件会丢
	//（mimo 的 error JSON / kimi 的最后一行都可能无尾换行）
	if onLine != nil && len(lineW.partial) > 0 {
		onLine(string(lineW.partial))
		lineW.partial = nil
	}
	return out, errBuf.buf.String(), 0, nil // waitErr == nil → 退出码 0
}

// exitStatus 从 Wait 错误提取退出码。
func exitStatus(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

// lineWriter 按行拆分 stdout，回调后仍写入 buf 保留全文。
type lineWriter struct {
	buf    *cappedBuffer
	onLine func(string)
	partial []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
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
	_, _ = w.buf.Write(p)
	return len(p), nil
}
