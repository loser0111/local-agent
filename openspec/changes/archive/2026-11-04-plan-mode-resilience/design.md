# Design

## Context

动机见 `proposal.md`。实现层面的现状约束：

- 计划数据在 `internal/store/plan.go`（`Plan`/`PlanStep` + `PlanStore`，每份计划一个 JSON 文件），状态机目前是 `awaiting_approval → running → completed/failed/cancelled`，`failed` 只能经 `Reopen` 退回待审核。
- 执行器在 `internal/app/chat.go`（`ChatPlan`、`executePlan`），每步复用 `runToolLoop`（`internal/app/chat.go`）；运行登记与取消在 `internal/agent/runcontrol.go`（`BeginExclusive` 保证单会话互斥，`ByPlan` 可按计划查活跃运行）。
- 前端计划开关是 `frontend/src/panes/ChatPane.vue` 里的 `const planMode = ref(false)`；计划态在 `frontend/src/stores/plan.js`（按会话分桶），面板在 `frontend/src/panes/PlanPane.vue`。
- 模型调用已有统一重试：`internal/llm/llmretry.go` 的 `DefaultRetryPolicy`（4 次尝试、500ms 起指数退避、8s 上限），并把 HTTP 失败包装成 `LLMHTTPError`（含 `Status`、`RetryAfter`，`Unwrap` 到认证/限流/服务端故障三个哨兵）。也就是说：**「网络问题导致消息无法继续」= 重试层已经放弃之后的状态**，要修的是它之后的重建能力，而不是再加重试。
- 会话持久化在 `internal/store/sessions.go`（`Session` + `SessionPatch`，单会话一个 JSON）；Wails 绑定由 `wails generate module` 生成。

## Goals / Non-Goals

**Goals:**

- 计划模式的「意图」「进度」「终端状态」三者都落在磁盘上；任何一次中断（网络、崩溃、界面重连）之后状态可解释、可继续。
- 续跑是「一次点击」，不需要先退回待审核、也不需要重打需求。
- 复用现有唯一执行引擎（`runToolLoop`）与唯一事件通道（`chat:event`），不新建第二套。
- 旧数据（已有 `sessions/*.json`、`plans/*.json`）零迁移可用。

**Non-Goals:**

- 不做步骤并行 / DAG 编排。
- 不做「无人值守自动续跑」：续跑必须由用户触发（避免失败循环无界烧 token）。
- 不改 `internal/llm` 的重试策略本身（次数、退避、判定口径保持现状）。
- 不做消息库的物理删除或回滚；幂等只作用在「构造模型请求」这一层。
- 不做跨会话计划复用 / 计划模板。

## Decisions

### D1 计划模式是会话字段，且是「粘性」的

`Session` 新增 `planMode bool`（JSON 只增字段，旧会话按 `false`）；新增绑定 `SetSessionPlanMode(sessionID, bool) (*Session, error)`；前端 `planMode` 改为读会话状态、切换时调后端（乐观更新 + 失败回滚）。

理由：用户抱怨的正是「开关消失」。粘性（不因一次发送自动复位）与原实现（生成计划后 `planMode=false`）相反，但更符合「模式」语义，也避免「规划失败后开关已经没了」的二义性。

备选与取舍：
- `localStorage`：无法多窗口/多入口一致，且浏览器 mock 与桌面端会分叉。
- 全局设置：不同会话任务性质不同（探索型 vs 修 bug），全局会带来噪声。
- 保持单次意图 + 失败后自动复位为开：仍然是内存态，刷新即丢，治不了根。

### D2 规划意图由 Plan 记录承载（新增 `planning` / `plan_failed`）

`Plan` 新增 `Query string`、`Error string`、`Revision int`；状态新增 `planning`、`plan_failed`、`interrupted`。

`ChatPlan` 的顺序改为：
1. 校验会话/模型（同现状）；
2. **先**落一条 `status=planning, query=query` 的计划（同会话已有 `planning`/`plan_failed` 记录则复用/覆盖，保证只有一条未完成记录）；
3. 调规划器；
4. 成功且 `needPlan=true` → 该记录转 `awaiting_approval` 并写入步骤；
5. 成功但 `needPlan=false` → 删除该记录，走普通 `runToolLoop` 直答；
6. 失败 → 该记录转 `plan_failed`，写入 `Error`，`emitPlanUpdate`，返回 `ChatResult{Plan: plan, Error: ...}`（注意：`App.Chat` 目前把「有 error 且无 reply」转成 Go error 返回，需为 `plan != nil` 的情况放行，让前端能拿到计划对象）。
   - 用户中途停止时转 `PlanCancelled` + `Error="规划已取消"` 并带 `Cancelled=true`：取消是主动行为，不该给一个「重试规划」按钮，也不该显示成失败。这类记录不可重试，下次发送会新建一条（它不是「未完成记录」）。
   - 应用在规划中途退出会留下永久 `planning` 的幽灵记录：`PlanStore.loadAll` 把它转成 `plan_failed`（可重试、且 `Query` 已落盘）。

