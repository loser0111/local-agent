# desktop-plugin 实现计划（M-A + M-B）

> 本文是 `docs/desktop-plugin-design.md`（需求主文档）+ `docs/desktop-plugin-requirements.md`（附录）
> 在**本仓库现状**下的落地计划。范围已拍板，不再讨论。

## 0. 已拍板前提

| # | 决策 | 取值 |
|---|---|---|
| 1 | 代码落点 | **新建 `plugin/` 子包**（照 ADR-4），`main` 只实现 `Host` 并装配 |
| 2 | 交付范围 | **M-A 主干打通 + M-B 可信化**（SC-1 全链路 + 托盘/单实例/原子写/崩溃恢复/去重/DND/暂停/panic 隔离） |
| 3 | `outputfilename` | **保持 `wails-tmp`**，不改 `go.mod` module 名、不清理注册表；F3.6 通知来源名暂不达标 |

范围外（本计划不做，留待 M-C~M-E）：执行型任务与 Executor、watchdog 无响应告警、熔断、开机自启（F4.7，P1）、P1/P2 其余项。

## 1. 实现思路

### 1.1 分层与依赖方向

```
main (package main)                     plugin (package plugin)
  main.go    窗口/单实例/关窗隐藏/退出    host.go     Host 接口 + DTO（唯一依赖面）
  app.go     装配（追加 2 行）           task.go     数据模型 + 校验
  taskhost.go  appHost → 实现 plugin.Host task_host 桥   store.go    持久化（原子写/迁移/bak/broken）
  taskapp.go   App 导出方法（wails 绑定）  sched.go    调度循环（单 goroutine）
  panic 隔离包装                          schedule.go  纯函数 NextAfter/Preview/Cron
                                          policy.go    决策树（DND/暂停/去重/频率）
                                          notifier.go  通知组装与降级
                                          history.go   触发记录落盘
                                          plugin.go    生命周期 + 状态机单点驱动
                                          tray_windows.go / tray_stub.go
                                          clock.go     Clock 接口（测试注入）
```

**依赖是单向的：`main → plugin`。`plugin` 绝不 import `main`。**

`plugin` 拿不到 wails runtime，也不需要：所有对外副作用（通知、执行、显窗、事件推送、取版本）都走 `Host`。

### 1.2 对文档的一处必要扩展（已在代码注释中标注）

附录把 `Host` 定成 11 个方法，但 `task:event` 的**推送方**在子包方案下无从获得 wails ctx。因此 `Host` 增加 1 个方法：

```go
Emit(name string, payload any)   // 由 main 实现为 wailsRuntime.EventsEmit(a.ctx, name, payload)
```

`Clock` 不进 `Host`，走 `plugin.Options`（纯测试注入，非宿主能力）。

### 1.3 为什么 plugin 不需要 `hideConsoleWindow`

`procexec_test.go` 的 `TestEverySpawnHidesConsoleWindow` 只扫**根目录非递归**的非测试 `.go`。子包不受其覆盖，但 `plugin/` 里**没有任何 `os/exec` 调用**（托盘走 `x/sys/windows` 的 `Shell_NotifyIconW` 系统调用、自启走注册表），所以不存在"绕过该测试拉起隐藏窗口"的风险。新增的根目录文件 `taskhost.go` / `taskapp.go` 同样不拉起进程。**该测试无需修改。**

### 1.4 M-A / M-B 的分工

