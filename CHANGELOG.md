# Changelog

## v0.10.1 (2026-09-15)

### Added

- **breaker**: `NewBreakers` 增加变参 `Option`（既有调用方零改动）与
  `WithProbeTimeout`——半开探测时限此前硬编码 `DefaultProbeTimeout`（1min），
  被保护操作带长超时（如 LLM agent 600s）时，探测在途 1min 后即被并发放行第二个
  探测（慢而健康的操作被并发双跑）。探测时限应 ≥ 最长正常耗时。
  `Breaker.Allow`/`Success`/`Failure` 语义不变。

## v0.10.0 (2026-09-14)

heimdallr 通用能力沉淀（近重复检测 / 评测统计 / Langfuse 只读客户端）。

### Added

- **textutil**: 近重复检测原语——`BigramSet` 字符 bigram 集合（小写化、去空白、
  单字有指纹）、`Jaccard` 集合系数（皆空视为相同）、`Similarity` 文本相似度组合
  便利、`NearDuplicate` 阈值判定。可解释、小文本下精确、零依赖；百~千候选规模
  直接比对足够快，不必上向量库（沉自 heimdallr internal/mine 挖掘去重）。
- **stats（新包）**: 评测/对比统计原语——`WilsonCI` 通过率 Wilson 得分置信区间
  （total=0 → (0,0)，结果钳 [0,1]；小样本下比正态近似诚实）、`McNemarExact`
  配对二分类双侧精确检验（无翻转 → 1，p 钳 1）。零依赖，z 由调用方传入
  （沉自 heimdallr internal/report）。
- **langfuse（新包）**: Langfuse Public API 只读客户端——`Client.FetchBatch`
  （列表分页 + 逐条详情合并 observations，串行对自托管友好）/ `GetTrace`、
  `Query` 选择口径（name/session/user/时间窗/tags，选择过程可复述）、官方
  OpenAPI 契约类型 `Trace`/`Observation`（新口径 usageDetails/costDetails 优先、
  旧口径 usage 兜底：`UsageTokens`/`UsageCost`）。只读、零 agentkit 内部依赖，
  与 obsx（写侧落日志）互补（沉自 heimdallr internal/observe）。

### Migrated（heimdallr 侧）

- `heimdallr/internal/mine/dedup.go` bigramSet/jaccard 算法 → `agentkit/textutil`（BigramSet/Jaccard/Similarity/NearDuplicate；领域包装 InstructionBigrams 留宿主）
- `heimdallr/internal/report` WilsonCI/McNemarExact/comb → `agentkit/stats`（原样，comb 转私有）
- `heimdallr/internal/observe/client.go + Trace/Observation 契约类型` → `agentkit/langfuse`（错误前缀 observe: → langfuse:；usageTokens/usageCost 导出为 UsageTokens/UsageCost；MapTrace 映射层留宿主）

## v0.9.8 (2026-09-14)

argus 宽容解析入口沉淀。

### Added

- **llmjson（新包）**: 模型输出 JSON 统一解析入口——`Unmarshal` 三级尝试：
  `llm.ExtractJSON` 快路径（剥围栏 + 首尾括号切片，命中则零额外开销）→ 切片经
  `jsonrepair.Repair` 语法级修复（全角结构标点/非法转义/尾逗号/截断未闭合）→
  `jsonrepair.ParseLenient` 全链兜底（可救散文包裹与截断）。全败时错误同时携带
  严格与宽容两路原因，回喂 LLM 重试无需调用方拼装。领域 schema 校验（字段
  语义/枚举约束）仍归调用方（沉自 argus/internal/llmjson，API 原样）。

### Migrated（argus 侧）

- `argus/internal/llmjson` → `agentkit/llmjson`（argus 改直接消费，内部包删除；
  消费点：builtin/acpx/remote 三适配器 + requirement R3/R3.5/反思修订）

## v0.9.7 (2026-09-14)

bianque 澄清/标准化词表内核沉淀（批次十六 §5 首项落地）。

### Added

