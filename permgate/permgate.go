// Package permgate 工具调用许可判定链：谁在什么模式下对什么输入得到
// allow/ask/deny。对标 ZCode permission/service.ts 的优先级状态机——顺序是
// 踩坑固化的，别调换：
//
//	requiresUserInteraction → ask（交互型工具天然要用户）
//	alwaysAsk 硬阻断段 → disallowed/project-deny 仍压过「问」（被硬禁的
//	  工具不能退化成「弹窗一点就能跑」）
//	alwaysAsk 会话授权段 → 命中即放行（钥匙是本会话的授权，不看模式）
//	disallowed → deny
//	project deny → deny
//	yolo → 放行（alwaysAsk 与一切「禁」都在它前面——yolo 跳过的是「问」不是「禁」）
//	project ask → ask
//	只读模式 → 只读工具放行、其余拦下
//	project allow → allow
//	预批钩子 → allow（WebFetch URL 白名单类）
//	配置白名单 → allow
//	兜底 → 只读工具放行，其余 ask
//
// 与 agentkit policy（操作审计门：四模式裁决矩阵）正交：policy 裁「业务操作
// 该不该做」，permgate 裁「这次工具调用要不要人确认」。
//
// 规则内容匹配（对标 matchesRuleContent）：精确 / `prefix:*` 前缀（prefix 或
// prefix+空白开头）/ `*` 通配。匹配对象从工具输入提取（command/url/file_path/
// path/pattern/patch_text 字段，或字符串输入本身）。
package permgate

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Behavior 判定行为。
type Behavior string

const (
	Allow Behavior = "allow"
	Ask   Behavior = "ask"
	Deny  Behavior = "deny"
)

// Mode 会话模式。
type Mode string

const (
	// ModeDefault 正常模式（兜底：只读放行、其余问）。
	ModeDefault Mode = "default"
	// ModeYolo 直通模式（跳过确认——但压不过 alwaysAsk 与硬禁）。
	ModeYolo Mode = "yolo"
	// ModeReadOnly 只读模式（plan 类：只放行只读工具）。
	ModeReadOnly Mode = "read_only"
)

// Capability 工具能力自报（注册期声明）。
type Capability struct {
	// ReadOnly 只读工具（读后无副作用）。
	ReadOnly bool
	// AlwaysAsk 工具自报必须逐次确认（不可被模式/白名单绕过，只可被硬阻断
	// 与会话授权短路）。
	AlwaysAsk bool
	// RequiresUserInteraction 用户交互型工具（AskUserQuestion 类）。
	RequiresUserInteraction bool
}

// Rule 单条许可规则。
type Rule struct {
	// ToolName 工具名；Edit 规则同时匹配 Write（写面等价，对标 ZCode 特例）。
	ToolName string
	// Content 输入内容模式（command/url/路径）：空 = 该工具全部调用；
	// "git status:*" 前缀（空白边界，命令形态专用）；含 * 通配；否则精确等值。
	// ⚠ 路径规则用 wildcard 形态（"/repo/data/*"）而非前缀形态——前缀边界是
	// 空格/制表符，对路径子树形同虚设；路径 subject 匹配前会 filepath.Clean
	//（`..` 穿越不命中），符号链接围栏归执行器。
	Content string
}

// Ruleset 三档规则集。
type Ruleset struct {
	Allow []Rule
	Ask   []Rule
	Deny  []Rule
}

// Decision 判定结果（RuleID 结构化审计）。
type Decision struct {
	Behavior Behavior
	RuleID   string
	Reason   string
}

// Service 许可判定器。并发安全；零值可用（全部走兜底档）。
type Service struct {
	// AllowedTools 配置级白名单（工具名集合）。
	AllowedTools map[string]bool
	// DisallowedTools 配置级黑名单（压过 yolo 与 alwaysAsk 的确认段）。
	DisallowedTools map[string]bool
	// ProjectRules 项目/产品级规则集。
	ProjectRules Ruleset
	// Preapprove 预批钩子（WebFetch URL 白名单类）：返回非空 ruleID 即放行。
	// 判定位次在项目 allow 之后、配置白名单之前（plan 拦截压得过预批）。
	Preapprove func(ctx context.Context, tool string, input any) (ruleID string, ok bool)

	mu           sync.Mutex
	sessionRules Ruleset
}