| 里程碑 | 内容 | 涉及文件 |
|---|---|---|
| **M-A** | 一次性/固定间隔/每日触发；任务 CRUD；`tasks.json` 落盘；单调度循环；Toast 通知；`task:event`；TasksPane 真实列表+表单+预览 | `host.go` `clock.go` `task.go` `schedule.go` `store.go` `sched.go` `notifier.go`(N1) `plugin.go` `taskhost.go` `taskapp.go` + 前端 4 个新/改文件 |
| **M-B** | 托盘常驻 + 关窗隐藏 + 单实例 + 退出确认；原子写；版本迁移 + `.bak`/`.broken`；崩溃恢复（残留 running → interrupted）；通知去重；DND 与全局暂停；panic 隔离；触发记录落盘 | `tray_windows.go` `tray_stub.go` `policy.go` `history.go` + `main.go` `/ `app.go` 改动 + `TaskSettings.vue` |

## 2. 文件清单

### 2.1 新增 — `plugin/`（子包，`import "wails-tmp/plugin"`）

| 文件 | 职责边界 | 关键符号 |
|---|---|---|
| `manifest.json` + `embed.go` | **插件描述文件**（清单）：身份 id/name/version、落地形态 kind=builtin、宿主接口版本、能力、数据文件、事件通道、所需宿主方法、文档指针。用 `go:embed` 内嵌进二进制 | 清单本体、`manifestJSON` |
| `manifest.go` | 清单的解析与**自检**：`LoadManifest()` + `Validate()`（必填项、kind 必须为 builtin、主版本兼容性、**hostMethods 须与 `Host` 接口反射结果逐一相符**） | `Manifest`、`LoadManifest`、`HostAPIVersion` |
| `host.go` | **只放接口与 DTO，零逻辑**。宿主能力与数据契约的唯一出口 | `Host`、`NotifyRequest`、`NotifyCategory`、`NotifyResponse`、`RevealPayload`、`RunRequest`、`RunResult`、`RunEvent`、`Options`、`Info` |
| `clock.go` | 时间获取抽象，隔断 `time.Now()` 以便注入 | `Clock`、`systemClock`、`Ticker` |
| `task.go` | 数据模型、默认值、字段级校验；`Trigger` 用判别字段+类型化指针（照 `ToolSource` 写法）；`Enabled` 与 `State` 严格分离；**不含 `nextFireAt` 字段** | `Task`、`Trigger`、`TaskState`、`Policy`、`NotifyConf`、`TaskFile`、`GlobalConfig`、`(*Task) Validate()` |
| `schedule.go` | **纯函数，不碰 IO、不碰 clock 实例**：算下次触发、算未来 3 次预览、cron 子集解析 | `NextAfter(tr, now, lastFired, firedCount)`、`Preview(tr, now, n)`、`ValidateTrigger` |
| `store.go` | `tasks.json` 读写：`RWMutex` + 整文件 JSON + **原子写**（同目录 temp → `Sync` → `rename`，`0o600`）+ 版本迁移 + `.bak` 首迁备份 + 损坏改名 `.broken` 后空集启动 | `TaskStore`、`NewTaskStore(dir)`、`Load`、`Save`、`migrate` |
| `sched.go` | **单 goroutine** 睡眠到"所有任务里最近的下次触发"；触发唤醒后回调；snooze 生成临时触发；**不用 per-task Ticker、不用每秒轮询**；时钟跳变后重算 | `Scheduler`、`Start`、`Stop`、`Wake`、`nearest()` |
| `policy.go` | 决策树（按序命中即停）：任务级覆盖 → 全局暂停 → L0仅记录 → 全屏静默 → DND → 频率上限合并 → 前台仅应用内 → Toast；通知去重台账 | `Decide`、`Decision`、`RateLimiter`、`Dedupe` |
| `notifier.go` | 6 类模板 N1–N6 组装、L0–L3 打断级别、内容规范（标题≤40/正文≤120、结论前置、失败必带原因）；以**发送结果**判定降级并做"每会话只提示一次" | `Notifier`、`Render`、`FallbackOnce` |
| `history.go` | `task-runs/<taskId>/<runId>.json` 落盘（`RunRecord`，`RunSessionID` 为指针）；崩溃恢复扫描 | `History`、`Append`、`ListByTask`、`RecoverRunning` |
| `plugin.go` | 生命周期与门面：`New(Options) → Start/Stop`；任务 CRUD（校验→落盘→重建定时器→`Emit`）；**状态机单点驱动并持久化，禁止前端推断**；panic 隔离（触发与通知路径 `recover`，异常只写历史） | `Plugin`、`New`、`Start`、`Stop`、`CreateTask`…`ListTasks` |
| `tray_windows.go` | 自研托盘：`Shell_NotifyIconW` + 自建 message-only window（**不依赖 Wails HWND**）；菜单项：打开窗口/暂停全部/快速新增/退出 | `trayStart`、`trayStop`、`traySetMenu` |
| `tray_stub.go` | 非 Windows 空实现，保证跨平台编译 | 同名函数 |
| `clock_test.go` `schedule_test.go` `store_test.go` `policy_test.go` `sched_test.go` `plugin_test.go` | 与被测文件同包同目录（照仓库惯例） | — |

**`plugin` 明确不做**：`os/exec`、任何网络请求、读 `localStorage`、直接 `time.Now()`（必须走 `Clock`）。

### 2.2 新增 — 根目录（`package main`）

| 文件 | 职责边界 |
|---|---|
| `taskhost.go` | `appHost` 结构实现 `plugin.Host`：把 `Notify` 接到 `runtime.SendNotification`、`RunAgent` 接到既有 `agentRun`+`runToolLoop`、`CancelRun` 接到 `runRegistry`/`runControl`、`Emit` 接到 `EventsEmit`、`BaseDir`/`AppVersion` 取 `App` 字段。**唯一做"桥接"的地方，不放业务逻辑** |
| `taskapp.go` | `func (a *App) TaskXxx(...)` 导出方法（F10.1 任务 CRUD/启停/立即执行/历史查询/全局配置），全部**薄委托**给 `a.taskPlugin`；供 Wails 自动绑定 |
| `taskapp_test.go` | 桥接层与导出方法的冒烟测试 |

### 2.3 修改 — 根目录

| 文件 | 改动 | 规模 |
|---|---|---|
| `main.go` | `OnStartup` 由 `app.startup` 改为包一层：`app.startup(ctx)` 后调 `startDesktopPlugin(app)`。**不动 `Title`/`Bind`** | +6 行 |
| `taskbootstrap.go`（新增，见 2.2.1） | 插件装配与进程级实例持有 | 新文件 |

**相对原计划的偏差（已实测原因）**：`app.go` 不在本次改动清单里 —— 它是 **CRLF 行尾**文件，多行精确替换无法落地。为把对既有文件的侵入降到最低，装配收敛到新文件 `taskbootstrap.go`，并由 `main.go`（**LF 行尾**，可精确编辑）的 `OnStartup` 调用。插件实例以包级变量持有（插件本质是进程级单例），`App` 结构体保持不变。

#### 2.2.1 `taskbootstrap.go`（新增）

`startDesktopPlugin(app)`：`plugin.New`（内嵌清单解析 + 兼容性校验）→ `plugin.Start`（注册通知分类与回调、订阅运行事件、广播 `task:plugin:ready`）→ 记录 `[desktop-plugin] 已注册: ...`。任一步失败只记录并留 nil，**插件异常不影响主程序其余功能**。`currentDesktopPlugin()` / `stopDesktopPlugin()` 供导出方法与退出流程使用。

`wails.json` / `go.mod` 的 `module` 名 / `main.go` 的 `Title` **均不改**。

### 2.4 新增 — 前端

| 文件 | 职责 |
|---|---|
| `frontend/src/api/task.js` | 从 `@/../wailsjs/go/main/App` 导入绑定、从 `@/../wailsjs/runtime/runtime` 导入 `EventsOn/EventsOff`；订阅 `task:event`；带 `isWails()` + localStorage mock 以支持浏览器 `vite dev`（照 `api/plan.js` 写法） |
| `frontend/src/stores/task.js` | Pinia store：任务列表/详情/历史/全局配置/预览；`task:event` 增量更新（**不本地推断状态**） |
| `frontend/src/components/business/TaskFormDialog.vue` | 创建/编辑表单：类型选择、时间规则编辑器、**保存前展示未来 3 次触发时间**、校验不通过不可存 |
| `frontend/src/components/business/TaskSettings.vue` | 设置页「定时任务」tab 内容：全局配置（默认 snooze、DND 时段、全局暂停、删除保留历史、并发上限等） |

### 2.5 修改 — 前端

| 文件 | 改动 |
|---|---|
| `frontend/src/panes/TasksPane.vue` | **整文件重写**（现为 95 行静态假数据）：真实列表（名称/下次触发/上次结果/启用态）+ 编辑/删除（二次确认）/启停 + 历史查看 |
| `frontend/src/views/Settings.vue` | `tabs` 数组加 `{ key: 'task', label: '定时任务' }`，内容渲染 `TaskSettings.vue` |
| `frontend/src/types/index.js` | 补 `Task`/`Trigger`/`TaskState`/`RunRecord`/`GlobalConfig` 的 typedef（已有 `tasks` 的 pane 类型定义，不改） |

`components/layout/PaneContainer.vue` 已注册 `tasks: TasksPane`，**无需改**。

### 2.6 生成物（改完 Go 后用 `wails build`/`wails dev` 重新生成，**不手改**）

`frontend/wailsjs/go/main/App.js`、`App.d.ts`、`frontend/wailsjs/go/models.ts`（后者会新增 `plugin` 命名空间）。

## 3. 依赖变更

**不新增任何第三方依赖，不需要联网。** 唯一动作是在 `go.mod` 的 `require` 直接块中**把 `golang.org/x/sys v0.46.0` 从间接提升为直接**：

- 已实测存在于 `GOMODCACHE`（`C:\Users\wonyking\go\pkg\mod\golang.org\x\sys@v0.46.0`），当前经 wails 间接引入；
- 托盘只用 `x/sys/windows`（`Shell_NotifyIconW`、`NOTIFYICONDATA`、`CreateWindowExW`）；
- 托盘/自启库（systray 等）与 cron 库**均不在 module cache**，本计划不引入 —— 调度器自研（`schedule.go` 纯函数），cron 只支持子集；
- 持久化继续用 `encoding/json`，不引入 sqlite。

退路：若 W0 spike 证明托盘在 Wails 进程内做不出来，则 M-B 降级为「无托盘 + 关窗最小化」，并在 UI 明示——**不触发联网引依赖**。

## 4. 验证方式（对应第 7 步）

| 层次 | 做法 |
|---|---|
| 编译 | `go build ./...`（含 `plugin`）+ 非 Windows 走 `*_stub.go` 保证跨平台编译 |
| 单测 | `go test ./plugin/`：`NextAfter` 边界（月末/闰年/跨年/多选星期/Until/MaxFires）、决策树表驱动、store 原子写与损坏恢复、调度器注入时钟、去重与频率上限 |
| 既有回归 | `go test .` 确认既有用例结果与改动前逐项一致（不改动其逻辑） |
| 前端 | `npm run build`（`node_modules` 已存在，离线可跑） |
| 打包态 | `wails build` 出真实 exe 后验证「设 1 分钟后提醒」全链路（L4，非 `wails dev`） |
| 场景 | 关窗隐藏后仍触发、重启后任务不丢、重复通知为 0、坏文件改名 `.broken` 后空集启动 |

## 5. 实施进度（截至核心功能完成）

### 已完成

| 模块 | 文件 | 说明 |
|---|---|---|
| 清单与入口 | `plugin/manifest.json`、`embed.go`、`manifest.go`、`host.go`、`clock.go`、`plugin.go` | 内嵌清单 + 自检（hostMethods 与接口反射比对）、`New→Start→Stop` 生命周期 |
| 数据模型 | `plugin/task.go` | `Task`/`Trigger`/`TaskState`/`Policy`/`GlobalConfig`；`Enabled` 与 `State` 分离；`nextFireAt` 不落盘的物理保证；`TaskView` 字段漂移护栏（见 `task_test.go`） |
| 调度计算 | `plugin/schedule.go` | 纯函数 `NextAfter`/`MissedBetween`/`Preview`/`ValidateTrigger` + cron 五段子集 |
| 持久化 | `plugin/store.go` | 原子写（temp→Sync→rename，`0o600`）、版本迁移、`.bak` 首迁备份、`.broken` 损坏保留、未知顶层字段兜底 |
| 调度循环 | `plugin/sched.go` | 单 goroutine、睡到最近触发时刻、**禁止 per-task Ticker 与每秒轮询**、已到点/积压走 `minOverdueWait` 快速通道、时钟跳变鲁棒 |
| 决策与限流 | `plugin/policy.go` | 决策树（命中即停，含全屏/前台维度）、持久化去重台账、加锁的滑动窗口限流 |
| 通知 | `plugin/notifier.go` | N1–N6 模板、7 个白名单占位符（未知占位符原样保留）、长度与格式约束、被动降级 |
| 历史 | `plugin/history.go` | `task-runs/<taskId>/<runId>.json`、崩溃恢复、保留策略清理、路径片段消毒 |
| 任务服务 | `plugin/service.go` | CRUD 流水线（校验→落盘→重算→唤醒→广播）、snooze、错过补偿三策略、熔断、panic 隔离 |
| 桥接与导出 | `taskhost.go`、`taskbootstrap.go`、`taskapp.go` | `plugin.Host` 实现、装配、18 个导出方法（已生成 wailsjs 绑定） |
| 前端 | `api/task.js`、`stores/task.js`、`panes/TasksPane.vue`、`components/business/TaskFormDialog.vue`、`components/business/TaskSettings.vue`、`types/task.js`、`views/Settings.vue` | 真实列表/表单/预览/历史、设置页新 tab、独立事件通道订阅 |

### 验证结果（实测，第 7 步）

| 项 | 命令 | 结果 |
|---|---|---|
| 编译 | `go build ./...` | ✅ exit 0 |
| 静态检查 | `go vet ./plugin/` | ✅ 干净 |
| 跨平台 | `GOOS=darwin/linux go build ./...` | ✅ 均通过（`plugin` 只用标准库，无需 stub） |
| 单元/集成 | `go test ./plugin/` | ✅ **54 个用例全绿** |
| 并发安全 | `go test ./plugin/ -race` | ✅ 无数据竞争 |
| 端到端 | 根包 `TestDesktopPluginEndToEndWithRealBootstrap` | ✅ 真实装配→真实调度循环在 ~1.24s 内触发并落盘历史 |
| 前端 | `npm run build` | ✅ 1868 模块，2.28s |
| 打包态（L4） | `wails build` | ✅ 产出真实 `build/bin/wails-tmp.exe`，无 "Not found" 警告 |
| 既有回归 | 根包全量（隔离副本，绕过既有的 `ask_test.go` 编译错误） | ✅ 与改动前**逐项一致**（同样 4 个既有失败用例） |
| 零新依赖 | `git diff --stat go.mod go.sum` | ✅ 无改动 |
| 零网络/零子进程 | `grep net\|os/exec\|x/sys plugin/*.go` | ✅ 仅标准库 |

### 验收标准逐条核对（S1–S6）

| # | 标准 | 状态 | 依据 |
|---|---|---|---|
| S1 | 不漏提醒（漏报=0） | 部分验证 | `TestSchedulerFiresAtDueTimeNotBefore`（不提前）、`TestSchedulerSurvivesClockJumpForward`、`TestMissedCatchUpNotifiesOnce`、`TestMissedBetween`。**真实休眠/唤醒、系统时间前跳需在有桌面会话的机器上按 L5 复验** |
| S2 | 不重复不轰炸（重复=0） | ✅ | `TestFireIsIdempotentPerCycle`、`TestLongSleepDoesNotFireHundredsOfTimes`、去重台账随 `tasks.json` 落盘（重启后仍去重） |
| S3 | 关窗/重启/休眠唤醒各验一次 | 部分 | 重启：`TestCreateTaskPersistsWithoutDerivedField`、`TestStoreRecoversRunningStateOnLoad`；休眠唤醒：`TestSchedulerSurvivesClockJumpForward`、`TestLongSleepDoesNotFireHundredsOfTimes`。**关窗隐藏随托盘一并未实现** |
| S4 | 自动执行可追溯、可回滚、可控 | 部分 | 可追溯 ✅（历史落盘 + 降级原因 + 被拒操作留痕字段）；**执行链路与 checkpoint 回滚属 M-C/M-D，未实现** |
| S5 | 插件异常不影响主程序 | ✅ | `TestSchedulerIsolatesPanicInCallback`（panic 不打死调度循环）、`TestStartDesktopPluginSkipsSafely`、`TestAppHostImplementsHostContract`、e2e 末尾（不再可用时返回可读错误而非 panic） |
| S6 | 内存增量 ≤8MB、空闲 CPU <0.1% | 未验证 | 需真实进程采样。「不轮询」有 `TestSchedulerDoesNotPoll`（1 小时唤醒 ≤10 次）守住，`minOverdueWait` 防忙等 |

**验证层次**：L1 ✅ / L2 ✅ / L3 ✅（仅 v1，缺多版本 fixture）/ L4 ✅（打包成功 + e2e 链路；未在真实桌面会话点击验证）/ L5 部分（注入时钟覆盖休眠唤醒、改时间、坏文件、暂停；多实例与断网、全屏未接）/ L6 ❌ / L7 部分（路径消毒、模板白名单、动作幂等已做；注册表写入未涉及）/ L8 ✅

### 第 7 步发现并修复的缺陷

| # | 缺陷 | 影响 | 修法 |
|---|---|---|---|
| 1 | **调度器「已到点/积压」路径永不触发** | `nearest` 在有待处理项时返回 `now`，`wait == 0` 使 `timer` 为 **nil channel**——select 永久阻塞。等于「刚启动时的错过补偿」「时钟前跳后的追赶」全部静默失效 | 抽出 `minOverdueWait`，`wait<=0` 走快速通道而非长睡；同时避免 `After(0)` 忙等 |
| 2 | **`RateLimiter` 数据竞争** | 调度 goroutine（自动触发）与界面 goroutine（手动「立即执行」）并发读写同一 map | `RateLimiter` 加互斥锁 |
| 3 | **`FallbackNotice` 数据竞争** | 启动流程与调度循环并发改写 `notified` map | 加互斥锁 |
| 4 | **`Plugin.sched` 无锁写入** | 调度循环启动后界面线程立即建任务并 `wake()` 读它，构成竞争 | 赋值移入 `p.mu` 临界区 |
| 5 | **`TaskView` 内嵌导致绑定丢类型** | Wails TS 生成器对提升字段解析失败，产出 `state: any` 并每次构建打印 `Not found: plugin.TaskState`——最关键的字段失去类型 | `TaskView` 改为显式字段，并加反射测试钉死与 `Task` 的字段一致性 |

> 缺陷 1 只有真实 goroutine 测试能发现：service 层用例都直接调 `tick()`，绕过了循环本身。这也是本轮补上 `sched_test.go` 的直接原因。

### 尚未完成（明确留待下一轮）

| 项 | 归属 | 说明 |
|---|---|---|
| **托盘常驻**（F4.1/F4.2/F4.4） | M-B | 自研 `Shell_NotifyIconW` + message-only window，需真实 Windows 桌面环境验证消息循环与 Wails 主线程共存（文档列为首要待验证项）。**不做托盘就不应先做「关窗隐藏」**：窗口藏起来却没有找回入口等于把用户关在门外。 |
| **单实例锁**（F4.3） | M-B | 依赖托盘（二次启动需聚焦既有窗口）；且会影响 `wails dev` 调试习惯，需同时给出临时关闭开关。 |
| **执行型任务**（F5.x/SC-2/SC-5） | M-C/M-D | 目前执行型任务**明确记录为「未执行」并写清原因**，不假装成功。 |
| **运行心跳/无响应告警**（SC-4）、失败重试（F8.4）、开机自启（F4.7） | M-C/M-B | 契约已就位（`RunEvent`、`Policy.MaxRetries`、`RetryBackoffMinutes`），逻辑未接。 |
| 日志落盘与滚动、应用内提醒横幅组件 | P1/P2 | — |

## 6. 风险与对策

| 风险 | 对策 |
|---|---|
| **托盘消息循环与 Wails 主线程共存**（文档列为首要待验证项） | 下一轮动手前先做 spike；失败则降级「无托盘」，不联网引依赖 |
| 「关窗隐藏」在没有托盘时会把用户关在门外 | **托盘未落地前不启用关窗隐藏**（已按此执行） |
| 长时间休眠后一次性触发上百次 | `processDue` 只把「最近一个」当正常触发，更早的一律折进错过补偿（有测试守住） |
| `NextAfter` 只看未来导致「已到点」永远不被处理 | `nearest` 先用 `firstPending` 报告积压，调度器据此立刻处理 |
| 单实例锁影响既有调试习惯 | 与托盘一并实施，并同时提供临时关闭开关 |
| 子包打破「零子包」惯例、偏离部分复用意念 | 只让 `plugin` 依赖 `Host`；需要 main 内部能力时**优先扩展 `Host`**，不在子包里复制实现 |
| `TaskFile` 版本迁移写错导致用户数据丢失 | 首迁强制留 `tasks.json.bak`（不覆盖已存在）、损坏文件改名不删、迁移用例各版本 fixture 齐全 |