- **clarify（新包）**: 口语→规范维度词条（term_map）内核——`Entry`/`Options` 词表模型、
  `LoadVocab` 外置 yaml（根键 term_map，缺文件=零行为、解析失败 fail-fast）、`Validate`
  内在校验（word 唯一/维度键规范/vague⟺clarify 完整；域注册与路由词冲突等宿主拓扑
  校验留宿主装配期）。`OrdinalIndex` 序数指代解析（第一个/第1个/第 2 个/1./选项二/选一，
  汉字+阿拉伯双形态，整体序数才命中）——宿主已有消歧衔接与标准化反问两处消费者。
  `ResolveAnswer` 澄清回答消解（序数→选项词包含匹配→逃生兜底前缀命中，ok=false=换话题）。
  挂起态存取/反问状态机/注入渲染留宿主（沉自 bianque 输入标准化层 + 消歧 clarify）。

### Migrated（bianque 侧）

- `bianque/internal/agents` TermEntry/TermClarify → `agentkit/clarify.Entry/Options`（别名薄层）
- `bianque/internal/agents` loadTermMap + 词表内在校验 → `agentkit/clarify.LoadVocab/Validate`
- `bianque/internal/engine/scheduler` ordinalIndex/resolveTermAnswer 消解核心 → `agentkit/clarify.OrdinalIndex/ResolveAnswer`

## v0.9.6 (2026-09-14)

bianque 操作审计门整包沉淀 + 词表内核后置否定补件。

### Added

- **policy（新包）**: 操作审计门——四种会话执行策略模式（confirm/auto/plan/full）下的
  统一操作裁决。`Gate.Decide` 三层裁决（例外规则首中即胜 → 模式×风险矩阵 → 未知形态
  兜底 need_human），三值裁决 + `plan_only`/`unknown` 过程值；红线（变异工具拒绝、
  最高危恒人审）矩阵层硬编码任何模式不可绕过。灰区可挂 `Arbiter` 仲裁插件，失败/非法
  输出一律 fail-safe 升人审（「只升不降」恒成立）。`LoadOverrides` 外置策略 yaml
  （路径入参，根键 `operation_policy`）阈值 clamp 只降不升。配套 `WithAuditGate`
  eino 工具装饰器：每次调用先裁决、回调留痕、deny 沿工具结果通道如实降级
  （沉自 bianque engine/policy + runner/audit.go，yaml 路径约定改显式入参）。
- **router**: `KeywordPostNegated` 后置否定守门——关键词命中处紧后方紧跟否定单字
  （不/没/无/非）即视为否定陈述（「负载不高」「磁盘没有问题」）。与 v0.9.4 否定前置
  守门对偶：那边复合短语按窗口回看，这边紧贴单字即判，判定从严（沉自 bianque
  输入标准化层 postNegated）。

### Migrated（bianque 侧）

- `bianque/internal/engine/policy` → `agentkit/policy`（薄转发维持调用点）
- `bianque/internal/engine/runner/audit.go` → `agentkit/policy.WithAuditGate`
- `bianque/internal/engine/scheduler/normalize.go` postNegated → `agentkit/router.KeywordPostNegated`

## v0.9.5 (2026-09-13)

bianque 生产两连沉淀：前缀定价估算 + 通用派发守卫。

### Added

- **llm**: `PriceOf` 按模型名匹配定价表估算单次调用成本（USD）——精确匹配优先，
  未命中回落最长前缀（模型名带版本/日期后缀时定价键可只写主干），未配价返回 0
  不阻塞记账。宿主只需提供 `map[模型名]Price`（沉自 bianque scheduler/usage.go priceOf）。
- **dispatch（新包）**: 通用派发守卫——allow 矩阵 + 深度上限 + 自派发拒绝；
  拓扑边经 `EdgeSource` 接口由宿主注册表提供，守卫构建期物化矩阵、运行期只读；
  被拒派发返回结构化 `*DenyError`（not_allowed / depth_exceeded / self_dispatch）——
  调用方不得转述、不得降级，按失败兜底如实上报
  （沉自 bianque engine/dispatch/guard.go，注册表耦合改接口）。

### Migrated（bianque 侧）

- `internal/scheduler/usage.go` priceOf → `agentkit/llm.PriceOf`
- `internal/engine/dispatch/guard.go` → `agentkit/dispatch`（`EdgeSource` 接口注入拓扑）

