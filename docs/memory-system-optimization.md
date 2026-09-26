# local-agent 记忆系统优化方案

> - 状态：**P0 已实施**（分支 `feature/memory-optimization`）；P1 / P2 待做
> - 对标：Claude Code `memdir/` + Session Memory + `CLAUDE.md`；本地分析见仓库根目录 `claude-code-memory-analysis.md`
> - 原则：保留 `memory.MemoryStore` 的工程能力，把 Claude Code 的**对话闭环**接进来；不换协议、不绑 1P API

---

## 一、目标与非目标

### 1.1 要解决什么

`memory/memorystore.go` 已经是一套可测的长期记忆库：四分类、user/project 作用域、PII 拦截、过期、相似合并、容量淘汰、词检索、相对龄与漂移提示。但它**没有调用方**——`App.startup` 不创建 store，`buildBasePrompt` / `runToolLoop` / `compactSession` 都不碰它。

结果是：跨会话知识进不了模型；模型也没有合法的写入入口；`/compact` 每次都从对话重新摘要，没有可复用的会话工作记忆。

Claude Code 的优势不在「有 index」，而在记忆**活在对话里**：索引常驻、按需召回正文、用户明说立刻写、回合结束兜底提取、会话记忆给压缩当廉价原料。本方案把这两套优点叠在一起。

### 1.2 目标

| 编号 | 目标 | 优先级 |
| --- | --- | --- |
| G1 | 把 `MemoryStore` 接到 `App` 与对话循环，用户/项目记忆能跨会话召回 | P0 |
| G2 | 渐进式披露：索引常驻系统提示，正文按本轮查询召回（对标 Skills L1/L2） | P0 |
| G3 | 模型可显式读写遗忘：`memory_search` / `memory_save` / `memory_forget` 直出 | P0 |
| G4 | 用户命令：`/remember`、`/forget`、`/memory`，以及本会话 `/ignore-memory` | P0 |
| G5 | 回合结束后台提取，与本轮显式写入互斥 | P1 |
| G6 | 工作区指令文件常驻（`AGENTS.md` 等），与自动记忆分开 | P1 |
| G7 | 会话工作记忆喂给现有压缩：有可用工作记忆时少打一次摘要 API | P1 |
| G8 | 设置页 / 面板可列出、删除、开关自动记忆 | P2 |

### 1.3 非目标（明确不抄）

- Anthropic Tool Search / `defer_loading` / `tool_reference`（绑 1P API，和记忆无关）
- 用 Sonnet 旁路选择记忆文件（P0/P1 继续用现有词检索；P2 才考虑可选重排）
- 让模型用 `write_file` / `edit_file` 直接改记忆目录（会绕过 PII、合并、淘汰）
- 团队记忆同步、夜间 `/dream` 蒸馏、KAIROS 日志
- 子代理独立记忆目录（子代理只读父项目记忆，不写 user 作用域）
- 把权限「本会话放行」和长期记忆混成一套（那是授权账本，不是知识）

---

## 二、两边各留什么

| 留下（local-agent） | 借来（Claude Code） | 不借 |
| --- | --- | --- |
| `index.json` + 主题文件，CRUD API | 记忆进入 `Chat` 闭环 | 纯 prompt 写文件、无 schema |
| PII / 过期 / 淘汰 / 相似合并 | 双轨写入（显式 + 提取，互斥） | 提取与显式双写同一段对话 |
| 词检索（零额外 LLM） | 索引常驻 + 正文按需 | 每轮同步打小模型做选择器 |
| 注入字符预算 | 已展示过的不再占名额；压缩后可再召回 | 把召回结果写进 `Messages`（污染原文） |
| `Source` 溯源到会话 | 「不要存能从代码推出来的东西」 | 把架构/目录结构当记忆 |
| 已有 `/compact` + `ContextSummary` | 会话工作记忆作为压缩第一级 | 再做一套并行压缩器 |

一句话：store 仍是唯一写入口；对话层只负责**何时检索、何时提取、注入哪一段**。

---

## 三、目标形态

四层，职责不重叠：

