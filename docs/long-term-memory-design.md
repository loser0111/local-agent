# 长期记忆技术方案（步骤 4）

> 基于：项目技术栈（Go 1.25 + Wails，**纯文件持久化，无 DB/向量依赖**）
>        + 核心执行流程（`Chat → buildBasePrompt → runToolLoop`，步骤 2）
>        + 业界调研结论（`docs/long-term-memory-research.md`，步骤 3）
>        + Claude Code 记忆机制（`claude-code-memory-analysis.md`）
>
> 本方案目标：给 local-agent 增加**跨会话长期记忆**能力，最小化改动、零新增依赖、可逆、用户可控。

---

## 0. 设计目标与原则

**目标**
1. agent 能在**新会话**中记住"用户偏好 / 项目约束 / 关键决策"，避免反复问用户。
2. 记忆**可查、可编辑、可删除、可追溯**（每条记忆指向来源会话）。
3. **不污染**现有 `Session.Messages`（短期工作记忆）——记忆是独立存储层。
4. 零新增编译依赖；记忆文件纯文本，用户可手动编辑/审计。

**原则（从项目现状推导）**
- **文件优先**：遵循 `sessions.go`（JSON per session）/ `skills.go`（markdown）的既有风格，不引入 SQLite/向量库。
- **可逆、非破坏**：借鉴 contextmgmt "压缩可逆" 的先例——记忆独立于会话，`/clear` 只清会话、不清记忆。
- **轻量检索**：MVP 用 in-process 关键词+标签匹配，**无 LLM 调用、无外部服务**。
- **用户掌控**：显式命令（`/remember`/`/forget`）+ 配置开关 + 列表管理 UI。
- **来源可溯**：每条记忆带 `source` 会话 ID，可回看原文。

---

## 1. 存储选型

### 1.1 决策

| 候选 | 优点 | 缺点 | 结论 |
|---|---|---|---|
| JSON 索引 + markdown 主题文件（**推荐**） | 纯文件、零依赖、人机可读、可手编、与现有风格一致 | 无语义检索（MVP 够用，关键词+标签） | ✅ 采用 |
| SQLite（mattn/go-sqlite） | 单文件、ACID、FTS5 全文 | 新增依赖、偏离"纯文件"哲学、不可读 | ❌ MVP 不用 |
| 外部向量库（FAISS/Qdrant） | 语义检索强 | 需 cgo/外部服务，重 | ❌ 不采用 |
| 纯单文件 JSON（无主题文件） | 简单 | 内容全挤 JSON、难手编、难 LLM 选择 | ⚠️ 只作索引 |

**选型**：`memory.json`（索引/元数据，KV）+ `user/`、`project/<slug>/` 主题 markdown（内容）。
这直接对标 Claude Code 的 `MEMORY.md` 索引 + 分类主题文件，但用 JSON 做索引（比手写 markdown 索引更易程序维护、易做标签/去重/衰减/淘汰）。

### 1.2 文件布局（新增目录，零迁移）

```
~/.local-agent/
└─ memory/
   ├─ index.json            # 所有记忆元数据（索引，含 path 指向主题文件）
   ├─ config.json           # 记忆配置（topK、autoExtract、maxCount、enabled…）
   ├─ user/                 # 用户级（跨项目、全局）
   │  └─ 中文注释风格.md     # 主题文件：YAML frontmatter + 正文
   └─ project/
      └─ local-agent/       # 项目级：按项目 slug 隔离
         └─ 测试用ginkgo.md
```

- `memory/` 权限 `0o700`，主题文件 `0o600`（对标 Claude Code，防目录污染）。
- 项目 slug 用 `sanitizePath`（拒绝 `..`/绝对路径/空字符），**不接受用户自定义绝对路径**。

---

## 2. 数据模型

### 2.1 记忆类型（对齐 Claude Code 四分类）

| type | 含义 | 示例 |
|---|---|---|
| `user` | 用户偏好/个人信息 | "用户习惯中文注释" |
| `feedback` | 用户对结果的反馈/评价 | "用户希望回复更简短" |
| `project` | 项目约束/决策/架构知识 | "该仓库用 ginkgo 测试，测试放 *_test.go" |
| `reference` | 参考资料/文档/命令/链接 | "构建命令 make build，CI 用 GitHub Actions" |

**明确排除**（对标 Claude Code `WHAT_NOT_TO_SAVE`）：可从项目状态直接推导的内容——代码模式、架构、git 历史、文件路径、可 `grep` 到的事实。记忆只存"写当下、跨会话、无法即时重推"的判断。

