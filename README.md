# agentkit

AI Agent 开发工具箱 —— 从生产项目提炼的通用组件库：代码审查平台 **Argus** + 智能运维多智能体平台 **bianque** + LLM 评测/观测平台 **heimdallr**。
当前版本 **v0.10.3** · Go ≥ 1.26 · 32 个包。

> 📖 **完整框架文档**：[docs/FRAMEWORK.md](docs/FRAMEWORK.md) —— 设计原则、六层架构、
> 各包逐一详解（API/示例/边界契约）、横向能力专题（可靠性/成本/多副本/安全）、
> 生产实践参考、版本纪律与陷阱清单。

## 七种 Agent 架构：何时用 / 何时不用

> 逐架构代码示例与完整陷阱清单见 [docs/patterns.md](docs/patterns.md)。
> 总原则：**从最简单的架构开始，被现实逼着升级；不要从最复杂的开始，被复杂度困死。**

### 1. Single Agent —— `llm`

- ✅ **该用**：一次调用能完成（分类/抽取/改写/摘要/单点问答）；无外部事实依赖；延迟成本敏感。
- ❌ **不该用**：答案依赖模型看不到的事实（→ ReAct）；输出需按硬标准迭代打磨（→ Reflection）。
- ⚠️ 最大陷阱是"杀鸡用牛刀"：把一次调用的事拆成 agent 循环，成本延迟失败面全部 ×N。**80% 的 LLM 调用应止步于此。**

### 2. ReAct —— `agentrun` + `toolprior`

- ✅ **该用**：步骤不可预知、边做边看；探索型任务（定位根因/未知代码库找证据）；工具结果会改变后续方向。
- ❌ **不该用**：步骤固定可枚举（≥4 步 → 写代码/Graph）；单次调用可完成；对延迟极敏感。
- ⚠️ MaxIterations 必设；有副作用的工具必配 `WithCallLimit`；必须有降级出口（失败落回单轮）。

### 3. Plan-and-Execute —— `agentrun.PlanAndExecute`

- ✅ **该用**：目标明确、可预规划为步骤清单，步骤多（≥4）或单步重需独立审计；计划本身有价值（可人工评审/当进度依据）。
- ❌ **不该用**：目标模糊（规划会把误解固化成计划，先 ReAct 探索）；步骤 ≤3 或单轮可完成；环境每步剧变（计划频繁作废，Replan 烧钱不收敛）。
- ⚠️ 计划质量 = Planner 模型质量（弱规划 + 强执行是常见错配）；Replanner 判"完成"要可靠，MaxSteps 兜底必设。

### 4. Reflection —— `agentkit/reflection`

- ✅ **该用**：有可判定的硬标准（编译过/测试过/清单可核对）；单稿质量不稳定且失败模式可枚举进 Rubric；产出重要到值得 2~3 倍调用成本。
- ❌ **不该用**：标准主观（"写得好"——Critic 自评不可信，收敛 ≠ 正确）；需外部事实核验（Critic 无工具，"通过"是幻觉）；第一稿已稳定达标。
- ⚠️ 全部价值在 Rubric 质量——写不进具体条目就别用；重要场景 Critic 换不同家族模型（同模型有共同盲区）。

### 5. Router + Skill —— `agentkit/router` + `agentkit/skill`

- ✅ **该用（router 显式路由）**：入口意图可枚举（≤10）且各类别处理链差异大；想按类别配成本档；分类错误有 Fallback 兜底。
- ✅ **该用（skill 隐式路由）**：类别多、持续增长、由领域文档承载；路由决策需要看了任务全文再定（渐进披露）。
- ❌ **不该用**：类别间处理链相同（路由白付一次分类调用）；类别 <3 或边界模糊；skill 文档质量差（隐式路由上限 = 文档质量）。
- ⚠️ router 的 MinConfidence + Fallback 必配；两者可组合：router 粗分到域，域内 skill 细分。**门槛在 Classify 内自守**：只取 `Classify` 决策、自行分发的编排器同样受 MinConfidence 约束（低置信/未知名返回错误，Decision 随错误返回供可观测）；分类置信度钳位 [0,1]。

### 6. Blackboard —— `agentkit/blackboard`

- ✅ **该用**：多视角分析同一材料且视角间要互看（后位专家依赖前位贡献）；贡献顺序不可预知、无中心指派逻辑；需要共识收敛信号（一轮无人补充 = 分析饱和）。
- ❌ **不该用**：有中心指派逻辑（→ eino adk Supervisor）；专家彼此独立无需互看（→ 并行 fan-out + 合并，Argus R4 模式）；强顺序依赖（→ Sequential/graph）。
- ⚠️ 无中心调度无法保证覆盖度——专家职责要互斥且完备；话痨专家会跑满轮次（要有"写过即沉默"约束）。