```
① 工作区指令     AGENTS.md / .local-agent.md     人写，每轮常驻系统提示
② 长期记忆       MemoryStore（跨会话）           机器写、按查询召回
③ 会话工作记忆   Session.WorkingMemory           本会话滚动状态，喂压缩
④ 原文           Session.Messages                永不因记忆/压缩而删除
```

数据流：

```
用户提交
  ├─ 内置命令 /remember /forget /memory /ignore-memory  → 直接落库或改开关，不走模型
  ├─ buildBasePrompt
  │    ├─ 人设 + 工作区 + 工具偏好 + 技能 L1
  │    ├─ ① 工作区指令（P1）
  │    └─ ② L1 记忆索引（id + description + 龄，有预算）
  ├─ 词检索本轮 query（同步；本地、毫秒级，不必预取）
  │    └─ 过滤 alreadySurfaced / 过期 / ignore
  ├─ 构建请求：ContextSummary 前缀 + 历史 + ② L2 召回块（不落盘）
  ├─ runToolLoop（模型可调 memory_save / search / forget）
  └─ 回合结束
       ├─ 本轮调过 memory_save → 跳过提取，推进游标
       └─ 否则 P1 提取 → AddMemory（仍走 store，PII 照拦）
```

压缩路径（接到已有 `contextmgmt.go`，不另起炉灶）：

```
将满
  ① 若 WorkingMemory 覆盖范围够用 → 当作 ContextSummary，不打摘要 API
  ② 否则走现有 compactSession（LLM 摘要 / 失败则 compactMessages）
  ③ 压缩成功后：清空 SurfacedMemoryIDs，允许再召回；索引仍在系统提示里
```

---

## 四、现状锚点（代码依据）

| 能力 | 现状 | 文件 |
| --- | --- | --- |
| 记忆库 | 完整、有测试、无调用方 | `memory/memorystore.go`、`memory/memorystore_test.go` |
| 注入 | 只拼 title+description，默认 800 字，正文不进 prompt | `FormatInjection` |
| 检索 | tag/标题/描述加权 + recency + importance | `Retrieve` |
| 对话组装 | 人设 + 工作区 + 技能 L1，无记忆段 | `chat.go` `buildBasePrompt` |
| 请求构建 | system +（可选 ContextSummary）+ 历史 | `contextmgmt.go` `buildRunMessages` |
| 压缩 | 阈值自动 + `/compact`，可逆 | `contextmgmt.go` `compactSession` |
| 命令拦截 | `/compact`、`/context-stat` 已在 `Chat` 最前 | `app.go` |
| 工具直出 | `directToolOrder`；提示词点名的必须在列 | `filetools.go` |
| 会话字段 | `Project`、`ContextSummary*` 已有 | `sessions.go` |
| App 装配 | `startup` 不创建 MemoryStore | `app.go` |

---

## 五、数据与配置

### 5.1 磁盘

```
~/.local-agent/memory/
├── config.json          # 已有 MemoryConfig
├── index.json           # 已有
├── user/                # ScopeUser 主题文件
└── project/<slug>/      # ScopeProject，slug = Session.Project，空则 "default"
```

目录权限保持 `0o700`。项目 slug **只来自会话配置**，不读仓库里的设置项去改记忆根目录——Claude Code 刻意排除 `projectSettings` 改 `autoMemoryDirectory`，同一条安全理由。

工作区指令文件只读、只加载，不进 MemoryStore：

```
<workspace>/AGENTS.md
<workspace>/.local-agent.md
<workspace>/LOCAL.md          # 可选，不提交的本地补充
```

多个都存在则按上面顺序拼接，单文件上限 40_000 字符（对标 Claude Code `MAX_MEMORY_CHARACTER_COUNT`），超了截断并在段末留一句提示。

### 5.2 会话上新增字段

只加记忆闭环需要的，压缩三字段不动。

