// Package bashguard bash 命令静态风险解析：不执行命令，经真 shell AST
// （mvdan.cc/sh）解析成调用序列，按策略表判定只读性。对标 ZCode
// bash-command-parser.ts + bash-readonly-policy 的「权限判定不执行命令」。
//
// 判定原则（保守到宁可误报）：
//   - 解析失败、含动态词（$() / 反引号 / 未展开 $VAR）、含写重定向、
//     复合控制流（if/for/while/case/函数定义）→ 一律非只读——无法静态证明
//     安全的就不放行；
//   - 调用按策略表：命令不在表=未知（非只读）；命中危险 flag=非只读；
//     未知 flag=非只读；值型 flag 吃一个参数后继续；
//   - 安全包装（env/nice/nohup/time/timeout/stdbuf/xargs）剥壳后判定内核；
//   - 复合命令（&& || ; |）每一段都必须只读，整体才只读。
//
// 上限 10K 字符（对标 ZCode：超长命令不进解析器）。
package bashguard

import (
	"fmt"
	"strings"

	mvdan "mvdan.cc/sh/v3/syntax"
)

// MaxCommandLen 解析上限。
const MaxCommandLen = 10 * 1024

// FlagKind 值型 flag 的参数形态。
type FlagKind string

const (
	FlagBool     FlagKind = "bool"     // 不吃参数
	FlagString   FlagKind = "string"   // 吃一个任意字面参数
	FlagNumber   FlagKind = "number"   // 吃一个数值参数
	FlagOptional FlagKind = "optional" // 可带可不带参数（按字面形态分辨）
)

// Policy 单命令策略。
type Policy struct {
	// ReadOnly 命令本体是否只读。
	ReadOnly bool
	// SafeFlags 已知安全 flag（含 - 前缀，如 "-n"）。长 flag 单写。
	SafeFlags map[string]FlagKind
	// UnsafeFlags 命中即非只读（显式优先于 SafeFlags）。
	UnsafeFlags []string
	// Subcommands 子命令策略表（git 形态，可嵌套）：子命令不在表=非只读。
	Subcommands map[string]Policy
	// Wrapper 安全包装（env/timeout 形态）：剥壳后按剩余首参判定。
	Wrapper bool
	// ConsumeBare Wrapper 剥 flag 后先吃掉的前置裸操作数个数（timeout 的时长）。
	ConsumeBare int
	// NoBareOperands 裸操作数即写形态（git branch <名> 创建 / git stash 裸 push /
	// git tag <名> 打标）：出现任何 flag 之外的裸操作数 → 非只读。
	NoBareOperands bool
}

// Table 策略表。
type Table struct {
	Commands map[string]Policy
}

// Invocation 提取的单次调用（观测面）。
type Invocation struct {
	Name    string
	Args    []string
	Dynamic bool // 含动态词（argv 非字面）
}

// Decision 判定结果。
type Decision struct {
	// ReadOnly 命令可判只读（不执行任何写路径）。
	ReadOnly bool
	// Reason 非只读原因（人可读，审计用）。
	Reason string
	// Invocations 提取的调用序列（观测/风险面）。
	Invocations []Invocation
}

// Analyze 静态解析与判定。超长/解析失败返回非只读（保守）。
func Analyze(cmdStr string, t *Table) Decision {
	if t == nil {
		t = DefaultTable()
	}
	if len(cmdStr) > MaxCommandLen {
		return Decision{Reason: "命令超长（>10K），不进解析器"}
	}
	parser := mvdan.NewParser(mvdan.Variant(mvdan.LangBash))
	f, err := parser.Parse(strings.NewReader(cmdStr), "")
	if err != nil {
		return Decision{Reason: fmt.Sprintf("解析失败: %v", err)}
	}
	d := Decision{ReadOnly: true}
	for _, stmt := range f.Stmts {
		if reason := t.evalStmt(stmt, &d.Invocations); reason != "" {
			d.ReadOnly = false
			d.Reason = reason
			return d
		}
	}
	return d
}

// evalStmt 评估单个语句；返回空串=只读，非空=非只读原因。
func (t *Table) evalStmt(stmt *mvdan.Stmt, invocations *[]Invocation) string {
	if reason := redirectsReason(stmt.Redirs); reason != "" {
		return reason
	}
	return t.evalCmd(stmt.Cmd, invocations)
}

// readRedirOps 读侧重定向（不产生写）：< 、<< heredoc、<<< word-heredoc。
var readRedirOps = map[mvdan.RedirOperator]bool{
	mvdan.RdrIn: true, mvdan.Hdoc: true, mvdan.DashHdoc: true, mvdan.WordHdoc: true,
}

