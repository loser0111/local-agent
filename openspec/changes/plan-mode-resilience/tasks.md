# Tasks

## 1. 数据契约与存储层

- [x] 1.1 `internal/store/plan.go`：新增状态常量 `PlanPlanning` / `PlanPlanFailed` / `PlanInterrupted`、`StepInterrupted`，`Plan` 增加 `Query`/`Error`/`Revision`，`PlanStep` 增加 `Attempt`/`ContextMark`；验证：`go test ./internal/store/...` 通过，旧 `plans/*.json` 反序列化不报错（补一条读取旧格式的用例）
- [x] 1.2 `PlanStore.saveLocked` 每次落盘自增 `Revision`，并补单测断言「连续 Save 后 Revision 严格递增，重新加载后保持」。验证：`go test ./internal/store -run Plan`
- [x] 1.3 `PlanStore.loadAll` 改为 `running → interrupted`（步骤 `running → interrupted` 并写原因）并立即落盘；`Reopen` 的注释与行为复核（`interrupted` 也应可退回待审核）。验证：更新 `internal/app/plan_test.go` 的启动恢复用例，断言状态与进度保留
- [x] 1.4 `internal/store/sessions.go`：`Session` 增加 `PlanMode bool`、`SessionPatch` 增加 `PlanMode *bool`，并在 Patch 应用处落值；验证：`go test ./internal/store -run Session`，含「旧会话无该字段按 false」用例
- [x] 1.5 `Message` 增加 `PlanStepIndex`/`Attempt`（`omitempty`），并补用例断言旧会话消息反序列化后两字段为 0。验证：`go test ./internal/store`

## 2. 错误分类与请求组装

- [x] 2.1 `internal/llm/llmretry.go` 导出 `ErrorKind` 与 `ClassifyError(err) ErrorKind`、`Retryable(kind) bool`（依据 `LLMHTTPError.Status` 与既有哨兵，不解析文案）。验证：新增 `llmretry_classify_test.go`，覆盖 401/429/500/网络错误/ctx 超时/未知错误
- [x] 2.2 `ChatResult` 增加 `ErrorKind`，`runToolLoop` 失败返回处填充（取消路径不填）。验证：在既有错误用例中断言 `ErrorKind` 取值
- [x] 2.3 `buildRunMessages` 增加「同一步骤仅保留最后一次尝试」的过滤（`latestAttemptMessages`），且仅在消息带 `PlanStepIndex` 时生效。验证：新增单测覆盖「两步 × 各两次尝试」「无步骤字段的普通消息不受影响」

## 3. 规划流程（会话级计划模式）

- [x] 3.1 `App.SetSessionPlanMode(sessionID, enabled) (*store.Session, error)`：校验会话存在、执行中拒绝切换（`runs.BySession` 非空且该运行是计划执行时返回可读错误）。验证：`internal/app/permission_test.go` 同级新增 `planmode_test.go` 用例
- [x] 3.2 `ChatPlan` 重构：先落 `planning` 记录（同会话未完成记录复用/覆盖）→ 调规划器 → 成功转 `awaiting_approval` / 失败转 `plan_failed` / `needPlan=false` 丢弃记录并直答。验证：`plan_test.go` 用 httptest 覆盖三条路径，断言 `GetSessionPlan` 结果与历史中用户消息条数
- [x] 3.3 `App.RetryPlan(planID) (*ChatResult, error)`：仅接受 `plan_failed`，复用 `Plan.Query` 重新规划、不追加用户消息、走 `BeginExclusive` 互斥。验证：单测断言「重试成功转 awaiting_approval」「再次失败仍为 plan_failed 且错误更新」「并发重试被拒绝」
- [x] 3.4 `App.Chat` 对 `Plan != nil` 的失败结果放行（不再转成 Go error），使前端能拿到失败计划对象。验证：单测断言「规划失败时返回 result 而非 error」

## 4. 执行器（续跑 + 自动重试 + 幂等）

- [x] 4.1 抽出 `runPlanPass(plan, useStream, resume)`，`ExecutePlan` 收窄为只接受 `awaiting_approval`。验证：`plan_test.go` 断言 `failed` 计划调用 `ExecutePlan` 被拒且提示改用续跑
- [x] 4.2 实现 `App.ResumePlan(planID, useStream)`：接受 `failed/interrupted/cancelled`，保留 `done` 步骤与其摘要，从第一个非 `done` 步骤执行；与普通发送互斥。验证：单测「失败后续跑只跑剩余步骤」「已完成步骤的 Summary/FinishedAt 不变」「连续两次续跑不重跑」
- [x] 4.3 失败路径不再 `MarkRemainingSkipped`：失败步骤 `failed`、其余保持 `pending`、计划 `failed`；取消路径保持 `cancelled + skipped`。验证：单测覆盖失败与取消两条路径的步骤状态
- [x] 4.4 步骤级自动重试：按 `ErrorKind` 判定可重试，最多 `1 + 2` 次，退避 2s/8s（可注入时钟/策略以便测试），每次重试前检查取消；重试时把上次尝试的工具摘要写进 system prompt。验证：httptest 模拟「前两次 500、第三次成功」，断言步骤 `done`、历史无重复步骤消息
- [x] 4.5 步骤幂等：`ensureStepRequestMessage`（同 `PlanStepIndex` 不重复写）+ `Attempt` 自增落盘。验证：单测断言重试后会话里该步骤只有一条 user 消息，且步骤 `Attempt` 递增
- [x] 4.6 兜底与恢复：`runPlanPass` 的 defer 在异常/提前返回时把计划收尾为 `interrupted`（而非 `failed`），并落盘。验证：单测模拟 panic 与提前返回两条路径

