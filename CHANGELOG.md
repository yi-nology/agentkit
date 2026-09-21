# Changelog

## v0.10.16 (2026-09-21)

### Added

- **llm**：Retry-After 捕获与消费（对标 ZCode runner-retry 的「服务端退避建议优先于
  本地曲线」）三件套——
  - `retryAfterTransport`（`OpenAIProviderConfig.CaptureRetryAfter=true` 启用）：
    429 响应的 Retry-After（秒数/HTTP-date）经 ctx sink 透明捕获，响应体零改写；
  - `WithRetryAfterSink`/`RetryAfterFrom`：重试环上游注入、退避计算处读取的公开对；
  - `SelectRetryDelay`：合理性钳制取舍——建议 >0 且（≤5min 或短于本地曲线值）才
    采信，长限流交调用方/failover 处置（ZCode 同款规则）。
  `Client.generateRetry` 已接入（安装 sink + 退避取舍）；消费方：
  `llm.OpenAIProviderConfig.CaptureRetryAfter=true` 一行启用。
- **agentrun**：`RunWithEventsAndRetry` 重跑分支前置 Retry-After 等待——429 时端点
  知道限流窗口还剩多久，对仍在窗口内的端点立即重跑只会再吃一个 429。等待钳制
  ≤5min、ctx 取消即止（steer/停机零延迟穿透）；超限不等待交调用方处置。sink 在
  本函数顶部安装，ReAct 主路径（经 OpenAIProvider 传输层）即被覆盖。

### Tests

- `parseRetryAfter`（秒数/HTTP-date/非法值）、`SelectRetryDelay`（五分支钳制表）、
  transport 捕获（429+头 → sink；无 sink 不炸）；
- 端到端：真 HTTP 端点首响 429+`Retry-After: 1` → 次响 200——generateRetry 全链
  等待 ≥1s 且最终成功（1.01s 实测）。

## v0.10.15 (2026-09-21)

### Added

- **mcp**：`WrapErrorAsObservation` + `Pool.Tools()` 出口统一启用——eino-ext 把
  MCP isError:true 的工具结果当调用 error 上抛，ReAct 直接以 NodeRunError 炸掉
  整个 agent 步骤，LLM 没机会看到错误并修正参数（实弹：k8sgpt get-resource 猜
  错 pod 名 → 专家整步 degraded）。现剥出错误文本降级为观察回传 LLM（是否修正
  参数/换路径由 LLM 自行决定）；传输层等其他错误原样上抛。
- **httpx**：`StatusError`（非 2xx 类型化，`errors.As` 按状态码分类；Error()
  文案与历史一致）+ `RetryConfig`/`DoJSONWithRetry`（指数退避+抖动，默认网络
  超时/5xx/429 可重试，`reqFn` 每轮重建请求体）——重试骨架此前锁在
  llm.generateRetry（私有、LLM 专用），泛 HTTP 消费方（langfuse/rag/websearch
  及外部）各自手写；review-service 收敛评估反哺。
- **logredact**：补平台 API token 裸形态模式（sk-/sk-ant-/ghp_/gho_/github_pat_/
  xoxb-/AKIA 及三段式 JWT）——键值对/连接串形态已有规则，裸 token 散文形态
  此前会泄漏；附防误伤用例（普通文本/路径不改写）。

## v0.10.14 (2026-09-20)

### Changed

- **lineage**【破坏性】：`Impact` 的 refs_changed 分支不再向
  `ToolsAdded/ToolsRemoved` 双写技能名（v0.10.7 引入 `SkillsAdded/
  SkillsRemoved` 时保留的"兼容旧消费方"填充——按仓库无兼容层纪律删除，
  该字段此前对 refs_changed 语义撒谎）。`Tools*` 字段现仅 tools_changed
  （MCP 工具面）填充，refs_changed 只读 `Skills*`。