```go
// Session 增补（sessions.go）
WorkingMemory        string   `json:"workingMemory,omitempty"`        // 本会话滚动工作记忆（markdown）
WorkingMemoryUpTo    int      `json:"workingMemoryUpTo,omitempty"`    // 已吸收到 Messages 的下标（不含）
SurfacedMemoryIDs    []string `json:"surfacedMemoryIDs,omitempty"`    // 本会话已注入正文的记忆 id
IgnoreMemory         bool     `json:"ignoreMemory,omitempty"`         // 本会话不注入、不提取
LastExtractMessageID string   `json:"lastExtractMessageID,omitempty"` // 提取游标
```

`Messages` 仍然是原文。召回块只在 `buildRunMessages` 时插入，**不** `AppendMessage`。这样压缩可逆、用户资产不被系统附件污染；`SurfacedMemoryIDs` 承担 Claude Code 扫附件得到的 `alreadySurfaced`。

压缩成功（`ContextCoveredUpTo` 前进）时清空 `SurfacedMemoryIDs`：旧召回已不在请求里，允许再召回。

### 5.3 配置（扩展已有 `MemoryConfig`）

```go
type MemoryConfig struct {
    // 已有
    MaxTopK           int
    MaxPerScope       int
    InjectionMaxChars int  // 改义：L1 索引预算，默认 800
    PIIFilter         bool
    StalenessCaveat   bool

    // 新增
    Enabled            bool `json:"enabled"`            // 总开关，默认 true
    ExtractEnabled     bool `json:"extractEnabled"`     // 回合结束提取，默认 true
    RecallMaxChars     int  `json:"recallMaxChars"`     // L2 正文预算，默认 8000
    RecallTopK         int  `json:"recallTopK"`         // 默认 5
    IndexMaxItems      int  `json:"indexMaxItems"`      // L1 最多列几条，默认 30
}
```

环境变量 `LOCAL_AGENT_DISABLE_AUTO_MEMORY=1` 与 `--bare` 同类开关（若以后有）优先于配置，关闭 L1/L2/提取，命令 `/memory` 仍可人工查看。

---

## 六、读路径（召回）

### 6.1 L1 索引常驻

对标 `MEMORY.md` 与 `BuildSkillIndex`：系统提示里只放路由信息。

```
## 长期记忆索引
跨会话偏好与项目知识。需要正文时用 memory_search，或等系统按本轮问题注入。
不要把能从当前仓库 grep/read 得到的东西再存一遍。
- [abc12] (user/feedback, yesterday) 不要在回复末尾复述 diff
- [de345] (project/foo, 12 days ago) staging 的 OAuth 回调曾挂过
```

规则：

- 空库则整段不出现（和技能 L1 一样）
- 按 importance × recency 截断到 `IndexMaxItems` / `InjectionMaxChars`
- `IgnoreMemory` 为真则整段省略，并写一句「用户要求本会话不使用记忆」
- 索引里的相对龄沿用 `relAge`（today / yesterday / N days ago），不要写裸 ISO

### 6.2 L2 本轮召回

用户消息提交后、第一次 `callLLM` 前：

```go
entries, _ := store.Retrieve(query, projectSlug, cfg.RecallTopK)
entries = dropSurfaced(entries, session.SurfacedMemoryIDs)
block := store.FormatRecall(entries, cfg.RecallMaxChars) // 新函数：带正文
```

词检索是本地 CPU，**同步即可**，不必抄 Claude Code 的 prefetch。预取只在 P2 上了 LLM 重排之后才有意义。

`FormatRecall` 与现有 `FormatInjection` 的差别：

| | `FormatInjection`（现） | `FormatRecall`（新） |
| --- | --- | --- |
| 内容 | title + description | 主题文件正文（已有 `buildEntry` 在读） |
| 预算 | 800 | `RecallMaxChars`（8000） |
| 超限 | 少显示几条 | 单条截断并提示可用 `memory_search(id)` 看全文 |
| 漂移 | 有一条就附 caveat | 仅当存在 `Stale` 的项目记忆时附 |

注入位置：`buildRunMessages` 在**最后一条 user 消息之后**追加一条 role=system 的召回块（或拼进 system prompt 的尾部，二选一，实现时锁定一种）。不要插进历史中间，以免打乱 tool_call 配对。

本轮成功注入的 id 写入 `SurfacedMemoryIDs` 并落盘。模型已经用 `memory_search` 读过的 id 同样记入，避免下一圈再灌一遍。