## v0.9.4 (2026-09-13)

bianque 生产装配四连沉淀：模型级 failover、副作用感知重试守卫、中文词表匹配内核、脱敏规则增强。

### Added

- **llm**: `FailoverModel` 主备 failover 装饰器——eino `BaseChatModel`/`ToolCallingChatModel`
  双形态；主模型失败且调用方 ctx 存活时切备模型重放同一次请求（Stream 仅首块前可切）。
  切换决策对任何错误恒真（确定性错误在主备异端点/异凭证时能救，误切代价仅一次备模型调用）；
  `OnFailover` 观测回调。与 `Resilient` 互补：Resilient 覆盖自家 Generator 客户端路径，
  FailoverModel 填补 ReAct 主路径（ADK ChatModelAgent 直调裸模型）的降级空白。
- **agentrun**: 副作用感知的重试守卫——`RunWithRetry`/`RunWithEventsAndRetry` 首轮已调用
  变更类（非幂等）工具后**不再整体重跑**（重复副作用风险，如实上抛交调用方降级；
  `Config.RetryAfterMutation=true` 可解除）。**行为变化**：原先无条件重试，守卫默认生效。
  配套导出 `MutatingVerbs`（动词段表，可扩展）+ `IsMutatingTool`（下划线分段精确匹配，
  `use_skill`/`get_running_config` 不误判）+ `SideEffectTracker`（事件回调观测器）。
- **router**: 中文词表匹配内核——`KeywordHit`（子串 + 否定前置守门：紧前方 15 字节窗口内
  出现否定短语即该处作废；「有没有/是不是/要不要」疑问构式先剥离再判）、
  `KeywordHitBoundary`（拉丁词边界：紧邻字符非字母/数字，kill 不误命中 skill/killed）、
  `KeywordHitExcept`/`KeywordHitBoundaryExcept`（排除构式：命中落在 mask 短语内部该处
  作废）。三重守门都只作废该处命中——多处出现任一处通过即命中。`OnNegationHit` 观测钩子。
- **logredact**: 合并 bianque 增强规则——凭证词键值对容忍 `: ` 空格形态
  （`private-key:`/`secret_key:`/`passphrase`/`kubeconfig`，kubeconfig/私钥 YAML 日志常见）
  + PEM 私钥整段打码（载荷防泄漏最后兜底）。

### Migrated（bianque 侧）

- `internal/llm/failover.go` → `agentkit/llm.NewFailoverModel`
- `internal/engine/runner` 变更类工具判定 → `agentrun.IsMutatingTool`
- `internal/agents/routing.go` 词表匹配内核 → `agentkit/router`（`KeywordHit` 四件套）
- `internal/logredact` 副本删除（增强已合入 agentkit）
- `internal/strutil.Truncate` → `textutil.TruncEllipsis`（v0.9.0 迁移表挂账清账）

## v0.9.3 (2026-09-12)

意图维度下沉两件：router 意图槽位 + skill 集成配置依赖声明。

### Added

- **router**: 意图槽位——`Config.Slots` 配置槽位定义（nil = 纯选路，提示词与解析
  原样），分类调用在选路的同时顺带提取附加意图维度（如「是否要方案」），与选路共用
  一次 LLM 调用（零额外延迟/费用）；`Decision.Slots` 携带提取结果。自守恒：未配置
  槽位名一律丢弃、空值丢弃、超长值截断——槽位提取失败不影响选路本身。
- **skill**: SKILL.md frontmatter 新增 `requires_config`——技能级集成配置依赖声明
  （如 `[rag, s3]`）。声明级契约：缺失由调用方（bianque reload）告警，不拦截加载；
  解析/写回保留。

## v0.9.2 (2026-09-11)

从 bianque 完整版契约工作（技能版本化 + MCP 工具面契约 + 装配血缘）沉淀三块平台通用件。

### Added（新包）

