# Changelog

## v0.11.1 (2026-10-02)

扁鹊下沉轮：流式节拍/出口围栏参数化/文本分片三面收编 bianque 手写实现。

### Added

- **pacer（新包）**：流式增量节流器 `Stream`——「≥interval 或 ≥chars」窗口
  合并发布 + 单流总量帽（超帽静默停发）+ seq 单调 + `Absorb` 权威快照收口
  （迟到分片丢弃）+ `WithClock` 注入；LLM token 级分片防洪泛事件通道/前端
  的通用原语（收编 bianque deltaPacer）。
- **egress**：`Options{AllowPrivate, AllowLoopback}` + `CheckWith`/
  `CheckHostWith`——运维平台「探测内网服务/本机自建服务是能力本身」场景的
  参数化放行面；链路本地（云元数据）/组播/CGNAT/保留段不因放行开关解禁，
  缺省零值与既有 `Check`/`CheckHost` 同语义（测试钉住两入口 parity）。
- **textutil**：`EstTokensCJK`（Han/Hangul/Kana/全角计 2 单位，units/3+1——
  纯 rune 口径对中文系统性低估的修正）；`ChunkLines`（行边界凑片 + 超长行
  硬切 + join 严格还原——SSE 打字机分片通用原语）；`ClampRuneBoundary`
  （字节切点回退 UTF-8 边界，头尾切分场景共用）。

## v0.11.0 (2026-10-02)

版本线升版（代码面 = v0.10.41，无新增代码变更）。

0.10.35~0.10.41 连续七轮积累了成体系的破坏式变更，按 pre-1.0 语义化版本
约定（0.x.y 中 x 位升版 = 破坏性变更界线）升 minor 划线：**0.10.x 冻结为
旧 API 面，0.11.0 起为新 API 面**，消费方跨线迁移按下列清单一次完成。

### 0.11 线的破坏式变更全景（相对 v0.10.34）

- **acpx**：`RunResult` 增 `Stderr` 字段；pi 空文本终态语义（终态到达即权威）；
  mimo/kimi 终态优先契约统一；信封 error 上抛；GenericAgent 内联占位符
  fail-fast；finalizeStream 三段式收尾骨架（内部）。
- **llm**：`Attempt.Err` string → error + `AttemptError.Unwrap() []error` +
  `BreakerSkipped` 字段；`RetryableLLMError(nil)` 返回 false；JSON 路径同
  模型重试对称化；retryafter 原语下沉 ekit（llm 门面保留）。
- **mcp**：`Pool.Tools` 返回 `ToolsResult{Tools; Failed}`；`ToolResultError`
  导出 + `IsToolResultError`；探活/列举本地取消不摘连接；Tools 取消语义。
- **agentrun**：入口族收敛为 `Run(ctx, cfg, query, ...RunOption)` +
  `WithOnEvent`/`WithRetry`（四入口删除）。
- **httpx / jsonrepair 包删除**：整体下沉 ekit v0.33.0（pkg/httpjson、
  pkg/jsonrepair），消费方改 import ekit 路径。
- **lineage**：`Hub.Set` 去返回值。
- **rag**：`MilvusConfig.IndexType` 删除；过滤白名单双后端统一（未知 key
  报错）；`Local.Retrieve` 对非法 filter key fail-fast。
- **skill**：Library 装载 FS 故障上抛（不再吞成空库）；清单输出字典序稳定。

### 迁移指引

- `RunWithEvents/RunWithRetry/RunWithEventsAndRetry` → `Run` + `WithOnEvent`/
  `WithRetry`；
- `pool.Tools(...)` 两返回值形态 → `res, err := pool.Tools(...)`，失败面
  读 `res.Failed`；
- `AttemptError.Attempts[i].Err` 按error 处理（文案消费方改 `errors.As`）；
- `agentkit/httpx` → `ekit/pkg/httpjson`（`httpx.` 前缀改 `httpjson.`）；
  `agentkit/jsonrepair` → `ekit/pkg/jsonrepair`（包名不变仅路径）；
- textutil/reportutil 公开 API 未变（截断与统计已薄委托 ekit 单源）。

## v0.10.41 (2026-10-02)

通用件下沉轮：agentkit 中与 agent 语义无关的通用实现下沉 ekit v0.33.0
（pkg/stringx、pkg/httpjson、pkg/jsonrepair、pkg/mathx），agentkit 删除自有
实现改消费 ekit 单源。破坏式：httpx 与 jsonrepair 两包删除。

### Changed（破坏式）

- **httpx 包删除**：整体下沉 ekit/pkg/httpjson（DoJSON/DoJSONWithRetry/
  StatusError/RetryConfig + Retry-After 捕获与钳制取舍家族）。消费方
 （langfuse、rag.OpenAIEmbedder、websearch.Searxng）改 import
  `ekit/pkg/httpjson`（API 同形，包名 httpx→httpjson）。
- **jsonrepair 包删除**：整体下沉 ekit/pkg/jsonrepair（剥围栏→散文抽对象→
  单遍字符串感知语法修复→标量归一→Unmarshal 三级宽容链；测试随迁）。
  消费方（llm/acpx）改 import `ekit/pkg/jsonrepair`（包名不变零代码改动）。
- **llm/retryafter.go**：传输/钳制原语（sink/transport/parse/select）下沉
  ekit，本包保留 WithRetryAfterSink/RetryAfterFrom/SelectRetryDelay/
  MaxServerRetryAfter 薄委托（agentrun/resilient 消费 llm.* 门面不变）；
  「仅限流失败采信建议」的策略门控仍在 llm.generateRetry。OpenAI Provider
  的传输层装配改 httpjson.RetryAfterTransport。

### Changed（门面委托，公开 API 不变）

- **textutil**：TruncRunes/TruncBytes/TruncEllipsis 薄委托
  ekit/pkg/stringx（实现单源在彼）；TruncNote/SplitRunes/token 估算等
  agent 语义件留本地。
- **reportutil**：WilsonCI/McNemarExact 薄委托 ekit/pkg/mathx。

### deps

- ekit v0.31.0 → v0.33.0。

## v0.10.40 (2026-10-01)

ekit 升级轮：v0.27.2 → v0.31.0，并按 ekit 最新治理纪律收编 agentkit 的
手搓实现与裸 goroutine。依赖边界从 logx/concurrency.async 扩展到 pkg/encoding。

### Changed

- **deps**：ekit v0.27.2 → v0.31.0（v0.28~31 增量对本库直接相关的是
  v0.29 的 async 补齐与裸 go 治理、encoding 弱哈希治理后的 Sha256/Sha256Hex；
  v0.30/31 的弹性栈/可观测指标属微服务基础设施面，不在本库边界内）。
- **async**：接入 v0.29 补齐的 `GoWithContext`——worker/leader 主循环、
  worker/pool 心跳协程、progress/bus 退订协程改为显式携带 ctx 的安全
  goroutine；裸 `go func` 全面收编 `GoSafe`：mcp 的 Tools fanout（跑
  dial/Initialize/SDK 解析，外部 SDK panic 不打死进程）与闲置回收协程
  （静默 panic 死亡 = 回收失效无信号）、toolsched 的 runOne 起飞（runGuarded
  之外的外层兜底）、procx 的 stdin 异步写入、sysprompt 的并行环境采集、
  worker 的心跳探测对——对齐 ekit 全仓「禁止裸 go func」治理纪律。