### 2.2 作用域（scope）

| scope | 路径 | 含义 |
|---|---|---|
| `user` | `memory/user/<slug>.md` | 全局，跨项目（个人偏好） |
| `project` | `memory/project/<slug-project>/<slug>.md` | 项目级，按项目隔离 |

### 2.3 索引条目（`index.json`）

```json
{
  "version": 1,
  "memories": [
    {
      "id": "mem_01HABC...",
      "title": "用户注释风格",
      "description": "一行摘要（用于相关性判断）",
      "type": "user",
      "scope": "user",
      "project": "",
      "path": "user/中文注释风格.md",
      "tags": ["coding-style", "comment", "中文"],
      "importance": "high",
      "source": "session_20260920_150405_ab12",
      "sourceSummary": "来自会话『修复登录 bug』",
      "createdAt": 1788332405000,
      "updatedAt": 1788332405000,
      "expiresAt": 0
    }
  ]
}
```

- `id`：`mem_` + 随机 16 hex（对标 session id 风格）。
- `importance`：`low|medium|high`（用于容量淘汰排序）。
- `expiresAt`：0 = 永久；非 0 = Unix 毫秒（TTL）。
- `path`：相对 `memory/` 的主题文件路径。
- `source`/`sourceSummary`：溯源（来源会话 id + 一句话上下文）。

### 2.4 主题文件（markdown，YAML frontmatter + 正文）

```markdown
---
id: mem_01HABC...
title: 用户注释风格
type: user
scope: user
tags: [coding-style, comment, 中文]
source: session_20260920_150405_ab12
created: 2026-09-20
updated: 2026-09-20
importance: high
expires: ""
---
用户习惯用中文写注释，函数命名用 camelCase，
偏好简洁的 doc comment（一句话说明用途+关键参数），
不要用英文注释。
```

**为什么双文件**：JSON 索引负责"元数据/去重/检索/衰减/淘汰"（程序维护）；markdown 主题文件负责"内容"（人可手编、可整段读入 prompt、可 LLM 选择）。

### 2.5 配置（`config.json`）

```json
{
  "enabled": true,
  "autoExtract": true,
  "maxTopK": 5,
  "maxMemoriesPerScope": 400,
  "stalenessCaveat": true,
  "piiFilter": true
}
```

- `enabled` 全局开关；**支持项目级 opt-out**（session 的 `EnabledSkills` 之外，新增会话字段 `MemoryEnabled`）。
- 优先级链（对标 Claude Code `isAutoMemoryEnabled`）：全局 env `LOCAL_AGENT_DISABLE_MEMORY` → 设置项 `enabled` → 会话级 `MemoryEnabled`。

---

## 3. 写入触发

### 3.1 三轨写入（双轨自动 + 一轨显式）

| 轨 | 时机 | 实现 | 成本 |
|---|---|---|---|
| **① 显式 `/remember`** | 用户主动 | 直接写，**无 LLM** | 极低 |
| **② 运行结束自动抽取** | 主会话 run 成功结束（最终回复、无 tool_calls）后 | 异步 goroutine，**有 LLM 调用** | 中（可关） |
| **③ `/dream` 离线蒸馏** | 手动/定时 | 扫历史会话 transcript 离线抽取 | 中 |

**互斥去重**（对标 Claude Code `hasMemoryWritesSince`）：
- 轨① 本轮已写记忆 → 轨② 跳过（检测：本轮 memory 写入计数 > 0）。
- 轨② 抽取结果与既有索引高相似 → **更新合并**（bump `updatedAt`）而非新增。

### 3.2 显式命令（零 LLM，最快、最可控）

```
/remember <内容>  [type]  [tags=a,b]  [project]
/forget <id 或 标题片段>
/memory-list            # 列出当前作用域记忆
/memory-edit <id>       # 编辑（走 UI，命令可选）
```

解析复用 `ParseSkillCommand` 同款前缀解析（`/` 开头）。`/remember` 的 `content` 直接写入正文；`type` 默认自动判定（可从关键词推断，缺省 `project` 若工作区可识别项目）。

### 3.3 自动抽取（异步，run 结束）

**触发点**：`chat.go` 中 `runToolLoop` 返回最终回复（finish_reason == stop 且无 error）后，`app.Chat` 末尾调用：

