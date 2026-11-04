# Proposal

## Why

计划（Plan & Execute）模式的全部「状态」目前都活在最脆弱的两个地方：一是 `ChatPane.vue` 里一个内存 `ref`（计划开关），二是 `~/.local-agent/plans/*.json` 里那份「要么还没生成、要么已经判死」的计划文件。于是只要中途发生一次网络问题，用户开的计划模式就凭空消失：

- **规划阶段**：`App.ChatPlan` 在规划器 LLM 调用失败时直接返回错误，**不落任何计划记录**。用户消息已经入库，但「这条请求是要做计划的」这件事没有任何痕迹，用户只能把整段需求重打一遍再开一次开关。
- **执行阶段**：某一步的网络错误（`internal/llm` 的统一重试已重试 4 次仍失败）会把步骤置 `failed`、计划置 `failed`、剩余步骤全部置 `skipped`。恢复路径只有 `ReopenPlan`（语义是「改计划再批准」），没有「从断点接着跑」；而 `skipped` 还抹掉了「这些步骤其实一行都没跑」这个信息。
- **进程/界面重启**：`PlanStore.loadAll` 把所有 `running` 计划一刀切成 `failed`；前端 `planMode` 是组件内存态，刷新页面、切会话、重开应用即丢；事件丢失后也没有任何补偿，界面可能永远显示「执行中」。

对用户而言，这就是「计划模式执行模式丢失」——我明明开了计划模式，网络抖了一下，回来发现计划没了、执行也接不上，只能从头再来一遍。

## What Changes

- **计划模式从「单次意图」升级为「会话级持久状态」**：`Session` 新增 `planMode` 字段（含 `SessionPatch`、绑定 `SetSessionPlanMode`），前端开关读写会话状态而不是组件 `ref`；切会话、刷新、重开应用都保持；开关由用户显式关闭（粘性），不再在生成计划后自动复位。
- **规划失败可一键重试（意图留痕）**：新增计划状态 `planning` / `plan_failed`，`Plan` 新增 `Query`（原始请求）与 `Error`。`ChatPlan` 先落一条 `planning` 记录再调规划器：成功转 `awaiting_approval`，失败转 `plan_failed`，新增绑定 `RetryPlan(planID)` 复用 `Query` 重新规划，**不必重打需求、不再追加重复的用户消息**；平凡请求（`needPlan=false`）则丢弃该占位记录。
- **执行断点续跑**：新增 `PlanStatus = interrupted`、新增绑定 `ResumePlan(planID, useStream)`；失败/中断的计划直接「继续执行」，保留已完成步骤与摘要，从第一个未完成步骤接着跑，**不需要**先退回待审核再批准。失败时不再把剩余步骤标 `skipped`（保留 `pending`，因为一行都没跑）。
- **步骤级自动重试 + 错误分类**：`ChatResult` 增加 `ErrorKind`，`internal/llm` 暴露 `ClassifyError`（复用现有 `LLMHTTPError`/哨兵错误，不新增判定口径）；计划执行对网络/限流/5xx/超时类错误在**步骤内**自动重试（默认 2 次，退避 2s→8s），重试耗尽才判失败；认证类等不可重试错误立即失败并给出一句能看懂的提示。
- **步骤尝试幂等**：`PlanStep` 增加 `Attempt` / `ContextMark`，`Message` 增加 `PlanStepIndex` / `Attempt`；重试不重复写「步骤 user 消息」，构建请求时同一步骤只保留最后一次尝试的 assistant/tool 消息（不删库，确定性过滤），避免上下文膨胀与「同一步骤看起来跑了两遍」。
- **启动恢复不再一刀切**：`loadAll` 把 `running` 计划置为 `interrupted`（可续跑），运行的步骤置 `interrupted` 并记录原因；计划文件不丢进度。
- **事件补偿与乱序防护**：`Plan` 增加 `Revision`（每次落盘自增），前端只接受更高 `Revision` 的 `plan_update`；页面加载/重连后对「执行中的会话」重新 `GetSessionPlan` 拉全量；后端判定「前端认为在执行、实际没有活跃运行」时复位为 `interrupted`。`chat:event` 的 `plan_update` 携带 `RunID`，旧运行的事件不覆盖新终态。
- **BREAKING（内部接口，非用户可见）**：`ChatPlan` 的返回值语义变化——不再「失败即无产物」，而是返回带 `plan` 的失败结果；`ExecutePlan` 的状态校验放宽到 `failed`/`interrupted`（走续跑语义）；`PlanStore.MarkRemainingSkipped` 只在取消路径使用。

## Capabilities

### New Capabilities

- `session-plan-mode`: 计划模式的会话级状态——开关持久化与恢复、规划意图留痕、规划失败一键重试、平凡请求直答。
- `plan-execution-recovery`: 计划执行的韧性——断点续跑、步骤级自动重试与错误分类、步骤尝试幂等、启动恢复、事件补偿与乱序防护。

### Modified Capabilities

（无：本仓库刚接入 OpenSpec，`openspec/specs/` 下还没有既有能力，本次两个能力均为新增。）

## Impact

- **后端 Go**：`internal/store/plan.go`（状态机/字段/Revision/Resume/Retry 语义）、`internal/store/sessions.go`（`planMode` 字段与 patch）、`internal/app/chat.go`（`ChatPlan`/`executePlan` 重构、`ErrorKind`、事件携带 RunID/Revision）、`internal/app/app.go`（新绑定 `SetSessionPlanMode` / `RetryPlan` / `ResumePlan`，`GetSessionPlan` 语义扩展）、`internal/llm/llmretry.go`（导出 `ClassifyError`）、`internal/agent/runcontrol.go`（运行与计划的关联查询，供「是否真的在跑」判定）。
- **前端 Vue/JS**：`frontend/src/panes/ChatPane.vue`（开关改为会话状态 + 发送分支）、`frontend/src/stores/plan.js`（Revision 过滤、事件补偿、`resume`/`retry` 动作）、`frontend/src/stores/session.js`（`planMode` 字段透传）、`frontend/src/panes/PlanPane.vue`（`planning`/`plan_failed`/`interrupted` 三态与按钮）、`frontend/src/api/plan.js`（新绑定 + mock）。
- **Wails 绑定**：`frontend/wailsjs/go/app/App.{js,d.ts}` 必须 `wails generate module` 重新生成（新增 3 个方法、`Plan`/`PlanStep`/`Session` 结构体字段变化）。
- **数据兼容**：`~/.local-agent/plans/*.json` 与 `sessions/*.json` 只增字段，旧文件缺字段按零值读（`planMode=false`、`revision=0`），无需迁移；旧 `failed` 计划可直接走 `ResumePlan`。
- **测试**：`internal/app/plan_test.go` 增加「规划失败→重试」「执行失败→续跑」「启动恢复 interrupted」「步骤重试幂等」四组用例；前端 mock 补齐三态，保证纯 `vite dev` 下可验证。
