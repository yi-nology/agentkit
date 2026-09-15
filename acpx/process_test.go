package acpx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExecCLIRejectsOptionBinary 锁死 exec 层守卫：argv[0] 以 "-" 开头即
// argument injection 面，必须 fail-fast（Bin 来自装配配置，越界即配置错误）。
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

	stdout, _, _, err := RunProcess(context.Background(), ProcessRequest{
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