- **pack**: 领域包契约面——`ToolManifest`（`_shared/mcp/<server>.yaml` 基线 +
  `<包>/mcp/<server>.yaml` 覆盖）+ `LoadToolManifests`（包清单整文件替换基线；
  包间同名冲突 → 警告 + 包名字典序第一生效；无清单目录合法）。conf 仍是装配
  事实源，清单是契约事实源，对账由调用方做。
- **lineage**: 装配血缘图——expert → skill → MCP server 三类节点的声明式依赖 +
  used_by 反查单源（`Build`，中性输入，不绑定装配实现）；`Diff` 产出 reload 前后
  结构化影响清单（版本/成熟度跃迁、工具面增减、引用边增减；nil 基线=首帧无 diff）；
  `Focus` 焦点邻接子图（depth≤2 无向遍历）；`Hub` 并发读中枢（nil 安全）。

### Added（扩展）

- **skill**: 技能版本化契约补全——
  - `MCPDep` 增加 `Tools`/`MinVersion`；`LibMeta` 增加 `Provides` 能力标签；
    `Deprecated` 增加 `ReplacedBy`。
  - `LibMeta.Validate()`：mode/maturity 枚举、SemVer、弃用窗口（deprecated 必填
    remove_after 日期）、requires_mcp.server 非空。**行为变化**：`LoadFromFS` 现在
    fail-fast 拒绝非法元数据（原先只扫描不校验）——错误带文件路径定位。
  - `RewriteMode`/`RewriteBody`：结构化 frontmatter 写回（改 mode 保留嵌套
    requires_mcp/provides；改正文保留围栏原文）。无 frontmatter/未闭合报错。
  - `VersionInRange`：SemVer 区间求解（Masterminds/semver 约束语法，空格=AND）。
  - `Library.DeprecatedExpiredInUse`：「弃用窗口已过且仍被引用」失败清单。
  - `Decl`：技能结构化声明引用（裸串与 `{name, version, optional}` yaml 双形态），
    供 agent/pack 装配声明复用。

## v0.9.1 (2026-09-10)

- **jsonrepair**: `Schema.Mutate` 更名为 `Schema.OnMap`，并在**子节点归一完成后**对每个
  map 调用一次（原先按 key 在子节点之前触发，领域钩子看不到已规范化的嵌套结构）。
  **API 变化**：依赖 `Mutate(key, m)` 的调用方请改为 `OnMap(m)`。

## v0.9.0 (2026-09-10)

从 bianque（智能运维多智能体平台）抽取通用组件，补齐平台级通用件缺口。

### Added（新包）

- **logredact**: 日志/审计凭据脱敏——`Redact`（URL 内嵌账号口令 / token= / Bearer 模式化打码）+
  `RedactValue`（递归脱敏 JSON 形态值）。零依赖，与 safejson（反注入）正交。
- **worker/pglease**: `worker.LeaseStore` 的 PostgreSQL 实现——原子 UPSERT 获取/续约、
  仅持有者释放、`Migrate` 建表；表名可配（`WithTable`，缺省 `agentkit_lease`）。
- **websearch**: 公开资料检索抽象——`Service` 接口 + SearXNG 客户端（`Language` 可配，
  缺省 zh-CN）+ `AsTool()` 包成 `web_search` eino 工具。与 knowledge/rag 互补。
- **hotplug**: 运行时插拔与热替换——`Plugboard`（启停视图，nil=全启用）+
  `Holder[T]`（泛型原子快照持有点，读无锁换整体）。
- **jsonrepair**: LLM 宽容 JSON 修复骨架——`StripFence` / `ExtractObject` / `Repair`
  （全角结构符、非法转义、尾逗号、未闭合括号）/ `Normalize`（Schema 可配 StringKeys/ListKeys）
  / `ParseLenient`。领域 schema 留给调用方。

### Added（扩展）

- **skill**: 多根 `Library`——`LoadFromFS` 扫描 `_shared/skills` + `<包>/skills`；
  `LibMeta` 扩展 mode/maturity/version/requires_mcp/deprecated；实现 Provider/Lister/
  AliasResolver（热替换无缓存）。`ParseRichFrontmatter`（yaml.v3）。
- **llm**: `UsageHandler`——eino callbacks 完整用量采集（Cached/Reasoning tokens、
  FinishReason、Duration、Iteration）；归因经 `Labels map[string]string` 泛化
  （`WithUsageLabels` / `WithCallCounter`）。