func redirectsReason(redirs []*mvdan.Redirect) string {
	for _, r := range redirs {
		if readRedirOps[r.Op] {
			continue // 输入侧重定向是读
		}
		return fmt.Sprintf("写重定向 op=%d", int(r.Op))
	}
	return ""
}

func (t *Table) evalCmd(node mvdan.Command, invocations *[]Invocation) string {
	switch c := node.(type) {
	case *mvdan.CallExpr:
		return t.evalCall(c, invocations)
	case *mvdan.BinaryCmd:
		if reason := t.evalStmt(c.X, invocations); reason != "" {
			return reason
		}
		return t.evalStmt(c.Y, invocations)
	case *mvdan.Subshell:
		for _, s := range c.Stmts {
			if reason := t.evalStmt(s, invocations); reason != "" {
				return reason
			}
		}
		return ""
	case *mvdan.Block:
		for _, s := range c.Stmts {
			if reason := t.evalStmt(s, invocations); reason != "" {
				return reason
			}
		}
		return ""
	default:
		return fmt.Sprintf("不支持的控制流 %T（if/for/while/case/函数等无法静态证明只读）", node)
	}
}

// evalCall 单调用判定：字面化 argv → 剥安全包装 → 策略表。
func (t *Table) evalCall(call *mvdan.CallExpr, invocations *[]Invocation) string {
	var argv []string
	dynamic := false
	for _, w := range call.Args {
		s, dyn := literalWord(w)
		if dyn {
			dynamic = true
		}
		argv = append(argv, s)
	}
	// 命令名本身动态 = 无法判定目标
	name := ""
	if len(call.Args) > 0 {
		n, dyn := literalWord(call.Args[0])
		if dyn {
			return "命令名含动态展开（$()/反引号/$VAR），无法静态判定"
		}
		name = n
		argv = argv[1:]
	} else if len(call.Assigns) > 0 {
		return "纯环境赋值语句（VAR=…）不带调用，无法证明后续用途"
	}
	if dynamic {
		invocationsAdd(invocations, Invocation{Name: name, Args: argv, Dynamic: true})
		return fmt.Sprintf("参数含动态展开（%s …），无法静态判定", name)
	}
	invocationsAdd(invocations, Invocation{Name: name, Args: argv})

	return t.evalInvocation(name, argv)
}

// evalInvocation 表驱动判定（递归剥包装）。
func (t *Table) evalInvocation(name string, argv []string) string {
	pol, ok := t.Commands[name]
	if !ok {
		return fmt.Sprintf("命令 %q 不在只读策略表", name)
	}
	if !pol.ReadOnly && !pol.Wrapper {
		return fmt.Sprintf("命令 %q 策略为非只读", name)
	}
	if pol.Wrapper {
		// 剥壳：跳过包装自身的 flag 与 KEY=VAL 赋值，判定内核命令
		rest := stripWrapper(argv, pol)
		if len(rest) == 0 {
			return "" // 纯包装无内核（env 裸跑=打印环境）
		}
		return t.evalInvocation(rest[0], rest[1:])
	}
	return evalPolicy(name, argv, pol)
}

// stripWrapper 剥包装参数：KEY=VAL 赋值、本包装已声明的安全 flag、ConsumeBare
// 个前置裸操作数（timeout 的时长）。
func stripWrapper(argv []string, pol Policy) []string {
	rest := argv
	for len(rest) > 0 {
		a := rest[0]
		if strings.Contains(a, "=") && !strings.HasPrefix(a, "-") {
			rest = rest[1:]
			continue
		}
		if kind, ok := pol.SafeFlags[a]; ok {
			rest = rest[1:]
			if kind != FlagBool && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
				rest = rest[1:] // 值型 flag 吃参
			}
			continue
		}
		break
	}
	for i := 0; i < pol.ConsumeBare && len(rest) > 0; i++ {
		rest = rest[1:]
	}
	return rest
}