- **logx**：包级 logger 迁 `Delegating` 纪律（ekit 现行约定：包归属用
  component 属性标识、懒解析到 `logx.Default()`，消费方可 `SetDefault`
  统一接管级别/后端）——rag（此前每次告警现场 NewSlogLogger 构造独立后端）、
  worker/pool、worker/leader。**audit 刻意保持 NewSlogLogger**：审计留痕
  不应随业务日志级别配置丢失（Delegating 会跟随 Default 级别），独立后端
  是其契约的一部分。
- **encoding**：skill 的 contentChecksum 与 reportutil 的 PatternKey 收编
  `encoding.Sha256`（v0.29 治理后该包不再提供弱哈希入口，sha256 包装是
  其明示用途）；两处手搓 sha256+hex 删除。

### 不迁移项（边界裁定）

- singleflight/ratelimit 维持 `golang.org/x/sync`、`golang.org/x/time`——
  标准库生态事实标准，ekit 同名包无语义增量，换用纯增依赖面。
- `network/httpx` 维持 agentkit/httpx 自持（68 行 DoJSON vs 微服务基础设施，
  既有边界裁定不变）。
- audit 的独立日志后端（见上）。

## v0.10.39 (2026-10-01)

工具链三件下沉（收编自 argus）：workcopy 浅克隆补全 + sast 确定性扫描新包 + mcp 配置卫生助手。

### Added

- **workcopy**：`EnsureMergeBase(ctx, dir, baseBranch, timeout)`——浅克隆工作
  副本按需历史补全：先本地探测 merge-base 可达性（零网络），不可达且
  .git/shallow 存在再 `git fetch --deepen` 指数加深（64 起步 ×2，8 轮覆盖
  8k+ 提交，时间预算兜底）；refspec 显式全量（--single-branch 克隆下
  origin/<base> 唯一可靠补全入口）；错误全程 logredact（传输失败回显带凭证
  clone URL）。收编自 argus internal/gitdeepen（v3.9.3 起生产验证）。
- **sast（新包）**：确定性 SAST 前置扫描——gitleaks/semgrep 执行+JSON 归一
  为结构化 `Finding`（与嵌入方契约解耦）：baseRef 非空走
  origin/<base>..HEAD 范围扫描、semgrep 聚焦变更文件（FocusPaths 防穿越/
  超限回退全仓）、单工具 findings 封顶留痕、失败置 Err 不中断其它工具。
  安全纪律：bin 只来自部署方信任源、gitleaks 恒 --redact 且 Secret/Commit
  绝不进 Finding、子进程环境白名单（EnvAllow）。收编自 argus internal/sastscan
  （v3.15.0 起 PR 审查生产验证）。
- **mcp**：`ExpandEnvRefs`/`ExpandEnvRef`——ServerConfig 头部  环境引用
  展开（token 不落配置文件；未定义保留原样+告警回调）；`WithCallMeta`/
  `CallMeta`——任务级 _meta 的 ctx 键标准实现（可直接作 Pool.RequestMeta，
  此前 argus/bianque 各写一份同型键）。

## v0.10.38 (2026-10-01)

第七轮：重构收口轮——第六轮审计「留观察」的结构性重构清单落地（破坏式）。

### Changed（破坏式）

- **agentrun**：入口族收敛——`Run`/`RunWithEvents`/`RunWithRetry`/
  `RunWithEventsAndRetry` 四个导出入口 → 唯一入口
  `Run(ctx, cfg, query, ...RunOption)` + `WithOnEvent` / `WithRetry` 正交
  组合。此前 retryQuery 是独立参数而 RetryAfterMutation 却在 Config，签名
  不对称；收敛后守卫逻辑单源。文档示例（README/FRAMEWORK/patterns）同步。
- **mcp**：`Pool.Tools` 返回 `ToolsResult{Tools; Failed map[string]error}`——
  被容忍的失败面（建连/列举失败、白名单未命中）程序化可见，调用方无须
  消费 OnError 副作用即可区分「失败被容忍」与「真的没工具」；调用方取消
  不记入 Failed（本地取消 ≠ server 失败，缓存冷时仍返回 ctx.Err()）。
- **lineage**：`Hub.Set` 去返回值（返回的一直是调用方传入的 lin，零信息量
  还暗示不存在的语义）。
- **acpx**：`RunResult.Stderr` 新增（≤400 rune 尾巴）——runCLI 此前把 stderr
  整条丢弃，CLI 告警/弃用提示在 transcript 上不可见；namedAgent 注册身份
  单源（ClaudeCode/Gemini/Mimo/Pi 四家 Name/bin 兜底逐字重复收敛，零值
  适配器的可执行名有兜底）；GenericAgent 模板内联占位符 fail-fast
  （`--model={model}` 不展开、能力推导不到，曾以字面量泄漏进 argv 静默错跑）。

### Fixed / 改进

- **llm**：estimateTokens 收敛 `textutil.EstTokensOf` 单源（此前 bytes/4 与
  包内 chars/3 两套口径并存，中文场景估算漂移 4~5 倍）；backoffDelay 的
  jitter 改按当次 delay 幅度（base/2 基准在高 attempt 档抖动占比趋近于零，
  打散失效）；「已重试 N 次」错误文案改真实计数（此前按 policy 上限谎报，
  401 首败也报「已重试 2 次」）。
- **permgate**：hardDeny 硬阻断单源（checkUserInteraction/checkAlwaysAsk/
  checkStandard 三段逐字重复）；subjects 多面匹配——同一输入的
  command/url/file_path 全部参与匹配（此前命中首字段即返回，command 与
  file_path 并存的输入里路径规则永不生效）。
- **toolsched**：plan/Schedule/Execute 拆分——Execute 的 runner 用依赖计数
  放行、不消费层级分组，此前内部整跑一遍 Schedule 白算 Groups。
- **rag**：Milvus 索引行 milvusRow 结构化（id/content/heading/file 四平行
  切片靠约定对齐的下标错位面消除）；`Local.Rescan` 并发收敛单份（scanning
  标志让位——多 goroutine 同时判过期此前各扫一遍，大目录内存峰值按并发数
  放大；换入后对所有人可见）。
- **compact**：Role 词表常量化（RoleUser/RoleAssistant/RoleTool——魔法字符
  串散落两文件，拼错即静默改变分组行为；值不变零迁移）。
- **acpx/pi**：`"error":null` 的事件不再转发字面 "null" 文案（仅回调文本
  面，判定不受影响）。

## v0.10.37 (2026-10-01)

diffx unified diff 解析纯函数（新包）+ textutil.FirstNonEmpty——收编自 argus。

### Added

- **diffx（新包）**：git 风格 unified diff 的结构性解析——`SplitChunks`
  （保序切块，同路径后者覆盖等确定性语义可用）/`SplitUnifiedDiff`
  （path→完整块，保留 "diff --git " 前缀）/`DiffPath`（+++ b/<path> 为准，
  删除文件回退 --- a/<path>，行尾制表符附加信息剥除）/`HunkLines`
  （hunk 新行闭区间，纯删除块不计；LineRange=[2]int 别名嵌入方签名零改动）。
  纯函数零依赖；语义级 diff 归 go-git-platform 侧，本包只管结构标记。
- **textutil**：`FirstNonEmpty(vals ...string)`——多候选回退语义单源。

收编来源：argus internal/context/diffprep（parseDiffByFile/rawDiffPath）与
internal/runner/inject（parseHunkLines）、runner+requirement 两处
firstNonEmpty；argus 侧保留 API 门面。

## v0.10.36 (2026-10-01)

