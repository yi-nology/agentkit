// Package sysprompt system prompt 分段组装：Section 化收集 → stable/dynamic
// 边界分块 → 计量。对标 ZCode context/builder.ts。
//
// 为什么分段：provider 的前缀缓存按「消息前缀相同」命中——身份/行为约束等
// **stable** 段放前且内容不变，缓存持续命中；环境信息/git 快照/日期等
// **dynamic** 段隔离在后，变化时不击穿前面的缓存。字符串拼接式组装没有
// 边界概念，每次注入都全量失效。
//
// 边界约定：块内内容组装后即冻结（Build 产物只读）；dynamic 段内容每次会话
// 重建时可以变，stable 段必须字节级不变（含空行）——调用方纪律，本包保证
// 排序与计量确定性。
//
// 用法：
//
//	b := &sysprompt.Builder{}
//	b.Add(sysprompt.IdentitySection("你是审查助手…"))            // stable
//	b.Add(sysprompt.EnvSection(env))                             // dynamic
//	b.Add(sysprompt.DateSection(time.Now()))                     // user_context
//	res := b.Build()
//	// res.System[0] = stable 块（缓存断点），res.System[1] = dynamic 块
//	// res.UserContext = 用户消息前缀的附加上下文（含「未必相关」免责句）
package sysprompt

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/yi-nology/agentkit/procx"
)

// token 估算口径（与 compact 包一致：字符数/3）。
const tokenCharDivisor = 3

// Boundary 缓存边界。
type Boundary string

const (
	// Stable 稳定段：会话期间字节级不变（身份/行为约束），排前保缓存命中。
	Stable Boundary = "stable"
	// Dynamic 易变段：环境快照/git 状态/日期等，隔离在后避免击穿前缀缓存。
	Dynamic Boundary = "dynamic"
)

// Target 注入位置。
type Target string

const (
	// TargetSystem system 消息块。
	TargetSystem Target = "system"
	// TargetUserContext 用户消息前缀的附加上下文（对齐 ZCode meta_user：
	// 「可能相关也可能无关」的参考材料不占 system 预算）。
	TargetUserContext Target = "user_context"
)

// Section 一段提示词内容（自计量：Chars/Tokens 组装即算，供预算观测）。
type Section struct {
	Name     string
	Target   Target
	Boundary Boundary
	Content  string
	Chars    int
	Tokens   int
}

// NewSection 段构造：target 空落 TargetSystem，boundary 空落 Dynamic（缺省
// 保守——没想清楚稳定性就别进缓存前缀）。
func NewSection(name string, target Target, boundary Boundary, content string) Section {
	if target == "" {
		target = TargetSystem
	}
	if boundary == "" {
		boundary = Dynamic
	}
	chars := len([]rune(content))
	return Section{
		Name:     name,
		Target:   target,
		Boundary: boundary,
		Content:  content,
		Chars:    chars,
		Tokens:   (chars + tokenCharDivisor - 1) / tokenCharDivisor,
	}
}

// IdentitySection 身份/行为约束段（stable——本包唯一约定为 stable 的内置段，
// 其余内置段全 dynamic；调用方自定义段自行声明）。
func IdentitySection(body string) Section {
	return NewSection("Identity", TargetSystem, Stable, strings.TrimSpace(body))
}

// Block 一个 system 消息块（调用方映射为 provider 消息 + 缓存断点标注）。
type Block struct {
	Boundary Boundary
	Content  string
}

// Result 组装产物。
type Result struct {
	// Sections 排序后的全量段（system-stable → system-dynamic →
	// user_context-stable → user_context-dynamic；组内保持插入序——确定性）。
	Sections []Section
	// System system 块序列：stable 块在前、dynamic 块在后（空块跳过）。
	System []Block
	// UserContext 用户消息前缀附加文本（无 user_context 段时为空串）。
	UserContext string
	// TotalChars / TotalTokens 全段计量。
	TotalChars  int
	TotalTokens int
}

// DefaultUserContextIntro 附加上下文的引导句缺省。
const DefaultUserContextIntro = "回答用户问题时可参考以下上下文："

// DefaultUserContextDisclaimer 附加上下文的免责句（对齐 ZCode：明示材料未必
// 相关，防止模型对参考材料过度反应）。
const DefaultUserContextDisclaimer = "重要：这些上下文未必与当前任务相关，除非高度相关，否则不要针对它们作答。"