- **废弃代码全仓清理结论**：孤儿符号扫描（导出 func/type/const/方法 ×
  仓内引用 × 测试 × 文档承诺面四重判据）确认除上述双写填充外无废弃残留
  ——`go.mod` 零未用依赖（tidy 无 diff）、无 Deprecated 标记、scripts 全部
  活跃；三个近似候选（`llm.CostTracker.Records`/`Resilient.PrimaryModel`/
  `skill.Decl.UnmarshalYAML`）分别属观测配套面、v0.10.9 承诺的迁移目标、
  yaml 接口回调，均保留。

## v0.10.13 (2026-09-20)

### Fixed

- **agentrun**【行为变化：修复】：`PlanAndExecute` 自 v0.8.0 起漏传 eino
  `planexecute.Config` 必填的 `Replanner`（无默认构造）——该导出 API 实际
  不可用，零测试掩盖六个版本。现 agentrun 兜底装配：`PlanExecuteConfig`
  新增可选 `Replanner` 字段（空 = 复用 Planner 模型；生产可配更便宜的模型
  跑完成判定）。

### Changed

- **agentrun**【行为变化】：最终答复只认 Replanner 的 respond 信封
  （`{"response":...}`，解包为纯文本）——此前 executor 步骤输出（同为无
  tool_calls 的 assistant 文本）会被当最终答复，MaxSteps 耗尽时尤甚；现
  耗尽场景如实报错"未产出最终答复"。
- **llm/llmtest**：桩支持 ToolCalls 脚本与 Stream 按脚本响应（eino adk
  planexecute 的 plan/respond tool-calling 协议可桩化）；新增 `ToolCall`
  便捷构造。
- **测试盲区收编**：worker guardedCall panic 隔离/错误透传（队列 panic 若
  穿透会杀死心跳 goroutine → 静默双跑，此前 0% 覆盖）；websearch AsTool
  错误契约（执行失败=模型可读文本）与正常路径；mcp dial 配置错误分支
  （无 command 无 url）。
- **门禁**：`make check` 纳入 race（`go test -race ./...` 全绿后收编）。
- **文档**：符号核对修漂移——README/FRAMEWORK 的 `acpx.ErrTimeout` →
  `procx.ErrTimeout`（哨兵 v0.10.11 随迁后遗留）、架构矩阵 `router.Do` →
  `router.Run`、P&E 示例补 Replanner 字段。

## v0.10.12 (2026-09-20)

### Changed

- **obsx/llm**【行为变化·破坏性】：token 记账单源化——`obsx.Options.OnUsage`
  退役（五数字回调删除），callbacks 侧记账统一出口 `llm.NewUsageHandler`
  （完整版 UsageRecord：Cached/Reasoning/FinishReason/Duration/Iteration/Labels）。
  防重护栏（Client 记账标记）自 obsx 移入记账侧：`NewUsageHandler` 检测
  `Client.OnUsage/Budget` 已配置时自动跳过，obsx 回归纯 trace（TracingHandler
  不再承担记账职责，跨包协议 `WithClientAccounting/ClientAccounted` 删除）。
  ReAct/RawModel 旁路的成本记账迁移：`callbacks.InitCallbacks(ctx, nil,
  llm.NewUsageHandler(sink))`。
- **新测试桩包 `llm/llmtest`**：llm（resilient/client_path/failover）与
  reflection/router 五份手写模型桩收敛为脚本化 `Model`/`ToolModel`/`Provider`
  ——脚本耗尽语义显式化（`RepeatLast` 重复末条 vs 默认报错），输入记录
  （FirstInput/LastInput/Inputs）、ResponseMeta/Usage 可编程、并发安全；
  含桩自测。
- **命名消歧（破坏性）**：`toolprior.WithCallLimit`→`LimitCalls`、
  `policy.WithAuditGate`→`AuditGate`——工具装饰器不再占用 `With*` 前缀
  （该前缀保留给 option applier 与 ctx setter，返回类型可由名字推断）。
- **文档**：patterns/README 的 API 漂移修正（`res.Answer`/`res.Output`→`Text`、
  `r.Do`→`r.Run`）。

## v0.10.10 (2026-09-19)

### Changed