```go
if result.Reply != "" && result.Error == "" &&
   a.memoryConfig().AutoExtract &&
   session.Status == "active" {
   go a.extractMemoriesAsync(session, query, result.Reply)  // 不阻塞
}
```

**抽取流程**（一次 LLM 调用，复用 `summarizeContext` 的调用栈）：
1. 素材 = 本轮 query + 最终回复 +（可选）最近 N 条关键工具结果摘要。
2. 提示词（记忆抽取 prompt，含 `WHAT_NOT_TO_SAVE`）：
   > "从下面对话中提取**值得跨会话保存**的记忆。只保留用户偏好/反馈/项目决策/参考资料。
    > 排除：可从项目状态推导的代码模式、架构、git 历史、文件路径。排除 PII。
    > 输出 JSON：[{type, scope, title, description, content, tags, importance}]。
    > 若没有值得记的，返回空数组。"
3. 解析候选 → PII 过滤 → 去重/合并（见 §6.1）→ 逐条 `AddMemory`。
4. 失败不阻断：记日志，下一 run 重试。

**边界**：子代理**不做**自动记忆抽取（子代理是工作产物，其结论已作为 tool 结果进入主会话；记忆只由主会话写，避免串味）。子代理会话 `ParentID` 非空时跳过。

### 3.4 `/dream`（离线蒸馏，Phase 3）

`/dream [session-id]`：扫指定/最近 N 个会话的 `Messages`（transcript），离线批量抽取记忆 → 去重合并 → 写存储。对标 Claude Code 夜间蒸馏。也可作为"回填旧会话记忆"的迁移工具（§12）。

---

## 4. 检索策略

### 4.1 MVP：in-process 关键词 + 标签匹配（零依赖、无 LLM、< 10ms）

**入口**：
```go
func (m *MemoryStore) Retrieve(query string, projectSlug string, topK int) ([]*MemoryEntry, error)
```

**算法**（`index.json` 内做，O(n) 扫全部记忆，n 百级 → 微秒）：
```
candidates = 索引记忆 where
    (scope == "user") OR (scope == "project" AND project == projectSlug)
    AND (expiresAt == 0 OR expiresAt > now)
    AND (enabled)

score(每个) =
    tag 命中        每命中 1 个 tag    +3.0
    title 关键词    每命中 1 个词       +2.0
    description 词  每命中 1 个词       +1.0
    recency         updatedAt 近 3 天   +1.0
    importance      high              +0.5

按 score 降序取 topK（默认 5，config 可调），
对命中条目 load 其 path 主题文件内容 → MemoryEntry{Meta, Content}
```

**查询分词**：中英文混合——CJK 按字符/词滑窗、拉丁按 `\W` 切，去 stopwords（"的、是、在、和、请、用" 等 + 英文常见 stopwords）。

**返回格式**（给 prompt 注入用，§5）：
```go
type MemoryEntry struct {
    Meta    MemoryMeta  // 元数据
    Content string      // 主题文件正文
    Age     string      // "3 days ago" 相对龄（memoryAge 风格）
    Stale   bool        // 项目级且 >1 天
}
```

### 4.2 增强（可选，Phase 3，默认关）：LLM 选择器

对标 Claude Code `findRelevantMemories`：扫索引 manifest（`[type] title (age): description`）→ 当前模型（非专门选择器）返回 ≤5 个最相关。
- **默认关**（避免每次 run 多一次 LLM 调用）。
- 配置 `useLLMSelector=true` 时才启用；作为关键词检索的"弱时兜底"。

### 4.3 不采用的：向量检索

MVP 不用 embedding/向量库。理由：
- 记忆规模小（百级），关键词+标签足够准且可解释。
- 避免新增依赖（embedding 模型/服务 + 向量存储）。
- 需要强语义检索时，Phase 3 用 `sqlite-vec` 单文件 embedding 增强，**不**引入外部服务。

---

## 5. 上下文注入接口

### 5.1 注入位置（关键决策）

**只在主会话 prompt 注入**，不注入子代理（子代理拿聚焦任务，避免记忆串味）。

`buildBasePrompt(session, dir)`（chat.go:553）**没有 query**，而记忆检索需要 query。因此注入放在 **`buildBasePromptWithSkill(session, dir, query)`**（chat.go:602，唯一带 query 的主路径）之后，新增一个包装方法：

