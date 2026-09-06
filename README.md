# agentkit

AI Agent 开发工具箱 —— 从 Argus 代码审查平台提取的通用组件库。

## 模块路径

```
git.enjoye.top/enjoydream/agentkit
```

## 包清单

| 包 | 说明 | 外部依赖 |
|---|---|---|
| `acpx` | CLI 编码 agent 统一调用（9 家 + GenericAgent 通用出口） | eino |
| `llm` | LLM 客户端（重试/限速/预算/fitInput/JSON + Resilient 降级链） | eino, eino-ext openai, x/time |
| `toolprior` | 工具优先级决策层（提示词/排序/限流三层约束） | eino |
| `mcp` | MCP server 工具池（lazy 建连 + 白名单 + eino 工具适配） | eino, eino-ext tool/mcp, mcp-go |
| `agentrun` | ReAct agent 运行样板（ADK 封装 + 事件流 + 重试） | eino adk |
| `obsx` | eino callbacks 追踪（llm.call.* 结构化日志） | eino, ekit |
| `breaker` | 熔断器（closed→open→half-open，探测超时兜底） | 无 |
| `worker` | DB 即队列 worker pool（心跳/panic 隔离/优雅停机） | ekit |
| `knowledge/rag` | 双后端 RAG（Local TF-IDF + Milvus 向量） | eino, milvus-sdk-go |
| `progress` | 泛型事件总线 `Bus[T]`（有损广播 + 丢弃计数） | ekit |
| `skill` | SKILL.md 解析器 + 决策使用（渐进披露） | eino（仅 decision 部分） |
| `severity` | 严重级别归一化 + 指纹 + glob 匹配 | 无 |
| `safejson` | Markdown/HTML 反注入 | 无 |
| `audit` | 审计日志 | ekit |
| `textutil` | rune 安全截断等文本工具 | 无 |
| `workcopy` | Git 工作副本沙箱（singleflight + 引用计数 + TTL 回收） | ekit, x/sync |

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

// 流式事件（回调在读取 goroutine 中执行：不得阻塞、不得 panic）
reg.Run(ctx, "claude", acpx.RunRequest{
    Prompt:  "重构 auth 模块",
    OnEvent: func(e acpx.Event) { fmt.Println(e.Type, e.Text) },
})

// 包成 eino 工具挂进 ReAct agent（LLM 自主决定调哪个 agent）
tool := reg.AsTool() // run_coding_agent(agent, prompt, work_dir)
```

各家协议由专用适配器处理（参数均经官方文档/真机核实）：

| Agent | 非交互调用 | Sandbox 支持 |
|---|---|---|
| claude / zcode | `-p <prompt> --output-format stream-json` | ✅ permission-mode 映射 |
| codex | `exec <prompt> --json --output-last-message` | ✅ `--sandbox` 原生 |
| opencode | `run <prompt> --json` | ❌ 忽略（静默） |
| kimi | `-p <prompt> --output-format stream-json` | ❌ 忽略（静默） |
| gemini / qwen | `-p <prompt> --output-format json` | ✅ `--approval-mode` 映射 |
| mimo | `run <prompt> --format json`（专用事件流：tokens/cost/sessionID） | ❌ 忽略（静默） |
| minimax | GenericAgent 模板（CLI 协议待官方稳定） | ❌ 忽略（静默） |

> 未标注 ✅ 的 agent 传入 `Sandbox` 会被静默忽略——安全敏感场景请选择支持沙箱的 agent，
> 或用 `AllowedTools`/`MaxTurns`（claude/codex 支持）自行收敧行为。
>
> 安全防线：prompt 以 `-` 开头时自动前置换行（防 CLI flag 注入）；子进程环境走
> 白名单（绝不继承密钥）；进程组执行，超时/取消 TERM → 3s 宽限 → KILL；
> stdout 8MB 限容 + 单行 1MB 上限。超时/取消可程序化区分：`errors.Is(err, acpx.ErrTimeout)`。

### LLM 客户端

```go
import "git.enjoye.top/enjoydream/agentkit/llm"

client := llm.NewClient(chatModel, "deepseek-v4", myBudget)
client.OnUsage = func(stage string, p, c int) { /* Prometheus 记账 */ }
client.Limiter = rate.NewLimiter(10, 20)
client.ContextTokens = 1_000_000
client.MaxOutputTokens = 50_000

// 普通生成（带重试 + 限速 + fitInput；不污染调用方 msgs）
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

**Resilient 多模型降级链**（429 立即切换、5xx 先重试再切、401/4xx 确定性失败不重试；
每模型独立熔断；预算耗尽全链短路）：