- **acpx/mimo**【行为变化】：错误详情路径修正——mimo 实际发 `error.data.message`，
  此前只读顶层 `error.message` 导致错误被静默吞掉；叠加 mimo 错误事件 exit=0，
  纯错误跑以 stdout 全文兜底成"成功"返回（huginn probe 误判通过、排障被带偏）。
  现 error 事件**如实上抛**（不因恰好有部分文本而掩盖），并修正解析路径
  （data.message 优先、顶层兼容回退）。
- **acpx**：error/step 类事件全量经 `OnEvent` 转发——此前 emitText 是多数适配器
  唯一触发点，纯错误跑 transcript 0 字节、102 类故障排障断链。覆盖：mimo
  error/result(step_finish)；codex error（exit=0 纯错误跑现报错）；claude
  result.is_error 以 EventError 型转发（成功/失败判定不动——result 优先策略
  为既有契约）。
- **acpx/gemini**【行为变化】：error 信封此前解析但从未检查——现如实报错
  （与 mimo 同纪律）。
- **acpx/mimo**：缺省模型配置化——`Mimo.DefaultModel` 字段（请求未指定 Model 时
  的 `-m` 缺省）。`-m` 须 `xiaomi/` 全名前缀（短名被服务端拒绝）；本机 CLI 缺省
  模型可能被服务端下线（ultraspeed 前车之鉴），生产装配建议
  `reg.Register(&acpx.Mimo{Bin: "mimo", DefaultModel: "xiaomi/..."})` 显式配置。
- **探活指导**：probe/健康判定以 `Run` 返回的 `err != nil` 为失败门槛——上述
  exit=0 错误形态已如实报错；勿用"有输出即通过"判定。

## v0.10.11 (2026-09-20)

### Changed

- **包结构调整（破坏性，无兼容层——本仓库不承诺跨版本兼容）**：
  - 新包 `procx`：acpx 的进程执行纪律（进程组执行/超时整组终止/stdout 与单行
    双限容/环境白名单 ChildEnv）迁出为全仓单源。此前 acpx 同时扮演「CLI agent
    适配层」与「全仓进程托管设施」两角，workcopy/mcp 为两个函数拖入整个
    agent 适配域 + eino 依赖。`acpx.RunProcess/ProcessRequest/ChildEnv` →
    `procx.Run/RunRequest/ChildEnv`（sentinel 随迁：`procx.ErrTimeout/
    ErrCanceled`）。
  - 新包 `reportutil`：severity/sampling/stats 三包合并——同一条评审/评测报告
    消费链（归一严重度 → 聚簇去重 → 置信区间/检验）、消费者画像相同、边界已
    漂移过一轮（v0.10.9 severity→sampling 指纹迁移）。API 原样随迁。
  - 新包 `httpx`：HTTP+JSON 调用纪律单源（ctx 感知构造 → 执行 → 限容读体 →
    状态码检查 → JSON 解码；错误体 rune 安全摘要）。langfuse/rag.OpenAIEmbedder/
    websearch.Searxng 三份手写样板收敛，**顺带修掉两个真缺陷**：embedder 吞
    `io.ReadAll` 错误、错误体按字节截断腰斩 UTF-8。
  - `llmjson` 包删除：`Unmarshal` 并入 `jsonrepair.Unmarshal`（宽松解析与语法
    修复本就是一条链）；`llm.ExtractJSON` 迁入 `jsonrepair`（解析原语不再依赖
    LLM 客户端栈——llmjson→llm 方向倒挂消除）。
- **obsx**【行为变化】：新增 `TokenUsageOf`——eino 回调输出的真实用量提取单源
  （llm/usage 与 obsx/tracing 共用）。修掉 compose 场景 obsx 侧漏采：图节点
  对裸 ChatModel 只透传 Message 时，obsx 原缺 ResponseMeta.Usage 回退，usage
  静默丢失（llm 侧自 v0.10.9 起有该回退，两份实现已漂移）。
- **skill**【破坏性】：`AliasResolver.CanonicalName` 签名加 ctx——此前
  `scanAliases` 内部用 `context.Background()` 调 ListSkills，ctx 在别名归一化
  链上完全断开（FileProvider.Resolve 同步补 ctx.Err() 检查）。