// Builder 分段收集器。零值可用。
type Builder struct {
	sections []Section
	// UserContextIntro / UserContextDisclaimer 附加上下文引导/免责句覆写
	// （空 = 缺省常量）。
	UserContextIntro      string
	UserContextDisclaimer string
}

// Add 收集一段（重复调用按插入序追加；段名仅观测用，不查重）。
func (b *Builder) Add(s Section) *Builder {
	b.sections = append(b.sections, s)
	return b
}

// Build 组装：排序（stable 先于 dynamic，system 先于 user_context）→ 分块 →
// 计量。产物不引用入参切片（Build 后再 Add 不影响已产出的 Result）。
func (b *Builder) Build() Result {
	pick := func(t Target, bd Boundary) []Section {
		var out []Section
		for _, s := range b.sections {
			if s.Target == t && s.Boundary == bd {
				out = append(out, s)
			}
		}
		return out
	}
	res := Result{Sections: make([]Section, 0, len(b.sections))}
	res.Sections = append(res.Sections, pick(TargetSystem, Stable)...)
	res.Sections = append(res.Sections, pick(TargetSystem, Dynamic)...)
	res.Sections = append(res.Sections, pick(TargetUserContext, Stable)...)
	res.Sections = append(res.Sections, pick(TargetUserContext, Dynamic)...)
	for _, s := range res.Sections {
		res.TotalChars += s.Chars
		res.TotalTokens += s.Tokens
	}
	if c := joinContent(pick(TargetSystem, Stable)); c != "" {
		res.System = append(res.System, Block{Boundary: Stable, Content: c})
	}
	if c := joinContent(pick(TargetSystem, Dynamic)); c != "" {
		res.System = append(res.System, Block{Boundary: Dynamic, Content: c})
	}
	var uc []string
	if c := joinContent(pick(TargetUserContext, Stable)); c != "" {
		uc = append(uc, c)
	}
	if c := joinContent(pick(TargetUserContext, Dynamic)); c != "" {
		uc = append(uc, c)
	}
	if len(uc) > 0 {
		intro := b.UserContextIntro
		if intro == "" {
			intro = DefaultUserContextIntro
		}
		disc := b.UserContextDisclaimer
		if disc == "" {
			disc = DefaultUserContextDisclaimer
		}
		res.UserContext = intro + "\n\n" + strings.Join(uc, "\n\n") + "\n\n" + disc
	}
	return res
}