mcp stdio 子进程与拨号预算层解绑 + agentrun 流式用量回调。

### Fixed

- **mcp**：stdio 子进程被拨号预算层误杀——spawn 的 exec.CommandContext 绑在
  dial 的连接级预算 ctx（Timeout 默认 30s）上，预算到期即 SIGKILL：建连与首轮
  列举在窗口内成功，30s 后任何 tools/list 必现 `transport closed`（消费方表现为
  「server 未返回工具」，长诊断后半程 MCP 全灭）。v0.10.35 的 dialGroup 外层
  WithoutCancel 挡不住内部重新包上的 WithTimeout 预算层。子进程改
  `context.WithoutCancel(cctx)` 承载，生命周期归连接关闭路径（evict/闲置回收/
  Pool.Close）。回归测试以测试二进制自举 stdio server，cancel 后列举工具钉住。

### Added

- **agentrun**：`Config.OnUsage` 流式用量回调（`Usage` 载荷）——开流后 eino
  callbacks OnEnd 拿到 StreamReader 无法取 Usage，reactHandler 收流 concat 后
  从 ResponseMeta 提取，每轮一次；非流式路径不触发（callbacks 面板两路不双计）。

## v0.10.35 (2026-10-01)

第六轮全面审计（6 面 code-reviewer 并行独立复查全部 54 个改动文件）+ 破坏式
重构收敛。5C + 14I + 次级修复与单源化。

### Fixed（第六轮审计）

Critical：

- **bashguard（3 个只读判定绕过）**：读侧重定向只看操作符放行——目标词与
  heredoc 体的命令替换/进程替换在真实 shell 会执行，`cat < $(rm -rf build)`、
  `cat < <(cmd)`、`cat <<< $(cmd)`、未引号定界 heredoc 体内的 `$(...)` 曾全程
  判只读（引号定界 `<<'EOF'` 体是纯字面不受影响，回归锁死）；`command` 无
  Wrapper 无子命令表，`command git push` 裸操作数被当不透明数据放行——Wrapper
  化（剥 flag 后按内核命令判定，`command -v ls` 不受影响）；`date -s/--set`
  （设系统时钟）列在安全值型 flag、macOS 裸操作数设时钟无守卫——移入
  UnsafeFlags + NoBareOperands（`+FORMAT` 格式串豁免前缀），`-r` 修正为值型。
- **rag/local**：代码块内单条超长行绕过 maxChunkRunes 硬上限——单行守卫只在
  正文路径，flush 出的单块越过 Milvus VarChar 65535 字节，整个文件（含正常
  段落）索引失败。守卫前置到 handleLine 顶部正文/代码块共用（handleOverlongLine
  单一入口），超长行按 maxChunkRunes 硬切成多块（不丢内容）。
- **mcp**：ReapIdle 绕过 errMu 直调用户 OnError——与 Tools 跨 server 并行的
  notifyError 并发调用同一回调是数据竞争（改走 notifyError）。
- **acpx/pi**：assistant 终态只含 thinking 块（纯思考收尾/末轮工具调用结束）
  时 `lastText==""` 误触全文兜底，整段 JSONL 事件流原文当正文以成功返回
  ——终态到达即权威，空文本是合法成功。

Important：

- **acpx**：mimo/kimi 终态优先迁移（step_finish/assistant 消息已到但进程退出
  码非零时以结果为准，与 claude/codex/pi 同契约，此前直接上抛丢全部结果）；
  envelopeOut 补 error 面（exit=0 的 `{"error":...}` 信封曾以空 text 触发全文
  兜底当成功返回，string 与 {message} 两形态都收）。
- **llm**：Resilient 的 JSON 路径同模型传输重试整体失效（固定经
  Client.GenerateJSON 按 Client 字段推导 policy，buildClient 设 MaxRetries=1
  ——一个瞬态 500 就切模型；抽 generateJSONFitted policy 参数版与 Generate
  路径对称）；Retry-After sink 陈旧值污染后续非限流退避（429 后的 500 曾白等
  整段限流窗口——仅 IsRateLimit 时采信）；截断 boost 一次后仍截断时同参数
  重复烧注定失败的请求（boosted 或无 MaxOutputTokens 可提升即停）；
  IsTransientStreamError 把 ctx 取消判瞬态（恢复循环会空转 10 轮）；
  RetryableLLMError(nil) 返回 true 的脚枪。
- **mcp**：自愈 redial 装配的工具不带 lease（inFlight==0，重试超 maxIdle 时
  连接可被闲置回收——assembleTools 装配单源化）；探活/目录列举把调用方 ctx
  取消当连接死亡摘除健康连接（外部短周期巡检会把好连接反复摘掉、stdio 子
  进程反复重启——本地取消甄别，与 dial 的 WithoutCancel 对齐）；Tools 缓存
  冷 + ctx 取消返回 (空表, nil) 静默降级（改返回 ctx.Err()）。
- **agentrun**：Retry-After 建议超钳制时跳过等待但仍立即原样重跑——恰是注释
  论证过要避免的动作；超钳制返回首轮错误交调用方/failover。
- **procx**：exec.ErrWaitDelay（leader 成功退出但守护化孙进程仍握管道，
  WaitDelay 强关）曾把成功执行整体判失败——特判为成功（npm 类包装器形态）；
  cmd.Cancel 对恰在 deadline 瞬间自然退出的 ESRCH 返回 os.ErrProcessDone。
- **permgate**：wildcard 路径规则可被 `..` 穿越绕过——file_path/path 类
  subject 匹配前 filepath.Clean（`/repo/data/../../etc/passwd` 不再命中
  `/repo/data/*`，fail-closed；Rule.Content 文档声明路径规则用 wildcard 形态）。
- **toolsched**：Task.Run 执行体 panic 无隔离——runGuarded 转为该任务 Err
  （与 worker.Pool 外部代码隔离同纪律），契约写入字段文档；runner.wg 从未
  Wait 的死代码移除。
- **skill**：Library 装载把权限/IO 故障吞成空库（仅 ErrNotExist 走跳过路径，
  其余上抛）；ListSkills/Names/DeprecatedExpiredInUse 按 map 迭代序随机输出
  （字典序稳定——字节级稳定提示词前缀的前提）；RenderList 降级截断碎多字节
  字符且头尾不计预算（TruncBytes rune 对齐 + 头尾入预算）。
- **jsonrepair**：尾逗号/非法转义两条全文正则不辨字符串边界——字符串字面量
  里合法的 `", ]"`/`\x` 序列被当损坏静默改写（修复器的错误成功比失败更贵）。
  重构为单遍字符串感知扫描器：全角标点/非法转义/尾逗号三类修复合一（与
  scanOpenBrackets/ExtractObject 同一 inStr/esc 纪律）。
- **pack**：parseDir 把权限/IO 故障吞成"无清单"（区分 ErrNotExist，与
  LayoutDirs 同纪律）；merge 的 `"_shared"` 字面量改 LayoutBaseline 常量。
- **rag/milvus**：主键 maxLength 128 与 file 字段 512 不一致（深路径文件
  Upsert 必因主键超长失败）——对齐 528；Local 对未知 filter key 静默返回空
  结果违反包契约（filterKeys/validateFilter 白名单单源，两后端同契约）；
  MilvusConfig.IndexType 删除（README 声明"保留但未生效"，从未接入）。
- **hookx**：Events 的 `/…/` 正则形态在事件派发热路径上每次重新编译
  （sync.Map 编译缓存，坏模式不缓存不匹配）。
