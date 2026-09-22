package procx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExecCLIRejectsOptionBinary 锁死 exec 层守卫：argv[0] 以 "-" 开头即
// argument injection 面，必须 fail-fast（可执行名来自装配配置，越界即配置错误）。
func TestExecCLIRejectsOptionBinary(t *testing.T) {
	_, _, _, err := execCLI(context.Background(), "", []string{"-evil", "x"}, nil,
		time.Second, func(string) {}, 0)
	if err == nil || !strings.Contains(err.Error(), "非法可执行名") {
		t.Fatalf("argv[0] 以 - 开头应被拒绝: %v", err)
	}
}

// TestChildEnvLiteralPassthrough 锁死 Env 契约：含 "=" 的条目按 KEY=VALUE 字面
// 注入（父进程无同名变量时也不丢失）；纯名条目按名透传父进程值。
func TestChildEnvLiteralPassthrough(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "dumpenv")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AKX_TEST_BYNAME", "from-parent")
	t.Setenv("AKX_TEST_LITERAL", "from-parent")

	stdout, _, _, err := Run(context.Background(), RunRequest{
		Argv: []string{script},
		Env:  []string{"AKX_TEST_BYNAME", "AKX_TEST_LITERAL=literal-wins", "AKX_TEST_NEW=new-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"AKX_TEST_BYNAME=from-parent",   // 纯名：透传父进程值
		"AKX_TEST_LITERAL=literal-wins", // 字面：覆盖父进程同名值
		"AKX_TEST_NEW=new-value",        // 字面：父进程无同名也注入
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("子进程环境缺 %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "AKX_TEST_LITERAL=from-parent") {
		t.Errorf("字面注入不得透传父进程同名值:\n%s", stdout)
	}
}

// TestLineWriterFloodCap C3 回归：单行无换行洪泛不得绕过限容（partial 上限），
// 且后续正常行可恢复回调。
func TestLineWriterFloodCap(t *testing.T) {
	var lines []string
	lw := &lineWriter{buf: &cappedBuffer{}, onLine: func(l string) { lines = append(lines, l) }}

	big := strings.Repeat("A", 3<<20) // 3MB 单行，无换行
	if _, err := lw.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	if !lw.overflow {
		t.Fatal("超限后 overflow 应置位")
	}
	if len(lw.partial) > maxLineLen {
		t.Fatalf("partial 应被限容: %d", len(lw.partial))
	}
	if len(lines) != 0 {
		t.Fatalf("超限行不应回调: %d", len(lines))
	}
	// 洪泛结束（出现换行）→ 重新同步，后续正常行照常回调
	if _, err := lw.Write([]byte("tail\n")); err != nil {
		t.Fatal(err)
	}
	if lw.overflow {
		t.Fatal("行尾后应解除 overflow")
	}
	if _, err := lw.Write([]byte(`{"type":"result","result":"ok"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], `"result":"ok"`) {
		t.Fatalf("洪泛后正常行应恢复回调: %v", lines)
	}
}

// TestRun 导出 API：进程组纪律 + 白名单环境 + 限容 + sentinel，供非 Agent 抽象调用方使用。
func TestRun(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ok")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$RUNPROC_OK\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNPROC_SECRET", "leak-me")
	t.Setenv("RUNPROC_OK", "passed")

	stdout, _, code, err := Run(context.Background(), RunRequest{
		Argv: []string{script}, Dir: dir,
		Env:       []string{"RUNPROC_OK"},
		Timeout:   30 * time.Second,
		MaxStdout: 1 << 20,
	})
	if err != nil || code != 0 {
		t.Fatalf("Run 失败: %v code=%d", err, code)
	}
	if !strings.Contains(stdout, "passed") {
		t.Fatalf("白名单变量的值应透传: %q", stdout)
	}
	if strings.Contains(stdout, "leak-me") || strings.Contains(stdout, "RUNPROC_SECRET") {
		t.Fatalf("非白名单变量不得继承: %q", stdout)
	}

	// 超时 → ErrTimeout sentinel
	sleep := filepath.Join(dir, "sleep")
	if err := os.WriteFile(sleep, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Run(context.Background(), RunRequest{
		Argv: []string{sleep}, Timeout: 150 * time.Millisecond,
	}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("超时应 errors.Is ErrTimeout: %v", err)
	}

	// 调用方取消 → ErrCanceled（不得误报超时）
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	_, _, _, err = Run(ctx, RunRequest{Argv: []string{sleep}})
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("调用方取消应 errors.Is ErrCanceled: %v", err)
	}
	if errors.Is(err, ErrTimeout) {
		t.Fatalf("取消不得同时命中 ErrTimeout: %v", err)
	}
}