// Check 判定一次工具调用。input 为工具入参（string 或 map 形态）。
// 分段优先级见包注释——顺序是踩坑固化的，别调换。
func (s *Service) Check(ctx context.Context, tool string, input any, mode Mode, cap Capability) Decision {
	switch {
	case cap.RequiresUserInteraction:
		return s.checkUserInteraction(tool, input)
	case cap.AlwaysAsk:
		return s.checkAlwaysAsk(tool, input)
	default:
		return s.checkStandard(ctx, tool, input, mode, cap)
	}
}

// decision 构造判定结果（RuleID 结构化审计）。
func decision(b Behavior, ruleID, reason string) Decision {
	return Decision{Behavior: b, RuleID: ruleID, Reason: reason}
}

// checkUserInteraction 交互型工具：硬禁与项目 deny 压过交互段——被 deny 的
// 交互型工具不该退化成「弹窗一点就能跑」（与 alwaysAsk 段同纪律）。
func (s *Service) checkUserInteraction(tool string, input any) Decision {
	if s.DisallowedTools[tool] {
		return decision(Deny, "rule.disallowedTools", "工具被显式禁用: "+tool)
	}
	if s.matchRules(&s.ProjectRules.Deny, tool, input) {
		return decision(Deny, "rule.project.deny", "项目规则拒绝: "+tool)
	}
	return decision(Ask, "tool.userInteraction", "交互型工具需要用户参与: "+tool)
}

// checkAlwaysAsk 逐次确认工具：先走硬阻断，再看会话授权，最后问——
// 不可被模式/白名单放行。
func (s *Service) checkAlwaysAsk(tool string, input any) Decision {
	if s.DisallowedTools[tool] {
		return decision(Deny, "rule.disallowedTools", "工具被显式禁用: "+tool)
	}
	if s.matchRules(&s.ProjectRules.Deny, tool, input) {
		return decision(Deny, "rule.project.deny", "项目规则拒绝: "+tool)
	}
	if matchRules(s.sessionRuleSet().Allow, tool, input) {
		return decision(Allow, "rule.session.allow", "本会话已授权: "+tool)
	}
	return decision(Ask, "tool.alwaysAsk", "工具要求逐次确认: "+tool)
}

// checkStandard 常规判定主干：与 ZCode 的历史差异——一切「禁」压过 yolo
// （其源码注释自述 yolo 先于硬禁是兼容遗留——新装配没有这个包袱）。
// yolo 跳过的是「问」，不是「禁」。
func (s *Service) checkStandard(ctx context.Context, tool string, input any, mode Mode, cap Capability) Decision {
	switch {
	case s.DisallowedTools[tool]:
		return decision(Deny, "rule.disallowedTools", "工具被显式禁用: "+tool)
	case s.matchRules(&s.ProjectRules.Deny, tool, input):
		return decision(Deny, "rule.project.deny", "项目规则拒绝: "+tool)
	case mode == ModeYolo:
		return decision(Allow, "mode.yolo", "yolo 模式直通")
	case s.matchRules(&s.ProjectRules.Ask, tool, input):
		return decision(Ask, "rule.project.ask", "项目规则要求确认: "+tool)
	case mode == ModeReadOnly:
		if cap.ReadOnly {
			return decision(Allow, "mode.read_only", "只读模式放行只读工具")
		}
		return decision(Deny, "mode.read_only", "只读模式拦下写入: "+tool)
	case s.matchRules(&s.ProjectRules.Allow, tool, input):
		return decision(Allow, "rule.project.allow", "项目规则放行: "+tool)
	}
	if ruleID, ok := s.preapproved(ctx, tool, input); ok {
		return decision(Allow, ruleID, "预批放行: "+tool)
	}
	return s.checkFallback(tool, input, cap)
}

// checkFallback 兜底段：配置白名单 → 会话授权 → 只读放行 → 默认确认。
func (s *Service) checkFallback(tool string, input any, cap Capability) Decision {
	switch {
	case s.AllowedTools[tool]:
		return decision(Allow, "rule.allowedTools", "白名单放行: "+tool)
	case matchRules(s.sessionRuleSet().Allow, tool, input):
		// 会话授权同样覆盖普通工具的 ask 兜底（比 ZCode 只在 alwaysAsk 段查
		// 会话规则更完整——用户点了「总是允许」不该再被问）
		return decision(Allow, "rule.session.allow", "本会话已授权: "+tool)
	case cap.ReadOnly:
		return decision(Allow, "cap.readOnly", "只读工具放行")
	default:
		return decision(Ask, "mode.default", "默认确认: "+tool)
	}
}