- **词表统一（破坏性）**：`router.Do`→`Run`（全仓主执行方法统一 Run 词表）；
  `reflection.RefineResult.Output`→`Text`、`agentrun.PlanExecuteResult.Answer`→
  `Text`（产出字段统一 Text=模型/agent 最终文本，与 acpx.RunResult.Text 对齐）。
- **llm**（破坏性）：`NewFailoverModel`/`NewChainFailoverModel` 平行切片构造器
  收敛为链节式 `NewFailoverModel(...ChainLink)`——names[i] 对不上 models[i] 是
  装配期静默事故，结构化链节在编译期消错位。
- **lineage**：`Hub` 改用 `hotplug.Holder` 持快照（读无锁整体原子换，消「快照
  热替换」概念的第二份实现）。
- **textutil**：`StripFence`→`StripCodeFence`（与 fence 包「数据区围栏」消歧）；
  新增 `TruncNote`（rune 截断 + 中文注记留痕单源——llm fitInput/rag 超长行/
  rag 工具摘要四处收敛）。
- **杂项收敛**：acpx tokenPair（claude/codex 同形 usage 结构）；llm 包三份
  package doc 合一（client.go 为唯一章程）；AttemptTimeout 双接线互链注释；
  全仓错误前缀补齐约 30 处（skill/clarify/policy/worker/mcp/langfuse/rag——
  同包混用两种风格最伤检索）；测试手写 contains/min/abs 助手清理、
  skill.cut→strings.Cut、policy.contains→slices.Contains；jsonrepair
  Flatten*/CoerceString 收为非导出（零外部消费者）。
- **文档**：README/FRAMEWORK 同步新包面与失效 API 示例修正（severity.Fingerprint
  等指向 v0.10.9 已迁 API）。

## v0.10.9 (2026-09-19)

### Changed

- **包结构调整（破坏性）**：`safejson` 包删除——`EscapeUntrusted` 归入 fence
  （包名与内容不符，且与数据区围栏同属注入卫生）；`severity` 职责收敛——
  `Fingerprint`/`NormalizeComment` 迁 sampling（聚簇签名的自然归属）、
  `GlobMatch` 迁 textutil，severity 只留级别归一。
- **llm**：Client.Generate 与 Resilient.tryOneProvider 的重试核心收敛为单实现
  `generateRetry`（截断错误文案已漂移——Resilient 路径现携带 completion_tokens）；
  `FailoverModel` 由主备二元泛化为 N 模型链（`NewChainFailoverModel`），全败上抛
  末模型错误；`Resilient.RawModel()` 改为返回包住整条链的 failover 装饰器
  （`RawModelWithFailover`）——ReAct 裸模型路径不再静默丢弃降级链【行为变化】；
  `NewStageRouter` 对 nil def 构造期 panic（fail-fast）。
- **llm/obsx**：token 记账防重护栏——Client 配置 OnUsage/Budget 时打 ctx 标记，
  TracingHandler 检测到标记跳过 callbacks 侧 OnUsage，同一物理调用不双倍记账
  （此前两侧同时配置会双倍计）。
- **acpx/mcp**：环境白名单单源化——`acpx.ChildEnv` 导出为全仓库子进程环境纪律
  （基础集取并集，含 LOGNAME/SHELL），mcp 删除漂移实现 whitelistEnv；MCP stdio
  server 环境新增 GIT_TERMINAL_PROMPT=0/CI=1 与代理/CA 白名单项。
- **rag/websearch**：内置工具错误契约统一——执行失败返回模型可读文本（agent 可
  自行降级，不中止整个运行），构造失败 panic（编程错误 fail-fast；rag 原吞错
  返回 nil tool）。
- **router**：`KeywordPostNegated` 改多处扫描——任一处紧随否定单字即作废
  （与其余守门「任一处」语义对齐）【行为变化】。
- **acpx**：GenericAgent 的 flag+占位符为条件单元——{model}/{session} 缺值时
  连带移除紧邻 flag，不再产生吞掉 prompt 的悬空 flag【行为变化】；ClaudeCode/
  Gemini 注册身份由构造器填充 name 字段（Bin 推导兜底）。
