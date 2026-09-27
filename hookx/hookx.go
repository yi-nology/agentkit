// Package hookx 外部 hook 拦截协议：在关键拦截点派生子进程，stdin 喂 JSON
// 事件载荷、stdout 收 JSON 决策，归并多 hook 结果。对标 ZCode hooks/
// runner.ts + output.ts 的「跨语言 hook 事实标准」形态。
//
// 决策协议（HookOutput）：
//
//	continue:false 或 decision:"block" → 阻断（PreToolUse/PermissionRequest
//	  事件上同时意味着 permission deny；Stop 事件上意味着「继续跑」）
//	decision:"approve" → 权限事件上升格放行（消费方按 permgate 语义消费——
//	  alwaysAsk 不可被 hook 升格，这是调用方纪律）
//	additionalContext / additional_context → 附加上下文累积
//	hookSpecificOutput.updatedInput → 输入改写（消费方必须用改写后的输入
//	  重走权限/许可链——hook 注入的输入不能绕过规则）
//
// 失败语义：hook 进程失败（超时/非零退出/stdout 非 JSON/事件名不符）按
// 「无意见」处理不炸主流程，经 OnError 可观测——扩展点故障不该阻断业务；
// 需要强一致的场景由调用方在 OnError 里自行收紧。子进程环境走 procx.ChildEnv
// 白名单（绝不继承全量密钥）。
package hookx

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"git.enjoye.top/enjoydream/agentkit/procx"
)

// DefaultTimeout 单 hook 执行预算（对标 ZCode 60s）。
const DefaultTimeout = 60 * time.Second

// maxStdout hook stdout 采集上限（限容纪律：决策 JSON 不该超过这个量级）。
const maxStdout = 1 << 20

// Event 拦截点。
type Event string

const (
	EventSessionStart       Event = "session_start"
	EventUserPromptSubmit   Event = "user_prompt_submit"
	EventPreToolUse         Event = "pre_tool_use"
	EventPermissionRequest  Event = "permission_request"
	EventPostToolUse        Event = "post_tool_use"
	EventPostToolUseFailure Event = "post_tool_use_failure"
	EventStop               Event = "stop"
)

// Hook 单个拦截器声明。
type Hook struct {
	// Command 子进程 argv（直传无 shell）。
	Command []string
	// Events 事件匹配："*"（全部）、"|"分隔精确名（"pre_tool_use|post_tool_use"）、
	// "/正则/" 形态（"/.*tool.*/"）。空 = "*"。
	Events string
	// Timeout ≤0 → DefaultTimeout。
	Timeout time.Duration
	// Env 追加透传的环境变量白名单（叠加 procx 基础集）。
	Env []string
}

// Input 派发载荷（stdin JSON）。
type Input struct {
	Event     Event          `json:"event"`
	Tool      string         `json:"tool,omitempty"`
	ToolInput any            `json:"input,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"` // 调用方附带上下文（会话 id 等）
}

// Specific hook 事件专属输出。
type Specific struct {
	HookEventName Event `json:"hookEventName"`
	// UpdatedInput 改写后的工具输入（消费方须以改写输入重走许可链）。
	UpdatedInput any `json:"updatedInput,omitempty"`
}