- **mcp**: `UnwrapMCPText` 解 MCP 工具信封取内层文本。
- **textutil**: `TruncEllipsis` 截断并追加省略号。

## v0.8.4 (2026-09-10)

- **agentrun**: `Event` 观测面补全——新增 `EventReasoning` 事件类型（推理型模型
  assistant 消息的 `reasoning_content` 以 reasoning 事件先行外发，先于同消息的
  tool_call/text）；`Event` 新增 `Args` 字段，`tool_call` 事件携带原始 JSON 参数串
  （观测/进度展示可看到调用命令）。新增回归测试锁死事件序列
  （reasoning → tool_call(带 Args) → tool_result → text）。

## v0.8.3 (2026-09-08)

- **router**: `Classify` 门槛自守——MinConfidence 置信度下限与分类合法性
  （结果不在路由表，含 "none"）在 Classify 统一校验，未过门槛返回携带原因的
  错误（Decision 仍返回供可观测）。此前门槛只在 `Do` 分发路径生效，只取
  Classify 决策自行分发的编排器配置了 MinConfidence 也从不生效（死配置）。
  **行为变化**：依赖 Classify 无条件放行的调用方升级后低置信场景将收到错误。
- **knowledge/rag**: 可靠性修复——`Local.Rescan` 记录 WalkDir 读取错误：根目录
  stat 通过但不可 readdir 时不再静默换入空索引（全部读失败保留旧索引，部分失败
  告警后照常换入）；`OpenAIEmbedder.Embed` 防御远端响应负数 index（原会 panic），
  缺失槽位由维度校验兜底报错；`MilvusStore.Index` 改为 Delete+Upsert——文件变短
  后不再残留过期 chunk 行，Index 真正幂等（与 Local.Rescan 全量重建同语义，
  Milvus 集成测试验证）。
- **llm**: 可靠性修复——`StageRouter.UsedTokens` 按 `BudgetHolder`（新可选接口，
  Client/Resilient 实现）暴露的预算指针身份去重：各链共享同一任务 Budget 时
  原实现按链数倍增上报（argus runner 生产装配已踩中）；`ClassifyLLMError` 截断
  marker 提到数字状态码 marker 之前——Client 自产截断错误文本含
  `completion_tokens=<n>`，n 恰为 401/404/429 等值时原会被误判为鉴权失败/限速，
  "截断→提升 MaxOutputTokens 重试"机制确定性失效。
- **worker**: 可靠性修复——`LeaderElector` 停机竞态：让位职责移入竞选 goroutine
  （退出前若持有则 Release），Stop 与在途 tick 穿插时不再泄漏租约/onGained 不再
  在 Stop 后触发/Stop 返回后 IsLeader 必为 false，ctx 取消导致的续约失败不再误报
  onLost；`Pool` 的 Queue 调用（Claim/心跳/过期重置）补 panic 隔离——调用方 Queue
  实现 panic 不再杀死 worker（池静默减员）或心跳 goroutine（在途任务被对端复位
  双跑）；`Pool.Stop` 心跳改为排空完成后才停——原实现在停机第一时刻就停心跳，
  grace 窗口超过 StaleRunningAfter 剩余预算时在途任务会被对端复位双跑。
- **acpx**: `childEnv` 修复 KEY=VALUE 字面透传契约——原实现只按名透传父进程值，
  字面值（父进程无同名时）被静默丢弃、（有同名时）被父进程值覆盖；现字面注入
  优先且每 key 唯一（与 mcp.whitelistEnv 同语义）。
- **acpx**: 适配器文件重组——`agents.go`/`agents2.go` 按 agent 家族拆为
  `claude.go`/`codex.go`/`opencode.go`/`generic.go`/`kimi.go`/`gemini.go`/`mimo.go`，
  测试文件同步按类型拆分（跨家命名测试归 `registry_test.go`）。纯文件移动，
  无 API 变化；此后新增 agent = 新增一个文件 + registry 一行注册。