```go
primary, _ := llm.NewOpenAIProvider(ctx, llm.OpenAIProviderConfig{
    BaseURL: "https://api.example.com/v1", APIKey: key, Model: "main-model",
    ContextTokens: 1_000_000,
})
fallback, _ := llm.NewOpenAIProvider(ctx, llm.OpenAIProviderConfig{
    BaseURL: "https://fallback.example.com/v1", APIKey: key2, Model: "backup-model",
})

r := llm.NewResilient("my-service",
    llm.NewFallbackChain(primary, fallback),
    llm.ResilientConfig{RetriesPerModel: 2})
r.OnFallback = func(from, to, stage, reason string) {
    log.Warn("模型降级", "from", from, "to", to, "stage", stage, "reason", reason)
}

msg, attempts, err := r.GenerateWithTrace(ctx, "R1", messages)

// ⚠️ RawModel() 返回裸模型——绕过重试/降级/熔断/记账。
// ReAct agent 等需要 model.BaseChatModel 的场景请自行权衡（降级链覆盖不到该流量）。
```

### ToolPrior —— 工具优先级决策层

```go
import "git.enjoye.top/enjoydream/agentkit/toolprior"

table := toolprior.NewTable()
table.Add(toolprior.Entry{Tool: getFileTool, Priority: toolprior.PriorityCore,
    When: "判定必需，必须最先使用", Cost: "low"})
table.Add(toolprior.Entry{Tool: mcpSearchTool, Priority: toolprior.PriorityExternal,
    When: "本地证据不足时补充", Cost: "high"})

instruction += table.StrategyPrompt(ctx)      // 软：提示词指引
tools := table.Ordered(ctx)                    // 隐式：按优先级排序
tools = append(tools, toolprior.WithCallLimit(mcpTool, 5)) // 硬：限流
// 超限返回 "LIMIT_REACHED: ..." 文本（软止损，模型可见可收尾，不中止运行）
```

### MCP —— 工具池

```go
import "git.enjoye.top/enjoydream/agentkit/mcp"

pool := mcp.NewPool(
    mcp.ServerConfig{Name: "docs", URL: "http://docs.svc/mcp",
        Headers: map[string]string{"Authorization": "Bearer " + tok}},
    mcp.ServerConfig{Name: "lint", Command: []string{"lint-mcp", "serve"},
        Env: []string{"LINT_CONFIG"}}, // 环境白名单：按名从当前进程透传
)
defer pool.Close()
pool.OnError = func(server string, err error) { log.Warn("mcp", "server", server, "err", err) }

tools, err := pool.Tools(ctx, []mcp.ToolSpec{
    {Server: "docs", Allow: []string{"search_docs"}}, // 工具白名单
})
// 安全模型：stdio 子进程环境绝不继承密钥（基础集 + Env 白名单）
```

### AgentRun —— ReAct 运行样板

```go
import "git.enjoye.top/enjoydream/agentkit/agentrun"

out, err := agentrun.RunWithEvents(ctx, agentrun.Config{
    Name:        "reviewer",
    Instruction: instruction,
    Model:       chatModel,
    Tools:       tools, // 或 ToolsFactory: func() []tool.BaseTool { ... }（重试时重建，限流预算按尝试重置）
    MaxIterations: 12,
}, query, func(e agentrun.Event) {
    // e.Type: text | tool_call | tool_result
})

// 失败回喂重试（重试过程也可观测）
out, err = agentrun.RunWithEventsAndRetry(ctx, cfg, query, retryQuery, onEvent)
```

### OBSX —— eino 调用追踪

```go
import "git.enjoye.top/enjoydream/agentkit/obsx"

// 一行启用：该 ctx 链上的 eino 组件调用自动产出 llm.call.start/end/error
// （stage/component/model/耗时/真实 token usage/慢调用告警）
ctx = obsx.InitLLMObservability(ctx, log, obsx.Options{
    SlowThreshold: 30 * time.Second,
    PreviewLen:    0, // 默认 0 = 不落内容（消息可能含用户代码/凭证）
})
```

### 熔断器

```go
import "git.enjoye.top/enjoydream/agentkit/breaker"

// 单 key：Allow==true 后必须恰好配对一次 Success/Failure
//（探测失联超过 DefaultProbeTimeout 会自动放行新探测，不会永久卡死）
b := breaker.New(3, 5*time.Minute)
if b.Allow(time.Now()) {
    if err := doSomething(); err != nil {
        b.Failure(time.Now())
    } else {
        b.Success()
    }
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
// 任务 panic 只损失该任务（worker 存活，心跳过期后复位重跑）
```

### 泛型事件总线

```go
import "git.enjoye.top/enjoydream/agentkit/progress"

type MyEvent struct { Type string; Data string }

bus := progress.NewBus[MyEvent]()
ch, cancel := bus.Subscribe(ctx)
defer cancel() // Background ctx 场景也必须 cancel（否则泄漏监听 goroutine）

bus.Publish(MyEvent{Type: "hello", Data: "world"})
ev := <-ch
n := bus.Dropped() // 订阅者积压导致的丢弃计数（有损是声明的设计）
```

### 知识检索

