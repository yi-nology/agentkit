# agentkit

AI Agent 开发工具箱 —— 从 Argus 代码审查平台提取的通用组件库。

## 模块路径

```
git.enjoye.top/enjoydream/agentkit
```

## 包清单

| 包 | 说明 | 外部依赖 |
|---|---|---|
| `acpx` | CLI 编码 agent 统一调用（claude/zcode/codex/opencode/minimax） | eino |
| `llm` | LLM 客户端（重试/限速/预算/fitInput/JSON + Resilient 降级链） | eino, x/time |
| `breaker` | 熔断器（closed→open→half-open） | 无 |
| `worker` | DB 即队列 worker pool（心跳/优雅停机） | ekit |
| `knowledge/rag` | 本地 RAG（markdown 检索 + eino tool 适配） | eino |
| `progress` | 泛型事件总线 `Bus[T]` | ekit |
| `skill` | 内容/方法论解析器（SKILL.md 等） | 无 |
| `severity` | 严重级别归一化 + 指纹 + glob 匹配 | 无 |
| `safejson` | Markdown/HTML 反注入 | 无 |
| `audit` | 审计日志 | ekit |
| `textutil` | 文本截断等工具 | 无 |
| `workcopy` | Git 工作副本沙箱（singleflight + TTL 回收） | ekit, x/sync |

## 快速使用

### ACPX —— 调用 CLI 编码 agent

```go
import "git.enjoye.top/enjoydream/agentkit/acpx"

// 缺省注册 9 家：claude/zcode/codex/opencode/minimax/kimi/gemini/qwen/mimo
reg := acpx.NewRegistry()

// 直接调用
res, err := reg.Run(ctx, "codex", acpx.RunRequest{
    Prompt:  "修复 utils.go 中的空指针 bug 并补测试",
    WorkDir: "/path/to/repo",
    Sandbox: acpx.SandboxWorkspace,
})
fmt.Println(res.Text, res.Usage)

// 流式事件
reg.Run(ctx, "claude", acpx.RunRequest{
    Prompt:  "重构 auth 模块",
    OnEvent: func(e acpx.Event) { fmt.Println(e.Type, e.Text) },
})

// 包成 eino 工具挂进 ReAct agent（LLM 自主决定调哪个 agent）
tool := reg.AsTool() // run_coding_agent(agent, prompt, work_dir)
```

各家协议由专用适配器处理（参数均经官方文档核实）：
Claude Code/ZCode（`-p --output-format stream-json`）、
Codex（`exec --json --output-last-message`）、
opencode（`run --json`）、
Kimi（`--print -p --output-format=stream-json`，会话 `--session`）、
Gemini/Qwen（`-p --output-format json` + `--approval-mode`）。
MiMo/MiniMax 等协议未稳定 CLI 走 `GenericAgent` argv 模板（`{prompt}`/`{model}`/`{session}`），
官方稳定后一行换专用适配器。

### LLM 客户端

```go
import "git.enjoye.top/enjoydream/agentkit/llm"

client := llm.NewClient(chatModel, "deepseek-v4", myBudget)
client.OnUsage = func(stage string, p, c int) { /* Prometheus 记账 */ }
client.Limiter = rate.NewLimiter(10, 20)
client.ContextTokens = 1_000_000
client.MaxOutputTokens = 50_000

// 普通生成（带重试 + 限速 + fitInput）
msg, err := client.Generate(ctx, "R1", messages)

// JSON 生成（解析失败回喂重试）
var spec MySpec
err := client.GenerateJSON(ctx, "R3", messages, &spec)

// 窗口派生配置
cfg := llm.BudgetConfig{ContextTokens: 1_000_000}
cfg.MaxDiffChars()     // 1,400,000
cfg.TaskTokenBudget()  // 600,000
cfg.MaxOutputTokens()  // 50,000
```

### 熔断器

```go
import "git.enjoye.top/enjoydream/agentkit/breaker"

// 单 key
b := breaker.New(3, 5*time.Minute)
if b.Allow(time.Now()) {
    // 执行
    b.Success()
} else {
    b.Failure(time.Now())
}

// 多 key
bs := breaker.NewBreakers(3, 5*time.Minute)
if bs.Allow("my-plugin") { /* ... */ }
```

