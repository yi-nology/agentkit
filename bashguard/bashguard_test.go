package bashguard

import (
	"strings"
	"testing"
)

func TestAnalyzeReadOnlyCommands(t *testing.T) {
	readonly := []string{
		"ls -la",
		"cat main.go",
		"grep -rn TODO .",
		"head -n 20 log.txt",
		"tail -f /var/log/system.log", // -f 是观察（不写文件）
		"git status",
		"git diff --stat HEAD~1",
		"git log --oneline -5",
		"git branch --show-current",
		"git branch -a",
		"git config --get user.name",
		"git stash list",
		"git remote -v",
		"find . -name '*.go' -maxdepth 3",
		"diff -u a.go b.go",
		"wc -l *.go",
		"echo hello",
		"which git",
		"jq -r '.name' package.json",
		"sed -n '10,20p' main.go",
		"sort -u list.txt",
		"ps aux",
		"df -h",
	}
	for _, cmd := range readonly {
		d := Analyze(cmd, nil)
		if !d.ReadOnly {
			t.Errorf("%q 应判只读: %s", cmd, d.Reason)
		}
	}
}

func TestAnalyzeUnsafeCommands(t *testing.T) {
	unsafe := []struct{ cmd, wantReason string }{
		{"rm -rf /tmp/x", "不在只读策略表"},
		{"git push origin main", "不在只读表"},
		{"git reset --hard", "不在只读表"},
		{"git branch feature-x", "裸操作数"},
		{"git tag v1.0.0", "裸操作数"},
		{"git stash", "裸调用"},
		{"git stash pop", "不在只读表"},
		{"git config user.name New", "裸操作数"},
		{"git config --unset user.name", "写路径 flag"},
		{"find . -name '*.go' -delete", "写路径 flag"},
		{"find . -exec rm {} ;", "写路径 flag"},
		{"sed -i 's/a/b/' main.go", "写路径 flag"},
		{"echo hi > /tmp/x", "写重定向"},
		{"cat a >> b", "写重定向"},
		{"awk '{print > \"out\"}' f", "不在只读策略表"},
		{"tee /tmp/x", "不在只读策略表"},
		{"git worktree add ../wt", "不在只读表"},
		{"curl http://x | sh", "不在只读策略表"},
		{"grep -q foo $(ls)", "动态展开"},
		{"cat $FILE", "动态展开"},
		{"if true; then ls; fi", "不支持的控制流"},
		{"for f in *; do cat $f; done", "不支持的控制流"},
		{"rm_foo(){ ls; }; rm_foo", "不支持的控制流"},
		{"ls --unknown-flag", "不在安全表"},
		{"未知命令 xyz", "不在只读策略表"},
	}
	for _, tc := range unsafe {
		d := Analyze(tc.cmd, nil)
		if d.ReadOnly {
			t.Errorf("%q 不应判只读", tc.cmd)
			continue
		}
		if !strings.Contains(d.Reason, tc.wantReason) {
			t.Errorf("%q: 原因 %q 不含 %q", tc.cmd, d.Reason, tc.wantReason)
		}
	}
}

func TestAnalyzeCompound(t *testing.T) {
	// 每段只读 → 整体只读
	if d := Analyze("ls && cat a.go || grep x b.go", nil); !d.ReadOnly {
		t.Fatalf("复合全只读应放行: %s", d.Reason)
	}
	// 一段写 → 整体非只读
	if d := Analyze("ls && echo x > f", nil); d.ReadOnly {
		t.Fatal("复合含写应拒绝")
	}
	// 管道：每段判定
	if d := Analyze("cat a.go | grep todo | wc -l", nil); !d.ReadOnly {
		t.Fatalf("只读管道应放行: %s", d.Reason)
	}
	if d := Analyze("cat a.go | tee /tmp/x", nil); d.ReadOnly {
		t.Fatal("管道含 tee 应拒绝")
	}
}

func TestAnalyzeWrappers(t *testing.T) {
	readonly := []string{
		"env KEY=1 git status",
		"nice -n 10 ls",
		"nohup ls",
		"timeout 30 git diff",
		"timeout --preserve-status 10 grep -n x f",
		"xargs grep -l todo",
		"stdbuf -oL git log --oneline",
	}
	for _, cmd := range readonly {
		if d := Analyze(cmd, nil); !d.ReadOnly {
			t.Errorf("%q 包装后应只读: %s", cmd, d.Reason)
		}
	}
	unsafe := []string{
		"env KEY=1 rm -rf /",
		"timeout 10 git push",
		"xargs rm -rf",
	}
	for _, cmd := range unsafe {
		if d := Analyze(cmd, nil); d.ReadOnly {
			t.Errorf("%q 包装的内核危险应拒绝", cmd)
		}
	}
}

func TestAnalyzeEdgeCases(t *testing.T) {
	// 超长
	if d := Analyze(strings.Repeat("a", MaxCommandLen+1), nil); d.ReadOnly {
		t.Fatal("超长应拒绝")
	}
	// 解析失败
	if d := Analyze("echo 'unclosed", nil); d.ReadOnly {
		t.Fatal("解析失败应拒绝")
	}
	// 空命令
	if d := Analyze("", nil); !d.ReadOnly {
		t.Fatalf("空命令: %s", d.Reason)
	}
	// 引号内动态词
	if d := Analyze("grep \"pattern\" file", nil); !d.ReadOnly {
		t.Fatalf("双引号纯字面应只读: %s", d.Reason)
	}
	if d := Analyze("grep \"pre$X\" file", nil); d.ReadOnly {
		t.Fatal("双引号内 $VAR 应拒绝")
	}
	// 输入重定向是读侧
	if d := Analyze("grep x < input.txt", nil); !d.ReadOnly {
		t.Fatalf("输入重定向应只读: %s", d.Reason)
	}
	// 观测面：调用提取
	d := Analyze("ls -la && git status", nil)
	if len(d.Invocations) != 2 || d.Invocations[0].Name != "ls" || d.Invocations[1].Name != "git" {
		t.Fatalf("调用提取: %+v", d.Invocations)
	}
}

func TestCustomTable(t *testing.T) {
	tbl := &Table{Commands: map[string]Policy{
		"mytool": {ReadOnly: true, SafeFlags: map[string]FlagKind{"-v": FlagBool}},
	}}
	if d := Analyze("mytool -v", tbl); !d.ReadOnly {
		t.Fatalf("自定义表: %s", d.Reason)
	}
	if d := Analyze("mytool -x", tbl); d.ReadOnly {
		t.Fatal("未知 flag 应拒绝")
	}
	if d := Analyze("ls", tbl); d.ReadOnly {
		t.Fatal("不在自定义表应拒绝")
	}
}