备选：单独的 `pending-plan.json` 或给用户消息挂标记——前者多一套文件生命周期，后者要动消息渲染；复用 Plan 能直接复用 `GetSessionPlan`、`plan_update` 事件与 PlanPane。

### D3 失败不再标记 skipped；新增 `interrupted` 状态

- 失败路径：失败步骤 `failed`，其余仍 `pending`，计划 `failed`（可续跑）。
- 取消路径：未执行步骤 `skipped`，计划 `cancelled`（保留现状）。
- 启动恢复：`running → interrupted`，运行中步骤 `interrupted` 并记录 `Error`。

理由：`skipped` 表达的是「因故不再执行」，而失败后那些步骤其实一行都没跑；把它们标 skipped 会让「继续执行」看起来像在重跑被放弃的步骤。

### D4 `ResumePlan` 与 `ReopenPlan` 语义分离

- `ExecutePlan(planID)`：只接受 `awaiting_approval`（**BREAKING 内部语义**：不再接受 `failed/cancelled`，避免「执行」与「续跑」混淆）。
- `ResumePlan(planID, useStream)`：接受 `failed/interrupted/cancelled`；不复位已完成步骤，直接从第一个非 `done` 步骤执行；不需要重新批准。
- `ReopenPlan(planID)`：保持现状（退回待审核、允许编辑步骤），供「我改了步骤再跑」。

状态机（计划级）：

```
planning ──ok──> awaiting_approval ──ExecutePlan──> running
   │                                   running ──ok──> completed
   └──fail──> plan_failed              running ──step fail──> failed ──ResumePlan──> running
                   │                   running ──cancel──> cancelled ──ResumePlan──> running
                   └─RetryPlan─> planning
running ──进程退出─> interrupted ──ResumePlan──> running
awaiting_approval ──丢弃─> (deleted)
```

### D5 步骤级自动重试，错误分类复用 llm 层

`internal/llm` 导出 `ClassifyError(err) ErrorKind`（`network` / `timeout` / `rate_limit` / `server` / `auth` / `bad_request` / `context_overflow` / `unknown`）与 `Retryable(kind) bool`；实现只读 `LLMHTTPError.Status` 与三个既有哨兵的错误链，**不解析错误文案**。

`ChatResult` 增加 `ErrorKind string`，由 `runToolLoop` 在失败返回处填充。`executePlan` 拿到 `ErrorKind` 后：
- `Retryable` → 步骤内自动重试（`1 + 2` 次，退避 2s / 8s，每次重试前检查取消；重试次数写进 `PlanStep.Attempts`，并在重试的 system prompt 里追加「上一次尝试已做过什么、失败原因」，供模型对账）；
- 不可重试（`auth` / `bad_request`）→ 立即失败并给出可读建议；
- `context_overflow` → 立即失败并按现有压缩路径提示（不在此处新增压缩逻辑）。

理由：重试次数放在计划层而不是 llm 层，因为计划层知道「这一步可以安全重来」，而 llm 层只是传输层。

### D6 步骤尝试幂等：新增消息归属字段 + 请求期过滤

- `PlanStep` 新增 `Attempt int`（第几次尝试）、`ContextMark int`（步骤开始时 `len(session.Messages)`）。
- `Message` 新增 `PlanStepIndex int` 与 `Attempt int`（`omitempty`）。
- 写「步骤请求消息」时：若历史里已存在同 `PlanStepIndex` 的 user 消息，**不再追加**（重试复用），否则追加并带上 `PlanStepIndex`、`Attempt`。
- `buildRunMessages`（组装请求处）在进入计划执行上下文时过滤：对每个 `PlanStepIndex`，只保留 `Attempt == max(Attempt)` 的那批消息；不删库，聊天流仍显示完整尝试（视图层可折叠）。
- `Message` 的结构变更对旧会话是零值 `0`，`0` 视为「不属于任何步骤」，过滤逻辑天然不命中。