### 6.3 用户说「先别用记忆」

`/ignore-memory` 置 `IgnoreMemory=true`：L1/L2/提取全停，工具 `memory_search` 返回空并说明原因。`/remember` 仍允许——那是用户主动写入。再发 `/memory on` 恢复。

自然语言「不要用记忆」不在 P0 做 NLU；P1 提取提示词里加一条：若用户明确要求忽略，调用方应置位，而不是存一条「用户让我忘掉记忆」的记忆。

---

## 七、写路径

唯一入口：`MemoryStore.AddMemory` / `UpdateMemory` / `DeleteMemory`。工具、命令、提取、设置页都走它。PII、合并、淘汰、过期继续生效。

### 7.1 工具（P0，直出）

| 工具 | 作用 | 权限 |
| --- | --- | --- |
| `memory_search` | query 或 id → 条目 + 正文 | 只读，自动放行 |
| `memory_save` | title/content/type/scope/tags | 只写 `~/.local-agent/memory`，自动放行 |
| `memory_forget` | id 或检索词（命中 1 条才删，多条只列出） | 同 save |

三条都进 `directToolOrder`，并在 `buildBasePrompt` 的「工具使用偏好」里点名——与 `exec_shell` / `spawn_agent` 同一条纪律：提示词点名 ⊆ 直出。

`memory_save` 的 description 写清分类与排除：

- type：`user` / `feedback` / `project` / `reference`（已有常量）
- 要记：用户身份与偏好、被纠正或被确认的做法、**无法从当前仓库推出来的**项目事实
- 不记：代码模式、目录结构、git 历史、本会话 todolist、计划步骤（那些有 Plan / 会话原文）
- 相对日期改成绝对日期再存
- 先 `memory_search` 再决定是新写还是让 store 合并

工具实现放在主包 `memorytools.go`，内部调 `a.memory`。不要让 `write_file` 的 root 能指到记忆目录。

### 7.2 用户命令（P0）

与 `/compact` 一样在 `Chat` 最前拦截，优先于同名技能：

| 命令 | 行为 |
| --- | --- |
| `/remember …` | `AddMemory`（默认 `user` + 当前 project）；不调模型 |
| `/forget …` | 按 id 或检索删除；多条只列出让用户收窄 |
| `/memory` | 列出当前 user + 本项目，带龄 |
| `/ignore-memory` | 本会话关闭注入与提取 |
| `/memory on` | 恢复 |

### 7.3 回合结束提取（P1）

对标 `extractMemories`，但**写入走 API，不 fork 一个带 Write 工具的子代理**。

触发：`runToolLoop` 正常结束（有最终 assistant、未被取消）之后异步启动；不阻塞把 `ChatResult` 返回给前端。

跳过：

- `IgnoreMemory` 或总开关关闭
- 本轮已出现 `memory_save` / `/remember`（`hasMemoryWritesSince`：扫本轮 tool_calls 与命令）
- 距 `LastExtractMessageID` 没有新的用户可见消息
- 子代理会话（`ParentID != ""`）——子代理不写长期记忆，避免把探索噪音写进用户画像

提取本身：一次**无工具**的 `callLLM`，输出 JSON 数组（可空）。提示词包含四分类、`WHAT_NOT_TO_SAVE`、相对日期转绝对、以及「不确定就不要写」。解析后逐条 `AddMemory`，`Source` 填当前 session id。PII 失败的条目记日志，不中断其余。

游标：无论写没写，成功跑完就把 `LastExtractMessageID` 推到本轮最后一条消息。显式写入跳过时也要推进，否则下次会把已手写过的那段再提取一遍。

失败策略：提取失败静默（用户对话已经结束）。不重试轰炸。连续失败可记在 store 统计里，不做熔断器——它不是热路径。

### 7.4 会话工作记忆（P1）

`WorkingMemory` 是本会话的滚动状态，不是跨会话知识。模板固定短节，避免自由发挥把窗口吃满：

```
# 本会话
- 目标：
- 当前状态：
- 已确认的约束：
- 关键文件：
- 未完成：
```

