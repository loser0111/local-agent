# 会话级事件与状态隔离（多会话并行）技术方案

> 目标：让"模型还在跑 → 切到另一个会话接着干活"这件事在**状态展示上**成立：
> 每个会话的进行中状态、流式文本、工具卡片、diff、计划、待应答弹窗，都只跟着自己的会话走。
>
> 本文是**诊断 + 设计 + 实施记录**三位一体。第 1 节先把"问题到底出在哪"钉死（含对最初判断的修正），
> 第 2 节是三条候选路线的对比与选型，第 3 节是架构与不变式，第 4 节逐文件列接线点，
> 第 5 节写清取舍与未做的事。动手改代码前先看第 3 节的不变式——每条都对应一类具体的坑。

---

## 1. 问题诊断

### 1.1 最初的说法，哪些成立、哪些要修正

原始描述：

> 前后端在向前端推送事件时，以及按钮状态等等都是针对其中一个会话展示的……
> 目前前后端交互上好像并没有处理推送的事件是属于哪个 session 的，
> 导致前端切换会话之后，会显示另一个会话的状态。

**成立的部分**（这才是问题的要害）：

1. **事件载荷里没有任何会话标识**。`ChatEvent`（`chat.go`）只有 `type/toolCall/reply/diff/turn/plan/...`，
   `diff:update` 与 `subagent:event` 同样如此。所有事件都往**进程级**通道
   （`chat:event` / `diff:update`）上广播，谁在跑、跑的是哪个会话，前端无从判断。
2. **前端的"当前会话视图状态"是一个单槽位**：`stores/chat.js` 只有一份 `messages` 与一个全局
   `isGenerating`；`stores/diff.js` 一份 `turns`；`stores/plan.js` 一个 `plan`；
   `stores/permissions.js` / `ask.js` 各一个 `pending`。它们描述的都是"我眼睛现在看的那个会话"，
   而后端推来的却是"某个正在跑的会话"——两者一旦不是同一个，就串台。

**需要修正的部分**（如果按"全都要改成会话级"去理解，会把工作量估大、也会改坏已有设计）：

3. **后端早就是按会话键控的**：`runRegistry` 以 runID 为主键、另有 `sessionID → runID` 索引
   （`runcontrol.go`）；`permissionBroker` / `askBroker` 的 pending 表按 sessionID 分组；
   `DiffService` 的基线、归因、checkpoint、文件改动日志、子代理会话全部按 sessionID 键控。
   所以后端**并不存在"全局当前会话"**，多会话并行在它这一侧本来就成立。
4. **前端也有一部分已经是会话级的**：`stores/pane.js`（面板布局按 sessionId 存）、
   `stores/subagent.js`（按 parentId 过滤）、`diffStore.load(sessionId)` 的取数口径。
   真正缺归属的是**事件传输**与**chat / diff / plan / 权限 四类视图状态**。

### 1.2 具体会看到什么（按严重程度排序）

| # | 场景 | 现象 | 根因 |
| --- | --- | --- | --- |
| **B1** | A 会话跑到一半切到 B，A 跑完 | **A 的回复被追加进 B 的消息列表**，B 里凭空多出不属于它的对话 | `ChatPane.sendMessage` 第 4/5 步用 `chatStore.messages.findIndex(...)` 和 `addLocalMessage(...)` 操作"当前会话"，而这里的"当前"在 await 期间已经变了 |
| **B2** | A 在跑，切到 B | B 的发送按钮被禁用、出现"停止"按钮、气泡转圈 | `chatStore.isGenerating` 是全局单值 |
| **B3** | A 在跑，切到 B 点"停止" | 后端**没停**（`StopChat(B)` 未命中），前端却复位了 `isGenerating` → 界面显示"已停止"，A 其实还在改文件 | 同上 + `stopGeneration` 用 `sessionId.value` |
| **B4** | A 触发 diff，切到 B | B 的差异面板里冒出 A 改的文件，甚至多出一个"第 N 轮" | `diff:update` 无归属，`diffStore.applyUpdate` 写进当前桶 |
| **B5** | A 产出/更新计划，切到 B | B 的计划面板显示 A 的计划 | `plan_update` 无归属，`planStore.plan` 单槽位 |
| **B6** | A 弹授权请求，切到 B | 弹窗出现在 B 上；回答后 B 里那张"等待授权"的卡片没有被标记（`markPendingToolCard` 扫的是当前会话的消息） | `user:interaction` 载荷虽有 sessionId，但 store 只留一个 pending、卡片标记扫当前会话 |
| **B7** | 两条会话**同时**在跑（本次要启用的用法） | 后启动的那条收不到任何事件，工具卡片永远停在"运行中"，直到超时 | `api/session.js` 的 `chat()` **按调用**注册 `EventsOn('chat:event')`，而 `finally` 里的 `EventsOff('chat:event')` 会移除该事件的**全部**监听（Wails 的语义）——先跑完的那条把另一条的监听一起摘了。同一个坑在 `user:interaction` 那轮已经踩过一次并留下了注释，`chat:event` 这条路**当时没一起改** |
| **B8** | A 弹提问、切到 B | 提问弹窗被 B 的"挂起请求轮询"覆盖/清掉；切回 A 才补回来 | 轮询 `/GetPendingInteraction(currentSessionId)` 只查当前会话，`askStore.loadPending` 会清掉非当前会话的 pending |