### 7. Graph Workflow —— eino `compose` + adk workflow

- ✅ **该用**：流程结构稳定且复杂（条件分支/并行汇聚/循环/人工审批）；需确定性保证与全链路可追溯（合规）；简单架构验证过的流程固化成产品级管线。
- ❌ **不该用**：流程还在探索期经常变（改图成本 > 改 prompt，先跑通再固化——这是图的正确定位）；需要 LLM 自主决策且路径空间大（图的分支是写死的 → ReAct）。
- ⚠️ 别把需要语义判断的地方写成确定性条件边；节点粒度 = 一件事一节点；定义好节点失败时的路由（跳过/重试/终止），别让第一个错误毒化全图。

## 模块路径

```
github.com/yi-nology/agentkit
```

## 包清单

| 包 | 说明 | 外部依赖 |
|---|---|---|
| `acpx` | CLI 编码 agent 统一调用（9 家 + GenericAgent）+ RunProcess 进程托管 | eino |
| `llm` | LLM 客户端（重试/限速/预算/fitInput/JSON + Resilient 降级链 + StageRouter 路由 + CostTracker + UsageHandler 完整用量采集） | eino, eino-ext openai, x/time |
| `toolprior` | 工具优先级决策层（提示词/排序/限流三层约束） | eino |
| `mcp` | MCP server 工具池（lazy 建连 + 白名单 + eino 工具适配 + UnwrapMCPText 信封剥离） | eino, eino-ext tool/mcp, mcp-go |
| `agentrun` | ReAct 样板 + Plan-and-Execute 样板（ADK 封装 + 事件流 + 重试） | eino adk |
| `reflection` | Reflection 架构原语（生成→批判→修订收敛循环） | eino |
| `router` | Router 架构原语（LLM 意图分类→选路→分发） | eino |
| `blackboard` | Blackboard 架构原语（共享黑板 + 专家轮转） | 无 |
| `clarify` | 澄清/标准化词表内核（term_map 模型/加载/校验 + 序数指代解析 + 回答消解） | yaml.v3 |
| `policy` | 操作审计门（四模式裁决矩阵 + 例外规则 + fail-safe 仲裁 + WithAuditGate 工具装饰器） | eino, yaml.v3 |
| `dispatch` | 通用派发守卫（allow 矩阵 + 深度上限 + 自派发拒绝，EdgeSource 拓扑注入） | 无 |
| `obsx` | eino callbacks 追踪（llm.call.* 结构化日志） | eino, ekit |
| `langfuse` | Langfuse Public API 只读客户端（trace 拉取 + 详情合并 + 官方契约类型） | 无 |
| `breaker` | 熔断器（closed→open→half-open，探测超时兜底） | 无 |
| `worker` | DB 即队列 worker pool（心跳/panic 隔离/优雅停机）+ LeaderElector 选主 | ekit |
| `worker/pglease` | LeaseStore 的 PostgreSQL 实现（原子 UPSERT + Migrate，表名可配） | 无（database/sql） |
| `knowledge/rag` | 双后端 RAG（Local TF-IDF + Milvus 向量） | eino, milvus-sdk-go |
| `websearch` | 公开资料检索抽象（Service + SearXNG + AsTool） | eino |
| `progress` | 泛型事件总线 `Bus[T]`（有损广播 + 丢弃计数） | ekit |
| `skill` | SKILL.md 解析 + 多根 Library（热替换）+ 决策使用（渐进披露）+ 版本化契约（maturity/弃用窗口/区间求解/结构化写回） | eino（decision）、yaml.v3（Library）、semver（区间） |
| `pack` | 领域包 MCP 工具面契约清单（_shared 基线 / 包覆盖 / 字典序冲突） | yaml.v3 |
| `lineage` | 装配血缘图（used_by 单源 + reload 影响面 diff + 焦点子图） | skill, pack |
| `hotplug` | 插拔视图 Plugboard + 泛型原子快照 Holder | 无 |
| `logredact` | 日志/审计凭据脱敏（URL/token/Bearer 打码 + Redact 高敏感抹除 + Masker/Restore 拓扑标识令牌化） | 无 |
| `jsonrepair` | LLM 宽容 JSON 修复（栅栏/尾逗号/全角/散文包裹 + 标量归一） | 无 |
| `llmjson` | 模型输出 JSON 统一解析入口（ExtractJSON 快路径 → 语法修复 → 全链宽容三级尝试） | llm, jsonrepair |
| `severity` | 严重级别归一化 + 指纹 + glob 匹配 | 无 |
| `stats` | 评测/对比统计（Wilson 置信区间 + McNemar 精确检验） | 无 |
| `safejson` | Markdown/HTML 反注入 | 无 |
| `audit` | 审计日志 | ekit |
| `textutil` | rune 安全截断 + 等分块 + TruncEllipsis + 近重复检测（bigram 集合 + Jaccard） | 无 |
| `workcopy` | Git 工作副本沙箱（singleflight + 引用计数 + TTL 回收） | ekit, x/sync |

