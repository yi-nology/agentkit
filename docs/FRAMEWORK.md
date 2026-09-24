# agentkit 框架完整文档

> 版本：v0.10.15 · Go ≥ 1.26 · 模块路径 `github.com/yi-nology/agentkit`
> 配套文档：[架构模式支持矩阵](patterns.md)（七架构何时用/何时不用）· [README](../README.md)（快速上手）

> 文中架构图使用 Mermaid：Forgejo/GitHub 等端原生渲染；不支持渲染的查看端，
> 代码块本身仍按文本可读。

---

## 目录

1. [框架定位与设计原则](#一框架定位与设计原则)
2. [总体架构与包清单](#二总体架构与包清单)
3. [L1 模型层 —— llm](#三l1-模型层--llm)
4. [L2 编排层 —— agentrun / reflection / router / blackboard / dispatch / policy / clarify](#四l2-编排层--agentrun--reflection--router--blackboard--dispatch--policy--clarify)
5. [L3 决策层 —— toolprior / skill](#五l3-决策层--toolprior--skill)
6. [L4 工具与上下文层 —— acpx / mcp / workcopy / knowledge/rag / textutil](#六l4-工具与上下文层)
7. [L5 运行时基础设施 —— breaker / worker / progress / reportutil / audit](#七l5-运行时基础设施)
8. [L6 可观测层 —— obsx / langfuse](#八l6-可观测层--obsx--langfuse)
9. [七架构模式支持](#九七架构模式支持)
10. [横向能力专题](#十横向能力专题)
11. [生产实践：Argus 参考实现](#十一生产实践argus-参考实现)
12. [版本纪律与陷阱清单](#十二版本纪律与陷阱清单)

---

## 一、框架定位与设计原则

**agentkit 是从生产项目提炼的 AI Agent 开发工具箱**：代码审查平台 Argus +
智能运维多智能体平台 bianque + LLM 评测/观测平台 heimdallr。一组可在 LLM 应用之间共享的 Go 组件，覆盖模型调用、
编排样板、工具治理、知识检索、运行时基础设施与可观测。

### 设计原则

| 原则 | 含义 |
|---|---|
| **不重复 eino** | 编排原语（图/顺序/并行/循环/Supervisor）由底座 eino 提供；agentkit 提供**原语之上的样板与领域件**——降级链、工具纪律、限流、知识。节点内用 agentkit 样板，节点间用 eino 编排 |
| **样板沉淀** | 凡是生产中复制过两次的胶水代码（ADK 构造/事件 drain/出口判定/进程组纪律/租约选主），提炼成包——消费方一行调用 |
| **生产纪律内建** | 重试/限速/预算/超时/降级/熔断/审计不是可选项，是组件默认行为；失败路径与成功路径同等对待 |
| **失败可见** | 有损设计必须可观测（progress.Dropped）、状态机边界必须有兜底（熔断探测超时）、文档承诺必须等于实现行为 |
| **从简单开始** | 架构选型从 Single Agent 起步，被现实逼着升级；七种架构的支持矩阵见 patterns.md |

### 不是什么

- 不是 LangChain 式的全家桶链式 DSL——编排交给 eino；
- 不是模型代理/网关——模型调用经 eino-ext 各 provider SDK 直连；
- 不绑定特定 LLM 供应商——一切经 eino `model.BaseChatModel` / `ToolCallingChatModel`
  抽象，OpenAI/百炼/DeepSeek 等任何兼容端点均可。

---

## 二、总体架构与包清单

```
┌──────────────────────────────────────────────────────────────┐
│ L2 编排层  agentrun(ReAct/P&E)  reflection  router  blackboard │
│            dispatch(派发守卫: allow矩阵+深度上限+自派发拒绝)     │
│            policy(操作审计门: 四模式裁决+例外规则+fail-safe仲裁) │
│            clarify(澄清词表: term_map模型+回答消解)             │
├──────────────────────────────────────────────────────────────┤
│ L3 决策层  toolprior(工具优先级/限流)    skill(渐进披露)         │
├──────────────────────────────────────────────────────────────┤
│ L1 模型层  llm(Client/Resilient 降级链/Budget/StageRouter/成本)  │
├──────────────────────────────────────────────────────────────┤
│ L4 工具与上下文  acpx(9家CLI agent)  mcp(工具池)  workcopy(副本) │
│                 knowledge/rag(双后端检索)  websearch  textutil   │
│                 pack(包契约清单)  lineage(装配血缘图)             │
│                 fence(数据区围栏)  conversation(会话窗口)          │
│                 procx(子进程纪律)  httpx(HTTP+JSON纪律)           │
├──────────────────────────────────────────────────────────────┤
│ L5 运行时  breaker(熔断)  worker+pglease(队列+选主+PG租约)      │
│            progress(总线)  hotplug(插拔/热替换)                 │
│            logredact(脱敏)  jsonrepair(宽容JSON+解析链)          │
│            fence(注入卫生) reportutil(严重度/聚簇/统计)           │
│            audit(审计)                                           │
├──────────────────────────────────────────────────────────────┤
│ L6 可观测  obsx(eino callbacks 追踪/真实 usage 回流)             │
│            langfuse(Public API 只读客户端/trace 读回)             │
└──────────────────────────────────────────────────────────────┘
        底座：eino v0.9.18 · eino-ext · mcp-go · milvus-sdk-go · ekit
```

| 包 | 职责 | 外部依赖 | 版本引入 |
|---|---|---|---|
| `llm` | LLM 客户端：重试/限速/预算/fitInput/JSON + Resilient 降级链 + StageRouter + UsageHandler + PriceOf 定价估算 | eino, eino-ext openai, x/time | v0.1.0（v0.2 降级链，v0.8.1 路由，v0.9.0 UsageHandler，v0.9.5 PriceOf） |
| `agentrun` | ReAct 运行样板 + Plan-and-Execute 样板（ADK 封装） | eino adk | v0.6.0（v0.8.0 P&E） |
| `toolprior` | 工具优先级决策层：提示词/排序/限流三层约束 | eino | v0.5.2 |
| `skill` | SKILL.md 解析 + 多根 Library（热替换）+ 决策使用（渐进披露）+ 版本化契约（maturity/弃用窗口/Validate/写回/区间/requires_config） | eino（decision）、yaml.v3（Library）、semver（区间） | v0.5.1（v0.9.0 Library，v0.9.2 契约，v0.9.3 requires_config） |
| `pack` | 领域包 MCP 工具面契约清单（_shared 基线 / 包整文件覆盖 / 字典序冲突） | yaml.v3 | v0.9.2 |
| `lineage` | 装配血缘图（used_by 单源 + reload 影响面 Diff + 焦点子图 + Hub） | skill, pack | v0.9.2 |
| `reflection` | 反思循环：生成→批判→修订收敛 | eino | v0.8.0 |
| `router` | LLM 意图路由：分类→选路→分发（意图槽位 + 词表匹配内核） | eino | v0.8.0（v0.9.3 槽位，v0.9.4 词表内核） |
| `blackboard` | 共享黑板 + 专家轮转 | 无 | v0.8.0 |
| `dispatch` | 通用派发守卫：allow 矩阵 + 深度上限 + 自派发拒绝（EdgeSource 拓扑注入，DenyError 结构化拒绝） | 无 | v0.9.5 |
| `policy` | 操作审计门：四模式统一操作裁决（例外规则→矩阵→fail-safe 兜底 + Arbiter 灰区仲裁 + AuditGate 工具装饰器） | eino, yaml | v0.9.6 |
| `clarify` | 澄清/标准化词表内核：term_map 模型/加载/内在校验 + OrdinalIndex 序数指代 + ResolveAnswer 回答消解 | yaml | v0.9.7 |
| `acpx` | 9 家 CLI 编码 agent 统一调用（执行纪律经 procx） | eino | v0.1.0 后（v0.10.11 进程托管迁 procx） |
| `mcp` | MCP server 工具池（lazy 建连/env 白名单/工具白名单）+ UnwrapMCPText | eino, eino-ext tool/mcp, mcp-go | v0.5.0（v0.9.0 unwrap） |
| `workcopy` | Git 工作副本沙箱（singleflight + 引用计数 + 保留复用/增量刷新 + TTL 兜底回收） | ekit, x/sync | v0.1.0（v0.10.6 保留复用+增量刷新） |
| `knowledge/rag` | 双后端 RAG：Local TF-IDF + Milvus 向量 | eino, milvus-sdk-go | v0.1.0 |
| `websearch` | 公开资料检索抽象：Service + SearXNG + AsTool | eino | v0.9.0 |
| `breaker` | 熔断器（closed→open→half-open，探测超时兜底，Abandon 放弃在途探测） | 无 | v0.1.0（v0.10.1 探测时限可配，v0.10.4 Abandon） |
| `worker` | DB 即队列 worker pool + LeaderElector 选主 | ekit | v0.1.0（v0.7.3 选主） |
| `worker/pglease` | LeaseStore 的 PostgreSQL 实现（表名白名单可配） | 无 | v0.9.0 |
| `progress` | 泛型事件总线（有损广播 + 丢弃计数） | ekit | v0.1.0 |
| `hotplug` | 插拔视图 Plugboard + 泛型原子快照 Holder | 无 | v0.9.0 |
| `logredact` | 日志/审计凭据脱敏（URL/token/Bearer）+ Redact 高敏感打码 + Masker/Restore 拓扑标识令牌化 | 无 | v0.9.0（v0.10.2 Redact/Masker） |
| `jsonrepair` | LLM 宽容 JSON 修复（栅栏/尾逗号/全角/散文 + 标量归一） | 无 | v0.9.0 |
| `procx` | 子进程托管纪律单源：进程组执行/超时整组终止/限容采集/环境白名单（acpx/mcp/workcopy 共用） | 无 | v0.10.11（自 acpx 进程层迁出） |
| `llm/llmtest` | 脚本化 ChatModel/Provider 测试桩（RepeatLast/耗尽报错两语义显式化，输入记录/ResponseMeta 可编程） | eino | v0.10.12 |
| `httpx` | HTTP+JSON 调用纪律单源：限容读体 + rune 安全错误摘要 + StatusError 类型化与 DoJSONWithRetry 重试骨架（langfuse/rag/websearch 共用） | 无 | v0.10.11 |
| `obsx` | eino callbacks 追踪（结构化日志 + 真实 usage 回流） | eino, ekit | v0.4.0 |
| `langfuse` | Langfuse Public API 只读客户端（FetchBatch 分页/GetTrace/Query 选择口径 + Trace/Observation 契约，UsageTokens/UsageCost 新旧口径兜底） | 无 | v0.10.0 |
| `reportutil` | 评审/评测报告后处理：严重度归一 + best-of-N 聚簇 + Wilson/McNemar 统计 | 无 | v0.10.11（severity/sampling/stats 三包合并） |
| `audit` | 审计日志 | ekit | v0.1.0 |

| `fence` | 提示词数据区围栏：不可信内容显式包裹 + 逃逸序列中和（返回中和计数作注入特征信号） | 无 | v0.10.5 |
| `conversation` | 多轮会话历史原语：Turn / Split 滚动窗口切分 / Render 截断渲染 / Combine 摘要拼装（确定性，摘要生成归调用方） | textutil | v0.10.5 |
| `textutil` | rune 安全截断/等分块/TruncEllipsis + 近重复检测（bigram Jaccard） | 无 | v0.1.0（v0.9.0 Ellipsis，v0.10.0 近重复） |

---

## 三、L1 模型层 —— llm

一切 LLM 调用的入口。两代客户端实现同一 `Generator` 接口，可无感互换：

```go
type Generator interface {
    Generate(ctx context.Context, stage string, msgs []*schema.Message) (*schema.Message, error)
    GenerateJSON(ctx context.Context, stage string, msgs []*schema.Message, out any) error
    UsedTokens() int
    RawModel() model.BaseChatModel
}
```

`stage` 不是装饰：它随 ctx 注入（obsx 追踪读取）、驱动退避策略、并作为 StageRouter 的路由键。

### Client —— 单模型客户端

```go
client := llm.NewClient(chatModel, "deepseek-v4", budget) // budget: TokenAccountant，可 nil
client.ContextTokens = 1_000_000      // 模型窗口；驱动 fitInput 自守恒
client.MaxOutputTokens = 50_000       // 输出预留
client.Limiter = rate.NewLimiter(10, 20) // 可选全局限速
client.OnUsage = func(stage string, p, c int) { /* Prometheus/记账 */ }

msg, err := client.Generate(ctx, "R1", msgs)            // 重试（指数退避+jitter，429 下限 5s）
err = client.GenerateJSON(ctx, "R3", msgs, &spec)       // 解析失败带错误回喂重试 1 次
```

内建行为：

- **fitInput 自守恒**：输入超 `(窗口-输出预留)×2×0.9` 时截断最长 user 消息并留痕；不污染调用方切片。
- **截断防护**：`finish_reason=length` 转为错误返回（带 completion_tokens）；`MaxOutputTokens>0` 时截断重试自动提升 max_tokens ×1.5。
- **错误分类**：`ClassifyLLMError` —— 结构化状态码优先（go-openai RequestError），文本兜底带数字词边界（"429" 不误命中 "1429ms"）；4xx 确定性失败（400/401/404/422…）不重试，5xx/网络错误重试，429 提高退避下限。
- **兼容开关**：`MaxOutputTokens < 0` = 不下发 `max_tokens` 参数（要求 `max_completion_tokens` 的推理模型）。

### Resilient —— 多模型降级链

```go
primary, _  := llm.NewOpenAIProvider(ctx, llm.OpenAIProviderConfig{
    BaseURL: "...", APIKey: "...", Model: "main", ContextTokens: 1_000_000})
fallback, _ := llm.NewOpenAIProvider(ctx, llm.OpenAIProviderConfig{...Model: "backup"...})

r := llm.NewResilient(llm.NewFallbackChain(primary, fallback), llm.ResilientConfig{
    RetriesPerModel: 2,
    Limiter: limiter,                     // 共享限速
    Tracker: llm.NewCostTracker(),        // 可选：内置成本记账（费率来自 Provider 配置）
    OnUsage: func(model, stage string, p, c int) {...},
    OnFallback: func(from, to, stage, reason string) {...},
})
msg, attempts, err := r.GenerateWithTrace(ctx, "R1", msgs) // attempts 留痕每次尝试
```

**降级决策矩阵**（有测试锁定）：

| 错误 | 同模型重试 | 切下一模型 |
|---|---|---|
| 429 | ❌（立即切） | ✅ |
| 401/403/上下文超限/其余 4xx | ❌ | ✅ |
| 5xx / 网络错误 | ✅（≤RetriesPerModel） | ✅ |
| 输出截断 | ✅（max_tokens ×1.5） | ✅ |

- 每模型独立熔断（Breakers）；全部模型熔断时报"全部 N 个模型均处于熔断冷却中"；
- 预算耗尽全链短路（入口 + 逐 provider 复查）；
- 半开探测有 `DefaultProbeTimeout` 超时兜底——探测失联不会永久逐出模型；
- `AttemptError` 全链失败留痕每次尝试（Provider/Model/Err/Duration）。

```mermaid
flowchart TD
    REQ["Generate(stage)"] --> SR{"StageRouter"}
    SR -- "未命中" --> GEN["缺省链"]
    SR -- "R1/R2/qa 覆盖" --> SGEN["阶段覆盖链（各自预算注入）"]
    GEN --> BUD{"预算剩余？"}
    SGEN --> BUD
    BUD -- "否" --> SHORT["预算短路报错"]
    BUD -- "是" --> ALLOW{"熔断 Allow？"}
    ALLOW -- "否（含探测超时兜底）" --> SKIP["跳过该模型"]
    ALLOW -- "是（半开放行探测）" --> TRY["Provider 调用"]
    TRY -- "成功" --> OK["Success + 记账 + 返回"]
    TRY -- "失败" --> CLS{"ClassifyLLMError"}
    CLS -- "4xx 确定性" --> SWITCH["切下一模型"]
    CLS -- "5xx/网络/截断" --> RETRY["同模型重试 ≤N（退避+jitter）"]
    RETRY -- "耗尽" --> SWITCH
    CLS -- "429" --> SWITCH
    SWITCH -- "还有模型" --> ALLOW
    SWITCH -- "全链失败" --> ATT["AttemptError 全链留痕"]
```

### Budget —— 任务级预算

```go
budget := llm.NewBudget(600_000) // TokenAccountant 标准实现
budget.Remaining() / Used() / Add(n)
```

传给 `NewClient` 第三参，或经 `BudgetInjector` 注入已有链：

```go
if bi, ok := gen.(llm.BudgetInjector); ok {
    gen = bi.WithBudget(budget) // Resilient 显式副本（共享熔断状态）；Client 浅拷贝
}
```

### StageRouter —— 分阶段模型路由（v0.8.1）

```go
sr := llm.NewStageRouter(defaultGen)  // 缺省 = 主链（含预算注入）
sr.Use("R1", bigWindowGen)            // 前缀路由："R1" 命中 "R1a"
sr.Use("qa", fastGen)                 // 精确路由
// 调用点不变：sr.Generate(ctx, "R1a", msgs) 自动走 bigWindowGen
```

精确命中优先、最长前缀其次、未命中走缺省链；`UsedTokens` 聚合全部链；
预算注入由调用方在注册前完成（`BudgetInjector.WithBudget`）。

### FailoverModel —— 主备模型降级装饰器（v0.9.4）

```go
fm := llm.NewFailoverModel(
    llm.ChainLink{Model: primaryModel, Name: "deepseek-v3"},
    llm.ChainLink{Model: backupModel, Name: "glm-4.7"}) // v0.10.11 起链节式
fm.OnFailover = func(from, to, reason string) { /* metrics */ }
```

eino `BaseChatModel`/`ToolCallingChatModel` 双形态装饰器：主模型失败且调用方 ctx
存活时切备模型重放同一次请求（Stream 仅首块前可切；ctx 取消/超时原样上抛）。
与 `Resilient` 互补——Resilient 覆盖自家 Generator 客户端路径，FailoverModel
填补 ReAct 主路径（ADK ChatModelAgent 直调裸模型）的降级空白。切换决策对任何
错误恒真：确定性错误（401/上下文超限）在主备异端点时能救，误切代价仅一次备
模型调用。`WithTools` 对主备分别派生后重新包装，ADK 绑工具契约成立。

### UsageHandler —— 完整用量采集（v0.9.0）

**全仓唯一的 callbacks 侧记账出口**（v0.10.12 前 obsx.Options.OnUsage 是并行
的五数字出口，已退役）：Cached/Reasoning tokens、FinishReason、Duration、
Iteration 都有；归因经 `Labels` 泛化，领域键由调用方决定。

```go
h := llm.NewUsageHandler(func(r llm.UsageRecord) {
    // r.Model/PromptTokens/CachedTokens/ReasoningTokens/DurationMS/Iteration/Labels
})
ctx = llm.WithUsageLabels(ctx, map[string]string{"session_id": "s1", "agent": "healer"})
ctx = llm.WithCallCounter(ctx) // 本作用域第几次调用（1 起）
// 挂进 eino callbacks；无 RunInfo/无 TokenUsage 的调用静默跳过（防双计）
```

只认带 `RunInfo` 的 OnEnd（compose 杂散包装会二次触发）；`Message.ResponseMeta.Usage`
作 compose 图节点兜底。调用方可在 sink 侧按 Labels 过滤（无归因跳过）。

### CostTracker —— 成本记账

```go
ct := llm.NewCostTracker()
ct.Record(model, stage, promptTokens, completionTokens, [2]float64{prompt费率, completion费率})
ct.Summary() // map[model]ModelSummary{TotalTokens, TotalCostUSD, CallCount}
```

上限 10_000 条（防长驻进程无界增长）。ReAct（RawModel 直用）路径的真实 usage
回收：把 NewUsageHandler 经 `callbacks.InitCallbacks` 注入 ctx——Client 侧已配
OnUsage/Budget 时 handler 自动跳过（防重护栏 v0.10.12 自 obsx 移入记账侧）。

#### PriceOf —— 定价估算（v0.9.5）

按模型名匹配定价表估算单次调用成本（USD）；精确匹配优先，未命中回落最长前缀
（模型名带版本/日期后缀时定价键可只写主干），未配价返回 0 不阻塞记账：

```go
cost := llm.PriceOf(map[string]llm.Price{
    "glm-4": {InputPerM: 0.6, OutputPerM: 2.2}, // 后缀版本名自动回落到此前缀
}, "glm-4.6-20260901", promptTokens, completionTokens)
```

宿主只需提供 `map[模型名]Price`；与 CostTracker 记账、UsageHandler 用量采集互补。

### 其他 API

- `ExtractJSON(s)`：从模型输出剥围栏/截取首个 JSON 对象或数组；
- `IsRateLimitError` / `IsTruncatedError` / `RetryableLLMError`：错误分类快速判定。

---

## 四、L2 编排层 —— agentrun / reflection / router / blackboard / dispatch / policy / clarify

### agentrun —— ReAct 运行样板

封装 eino ADK 的 ChatModelAgent + Runner：构造、事件流 drain、出口判定
（assistant 无 tool_calls 即最终答复）、重试样板。

```go
out, err := agentrun.RunWithEvents(ctx, agentrun.Config{
    Name: "reviewer", Description: "审查专家",
    Instruction: instruction,
    Model: chatModel,                       // 或 RawModel()
    ToolsFactory: func() []tool.BaseTool {  // 每次尝试重建——LimitCalls 计数按尝试重置
        return buildTools()
    },
    MaxIterations: 12,
}, query, func(e agentrun.Event) {
    // e.Type: text | tool_call | tool_result（工具轨迹可观测）
})
```

- `Run` / `RunWithEvents` / `RunWithRetry` / `RunWithEventsAndRetry` 四种入口；
- `Config.Tools`（静态表）与 `ToolsFactory`（按尝试重建）二选一，后者优先——
  **RunWithRetry + LimitCalls 组合必须用 Factory**，否则限流计数跨尝试累计；
- MaxIterations 默认 12；迭代耗尽/空答复返回明确错误，无死循环；
- 失败语义：与 toolprior 软止损配合（超限返回 LIMIT_REACHED 文本而非 error，
  不会中止整图丢弃进展）；
- 副作用守卫（v0.9.4）：`RunWithRetry`/`RunWithEventsAndRetry` 首轮已调用变更类
  工具（`IsMutatingTool` 动词段判定，`MutatingVerbs` 可扩展）后不再整体重跑——
  重跑会重复副作用（脚本执行/服务操作类工具首轮已生效），如实上抛交调用方降级；
  只读/幂等工具场景置 `Config.RetryAfterMutation=true` 恢复无条件重试。

```mermaid
sequenceDiagram
    participant C as 调用方
    participant A as agentrun
    participant M as ChatModel（可被 R3ChatModel 覆盖）
    participant T as 工具表（toolprior 三层约束）
    C->>A: Run(query)
    loop MaxIterations 内
        A->>M: 指令 + 对话历史
        M-->>A: assistant + tool_calls → 事件 tool_call
        A->>T: 执行（超限返回 LIMIT_REACHED 文本软止损）
        T-->>A: 工具结果 → 事件 tool_result
    end
    M-->>A: assistant 无 tool_calls → 事件 text
    A-->>C: 最终答复
```

### agentrun.PlanAndExecute —— 计划先行编排

```go
res, err := agentrun.PlanAndExecute(ctx, agentrun.PlanExecuteConfig{
    Planner:  plannerModel,  // 须支持 tool calling（计划结构以 ToolInfo 强制产出）
    Replanner: replannerModel, // 可选：完成判定模型（空 = 复用 Planner；v0.10.13 起支持）
    Executor: executorModel,
    Tools:    tools,
    MaxSteps: 10,
    PlannerInstruction: "约束/背景（经输入整形注入，不覆盖格式提示词）",
}, goal)
```

封装 eino `adk/prebuilt/planexecute` 三件套：Planner 拆解 → Executor 带工具执行 →
Replanner 决定完成或修订，"执行-重规划"循环收敛。适用边界见 patterns.md。

### reflection —— 反思循环

```go
res, err := reflection.Refine(ctx, &reflection.Config{
    Model: chatModel, Task: "实现该需求的函数", Input: 素材,
    Rubric: "1. 处理空切片 2. 无 data race",   // 必填：全部价值在 Rubric 质量
    MaxIterations: 3,
})
// res.Text 末稿（v0.10.11 起自 Output 改名）；res.Converged 是否过审；res.Rounds 每稿+批判留痕
```

生成 → Critic 结构化评审（`{pass, issues}`）→ 带全量 issues 修订 → 收敛或达上限。
防御：pass=true 仍带 issues 判不通过（不自洽评审不可信）。

### router —— 显式意图路由

```go
r, _ := router.New(&router.Config{
    Model: fastModel, // 分类用快模型
    Routes: []router.Route{
        {Name: "bug-fix", Description: "修代码类", Handle: fixChain},
        {Name: "explain", Description: "解释类", Handle: explainChain},
    },
    MinConfidence: 0.6,
    Fallback: func(ctx context.Context, input, reason string) (string, error) { ... },
})
d, out, _ := r.Run(ctx, userInput) // d.Route/d.Confidence/d.Reason 全可观测
```

与 skill 的分工：意图可枚举（≤10）用 router（一次分类调用，各类链任意编排）；
类别多/由文档承载/需看全文再定用 skill 隐式路由。可组合：router 粗分到域，域内 skill 细分。

#### 意图槽位（v0.9.3）

分类调用在选路的同时顺带提取附加意图维度（如「是否要方案」）——与选路共用一次
LLM 调用（零额外延迟/费用）；槽位是选路之外的正交维度，不改变走哪条链，只改变链内行为：

```go
r, _ := router.New(&router.Config{
    // ...Routes 同上...
    Slots: []router.SlotSpec{
        {Name: "plan", Description: "用户是否要处理方案，true/false"},
    },
})
d, _, _ := r.Classify(ctx, input)
d.Slots["plan"] // "true"/"false"；模型未给的槽位不出现在结果里
```

自守恒：未配置槽位名一律丢弃、空值丢弃、超长值截断——槽位提取失败不影响选路本身；
`Slots: nil` 时提示词与解析保持原样。

#### 词表匹配内核（v0.9.4）

LLM 分类之外的确定性层（词表兜底优先于 LLM）——中文关键词三重守门：

```go
router.KeywordHit("磁盘没有问题", "磁盘")            // false：否定前置守门
router.KeywordHitBoundary("进程被 killed", "kill")   // false：词边界（不误命中 killed）
router.KeywordHitExcept("怎么执行这个脚本", "执行",
    []string{"怎么执行"})                             // false：排除构式（how-to 不触发执行链）
```

守门只作废该处命中——多处出现任一处通过即命中；疑问构式（有没有/是不是/要不要）
先剥离再判否定。适用任何中文意图词表路由，不限于 router.New 的 LLM 分类流程。

### clarify —— 澄清/标准化词表内核（v0.9.7）

口语→规范维度词条（term_map）的模型、外置加载、内在校验，以及澄清反问的回答消解。
归一/澄清只补充语义不改路由；挂起态存取、反问状态机与注入段渲染留宿主：

```go
entries, err := clarify.LoadVocab(os.DirFS(expertsDir), "_shared/term_map.yaml")
// 缺文件 (nil,nil)=零行为；解析失败 fail-fast。词表命中由 router.KeywordHit* 内核承担
err = clarify.Validate(entries) // word 唯一/维度键规范/vague⟺clarify；域注册等拓扑校验留宿主

n, ok := clarify.OrdinalIndex("第 2 个") // 2, true（整体序数才命中，普通句子不误伤）
picked, isFallback, ok := clarify.ResolveAnswer(
    []string{"CPU", "内存", "磁盘 IO"}, "不清楚，全面查", "帮我看看磁盘io")
// "磁盘 IO", false, true（序数→选项词包含→逃生前缀命中；ok=false=用户换话题）
```

`ResolveAnswer` 的消解序：序数指代（1-based 对 options 序，越界=放弃）→ 选项词包含
匹配（双侧去空格）→ 逃生选项（答案是其前缀也算）。沉淀自 bianque 输入标准化层 +
消歧 clarify：序数解析在宿主已有两处消费者。

### blackboard —— 无中心多专家协作

```go
board := blackboard.NewBoard()
board.Seed("material", 材料文本)
res, _ := blackboard.Convene(ctx, board, []blackboard.Specialist{
    {Name: "security", Act: func(ctx, b, since int64) (bool, error) {
        for _, e := range b.EntriesAfter(since) { /* 观察增量 */ }
        b.Write("security", "finding", "...")
        return true, nil
    }},
    {Name: "perf", Act: ...},
}, &blackboard.ConveneOptions{MaxRounds: 3})
```

线程安全 Board（单调 Seq + since 游标增量）；每轮各专家依次观察，**一整轮无人贡献
即共识停止**。与 Supervisor（中心指派）互斥选择；专家彼此独立时用并行 fan-out 更优。

### dispatch —— 通用派发守卫（v0.9.5）

多智能体引擎的派发闸门：allow 矩阵 + 深度上限 + 自派发拒绝。拓扑边由宿主经
`EdgeSource` 抽象提供（slug 全集 + 各 slug 允许派发的目标），构建期物化矩阵、
运行期只读：

```go
g := dispatch.NewGuard(myRegistry, 4)         // EdgeSource + maxDepth
err := g.Assert("planner", "executor", depth) // 不合法返回 *DenyError
// DenyError{Caller, Callee, Depth, Reason: not_allowed | depth_exceeded | self_dispatch}
g.AssertDepth(depth)                          // 仅校验深度（引擎内部环节推进用）
g.Allowed("planner", "executor")              // 只读查询（事件载荷/诊断面展示可达性）
```

`DenyError` 是结构化拒绝——调用方不得转述、不得降级，按失败兜底如实上报
（可原样落审计事件载荷）。沉淀自 bianque engine/dispatch，注册表耦合改为
`EdgeSource` 接口（宿主注册表的最小投影）。

---
### policy —— 操作审计门（v0.9.6）

会话执行策略模式下的统一操作裁决门：每个操作必过、三值裁决、全程留痕。模式改变的是
裁决策略，不是移除审计点；红线（变异工具拒绝、最高危恒人审）在矩阵层硬编码，任何模式
不可绕过：

```go
g := policy.NewGate(pol, arbiter)  // Policy 可 nil 用内置基座；Arbiter 可 nil
dec := g.Decide(ctx, policy.Op{Type: policy.OpToolCall, Mode: policy.ModeConfirm,
    Tool: "shell_execute", Mutating: true})
// Decision{Verdict: auto_proceed|need_human|deny|plan_only, Decider, RuleID, Reason}
```

裁决分层：例外规则（`Policy.Rules`，首个命中即胜）→ 模式×风险矩阵 → 未知形态兜底
need_human。灰区规则（`verdict: unknown`）交给 `Arbiter` 仲裁插件——失败或非法输出
一律 fail-safe 升人审，「只升不降」在 fail-safe 方向恒成立。策略表 yaml 外置：

```go
pol, err := policy.LoadOverrides(os.DirFS(expertsDir), "_shared/operation_policy.yaml")
// 缺文件回内置基座（auto≤L2/full≤L3）；阈值 clamp 只降不升（调高=扩权，须改基座）
```

工具调用面配套 `AuditGate` 装饰器：每次调用先裁决、`onAudit` 回调留痕、deny 沿
工具结果通道返回错误（agent 拿到工具错误自行降级，不中断整个环节）；gate 为 nil 原样
返回（未装配=存量行为）。沉淀自 bianque engine/policy + runner/audit.go，yaml 路径
约定改为显式入参。

---

## 五、L3 决策层 —— toolprior / skill

### toolprior —— 工具优先级三层约束

扁平工具表上叠加：① `StrategyPrompt`（提示词软约束：优先级序/何时用/成本）②
`Ordered`（表序注意力引导，Info 失败条目排末尾）③ `LimitCalls`（硬限流：超限
返回模型可见的 `LIMIT_REACHED: ...` 文本软止损——**不返回 error**，避免 eino
ToolsNode 上抛中止整图丢弃全部进展）。

```go
table := toolprior.NewTable()
table.Add(toolprior.Entry{Tool: getFileTool, Priority: toolprior.PriorityCore,   // 0 核心证据
    When: "判定前必查", Cost: "low"})
table.Add(toolprior.Entry{Tool: mcpTool, Priority: toolprior.PriorityExternal,  // 2 外部工具
    When: "本地证据不足再用", Cost: "high"})
tools := table.Ordered(ctx)                 // 排序后工具表
instruction += table.StrategyPrompt(ctx)    // 策略提示注入 instruction
limited := toolprior.LimitCalls(invokableTool, 5) // 每次包装新建实例（计数不跨任务）
```

档位：Core(0) < Support(1) < External(2)，自定义数值可插中间。`Table` 构建期写入、
构建后只读；`Add(nil Tool)` 直接 panic（注册期 fail fast）。

```mermaid
flowchart TD
    T["Table（注册期）"] --> L1["层1 软：StrategyPrompt<br/>优先级序/何时用/成本 → 注入 instruction"]
    T --> L2["层2 隐式：Ordered<br/>按优先级稳定排序 → 模型表序注意力"]
    T --> L3["层3 硬：LimitCalls<br/>超限返回 LIMIT_REACHED 文本（模型可见）<br/>不返回 error——不中止整图"]
```

### skill —— SKILL.md 渐进披露

Agent Skills 标准最小实现：目录约定 `root/<name>/SKILL.md`，frontmatter（`---` 围栏
name/description/version）+ 正文；canonical checksum=sha256(正文)[:16]（两 Provider\n统一口径，观测守卫比对基准不随 Provider 漂移）；路径遍历防护。

两种用法：

```go
provider := skill.NewFileProvider("/path/to/skills")

// 模式一：静态注入（skill 少时）
s, _ := provider.Resolve(ctx, skill.Ref{Name: "ocr-grading"})
instruction += fmt.Sprintf("--- skill:%s ---\n%s", s.Name, s.Content)

// 模式二：决策使用/渐进披露（skill 多/大时）
metas, _ := provider.ListSkills(ctx)            // Meta.Name=目录名（加载依据）
prompt += skill.ListPrompt(metas)               // 清单进提示词（Meta.Title=展示别名括注）
useSkill, _ := skill.AsSkillTool(provider, nil) // use_skill(name) 工具；别名自动归一化
```

关键契约：`Meta.Name`（规范引用名）是 use_skill 加载与 allowed 清单的唯一依据；
frontmatter name 只是展示别名，模型用别名回填会被 `AliasResolver` 归一化。
`Format`（eino FString/pyfmt）渲染模板变量——正文含裸 `{}` 需 `{{}}` 转义。

### Library —— 多根技能库（v0.9.0）

FileProvider 假定单根且缓存不变；平台「文件管定义、页面管状态」需要可热替换的
多根库（`_shared/skills/` + `<包>/skills/`）：

```go
lib, err := skill.LoadFromFS(fsys) // 重名 fail-fast；_shared 缺省 frozen
lib.Get(name) / Body / Has / Describe / Names
lib.Resolve(ctx, skill.Ref{Name: "oom-diag"}) // 实现 Provider（无缓存，读当前实例）
lib.ListSkills(ctx) / CanonicalName(ctx, "展示名")  // Lister + AliasResolver（v0.10.11 起签名带 ctx）
```

`LibMeta` 承载技能版本化契约（v0.9.2）：mode/maturity/version/requires_mcp
（tools+min_version）/deprecated（replaced_by+remove_after）/provides/compatibility；
`requires_config`（v0.9.3）声明技能依赖的集成配置类型（如 `[rag, s3]`）——声明级
契约，缺失由调用方告警、不拦截加载。JSON 标签即对外 API 契约。

**加载即校验（v0.9.2 行为变化）**：`LoadFromFS` 对每条技能跑 `LibMeta.Validate()`
——mode/maturity 枚举、SemVer、弃用窗口（deprecated 必填 remove_after 日期）、
requires_mcp.server 非空，非法即 fail-fast（错误带文件路径）。原先只扫描不校验。

```go
ok, err := skill.VersionInRange("1.2.0", ">=1.0.0 <2.0.0") // SemVer 区间求解
out, err := skill.RewriteMode(src, skill.ModeOnDemand)     // 结构化写回：改 mode 保留嵌套契约字段
out, err := skill.RewriteBody(src, "新正文")                // 编辑器路径：围栏原文不动
expired := lib.DeprecatedExpiredInUse(used, time.Now())    // 弃用窗口已过且仍被引用 → reload 失败清单

type Decl struct{ Name, Version string; Optional bool }    // agent.yaml skills 元素：
                                                            // 裸串与 {name,version,optional} 双形态
```

写回纪律：`RewriteMode` 解析→改字段→yaml 序列化，嵌套 requires_mcp/provides 不丢；
无 frontmatter/未闭合拒绝操作（不猜格式）。`RewriteBody` 保留围栏原文仅换正文。

---

## 六、L4 工具与上下文层

### acpx —— CLI 编码 agent 统一调用

9 家 CLI agent（claude/zcode/codex/opencode/minimax/kimi/gemini/qwen/mimo）+
GenericAgent 通用出口，统一契约：

```go
reg := acpx.NewRegistry() // 缺省注册 9 家
res, err := reg.Run(ctx, "claude", acpx.RunRequest{
    Prompt: "修复 X", WorkDir: repo, Sandbox: acpx.SandboxReadonly,
    MaxTurns: 20, Timeout: 10 * time.Minute,
    OnEvent: func(e acpx.Event) {}, // text/thinking/tool_call/tool_result/error/result
})
// res.Text/Usage/SessionID/Duration/ExitCode
tool := reg.AsTool() // run_coding_agent：包成 eino 工具给 ReAct agent 自主调度
```

内建生产纪律：进程组执行（Setpgid → 超时/取消 TERM 整组 → 3s 宽限 → KILL）；
环境白名单（绝不继承密钥）；stdout 8MB + 单行 1MB 限容；prompt 以 `-` 开头自动
前置换行（防 CLI flag/沙箱旁路注入）；`errors.Is(err, procx.ErrTimeout/ErrCanceled)`
程序化区分超时与取消。各家的 Sandbox 支持矩阵与协议细节见 README。

**错误纪律与事件转发（v0.10.10）**：exit=0 错误形态（mimo error 事件
`error.data.message`、gemini error 信封、codex error 事件）**如实上抛**，不因
部分文本而掩盖；error/step 类事件全量经 OnEvent 转发（transcript 可排障）。
探活判定以 `Run` 的 `err != nil` 为门槛。mimo `-m` 须 `xiaomi/` 全名前缀，
`Mimo.DefaultModel` 支持配置化缺省模型（本机缺省可能被服务端下线）。

**能力契约（v0.10.8）**：`Agent` 接口含 `Capabilities() Capability`（Model/Session/
MaxTurns/AllowedTools/Sandbox 五字段支持声明，编译期强制）；`Registry.Run` 对请求中
声明不支持的非零字段 **fail-fast 报错**——请求了即须兑现，拒绝静默降级
（历史教训：kimi 收到 Sandbox=readonly 实则全自主裸跑）。GenericAgent 的能力由
argv 模板占位符推导。

**procx**（v0.7.1 acpx.RunProcess，v0.10.11 迁出为独立包）：只要进程组托管
纪律、不需要 Agent 解析层的调用方（workcopy 跑 git / mcp spawn stdio server /
包装外部 cli 审查器）共用同一出口——纪律只有一份：

```go
stdout, stderr, code, err := procx.Run(ctx, procx.RunRequest{
    Argv: []string{"cli", "run"}, Dir: dir, Env: []string{"NEEDED_VAR"},
    Timeout: 5 * time.Minute, MaxStdout: 1 << 20,
})
// 进程组执行（Setpgid → 超时/取消 TERM 整组 → 宽限 SIGKILL）+ 环境白名单
//（procx.ChildEnv——mcp 自行 spawn 子进程时共用）+ stdout/单行双限容 +
// errors.Is(err, procx.ErrTimeout/ErrCanceled) 程序化分类
```

### mcp —— MCP 工具池

```go
pool := mcp.NewPool(
    mcp.ServerConfig{Name: "docs", URL: "http://docs.svc/mcp", Headers: {...}},
    mcp.ServerConfig{Name: "lint", Command: []string{"lint-mcp", "serve"},
                     Env: []string{"LINT_CONFIG"}}, // env 白名单：按名透传，绝不继承密钥
)
pool.OnError = func(server string, err error) {...}
tools, err := pool.Tools(ctx, []mcp.ToolSpec{{Server: "docs", Allow: []string{"search_docs"}}})
defer pool.Close()
```

lazy 建连缓存 + Initialize 握手；列举失败自动摘除坏连接（下次重建）；Close 后拒绝
新建；Allow 全部未命中经 OnError 告警。schema 转换委托 eino-ext，无自造轮子。
出口统一经 `WrapErrorAsObservation` 包装（v0.10.15）：isError:true 的 MCP 业务
失败降级为文本观察回传 LLM（修正参数/换路径由模型自行决定），不再以
NodeRunError 炸掉整个 agent 步骤；传输层等其他错误原样上抛。

### workcopy —— Git 工作副本沙箱

```go
wc := workcopy.NewPool(workRoot, log)
wc.CredentialOf = func(platform string) (baseURL, token string, ok bool) {...}
dir, err := wc.Ensure(ctx, workcopy.WorktreeKey{
    Platform: "gitea", Owner: "o", Repo: "r", Number: "1", HeadSHA: sha, DefaultBranch: "main"})
defer wc.Release(key)   // 引用归零保留目录（同 PR 复用），TTL 交 Sweep 兜底
wc.Sweep(time.Hour)     // 兜底回收 rc=0 超时目录与孤儿（不会删 rc>0 在用目录）
```

浅克隆 base + fetch PR head + checkout；singleflight 防并发重克隆（登记在 flight 内，
杜绝共享到已删目录）；凭证嵌 clone URL 绝不落盘（错误信息经 scrub 脱敏）；
**多实例部署时每实例独立 WorkRoot**（`WorkRoot/instance-<id>/`）。

保留复用与增量刷新（v0.10.6【行为变化】）：`Release` 引用归零**不再立即删目录**——
保留供同 PR 下次审查复用；同 PR 换 head（新推送）时 `Ensure` 领用保留目录做增量
刷新（只 fetch 新的 PR refspec，base 分支浅对象已在库，省整轮重克隆），刷新失败
（force push 抹掉旧引用等）回落全新克隆；不同 PR 不复用；rc=0 目录的 TTL 回收
交 `Sweep` 兜底。

### knowledge/rag —— 双后端知识检索

```go
// Local（TF-IDF，零外部依赖；重扫 copy-on-write）
local, _ := rag.NewLocal("/path/to/knowledge")
chunks, _ := local.Retrieve(ctx, "如何配置 X", 5, nil)

// Milvus（向量；Index 幂等 Upsert——重复启动重刷不累积重复行）
store, _ := rag.NewMilvusStore(ctx, rag.MilvusConfig{Address: "localhost:19530", Dimension: 1024},
    rag.NewOpenAIEmbedder(embedURL, embedKey, "text-embedding-v3", 1024))
store.Index(ctx, "doc.md", content)
store.DeleteFile(ctx, "doc.md")

// 统一接口 + eino 工具化（渐进披露到 ReAct agent）
var svc rag.KnowledgeService = ... // NewNoop() 兜底
tool := svc.AsTool()               // search_knowledge
```

chunk 规则：标题/空行分段、代码块保护、块间 100 rune 重叠、4000 rune 硬上限
（防超长行/未闭合围栏撑爆 VarChar）。过滤字段白名单：file / heading。

### websearch —— 公开资料检索（v0.9.0）

与 knowledge/rag 互补：本包面向外网公开资料（概念/方法论类兜底），rag 面向私有知识库。

```go
type Service interface {
    Search(ctx context.Context, query string, topK int) ([]Result, error)
}
ws := websearch.NewSearxng("http://searxng.local", 10*time.Second)
ws.Language = "zh-CN" // 空串=不传 language
results, err := ws.Search(ctx, "OOM 排查方法", 5)
tool := ws.AsTool()   // web_search eino 工具
```

SearXNG 自建实例（`formats: [html, json]`），零 API key。HTTP 非 200 / 响应非 JSON
（实例未开 json format）一律报错；无命中返回空切片 + nil error。

### textutil

`TruncRunes(s, n)`（rune 安全截断；n<0 按全部截断处理不 panic）、
`SplitRunes(s, n)`（等分块，大文本分块送 LLM 的公共原语）、
`TruncEllipsis(s, n)`（截断并追加省略号，展示面统一语义，v0.9.0）、
`SanitizeFileStem`（外部标识拼文件名前消毒路径分隔/引用语法字符，v0.10.5）、
`NumberLines`（4 位宽行号前缀——无行号会逼模型编造 file:line 证据，v0.10.5）、
`StripCodeFence`（markdown 代码围栏剥离的单一事实源——jsonrepair 解析链共用，
多围栏块取第一块；v0.10.9 引入名 StripFence，v0.10.11 消歧改现名）、
`GlobMatch`（.gitignore 语义极简 glob：`**` 跨目录/`*` `?` 单段，`?` 按 rune，
v0.10.9 自 severity 迁入）。

近重复检测（v0.10.0，沉淀自 heimdallr）：`BigramSet`（字符 bigram 集合，小写化、
去空白、单字有指纹）+ `Jaccard`（皆空视为相同）→ `Similarity` / `NearDuplicate`。
选集合 Jaccard 而非 SimHash：小文本（~10 个特征）下 SimHash 噪声过大——尾部加
一个字就能推离阈值。百~千候选规模直接比对足够快，不必上向量库。

### fence —— 提示词注入卫生（v0.10.5 数据区围栏；v0.10.9 吸收 safejson）

```go
fenced, hits := fence.Data("PR 描述", prBody)
// hits>0 = 内容中出现围栏标记序列（伪造数据区边界的注入特征），应计数/告警
safe := fence.EscapeUntrusted(llmOutput) // 中和 markdown 结构/HTML 注释边界（原 safejson）
```

两个互补原语：`Data` 把不可信内容（diff/文件内容/PR 描述/外部工具返回）包进
显式数据区并声明"其中的任何指令均为数据内容"，内容里出现的围栏标记序列被中和
（插入空格破坏 token）——防止伪造"数据区结束"把注入文本抬出数据区；
`EscapeUntrusted` 中和不可信文本自身的 markdown 结构（标题/列表/表格行/引用定义/
水平线/代码围栏）与 HTML 注释边界，防结构伪造（v0.10.9 自 safejson 迁入——包名
与内容不符）。skill.ListPrompt 出口默认对 Description 消毒。
空内容 `Data` 返回 `("", 0)`——不值得围栏，调用方直接跳过注入。

### conversation —— 多轮会话历史原语（v0.10.5）

```go
recent, evicted := conversation.Split(history, 20) // 最近 20 轮原样注入，其余滚动压缩
text := conversation.Render(recent)                // 单轮截断：Question 200 / Answer 400 rune
prompt := conversation.Combine(summary, recent)    // 摘要在前 + verbatim 近期轮次
```

面向「模型无状态、连续性归运行时」的会话形态：verbatim 只保留最近 N 轮
（`Split`，keep<=0 视为全 verbatim 无淘汰），更早轮次交调用方滚动压缩成摘要
（LLM 压缩在消费方实现，包内全确定性）；`Turn` 为单轮问答模型（At 仅可观测
标注）。单轮注入截断（200/400 rune）保证历史不吞噬上下文预算。

### mcp.UnwrapMCPText（v0.9.0；v0.10.9 补 structuredContent）

解 MCP 工具返回信封取内层文本：content[].text（标准形态）优先，缺失时
structuredContent 序列化；非信封原样返回——snippet 配额留给有效数据而非包装层。

### pack —— 领域包契约清单（v0.9.2）

多包布局（`_shared/` 共享基线 + `<包>/`）下的 MCP 工具面契约：conf 是装配事实源，
清单是契约事实源，两者对账由调用方做（无清单=现状语义，授予反查推导）。

```go
manifests, warns, err := pack.LoadToolManifests(fsys)
m := manifests["security-assistant"]
m.Has("collect_logs")   // 工具名对账
m.Version               // min_version 对账基准（skill.VersionInRange）
```

加载规则（确定性、可测试）：包清单**整文件替换** `_shared` 基线（不是 merge，
扩展清单须拷出全量再增改）；包间同名冲突 → 警告 + 包名字典序第一生效；
目录不存在=无清单（合法）。警告文案即治理信号——两包真争同一 server 属治理问题，
暴露给人裁决。

### lineage —— 装配血缘图（v0.9.2）

expert → skill → MCP server 三类节点的声明式依赖图：used_by/granted_by 反查字段的
单一事实源（多 list API 共享一份计算，消灭双份漂移）。只管图机制，不绑定装配实现
——有效技能集（覆盖感知）由装配方经中性输入注入：

```go
lin := lineage.Build(experts, skills, manifests) // used_by/grants/requires_mcp 边 + 确定性排序
impacts := lineage.Diff(prev, lin)               // reload 影响面：版本/成熟度跃迁、工具面增减、引用边增减
focus, ok := lin.Focus("skill-x", 2)             // 焦点邻接子图（无向，depth≤2）
hub.Set(lin, manifests)                          // Hub：reload/Resync 重建，list API 共享读（nil 安全）
```

Diff 契约：nil 基线（重启首帧）不产出 impact——把全量资产报成「(新增)」是噪音；
工具面 diff 取 manifest ∪ 显式授予并集（只 diff manifest 会让授予变更静默），
任一侧 Unlimited 授予则枚举不可知、跳过不产出。

---

## 七、L5 运行时基础设施

### breaker —— 熔断器

```go
b := breaker.New(3, 5*time.Minute) // 连续 3 败熔断，冷却 5 分钟
if b.Allow(now) {                  // Allow==true 后必须恰好配对一次 Success/Failure/Abandon
    if err := do(); err != nil { b.Failure(now) } else { b.Success() }
}
bs := breaker.NewBreakers(0, 0)    // 按 key 的熔断板（0 = 用缺省参数）
```

closed → open（连续失败达阈值）→ half-open（冷却后放行一个探测）→ closed/reopen。
两道防线：**探测超时兜底**（`DefaultProbeTimeout`=1min，探测方失联后放行新探测，
不会永久卡死）；**open 期 Failure 不续期冷却**（高流量下被拒请求的 Failure 不会把
熔断器钉死在 open）。

**Abandon（v0.10.4）**：Allow==true 后调用方在取得结果前终止（任务级取消、优雅
停机）时调 `Abandon()`——结果未知，不计成功也不计失败；半开态立即恢复放行新探测
（不必等探测超时失联），closed 态无副作用。

### logredact —— 凭据脱敏（v0.9.0）

与 fence（注入卫生）正交：本包打码凭据，防密钥进日志/审计载荷。

```go
logredact.Redact("nats://ops:s3cret@host:4222") // nats://ops:****@host:4222
logredact.RedactValue(payload) // 递归脱敏 map/slice 中的字符串
logredact.RedactSecrets(msg, token) // 抹除调用方已知确切秘密（长串优先，不回填）
```

规则：URL 内嵌账号口令、token/secret/password/api_key 键值对、Authorization/Bearer 头、
平台 API token 裸形态特征前缀（sk-/ghp_/AKIA 等，v0.10.15）与裸 JWT 三段式。
三机制分工：Redact=未知形态模式化打码；RedactSecrets=已知值精确抹除（v0.10.8）；
Masker=低敏感拓扑标识令牌化（可 Restore 回填）——高敏感秘密不走 Masker。

### hotplug —— 插拔与热替换（v0.9.0）

```go
pb := hotplug.NewPlugboard(allSlugs, disabled) // nil 指针=全启用
pb.Enabled("expert/io") / pb.Disabled()

h := hotplug.NewHolder[Snapshot]()
h.Store(newSnap) // 原子换整体；读无锁
cur := h.Load()  // 运行中请求继续用旧快照跑完，新请求即时用新快照
```

### jsonrepair —— LLM 宽容 JSON（v0.9.0）

栅栏剥离 → 散文抽对象 → 语法修复 → 标量归一。领域 schema 留给调用方。

```go
jsonrepair.ExtractJSON(s) / ExtractObject(s) / Repair(s)   // 全角、非法转义、尾逗号、未闭合括号
schema := &jsonrepair.Schema{
    StringKeys: map[string]bool{"summary": true},
    ListKeys:   map[string]bool{"steps": true},
    OnMap:      func(m map[string]any) { /* 每 map 子节点归一后的领域钩子 */ },
}
err := jsonrepair.ParseLenient(raw, &v, schema)
```

### jsonrepair.Unmarshal —— 模型输出 JSON 宽容解析出口（v0.9.8 llmjson；
v0.10.11 并入本包）

宿主不拼装：一次调用走完 ExtractJSON 快路径 → 语法修复 → 全链宽容三级尝试，
半损坏产出（全角结构标点/尾逗号/非法转义/截断未闭合）不再整轮判死。领域
schema 校验（字段语义/枚举约束）仍归调用方。（沉自 argus/internal/llmjson；
v0.10.11 原 llmjson 包删除——其唯一依赖 llm.ExtractJSON 一并迁入 jsonrepair，
解析原语不再依赖 LLM 客户端栈。）

```go
var report ReviewReport
err := jsonrepair.Unmarshal(modelOutput, &report)
// 全败时错误同时携带严格与宽容两路原因，可直接回喂 LLM 重试
```

### worker —— DB 即队列 + 选主

```go
wp := &worker.Pool{Queue: myQueue, Run: handle, N: 4, Log: log}
wp.Start(ctx)
defer wp.Stop(60 * time.Second) // 两阶段：取消领取 → grace → 硬取消执行
```

- `TaskQueue` 三方法接口（ClaimNextPending 原子抢占 / TouchRunningHeartbeats /
  ResetRunningToPending）由调用方按存储实现（GORM 一张表即可）；
- **多副本天然分片**：N 实例共库，抢占互斥；实例崩溃 ≤ StaleRunningAfter(90s) 被他实例接走；
- 任务 panic 只损失该任务（worker 存活，心跳过期复位重跑）；
- Stop-before-Start 安全（不会烧穿停机路径）；Start 幂等。

**LeaderElector**（v0.7.3）——多副本时"只能跑一份"的控制面组件（出站轮询/定时清理）：

```go
// 存储：实现 worker.LeaseStore（SQL 一条条件 UPSERT：WHERE lease_key=? AND (holder=? OR expires_at<?)）
// 或直接用内置 PG 实现（v0.9.0）：
ls := pglease.NewPGLeaseStore(sqlDB).WithTable("bq_lease") // 表名白名单，防拼接注入
_ = ls.Migrate(ctx)
e := worker.NewLeaderElector(ls, "argus/poller", instanceID,
    30*time.Second, 10*time.Second,
    worker.WithOnGained(func() {...}), worker.WithOnLost(func() {...}))
e.Start(ctx)
if e.IsLeader() { ... } // 失联 ≤ttl 自动换主；Stop 主动让位
```

**worker/pglease**（v0.9.0）：`TryAcquire` 原子 UPSERT（空闲/过期/本人持有 → true）、
`Release` 仅持有者生效、`Migrate` 建表。缺省表名 `agentkit_lease`。

多副本全景（任务面分片 + 控制面选主）：

```mermaid
flowchart TD
    subgraph 实例A
      WA["worker pool ×N"] --> QA["ClaimNextPending 原子抢占"]
      HA["心跳续期 running 任务"]
      EA["LeaderElector 竞选"]
    end
    subgraph 实例B
      WB["worker pool ×N"] --> QB["ClaimNextPending"]
      HB["心跳"]
      EB["LeaderElector 竞选"]
    end
    QA --> DB[("tasks 表：pending / running / done")]
    QB --> DB
    HA --> DB
    HB --> DB
    EA --> LS[("leader_leases 租约")]
    EB --> LS
    LS -- "仅 leader 放行 tick" --> P["出站轮询 poller"]
```

### progress —— 泛型事件总线

```go
bus := progress.NewBus[Event]()
ch, cancel := bus.Subscribe(ctx) // 每订阅者 64 缓冲；cancel 与 ctx 双向收口（无 goroutine 泄漏）
defer cancel()
bus.Publish(ev)     // 满了就丢（有损是声明的设计）；bus.Dropped() 丢弃计数可观测
```

nil bus 安全；Publish 永不阻塞。

### safejson（已并入 fence，v0.10.9）

`EscapeUntrusted` 与数据区围栏同属注入卫生，v0.10.9 起归 `fence.EscapeUntrusted`
（原 `safejson` 包名与内容不符已删除）：

```go
safe := fence.EscapeUntrusted(llmOutput) // 中和标题/列表/围栏/水平线/表格行/引用定义/HTML 注释
```

用于把不可信文本（LLM 产出/PR 描述）渲染进报告前中和结构伪造。前提：下游渲染器
仍需自行 sanitize 裸 HTML 标签。

### reportutil —— 评审/评测报告后处理（v0.10.11 severity/sampling/stats 合并）

同一消费链的三段原语合一（归一严重度 → 聚簇去重 → 置信区间/检验），零依赖：

```go
sev, ok := reportutil.Normalize("CRITICAL")      // → "high", true（词表 high/medium/low）
sev, _ = reportutil.Normalize("P0")              // → "high"（别名折叠，同 P1→medium/P2,P3→low）
rank := reportutil.Rank("high")                  // 排序权重
fp := reportutil.Fingerprint(file, comment)      // SHA256 前 16 位（跨轮去重；聚簇签名通道）
textutil.GlobMatch("web/**", "web/src/a.go")     // .gitignore 语义；? 按 rune（v0.10.9 迁 textutil）

groups := reportutil.Aggregate(outputs,          // best-of-N 聚簇：Count≥2 = 相互复现
    func(o Finding) []string { return []string{o.Fingerprint, o.File + o.NormText} },
    func(a, b Finding) bool { return sameFinding(a, b) })

lo, hi := reportutil.WilsonCI(8, 10, 1.96)       // 通过率诚实区间（小样本）
p := reportutil.McNemarExact(6, 0)               // 配对差异显著性（6:0 单向翻转 → p≈0.031）
```

### audit —— 审计日志

```go
al := audit.New(log, "argus-audit") // nil logger 回退缺省；nil receiver 安全
al.Log("feedback.suppressed", "repo", "o/r", "fp", "abcd1234")
```

reportutil 详解见上（聚簇语义与 Wilson/McNemar 口径不重复展开：
「与首见者等价才并入」防链式漂移；Wilson 区间回答"至少多好"、McNemar 只看
方向翻转的配对）。

---

## 八、L6 可观测层 —— obsx / langfuse

对齐 eino callbacks 体系的 LLM 调用追踪——一行启用，ctx 链上所有 eino 组件调用
（含 ReAct agent 直用 RawModel 的路径）自动产出结构化日志：

```go
ctx = obsx.InitLLMObservability(ctx, log, obsx.Options{
    SlowThreshold: 60 * time.Second,
    PreviewLen:    0, // 默认 0 = 不落消息内容（可能含用户代码/凭证），勿误设
})
// v0.10.12：Options.OnUsage 退役——TracingHandler 只做 trace；callbacks 侧
// 记账统一 llm.NewUsageHandler（ReAct RawModel 旁路的成本/预算记账入口）
// 之后自动产出：llm.call.start / llm.call.end / llm.call.error
// 字段：stage/component/model/duration_ms/prompt|completion|total|reasoning_tokens
```

`WithStage(ctx, "R1")` 标记业务阶段（llm 包自动注入）；慢调用自动升级 Warn。

```mermaid
flowchart LR
    CALL["任意 eino 模型调用<br/>（直连 Generate 或 ReAct RawModel）"] --> CB["callbacks 触发"]
    CB --> H["obsx TracingHandler"]
    H --> LOG["llm.call.start / end / error<br/>stage/model/耗时/真实 usage"]
    CB -- "llm.NewUsageHandler" --> CT["CostTracker<br/>（任务级 + 全局）"]
```

langfuse（v0.10.0，沉淀自 heimdallr）——Langfuse Public API 只读客户端，把已经
发生的历史 trace 拉回来（`FetchBatch` 列表分页 + 逐条详情合并 observations；
`Query` 选择口径可复述）。契约类型对齐官方 OpenAPI；usage/cost 新旧口径兜底
（`UsageTokens`/`UsageCost`）。与 obsx 互补：obsx 写侧落日志，langfuse 读侧回放。
错误前缀 `langfuse:`；服务端非 200 显式失败，不静默返回空批次。

---

## 九、七架构模式支持

| # | 架构 | 载体 |
|---|---|---|
| 1 | Single Agent | `llm` + `agentrun.Run` |
| 2 | ReAct | `agentrun` + `toolprior` |
| 3 | Plan-and-Execute | `agentrun.PlanAndExecute` |
| 4 | Reflection | `reflection.Refine` |
| 5 | Router+Skill | `router.Run` + `skill.AsSkillTool` |
| 6 | Blackboard | `blackboard.Convene` |
| 7 | Graph Workflow | eino `compose.Graph`/Branch + adk Sequential/Parallel/Loop/Supervisor（原生，agentkit 包作节点构件） |

**每种的"何时该用 / 何时不该用"与陷阱清单**（含六问选型决策流）见
[docs/patterns.md](patterns.md)。一句话总纲：**从最简单的架构开始，被现实逼着升级。**

---

## 十、横向能力专题

### 可靠性纵深

```
请求 → StageRouter → Resilient（4xx 不重试/5xx 重试/429 立即切换）
     → 每模型熔断（探测超时兜底）→ 预算短路 → fitInput 裁剪 → eino provider
```

重试卫生：指数退避 + jitter、429 下限 5s 封顶 30s、非幂等风险为零（生成无状态）。
确定性 4xx 不重试——重试失败不如切模型或直接报错。

### 成本治理

```
预算：BudgetConfig 由窗口单一基准派生（diff 上限 35%/任务预算 60%/输出 5%）
记账：llm.Client.OnUsage（直连）+ llm.NewUsageHandler（callbacks 单出口，防重护栏内置）→ CostTracker
定价：llm.PriceOf 前缀定价表估算成本（精确 → 最长前缀回落，未配价归零）
路由：StageRouter 按阶段分级配模型（大窗口吃长输入/快模型跑判定/强模型保质量）
```

### 多副本扩展

任务面：worker 队列原子抢占分片 + 心跳接管（实例崩溃 ≤90s 恢复）；控制面：
LeaderElector 租约选主（轮询/清理类单份组件）。前提：共享 DB 用网络库（SQLite 仅单副本）。

### 安全模型

| 面 | 防线 | 位置 |
|---|---|---|
| 子进程环境 | 白名单透传，绝不继承密钥 | acpx baseEnvAllow / mcp whitelistEnv |
| CLI flag 注入 | `-` 开头 prompt 前置换行 | acpx promptArg |
| 提示词注入（数据区） | 工具返回包 DataFence 围栏 | 消费方（Argus）模式 |
| 报告结构伪造 | markdown 结构中和 | fence |
| MCP 工具暴露面 | Server/Allow 双白名单 | mcp ToolSpec |
| 供应链 | CLI 命令仅来自配置白名单，仓库配置无权声明 | 消费方模式 |

---

## 十一、生产实践：Argus 参考实现

Argus（代码审查平台）是 agentkit 的完整参考消费者（49+ 引用点），组合方式：

```
webhook/poller ──▶ worker pool（多副本，ClaimNextPending 分片 + 心跳接管）
                ──▶ runner.Execute
                      ├─ obsx.InitLLMObservability（全链追踪 + 成本回流）
                      ├─ StageRouter（R1/R2 阶段模型覆盖 + 预算注入）
                      ├─ Workflow:
                      │   R1 需求解析（llm.GenerateJSON）
                      │   R2 变更理解（长 diff 分块 textutil.SplitRunes）
                      │   R3 完成度判定（agentrun ReAct + toolprior 工具表：
                      │        get_file/get_diff → search_knowledge/use_skill → MCP 限 5 次）
                      │   R4 专家 fan-out（builtin ReAct / cli=procx.Run / acpx=9 家 CLI；
                      │        squads 交叉编队；预算裁剪；熔断门）
                      │   R5 Merger（severity 指纹去重 + 跨轮抑制 + 交叉分歧标注）
                      ├─ workcopy 沙箱（引用计数；实例隔离目录）
                      ├─ knowledge 飞轮（误报反馈 → Milvus 回流 → R3 检索命中）
                      └─ progress 总线（SSE 进度流）+ audit 留痕
```

新项目接入建议按同一顺序装配：存储 → LLM（Client/Resilient + 预算）→ 观测 →
工具层 → 编排 → 队列。

同链路的渲染版全景：

```mermaid
flowchart TD
    WH["webhook / poller（选主收敛）"] --> WP["worker pool 多副本"]
    WP --> EX["runner.Execute"]
    EX --> OBS["obsx 追踪 + 成本回流"]
    EX --> SR["StageRouter（R1/R2 覆盖 + 预算注入）"]
    EX --> WF["Workflow"]
    WF --> R1["R1 需求解析（GenerateJSON）"]
    WF --> R2["R2 变更理解（SplitRunes 分块）"]
    WF --> R3["R3 ReAct（toolprior 工具表 + R3ChatModel 覆盖）"]
    WF --> R4["R4 专家 fan-out<br/>builtin ReAct / cli=procx.Run / acpx 9 家 / squads 编队"]
    WF --> R5["R5 Merger（指纹去重 + 抑制 + 分歧标注）"]
    R3 -. "search_knowledge" .-> KW[("Milvus 知识库<br/>含误报回流")]
    R4 -. "工作副本" .-> WC[("workcopy 沙箱")]
    R5 --> POST["报告回帖（fence 反注入）"]
    EX --> BUS["progress 总线 → SSE"]
```


---

## 十二、版本纪律与陷阱清单

### 发布纪律

- **Go module proxy 永久缓存 tag——严禁 force-push 已发布 tag，必须递增版本号**；
- checksum 不匹配时删除 go.sum 对应行重新 `go mod tidy`；
- 私有模块：`GONOSUMCHECK='git.enjoye.top/*'`（或 GOPRIVATE）。

### 高频陷阱（审查实战沉淀）

| 陷阱 | 正解 |
|---|---|
| ReAct + LimitCalls 重试后限流不重置 | 用 `Config.ToolsFactory` 每次尝试重建工具表 |
| ReAct 流量绕过降级/预算 | RawModel 旁路是设计取舍；成本经 llm.NewUsageHandler 注入 callbacks 记账 |
| 熔断 Allow 后不配对 Success/Failure | 半开会卡死——v0.7.0 起有探测超时兜底，但仍应配对 |
| prompt 传给 CLI 被当 flag 消费 | acpx 已自动防护（`-` 开头前置换行）；自拼 argv 时注意同类注入 |
| MCP 子进程继承宿主密钥 | mcp 包 env 白名单已强制；自写 spawn 时同样只能白名单 |
| 黑板/事件总线 goroutine 泄漏 | progress cancel 与 ctx 双向收口已内建；自写时记得双向 |
| 共享 workcopy root 多实例互删 | 每实例 `WorkRoot/instance-<id>/` 隔离 |
| 多字节字符被截断腰斩 | 一律走 textutil.TruncRunes/SplitRunes |
| runner Stop 先于 Start 烧穿停机 | v0.7.0 起已防御；自写生命周期建议 mutex 而非 sync.Once |
| Go 模型测试桩只实现 Generate | eino v0.9 BaseModel 还要求 Stream 方法 |

### 版本历史摘要

- **v0.1.0**：从 Argus 提取的 11 包首发
- **v0.2.x**：llm Resilient 降级链 / 预算
- **v0.4.0**：obsx 可观测
- **v0.5.x**：mcp 工具池 / skill 决策使用 / toolprior
- **v0.6.0**：agentrun ReAct 样板
- **v0.7.0**：全库审查修复（3 Critical + ~20 Important；mcp env 白名单 / acpx 注入防护 / 熔断兜底 / Upsert 幂等…）
- **v0.7.1**：acpx.RunProcess / textutil.SplitRunes 导出
- **v0.7.2**：obsx.OnUsage 真实 usage 回流
- **v0.7.3**：worker.LeaderElector 选主
- **v0.8.0**：七架构补全（PlanAndExecute / reflection / router / blackboard / patterns.md）
- **v0.8.1**：llm.StageRouter 分阶段路由
- **v0.8.2**：deps 升级（ekit v0.27.2 / go 1.26.0，grpc·protobuf 传递升级）
- **v0.8.3**：router 门槛自守（行为变化）+ 全面可靠性修复（2C+6I）+ rag 等价性能优化 + acpx 文件重组
- **v0.8.4**：agentrun 观测面补全（reasoning 事件 + tool_call 携带 Args）
- **v0.9.0**：从 bianque 抽取通用组件——新包 logredact / worker/pglease / websearch /
  hotplug / jsonrepair；扩展 skill.Library（多根热替换）、llm.UsageHandler（完整用量+Labels）、
  mcp.UnwrapMCPText、textutil.TruncEllipsis
- **v0.9.1**：jsonrepair.Schema.Mutate → OnMap（子节点归一后触发）
- **v0.9.2**：从 bianque 契约工作沉淀——新包 pack（工具面契约清单）/ lineage（装配血缘）；
  skill 版本化契约（Validate 加载即校验【行为变化】/ RewriteMode·RewriteBody 写回 /
  VersionInRange / Decl / DeprecatedExpiredInUse；MCPDep.Tools·MinVersion、Provides、ReplacedBy）
- **v0.9.3**：router 意图槽位（选路同调用顺带提取，`Decision.Slots`，自守恒）；
  skill frontmatter 新增 requires_config（集成配置依赖声明级契约）
- **v0.9.4**：bianque 生产装配四连——llm.FailoverModel 主备降级装饰器；
  agentrun 副作用感知重试守卫【行为变化：变更类工具后不再整体重跑】；
  router 中文词表匹配内核（KeywordHit 四件套）；logredact 合并 bianque 增强
  规则（凭证词 `: ` 空格形态 + PEM 整段打码）
- **v0.9.5**：llm.PriceOf 前缀定价估算；新包 dispatch（通用派发守卫，
  EdgeSource 拓扑注入 + DenyError 结构化拒绝）——第 27 包
- **v0.9.6**：bianque 操作审计门沉淀——新包 policy（四模式三层裁决 + Arbiter 灰区仲裁 +
  AuditGate 装饰器）——第 28 包；router KeywordPostNegated 后置否定守门
- **v0.9.7**：新包 clarify（澄清/标准化词表内核：term_map/LoadVocab/Validate +
  OrdinalIndex 序数指代 + ResolveAnswer 回答消解）——第 29 包
- **v0.9.8**：新包 llmjson（模型输出 JSON 统一解析入口：ExtractJSON 快路径 →
  jsonrepair.Repair 语法修复 → ParseLenient 全链宽容，错误携带两路原因）——第 30 包
- **v0.10.0**：heimdallr 沉淀——textutil 近重复检测（BigramSet/Jaccard/NearDuplicate）；
  新包 stats（WilsonCI/McNemarExact 评测统计）——第 31 包；新包 langfuse（Public API
  只读客户端，FetchBatch/GetTrace/Query + UsageTokens/UsageCost）——第 32 包
- **v0.10.1**：breaker WithProbeTimeout——半开探测时限可配（NewBreakers 变参 Option，
  探测时限应 ≥ 被保护操作最长正常耗时）
- **v0.10.2**：logredact 凭据脱敏升级——Redact（高敏感模式化打码，永不回填）+
  Masker/Restore（拓扑标识 «Tn» 令牌化进 LLM、展示面回填，回填不回灌二次输入）
- **v0.10.3**：agentrun Event.CallID 原生透传（tool_call/tool_result 精确配对根基）；
  skill use_skill 出参自证（name/requested/version/checksum）+ Library.Resolve 回 Version
- **v0.10.4**：breaker Abandon——放弃在途半开探测的取消/终止语义（结果未知不计
  统计，半开态立即恢复放行新探测，closed 无副作用）
- **v0.10.5**：argus 侧验证过的通用能力上游化——新包 fence（提示词数据区围栏，第 33 包）/
  conversation（多轮会话窗口原语，第 34 包）/ sampling（best-of-N 确定性聚簇，第 35 包）；
  textutil SanitizeFileStem/NumberLines；severity Normalize 别名折叠
  （P0/fatal/urgent→high、P1/major→medium、P2/P3/trivial→low，原词表行为不变）
- **v0.10.6**：workcopy 沙箱保留复用【行为变化】——Release 引用归零不再立即删目录，
  同 PR 换 head 增量刷新（只 fetch 新 PR refspec，省整轮重克隆），TTL 交 Sweep 兜底
- **v0.10.7**：高内聚低耦合全面重构（导出 API 零变化）——llm/acpx/agentrun/worker/
  workcopy 重复骨架下沉（walkChain/runCLI/drainEvents/guardedCall/runGit 复用
  acpx.RunProcess）；skill frontmatter schema 与围栏语义单源化；pack.LayoutDirs
  布局约定单源；lineage SkillFromMeta + Impact.SkillsAdded/Removed；dispatch
  拒绝原因常量
- **v0.10.8**：acpx 能力契约【行为变化】——Agent 接口新增 Capabilities（编译期
  强制），Registry.Run 对不支持字段 fail-fast（拒绝 Sandbox 类安全语义静默降级）；
  skill Checksum/Version/Content 两 Provider 口径统一【行为变化】——checksum=
  sha256(正文)[:16]、version 取 frontmatter 声明；logredact.RedactSecrets
  （已知秘密精确抹除），workcopy 脱敏切换至该单源
- **v0.10.9**：第三轮内聚优化（llm 重试骨架单源 generateRetry；FailoverModel 泛化
  N 模型链 + Resilient.RawModelWithFailover 组合点；safejson 并入 fence 更名
  EscapeUntrusted——第 34 包；severity 指纹迁 sampling、glob 迁 textutil；
  围栏单源 textutil.StripFence；环境白名单单源 acpx.ChildEnv；rag/websearch
  工具错误契约统一；obsx 记账防重护栏；breaker/mcp/progress 杂项收敛）
- **v0.10.10**：mimo 实弹修复——错误详情路径修正（error.data.message）+ 纯错误跑
  如实报错【行为变化】+ error/step 事件全量转发（transcript 排障）+ DefaultModel
  配置化（-m 须 xiaomi/ 全名）；gemini error 信封/codex error 事件/claude
  is_error result 同纪律横向补齐【行为变化：gemini 错误信封现报错】
- **v0.10.11 续（第四轮包收敛，包数 34→33）【破坏性，无兼容层】**：
  新包 procx（acpx 进程执行纪律迁出——acpx/mcp/workcopy 共用，acpx 不再兼
  「CLI agent 适配」与「全仓进程托管」两角）；新包 reportutil（severity/
  sampling/stats 合一）；新包 httpx（langfuse/rag/websearch 三份 HTTP+JSON
  样板单源，修 embedder 吞读错/UTF-8 腰斩两缺陷）；llmjson 并入 jsonrepair
  （解析原语不再依赖 LLM 客户端栈）；obsx.TokenUsageOf 用量提取单源（修
  compose 场景 obsx 侧漏采）【行为变化】；skill AliasResolver.CanonicalName
  签名加 ctx（scanAliases 不再 Background 断链）；router.Do→Run、
  reflection.Output/agentrun.Answer→Text 词表统一；llm.NewFailoverModel 改
  ChainLink 链节式（消平行切片错位）；lineage.Hub 复用 hotplug.Holder；
  textutil.StripFence→StripCodeFence 消歧 + TruncNote 截断留痕单源；
  acpx.tokenPair 收敛；全仓错误前缀补齐（skill/clarify/policy/worker/mcp/
  langfuse/rag 约 30 处）；llm 包三份 package doc 合一；jsonrepair
  Flatten*/CoerceString 收为非导出