**B7 是"要不要做多会话并行"的分水岭**：不做它，其他都改了也跑不起来；做了它，
B1–B6 就从"偶发串台"变成"必然串台"，所以必须一起改。

### 1.3 一个必须先回答的问题：后端扛得住两条并行吗

扛得住，但有一处需要补：

- **不同会话并行**：安全。会话 JSON 各写各的文件；diff 归因、checkpoint、文件改动日志、
  附件、子代理都已按 sessionID 分组。
- **同一会话被并发启动两次**：**不安全，且当前没有拦截**。`runRegistry.begin` 对同一个
  sessionID 会直接覆盖 `sessionRun[sessionID]` 索引——旧那条运行还在跑，但**再也停不掉了**
  （`StopChat` 只能命中新的那条），而且两条运行会同时往同一个会话文件里追加消息。
  之前靠"前端全局禁用发送"间接避免了，一旦放开并行就必须在**后端**加互斥（前端的状态永远只是提示）。
- **同一个工作区目录下两个会话同时跑**：diff 归因可能多算（`DiffService.NoteActivity` 的注释里
  已写明这是已知取舍：偏向多算，面板不会漏显示）。本次**不改**，只记录。

---

## 2. 方案选型

### 2.1 事件归属怎么带

| 方案 | 做法 | 评价 |
| --- | --- | --- |
| A. 每条通道一个"当前会话" | 后端记住"当前会话"，只给它的运行推事件 | ❌ 后端本来就没有当前会话的概念，为了前端视图引入一个后端全局态，方向就是错的 |
| B. 换通道名，每会话一条通道 | `chat:event:<sessionId>` 动态订阅 | ❌ Wails 的 `EventsOff(name)` 是按名移除全部监听，动态名会把"订阅生命周期"问题扩散到每条通道；且会话数量无上限 |
| **C. 载荷带归属 + 进程级单订阅 + 前端路由** ✅ | 事件里加 `sessionId`（与 `runId`）；前端**只订阅一次**，按 `sessionId` 分发给对应会话的处理器与状态桶 | ✅ 与 `user:interaction` 那一轮已经验证过的结论一致（"这类请求必须由根部订阅一次"）；改动集中在两个函数里，新增调用入口不会漏 |

**选型：C。**

### 2.2 前端状态怎么分桶

| 方案 | 做法 | 评价 |
| --- | --- | --- |
| A. 保持单槽位，切换时丢弃/重放 | 切走就丢，切回来重新拉 | ❌ 后台会话的流式文本、工具卡片进度全丢，切回来只看到"过去某个时刻的快照"，还要等下一次事件才续上；正在流的内容会缺一段 |
| **B. store 内部按会话分桶，对外保留"当前会话"视图** ✅ | `messagesBySession` / `generatingBySession` / `turnsBySession` / `planBySession` / `pendingBySession`，再用 computed 暴露当前会话那一份 | ✅ 组件与模板几乎不用改（仍读 `chatStore.messages`）；切回来立刻看到完整现场（流式消息还在内存里继续长）；写操作显式带 sessionId，杜绝"写到当前会话" |
| C. 每个会话一份 store 实例 | `useChatStore(id)` | ❌ Pinia setup store 不支持参数化实例，得自己写工厂 + 全局注册表，成本高于收益 |

**选型：B。** 关键约束：**所有写操作必须以显式 sessionId 为参数**。这就是 B1/B4/B5 的根因所在——
让"隐式写当前会话"在 API 上不再可能。