## 快速使用

### ACPX —— 调用 CLI 编码 agent

```go
import "github.com/yi-nology/agentkit/acpx"

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

// RunProcess：只要进程组托管纪律、不需要 Agent 解析层时（v0.7.1）
stdout, stderr, code, err := acpx.RunProcess(ctx, acpx.ProcessRequest{
    Argv: []string{"my-cli", "run"}, Dir: workDir, Env: []string{"NEEDED_VAR"},
    Timeout: 5 * time.Minute, MaxStdout: 1 << 20,
})
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
import "github.com/yi-nology/agentkit/llm"

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

r := llm.NewResilient(llm.NewFallbackChain(primary, fallback), llm.ResilientConfig{
    RetriesPerModel: 2,
    Tracker:         llm.NewCostTracker(), // 可选：内置成本记账（费率来自 Provider 配置）
})
r.OnFallback = func(from, to, stage, reason string) {
    log.Warn("模型降级", "from", from, "to", to, "stage", stage, "reason", reason)
}

msg, attempts, err := r.GenerateWithTrace(ctx, "R1", messages)

// ⚠️ RawModel() 返回裸模型——绕过重试/降级/熔断/记账。
// ReAct agent 等需要 model.BaseChatModel 的场景请自行权衡（降级链覆盖不到该流量）。
```

**StageRouter 分阶段模型路由**（v0.8.1）——不同阶段配不同模型，调用点零改动：

```go
sr := llm.NewStageRouter(defaultGen) // 缺省 = 主链（含预算注入）
sr.Use("R1", bigWindowGen)           // 前缀路由："R1" 命中 "R1a"
sr.Use("qa", fastGen)                // 精确路由
// 之后 sr.Generate(ctx, "R1a", msgs) 自动走 bigWindowGen；UsedTokens 聚合全部链
```

**成本记账双口径**：`llm.Client.OnUsage` 覆盖 Generate 直连路径；ReAct（RawModel
直用）路径的真实 usage 经 `obsx.Options.OnUsage` 回流（见 OBSX 节）→ 汇入
`llm.CostTracker`（`Record(model, stage, p, c, 费率)` / `Summary()` 按模型汇总，
1 万条封顶）。


### ToolPrior —— 工具优先级决策层

```go
import "github.com/yi-nology/agentkit/toolprior"

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
import "github.com/yi-nology/agentkit/mcp"

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
import "github.com/yi-nology/agentkit/agentrun"

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

**PlanAndExecute**（计划先行编排，适合目标明确、步骤可预规划的长链路任务）：

```go
res, err := agentrun.PlanAndExecute(ctx, agentrun.PlanExecuteConfig{
    Planner:  plannerModel,  // 须支持 tool calling（计划结构强制产出）
    Executor: executorModel,
    Tools:    tools,
    MaxSteps: 10,
}, goal)
fmt.Println(res.Answer)
```

姊妹原语（v0.8.0）：

```go
// Reflection：生成→Rubric 批判→修订收敛（硬质量标准场景）
res, _ := reflection.Refine(ctx, &reflection.Config{
    Model: chatModel, Task: "实现函数", Input: 需求,
    Rubric: "1. 处理空切片 2. 无 data race", MaxIterations: 3,
})
// res.Output / res.Converged / res.Rounds

// Router：LLM 意图分类→选路→分发（入口意图可枚举场景）
r, _ := router.New(&router.Config{Model: fastModel, Routes: []router.Route{
    {Name: "bug-fix", Description: "修代码类", Handle: fixChain},
    {Name: "explain", Description: "解释类", Handle: explainChain},
}, MinConfidence: 0.6, Fallback: fallbackFn})
d, out, _ := r.Do(ctx, userInput) // d.Route/d.Confidence/d.Reason 可观测