备选：物理删除旧尝试（破坏审计，且 diff/checkpoint 按时间窗口归因，删除会造成错位）；不写步骤消息（丢审计，聊天流里看不到这一步干了什么）。

### D7 版本号 + 惰性对账，解决事件丢失与乱序

- `PlanStore.saveLocked` 中 `p.Revision++`；`emitPlanUpdate` 携带 `Plan.Revision` 与 `RunID`（`runs.ByPlan` 取）。
- 前端 `applyUpdate` 只在 `incoming.revision >= current.revision` 时接受（旧数据 `revision=0` 视为最低）。
- `App.GetSessionPlan` 做**惰性对账**：若 `plan.status == running` 且 `a.runs.ByPlan(plan.ID) == nil`，则把计划转 `interrupted`（运行步骤一并转），落盘并 `emitPlanUpdate`，再返回。这样「界面显示执行中但后端其实没在跑」只要拉一次就收敛，不需要新绑定，也不需要前端猜。
- 前端在打开面板、页面重载、事件通道重连后，对「当前会话 + `executingSessionIds`」逐条 `loadForSession`。

### D8 启动恢复保留进度

`PlanStore.loadAll`：`running → interrupted`，`running` 步骤 `interrupted` + `Error="应用重启，执行中断"`，并保留 `Done` 步骤与其摘要。旧版本遗留的 `failed` 计划无需改写（`ResumePlan` 接受 `failed`）。

### D9 前端开关与错误态交互

- `ChatPane` 的 `planMode` 改为 `computed(() => sessionStore.planModeOf(sid))`；`togglePlanMode` 调后端并乐观更新。
- 生成计划成功后**不再复位**开关；普通（非计划）发送不受影响。
- `PlanPane` 增加三态渲染：`planning`（进行中 + 取消）、`plan_failed`（错误原因 + 「重试规划」）、`interrupted`（中断原因 + 「继续执行」）；`failed` 时按钮从「修改后重试」改为「继续执行」+「编辑计划」两个入口。
- 计划模式发送走 `Chat` 时前端不再先把用户消息落库（后端 `ChatPlan` 已有 `query`，避免重复落库）；非计划路径保持现状。这是本次前端唯一的行为性改动，需在 `api/session.js` 注释里写清。

### D10 并发与取消

- `RetryPlan` 与 `ResumePlan` 都走 `runs.BeginExclusive(sessionID, planID)`，与普通发送、计划执行互斥；`ByPlan` 已存在时返回「正在执行」。
- 取消语义不变：执行中取消 → 当前步骤按实际结果落状态、剩余 `skipped`、计划 `cancelled`；取消后仍可 `ResumePlan`。

## 数据契约变更

```go
// Plan（新增字段，全部 omitempty 兼容旧文件）
Query    string `json:"query,omitempty"`
Error    string `json:"error,omitempty"`
Revision int    `json:"revision,omitempty"`

// PlanStep
Attempt     int `json:"attempt,omitempty"`
ContextMark int `json:"contextMark,omitempty"`

// Message
PlanStepIndex int `json:"planStepIndex,omitempty"`
Attempt       int `json:"attempt,omitempty"`
```

新增常量：

```go
PlanPlanning     = "planning"
PlanPlanFailed   = "plan_failed"
PlanInterrupted  = "interrupted"

StepInterrupted  = "interrupted"
```

绑定变更（需 `wails generate module`）：

| 方法 | 变更 |
| --- | --- |
| `SetSessionPlanMode(sessionID, enabled) (*Session, error)` | 新增 |
| `RetryPlan(planID, useStream) (*ChatResult, error)` | 新增（复用 `Plan.Query` 重规划；`useStream` 只影响「无需计划」直答是否流式） |
| `ResumePlan(planID, useStream) (*ChatResult, error)` | 新增（从断点续跑） |
| `GetSessionPlan(sessionID) *Plan` | 语义扩展：读取时惰性对账 `running` |
| `ExecutePlan(planID, useStream) *ChatResult` | 状态校验收窄为 `awaiting_approval` |

## 关键实现骨架

`executePlan` 拆成「一次执行回合」（可被 `ExecutePlan`/`ResumePlan` 共用）：

