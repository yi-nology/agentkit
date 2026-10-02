---
feature: extract-bianque-common
status: delivered
updated: 2026-09-10
branch: extract/bianque-common-v0.9.0
commits: 7de8d09..a502eb7
---

# 从 bianque 抽取通用组件进 agentkit

## Report

**What was built** — agentkit v0.9.0 新增 5 个包（logredact、worker/pglease、websearch、hotplug、jsonrepair）并扩展 4 个既有包（skill 多根 Library、llm.UsageHandler、mcp.UnwrapMCPText、textutil.TruncEllipsis）。能力来自生产项目 bianque 的本地重复实现，接口做了泛化：租约表名可配、检索语言可配、用量归因改 Labels map、JSON 校验 Schema 可配。

bianque 同轮切换：删除 internal/logredact、search、cluster、strutil；Plugboard 改为 hotplug 别名；SnapshotStore 嵌 hotplug.Holder；llm.obs 改为 agentkit UsageHandler 薄适配；UnwrapMCPText 委托 agentkit/mcp。skills 领域校验层（maturity/requires_mcp/rewrite）与 protocol 报告 schema 仍留 bianque——通用扫描骨架已在 agentkit/skill.Library。

**Verification** — agentkit `go test ./...` 全绿（含新包）；bianque `go test ./...` 全绿（go.mod replace 指向 agentkit worktree）。命令：`cd agentkit/.worktrees/extract-bianque && go test ./... -count=1`；`cd bianque/.worktrees/extract-bianque && go test ./... -count=1`。

**Journey log** — macOS sed 的 `\b` 无效导致 `search.` 误替换污染字符串，改用 Python 精确替换 + import 别名（`search "…/websearch"`）收敛。skill ParseRichFrontmatter 闭合围栏有 off-by-one（`rest[i+1:]` 未剥 `---`），按 bianque 原实现（独立成行检测 + TrimPrefix("---")）修正。normalizeScalars 领域键（decision/scope/charts）未上收，jsonrepair 只提供 Schema 可配骨架。

## [S1] Problem

agentkit（AI Agent 通用工具箱）当前缺若干「平台级通用件」，而生产项目 bianque
已在本地重复实现了它们：日志脱敏、PG 租约存储、公开网搜、插拔/热替换持有点、
多根技能库、LLM 完整用量采集、宽容 JSON 修复、MCP 信封解包。

结果是：通用能力散落在领域仓、agentkit 缺口未补、后续项目会再抄一遍。
本轮把这些结构从 bianque 抽到 agentkit v0.9.0，并同轮把 bianque 切到消费新包。

## [S2] Design

### 抽取原则

1. **零领域耦合优先**：目标包不得引用 bianque 的 model/conf/engine。
2. **接口先于实现**：已有接口（如 `worker.LeaseStore`）只补实现，不改语义。
3. **泛型/可配置化**：领域硬编码（表名、语言、归因字段）参数化。
4. **bianque 切换收口**：抽完后删本地重复代码，import 指向 agentkit。

### 目标包与来源映射

| # | 来源（bianque） | 目标（agentkit） | 形态 |
|---|---|---|---|
| A | `internal/logredact` | **新包** `logredact` | 整包迁移 |
| B | `internal/cluster` | **新包** `worker/pglease` | 实现迁移，表名可配 |
| C | `internal/search` | **新包** `websearch` | 整包迁移，language 可配 |
| D | `internal/agents` plugboard + snapshot 持有 | **新包** `hotplug` | Plugboard + 泛型 Holder |
| E | `internal/skills` Library 核心 | **扩展** `skill` | 多根 Library + 决策适配 |
| F | `internal/llm/obs.go` | **扩展** `llm` | UsageHandler + 归因泛化 |
| G | `protocol` 宽容 JSON 修复 | **新包** `jsonrepair` | 管线骨架 |
| H | `protocol.UnwrapMCPText` | **扩展** `mcp` | 单函数迁移 |

### 接口契约

#### A. `logredact`（零依赖）

```go
func Redact(s string) string           // 模式化打码（URL 凭据/token/Bearer）
func RedactValue(v any) any            // 递归脱敏 JSON 形态值
```

规则集从 bianque 原样迁移；与 `safejson` 正交（反注入 ≠ 凭据打码）。

#### B. `worker/pglease`

```go
type PGLeaseStore struct{ /* db *sql.DB; table string */ }
func NewPGLeaseStore(db *sql.DB) *PGLeaseStore
func (s *PGLeaseStore) WithTable(name string) *PGLeaseStore // 缺省 "agentkit_lease"
func (s *PGLeaseStore) TryAcquire(ctx, key, holder string, ttl time.Duration) (bool, error)
func (s *PGLeaseStore) Release(ctx, key, holder string) error
func (s *PGLeaseStore) Migrate(ctx context.Context) error
```