func (s *Service) preapproved(ctx context.Context, tool string, input any) (string, bool) {
	if s.Preapprove == nil {
		return "", false
	}
	ruleID, ok := s.Preapprove(ctx, tool, input)
	return ruleID, ok && ruleID != ""
}

// AllowForSession 会话级授权登记（用户对 ask 选择「本会话总是允许」时调用）。
func (s *Service) AllowForSession(tool, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionRules.Allow = append(s.sessionRules.Allow, Rule{ToolName: tool, Content: content})
}

// ResetSession 会话结束清空会话授权（实例消亡语义的显式形态）。
func (s *Service) ResetSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionRules = Ruleset{}
}

func (s *Service) sessionRuleSet() *Ruleset {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.sessionRules
	return &r
}

// matchRules 任一规则命中即 true。
func (s *Service) matchRules(rules *[]Rule, tool string, input any) bool {
	return matchRules(*rules, tool, input)
}

func matchRules(rules []Rule, tool string, input any) bool {
	for _, r := range rules {
		if matchToolName(r.ToolName, tool) && matchRuleContent(r.Content, subjects(input, tool)) {
			return true
		}
	}
	return false
}

func matchToolName(ruleTool, tool string) bool {
	if ruleTool == tool {
		return true
	}
	// 写面等价：Edit 规则匹配 Write（对标 ZCode 同款特例）
	return tool == "Write" && ruleTool == "Edit"
}

// subjectKeys 工具输入里承载「匹配对象」的字段名（与 ruleSubjects 清单同步）。
// file_path/path 类 subject 先 filepath.Clean 再匹配：wildcard 路径规则
// （"/repo/data/*"）可被 `..` 穿越与重复分隔符绕过，Clean 后
// "/repo/data/../../etc/passwd" 归一为 "/etc/passwd"，不再误命中（fail-closed；
// 第六轮审计）。符号链接绕过须执行器侧 EvalSymlinks 治理——本包不做文件系统
// 访问，保持纯函数判定。
var subjectKeys = []string{"command", "url", "file_path", "path", "pattern", "patch_text"}

// pathSubjectKeys 匹配前须 Clean 的路径类字段。
var pathSubjectKeys = map[string]bool{"file_path": true, "path": true}

// subjects 从工具输入提取匹配对象（对标 ruleSubjects 的字段清单）。
func subjects(input any, tool string) []string {
	switch v := input.(type) {
	case string:
		return []string{v}
	case map[string]any:
		for _, key := range subjectKeys {
			if s, ok := v[key].(string); ok && s != "" {
				if pathSubjectKeys[key] {
					s = filepath.Clean(s)
				}
				return []string{s}
			}
		}
	}
	return nil
}

// matchRuleContent 内容模式匹配：空模式=全匹配；`p:*` 前缀（含空白边界）；
// 含 * 通配；否则精确。
func matchRuleContent(content string, subs []string) bool {
	if content == "" {
		return true
	}
	if len(subs) == 0 {
		return false
	}
	prefix := strings.TrimSuffix(content, ":*")
	isPrefix := prefix != content
	for _, sub := range subs {
		switch {
		case isPrefix && (sub == prefix || strings.HasPrefix(sub, prefix+" ") || strings.HasPrefix(sub, prefix+"\t")):
			return true
		case !isPrefix && strings.Contains(content, "*") && wildcardMatch(content, sub):
			return true
		case !isPrefix && sub == content:
			return true
		}
	}
	return false
}

var wildcardCache sync.Map // string → *regexp.Regexp

// wildcardMatch `*` 通配匹配（* 跨任意字符；其余字面）。
func wildcardMatch(pattern, s string) bool {
	reAny, ok := wildcardCache.Load(pattern)
	re, _ := reAny.(*regexp.Regexp)
	if !ok || re == nil {
		quoted := regexp.QuoteMeta(pattern)
		re = regexp.MustCompile("^" + strings.ReplaceAll(quoted, `\*`, ".*") + "$")
		wildcardCache.Store(pattern, re)
	}
	return re.MatchString(s)
}