- **breaker**：`Breakers.Opened` 改只读（未登记 key 返回 false 不创建条目——
  监控轮询不再撑大内部 map）；独立 Breaker 新增 `WithProbeTimeout`。
- **blackboard**：Convene 构建期对专家名查重/非空 fail-fast（观察游标按名键控）。
- **杂项**：jsonrepair walkNormalize 死代码清理；langfuse truncateStr 修 UTF-8
  腰斩（经 textutil.TruncEllipsis 单源）；obsx preview/conversation truncateLine
  截断单源化；hotplug.Disabled() 字典序稳定输出；rag sqrtF 陈旧注释修正。

### Added

- **textutil**：`StripFence`（markdown 围栏剥离单一事实源——jsonrepair 与
  llm.ExtractJSON 共用，多围栏块取第一块）；`GlobMatch`（自 severity 迁入）。
- **sampling**：`Fingerprint`/`NormalizeComment`（自 severity 迁入）；Aggregate
  签名函数每 item 只调用一次（防非纯签名注册/匹配不一致）。
- **fence**：`EscapeUntrusted`（自 safejson 迁入）；skill.ListPrompt 出口默认对
  Description 消毒（安全约定从文档落到代码）。
- **llm**：`NewChainFailoverModel`（N 模型链）、`Resilient.RawModelWithFailover`。
- **mcp**：`UnwrapMCPText` 补 structuredContent 信封支持（content[].text 优先）。

## v0.10.8 (2026-09-19)

### Changed

- **acpx**【行为变化】：`Agent` 接口新增 `Capabilities() Capability`（Model/Session/
  MaxTurns/AllowedTools/Sandbox 五字段支持声明，编译期强制，实现者无法"忘记声明"）；
  `Registry.Run` 对请求中声明不支持的非零字段 **fail-fast 报错**，不再静默丢弃——
  此前给 kimi/mimo/opencode/generic 传 `Sandbox=readonly` 实则全自主裸跑（安全语义
  静默降级）。`Registry.Run` 新增能力校验错误路径；直连 `Agent.Run` 的调用方应自行
  经 `Capabilities` 判断。GenericAgent 能力由 argv 模板占位符推导。
- **skill**【行为变化】：Checksum/Version/Content 口径两 Provider 统一——
  `Skill.Checksum` canonical 定义为 **sha256(正文) 前 16 位**（FileProvider 原对
  全文含 frontmatter 计算，现与 Library 一致——绑定实际注入提示词的内容，观测守卫
  比对基准不再随 Provider 漂移）；`Skill.Version` 取 frontmatter 声明（FileProvider
  原回显请求的 ref.Version；ref.Version 回归请求约束/缓存键本职）；FileProvider 的
  Content 剥壳后去首尾空白（与 Library 同一口径，同文件两 Provider 逐字节一致）。

### Added

- **logredact**：`RedactSecrets(s, secrets...)`——抹除调用方已知的确切秘密
  （clone URL 内嵌 token、动态签发临时凭据），长秘密优先替换防前缀截断泄漏；
  与模式化 `Redact`（未知形态）互补、与 `Masker`（低敏感可回填）相反。
- **workcopy**：脱敏机制切换至 `logredact.RedactSecrets`（单一事实源），
  包内 `scrub` 私有实现删除。

## v0.10.7 (2026-09-19)

### Changed