### Worker Pool

```go
import "git.enjoye.top/enjoydream/agentkit/worker"

// 实现 TaskQueue 接口
type MyQueue struct { /* ... */ }
func (q *MyQueue) ClaimNextPending(ctx context.Context) (string, bool, error) { /* ... */ }
func (q *MyQueue) TouchRunningHeartbeats(ctx context.Context) error { /* ... */ }
func (q *MyQueue) ResetRunningToPending(ctx context.Context, d time.Duration) (int64, error) { /* ... */ }

pool := &worker.Pool{
    Queue: &MyQueue{},
    Run:   func(ctx context.Context, taskID string) error { /* ... */ },
    N:     2,
    Log:   log,
}
pool.Start(ctx)
defer pool.Stop(60 * time.Second)
```

### 泛型事件总线

```go
import "git.enjoye.top/enjoydream/agentkit/progress"

type MyEvent struct { Type string; Data string }

bus := progress.NewBus[MyEvent]()
ch, cancel := bus.Subscribe(ctx)
defer cancel()

bus.Publish(MyEvent{Type: "hello", Data: "world"})
ev := <-ch
```

### 知识检索

```go
import "git.enjoye.top/enjoydream/agentkit/knowledge/rag"

// 本地 RAG（目录放 markdown 文件）
knowledge, err := rag.NewLocal("/path/to/knowledge")
chunks, _ := knowledge.Retrieve(ctx, "如何配置 Nacos", 5, nil)

// 挂为 eino 工具
tool := knowledge.AsTool()
```

### 安全工具

```go
import "git.enjoye.top/enjoydream/agentkit/safejson"

// 中和不可信文本中的 markdown 注入
safe := safejson.EscapeUntrusted(llmOutput)

import "git.enjoye.top/enjoydream/agentkit/severity"

// 归一化严重级别
sev, ok := severity.Normalize("CRITICAL") // → "high", true

// 指纹去重
fp := severity.Fingerprint("main.go", "未处理错误返回值")

// glob 匹配
matched := severity.GlobMatch("web/**/*.vue", "src/components/Foo.vue")
```

### 内容解析器

```go
import "git.enjoye.top/enjoydream/agentkit/skill"

provider := skill.NewFileProvider("/path/to/skills")
s, _ := provider.Resolve(ctx, skill.Ref{Name: "ocr-grading"})
// s.Content → SKILL.md 内容，s.Checksum → sha256 前 16 位
```

## 依赖关系

```
你的项目
    │
    ▼
agentkit/llm          ← LLM 调用（重试/限速/预算）
agentkit/worker       ← 异步任务队列
agentkit/breaker      ← 熔断保护
agentkit/knowledge/   ← 知识检索 + 方法论注入
agentkit/progress     ← 事件总线
    │
    ▼
  ekit                ← 基础设施（日志/并发/指标/配置）
    │
    ▼
  eino / x/time / x/sync
```

## 从 Argus 迁移

Argus 内部包改为 import agentkit：

| Argus 旧路径 | agentkit 新路径 |
|---|---|
| `argus/internal/llm` | `agentkit/llm` |
| `argus/internal/worker` | `agentkit/worker` |
| `argus/internal/textutil` | `agentkit/textutil` |
| `argus/internal/audit` | `agentkit/audit` |
| `argus/internal/skill` → `knowledge/skill` | `agentkit/skill` |
| `argus/internal/progress` | `agentkit/progress` |
| `argus/internal/knowledge/rag` | `agentkit/knowledge/rag` |
| `argus/internal/context/workcopy` | `agentkit/workcopy` |
| `argus/internal/plugin.Breaker/Breakers` | `agentkit/breaker` |
| `argus/internal/plugin.SeverityRank/Normalize...` | `agentkit/severity` |
| `argus/internal/plugin.TokenBudget` | `agentkit/llm.TokenAccountant` 接口 |
| `argus/internal/plugin.GlobMatch` | `agentkit/severity.GlobMatch` |
| `argus/internal/plugin.Fingerprint` | `agentkit/severity.Fingerprint` |
| `argus/internal/reportview.EscapeUntrusted` | `agentkit/safejson.EscapeUntrusted` |