### 2.3 并行要不要真的放开

放开。`sendMessage` 的守卫从"全局 isGenerating"改成"**本会话**是否有运行"，
于是 A 在跑时 B 可以照常发送。配套：
- 后端同会话互斥（`beginExclusive`），重复发送得到明确报错而不是静默覆盖；
- 会话列表上显示"运行中 / 等待应答"，让用户知道后台还有几条在跑（否则并行等于隐形）。

---

## 3. 架构与不变式

### 3.1 数据流

```
                    ┌──────────────── 后端 ────────────────┐
runToolLoop ──► ar.Recorder (appRecorder / subagentRecorder)
                    │  在这里**唯一一次**盖上 sessionId / runId
                    ▼
        chat:event / diff:update / subagent:event / user:interaction
                    │  （进程级通道，载荷自带归属）
                    ▼
                    ┌──────────────── 前端 ────────────────┐
        api/eventbus.js：进程级只订阅一次 + 按 sessionId 路由
                    │
        ┌───────────┼───────────┬───────────┬──────────────┐
        ▼           ▼           ▼           ▼              ▼
   chatStore   diffStore   planStore  permission/ask   subagentStore
  messagesBySession ...（全部按 sessionId 分桶）
        │
        ▼  computed：当前会话那一份
   ChatPane / DiffPane / PlanPane / 弹窗（模板与现在几乎一致）
```

### 3.2 不变式（改代码时先看这七条）

1. **凡是描述"某次运行"的事件，必须带 `sessionId`；`chat:event` 上不带 `sessionId` 的事件一律视为非法。**
   为了让它不可能被忘记，后端把出口收敛成两个盖章函数（`appRecorder.Emit` / `emitInteraction`），
   并把 `emitChatEvent` / `emitInteraction` 的**签名改成必须传 sessionID**——漏传是编译错误，不是运行期静默。
2. **前端只订阅一次事件通道。** 任何"按调用注册 + finally 退订"的写法都会连带摘掉别人的监听
   （Wails 的 `EventsOff(name)` 移除该名下的全部监听）。新增消费方一律走 `eventbus.js` 的注册表。
3. **store 的写操作必须显式传 sessionId。** 不提供"写到当前会话"的隐式重载——
   在 await 之后"当前会话"可能已经换了，这正是 B1 的成因。
4. **前端不做归属推断。** 事件里没有 sessionId（旧版后端）时，按"丢弃并记一条 warn"处理，
   而不是"当成当前会话"。**猜错的代价（把别人的消息写进这个会话）远大于丢一条事件的代价。**
5. **陈旧事件按 runId 丢弃。** 同一会话的两次运行之间，前后端过桥是异步的，
   上一轮的最后几条事件可能落在下一轮订阅之后。runId 单调递增（`run_<毫秒>_<序号>`），
   前端只接受"不早于已见过的最新 runId"的事件。
6. **同会话互斥在后端。** 前端按钮状态只是提示，不是保证。
7. **弹窗只对"当前会话"的挂起请求弹出；后台会话的挂起请求在会话列表上出标记。**
   两个会话同时等人应答是合法状态，不能互相顶掉（B6/B8）。

---

## 4. 改动清单（逐文件）

### 4.1 后端（Go）

| 文件 | 改动 |
| --- | --- |
| `chat.go` | `ChatEvent` 增 `sessionId` / `runId`；`emitChatEvent(sessionID, runID, ev)` 与 `emitInteraction(sessionID, ev)` **改签名**（强制每个调用点显式给归属）；`startDeltaFlusher(sessionID, runID)`；`emitPlanUpdate` 从 `plan.SessionID` 取；`executeChat` / `ChatPlan` / `runToolLoop` 入口改用 `beginExclusive`，冲突时返回可读错误 |
| `agentrun.go` | `appRecorder` 增 `sessionID` / `runID`，`Emit` / `EmitDiff` / `FlushStream` 三处盖章（**这是运行期事件的唯一出口**，运行内的十几处 `Recorder.Emit` 全部自动获得归属） |
| `runcontrol.go` | 新增 `beginExclusive`（同会话已有运行 → 返回错误，不覆盖索引）；新增 `activeSessions()` |
| `app.go` | `handleCompactCommand` / `handleContextStatCommand` 的 `done` 事件补归属；新增 `ListRunningSessions`（前端刷新后仍能标出"哪几条在跑"） |
| `permission_app.go` / `ask.go` | `emitPermissionRequest` / `AskUser` 的交互事件补归属（载荷里本来就有 sessionID，只是没写进事件） |
| `visiontest.go` | `/vision` 的 `done` 事件补归属；运行改用 `beginExclusive` |
| `docs/` | 本文 |