func joinContent(sections []Section) string {
	if len(sections) == 0 {
		return ""
	}
	parts := make([]string, 0, len(sections))
	for _, s := range sections {
		if strings.TrimSpace(s.Content) != "" {
			parts = append(parts, s.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

// ---- 内置段构建器（数据驱动、确定性；采集见 DetectEnv） ----

// EnvInfo 运行环境快照。
type EnvInfo struct {
	Cwd       string
	IsGitRepo bool
	Platform  string // runtime.GOOS
	Shell     string // $SHELL
	OSVersion string // 采集可得则填（uname），缺省留空
	ModelID   string // "provider/model" 展示（可空）
}

// EnvSection 环境信息段（dynamic——工作目录/模型都可能换）。
func EnvSection(info EnvInfo) Section {
	lines := []string{"# Environment", "You have been invoked in the following environment:",
		"- Primary working directory: " + info.Cwd,
		fmt.Sprintf("- Is a git repository: %s", yesNo(info.IsGitRepo)),
		"- Platform: " + info.Platform,
		"- Shell: " + info.Shell,
		"- OS Version: " + info.OSVersion,
	}
	if info.ModelID != "" {
		lines = append(lines, "- You are powered by the model named "+info.ModelID+".")
	}
	return NewSection("Environment Info", TargetSystem, Dynamic, strings.Join(lines, "\n"))
}

// GitInfo git 仓快照。
type GitInfo struct {
	Branch        string
	MainBranch    string // PR 通常基准的分支（可空）
	User          string
	StatusLines   []string // git status --porcelain -b 输出（空+非 unknown = clean）
	StatusUnknown bool
	RecentCommits []string
}

// GitSection git 快照段（dynamic）。必须带「会话开始时的快照」免责——模型对
// 过期状态自信是真实事故面（对标 ZCode 同款措辞）。
func GitSection(g GitInfo) Section {
	var b strings.Builder
	b.WriteString("gitStatus: This is the git status at the start of the conversation. " +
		"Note that this status is a snapshot in time, and will not update during the conversation.")
	if g.Branch != "" {
		b.WriteString("\n\nCurrent branch: " + g.Branch)
	}
	if g.MainBranch != "" {
		b.WriteString("\n\nMain branch (you will usually use this for PRs): " + g.MainBranch)
	}
	if g.User != "" {
		b.WriteString("\n\nGit user: " + g.User)
	}
	status := "(unknown)"
	switch {
	case g.StatusUnknown:
	case len(g.StatusLines) > 0:
		status = strings.Join(g.StatusLines, "\n")
	default:
		status = "(clean)"
	}
	b.WriteString("\n\nStatus:\n" + status)
	if len(g.RecentCommits) > 0 {
		b.WriteString("\n\nRecent commits:\n" + strings.Join(g.RecentCommits, "\n"))
	}
	return NewSection("Git Snapshot", TargetSystem, Dynamic, b.String())
}

// DateSection 当前日期段（user_context——日期是参考信息，不进 system）。
func DateSection(now time.Time) Section {
	return NewSection("Current Date", TargetUserContext, Dynamic,
		"# currentDate\nToday's date is "+now.Format("2006-01-02")+".")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// ---- 运行期采集 ----

// DetectEnv 采集环境快照与 git 仓信息（git 子命令经 procx 纪律执行：argv 直传、
// 超时 5s、限容采集）。dir 非 git 仓时 GitInfo 返回 nil 不报错；git 不可用同。
// OSVersion 经 uname -s -r 采集（非 unix 平台留空）。
func DetectEnv(ctx context.Context, dir string) (EnvInfo, *GitInfo, error) {
	info := EnvInfo{
		Cwd:      dir,
		Platform: runtime.GOOS,
		Shell:    shellOf(),
	}
	if v, err := gitOut(ctx, dir, "rev-parse", "--is-inside-work-tree"); err == nil {
		info.IsGitRepo = strings.TrimSpace(v) == "true"
	}
	if runtime.GOOS != "windows" {
		if out, _, _, err := procx.Run(ctx, procx.RunRequest{
			Argv: []string{"uname", "-s", "-r"}, Timeout: 5 * time.Second, MaxStdout: 1 << 10,
		}); err == nil {
			info.OSVersion = strings.TrimSpace(out)
		}
	}
	if !info.IsGitRepo {
		return info, nil, nil
	}
	g := &GitInfo{}
	if v, err := gitOut(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		g.Branch = strings.TrimSpace(v)
	}
	if v, err := gitOut(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		// refs/remotes/origin/HEAD → origin/main 形态取 main
		g.MainBranch = strings.TrimPrefix(strings.TrimSpace(v), "origin/")
	}
	if v, err := gitOut(ctx, dir, "config", "user.name"); err == nil {
		g.User = strings.TrimSpace(v)
	}
	if v, err := gitOut(ctx, dir, "status", "--porcelain=v1", "-b"); err == nil {
		lines := strings.Split(strings.TrimRight(v, "\n"), "\n")
		var status []string
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				status = append(status, l)
			}
		}
		// 首行 ## branch... 是分支元信息，状态行才是工作区状态
		g.StatusLines = status
		if len(status) <= 1 {
			g.StatusLines = nil // 仅 ## 行 = clean
		}
	} else {
		g.StatusUnknown = true
	}
	if v, err := gitOut(ctx, dir, "log", "--oneline", "-10"); err == nil {
		for _, l := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
			if strings.TrimSpace(l) != "" {
				g.RecentCommits = append(g.RecentCommits, l)
			}
		}
	}
	return info, g, nil
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	out, _, code, err := procx.Run(ctx, procx.RunRequest{
		Argv:    append([]string{"git", "-C", dir}, args...),
		Timeout: 5 * time.Second, MaxStdout: 1 << 16,
	})
	if err != nil || code != 0 {
		return "", fmt.Errorf("git %v: exit=%d err=%v", args, code, err)
	}
	return out, nil
}

func shellOf() string {
	if s := strings.TrimSpace(os.Getenv("SHELL")); s != "" {
		return s
	}
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}