```go
// 新增：在主 prompt 末尾追加"长期记忆"段（仅主会话）
func (a *App) buildPromptWithMemory(session *Session, dir, query string) (string, error) {
    prompt, err := a.buildBasePromptWithSkill(session, dir, query)
    if err != nil {
        return "", err
    }
    if session.ParentID != "" {          // 子代理：不注入记忆
        return prompt, nil
    }
    return a.appendMemorySection(prompt, session, query), nil
}
```

调用点替换：
- `executeChat`（chat.go:543）→ `a.buildPromptWithMemory(session, dir, query)`
- `ChatPlan` 规划/执行路径 → 同样替换
- 子代理 `runSubagent` → **继续用** `buildSubagentPrompt`，不动

### 5.2 注入接口（新增 `memorystore.go`）

```go
// 新增文件 memory/memorystore.go，对标 sessions.go 的 SessionStore
type MemoryStore struct {
    mu    sync.RWMutex
    dir   string // ~/.local-agent/memory
    cfg   MemoryConfig
    index *MemoryIndex
}

func NewMemoryStore(dir string) *MemoryStore

// 写入
func (m *MemoryStore) AddMemory(meta MemoryMeta, content string) (*MemoryMeta, error)
func (m *MemoryStore) UpdateMemory(id string, meta MemoryMeta, content string) error
func (m *MemoryStore) DeleteMemory(id string) error
func (m *MemoryStore) ListMemories(scope, project string) []*MemoryEntry

// 检索 + 注入
func (m *MemoryStore) Retrieve(query, projectSlug string, topK int) ([]*MemoryEntry, error)
func (m *MemoryStore) FormatInjection(entries []*MemoryEntry, staleCaveat bool) string
```

### 5.3 注入块格式（append 到 prompt）

```
## 长期记忆（跨会话）
以下为过去积累的偏好/项目知识，与当前任务相关时请优先参考：
  1. [user] 用户习惯中文注释、函数名 camelCase（3 days ago）
  2. [project] 测试用 ginkgo，用例放 *_spec.go（yesterday）

> 注意：记忆是"写下时的观察"。超过 1 天的项目级记忆可能已过
> 期（代码会漂移）。引用具体文件/函数/行为前，先 grep/read 核实。
```

- topK 默认 5（`config.maxTopK`）。
- 每条渲染：`[scope/type] 标题 — 一行描述（龄）`。
- **龄**用相对时间（`memoryAge`）：`today / yesterday / N days ago`（对齐 Claude Code，因为模型不擅长算日期）。
- 项目级且 >1 天 → 置 `Stale`，触发上方 drift caveat。
- 记忆块整体**预算**：≤ 800 字符（超则截断，优先保 importance=high）。

### 5.4 不变式（对齐 contextmgmt 的工程纪律）

- 记忆注入是**纯增**到 system prompt，不改变 `Session.Messages`，不破坏 tool_calls/tool 配对（记忆在 system，不在消息流）。
- 检索失败 / 记忆目录缺失 → **不报错**，直接不注入（降级为无记忆），保证对话不中断。
- 注入是**幂等、按 run 一次**（同一 run 内多次 LLM 调用复用同一 prompt，不重复检索）。

---

## 6. 更新、去重与遗忘

### 6.1 去重/合并（upsert）

写入前 `ResolveMerge(existing, candidate)`：
```
for 每条现有记忆 e：
    sim = tagOverlap(e, c) * 0.4 + titleDescOverlap(e, c) * 0.4 + typeMatch(c, e) * 0.2
    if sim > 0.6：
        → 合并：e.content += "\n" + c.content（去空白）
                  e.updatedAt = now
                  e.tags ∪= c.tags
        → 更新 e（不落新条目），返回
否则 → 新增
```

### 6.2 衰减与淘汰

- **TTL**：`expiresAt != 0` 且过期 → 检索时排除（懒删除，写入时批量清）。
- **容量淘汰**：某 scope 记忆数超 `maxMemoriesPerScope`（400）→ 按 `importance × recency` 升序淘汰最低（先删 low 且最老）。
- **时效提示**：注入时附相对龄 + drift caveat（§5.3），不自动删。

### 6.3 遗忘

- 显式：`/forget <id>`、`/memory-delete`；UI 删除。
- 删除操作：删 `path` 主题文件 + 从 `index.json` 移除 + 写 `audit.jsonl`。
- `/clear`（现有）**不清除**记忆（记忆是项目级跨会话资产）。

### 6.4 PII 过滤