// Output hook stdout 决策协议（未知字段忽略）。
type Output struct {
	Continue      *bool  `json:"continue,omitempty"`
	StopReason    string `json:"stopReason,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Decision      string `json:"decision,omitempty"` // "approve" | "block"
	SystemMessage string `json:"systemMessage,omitempty"`
	// 附加上下文（两种拼写都收——事实标准里两代拼写并存）。
	AdditionalContext  string    `json:"additionalContext,omitempty"`
	AdditionalContext2 string    `json:"additional_context,omitempty"`
	HookSpecificOutput *Specific `json:"hookSpecificOutput,omitempty"`
}

// RunResult 事件级归并结果（多个 hook 单调合并）。
type RunResult struct {
	AdditionalContexts []string
	// BlockRequested 任一 hook 请求阻断。
	BlockRequested bool
	// PreventContinuation 阻断且该事件不允许继续（PreToolUse/PermissionRequest）。
	PreventContinuation bool
	StopReason          string
	// StopShouldContinue Stop 事件上 hook 否决停止（continue:true）。
	StopShouldContinue bool
	// PermissionBehavior "" | "allow" | "deny"（deny 单调——保守合并）。
	PermissionBehavior string
	// UpdatedInput 最后一个改写输入（nil=未改写）。
	UpdatedInput any
}

// Runner hook 执行器。零值可用（无 hook 时 Run 返回空结果）。
type Runner struct {
	Hooks []Hook
	// OnError hook 执行失败回调（nil=静默容错）。
	OnError func(h Hook, err error)
}

// Run 派发事件：顺序执行全部命中 hook 并归并（顺序确定性——先注册先执行）。
func (r *Runner) Run(ctx context.Context, event Event, payload Input) RunResult {
	var merged RunResult
	for _, h := range r.Hooks {
		if !h.matches(event) {
			continue
		}
		out, err := runHook(ctx, h, payload)
		if err != nil {
			if r.OnError != nil {
				r.OnError(h, err)
			}
			continue
		}
		res := processOutput(event, out)
		merged.merge(res)
	}
	return merged
}

func (h Hook) matches(event Event) bool {
	pattern := strings.TrimSpace(h.Events)
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, "/") && strings.HasSuffix(pattern, "/") && len(pattern) > 2 {
		re, err := regexp.Compile(pattern[1 : len(pattern)-1])
		return err == nil && re.MatchString(string(event))
	}
	for _, name := range strings.Split(pattern, "|") {
		if strings.TrimSpace(name) == string(event) {
			return true
		}
	}
	return false
}

// runHook 派生子进程：stdin 喂载荷、限容收 stdout、超时整组终止——执行纪律
// 经 procx 单源（Setpgid 建组；sh -c 派生的孙进程被杀后仍握管道写端会让裸
// exec 的 Wait 挂到孙进程退出，进程组终止是唯一正确形态）。
func runHook(ctx context.Context, h Hook, payload Input) (*Output, error) {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	stdout, _, _, err := procx.Run(ctx, procx.RunRequest{
		Argv:      h.Command,
		Env:       h.Env,
		Timeout:   timeout,
		MaxStdout: maxStdout,
		Stdin:     payloadJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("hookx: %w", err)
	}
	raw := strings.TrimSpace(stdout)
	if raw == "" {
		return nil, nil // 无输出=无意见
	}
	var out Output
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("hookx: stdout 非 JSON: %v（%s）", err, truncate(raw))
	}
	if out.HookSpecificOutput != nil && out.HookSpecificOutput.HookEventName != payload.Event {
		return nil, fmt.Errorf("hookx: hook 返回了错误的事件名 %q（期望 %q）",
			out.HookSpecificOutput.HookEventName, payload.Event)
	}
	return &out, nil
}

// processOutput 单 hook 输出 → 结果（对标 processHookOutput）。
func processOutput(event Event, out *Output) RunResult {
	var res RunResult
	if out == nil {
		return res
	}
	isPermission := event == EventPermissionRequest || event == EventPreToolUse
	if out.Continue != nil && !*out.Continue && event != EventStop {
		res.BlockRequested = true
		res.StopReason = firstNonEmpty(out.StopReason, out.Reason)
		if isPermission {
			res.PermissionBehavior = "deny"
		}
		res.PreventContinuation = true
	}
	if event == EventStop && out.Continue != nil && *out.Continue {
		res.StopShouldContinue = true
		res.StopReason = firstNonEmpty(out.StopReason, out.Reason)
	}
	if out.Decision == "approve" && isPermission {
		res.PermissionBehavior = "allow"
	}
	if out.Decision == "block" {
		res.BlockRequested = true
		res.StopReason = firstNonEmpty(out.StopReason, out.Reason, out.SystemMessage)
		if isPermission {
			res.PermissionBehavior = "deny"
		}
		res.PreventContinuation = true
		if event == EventStop {
			res.StopShouldContinue = true
			if out.SystemMessage != "" {
				res.AdditionalContexts = append(res.AdditionalContexts, out.SystemMessage)
			}
			if out.Reason != "" {
				res.AdditionalContexts = append(res.AdditionalContexts, out.Reason)
			}
		}
	}
	if out.AdditionalContext != "" {
		res.AdditionalContexts = append(res.AdditionalContexts, out.AdditionalContext)
	}
	if out.AdditionalContext2 != "" {
		res.AdditionalContexts = append(res.AdditionalContexts, out.AdditionalContext2)
	}
	if out.HookSpecificOutput != nil {
		res.UpdatedInput = out.HookSpecificOutput.UpdatedInput
	}
	return res
}

// merge 单调归并：阻断/deny/continue-flags 只能置位不能清除；stopReason 与
// updatedInput 后者胜；上下文累积。
func (t *RunResult) merge(next RunResult) {
	t.AdditionalContexts = append(t.AdditionalContexts, next.AdditionalContexts...)
	if next.BlockRequested {
		t.BlockRequested = true
		if next.StopReason != "" {
			t.StopReason = next.StopReason
		}
	}
	if next.PreventContinuation {
		t.PreventContinuation = true
		if next.StopReason != "" {
			t.StopReason = next.StopReason
		}
	}
	if next.StopShouldContinue {
		t.StopShouldContinue = true
		if next.StopReason != "" {
			t.StopReason = next.StopReason
		}
	}
	if next.PermissionBehavior == "deny" {
		t.PermissionBehavior = "deny" // deny 单调（保守合并）
	} else if t.PermissionBehavior != "deny" && next.PermissionBehavior != "" {
		t.PermissionBehavior = next.PermissionBehavior
	}
	if next.UpdatedInput != nil {
		t.UpdatedInput = next.UpdatedInput
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string) string {
	const cap = 200
	r := []rune(s)
	if len(r) <= cap {
		return s
	}
	return string(r[:cap]) + "…"
}