- **bashguard**：`time` 是 bash 保留字，mvdan 解析为 TimeClause 走不到表条目
  ——`time ls` 曾报"不支持的控制流"误拒（递归判定被计时语句）。
- **clarify**：逃生选项判定是双向子串包含——「全面」「不」这类词把有效回答
  误消解为放弃细分（收紧为完整短语包含或 ≥2 字前缀）。

### Changed（破坏式重构）

- **llm**：`Attempt.Err` string → error，`AttemptError` 增 `Unwrap() []error`
  与 `BreakerSkipped` 字段——聚合错误可 errors.As 穿透到 RequestError/
  context.Canceled（此前只能对拼接文案做文本分类）；`RetryableLLMError(nil)`
  改返回 false。
- **mcp**：`ToolResultError` 导出（含 `Payload()`）+ `IsToolResultError`——
  包外消费方类型化识别工具业务失败，无须文案匹配。
- **acpx**：`finalizeStream` 三段式收尾骨架单源（执行错终态优先/exit=0 流内
  失败如实报错/无终态全文兜底；会话 ID 与终态正交，兜底保留）——claude/pi/
  mimo 接入，各家手写漂移（漏迁移终态优先、空文本终态当空壳）结构性消除；
  emit/emitText/emitError 统一 nil 安全发送；信封结果错误上抛。
- **textutil**：`TruncBytes`（字节预算 + rune 对齐）成为字节截断唯一原语，
  skill 的 capContent/RenderList 改用。
- **skill**：Library.byTitle 与 FileProvider.scanAliases 的同 Title 归一统一
  为字典序最小者（此前两 Provider 规则不同构）。
- **bashguard**：valueFlagEats 值型 flag 吃参判定三处（consumeFlags/
  splitSubcommand/stripWrapper）单源；date/命令派发策略面见 Fixed。
- **mcp**：connState.detachLocked/firstPositive/assembleTools 内部单源化
  （evict/ReapIdle/Close 失效逻辑、超时回退、装配+lease 注入各一份）。

## v0.10.34 (2026-10-01)

codeintel 轻量代码符号索引（Go AST + 多语言词法）——收编自 argus internal/codegraph。

### Added