- **knowledge/rag**: Local 检索与索引性能优化（等价变换，公共 API 与打分语义不变）——
  IDF 在 rescan 时预计算（检索路径零 `math.Log`）；topK 改固定容量小顶堆选择
  （替代全量收集 + 全排序）；tokenize 改字节偏移迭代 + CJK bigram 原串切片 +
  ASCII 词写入即小写（消除 `[]rune` 全量拷贝）；rune 计数改 `utf8.RuneCountInString`；
  非过期路径检索加锁次数 2→1。基准（M5，800/8000 chunks）：检索 -24%/-70%，
  检索内存 -99.9%（2.15MB→944B/op，35→10 allocs），rescan -61%（allocs -79%）。
  新增 `TestTokenize` 锁死分词语义（bigram/单字补齐/非 Han 边界），
  `progress` 补 Publish 基准留档（subs=1 时 27ns/op，无需优化）。

## v0.8.1 (2026-09-07)

- **llm**: `StageRouter` — per-stage Generator multiplexing implementing `Generator`
  (exact match first, then longest prefix — registering "R1" covers "R1a";
  unmatched stages fall to the default chain). Enables per-stage model routing
  (big-window model for long inputs, fast model for judgments, strong model for
  quality-critical stages) without touching call sites. `UsedTokens` aggregates
  all registered chains; budget injection stays the caller's responsibility.
- **docs**: `docs/FRAMEWORK.md` — complete framework documentation (positioning &
  design principles, 6-layer architecture, all 19 packages with APIs/examples/
  contracts, seven-architecture matrix, horizontal capability deep-dives
  (reliability/cost/multi-replica/security), Argus production reference, release
  discipline & pitfalls checklist).

## v0.8.0 (2026-09-07)

Seven-architecture coverage sweep（Single Agent / ReAct / Plan-and-Execute /
Reflection / Router+Skill / Blackboard / Graph Workflow）——support matrix and
migration guide in `docs/patterns.md`.

### Added

- **agentrun**: `PlanAndExecute` — thin, batteries-included wrapper over eino adk
  prebuilt planexecute (Planner/Executor/Replanner composition, tool-calling plan
  schema, optional planner instruction via input shaping)
- **reflection**: new package — Generate→Critique(structured pass/issues)→Revise
  convergence loop with per-round audit trail; self-contradictory critiques
  (pass=true with issues) treated as fail
- **router**: new package — LLM intent classification → route dispatch with
  confidence threshold and optional fallback; decision (route/confidence/reason)
  fully observable
- **blackboard**: new package — thread-safe shared board (ordered entries +
  since-cursor incremental reads) and `Convene` specialist rotation until
  consensus or round cap; complements eino adk supervisor (centered assignment)

### Notes

- Graph Workflow / Supervisor / Sequential-Parallel-Loop remain eino-native by
  design (`compose.Graph` + adk workflow) — agentkit packages serve as node
  building blocks; see docs/patterns.md for the composition guidance.

## v0.7.3 (2026-09-07)

- **worker**: leader-election primitive for multi-replica deployments — `LeaseStore` interface (one conditional UPSERT to implement over SQL) + `LeaderElector` (ttl/interval-based campaign, `IsLeader()`, onGained/onLost callbacks, graceful yield on Stop). Complements the DB-as-queue sharding: task plane scales via `ClaimNextPending`, control plane ("only one may run" components like outbound pollers) converges via lease.

## v0.7.2 (2026-09-07)

- **obsx**: `Options.OnUsage func(component, model, stage string, prompt, completion int)` — real token-usage callback fired on every traced model call. Covers the RawModel bypass (ReAct agents driving `BaseChatModel` directly) that `llm.Client.OnUsage` cannot see; stage comes from the ctx marker. Consumer (Argus) uses it for per-task cost accounting.

## v0.7.1 (2026-09-07)

Exported building blocks requested by consumers (Argus adapter_cli dedup):

- **acpx**: `RunProcess(ProcessRequest)` — the process-group discipline (Setpgid → TERM group → grace → SIGKILL), env whitelist, stdout cap (configurable `MaxStdout`) and `ErrTimeout`/`ErrCanceled` sentinels, for callers that wrap external CLIs without the Agent abstraction
- **textutil**: `SplitRunes(s, n)` — even rune-safe chunking for large-text LLM pipelines