// Blackboard：无中心多专家互看协作（多视角分析场景）
board := blackboard.NewBoard()
board.Seed("material", 材料文本)
res, _ := blackboard.Convene(ctx, board, []blackboard.Specialist{
    {Name: "security", Act: observeAndContribute},
    {Name: "perf", Act: observeAndContribute},
}, &blackboard.ConveneOptions{MaxRounds: 3})
```

### OBSX —— eino 调用追踪

```go
import "github.com/yi-nology/agentkit/obsx"

// 一行启用：该 ctx 链上的 eino 组件调用自动产出 llm.call.start/end/error
// （stage/component/model/耗时/真实 token usage/慢调用告警）
ctx = obsx.InitLLMObservability(ctx, log, obsx.Options{
    SlowThreshold: 30 * time.Second,
    PreviewLen:    0, // 默认 0 = 不落内容（消息可能含用户代码/凭证）
    OnUsage: func(component, model, stage string, prompt, completion int) {
        // v0.7.2：真实 usage 回流——覆盖 ReAct RawModel 旁路（Client.OnUsage 看不到）
    },
})
```

### 熔断器

```go
import "github.com/yi-nology/agentkit/breaker"

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
import "github.com/yi-nology/agentkit/worker"

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

// LeaderElector（v0.7.3）：多副本时"只能跑一份"的控制面组件（出站轮询/定时清理）
// 存储：实现 worker.LeaseStore（SQL 一条条件 UPSERT 即可）
e := worker.NewLeaderElector(store, "argus/poller", instanceID,
    30*time.Second, 10*time.Second,
    worker.WithOnGained(func() { log.Info("当选") }),
    worker.WithLeaderLogger(log))
e.Start(ctx)
if e.IsLeader() { /* 仅 leader 执行 */ }
defer e.Stop() // 主动让位，缩短换主窗口

// PG 可直接用内置租约实现（v0.9.0，免自写 LeaseStore）：
// ls := pglease.NewPGLeaseStore(sqlDB).WithTable("my_lease") // 表名白名单，防拼接注入
// _ = ls.Migrate(ctx)                                       // 缺省表名 agentkit_lease
```

### 泛型事件总线

```go
import "github.com/yi-nology/agentkit/progress"

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
import "github.com/yi-nology/agentkit/knowledge/rag"

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
import "github.com/yi-nology/agentkit/safejson"

// 中和不可信文本中的 markdown 注入（标题/列表/围栏/水平线/表格行/引用定义/HTML 注释）
safe := safejson.EscapeUntrusted(llmOutput)

import "github.com/yi-nology/agentkit/severity"

// 归一化严重级别
sev, ok := severity.Normalize("CRITICAL") // → "high", true

// 指纹去重
fp := severity.Fingerprint("main.go", "未处理错误返回值")

// glob 匹配（.gitignore 语义：web/** 匹配目录内部，不含目录自身；? 按 rune）
matched := severity.GlobMatch("web/**/*.vue", "web/src/components/Foo.vue")
```

### Skill —— 内容解析 + 决策使用

```go
import "github.com/yi-nology/agentkit/skill"

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

### TextUtil —— rune 安全文本工具

```go
import "github.com/yi-nology/agentkit/textutil"

trunc, trimmed := textutil.TruncRunes(longText, 800) // rune 截断（多字节不腰斩）
chunks := textutil.SplitRunes(bigText, 4000)         // 等分块（大文本分批送 LLM）
ellipsis := textutil.TruncEllipsis(longText, 60)     // 截断 + 省略号（展示面统一语义，v0.9.0）
```

### v0.9 新增组件速览