### 4.2 前端（JS/Vue）

| 文件 | 改动 |
| --- | --- |
| `api/eventbus.js`（新） | 进程级单订阅 + 注册表：`subscribeChat(sessionId, handlers)` 按会话路由、按 runId 丢陈旧事件。`diff:update` / `subagent:event` 不移入总线——它们的订阅本来就是一次性的（各只有一处消费者），载荷加上 sessionId 就够了（理由见 §5 第 6 条） |
| `api/session.js` | `chat()` 改为经 eventbus 订阅（不再 `EventsOn/Off`）；新增 `listRunningSessions()` |
| `api/plan.js` | `executePlan` 同上，参数补 `sessionId`（放在第一个参数位，漏传就是 `undefined` 会话，事件路由不出来） |
| `stores/chat.js` | `messagesBySession` / `generatingBySession` / `runTokenBySession` / `contextBySession`；写操作显式带 sessionId；`messages` / `isGenerating` / `contextStat` 改为当前会话的 computed；新增 `isGeneratingIn(sid)`、`runningSessionIds` |
| `stores/diff.js` | `turnsBySession` / `checkpointsBySession`；`applyUpdate` 读事件的 sessionId；computed 暴露当前会话 |
| `stores/plan.js` | `planBySession` / `executingBySession`；`applyUpdate(sessionId, p)`、`execute(sessionId, planId, ...)` |
| `stores/permissions.js` / `asks.js` | `pendingBySession` + 当前会话 computed；应答/跳过带 sessionId |
| `stores/subagent.js` | `runsByParent` 分桶（切回来不再空白） |
| `panes/ChatPane.vue` | `sendMessage` 全程用捕获的 `sid`；`isGenerating` → `isGeneratingIn(sid)`；`markPendingToolCard` 用请求自带的 sessionId；停止按钮按会话 |
| `panes/DiffPane.vue` / `PlanPane.vue` | 跟随 store 的 computed（改动很小） |
| `components/layout/SessionSidebar.vue` | 会话项上显示「运行中 / 等待应答」标记 |
| `wailsjs/go/main/*` | 手工同步 `ListRunningSessions` 绑定 |

---

## 5. 取舍与未做的事

1. **不做"同一会话内并行"**。一个会话同时只能有一次运行，这是设计决定：同一份会话文件、
   同一套 diff 基线与归因集合，两条并发的运行写进去只会互相破坏记录。用户想要并行 → 开新会话。
2. **不做后端到前端的"运行快照"**。前端刷新（`wails dev` 热更新 / 手动 reload）后，
   内存里的流式内容会丢；`ListRunningSessions` 只能让界面标出"这条在跑、可停止"，
   补不回已经流出去的那段文字。真要补得让后端把增量也落盘，成本与收益不匹配。
3. **同一工作区下两个会话并行时，diff 归因可能多算**（`DiffService` 已有的已知取舍），本次不动。
4. **弹窗策略是"当前会话 + 列表标记"**，不是"所有会话的挂起请求排队弹"。
   同时弹两个授权框在桌面端很容易误答（后端每个请求都有超时，误答的代价高）。
5. **`diff:update` 仍按会话过滤后写入该会话的桶**，不区分"这条事件属于哪一次运行"——
   diff 是幂等的全量更新，重复应用无害。
6. **`diff:update` / `subagent:event` 没有移入事件总线**，只给载荷加了归属。
   它们的订阅点各只有一处（DiffPane 所在组件、子代理面板的 store），不存在
   "按调用注册 / 按调用退订"的模式，也就没有 B7 那个坑。等出现第二个消费者再收编，
   现在收编只会多一层间接。**注意**：`onDiffUpdate` 的退订仍是 `EventsOff('diff:update')`
   （移除全部监听），新增消费者时必须先把它改掉。

---

## 6. 验证记录（本轮做了什么、没做什么）

**能跑的（沙箱内实际执行过）**