```go
import "git.enjoye.top/enjoydream/agentkit/knowledge/rag"

// 本地 RAG（目录放 markdown 文件）
knowledge, err := rag.NewLocal("/path/to/knowledge")
chunks, _ := knowledge.Retrieve(ctx, "如何配置 Nacos", 5, nil)

// 挂为 eino 工具
tool := knowledge.AsTool()

// 或向量后端（Milvus + OpenAI 兼容 Embedding；Index 幂等——重复启动重刷不累积重复行）
store, _ := rag.NewMilvusStore(ctx, rag.MilvusConfig{
    Address: "localhost:19530", Dimension: 1024,
}, rag.NewOpenAIEmbedder(baseURL, apiKey, "text-embedding-v3", 1024))
```

**性能参考**（Apple M5 实测，df 预计算后）：

| 语料 | 检索延迟 | 分配 |
|---|---|---|
| 10 篇（~80 块） | 60µs | 25 allocs |
| 100 篇（~800 块） | 2.2ms | 28 allocs |
| 1000 篇（~8000 块） | 47ms | 36 allocs |

基准：`go test ./knowledge/rag/ -bench BenchmarkLocal`

**Milvus 集成测试**（需容器环境）：

```sh
docker compose -f docker-compose.milvus-test.yml up -d   # 等 healthy
MILVUS_TEST_ADDR=127.0.0.1:19530 go test ./knowledge/rag/ -run TestMilvusIntegration -v
docker compose -f docker-compose.milvus-test.yml down -v  # 用完清理
```

覆盖建连/自动建表/索引/Upsert 幂等/向量相似度检索/metadata 过滤/删除全链路。

### 安全工具

```go
import "git.enjoye.top/enjoydream/agentkit/safejson"

// 中和不可信文本中的 markdown 注入（标题/列表/围栏/水平线/表格行/引用定义/HTML 注释）
safe := safejson.EscapeUntrusted(llmOutput)

import "git.enjoye.top/enjoydream/agentkit/severity"

// 归一化严重级别
sev, ok := severity.Normalize("CRITICAL") // → "high", true

// 指纹去重
fp := severity.Fingerprint("main.go", "未处理错误返回值")

// glob 匹配（.gitignore 语义：web/** 匹配目录内部，不含目录自身；? 按 rune）
matched := severity.GlobMatch("web/**/*.vue", "web/src/components/Foo.vue")
```

### Skill —— 内容解析 + 决策使用

```go
import "git.enjoye.top/enjoydream/agentkit/skill"

provider := skill.NewFileProvider("/path/to/skills")

// 模式一：静态注入（直接解析）
s, _ := provider.Resolve(ctx, skill.Ref{Name: "ocr-grading"})
// s.Content → SKILL.md 正文，s.Checksum → sha256 前 16 位

// 模式二：决策使用（渐进披露——skill 多/大时不撑爆系统提示词）
metas, _ := provider.ListSkills(ctx)          // Meta.Name=目录名（use_skill 加载依据），
                                              // Meta.Title=frontmatter name（展示别名）
prompt := skill.ListPrompt(metas)             // 注入提示词的可用清单
useSkill, _ := skill.AsSkillTool(provider, nil) // use_skill(name) eino 工具
// 模型用展示名（frontmatter name）调用也会被归一化到目录名
```

## 依赖关系

```
你的项目
    │
    ▼
agentkit/llm          ← LLM 调用（重试/降级/限速/预算）
agentkit/agentrun     ← ReAct 运行样板
agentkit/toolprior    ← 工具优先级决策
agentkit/mcp          ← MCP 工具池
agentkit/acpx         ← CLI 编码 agent
agentkit/worker       ← 异步任务队列
agentkit/breaker      ← 熔断保护
agentkit/knowledge/   ← 知识检索 + 方法论注入
agentkit/progress     ← 事件总线
agentkit/obsx         ← eino 调用追踪
    │
    ▼
  ekit                ← 基础设施（日志/并发/指标/配置）
    │
    ▼
  eino / eino-ext / mcp-go / milvus-sdk-go / x/time / x/sync
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
| `argus/internal/adapter_cli` / `acpx` | `agentkit/acpx` |
| `argus/internal/mcp`（如适用） | `agentkit/mcp` |
| `argus/internal/obs`（如适用） | `agentkit/obsx` |
| `argus/internal/plugin.Breaker/Breakers` | `agentkit/breaker` |
| `argus/internal/plugin.SeverityRank/Normalize...` | `agentkit/severity` |
| `argus/internal/plugin.TokenBudget` | `agentkit/llm.TokenAccountant` 接口 |
| `argus/internal/plugin.GlobMatch` | `agentkit/severity.GlobMatch` |
| `argus/internal/plugin.Fingerprint` | `agentkit/severity.Fingerprint` |
| `argus/internal/reportview.EscapeUntrusted` | `agentkit/safejson.EscapeUntrusted` |