```go
import "github.com/yi-nology/agentkit/logredact"

logredact.Redact("nats://ops:s3cret@host:4222") // nats://ops:****@host:4222
logredact.RedactValue(payload)                  // 递归脱敏 map/slice 中的凭据字符串

import "github.com/yi-nology/agentkit/jsonrepair"

var v MyStruct
err := jsonrepair.ParseLenient(llmOutput, &v, &jsonrepair.Schema{
    StringKeys: map[string]bool{"summary": true}, // 领域 schema：标量归一目标
    ListKeys:   map[string]bool{"steps": true},
    OnMap:      func(m map[string]any) { /* 每个子 map 归一完成后的领域钩子 */ },
})
// 栅栏剥离 → 散文抽对象 → 语法修复（全角/尾逗号/未闭合）→ 标量归一

import "github.com/yi-nology/agentkit/llmjson"

var report ReviewReport
err := llmjson.Unmarshal(llmOutput, &report)
// ExtractJSON 快路径 → 语法修复 → 全链宽容；全败错误携带两路原因，可直接回喂重试

import "github.com/yi-nology/agentkit/websearch"

ws := websearch.NewSearxng("http://searxng.local", 10*time.Second) // 实例须开 json format
ws.Language = "zh-CN"
results, _ := ws.Search(ctx, "OOM 排查方法", 5) // 与 rag 互补：面向外网公开资料
tool := ws.AsTool()                              // web_search eino 工具

import "github.com/yi-nology/agentkit/hotplug"

pb := hotplug.NewPlugboard(allSlugs, disabled) // nil disabled = 全启用
h := hotplug.NewHolder[Snapshot]()
h.Store(newSnap) // 原子换整体；在途请求用旧快照跑完，新请求即时用新快照
cur := h.Load()

import "github.com/yi-nology/agentkit/pack"
import "github.com/yi-nology/agentkit/lineage"

manifests, warns, _ := pack.LoadToolManifests(fsys) // 包清单整文件替换 _shared 基线
                                                    // warns 非空 = 包间同名冲突（字典序第一生效）
lin := lineage.Build(experts, skills, manifests)    // expert→skill→MCP 血缘图（used_by 单源）
impacts := lineage.Diff(prev, lin)                  // reload 影响面（nil 基线=首帧无 diff）
focus, _ := lin.Focus("skill-x", 2)                 // 焦点邻接子图（depth≤2 无向）
```

## 细节与边界契约（必读）

> 每个包的完整契约见对应 godoc；此处集中列出**最容易踩的细节**（全部来自生产审查实战）。

### 横切契约

- **context 传播**：所有 eino 工具包装（acpx.AsTool / rag.AsTool / skill.AsSkillTool /
  mcp 池）都透传调用方 ctx——上层取消/超时会真正终止子进程与网络调用。
- **哨兵错误**：`errors.Is(err, acpx.ErrTimeout / ErrCanceled)` 区分 CLI 超时与取消；
  llm 全链失败返回 `*llm.AttemptError`（含每次尝试的 Provider/Model/Err/Duration）；
  其余包错误均 `%w` wrap，可逐层解包。
- **并发模型**：llm/agentrun/mcp.Pool/breaker/progress/blackboard.Board/worker.Pool
  并发安全；**例外**——toolprior.Table 与 skill.FileProvider 的注册/首扫是
  build-then-read（构建后只读），skill 缓存无淘汰（假设进程内内容不变）。
- **模型测试桩**：eino v0.9 的 `model.BaseChatModel` 要求 `Generate` + `Stream`
  **两个方法都实现**，只写 Generate 编译不过。
- **测试门控**：`ACPX_SMOKE=1` 触发 acpx 真机冒烟（真调 LLM，花钱）；
  `MILVUS_TEST_ADDR=127.0.0.1:19530` 触发 Milvus 集成测试（需容器）。
- **私有模块**：`go mod tidy` 需 `GONOSUMCHECK='git.enjoye.top/*'`（或 GOPRIVATE）。

### 逐包细节速查

**llm**
- Client 缺省：MaxRetries=3（含首次）、BaseDelay=2s、MaxDelay=30s；429 退避下限 5s。
- `GenerateJSON` 解析失败只回喂重试 1 次；截断在 `Generate` 内部已转错误，不会流到这里。
- `MaxOutputTokens < 0` = 不下发 `max_tokens` 参数（推理模型兼容）；`0` = 走派生缺省。
- fitInput 截断发生在最长 **user** 消息上并追加留痕标记；巨型 system 消息不在裁剪范围。
- 预算注入：Client 浅拷贝 / Resilient 显式副本（共享熔断状态）——都用 `BudgetInjector`。
- CostTracker 上限 1 万条（超限丢最旧）；费率来自 Provider 的 CostPer1K 配置。
- StageRouter：`Use` 的 stage 既可以是精确名（"qa"）也可以是前缀（"R1" 命中 "R1a"）；
  预算注入在注册前完成；`RawModel()` 恒透传缺省链。

**acpx**
- 缺省超时 10 分钟；stdout 8MB / 单行 1MB 上限；stderr 只留尾部 400 字节进错误。
- 子进程环境 = 基础集（PATH/HOME/TMPDIR/代理/CA/XDG 等）+ `Env` 按名透传；
  额外注入 `GIT_TERMINAL_PROMPT=0`、`CI=1`。
