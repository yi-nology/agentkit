# Agent 架构模式支持矩阵

七种常见 agent 架构在 agentkit（+ eino 底座）上的支持情况。原则：**编排原语不重复 eino**，
agentkit 提供的是"原语之上的样板与领域件"（降级链/工具纪律/限流/知识）。

| # | 架构 | 支持状态 | 载体 |
|---|---|---|---|
| 1 | Single Agent | ✅ 一等支持 | `llm`（Generator/Resilient 降级链/预算/fitInput）+ `agentrun.Run` 单查询 |
| 2 | ReAct | ✅ 一等支持 | `agentrun`（ChatModelAgent + 事件流 + ToolsFactory + 重试样板）+ `toolprior` 三层工具约束 |
| 3 | Plan-and-Execute | ✅ v0.8.0 样板 | `agentrun.PlanAndExecute`（封装 eino adk prebuilt/planexecute 三件套） |
| 4 | Reflection | ✅ v0.8.0 原语 | `agentkit/reflection`（生成→批判→修订收敛循环） |
| 5 | Router+Skill | ✅ v0.8.0 原语 | `agentkit/router`（LLM 显式意图路由）+ `agentkit/skill`（渐进披露 skill 决策使用） |
| 6 | Blackboard | ✅ v0.8.0 原语 | `agentkit/blackboard`（共享黑板 + 专家轮转） |
| 7 | Graph Workflow | ✅ eino 原生 | `eino/compose`（Graph/Branch 条件边/并行）+ adk Sequential/Parallel/Loop/Supervisor |

---

## 1. Single Agent —— `llm`

无编排，一次调用。90% 的场景从这里开始；所有上层架构的节点最终都是一个 Single Agent。

```go
client := llm.NewClient(chatModel, "deepseek-v4", budget)
client.ContextTokens = 1_000_000
msg, err := client.Generate(ctx, "stage", msgs)          // 带重试/限速/fitInput
err = client.GenerateJSON(ctx, "stage", msgs, &out)      // JSON 版（解析失败回喂）
```

需要多模型容灾时换 `llm.NewResilient`（降级链），接口不变（`llm.Generator`）。

## 2. ReAct —— `agentrun` + `toolprior`

边思考边调用工具的循环。**首选默认架构**——目标模糊、步骤不可预知时用它。

```go
out, err := agentrun.RunWithEvents(ctx, agentrun.Config{
    Name: "reviewer", Instruction: instruction,
    Model: chatModel,
    ToolsFactory: func() []tool.BaseTool { ... },  // 每次尝试重建（限流计数按尝试重置）
    MaxIterations: 12,
}, query, func(e agentrun.Event) { /* text|tool_call|tool_result */ })
```

工具表经 `toolprior` 组织：`StrategyPrompt`（提示词软约束）+ `Ordered`（排序注意力）
+ `WithCallLimit`（硬限流，超限返回模型可见的 LIMIT_REACHED 文本软止损）。

## 3. Plan-and-Execute —— `agentrun.PlanAndExecute`

计划先行：Planner 拆解目标为分步计划 → Executor 带工具逐步执行 → Replanner 评估进度
决定完成或修订计划，循环收敛。**目标明确、步骤可预规划的长链路任务**用它（区别于
ReAct 的边想边做）；每步执行结果可审计。

```go
res, err := agentrun.PlanAndExecute(ctx, agentrun.PlanExecuteConfig{
    Planner:  plannerModel,     // 须支持 tool calling（计划结构以 tool-calling 强制产出）
    Executor: executorModel,
    Tools:    tools,
    MaxSteps: 10,
    PlannerInstruction: "只规划代码相关步骤",
}, "把模块 X 从框架 A 迁移到框架 B")
fmt.Println(res.Answer)
```

需要定制 Replanner 提示词/输入整形时，直接用 eino `adk/prebuilt/planexecute` 原语。

## 4. Reflection —— `agentkit/reflection`

生成 → 按 Rubric 自我批判（结构化 pass/issues）→ 带全量问题修订 → 收敛或达轮次上限。
**产出质量有可判定的硬标准**时用它；标准主观或需外部事实核验的场景 Critic 不可靠，
不要用。

```go
res, err := reflection.Refine(ctx, &reflection.Config{
    Model: chatModel, ModelName: "deepseek-v4",
    Task:  "实现该需求的 Go 函数",
    Input: requirementText,
    Rubric: "1. 处理了空切片 2. 无 data race 3. 有表驱动测试",
    MaxIterations: 3,
})
fmt.Println(res.Output, res.Converged, len(res.Rounds)) // Rounds 含每稿与批判留痕
```

