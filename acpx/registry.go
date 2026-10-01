package acpx

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Registry agent 注册表：名字 → Agent 实例。
type Registry struct {
	agents map[string]Agent
	order  []string
	// OnRun 每次经本 Registry 发起的运行结束后的回调（nil 安全）。
	// 观测入口：接结构化日志/Prometheus/trace 均可，本包不硬依赖任何观测后端。
	OnRun func(RunEvent)

	mu sync.RWMutex // Register（装配期）与 Run/Get/Names（运行期）并发安全的兜底：
	//              文档仍建议装配期完成注册（热插拔 Agent 不是支持场景）
}

// NewRegistry 创建注册表并注册缺省 agent
// （claude/zcode/codex/opencode/minimax/kimi/gemini/qwen/mimo/pi/dsh，11 家）。
func NewRegistry() *Registry {
	r := &Registry{agents: map[string]Agent{}}
	r.Register(NewClaudeCode())
	r.Register(NewZCode())
	r.Register(NewCodex())
	r.Register(NewOpenCode())
	r.Register(NewMinimax())
	r.Register(NewKimi())
	r.Register(NewGemini())
	r.Register(NewQwen())
	r.Register(NewMimo())
	r.Register(NewPi())
	r.Register(NewDsh())
	return r
}

// Register 注册 agent（重名覆盖）。
func (r *Registry) Register(a Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.agents[a.Name()]; !exists {
		r.order = append(r.order, a.Name())
	}
	r.agents[a.Name()] = a
}

// Get 按名查找。
func (r *Registry) Get(name string) (Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[name]
	return a, ok
}

// Names 返回全部注册名（注册序）。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

// Run 按名执行。入口先做能力校验：请求中 agent 声明不支持的非零字段 →
// fail-fast 报错（请求了即须兑现，拒绝静默降级——直连 Agent.Run 的调用方
// 应自行经 Capabilities 判断）。执行结束后发 RunEvent。
func (r *Registry) Run(ctx context.Context, name string, req RunRequest) (*RunResult, error) {
	a, ok := r.Get(name)
	if !ok {
		r.mu.RLock()
		order := append([]string(nil), r.order...)
		r.mu.RUnlock()
		return nil, fmt.Errorf("acpx: 未注册的 agent %q（可用: %s）", name, strings.Join(order, ","))
	}
	if ignored := UnsupportedFields(a, req); len(ignored) > 0 {
		return nil, fmt.Errorf("acpx: agent %q 不支持请求字段 %s（能力: %s）——请求了即须兑现，拒绝静默降级",
			name, strings.Join(ignored, ","), capsString(a.Capabilities()))
	}
	start := time.Now()
	res, err := a.Run(ctx, req)
	if r.OnRun != nil {
		ev := RunEvent{Agent: name, Duration: time.Since(start), Err: err}
		if res != nil {
			ev.TextLen = len(res.Text)
			ev.Usage = res.Usage
			ev.SessionID = res.SessionID
		}
		r.OnRun(ev)
	}
	return res, err
}