更新时机（满足其一即可，取较晚的游标）：

- 自动压缩或 `/compact` 成功：把刚生成的 `ContextSummary` 同步进 `WorkingMemory`（几乎零成本）
- 每累计 ≥ 3 次工具循环且距上次更新 ≥ 若干新消息：一次无工具 LLM，只改这一份 markdown

压缩时的使用（改 `compactSession` 开头）：

```
if session.WorkingMemory != "" && session.WorkingMemoryUpTo >= 候选切点:
    用 WorkingMemory 作为 ContextSummary，CoveredUpTo = WorkingMemoryUpTo
    不调用摘要模型
else:
    走现有摘要 / 降级截断
    成功后回写 WorkingMemory
```

这就是 Claude Code「session memory compact 不打 API」的本地版，复用已有切点与可逆语义。

---

## 八、提示词与系统纪律

`buildBasePrompt` 增补两段（有内容才加）：

1. **工作区指令**（P1）：原文，标注来源路径。
2. **长期记忆索引**（P0）：见 §6.1。另写三句行为：
   - 用户明确说记住 / 忘记，立刻调工具，不要只口头答应
   - 记忆是写下时的观察；项目级超过 1 天的事实，引用文件或函数前先 grep/read
   - 计划、本轮 todo、能从仓库读到的架构**不要** `memory_save`

启动或单测断言（对标工具暴露那条）：`buildBasePrompt` 点名的工具名 ⊆ `GetToolsForLLM()`。`memory_*` 加上后必须进 `directToolOrder`。

---

## 九、文件与职责

不把提取、工具、注入塞回 `memorystore.go`。store 继续只做持久化与检索。

```
memory/memorystore.go      已有；小改：FormatRecall、IndexForPrompt、config 新字段
memory/memorystore_test.go 已有；补 L1/L2 预算与 alreadySurfaced 过滤

memoryapp.go               App 装配、命令、IgnoreMemory、对外 List/Delete API
memorytools.go             三个工具 + 注册进 BuildView / directToolOrder
memoryextract.go           回合结束提取（P1）
memoryinject.go            L1 拼进 prompt、L2 召回块、surfaced 更新
sessionmemory.go           WorkingMemory 更新与 compact 短路（P1）

chat.go                    buildBasePrompt 接 L1；runToolLoop 结束调提取
contextmgmt.go             compact 开头尝试 WorkingMemory
app.go                     startup 创建 MemoryStore；Chat 拦截新命令
filetools.go               directToolOrder 追加三条
sessions.go                Session 新字段
frontend/                  P2：记忆列表与开关
```

`App` 增字段：

```go
memory *memory.MemoryStore
```

`startup`：

```go
a.memory, err = memory.NewMemoryStore(filepath.Join(baseDir, "memory"), memory.MemoryConfig{})
```

失败只打日志，对话仍可用（降级为无记忆），不要让记忆目录损坏挡住启动。

---

## 十、分阶段

### P0 — 闭环最小集（没有提取也能用）

1. `App` 装配 store  
2. L1 索引进 `buildBasePrompt`  
3. L2 `Retrieve` + `FormatRecall` 进 `buildRunMessages`；`SurfacedMemoryIDs`  
4. 三个直出工具 + 提示词点名  
5. `/remember` `/forget` `/memory` `/ignore-memory`  
6. `IgnoreMemory`、总开关、PII 路径保持  
7. 测试见 §12

**完成标准**：新开一个会话说「记住我用 Go，回复别客套」；再开一个同项目会话问无关键的问题，系统提示或召回块里能看到这条；`/forget` 后不再出现。

### P1 — 自动学习与便宜压缩

1. 回合结束提取 + 与 `memory_save` 互斥 + 游标  
2. 工作区指令文件  
3. `WorkingMemory` + `compactSession` 短路  
4. 提取提示词里的分类 / 排除 / 日期绝对化  

**完成标准**：用户只说「别再在每条回复后面总结」，不调 `/remember`，下一会话仍遵守；一次长对话压缩时，若工作记忆已覆盖切点，请求日志里没有第二次摘要调用。