语义与 `worker.LeaseStore` 一致（原子 UPSERT / 仅持有者释放）。
bianque 切换后表名仍用 `bq_lease`（`WithTable("bq_lease")`），避免迁移已有库。

#### C. `websearch`

```go
type Result struct{ Title, URL, Snippet string }
type Service interface {
    Search(ctx context.Context, query string, topK int) ([]Result, error)
}
type Searxng struct {
    BaseURL     string
    Language    string // 缺省 "zh-CN"；空=不传 language 参数
    DefaultTopK int
    HTTP        *http.Client
}
func NewSearxng(baseURL string, timeout time.Duration) *Searxng
func (s *Searxng) Search(...) ([]Result, error)
func (s *Searxng) AsTool() tool.BaseTool   // web_search eino 工具
```

#### D. `hotplug`

```go
// Plugboard 启停视图：未登记=启用；nil 指针=全启用。
type Plugboard struct{ /* atomic.Pointer[map[string]bool] */ }
func NewPlugboard(all []string, disabled map[string]bool) *Plugboard
func (p *Plugboard) Enabled(id string) bool
func (p *Plugboard) Disabled() []string

// Holder 泛型原子快照持有点（读无锁、换整体）。
type Holder[T any] struct{ /* atomic.Pointer[T] */ }
func NewHolder[T any]() *Holder[T]
func (h *Holder[T]) Load() *T
func (h *Holder[T]) Store(v *T)
```

bianque 的 `Snapshot` 领域字段（Registry/RoutingTable/Skills…）及其
nil-继承兜底逻辑 **留 bianque**；只把 Plugboard 与原子持有换成 agentkit 类型。

#### E. `skill` 扩展 — 多根 Library

```go
// Library 进程内技能库（可热替换；无缓存，每次读当前快照）。
type Library struct{ /* byName map[string]Entry */ }
type Entry struct {
    Full, Body string
    Meta       Meta   // 扩展：Mode/Maturity/Version/RequiresMCP/Deprecated
    Path       string
}

// LoadFromFS 多前缀扫描：opts.ScanPrefixes 缺省 ["_shared/skills"] + "<pkg>/skills"。
func LoadFromFS(fsys fs.FS, opts ...LoadOption) (*Library, error)

func (l *Library) Get(name string) (string, bool)     // 全文（含 frontmatter）
func (l *Library) Body(name string) (string, bool)    // 剥 frontmatter 正文
func (l *Library) Has(name string) bool
func (l *Library) Describe(name string) Meta
func (l *Library) Names() []string
func (l *Library) Provider() Provider                  // 决策式 Provider（AgentProvider）
func (l *Library) CanonicalName(name string) (string, bool)
```

frontmatter 扩展字段（mode/maturity/version/requires_mcp/deprecated）
进入 agentkit skill.Meta——它们是技能生命周期通用概念，不是扁鹊专属。
bianque 保留：RewriteMode/RewriteBody、DeprecatedExpiredInUse、平台校验矩阵。

#### F. `llm` 扩展 — UsageHandler

```go
type UsageRecord struct {
    Model            string
    PromptTokens     int
    CachedTokens     int
    CompletionTokens int
    ReasoningTokens  int
    TotalTokens      int
    DurationMS       int64
    FinishReason     string
    Iteration        int64
    Labels           map[string]string // 归因标签（bianque 填 session/case/step/agent）
}

type Sink func(UsageRecord)

func WithUsageLabels(ctx context.Context, labels map[string]string) context.Context
func WithCallCounter(ctx context.Context) context.Context
func NewUsageHandler(onRecord Sink) callbacks.Handler
```

归因从固定字段改为 `Labels map[string]string`，领域键由调用方决定。
bianque 的 `WithAttribution` 变薄包装。

#### G. `jsonrepair`

```go
func ExtractObject(s string) string          // 提取首个平衡 JSON 对象
func Repair(s string) string                 // 尾逗号/未闭合括号/无效转义修复
func NormalizeScalars(raw json.RawMessage) (string, error)
func ParseLenient(raw string, v any) error   // 栅栏剥离→修复→提取→反序列化
```

报告 schema 与 `ErrNotReport` 留 bianque；骨架进 agentkit。

#### H. `mcp.UnwrapMCPText`

```go
func UnwrapMCPText(raw string) string
```

### bianque 切换清单