- mimo 运行期错误 **exit code 仍为 0**，只能从 error 事件识别；codex 的 usage 是
  逐轮累加口径；kimi 新版 `-p` 直跑（无 `--print`，不可与 `--auto` 组合）。
- `OnEvent` 回调在 stdout 读取 goroutine 中同步执行——不得阻塞、不得 panic。
- `Registry` 构建期注册、运行期只读；`AsTool` 丢弃 Model/Sandbox 等字段（只传
  Prompt/WorkDir）——需要沙箱约束时自行构造 RunRequest 而非走 AsTool。

**mcp**
- Timeout 覆盖连接 + Initialize（30s 缺省），不含子进程 spawn 阶段。
- 列举失败自动摘除坏连接（下次调用重建）；`Close` 后池不可复用；重名 server 保留先到。
- `Allow` 白名单全部未命中时经 `OnError` 告警（返回 0 个工具不报错）。

**toolprior**
- `WithCallLimit` 每次调用**新建包装实例**（计数不跨任务共享）——跨重试需要重置
  预算时配合 agentrun `ToolsFactory`。
- 超限返回 `LIMIT_REACHED: ...` 文本（nil error）：模型可见、可收尾；不要改回
  返回 error——那会中止整个 agent 运行。
- `Table` 构建后只读；`Add` nil Tool 直接 panic。

**skill**
- frontmatter 只解析 name/description（`---` 围栏）；缓存无淘汰（进程内内容不变假设）；
  symlink 不在路径防护范围（root 应为可信目录）。
- `Format` 用 eino FString（pyfmt）：正文含裸 `{`/`}` 需写成 `{{`/`}}`。
- `Meta.Name` = 目录名（use_skill/allowed 唯一依据）；`Meta.Title` = frontmatter
  展示别名；模型用别名回填会被归一化。

**agentrun**
- 出口判定 = assistant 消息且无 tool_calls；空内容答复报错文案区分"空答复"与"没答复"。
- `MaxIterations` 默认 12（比 ADK 缺省 20 更收紧）。
- `RunWithRetry` 两次尝试**共用 Tools 实例**——有状态包装（限流）跨尝试累计，
  需要重置请用 `ToolsFactory`。

**reflection**
- Critic 输出 `{"pass":bool,"issues":[]}`；pass=true 仍带 issues 判不通过。
- MaxIterations 默认 3；收敛只代表符合 Rubric，不代表正确。

**blackboard**
- Board 是内存态；`Convene` 每轮**串行**调用各专家（专家内部自行并发）；
  游标语义 = 错过的增量不重看（每轮都是新起点）。

**knowledge/rag**
- chunk 缺省：目标 600 rune / 硬上限 4000 / 块间重叠 100 / 重扫间隔 10min；
  topK 缺省 5；工具返回单片段截断 800 rune。
- 过滤字段白名单：`file` / `heading`（其他 key 直接报错）。
- Local 分词 = ASCII 词 + 中文二元组（日韩文暂不支持）；Milvus 索引固定 IVF_FLAT
  （`IndexType` 字段保留但未生效）；COSINE 下 Score 是相似度（越大越好）。
- Embedder 每批 16 条串行、带维度校验；集成测试需 `MILVUS_TEST_ADDR` 门控。

**websearch**
- 面向外网公开资料（与 knowledge/rag 私有知识库互补）；SearXNG 实例须配置
  `formats: [html, json]`，否则响应非 JSON 直接报错。
- HTTP 非 200 / 响应非 JSON 一律报错；无命中 = 空切片 + nil error；`Language`
  空串 = 不传 language 参数。

**jsonrepair / logredact / hotplug**
- jsonrepair：修复顺序 = 栅栏剥离 → 散文抽对象 → 语法修复 → 标量归一；领域
  schema（StringKeys/ListKeys/OnMap）留给调用方。`OnMap` 在**子节点归一完成后**
  对每个 map 触发（v0.9.1 更名自 `Mutate` 并改时机——旧 API 已删，领域钩子现在
  看得到已规范化的嵌套结构）。
- logredact：只打码三类——URL 内嵌账号口令 / token·secret·password·api_key
  键值对 / Authorization·Bearer 头；与 safejson（反注入）正交，一个管密钥一个管注入。
- hotplug：`NewPlugboard(all, disabled)` 传 nil = 全启用；`Holder.Store` 原子换
  整体、读无锁——在途请求继续用旧快照跑完，新请求即时用新快照。