// evalPolicy 策略执行：危险 flag 显式优先 → 子命令先剥离（子命令的 flag 归
// 子命令策略）→ 白名单 flag 消费（未知即拒）→ 裸操作数守卫。
func evalPolicy(name string, argv []string, pol Policy) string {
	for _, a := range argv {
		for _, u := range pol.UnsafeFlags {
			if a == u || strings.HasPrefix(a, u+"=") {
				return fmt.Sprintf("%s 的 %s 是写路径 flag", name, u)
			}
		}
	}
	if len(pol.Subcommands) > 0 {
		// 先定位子命令 token（值型 flag 吃参后首个非 flag 项），其后整段交给
		// 子命令策略——git log --oneline 的 --oneline 属于 log 不属于 git
		sub, rest, giReason := splitSubcommand(argv, pol)
		if giReason != "" {
			return giReason
		}
		if sub == "" {
			if pol.NoBareOperands {
				return name + " 裸调用是写形态（如 stash 默认 push）"
			}
			return ""
		}
		subPol, ok := pol.Subcommands[sub]
		if !ok {
			return fmt.Sprintf("%s 子命令 %q 不在只读表", name, sub)
		}
		if !subPol.ReadOnly && !subPol.Wrapper {
			return fmt.Sprintf("%s 子命令 %q 非只读", name, sub)
		}
		if subPol.Wrapper {
			return "嵌套包装子命令不支持"
		}
		return evalPolicy(name+" "+sub, rest, subPol)
	}
	operands := []string{}
	i := 0
	afterSep := false
	for i < len(argv) {
		a := argv[i]
		if afterSep || !strings.HasPrefix(a, "-") || a == "-" {
			operands = append(operands, a)
			i++
			continue
		}
		if a == "--" { // -- 之后全是操作数
			afterSep = true
			i++
			continue
		}
		if ok, kind := flagLookup(a, pol); ok {
			i++
			if kind != FlagBool && !strings.Contains(a, "=") &&
				i < len(argv) && !strings.HasPrefix(argv[i], "-") {
				i++ // 值参数
			}
			continue
		}
		return fmt.Sprintf("%s 的 flag %q 不在安全表", name, a)
	}
	if pol.NoBareOperands && len(operands) > 0 {
		return fmt.Sprintf("%s 带裸操作数 %q 是写形态（创建分支/标签/弹栈等）", name, operands[0])
	}
	return ""
}

// flagLookup 单 flag 解析：精确命中；`--flag=value` 形态（optional/string 型，
// 值内联不吃下一参）；联合短 flag（-rn）：全部组成字符必须各自是 FlagBool 且
// 不在 UnsafeFlags——值型 flag 折进联合 flag 后其参数会伪装成操作数（GNU getopt
// 下 sort -ro f = -r -o f 是写路径），这是曾经的真实漏判面。
func flagLookup(a string, pol Policy) (bool, FlagKind) {
	if kind, ok := pol.SafeFlags[a]; ok {
		return true, kind
	}
	// --flag=value：值内联，仅 optional/string 型放行
	if base, val, found := strings.Cut(a, "="); found && strings.HasPrefix(base, "--") {
		if kind, ok := pol.SafeFlags[base]; ok && (kind == FlagOptional || kind == FlagString) {
			_ = val
			return true, FlagBool // 值已内联，不吃下一参
		}
		return false, ""
	}
	// 联合短 flag：全组成 bool 才放行
	if !strings.HasPrefix(a, "--") && len(a) > 2 && strings.HasPrefix(a, "-") {
		for _, u := range pol.UnsafeFlags {
			if u != "" && strings.HasPrefix(a, u) {
				return false, ""
			}
		}
		allBool := true
		for _, ch := range a[1:] {
			kind, ok := pol.SafeFlags["-"+string(ch)]
			if !ok || kind != FlagBool {
				allBool = false
				break
			}
		}
		if allBool {
			return true, FlagBool
		}
	}
	return false, ""
}

// splitSubcommand 在子命令策略表中定位子命令：按本层 flag 白名单消费（值型
// 吃参）直到首个非 flag token。返回子命令名与其后整段参数；无子命令返回空名。
func splitSubcommand(argv []string, pol Policy) (sub string, rest []string, reason string) {
	i := 0
	for i < len(argv) {
		a := argv[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			return a, argv[i+1:], ""
		}
		if a == "--" {
			if i+1 < len(argv) {
				return argv[i+1], argv[i+2:], ""
			}
			return "", nil, ""
		}
		if ok, kind := flagLookup(a, pol); ok {
			i++
			if kind != FlagBool && !strings.Contains(a, "=") &&
				i < len(argv) && !strings.HasPrefix(argv[i], "-") {
				i++
			}
			continue
		}
		return "", nil, fmt.Sprintf("flag %q 不在本层安全表", a)
	}
	return "", nil, ""
}

// literalWord 字面化一个词：全字面返回 (text,false)；含展开返回 (拼到一半的文本,true)。
func literalWord(w *mvdan.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *mvdan.Lit:
			b.WriteString(p.Value)
		case *mvdan.SglQuoted:
			b.WriteString(p.Value)
		case *mvdan.DblQuoted:
			for _, ip := range p.Parts {
				if lit, ok := ip.(*mvdan.Lit); ok {
					b.WriteString(lit.Value)
				} else {
					b.WriteString("<dynamic>")
					return b.String(), true
				}
			}
		default:
			// ParamExp / CmdSubst / ArithmExp 等：动态
			b.WriteString("<dynamic>")
			return b.String(), true
		}
	}
	return b.String(), false
}

func invocationsAdd(invocations *[]Invocation, inv Invocation) {
	*invocations = append(*invocations, inv)
}