### P2 — 产品面与检索增强

1. 设置页列表 / 删除 / 开关自动记忆与提取  
2. 可选：词检索 topK 后再用当前会话模型做一次 JSON 重排（失败则退回词序）；这时才需要预取  
3. 召回时带上本轮已用工具名，去掉「正在用的工具的 reference 文档」（对标 `recentTools`）  
4. 子代理只读、不提取  

---

## 十一、边界与失败

| 场景 | 行为 |
| --- | --- |
| store 初始化失败 | 启动不阻断；工具返回「记忆不可用」 |
| PII | `AddMemory` 拒绝；工具把错误原文回给模型 |
| 检索 0 条 | 不注入 L2 段；L1 仍在 |
| 召回块超预算 | 少条、单条截断，指向 `memory_search` |
| 提取与主对话赛跑 | 提取只用本轮结束时的消息快照；写 store 有锁 |
| 压缩与召回 | 压缩清空 surfaced，不删记忆文件 |
| `/clear` 类重置（若以后做） | 清会话字段与工作记忆，**不**删 `memory/` |
| 恶意仓库 | 指令文件可被读进 prompt（用户选了这个工作区）；不能改记忆根路径 |
| 子代理 | 可读 L1/L2；禁止 `memory_save` 写 `user`；不跑提取 |
| 与技能 L1 抢预算 | 两段独立封顶，互不挤占；记忆再长也不得删技能清单 |

---

## 十二、测试要点

沿用现有风格：不依赖 UI，store 单测继续绿。

**P0**

- `IndexForPrompt`：空库为空；超 `IndexMaxItems` 截断；ignore 时为空  
- `FormatRecall`：含正文；超 `RecallMaxChars` 截断；stale 才带 caveat  
- 召回过滤 `SurfacedMemoryIDs`；压缩后 id 被清，同一 query 能再次命中  
- `memory_save` 走 PII / 合并；`memory_forget` 多条不删  
- `Chat("/remember …")` 不调模型且下一次 `buildBasePrompt` 含该条  
- 提示词点名的 `memory_*` ∈ `GetToolsForLLM()`  
- `IgnoreMemory` 后 L2 为空、提取不跑、`/remember` 仍可写  

**P1**

- 本轮有 `memory_save` 则提取不调用 LLM，游标仍前进  
- 提取 JSON 空数组不写库  
- 提取产出的「可从 git 推出来的」条目靠提示词约束（抽一条固定 fixture 测解析，不测模型自觉）  
- `WorkingMemoryUpTo` 覆盖切点时 `compactSession` 不走摘要器  
- 工作区超大 `AGENTS.md` 被截断  

**P2**（有再补）重排失败回退词序；子代理 save user 被拒。

---

## 十三、实施时不要做的事

1. **不要**为了「更像 Claude Code」丢掉 `index.json` 改成只维护 `MEMORY.md`。索引文件给模型看可以，给程序当数据库不行。  
2. **不要**把召回结果写成 `role=user` 的假用户消息——会进会话原文、进压缩素材、进提取，污染所有下游。  
3. **不要**在 P0 为检索再打一次模型。词检索接上循环，比一个没人调用的聪明 ranker 有用。  
4. **不要**让 `write_file` 的工作区根指向 `~/.local-agent/memory`。  
5. **不要**把授权「session 记忆」和本方案的记忆混名；文档与 API 继续叫 grant / rule，这里叫 memory。  

---

## 十四、验收清单（产品）

- [ ] 跨会话：偏好与项目事实在新会话里能被模型用到，而不必用户再说一遍  
- [ ] 用户可查、可删、可本会话关闭，不依赖模型配合  
- [ ] 敏感串写不进库  
- [ ] 自动提取不把「仓库里本来就有的架构说明」存成记忆（抽检）  
- [ ] 长对话压缩在有工作记忆时不再每次都打摘要  
- [ ] 关闭记忆或 store 损坏时，对话与工具循环与今天行为一致  

落地顺序按 §十：先 P0 让库活在对话里，再 P1 自动学习与便宜压缩。P0 单独可交付。