| 本地代码 | 动作 |
|---|---|
| `internal/logredact` | 删包，全部 import → `agentkit/logredact` |
| `internal/cluster` | 删包，`main.go` 改用 `worker/pglease` + `WithTable("bq_lease")` |
| `internal/search` | 删包，import → `agentkit/websearch` |
| `internal/agents/plugboard.go` | 删文件，类型别名或直接用 `hotplug.Plugboard` |
| `internal/agents/snapshot.go` | 保留 Snapshot 结构；`SnapshotStore` 改嵌 `hotplug.Holder[Snapshot]` |
| `internal/strutil` | 删包，调用点改 `textutil.TruncEllipsis`（本轮新增便利函数） |
| `internal/skills` Library/DecisionProvider 核心 | 改为薄封装或直接用 `agentkit/skill.Library` |
| `internal/llm/obs.go` | 删文件，改用 `agentkit/llm.NewUsageHandler` + Labels |
| `protocol` repair 函数 | 删实现，改调 `agentkit/jsonrepair` |
| `protocol.UnwrapMCPText` | 删函数，改调 `agentkit/mcp.UnwrapMCPText` |
| `metrics.ObserveLLM` | 留 bianque（指标名绑域名）；可选后续再抽 |

### 附加：textutil 便利函数

```go
// TruncEllipsis 截断并追加省略号（展示面统一语义，替代 strutil.Truncate）。
func TruncEllipsis(s string, n int) string
```

### 版本与文档

- agentkit 版本升 **v0.9.0**（新包 + 扩展 API，minor bump）。
- README 包清单、FRAMEWORK.md 对应章节、CHANGELOG 同步。
- bianque `go.mod` 升 agentkit v0.9.0（经 replace 指向 worktree 便于联调；交付时按仓库惯例发版/替换）。

## [S3] Out of Scope

- 不抽：`auth`（Hertz 绑定）、`repo`（领域仓储）、`eventbus`（progress.Bus 已覆盖）、
  `agents/packs+registry+routing`（扁鹊专家包 schema）、`llm.ModelRouter`（应改用 StageRouter，另案）、
  `metrics` 业务指标、`engine/protocol` 报告 schema、审批闸门/HITL 原语。
- 不改 agentkit 现有包的破坏性 API（除 skill.Meta 增加可选字段）。
- 不做 bianque 本地 replace 的永久化（交付说明即可）。

## Tasks

- [x] T1: agentkit 新包 logredact + 单测 — acceptance: `go test ./logredact` 通过；Redact/RedactValue 语义与 bianque 一致 (covers: S2.A)
- [x] T2: agentkit worker/pglease + 单测 — acceptance: 实现 LeaseStore 编译期断言；表名可配；Migrate/TryAcquire/Release 行为正确 (covers: S2.B)
- [x] T3: agentkit websearch + 单测 — acceptance: Service/Searxng/AsTool 可用；language 可配；表驱动覆盖错误路径 (covers: S2.C)
- [x] T4: agentkit hotplug（Plugboard+Holder）+ 单测 — acceptance: 并发 Store/Load 安全；nil Plugboard=全启用 (covers: S2.D)
- [x] T5: agentkit jsonrepair + 单测 — acceptance: Extract/Repair/Normalize/ParseLenient 覆盖栅栏、尾逗号、散文包裹 (covers: S2.G)
- [x] T6: agentkit mcp.UnwrapMCPText 迁移 + 单测 — acceptance: 五例语义与 bianque 原测试一致 (covers: S2.H)
- [x] T7: agentkit textutil.TruncEllipsis + 单测 — acceptance: n 边界与 rune 截断正确 (covers: S2 附加)
- [x] T8: agentkit skill 扩展（Library/LoadFromFS/Provider/CanonicalName + Meta 扩展字段）+ 单测 — acceptance: 多根扫描、重名报错、决策 Provider 可用 (covers: S2.E)
- [x] T9: agentkit llm.UsageHandler（Labels/CallCounter/NewUsageHandler）+ 单测 — acceptance: 无归因跳过、Labels 透传、Duration/Iteration 正确 (covers: S2.F)
- [x] T10: agentkit 文档（README 包表/CHANGELOG v0.9.0；FRAMEWORK 详章可后补）— acceptance: README 包清单与代码一致 (depends: T1-T9)
- [x] T11: bianque 切换 logredact/cluster/search/strutil — acceptance: 编译通过；本地对应包删除；测试绿 (covers: S2 切换; depends: T1,T2,T3,T7)
- [x] T12: bianque 切换 hotplug/llm-obs/mcp-unwrap（skills 领域校验与 jsonrepair 领域归一留 bianque）— acceptance: 编译与现有测试通过 (covers: S2 切换)
- [x] T13: 双仓验证（agentkit `go test ./...` + bianque `go test ./...`）— acceptance: 全绿 (depends: T12)
- [x] T14: 评审与文档收口 — acceptance: spec status=delivered (depends: T13)