```go
// runPlanPass 从第一个非 done 步骤开始执行；返回时计划一定落在终态或 interrupted。
// resume=false 时要求 plan.Status == awaiting_approval（首次执行）。
func (a *App) runPlanPass(plan *store.Plan, useStream, resume bool) *ChatResult {
    // 1. 互斥登记 run（与普通聊天共用）
    // 2. plan.Status = running; Revision++; Save; emit
    // 3. defer 兜底：仍在 running → interrupted（不再 failed）
    // 4. for step := range plan.Steps {
    //       if step.Status == done { continue }
    //       if cancel { cancelled + MarkRemainingSkipped; return }
    //       step.Status, Attempt, ContextMark = running, step.Attempt+1, len(session.Messages)
    //       ensureStepRequestMessage(plan, step)   // 幂等：同 PlanStepIndex 不重复写
    //       for try := 0; ; try++ {
    //           res := runToolLoop(...)            // compact=true
    //           if res.Error == "" || !llm.Retryable(res.ErrorKind) || try >= planStepAutoRetries { break }
    //           if cancel { ... } ; sleepBackoff(try)
    //       }
    //       if cancel { ... }
    //       if res.Error != "" { step=failed; plan=failed（剩余保持 pending）; return }
    //       step=done + Summary
    //   }
    // 5. plan.Status = completed
}
```

`buildRunMessages` 过滤（仅当消息带 `PlanStepIndex` 时生效）：

```go
// 同一步骤的多次尝试：只保留最后一次（模型不该看到自己上次失败的过程，
// 那既浪费上下文，也会让模型重复执行已产生的副作用）。
func latestAttemptMessages(msgs []store.Message) []store.Message
```

`loadAll` 恢复：

```go
if p.Status == PlanRunning {
    p.Status = PlanInterrupted
    for _, st := range p.Steps {
        if st.Status == StepRunning {
            st.Status, st.Error = StepInterrupted, "应用重启，执行中断"
        }
    }
    _ = s.saveLocked(&p) // 立刻落盘，避免二次重启又回到 running
}
```

## Risks / Trade-offs

- **粘性开关带来额外规划调用**：每条消息都会多一次规划器请求。→ `needPlan=false` 直答 + 规划器用低温短提示；开关在工具栏上显式可见，用户可随时关。若观测到成本问题，再加「本会话内粘性 N 分钟」策略（不改数据结构）。
- **自动重试可能重复副作用**（工具已写文件）：引擎没有事务。→ 重试只发生在「步骤内、模型调用失败」时；重试时把上次尝试的工具摘要注入 system prompt，要求模型先对账再动手；已知限制写进文档，不做假装幂等。
- **请求期过滤让「历史」与「上下文」不一致**：审计在库里、模型只看最后一次。→ 过滤逻辑集中在 `buildRunMessages` 一处并写明注释；`viewMode` 下仍可展开完整尝试。
- **Revision 只解决乱序，不解决丢失**：→ 靠 `GetSessionPlan` 惰性对账 + 打开面板拉全量兜底。
- **`interrupted` 计划可能堆积**：→ 会话切换到该会话时提示一条；计划面板提供「忽略/取消计划」（转 `cancelled`），不自动清理文件。
- **前端首次改动（计划模式发送不再先落用户消息）影响面**：→ 只改计划分支；普通分支保持原逻辑；补齐 mock 与浏览器端验证。
- **旧前端 + 新后端**（回滚场景）：新状态值 `planning/plan_failed/interrupted` 在旧 `PlanPane` 会落到 default 文案。→ `plan.js` 已有 `STATUS_META` 缺失兜底；回滚只回退前端时后端字段被忽略，不破坏数据。

## Migration Plan

1. 后端：结构与 Store/执行器（可独立上线，行为对旧前端不变；`ExecutePlan` 收起 `failed` 校验时保留一个兼容分支：`failed` 且 `resume` 未标记时提示改用 `ResumePlan`）。
2. 绑定重生成 `wails generate module`，前端接新字段与新按钮。
3. 打开「计划模式粘性」开关：先在一个会话上验证，再默认启用（无设置项，默认即新行为）。
4. 回滚：前端回退到组件 `ref` 实现即可（后端新字段闲置，数据无副作用）；若需回退后端，`planning/plan_failed/interrupted` 记录在旧代码里表现为未知状态（`ExecutePlan` 会报「状态不允许执行」），需手工把这些计划改为 `awaiting_approval` 或删除——此风险由「回滚只回前端」的策略规避。

## Open Questions

- 步骤自动重试次数是否暴露为设置项（先固定 2 次，观察一段时间再决定）。
- 是否给「计划模式」加全局默认值/新会话继承（本轮不做，粘性只在会话内）。