| 手段 | 结果 |
| --- | --- |
| Go 静态检查（`.tmp` 脚本，先对 `HEAD` 归档版本校准到零报错，再做负向自检） | 括号配平 / 未使用 import / 包级重复声明 / 指定结构体的字面量字段名 —— **94 个文件零报错**；负向自检（注入一个未使用的 import + 一个错的字段名）**两项都被报出** |
| 声明核对（按声明模式匹配，不用"全文搜名字"） | `beginExclusive` / `beginLocked` / `bySessionLocked` / `activeSessions` / `ListRunningSessions` / `startDeltaFlusher` / `emitChatEvent` / `emitInteraction` / `errSessionBusy` / `parseRunID` 均有声明；新增字段 `ChatEvent.SessionID/RunID`、`appRecorder.sessionID/runID` 有声明 |
| 字段读写点有界核对 | `r.sessionID` / `r.runID` / `ev.SessionID` / `ev.RunID` / `plan.SessionID` / `req.SessionID` 的全部出现点逐一比对了所属类型 |
| `@vue/compiler-sfc`（parse + compileScript + compileTemplate） | **34 个 `.vue` 全通过**（含改动的 4 个） |
| SCSS 编译（`compileStyleAsync` + `sass`） | 唯一改了样式的 `SessionSidebar.vue` 通过（新增的 `$color-*` 变量都能解析） |
| `node --check`（改动的 10 个 `.js`） | 全通过 |
| import/export 一致性（297 处具名 import） | 全部对上（唯一一条"找不到模块"是 mock 数据里的 `'./App.vue'` 字符串，非真实 import） |
| **事件路由 + chat store 行为测试**（Node 里直接跑 `eventbus.js` 与 `chat.js`，脱离 Wails 与组件） | **28 项断言全通过**：跨会话不串台、无归属丢弃、同会话陈旧 runId 丢弃、**并行会话的 runId 不互相误杀**、退订只退自己、各事件类型分发；消息/运行状态/占位替换/hydrate 全部按会话键控。**并做了负向自检**：把 `addLocalMessage`/`appendStreamContent` 退回"写当前会话"，5 条断言立刻失败 —— 说明这组测试真的在测这次修复的东西 |

**做不到的**

- **没有 Go 工具链**（`go build` / `go test` / `gofmt` 都不可用：装不上，也没有 `vendor/` 可以离线编译），
  所以 `beginExclusive` 那 4 个新测试**没有跑过**，Go 侧改动**未编译**。
- 静态检查挡不住**类型错误**（把 A 类型当 B 类型传、读了不存在的字段）。本轮改动面
  （后端 8 个文件 + 前端 12 个文件）不小，预期第一轮 `go build` 可能还会报 1–2 处。
- 没有真机验证多会话并行的实际观感（需要用户 `wails build` 后手工跑两条会话）。

**请用户补做的两步**

1. `gofmt -w . && go build ./... && go test ./...`，报错贴回来（历史上这一步总能捞到漏网之鱼）。
2. `wails build`（**不是**只跑 `npm run build`：`frontend/dist` 是在 Go 编译那一刻
   用 `go:embed` 烤进二进制的，只重建前端不会改变应用行为），然后按 §7 的场景表手工验一遍。

---

## 7. 手工验收场景（值得逐条走的）

| # | 操作 | 期望（改造后） | 改造前的表现 |
| --- | --- | --- | --- |
| 1 | A 发一条长任务，跑到一半切到 B | B 的发送按钮可用、不显示"停止"；侧栏 A 上有"运行中"标记 | B 被禁用、气泡转圈 |
| 2 | 保持切走状态，等 A 跑完 | A 的回复进 A；切回 A 能看到完整回复；**B 里不会多出任何消息** | A 的回复被追加进 B |
| 3 | A 在跑，切到 B 也发一条 | 两条同时跑，各自的流式内容只出现在各自的会话里 | 第二条会被前端拦住（或有则事件全乱） |
| 4 | A 在跑，切到 B 点"停止" | 停的是 B（B 若空闲则按钮不复位成"已停止"的假象） | 界面显示已停止，A 其实还在跑 |
| 5 | A 弹授权框时切到 B | 弹窗收起（A 的请求进列表标记），切回 A 弹窗还在 | 弹窗留在 B 上 |
| 6 | 两条会话同时跑，其中一条改文件 | 各自差异面板只显示自己的改动 | A 的改动出现在 B 的 diff 面板 |
| 7 | 同一会话连点两次发送 | 第二次被后端拒绝并给出「该会话已有正在进行的任务」 | 两条运行同时写同一份会话文件 |