## 5. Router + Skill —— `agentkit/router` + `agentkit/skill`

两层含义互补：

- **显式路由**（`router`）：入口流量可枚举为有限意图时，先用一次廉价分类调用选路，
  再分发到对应处理链（各链可以是任意架构——ReAct/P&E/单轮）。

  ```go
  r, _ := router.New(&router.Config{
      Model: fastModel, // 分类用快模型即可
      Routes: []router.Route{
          {Name: "bug-fix", Description: "修代码类请求", Handle: fixChain},
          {Name: "explain", Description: "解释类请求", Handle: explainChain},
      },
      MinConfidence: 0.6,
      Fallback: func(ctx context.Context, input, reason string) (string, error) { ... },
  })
  d, out, _ := r.Do(ctx, userInput) // d.Route/d.Confidence/d.Reason 可观测
  ```

- **隐式路由**（`skill`）：类别多到枚举不动/由领域文档定义时，不做前置分类——把 skill
  清单注入提示词，agent 按任务相关性自主 `use_skill` 按需加载全文（渐进披露）。

  ```go
  metas, _ := provider.ListSkills(ctx)
  instruction += skill.ListPrompt(metas)   // 清单进提示词
  useSkill, _ := skill.AsSkillTool(provider, nil) // use_skill 工具进工具表
  ```

## 6. Blackboard —— `agentkit/blackboard`

共享黑板 + 多专家轮转读写，无中心调度员（区别于 eino adk `prebuilt/supervisor` 的
中心指派）。每位专家观察黑板增量、判断与己相关则贡献；一轮无人补充（共识）或达
轮次上限停止。**多视角分析、贡献顺序不可预知**的场景。

```go
board := blackboard.NewBoard()
board.Seed("material", diffText)
res, _ := blackboard.Convene(ctx, board, []blackboard.Specialist{
    {Name: "security", Act: func(ctx context.Context, b *blackboard.Board, since int64) (bool, error) {
        for _, e := range b.EntriesAfter(since) { /* 观察增量 */ }
        b.Write("security", "finding", "SQL 注入风险")
        return true, nil
    }},
    {Name: "perf", Act: ...},
}, &blackboard.ConveneOptions{MaxRounds: 3})
fmt.Println(res.Rounds, len(board.Entries()))
```

专家内部可以用任何架构实现（常见：每位专家内部是一个 ReAct）。

## 7. Graph Workflow —— eino `compose` + adk workflow

**agentkit 不重复编排原语**——图编排直接用 eino 底座，agentkit 的包（llm/agentrun/
toolprior/reflection/...）作为图上的节点构件：

- `compose.NewGraph[I, O]` + `AddChatModelNode/AddToolNode/AddLambdaNode` +
  `NewGraphBranch`（条件边）/并行分支——DAG/状态图/循环图；
- `adk.NewSequentialAgent / NewParallelAgent / NewLoopAgent`——顺序/并行/循环的
  agent 级 workflow（`LoopAgent` 配 `NewBreakLoopAction` 可提前退出）；
- `adk/prebuilt/supervisor`——中心调度员的多 agent 协作（与 blackboard 的无中心
  形态互补，按"是否有明确指派逻辑"选择）。

组合示例：图的每个 LLM 节点用 `llm`（重试/预算），工具节点用 `toolprior.Ordered`
产物，单个复杂节点内部是 `agentrun` ReAct——**节点内用 agentkit 样板，节点间用
eino 编排**。

---

## 选型速查

| 场景特征 | 用什么 |
|---|---|
| 一次调用能解决 | `llm` Single Agent |
| 步骤不可预知，边做边看 | ReAct（`agentrun`） |
| 步骤可预规划、需可审计 | Plan-and-Execute（`agentrun.PlanAndExecute`） |
| 有硬质量标准，可自评 | Reflection（`reflection.Refine`） |
| 意图可枚举、处理链差异大 | Router（`router.Do`） |
| 意图由领域文档定义、数量大 | Skill 渐进披露（`skill.AsSkillTool`） |
| 多视角无中心协作 | Blackboard（`blackboard.Convene`） |
| 有中心指派的多 agent | Supervisor（eino adk prebuilt） |
| 复杂流程/条件分支/并行 | Graph（eino compose / adk workflow） |