- **Prompt 层**：抽取 prompt 明确"排除 PII"。
- **代码层**（`piiFilter=true`）：写入前正则匹配并**拒绝或脱敏**：
  - 邮箱、手机号（中国 `1[3-9]\d{9}`）、银行卡、`password|passwd|secret|token|api[_-]?key` 前后文、私钥头 `-----BEGIN`.
  - 命中 → 该条 `AddMemory` 失败（记日志，不写）。

---

## 7. 隐私与安全

1. **本地存储**：记忆全在 `~/.local-agent/memory/`，**绝不上云**。
2. **权限**：目录 `0o700`、文件 `0o600`（对标 Claude Code）。
3. **路径安全**：
   - 固定 baseDir（`~/.local-agent/memory`），**不接受用户自定义绝对路径**（防路径遍历/污染 `~/.ssh` 等）。
   - 项目 slug 用 `sanitizePath`；主题文件 slug 拒绝 `..`/绝对路径/空字符（对标 `isSkillIDChar` 同款校验）。
4. **PII**：§6.4 双保险（prompt + 正则）。
5. **作用域隔离**：user 级只写 `user/`，project 级只写 `project/<slug>/`，不跨域写。
6. **审计**：`memory/audit.jsonl` 记录每次 write/update/delete/consolidate（可选，便于回滚/排障）。

---

## 8. 评估指标

### 8.1 记忆质量（人工/评测集）

| 指标 | 定义 | 目标 |
|---|---|---|
| **抽取精度** | 自动抽取的记忆中"非垃圾/非 PII/不可推导"占比 | ≥ 80% |
| **抽取召回** | 评测集中"应记"事实被记住占比 | ≥ 70% |
| **去重率** | 自动去重/合并触发率（衡量是否膨胀） | 适中（< 30% 过激，> 70% 漏合并） |
| **遗忘正确率** | 用户 `/forget`/UI 删除后不再出现占比 | 100% |
| **用户采纳/纠正** | 记忆中"被用户确认有用 / 标记错" 比例（thumbs 反馈） | 有用 ≥ 70% |

### 8.2 检索质量

| 指标 | 定义 | 目标 |
|---|---|---|
| **Retrieval Precision@K** | 命中记忆中相关占比（评测集） | ≥ 0.7 |
| **Retrieval Recall@K** | 相关记忆被检索到占比 | ≥ 0.6 |
| **注入 token 占比** | 记忆块 token / system prompt token | ≤ 5% |
| **检索延迟** | `Retrieve` p99 | < 10ms（无 LLM） |

### 8.3 记忆健康（运行监控，日志/`/memory-stat`）

- 总记忆数、每 scope 数量、近 30 天新增/删除/过期数。
- 膨胀率（每会话平均新增）、淘汰触发次数、PII 拦截次数。
- 提供 `/memory-stat` 命令查看。

**评测方法**：建一组"黄金记忆"评测集（固定对话 → 应记住的事实列表），跑 agent → 断言新会话能否正确调用/回答，量化抽取/检索/注入端到端效果。

---

## 9. 代码落地点（文件/函数级）

| 新增/改动 | 文件 | 内容 |
|---|---|---|
| 新增 | `memory/memorystore.go` | `MemoryStore`、`MemoryMeta`、`MemoryEntry`、CRUD、`Retrieve`、`FormatInjection`、去重、PII、淘汰 |
| 新增 | `memory/config.go`（或并入 memorystore） | `MemoryConfig`、默认值、env 优先级链 `isMemoryEnabled` |
| 新增 | `memory/extract.go` | `extractMemoriesAsync`、抽取 prompt、`/dream` |
| 改 | `app.go`（startup ~75 行） | `a.memoryStore = NewMemoryStore(filepath.Join(baseDir, "memory"))`；`a.memoryCfg` |
| 改 | `app.go`（Chat ~610） | `/remember`/`/forget`/`/memory-list`/`/memory-stat` 命令拦截（同 `/compact` 前置） |
| 改 | `chat.go`（executeChat ~543） | `buildBasePromptWithSkill` → `buildPromptWithMemory` |
| 改 | `chat.go`（新增方法） | `buildPromptWithMemory`（§5.1）、`appendMemorySection`（§5.3） |
| 改 | `sessions.go`（Session 结构） | 新增 `MemoryEnabled bool`（会话级 opt-out） |
| 新增 | `frontend/.../MemoryList.vue`（Phase 3） | 记忆列表 UI（增删改，调用 `ListMemories`/`DeleteMemory`） |

---

## 10. 分期实施计划（迁移方案）