- **codeintel（新包）**：工作副本目录索引——Go 侧 go/parser+go/scanner 建
  顶层符号定义表（func/type/var/const，含 receiver/签名片段）与标识符出现
  反向索引（建索引期一次付清，查询 O(1)）；JS/TS/Python 词法符号表（def/
  function/const 箭头函数，含参数表与 docstring 首行）；get_callers 词法调用
  边（pkg.F( 与 F( 形态，定义行不计）。API：`NewIndex(dir, maxFiles)`/
  `Definitions`/`References`/`GoCallers`/`PolyDefinitions`/`TouchedByLines`/
  `IndexedFiles`/`SkippedFiles`。只读并发安全；vendor/node_modules/testdata
  等噪声目录跳过；坏语法文件跳过计数不致命；maxFiles 封顶防超大仓。
  词法级边界（字符串/注释内命中、动态调用）由工具描述向 agent 声明核实，
  不做类型解析级调用图。收编自 argus（v3.15.0 起 PR 审查生产验证），
  argus 侧保留 API 门面。

## v0.10.33 (2026-10-01)

llm/wrap 模型装饰器集（eino BaseChatModel 窗口拟合/截断续写/限速）+ reportutil 模式键/Beta 统计——收编自 argus 生产验证实现。

### Added

- **llm/wrap（新包）**：eino BaseChatModel 装饰器三件，职责正交可组合
  （推荐 Rate→Fit→Continue），WithTools 均绑定透传再包装——ReAct 绑定后装饰
  不失效：
  - **FitModel**：发送前输入自守恒，三级递进（L1 microcompact 工具结果轮次组
    置换占位/L2 截最长 user TruncNote 留痕/L3 病理态迭代截最大工具结果），
    错误/提醒结果全级别保护；真实 usage 校准（RecordUsage 回灌配对
    tokens/char 系数，只紧不松，脏样本 0.02~4 界外不采信）；观测回调
    OnFit/OnBreakdown（按 role 分桶 rune 量）可 nil。
  - **ContinueModel**：finish reason=length/max_tokens 且无 tool_calls 时
    保留部分输出续写 ≤3 轮顺序拼接（拼接补全 JSON 恰是 jsonrepair 前置救济）；
    续写失败返回已得部分。
  - **RateModel**：x/time/rate 限速，直调旁路与 Generator 链同桶真全局。
  与 llm.Client.fitInput 分工：Client 侧服务 Generator 链单级截断，wrap
  服务直调旁路三级拟合，同进程并存无副作用。
- **reportutil**：模式级学习环确定性数学与键派生单源——`PatternKey`
  （sha256(plugin\x00normalize)[:16]，plugin 空落 unknown）、
  `NormalizePatternText`（小写/剥路径/hex/反引号/数字/折叠，截 160 rune）、
  `PatternSample`、`BetaConfidence`（Beta 后验：先验 0.5，Posted 抬、
  Regret/Suppress 压、Restore 只回抬不加正证据——suppress+restore 成对循环
  不可洗白）、`PatternSamples`。与 WilsonCI/McNemarExact 同族。

收编来源：argus internal/context（ctxfit/ctxcontinue/ctxrate，v3.36.0 起
生产验证）与 internal/learning；argus 侧保留 API 门面零改动。

## v0.10.32 (2026-10-01)

agentrun 真流式：ReAct 默认开流，思考/正文增量事件外发。

### Added

- **agentrun**：`Config.DisableStreaming`（缺省开流）——ADK Runner
  `EnableStreaming: true`，assistant 流式消息在 `reactHandler` 逐分片消费，
  新增 `reasoning_delta`/`text_delta` 增量事件（Text=分片非快照）；流收束后
  concat 成完整消息走既有派发逻辑——tool_calls 派发/终稿判定/最终 text 事件
  语义不变，消费方零改动兼容（增量是纯增量通道）。携带 tool_calls 分片的
  正文不外发增量（调用轮片段拼接非可读答复，其前导正文分片仍外发）。
  消费方节流建议：token 级分片直接落库会洪泛，按时间窗/字符合并后再发。

## v0.10.31 (2026-09-28)

pi 会话续聊 + 第五轮审计（1 确认 + 1 前四轮漏网绕过）。

### Added

- **acpx/pi**：会话续聊能力——`req.SessionID` → `--session-id <id>`（精确
  按项目会话 ID 续，缺省仍 `--no-session` 一次性）；session 事件的 id 回填
  `RunResult.SessionID`（新会话首跑即拿 id 续聊，三条返回路径全覆盖）。
  真机往返验证：首跑存暗号 → 续聊正确回忆（跨进程会话状态持久）。Capabilities
  增 Session。

### Fixed（第五轮审计）

- **bashguard（前四轮漏网的绕过面）**：前置环境赋值的动态展开未检查——
  `X=$(rm -rf build) ls` 曾被判只读：赋值右值的命令替换在真实 shell 里先于
  内核命令执行，判定面完全看不到（自动放行路径上可执行任意副作用）。
  Assigns 右值逐词 literalWord 检查，含动态展开即非只读；字面前置
  （`LC_ALL=C grep`）不受影响。
- **skill**：CRLF 行尾的起始围栏分歧——parseFrontmatter 要求围栏后紧跟 \n，
  `\r\n` 直接 bail（整份当正文、元数据丢失、与 Library 路径的 Content/
  Checksum 分歧）；闭合围栏本就容忍 CRLF，两路径围栏语义现真正对齐
  （BOM 同源家族的另一半）。

## v0.10.30 (2026-09-28)

acpx 扩员 + 第四轮「修复的修复」审计。

### Added

- **acpx**：新 agent 两家（均经本机真实 CLI 核实 + 真机冒烟通过）——
  - **pi**（pi-coding-agent，专用适配器）：`pi --mode json -p --no-session`
    JSONL 事件流解析——assistant message_end 的 text 内容块聚合正文（thinking
    块不计入）、usage/cost 提取、abort 类事件如实报错（exit=0 中止不静默）、
    `--model provider/id` 支持 + DefaultModel 回退、无事件流走全文兜底。
    保留本机扩展/技能/AGENTS.md 发现（编码任务需要环境）。
  - **dsh**（DeepSeek Harness，GenericAgent 预设）：`dsh --profile headless
    "<task>"` 单任务出口（stdout 即最终答复，真机核实 exit 0）。
  缺省注册 9→11 家。

### Fixed（第四轮审计：修复的修复）

- **skill**：BOM 修复只落了 ParseRichFrontmatter——parseFrontmatter（FileProvider
  主加载路径）仍不剥 BOM，带 BOM 的 SKILL.md 整份被当正文、元数据静默丢失、
  `---` 围栏泄漏进提示词。共用 stripBOM 双路径对齐 + FileProvider 级回归。
- **bashguard**：内联值修复不完整——bool 型 flag 的 `--flag=value` 形态
  （`git log --format=%H`/`--pretty=oneline`/`blame --date=iso`）仍被拒，
  readonly 快路径对 git log 基本失效。flagLookup 两级查表（base 本键 +
  base+"=" 显式内联键，后者让表里既有死键活过来）。
- **llm**：BreakerProbeTimeout 推导硬编码 90s 每尝试预算——provider 配置
  600s AttemptTimeout 时半开探测仍会被判失联、并发第二探测放行（v0.10.28
  修的隔离在长配置下复现）。预算改为 max(90s, 链上 AttemptTimeout 最大值)。
- **llm**：GenerateJSON 的 reflect 改写把误用（值类型/nil 指针 out）从
  InvalidUnmarshalError 可捕获错误退化成 panic。入口校验恢复错误语义。
- **permgate**：交互型工具段补项目 deny——被 deny 的交互型工具曾退化为
  「弹窗一点就能跑」的 ask（与 alwaysAsk 段纪律对齐）。
- **bashguard**：env 的死只读配置删除（被 wrapper 定义覆盖，内含永不匹配键）。
- **rag/Milvus**：Index 注释修正（Delete 成功后 Upsert 失败的残余窗口如实
  声明，不再宣称「旧数据完好」）。

### 备注

- eino v0.10 仍无正式版（最新 v0.10.0-alpha.35），维持 v0.9.21——工具箱
  不上 alpha；eino-ext 组件无更新。

## v0.10.29 (2026-09-27)

审计盲区补全轮：agentrun 与 mcp 包本体两路独立复查（此前两轮审计未覆盖的
面）+ 上轮遗留次级项清零。修复 1 Critical + 6 Important。

### Fixed

- **mcp（Critical）**：现代协议（2026-07-28）连接上 `Client.Ping` 是 no-op
  （协议移除了 ping RPC）——mcp-go v1.1 升级后 PingServer 对现代 server 虚报
  存活、touch 给死连接续命、闲置回收永远收不走，「无声死亡只有探活才能暴露」
  的租约面承诺被整体击穿。建连时记录协商代际（Initialize 后断言 concrete
  client 的 ProtocolVersion），现代连接探活改发真实轻量 RPC（tools/list），
  legacy 连接维持协议 ping；测试杀 server 实证现代代际下探活暴露死亡。
- **mcp**：WithHTTPTimeout 误把连接级 Timeout（30s）设成该连接每一次 HTTP
  请求的硬上限——任何执行超 30s 的 MCP 工具在 HTTP 传输下永远失败且无法按次
  放宽。拆分 `ServerConfig.RequestTimeout`（缺省 10 分钟，挂死兜底；工具正常
  时长控制归调用方 ctx）；连接 Timeout 回归 dial+Initialize 预算本义。
- **mcp**：并发自愈互杀——redial 无条件 evict 缓存连接，会关掉别的自愈刚建好
  且正在重试的新连接（健康 server 上并发自愈成功率不确定、伪失败+重复建连）。
  evict 带实例比对（cached == 本工具失败的死连接才摘）。
- **mcp**：ReapIdle 闲置回收不失效目录缓存——重建连接后命中旧目录不重新列举，
  闲置期间 server 工具面/schema 变化永久不可见（违反 catalog.go 声明的不变量）。
  回收与 evict 同构（clients/lastUsed/catalog/modern 四表齐删）。
- **mcp**：自愈层 %v 包装吞掉 OAuthAuthorizationRequiredError 的错误链——
  自愈重连撞上授权失效时调用方拿不到「需重新授权」确定态。改 %w 保链。
- **mcp**：Close 后再调 StartIdleReaper 会启动永无人停的回收协程（泄漏）。
  closed 守卫拒绝。
- **agentrun**：PlanAndExecute 恒设 GenInputFn 把 eino 默认规划提示词
  （PlannerPrompt——完整规划方法论与格式要求）整体丢弃——无指令时 planner
  裸奔、有指令时默认提示词也不在场，与两处文档承诺相反。仅带指令时设置且
  追加注入（指令前置 + 默认提示词保留），规划结构兜底掩盖了质量退化故测试
  全绿未暴露。llmtest 桩补 Flats/Heads 记录支撑提示词组装断言。
- **acpx**：Registry 的 agents map 无锁——Register 与 Run/Get/Names 并发是
  map 读写 fatal（文档建议装配期注册，但一行守卫的成本远低于误用代价）。
  RWMutex 全覆盖 + 并发回归测试。
- **worker/pglease**：expires_at 用应用时钟写入、DB now() 判过期——多实例
  时钟漂移等量放大/缩短租约，削弱「失联后 ≤ttl 换主」。改 DB 时钟
  （now() + make_interval）统一。
- **breaker**：Failure 的配对契约显式化（仅 Allow==true 放行过的调用上报；
  被拒调用上报会被当探测失败重新熔断）。

## v0.10.28 (2026-09-27)

老包深度审计轮：三路独立复查（acpx 解析面 / worker-workcopy 并发面 /
llm-skill-检索面）+ 覆盖率盲区调查，修复 1 Critical + 6 Important + 4 次级
（全部带回归测试）。

### Fixed

- **workcopy（Critical）**：增量刷新窗口的领用不摘牌——claimStale 领用保留
  目录时未从 entries 摘除旧 key 条目，刷新（fetch + checkout --force，
  秒到分钟级）期间并发 Ensure(旧head) 会命中正被重写的工作树（撕裂读）、
  Release(旧key) no-op 导致 rc 永久泄漏 Sweep 永不回收、竞争窗口的
  RemoveAll 会删掉在用目录。领用即摘牌（rc++ 与 delete 同临界区）；刷新
  失败不回挂（可能撕裂的树不再可命中，交 Sweep/孤儿扫描兜底）。
- **acpx/codex**：error 事件被 lastText 条件吞没——exit=0 且事件流含 error
  且有部分 assistant 文本时错误完全丢弃（部分输出后配额耗尽/流中断的实弹
  形态，mimo 纪律曾明确锁定此教训）。有错如实报错。
- **acpx/claude**：init-only 空壳 result 绕过全文兜底——init 事件把 result
  置非 nil 空壳，result 事件丢失（超长行 >1MB 被限容丢弃/流尾 8MB 截断）
  时返回空文本成功。sawResult 跟踪区分「result 到达但文本为空」（合法
  空成功）与「result 未到达」（走兜底）。
- **llm/resilient**：熔断半开探测时限未接线——Breakers 缺省 1 分钟 <
  单模型一次完整尝试的最坏在途（≈3 分钟），超时探测被判失联、并发新探测
  全放行，半开单探测隔离失效。ResilientConfig.BreakerProbeTimeout 缺省按
  (RetriesPerModel+1)×(MaxDelay+90s) 推导并接线 WithProbeTimeout。
- **llm/resilient**：用户取消计熔断失败——连续 3 次主动取消（Esc/关页/请求
  超时）即把模型熔断 5 分钟全员降级。取消走 breaker.Abandon（v0.10.4 为
  此场景设计的原语：结果未知不计统计、解除半开占位）。
- **knowledge/rag（Milvus）**：Index 先删后嵌——embedding 失败（外部网络
  调用，抖动/限流/超时是常规事件）时该文件旧索引已清，从检索面静默消失
  且检索侧无信号。调序为 chunk → Embed 成功 → Delete → Upsert。
- **worker/leader**：tick 的 TryAcquire 无独立限时——存储挂起时 Stop 永久
  阻塞在 <-e.done（优雅停机悬挂）。acquireTimeout=5s（与 releaseTimeout
  同口径），超时按存储抖动处理（保守失主、下周期重试）。
- **llm/client**：GenerateJSON 重试共用同一 out——encoding/json 失败/合并
  不清零已解码字段，上次部分解码残留泄入重试成功的结果。解进临时零值、
  成功后整体赋回（Resilient 的 JSON 路径同受益）。
- **skill**：FileProvider.CanonicalName 缺精确名优先守卫——技能 A 的
  frontmatter name 恰与技能 B 目录名相同时 use_skill("B") 被别名表重定向
  到 A（allowed 误拒/拿错全文）。精确路径可达即短路（同 loadName 的防穿越
  归一）。
- **skill**：ParseRichFrontmatter 不容忍 BOM/前导空白（parseFrontmatter
  容忍）——带前导空行或 UTF-8 BOM 的 SKILL.md 被整份当正文、元数据静默
  丢失；capContent 字节截断可切非法 UTF-8（rune 安全回退，与 httpx 口径
  一致）。

### Changed

- **审计确认项**：acpx 事件流健壮性（半行/超长行/UTF-8/goroutine 生命周期）、
  procx 竞选/熔断语义、hotplug/progress、logredact ReDoS（RE2 线性）、
  skill 路径遍历、usage 记账防重、failover 并发、httpx 限容——均复核无问题。

## v0.10.27 (2026-09-27)

深度回归审计轮：对新移植的十个面做独立复查 + 跨包组合验证，修复 3 Critical +
4 Important（全部带回归测试锁死）。

### Fixed

- **procx（Critical）**：Stdin 管道在 Start 之后才建——os/exec 守卫使整段成为
  死代码，子进程实际拿到空 stdin（hookx 全部 hook 派发受影响：遵循协议的
  hook 静默失效）。StdinPipe 移到 Start 前、建立失败 fail-fast；新增
  TestStdinPayloadDelivered 断言 payload 真实可达。
- **hookx（Critical）**：Stop 事件 continue 布尔与协议相反——continue:false
  （否决停止）被忽略、continue:true（无异议）反被当否决可造成无限续跑。
  修正为 continue:false→StopShouldContinue；continue:true 无意见。
- **bashguard（Critical）**：联合 flag 折叠校验不查 FlagKind——值型 flag 折进
  联合形态后其参数伪装成操作数（`sort -ro f` = `-r -o f` 写路径被判只读）。
  折叠现在要求全部组成为 FlagBool 且不在 UnsafeFlags；支持 `--flag=value`
  内联值（optional/string 型）；sort 的 -o 矛盾条目移除。
- **compact**：FormatSummary 用 ReplaceAllString 把摘要正文当替换模板——
  `${VAR}`/`$1` 被展开腐蚀（代码摘要高频面）。改字面拼接；TokenGap 的
  Anthropic 形态符号反了（真实报文 requested 在前，原实现恒解析失败）；
  TruncateForRetry 重试预算差一（第 3 次被拒）。
- **filestate**：mtime 未前进+同 size 时先于内容比对返回——保留时间戳的同长
  覆盖（cp -p/rsync -a 回灌）静默绕过 staleness；现在该分支先比对内容。
  读取历史无界增长（全量 Content 永久驻留，编辑型 agent 长会话数百 MB 级）
  ——每路径只保留最新一条。
- **bashguard 表**：git remote 改子命令形态（show/get-url 原是永不命中的
  死条目）、env -u 转 FlagString（原必拒）、git 全局前置 flag（-C/-c/
  --no-pager 等）入顶层 SafeFlags、branch/tag 陈旧注释清理。

### Added

- **integration_test**（仓库根）：跨包组合验证——bashguard 静态解析 →
  permgate 许可判定 → hookx 改写输入后重走链 → toolsched 注解直转调度 →
  compact 压力面，验证五件套接缝语义（新原语拼成完整工具调用治理故事）。

## v0.10.26 (2026-09-27)

acpx 直连出口:能力核对与展示双单源导出(下游 preFlight 降级范式一等公民化)。

### Added

- **acpx `UnsupportedFields(a, req) []string`**(原私有 `unsupportedFields`
  导出):非零但不支持的控制面字段名单(Go 字段名,声明序)。Registry.Run 的
  fail-fast 与直连调用方的降级裁决共用——绕过 Registry 直连 `Agent.Run` 的
  嵌入方(如 huginn preFlight:整跑失败代价高,选择降级而非 fail-fast)按
  名单自行裁决「剔除降级 / 保留透传+显式告警,绝不静默」。
- **acpx `CapsNames(c) (supported, unsupported []string)`**:能力五字段的
  支持/不支持双名单(小写字段名,声明序)。原 `capsString`(仅支持侧)改由
  它派生;直连调用方的能力展示面(如 huginn agents 清单「支持:… | 不支持:…」)
  共用此单源,增删 Capability 字段时名单自动跟上。

## v0.10.25 (2026-09-27)

ZCode 移植清单收官轮：剩余七项一次落地（5 新包 + 3 包扩展 + procx 单源增强）。

### Added

- **filestate（第 39 包）**：读后写一致性状态——编辑/覆写前必须读过且读后无
  外部改动。ErrNotRead（未读/部分读不满足编辑前提）/ErrStale（整数毫秒 mtime
  前进或 size 变化——亚毫精度差不误报）；全量读+内容一致豁免（formatter 重写
  场景）；RecordEdit 回写防连锁误报；路径归一（Clean+大小写不敏感盘折叠）。
  对标 ZCode read-file-state.ts。
- **permgate（第 40 包）**：工具调用许可判定链（allow/ask/deny + 结构化
  ruleId 审计）。优先级：交互型→ask；alwaysAsk 硬阻断段（disallowed/项目
  deny 仍压过「问」）→会话授权→ask；一切「禁」压过 yolo（与 ZCode 的历史
  差异：其 yolo 先于硬禁是兼容遗留，新装配「yolo 跳过的是问不是禁」）；项目
  deny→deny、ask→ask；只读模式拦写入；项目 allow→预批钩子→配置白名单→会话
  授权→只读兜底→默认 ask。规则内容匹配（精确/`prefix:*` 边界前缀/`*` 通配）
  + 输入对象提取（command/url/file_path/path/pattern/patch_text）+ Edit 规则
  匹配 Write + 会话授权生命周期。与 policy（操作审计门）正交。
- **hookx（第 41 包）**：外部 hook 拦截协议——7 拦截点 + 子进程 stdin JSON/
  stdout JSON 决策协议 + 多 hook 单调归并（deny 单调、阻断置位不撤销、
  updatedInput 后者胜）。决策面：continue:false/decision:block→阻断（权限
  事件同时 deny）、decision:approve→权限升格、additionalContext 累积、
  updatedInput 改写（消费方须以改写输入重走许可链）。hook 故障按「无意见」
  容错+OnError 可观测。执行经 procx（进程组终止——sh 孙进程握管道写端会让
  裸 exec 的 Wait 挂死）。
- **bashguard（第 42 包）**：bash 命令静态风险解析——mvdan.cc/sh 真 AST 解析
  → 调用提取 → 表驱动只读判定，**权限判定不执行命令**。保守原则：解析失败/
  动态词（$()/反引号/$VAR）/写重定向/控制流（if/for/while/函数）一律非只读；
  不在表=非只读；未知 flag=非只读；危险 flag 显式优先。值型 flag 消费
  （bool/string/number/optional）、联合 flag（-rn）全组成安全才放行、子命令
  递归（git 表：log/diff/status/branch 列表形态等；`git branch <名>` 创建/
  `git stash` 裸 push/`git tag <名>` 打标经 NoBareOperands 守卫拒绝）、安全
  包装剥壳（env/nice/nohup/time/timeout/stdbuf/xargs）。10K 长度上限。
  新外部依赖 mvdan.cc/sh/v3。
- **steering（第 43 包）**：运行中转向队列——guide（模型请求边界注入，不打断
  工具执行）/queued（turn 后消费）双投递语义；guide 不丢（窗口关闭降级排队）。
- **compact 扩展（二期）**：溢出自适应重试（TokenGap 从 provider 报文提取
  超差；SelectAfterOverflow 按 gap 上移保留组数；TruncateForRetry 丢最旧组
  兜底，上限 3 次）；rapid-refill 熔断（压缩后 <3 工具轮又满连续 3 次→
  ErrRapidRefill，指引排查大输出源）；GroupRounds 轮次分组（对标
  groupByAssistantStartedRounds 含 leading-user 自成组语义）；摘要 prompt
  模板（9 段结构+安全约束逐字保留+前后 no-tools 围栏）+ FormatSummary
  （剥 analysis/展开 summary）+ SummaryMessage 续接引导；中断补洞
  RepairInterrupted（调用无结果插占位、与调用同序——恢复回放不被 provider 拒）。
- **llm 扩展**：流恢复原语——IsTransientStreamError（瞬态原因码双写兼容）/
  SuspiciousEmptyResult（零文本+零调用+非 stop+零用量=可重试空终态）/
  IsContextExceededFinish|Error（溢出锚点，接 compact reactive）/busy 准入
  （3008/3009/3010 + 1s/2s 两档退避）+ MaxStreamRecoveryRetries=10。

### Changed

- **procx**：RunRequest 新增 Stdin（启动后写子进程 stdin 并关闭；子进程不读
  stdin 提前退出的 EPIPE 静默容忍）——hookx 的交互式协议驱动，进程纪律仍单源。

## v0.10.24 (2026-09-27)

### Added

- **compact（第 37 包）**：会话压缩策略面，对标 ZCode compact/policy.ts +
  microcompact.ts。两层：
  - `ShouldCompact` auto-compact 判定——阈值 = (contextWindow − min(output 预留,
    21K 上限)) − 13K buffer（provider 窗口输入输出共享，分母先扣 output 侧）；
    token 双轨（provider 真实用量优先于本地估算——provider 口径含 cache 读写，
    本地估算 chars/3 必然低估）；连续失败 3 次熔断；估算计工具调用入参
    （大型 args 漏计是 ZCode 踩过的坑）；<2 个 assistant 轮不压（摘要开销可能
    超过省下的窗口）。
  - `MaybeMicrocompact` 轻量先手——旧工具结果替换占位符（`[Old tool result
    content cleared]`，LLM 可感知的诚实形态）：保留最近 N 组（缺省 5）与出错
    结果（排障证据，可选放开）；组 = 同一 assistant 轮内的连续结果（随轮整体
    清除）；最小节省门槛 256（微小收益不让会话形态抖动）；idle 60 分钟也触发
    （挂起会话的旧结果大概率无引用价值）；阈值推导 `min(auto 阈值×0.9, 阈值
    −2000)`（BuildDefaultThreshold）。白名单缺省不限（通用工具箱无 ZCode 的
    内置工具名表，生产应传实际名单收紧）。
  - 摘要生成（真压缩）归调用方——模型请求不在原语层。
- **sysprompt（第 38 包）**：system prompt 分段组装，对标 ZCode
  context/builder.ts。Section 化（Name/Target/Boundary/Content + chars/tokens
  自计量）→ 排序（system-stable → system-dynamic → user_context；组内插入序
  确定性）→ 分块（stable 块在前保 provider 前缀缓存命中，dynamic 块隔离在后
  防击穿）+ user_context 附加文本（引导句 + 「未必相关」免责句，措辞可覆写）。
  内置段：IdentitySection（唯一 stable 内置）、EnvSection、GitSection（带
  「会话开始时的快照」免责——模型对过期状态自信是真实事故面）、DateSection。
  `DetectEnv` 运行期采集（git 子命令经 procx 纪律：argv 直传/5s 超时/限容）。

## v0.10.23 (2026-09-27)

### Changed

- **deps 深升级**：eino v0.9.18 → v0.9.21（ADK filesystem/cancel/checkpoint 修复面，
  无 API 破坏）；**mcp-go v0.43.0 → v1.1.1（跨大版本）**——2026-07-28 现代协议
  （server/discover 无状态握手、请求级 `_meta` 协议键）、SEP-2322 多往返输入、
  异步 Task、server 侧入参 schema 校验全量解锁。适配点：in-process 传输改为
  真实 JSON 往返（meta_test 只回显业务键，协议自注入的 `_meta` 键不再断言
  string）。
- **mcp**：dial 改 transport 直构 + `client.NewClient` 选项装配（client 级选项
  只能在 NewClient 挂载；stdio 便捷构造器隐含的 Start 显式化）。新增
  `Pool.ElicitationHandler client.ElicitationHandler`——server 工具执行中反向
  征集输入（表单/URL 模式）时回调；Initialize 同步声明 elicitation 能力。
  测试覆盖现代协议全链（真实 streamable HTTP：InputRequests 征集 → handler
  应答 → 重试回显）；未装 handler 时协议错误如实冒泡。

### Added

- **toolsched（第 35 包）**：工具并发调度器，对标 ZCode tool/scheduler.ts +
  batch-runner.ts——一条模型消息里的多个工具调用按注解与依赖分组：组内并发
  （`MaxConcurrency` 上限切组）、组间有序。并行判定链：destructive 一票否决 →
  idempotent（≈concurrentSafe）显式优先 → readOnly → 具名未声明查
  `ReadOnlyTools` 兜底表（缺省保守不并行）；匿名无声明放行。`StopAfter`
  停轮门声明（plan 批准类）独占单例组，成功后剩余组以 Skipped 呈现、失败不停轮；
  组内失败不截断后续组（ZCode 踩坑语义：本地失败误截断后续 Agent 已固化教训）；
  结果恒按输入序返回。`Schedule` 纯函数可测，`Execute` 并发执行；ID 重复/依赖
  悬空/依赖环确定性报错。Hints 与 mcp.ToolHints 字段同构（解耦：非 MCP 工具
  同形态入调度）。
- **egress（第 36 包）**：出口围栏——LLM 可控 URL 的字面量层 SSRF 防护，对标
  ZCode webfetch-egress-guard.ts。阻断：localhost/.localhost 域、IPv4/IPv6
  字面量落非公网段（回环/私网/链路本地含云元数据 169.254.169.254/组播/
  CGNAT/benchmark/文档段/保留段、ULA/ORCHID/discard/6to4/Teredo）；
  **IPv4-mapped IPv6（::ffff:0:0/96）与 NAT64 well-known prefix（64:ff9b::/96）
  先还原内嵌 IPv4 再套同一策略**（经典逃逸路径）；域名不做 DNS preflight
  （部分网络 1s 内解析不完会误杀公网——取舍对标 ZCode 注释）。`BlockedError`
  结构化命中（原因+地址）；URL 解析失败/空 host fail-closed。

### Fixed

- **llm**：retryafter_test 存量 errcheck（Body.Close/w.Write 返回值显式丢弃）——
  `make check` lint 门禁恢复全绿。

## v0.10.21 (2026-09-25)

### Added

- **mcp**：URL 传输 OAuth2 客户端装配（批次五十六 C，对标 ZCode OAuth 全栈）——
  `ServerConfig.OAuth *OAuthClientConfig`（ClientID/Secret/Scopes/MetadataURL/
  RedirectURI/TokenStore），dial 分支走 `client.NewOAuthStreamableHttpClient`
  （PKCE 恒开；静态 headers 保留可共存）。TokenStore 必填 fail-fast（缺省会落内存
  store，重连丢授权态）。401 经 `client.OAuthAuthorizationRequiredError` 冒泡
  （errors.As 可穿透池的 %w 包装），授权码流程由调用方经 `client.GetOAuthHandler`
  驱动——handler 独立于 MCP 连接存活，连接 Close 不影响 ProcessAuthorizationResponse。

## v0.10.20 (2026-09-25)

### Added

- **mcp**：请求侧上下文透传——`Pool.RequestMeta func(ctx) map[string]any`（nil 安全，
  批次五十六 B，对标 ZCode `com.zcode/request-context`）。metaFn 在调用方 ctx 上取值
  （池不感知业务键），经 `metaClient` 装饰器注入每笔 CallTool 的 `req.Params.Meta`
  （stdio/HTTP 同走 JSON-RPC params；装饰在 client() 单点——初次建连与自愈 redial
  同走；已有 Meta 按 AdditionalFields 合并，metaFn 键胜）。server 侧通用可读——
  供下游 serverkit 写审计关联字段。
- **mcp**：`ServerConfig.ProbeTimeout` 探活独立预算（缺省 `DefaultProbeTimeout`=5s，
  对标 ZCode MCP_PING_TIMEOUT_MS）——ping 是轻量协议方法，此前误吃连接级 30s 预算。

## v0.10.19 (2026-09-24)

### Changed

- **mcp**：工具目录层——tools/list 在建连后仅走线一次并缓存（批次对标 ZCode
  「连接时列一次、注册面全量」adapters/src/mcp），此前每次 `Tools()` 都经
  `einomcp.GetTools` 全量走线。转换自持（mcp.Tool → eino tool，口径与 eino-ext
  components/tool/mcp v0.9 一致；isError:true 降级锚点文案不变），本包不再依赖
  eino-ext/components/tool/mcp。目录随连接 evict（列举失败/探活死亡/自愈摘除）
  一并失效，重建连接重新列举。

### Added

- **mcp**：工具注解归一 `ToolHints{ReadOnly,Idempotent,Destructive}` + `Pool.Hints(server)`
  快照——随目录同源提取（指针字段 nil 按规范缺省，mcp-go 服务端 NewTool 缺省
  destructive=true，第三方无注解工具保守落非只读）。消费面：url 并发开关
  （annotations 档读写锁）、审计门风险分级、控制台风险徽标。`ConcurrentSafe()`
  （readOnly+idempotent，对标 ZCode concurrentSafe）、`RiskLevel()`
  （readOnly→low / destructive→high / 其余 medium）。

## v0.10.18 (2026-09-22)

### Added

- **skill**：两级渐进披露的预算面（批次五十三自 bianque 沉淀，对标 ZCode skills.ts
  预算-降级形态）——
  - `RenderList(metas, budget)`：清单渲染 + 确定性预算降级。budget≤0=不限（等价
    ListPrompt）；超预算先降级为纯名单（name only），仍超按预算截断并注明。降级
    是确定性行为而非异常：形态可测试、可预期；多字节字符按完整名回退不截半个字。
  - `AsSkillTool` 新增可变参 `ToolOption`：`WithContentCap(n)` 限定 use_skill 单次
    返回全文上限（超限截断 + 尾部声明——LLM 可感知的诚实形态，不静默吞内容；
    缺省 DefaultContentCap=100KB，≤0=不限）。变参向后兼容，存量调用零改动。
  - 缺省常量 `DefaultListBudget=20000` / `DefaultContentCap=100000`。
- **mcp**：连接租约面（对标 ZCode pool 探活+惰性关闭；语义按借用模型裁定）——
  - `PingServer(ctx, name)`：MCP 协议级探活。HTTP server 被停掉不派发断连回调，
    「无声死亡」只有显式探活才能暴露；死亡连接立即摘除出缓存并关闭，下次调用
    透明重建。无缓存连接返回 `ErrNotConnected` 哨兵（懒建连模型下属正常态）。
  - `ReapIdle(maxIdle)` / `StartIdleReaper(interval, maxIdle)`：闲置回收租约。
    语义裁定：ZCode 原型的引用计数在本池不可行（工具对象生命周期调用方不可见，
    refcount 无处挂钩），借用模型的等价租约 = 闲置回收 + 调用期透明重建。闲置
    时钟 = 最近一次建连/取工具/探活成功；启用回收的部署须保证 maxIdle 显著大于
    最大工具超时（在途调用不触碰时钟）。Close 停止回收协程。

### Fixed

- RenderList 截断分支对「名单短于预算但含头超预算」形态的切片越界，随首版一并
  修复并以测试钉住。

## v0.10.17 (2026-09-21)

### Added

- **mcp**：调用期自愈 `selfHealTool`（批次五十，对标 ZCode callTool 自愈：先查
  disconnected 再重连、SDK 裸 "Not connected" 竞态重试一次）——`Pool.Tools` 返回的
  每个可调用工具套自愈层：传输层死亡类错误（not connected/connection closed/broken
  pipe 等，`isTransportDead` 判定）时摘死连接→重连→GetTools 按名解析同名工具重试
  一次；业务类错误（isError:true，errorAsObservation 层）不触发自愈。自愈层在最外，
  观察包装在内层。自愈重连失败/重试仍失败如实上抛（错误带自愈轨迹）。

### Fixed

- 无（自愈覆盖此前「GetTools 成功后连接才死→工具调用持续失败到会话结束」的窗口；
  GetTools 失败路径的 evict+重连既有语义不变）。

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