## 5. 启动恢复、惰性对账与事件

- [x] 5.1 `App.GetSessionPlan` 惰性对账：`running` 且 `runs.ByPlan == nil` → 转 `interrupted`、落盘、emit。验证：单测构造「磁盘上 running、注册表为空」的计划，调用后返回 `interrupted`
- [x] 5.2 `emitPlanUpdate` 携带 `RunID`；前端侧只需要 `Plan.Revision` 判定，但事件必须能区分运行归属。验证：单测断言事件字段（沿用现有事件断言写法）
- [x] 5.3 为上述行为补充 README/`docs/plan-execute-implementation-plan.md` 的状态机段落（新增 `planning`/`plan_failed`/`interrupted` 与续跑入口），并验证文档中的接口名与生成绑定一致（对照 `frontend/wailsjs/go/app/App.d.ts`）

## 6. 绑定与前端状态层

- [ ] 6.1 `wails generate module` 重新生成绑定，确认 `App.d.ts` 出现 `SetSessionPlanMode`/`RetryPlan`/`ResumePlan`，且 `Plan`/`PlanStep`/`Session` 含新字段。验证：检查生成文件内容并在 `wails dev` 下无 TS 报错
- [ ] 6.2 `frontend/src/api/plan.js`：新增 `retryPlan`/`resumePlan`/`setSessionPlanMode`（Wails + 浏览器 mock 双实现），mock 支持 `planning`/`plan_failed`/`interrupted` 三态。验证：纯 `vite dev` 下用 mock 走通三态
- [ ] 6.3 `frontend/src/stores/session.js`：透传 `planMode` 并提供 `planModeOf(sid)`/`setPlanMode(sid, v)`（乐观更新 + 失败回滚，参考 `touchSession` 写法）。验证：切换会话后开关保持；保存失败时回滚
- [ ] 6.4 `frontend/src/stores/plan.js`：`applyUpdate` 按 `revision` 过滤（旧数据 `0` 兜底）、新增 `retry()`/`resume()`、`loadForSession` 在面板打开与事件重连后调用。验证：单测式手动验证「旧事件不覆盖终态」「重连后拉取全量」

## 7. 前端界面

- [ ] 7.1 `ChatPane.vue`：`planMode` 改为会话状态派生 + 切换调后端 + 执行中禁用；生成计划后不再复位开关。验证：切换会话/刷新后开关保持；计划执行期间开关不可点
- [ ] 7.2 `ChatPane.vue`：计划模式发送不再先落用户消息（改由 `ChatPlan` 落 `Query`），失败消息附带「重试规划」按钮（调 `planStore.retry`）。验证：mock 下规划失败只出现一条用户消息，重试不追加
- [ ] 7.3 `PlanPane.vue`：补齐 `planning`（进行中 + 取消）、`plan_failed`（原因 + 重试规划）、`interrupted`（原因 + 继续执行）三态渲染；`failed` 提供「继续执行」与「编辑计划」两个入口。验证：mock 全状态机走查，文案与按钮行为一致
- [ ] 7.4 `frontend/src/types/index.js`：补充新增状态取值与字段的 JSDoc。验证：`npm run build` 或 eslint 无新增告警

## 8. 集成验证

- [ ] 8.1 端到端（httptest）：规划失败 → 重试 → 批准 → 第 2 步网络失败 → 续跑 → 完成，断言计划终态、步骤状态、会话消息条数、事件序列。验证：`go test ./internal/app -run Plan`
- [ ] 8.2 桌面端手测：计划模式发送 → 断网（或指向不可用端口）→ 规划失败 → 恢复网络重试 → 批准 → 中途断网 → 继续执行；记录结果到 change 的说明中。验证：手测通过且界面状态与磁盘 JSON 一致
- [ ] 8.3 回归：`go test ./...` 全绿；`gofmt -l` 对本次改动的 `.go` 文件无输出；`wails dev` 启动无绑定/类型错误。验证：命令输出为空或全绿

## Workflow follow-up

- 实现与验证完成后按项目评审要求归档该 change（`openspec archive plan-mode-resilience --yes`）。
- 归档后确认 `openspec/specs/session-plan-mode/` 与 `openspec/specs/plan-execution-recovery/` 已生成且 `openspec validate --all` 通过。