> **迁移本质**：记忆是**全新目录/全新文件**，与现有 session/skill 数据完全隔离 → **无存量数据迁移**，纯增量、可逆、可回滚（删 `memory/` 即无损）。旧会话/压缩逻辑零改动。

### Phase 0（当前）
- 无改动。已调研（步骤 3）、已出方案（本文件）。

### Phase 1（MVP，显式记忆）
1. `memorystore.go`：`index.json` + 主题文件 CRUD（Add/Update/Delete/List），权限 0o700/0o600。
2. `/remember`、`/forget`、`/memory-list` 命令。
3. `Retrieve`（关键词+标签）+ `appendMemorySection` 注入主 prompt（§5）。
4. `config.json` + `enabled` 开关 + 会话级 `MemoryEnabled`。
5. 测试：`memorystore_test.go`（CRUD、去重、检索、注入格式、PII 过滤、路径安全）。
> ✅ 此阶段已完成记忆"读/显式写"闭环，零 LLM、可验证。

### Phase 2（自动记忆）
1. `extractMemoriesAsync`（run 结束异步抽取）+ 抽取 prompt + 去重合并。
2. PII 正则过滤生效。
3. 时效衰减注入（相对龄 + drift caveat）。
4. 容量淘汰（importance×recency）。
5. 评测集 + 指标（§8.1/8.2）首次测量。

### Phase 3（增强）
1. `/dream` 离线蒸馏 + 回填旧会话记忆（迁移/补全工具）。
2. 可选 LLM 选择器（默认关）。
3. 可选 `sqlite-vec` 语义检索（默认关）。
4. 记忆管理 UI（`MemoryList.vue`：增删改、编辑 frontmatter）。
5. `/memory-stat` 健康监控。

**回滚**：任何 Phase 可整目录删除 `~/.local-agent/memory/` 无损回滚；配置 `enabled=false` 即时关闭。

---

## 11. 风险与边界

1. **记忆漂移/过期**：记忆是"写下时的观察"，代码会漂移。→ 相对龄 + drift caveat + 要求引用前 `grep/read` 核实（§5.3/§7）。
2. **记忆膨胀**：自动抽取可能产垃圾/重复。→ 去重合并 + 容量淘汰 + 评测精度门槛 + 用户可控。
3. **PII 泄露**：用户对话含敏感信息被记住。→ 双保险（prompt + 正则）；`pIIFilter` 可关。
4. **检索不准**：关键词+标签在语义模糊查询时弱。→ topK 保守（5）、importance 加权、Phase 3 语义增强。
5. **串味**：多项目共用记忆。→ 作用域隔离（user/project），project 级按 slug 分目录。
6. **性能**：每次 run 检索。→ 无 LLM、O(n) 微秒级、按 run 一次幂等。
7. **子代理误注入**：→ 子代理（`ParentID` 非空）不注入、不做自动抽取（§5.1/§3.3）。

---

## 12. 关键接口契约（给实现者）

**`Retrieve` 返回**（供注入）：
```go
// 按 score 降序，最多 topK 条；每条带相对龄与 stale 标志
type MemoryEntry struct {
    ID        string
    Scope     string   // user|project
    Type      string   // user|feedback|project|reference
    Title     string
    OneLine   string   // description
    Content   string   // 主题文件正文
    Age       string   // "today"|"yesterday"|"N days ago"
    Stale     bool     // 项目级且 >1 天 → 触发 drift caveat
    Importance string
}
```

**`AddMemory` 幂等**：相同 `(scope, project, title 指纹)` 且内容高相似 → 返回已有 id + updated=true（不新增）。

**注入预算**：`FormatInjection` 输出总长度 ≤ `memoryInjMaxChars`（默认 800），超则按 `importance` 降序截断并追加"（共 N 条，已显示 top K）"。

**命令优先级**：`/remember`/`/forget`/`/memory-list` 与 `/compact` 一样**前置拦截**（在 `ParseSkillCommand` 技能匹配前），内置命令优先级高于同名技能。

---

## 13. 结论

本方案以 **`memory.json` 索引 + 主题 markdown** 为存储、**关键词+标签** 为 MVP 检索、**双轨写入（显式 /remember + run 结束异步抽取）**、**主 prompt 注入（不注入子代理）**、**来源可溯 + 时效衰减 + 容量淘汰 + 用户可控**为核心，**零新增依赖**、**纯增量可回滚**，与 local-agent 现有"纯文件 + 可逆"工程纪律一致，且逐 Phase 可验证。