## v0.7.0 (2026-09-06)

Full-library audit fixes: 3 Critical + ~20 Important across 16 packages, each with regression tests.

### Security

- **mcp**: stdio subprocess env is now a real whitelist (PATH/HOME/TMPDIR base set + `Env` entries) instead of inheriting the full parent environment; `Env` entries are variable names looked up from the current process (or literal `KEY=VALUE`). Previously all parent secrets leaked to external MCP servers, contradicting the documented security model
- **acpx**: prompt is guarded before being passed as positional/flag argument — prompts starting with `-` get a newline prefix so CLI flag parsers cannot consume them as flags (prompt injection could bypass sandbox flags when the LLM fills the prompt)
- **workcopy**: `prepare` error path scrubbed the wrong argument — git failure details were dropped and token redaction never applied; error now keeps git output with token redacted

### Reliability

- **acpx**: single-line stdout flood no longer bypasses the 8MB cap (line buffer capped at 1MB, oversized lines dropped and resynced at next newline); timeout/cancel now uses `cmd.Cancel` (TERM group) + `WaitDelay` (SIGKILL) so the documented grace period actually works, and caller cancellation is no longer misreported as timeout (`ErrTimeout`/`ErrCanceled` sentinels, errors wrapped with `%w`); partial trailing line is flushed on failure paths too
- **llm**: `finish_reason=length` no longer panics when `Usage` is missing; deterministic 4xx (400/402/404/405/413/422 + auth/invalid-model markers) classified non-retryable via structured status code first, then digit-boundary text matching ("429" no longer matches "1429ms"); `AttemptTimeout` now bounds each attempt instead of the whole retry cycle
- **llm/breaker**: half-open probe can no longer wedge a model out of the fallback chain — breaker adds a probe deadline (`DefaultProbeTimeout`) and ignores failures recorded for rejected requests during open (cooldown no longer extended indefinitely); resilient pairs `Failure` on ctx-cancel and nil-model paths after `Allow`
- **breaker**: probe-stale recovery + no cooldown extension, regression-tested
- **worker**: `Stop` before `Start` no longer burns the shutdown path (mutex-based lifecycle, re-`Start` guarded); task panic only loses that task (worker survives, stale-reset requeues it); heartbeat joins the stop WaitGroup with per-call timeouts
- **progress**: manual `cancel()` now wakes the ctx listener goroutine (Background-ctx subscribers no longer leak)
- **workcopy**: `Sweep` no longer deletes in-use worktrees (rc>0 kept regardless of TTL — configure TTL above the longest task; crash leaks still handled by orphan sweep); singleflight race between shared result and `Release` fixed (registration moved inside the flight, bounded retry)
- **knowledge/rag**: `MilvusStore.Index` upserts by deterministic primary key — repeated restarts no longer accumulate duplicate rows; existing collections are validated (dim/metric) and loaded
- **skill**: progressive disclosure closes the frontmatter-name gap — `Meta.Name` is the canonical ref name (use_skill/allowed basis), frontmatter name becomes display alias `Meta.Title`; `AsSkillTool` normalizes alias inputs via the new `AliasResolver` capability

### API

- **toolprior**: `WithCallLimit` over-limit now returns a model-visible `LIMIT_REACHED: ...` text (nil error) instead of a Go error — eino ToolsNode propagates tool errors as whole-run aborts, discarding all partial progress; `Ordered` sorts Info-failed entries last as documented; call counter widened to int64; `Table.Add` panics on nil Tool (fail fast at registration)
- **agentrun**: `Config.ToolsFactory` builds a fresh tool table per attempt — use with `RunWithRetry` so `WithCallLimit` budgets reset instead of carrying over; new `RunWithEventsAndRetry` makes retries observable; `tool_result` events added; empty final reply is distinguished from "no final reply"
- **acpx/mcp/skill/rag**: eino tool wrappers pass through the framework context instead of `context.Background()` (cancellation/timeout now reaches subprocesses and Milvus calls)

### Polish (minor)