- **全面重构（高内聚低耦合）**：纯内部重构 + 纯增量新增，导出 API 签名全部不变，
  `make check`（build/vet/golangci-lint/test/govulncheck）全绿。要点：
  - **llm**：`generateWithTrace`/`generateJSONWithTrace` 双份降级链循环收敛为
    `walkChain` 骨架；Client/Resilient 双份退避统一为包级 `backoffDelay`；
    `ExtractJSON` 迁出 errors.go（extract.go）。
  - **acpx**：7 个适配器逐字重复的 validate→childEnv→timeout→execCLI 骨架下沉
    `runCLI`（run.go），另下沉 `fallbackResult`/`parseJSONOut`/`emitText`；JSON
    容错提取改用 jsonrepair 括号配平（删 util.go 第三份弱化实现）；默认超时提为
    `defaultTimeout` 常量；process.go 拆 env.go/linewriter.go，registry.go 拆
    tool.go/observe.go。
  - **agentrun**：`RunWithRetry` 委托 `RunWithEventsAndRetry`；run 与
    PlanAndExecute 的事件流 drain 骨架统一为 `drainEvents`（消出口判定分叉隐患）。
  - **worker**：heartbeat/resetStale 收敛 `guardedCall`；tick/yield 让位收敛
    `releaseLease`；5s 硬编码提为 `queueCallTimeout`/`releaseTimeout`。
  - **workcopy**：`Ensure` 拆为 hitExisting/claimStale/refreshClaim/buildAndRegister；
    `runGit` 复用 acpx.RunProcess——进程组 TERM/SIGKILL 纪律 + 环境白名单
    （clone URL 内嵌 token 场景不再全量透传宿主环境）。
  - **skill**：frontmatter schema 三处声明收敛为 LibMeta 唯一事实源；`---` 围栏
    定位语义统一为 `findClosingFence`/`isFenceLine`（`---x` 伪围栏不再被
    FileProvider 路径吞成半个围栏）；Validate 归位 library.go、
    ParseRichFrontmatter 归位 frontmatter.go。
  - **router/policy/toolprior**：关键词四入口收敛 `hitScan` 骨架；Normalize/
    ValidMode 共用 modes 集合；Ordered/StrategyPrompt 共用 sortedEntries 排序
    视图（修 Info 失败时提示词序与工具表序矛盾）。
  - **blackboard**：文档声明 Specialist.Name 唯一性约束（观察游标按名键控）。

### Added

- **pack**：`LayoutDirs` + `LayoutBaseline`——领域包布局约定（`_shared` 基线 +
  非 `_` 前缀包目录）的单一事实源，LoadToolManifests 与 skill.LoadFromFS 共用。
- **lineage**：`SkillFromMeta`（LibMeta→Skill 投影的唯一事实源）；
  `Impact.SkillsAdded/SkillsRemoved`——refs_changed 类型历史上把技能名装进
  tools_added/tools_removed（字段名撒谎），新字段如实命名，旧字段同步填充保兼容。
- **dispatch**：拒绝原因常量 `ReasonNotAllowed`/`ReasonDepthExceeded`/
  `ReasonSelfDispatch`（DenyError.Reason 词表，审计消费方按常量比对）。

## v0.10.6 (2026-09-18)

### Changed

- **workcopy**【行为变化】：`Release` 引用归零不再立即删除沙箱目录——保留供
  同 PR 下次审查增量复用（换 head 只 fetch 新 PR refspec，base 分支浅对象已在
  库，省整轮重克隆），TTL 回收交 `Sweep` 兜底。同 PR 换 head（新推送）时
  `Ensure` 领用保留目录做增量刷新（`fetch --depth 1` + `checkout --force`），
  刷新失败（force push 抹掉旧引用等）回落全新克隆；刷新期间同 key 被常规建仓
  登记则既有条目胜出、领用条目整条作废；不同 PR（Number 不同）不复用。
  CLI 审查场景同 PR 多次触发是常态，此前每次 Release 即删导致重复克隆。

## v0.10.5 (2026-09-18)

### Added

- **fence**（新包）：提示词数据区围栏原语 `Data(title, content) (string, int)`——
  把不可信内容包进显式数据区，并中和内容中出现的围栏标记序列（防伪造
  "数据区结束"把注入文本抬出数据区）；返回中和次数作注入特征信号，
  留痕/打点策略归调用方（包零副作用）。
- **conversation**（新包）：多轮会话历史通用原语——`Turn` 类型、滚动窗口
  切分 `Split(history, keep) (recent, evicted)`、历史渲染 `Render`（单轮截断
  200/400 rune）、摘要+verbatim 拼装 `Combine`。摘要生成策略归调用方
  （LLM 压缩在消费方实现，包内全确定性）。