**pack / lineage**
- pack：包清单对 `_shared` 基线是**整文件替换**不是 merge——扩展清单须拷出全量
  再增改；包间同名 server 冲突 = 警告 + 包名字典序第一生效（警告即治理信号，
  两包真争同一 server 属治理问题）；无清单目录合法。conf 是装配事实源、清单是
  契约事实源，对账由调用方做。
- lineage：只管图机制不绑定装配实现——有效技能集（覆盖感知）由装配方经中性输入
  注入；`Diff` nil 基线不产出 impact（重启首帧把全量资产报成"新增"是噪音）；
  工具面 diff 取 manifest ∪ 显式授予并集（只 diff manifest 会让授予变更静默），
  任一侧 Unlimited 授予则枚举不可知、跳过不产出；`Focus` depth≤2 无向遍历。

**worker**
- 心跳 20s / 过期判定 90s（`StaleRunningAfter`）——任务须短于此或自行续期。
- `ClaimNextPending` 实现必须原子（多副本正确性的根）；`Log`/`Queue`/`Run` 为 nil
  会在启动或消费期回退/报错。
- 多副本要求共享 DB 为网络库；`Stop(grace)` 后心跳可能还有 ≤5s 尾巴。
- pglease：`TryAcquire` 原子 UPSERT（空闲/过期/本人持有 → true），`Release` 仅
  持有者生效；表名白名单 `WithTable`（防拼接注入），缺省 `agentkit_lease`，
  使用前需 `Migrate`。

**breaker / progress / audit / textutil**
- breaker：`Allow==true` 后必须恰好配对一次 Success/Failure；`Breakers.Now` 仅
  启动期可注入。
- progress：订阅缓冲 64，满了丢（`Dropped()` 可观测）；cancel 与 ctx 双向收口。
- audit：走 Info 级别——日志级别调到 Warn 以上会吞掉审计事件（可用独立 sink）。
- textutil：`TruncRunes(s, -1)` 返回空串 + true（不 panic）；`SplitRunes(s, 0)` 整串单块。

## 依赖关系

```
你的项目
    │
    ▼
agentkit/llm            ← LLM 调用（重试/降级/限速/预算/阶段路由/成本/用量）
agentkit/agentrun       ← ReAct / Plan-and-Execute 样板
agentkit/reflection     ← 反思循环
agentkit/router         ← 意图路由
agentkit/blackboard     ← 多专家黑板协作
agentkit/dispatch       ← 派发守卫（allow 矩阵 + 深度上限）
agentkit/policy         ← 操作审计门（四模式裁决）
agentkit/clarify        ← 澄清/标准化词表内核（回答消解）
agentkit/toolprior      ← 工具优先级决策
agentkit/skill          ← SKILL.md 渐进披露 + 多根库 + 版本化契约
agentkit/pack           ← 领域包 MCP 工具面契约清单
agentkit/lineage        ← 装配血缘图（依赖 skill + pack）
agentkit/mcp            ← MCP 工具池
agentkit/acpx           ← CLI 编码 agent
agentkit/websearch      ← 公开资料检索
agentkit/knowledge/rag  ← 双后端知识检索
agentkit/worker         ← 异步任务队列（+ pglease PG 租约 / LeaderElector 选主）
agentkit/hotplug        ← 插拔/热替换
agentkit/jsonrepair     ← 宽容 JSON 修复
agentkit/llmjson        ← 模型输出 JSON 统一解析入口
agentkit/logredact      ← 凭据脱敏
agentkit/breaker        ← 熔断保护
agentkit/progress       ← 事件总线
agentkit/obsx           ← eino 调用追踪
agentkit/langfuse       ← Langfuse trace 读回（只读客户端）
agentkit/stats          ← 评测/对比统计（Wilson CI + McNemar）
agentkit/safejson · severity · audit · textutil · workcopy  ← 安全/审计/文本/副本沙箱
    │
    ▼
  eino / eino-ext / mcp-go / milvus-sdk-go / ekit / x/time / x/sync / semver / yaml.v3
```

## 从生产项目迁移

### Argus

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

### argus（v0.9.8）

| argus 旧路径 | agentkit 新路径 |
|---|---|
| `argus/internal/llmjson` | `agentkit/llmjson`（直接消费，内部包删除） |

### bianque（v0.9.0）