- **acpx**: Codex adopts the result-priority strategy on failure paths and accumulates per-turn usage (consistency with claude/mimo); test fixtures cleaned up (`t.Setenv`, malformed JSON fixture)
- **llm**: `Client.Generate/GenerateJSON` no longer mutate the caller's message slice; `Attempt.Duration` actually recorded; `OnFallback` reports the actual next model; all-breakers-open returns a distinct error instead of "全部 0 个模型失败"; backoff jitter guard for sub-nanosecond `BaseDelay`; dead truncation-retry branch removed from `GenerateJSON`; `OpenAIProviderConfig.MaxOutputTokens < 0` omits the `max_tokens` param (inference-model compat); `CostTracker` capped at 10k records; unused credential fields dropped from `OpenAIProvider`
- **knowledge/rag**: `sqrtF` uses `math.Sqrt` (hand-rolled Newton iterations drifted up to 60%); `Rescan` keeps the old index when the root dir is missing (no silent empty knowledge base); chunk hard cap (4000 runes) guards Milvus VarChar limit on pasted base64/logs/unclosed code fences; `NProbe <= 0` falls back to default; search-param errors no longer swallowed; filter keys whitelisted (file/heading) against expression injection; `Local.Retrieve` honors ctx; default-value logic extracted to a pure function shared with tests
- **workcopy**: `cloneURL` preserves the original scheme (no forced https upgrade for intranet http); `GIT_TERMINAL_PROMPT=0` set on all git calls
- **mcp**: empty Allow-hit results reported via `OnError` (typo'd tool names no longer silently invisible); `NewPool` ignores empty names and keeps the first duplicate
- **skill**: `..` rejected only as a path segment (names like `v1..2` allowed); frontmatter quotes stripped only when paired; symlink boundary and cache semantics documented; prompt-injection caveat documented for `ListPrompt`
- **obsx**: missing OnStart state no longer produces astronomic durations (1.7 万年) or spurious slow-call warnings; package doc example signature fixed
- **safejson**: horizontal rules (`***`/`___`), table rows, reference definitions and tab-ordered lists now neutralized; emphasis text (`***bold***`) not falsely flagged
- **severity**: trailing `/**` no longer matches the directory itself (.gitignore semantics); `?` matches one rune (multibyte filenames)
- **textutil**: negative length no longer panics; no full `[]rune` allocation when no truncation is needed
- **audit**: nil-logger/-receiver safe; test name now matches behavior
- **progress**: dropped-event counter (`Bus.Dropped()`)
- **README**: package table now covers all 16 packages; acpx protocol/sandbox support matrix corrected (9 adapters); Resilient / toolprior / mcp / agentrun / obsx quick-start sections added; glob example fixed

## v0.1.0 (2026-09-01)

Initial release — extracted from Argus v3.0.5 code review platform.

### Packages

- **llm**: LLM client with retry (exponential backoff + jitter), rate limiting (token bucket), budget tracking (TokenAccountant interface), fitInput context window guard, GenerateJSON with truncation retry
- **breaker**: Circuit breaker (closed → open → half-open → closed), per-key Breakers registry
- **worker**: DB-as-queue worker pool with TaskQueue interface, heartbeat-based crash recovery, two-phase graceful shutdown
- **knowledge/rag**: Local RAG (markdown chunking + ASCII/CJK tokenization + TF-IDF scoring), eino tool adapter
- **progress**: Generic event bus `Bus[T]` with multi-subscriber broadcast, 64-buffered channels, lossy drop
- **skill**: File-based content/methodology resolver with path traversal protection, in-process cache, checksum
- **severity**: Severity normalization, SHA256 fingerprinting, glob matching (`**`, `*`, `?`)
- **safejson**: Markdown/HTML anti-injection (headings, fences, quotes, lists, HTML comments)
- **audit**: Structured audit logging
- **textutil**: Rune-safe text truncation
- **workcopy**: Git working copy sandbox with WorktreeKey, singleflight dedup, TTL sweep, orphan cleanup

### Dependencies

- ekit v0.20.1
- eino v0.9.18
- golang.org/x/sync v0.22.0
- golang.org/x/time v0.15.0