- **sampling**（新包）：测试时计算放大（best-of-N）的确定性聚簇原语
  `Aggregate[T](items, signature, eq) []Group[T]`——多通道签名快速定位 +
  eq 对比簇代表判归属（与首见者等价才并入，链式漂移不成簇）；「多份采样
  相互复现」作为可信度信号，不经任何模型。泛型实现，go 1.26。
- **textutil**：`SanitizeFileStem`（外部标识拼文件名前的路径分隔/引用语法
  字符消毒，防写入失败与目录穿越）、`NumberLines`（4 位宽行号前缀——给
  agent 的文件原文加行号，无行号会逼模型编造 file:line 证据）。

### Changed

- **severity**：`Normalize` 词表折叠外部审查专家常用别名——P0/fatal/urgent→
  high、P1/major→medium、P2/P3/trivial/nit(s)→low。此前消费方各自维护
  别名克隆（argus v3.8.0 修复的"外部专家 P0 整批落 low"问题在此收敛为
  一处）；原词表行为不变，越界词仍归 low 且第二返回值 false。

消费动机：argus v3.13.0 路线收尾把这些在 argus 侧验证过的通用能力
（输入卫生/会话窗口/采样投票/词表归一）下沉 agentkit，供多产品复用。

## v0.10.4 (2026-09-18)

### Added

- **breaker**: `Breaker.Abandon()` / `Breakers.Abandon(name)`——放弃在途半开
  探测的取消/终止语义（Allow==true 后调用方在取得结果前终止：任务级取消、
  优雅停机）。结果未知，既非成功也非失败，不计入统计；半开态立即恢复可
  放行新探测（此前只能等 probeTimeout 失联超时），closed 态无副作用。
  Allow 的配对契约扩为「Success / Failure / Abandon 三者恰好其一」；既有
  调用方零改动。消费动机：argus Dispatcher/consensus 的任务取消路径此前
  消费 Allow 后不配对，半开探测位滞留最长约 10 分钟（probeTimeout=最大
  插件超时+30s），期间健康插件被"熔断中"误跳过。

## v0.10.3 (2026-09-15)

bianque 工具身份一致性守卫根基（spec 2026-09-15-tool-identity-otel-guard-design P0/P1）。

### Added

- **agentrun**: `Event` 增加 `CallID`——tool_call 事件携带 assistant 声明的原生
  `ToolCalls[].ID`，tool_result 事件携带 tool 消息的 `ToolCallID`。消费侧
  （bianque toolPairer）据此做声明↔结果精确配对，取代按名 FIFO 猜配对——
  eino ToolsNode 默认并行执行同名工具，乱序返回下 FIFO 会张冠李戴（观测面
  「说 A 实得 B」假告警之源）。既有调用方零改动（新字段空值 = 旧事件流）。

- **skill**: `use_skill` 出参自证——`useSkillOut` 新增 `name`（解析后规范引用名）、
  `requested`（模型原始入参，别名命中时与 name 不同）、`version`（frontmatter
  version）、`checksum`（内容校验和，sha256 前 8 字节 16 hex，Provider 口径）。
  观测守卫以结果自报身份+校验和为锚点检测「声明加载 A、实际返回 B 正文」，
  不依赖事件流相邻顺序。`Library.Resolve` 补回 `Version`（此前恒空）。

## v0.10.2 (2026-09-15)

### Added

- **logredact**: 日志/审计载荷凭据脱敏——`Redact`（高敏感：密码/密钥/连接串/
  Bearer 模式化打码，删除后永不回填）+ `Masker`（低敏感拓扑标识 K8sGPT
  anonymize 形态：IPv4/注册精确串 → «Tn» 令牌进 LLM，展示面 Restore 回填真名；
  回填不回灌二次 LLM 输入，防掩码词表被注入探测）。与 safejson 正交
  （safejson 防 Markdown/HTML 注入，本包打码凭据）。argus runner 错误落日志
  路径已消费 `Redact`。

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

## v0.8.2 (2026-09-07)

- **deps**: ekit v0.20.1 → v0.27.2（Go 1.26.0；grpc 1.83 / protobuf 1.36.11 / gjson 1.18 传递升级）

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