| bianque 旧路径 | agentkit 新路径 |
|---|---|
| `bianque/internal/logredact` | `agentkit/logredact` |
| `bianque/internal/search` | `agentkit/websearch` |
| `bianque/internal/cluster`（PGLeaseStore） | `agentkit/worker/pglease` |
| `bianque/internal/strutil.Truncate` | `agentkit/textutil.TruncEllipsis` |
| `bianque/internal/agents` Plugboard/SnapshotStore | `agentkit/hotplug` |
| `bianque/internal/skills` Library/DecisionProvider | `agentkit/skill.Library` |
| `bianque/internal/llm/obs.go` UsageHandler | `agentkit/llm.NewUsageHandler` |
| `bianque/internal/engine/protocol` 宽容 JSON / UnwrapMCPText | `agentkit/jsonrepair` / `agentkit/mcp.UnwrapMCPText` |

报告 schema、专家包 schema、审批/会话等领域模型仍留在 bianque。

### bianque（v0.9.2）

| bianque 旧路径 | agentkit 新路径 |
|---|---|
| `bianque/internal/skills`（SkillMeta/写回/校验/弃用窗口） | `agentkit/skill`（LibMeta/Validate/RewriteMode/RewriteBody/DeprecatedExpiredInUse） |
| `bianque/internal/skills.VersionInRange` | `agentkit/skill.VersionInRange` |
| `bianque/internal/agents.SkillRef` | `agentkit/skill.Decl` |
| `bianque/internal/service.ToolManifest` + loadToolManifests | `agentkit/pack.ToolManifest` + `LoadToolManifests` |
| `bianque/internal/service` Lineage/DiffLineage/Focus/LineageHub | `agentkit/lineage`（Build/Diff/Focus/Hub，中性输入） |

### bianque（v0.9.4）

| bianque 旧路径 | agentkit 新路径 |
|---|---|
| `bianque/internal/llm/failover.go`（failoverModel） | `agentkit/llm.NewFailoverModel`（OnFailover 回调替代全局钩子） |
| `bianque/internal/engine/runner` mutatingToolSegments | `agentrun.MutatingVerbs` + `IsMutatingTool` + `SideEffectTracker`（重试守卫内建于 RunWithRetry） |
| `bianque/internal/agents/routing.go` 词表匹配内核 | `agentkit/router.KeywordHit` 四件套（否定守门/词边界/排除构式） |
| `bianque/internal/logredact`（副本分叉） | `agentkit/logredact`（凭证词 `: ` 空格形态 + PEM 整段规则已合入） |
| `bianque/internal/strutil.Truncate` | `agentkit/textutil.TruncEllipsis`（v0.9.0 挂账清账） |

### bianque（v0.9.5）

| bianque 旧路径 | agentkit 新路径 |
|---|---|
| `bianque/internal/scheduler/usage.go` priceOf | `agentkit/llm.PriceOf`（精确 → 最长前缀回落，未配价归零） |
| `bianque/internal/engine/dispatch/guard.go` | `agentkit/dispatch`（注册表耦合改 `EdgeSource` 接口注入拓扑） |

### bianque（v0.9.6）

| bianque 旧路径 | agentkit 新路径 |
|---|---|
| `bianque/internal/engine/policy` | `agentkit/policy`（yaml 路径约定改显式入参；bianque 侧薄转发） |
| `bianque/internal/engine/runner/audit.go` | `agentkit/policy.WithAuditGate` |
| `bianque/internal/engine/scheduler/normalize.go` postNegated | `agentkit/router.KeywordPostNegated` |

### bianque（v0.9.7）

| bianque 旧路径 | agentkit 新路径 |
|---|---|
| `bianque/internal/agents` TermEntry/TermClarify + loadTermMap | `agentkit/clarify.Entry/Options` + `LoadVocab`（别名薄层） |
| `bianque/internal/agents` 词表内在校验 | `agentkit/clarify.Validate`（域注册/路由词冲突校验留宿主） |
| `bianque/internal/engine/scheduler` ordinalIndex/resolveTermAnswer | `agentkit/clarify.OrdinalIndex/ResolveAnswer` |

### heimdallr（v0.10.0）

| heimdallr 旧路径 | agentkit 新路径 |
|---|---|
| `heimdallr/internal/mine/dedup.go` bigramSet/jaccard | `agentkit/textutil`（BigramSet/Jaccard/Similarity/NearDuplicate；InstructionBigrams 留宿主） |
| `heimdallr/internal/report` WilsonCI/McNemarExact | `agentkit/stats`（原样，comb 转私有） |
| `heimdallr/internal/observe/client.go` + Trace/Observation 契约类型 | `agentkit/langfuse`（错误前缀 langfuse:；UsageTokens/UsageCost 导出；MapTrace 留宿主） |
