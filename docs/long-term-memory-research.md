# 长期记忆设计模式调研（步骤 3 调研报告）

> 调研对象：业界/开源长期记忆系统的设计模式，服务于 local-agent（纯文件存储、本地运行的 Go 桌面智能体）的长期记忆模块设计。
> 主要参考：项目自带 `docs/claude-code-memory-analysis.md`（Claude Code 逆向源码分析，已完整阅读）+ 业界公开资料。

## 0. 调研结论速览

| 维度 | 业界主流 | local-agent 建议 |
|---|---|---|
| 短期/长期分层 | 会话上下文（短期）+ 持久文件/库（长期） | 已有短期（`Session.Messages`），新增长期目录 `~/.local-agent/memory/` |
| 存储结构 | KV（Zettelkasten）、Markdown 文档、向量库、事件日志 | **Markdown 文档 + 索引 + KV 元数据**（对标 Claude Code 的 `MEMORY.md`+主题文件），事件日志复用现有 transcript |
| 检索 | 向量语义检索 / 关键词 / 混合 / LLM 选择器 | **关键词+标签 为主，可选轻量 LLM 选择器**（不引入重型向量库） |
| 摘要 | 滚动摘要、LLM 抽取、夜间蒸馏 | 复用现有 `compactSession` 摘要能力 + 运行结束抽取 |
| 更新与遗忘 | upsert、TTL、衰减、容量淘汰、显式删除 | 显式删除 + 时效衰减 + 容量淘汰 + 来源可追溯 |
| 隐私安全 | 本地 vs 云端、PII 过滤、加密、权限隔离 | 本地文件 + 严格权限 + PII 提示 + 路径校验 |

---

## 1. 短期 vs 长期记忆分层

### 1.1 业界分层模型

- **ChatGPT（OpenAI）Memory**：
  - 短期：当前对话线程（线程内消息，会过期/被新线程覆盖）。
  - 长期：账号级 memory，后端自动从对话推断保存（"remember"），用户可手动 `/remember`；新对话启动时按需检索注入。
  - 关键：短期是"会话状态"，长期是"跨会话事实库"，两者作用域与生命周期明确不同。

- **Claude Code**（本项目最强蓝本，见 `docs/claude-code-memory-analysis.md`）：
  - ① **Auto-Memory**（`memory/`）：项目级、跨会话、永久。
  - ② **Session Memory**：单会话内，服务压缩（`summary.md`）。
  - ③ **CLAUDE.md 层级**：人工维护的常驻规则（用户/项目/本地/管理四级）。
  - ④ **Transcript**：单会话原始对话流（`*.jsonl`），永久归档。
  - **精髓：内存只存指针与缓存，记忆本体全在文件。**

- **MemGPT**：
  - **Core memory slots**（如 `HumanDescription`、`Personality`）：常驻上下文内，占 token 预算。
  - **File memory / archive**：外置文件，超出 token 预算时检索（可无限扩展）。
  - **Conolidation**：定期把短期/外置记忆蒸馏进 core slots。

- **LangChain**：无严格"分层"，靠多种 `Memory` class 实现（`BufferMemory`、`ConversationBufferWindowMemory`、`EpiMemory` 等），开发者自行组合。

### 1.2 对 local-agent 的启发

1. **短期 = 现有 `Session.Messages`**（完整原文，每会话一个 JSON 文件），不要动它。
2. **长期 = 新增独立目录** `~/.local-agent/memory/`，与短期严格隔离（对标 Claude Code 的 `memory/` 目录，跨会话存活，`/clear` 不清除）。
3. **三层结构**（简化版 Claude Code 四套）：
   - **L0 事件日志（transcript）**：复用现有 `Session.Messages`，作为记忆的"原始来源"。
   - **L1 项目级记忆**（长期）：`~/.local-agent/memory/<项目slug>/`，跨会话。
   - **L2 用户级记忆**（长期）：`~/.local-agent/memory/user/`，跨项目（个人偏好）。
4. 作用域分层（对标 Claude Code agent memory 的 user/project/local 三档）：
   - `user/`：个人偏好（如"用户习惯中文注释"），全局。
   - `<projectslug>/`：项目知识（如"该仓库用 ginkgo 测试"），按项目隔离。
   - `local/`：临时/本地（可选，可 gitignore）。

---

## 2. 向量检索（向量记忆）

### 2.1 业界做法

- **向量库**：每个记忆 embedding，存向量库（FAISS / Chroma / Qdrant / sqlite-vec），query 向量化后 ANN 检索。
- **本地轻量**：
  - **FAISS**：纯 C++ 单文件，可嵌入，无 Python 依赖（但对 Go 需 cgo 或 HTTP 服务，较重）。
  - **Chroma**：Python 嵌入式，本地零运维。
  - **sqlite-vec**：SQLite 扩展，单文件向量检索，**最适合 local-agent**（Go 可直接通过 mattn/go-sqlite + vec 扩展，或 SQLite 内置 FTS5）。
  - **SQLite FTS5**：内置全文（BM25 关键词），纯 C 扩展，Go 友好，**零依赖**（除了 sqlite 驱动）。
- **LLM 选择器**：Claude Code 的 `findRelevantMemories`——扫 `.md`（上限 200）→ 拼 manifest → 用 **Sonnet 做选择器**返回 ≤5 个最相关。**没有向量库**，靠 LLM 判断相关性。

### 2.2 对 local-agent 的启发（关键决策）

**现状约束**：go.mod 无向量库、无 embedding 服务依赖；项目哲学是"纯文件持久化 + 本地轻量"。引入重型向量库（FAISS 需 cgo、Qdrant 需外部服务、Chroma 是 Python）会破坏这一哲学。

**推荐方案（分层）：**
- **一级（MVP，零依赖）**：**SQLite FTS5 关键词检索 + 标签过滤**。记忆写 JSONL/JSON，FTS5 索引 `content` 字段，query 分词后 BM25 检索 top-K。纯 C 扩展、Go 友好、单文件、可全文搜索。
- **二级（可选增强）**：**轻量 embedding**：用本地小模型或 API 对记忆做 embedding，存 `sqlite-vec` 或 JSON 数组，做 ANN 补充关键词。仅在记忆量大、关键词检索不准时启用。
- **三级（对标 Claude Code，高级）**：**LLM 选择器**：扫记忆目录 manifest（`[type] name (timestamp): description`），用当前模型（非 Sonnet）做选择器返回 ≤5 个相关。贵但准确，适合低频（每次 run 一次）场景。

**不推荐**：引入独立向量数据库服务（Qdrant/Milvus/Weaviate）——本地桌面应用运维成本过高。

---

## 3. KV 记忆（键值对）

### 3.1 业界做法

- **Zettelkasten（卡片笔记）**：每段记忆是一个带唯一 ID 的卡片（`Zettel`），卡片间通过 `[[wikilink]]` 互链，形成知识图谱。存储：文件 per 卡片（`123-标题.md`）。
- **KV 记忆**：`key → value` 对，key 是实体/概念标签，value 是事实。
- **优势**：精确读写、易 upsert（按 key 更新）、易删除、去重天然（key 唯一）。
- **局限**：无语义检索（除非 key 本身是关键词或做 embedding）。

### 3.2 对 local-agent 的启发

1. **KV 作为元数据层**：每条记忆带结构化字段（ID、key/标签、type、source、createdAt、updatedAt、importance、expiresAt），形成"KV 索引"。
2. **内容 vs 元数据分离**：
   - 元数据（KV）存 `memory.json`（或 SQLite），用于标签过滤、去重、衰减、容量淘汰。
   - 内容（Markdown）存主题文件，供 LLM 选择器/全文检索。
3. **去重 upsert**：写入时按 `key`（语义标签）判断——若存在高相似条目则**更新**而非追加（避免记忆膨胀）。

**推荐**：KV 元数据（`memory.json`，扁平 JSON 数组）+ Markdown 内容文件，混合模型。

---

## 4. 事件日志（append-only log / transcript）

### 4.1 业界做法

- **Transcript（Claude Code / local-agent 现有）**：每条交互一行（JSONL），可追溯、可重放、可离线后处理。
- **MemGPT event log**：记录每次记忆操作（write/update/read/consolidation），支持审计。
- **离线蒸馏**：从 transcript 离线抽取记忆（如 Claude Code 的夜间 `/dream`：把 `memory/logs/YYYY/MM/YYYY-MM-DD.md` 的追加日志蒸馏进 MEMORY.md）。

### 4.2 对 local-agent 的启发

1. **复用现有 `Session.Messages` 作为 transcript**：它已是 append-only 会话日志（完整原文、含工具调用/结果）。**不要新增一套 transcript**，记忆从它离线蒸馏即可。
2. **记忆操作日志**：新增 `~/.local-agent/memory/audit.jsonl`（可选），记录每次记忆 write/update/delete/consolidate，便于排障与回滚。
3. **离线蒸馏**：提供 `/dream` 式命令（后台定时或手动），把历史会话的 transcript 离线抽取成记忆，写入长期目录。对标 Claude Code 夜间蒸馏。

---

## 5. 摘要（consolidation / summarization）

### 5.1 业界做法

- **滚动摘要**：每 N 轮/每 K token 把历史压缩成摘要（local-agent 已有 `compactSession`，70% 窗口触发）。
- **记忆摘要/抽取**：从摘要中"提炼"值得长期保存的条目（区别于工作摘要）。
- **Claude Code**：
  - Session Memory 模板 10 节（Session Title / Current State / Task / Files & Functions / Workflow / Errors / Codebase / Learnings / Key results / Worklog）。
  - 完整压缩 9 节（Primary Request / Key Concepts / Files & Code / Errors & fixes / Problem Solving / All user messages / Pending Tasks / Current Work / Next Step）。
  - 夜间 `/dream` 蒸馏。

### 5.2 对 local-agent 的启发

1. **两种摘要要分清**：
   - **工作摘要**（`compactSession` 的 `ContextSummary`）：服务**当前会话**的上下文压缩，**不是**长期记忆，不要混入 memory 目录。
   - **记忆摘要/抽取**：从会话中提炼**跨会话事实**，写入 memory。
2. **抽取 prompt 设计**（对标 Claude Code 的 `WHAT_NOT_TO_SAVE_SECTION`）：
   - 值得保存：用户偏好、反馈、项目约束、决策及原因、跨会话有用的项目知识。
   - **不值得保存**：可从项目状态推导的内容（代码模式、架构、git 历史、文件路径）。
3. **抽取时机**：
   - 自动：运行结束后（模型产出最终回复且无工具调用时）触发。
   - 显式：`/remember <内容>`。
4. **结构**：用 Claude Code 的"索引 + 主题文件"模型——`MEMORY.md` 索引（每条 `- [Title](file.md) — one-line hook`）+ 主题文件（markdown + YAML frontmatter 分类）。

---

## 6. 记忆更新与遗忘

### 6.1 业界做法

- **更新（upsert）**：按语义相似度判断，高相似则**更新**而非追加（去重、防膨胀）。
- **TTL/过期**：记忆带 `expiresAt`，到期自动失效。
- **衰减（recency decay）**：最近访问 bump 时间戳；长期未访问降权。Claude Code 用**相对时间表述**（"47 days ago" 而非 ISO 时间戳）触发模型时效推理，配 `<system-reminder>` 漂移告警。
- **容量淘汰**：超过上限按"重要性×最近访问"淘汰。Claude Code `MEMORY.md` 硬上限 200 行 / 25KB。
- **显式删除**：用户 `/forget` 或 UI 删除。
- **功能开关**：`isAutoMemoryEnabled()` 优先级链（环境变量/`--bare`/远程模式/设置项），支持项目级 opt-out。

### 6.2 对 local-agent 的启发

1. **时效衰减**（对标 Claude Code `memoryAge`）：记忆注入时附相对时间（"3 days ago"），>1 天加 `<system-reminder>` 提示"这是时间点观察，代码/引用可能过期，断言前先 grep 核实"。
2. **容量**：`MEMORY.md` 索引上限 200 行（细节移入主题文件）；主题文件 >40KB 告警。
3. **upsert 去重**：写入时扫描现有记忆，语义/标签相似度高则更新 `updatedAt`。
4. **遗忘**：
   - 显式删除（`/forget <id>` + UI 删除）。
   - 容量淘汰（按 importance×recency 排序，淘汰最低）。
   - TTL（可选 `expiresAt`）。
5. **功能开关**：`memory.enabled` 设置项（支持项目级 opt-out）；`/clear` 仅重置会话、**不清除记忆**（记忆是项目级，跨会话存活）。

---

## 7. 隐私与安全

### 7.1 业界做法

- **本地 vs 云端**：ChatGPT/OpenAI memory 在云端；Claude Code/MemGPT 本地文件。本地智能体应**本地存储**。
- **PII 过滤**：不存邮箱、手机号、密码、API key 等敏感信息（prompt 级约束 + 可选正则检测）。
- **加密**：本地文件可 AES-GCM 加密，或依赖 OS 权限（0o700/0o600）。
- **权限隔离**：Claude Code 目录 `0o700`、文件 `0o600`。
- **路径安全**：`validateMemoryPath` 拒绝相对路径/根路径/Windows 盘根/UNC/空字符路径；**刻意排除 projectSettings**（防恶意仓库把 `autoMemoryDirectory: ~/.ssh` 污染敏感目录）。
- **作用域隔离**：user/project/local 三级，local 可 gitignore。

### 7.2 对 local-agent 的启发

1. **本地存储**：记忆全在 `~/.local-agent/`，绝不上传云端。
2. **权限**：memory 目录 `0o700`、文件 `0o600`（对标 Claude Code）。
3. **PII**：抽取 prompt 明确排除敏感信息；可选正则过滤（邮箱/手机号/密码/key 前缀）。
4. **路径校验**：记忆路径基于固定 baseDir（`~/.local-agent/memory/`），**不接受用户自定义绝对路径**（避免路径遍历/污染）；项目 slug 用 sanitize（对标 Claude Code `sanitizePath`）。
5. **用户可控**：记忆列表 UI（可看可删），对标会话列表 UI；`/forget` 命令。

---

## 8. 综合推荐（针对 local-agent：纯文件、本地 Go 桌面智能体）

### 推荐采用的模式

1. **存储**：**Markdown 主题文件 + `MEMORY.md` 索引 + `memory.json` KV 元数据**（三件套，对标 Claude Code 但简化）。事件日志复用现有 `Session.Messages`，不新增 transcript。
2. **检索（MVP 零依赖）**：**SQLite FTS5 关键词 + 标签过滤**。纯 C 扩展、Go 友好、单文件、全文可搜。
3. **写入**：**双轨**——主 Agent 显式 `/remember`（轨一）+ 运行结束后自动抽取（轨二，复用 `compactSession` 摘要能力 + 专用抽取 prompt，异步执行不阻塞）。两轨**互斥去重**（若主 Agent 本轮已写记忆，后台跳过）。
4. **读取**：**每次 run 开始构建 system prompt 时检索一次**（对标 Claude Code "常驻 + 按需"），注入 "## 长期记忆" 段。
5. **遗忘**：显式删除 + 时效衰减（相对时间 + 漂移告警）+ 容量淘汰（importance×recency）。
6. **安全**：本地存储 + 0o700/0o600 权限 + PII prompt 约束 + 固定 baseDir（不接受自定义绝对路径）。

### 不推荐采用的模式（本阶段）

1. **重型向量数据库**（FAISS/Qdrant/Milvus）：本地桌面运维成本高，破坏"纯文件轻量"哲学。用 SQLite FTS5 先满足 MVP，需语义检索时再引入 `sqlite-vec` 轻量 embedding。
2. **独立外部服务**：记忆必须本地、零外部依赖。
3. **Python 生态**（Chroma 等）：与 Go 技术栈不一致。
4. **向量检索优先**：先做关键词+标签（准确可解释、零依赖），语义检索作增强而非主体。

### 最小可行方案（MVP）路线图

1. **`internal/memstore/memorystore.go`**（对标 `internal/store/sessions.go`）：`MemoryStore` 结构，`~/.local-agent/memory/memory.json` + 主题文件目录；`AddMemory / ListMemories / SearchMemories / DeleteMemory / UpdateMemory`，`mu sync.RWMutex`。
2. **app.go startup**：`a.memoryStore = NewMemoryStore(filepath.Join(baseDir, "memory"))`；`memory.enabled` 设置项。
3. **读取接入**（`buildBasePrompt`）：每次 run 前 `memoryStore.Search(query, session.Project, topK)` → 注入 "## 长期记忆" 段。
4. **写入接入**：
   - 显式：`/remember <内容>`（Chat 方法开头拦截）。
   - 自动：运行结束后异步 `extractMemoryAsync(sessionID, query, result)`。
5. **遗忘**：`/forget <id>` + UI 删除 + 时效衰减注入 + 容量淘汰。
6. **安全**：0o700/0o600 权限 + PII prompt + 固定 baseDir。

---

## 附：与步骤 2 接入点的衔接

步骤 2 已识别长期记忆可接入点：
- **写入**：B（运行完成点）+ C（显式 `/remember`）→ 对应本调研 §5.2 / §8.3。
- **读取**：D（`buildBasePrompt` 注入 "## 长期记忆" 段）→ 对应本调研 §8.3.4。
- **存储**：`~/.local-agent/memory/memory.json` → 对应本调研 §8.1。

下一步（步骤 4）将基于本调研 + 步骤 2 的接入点，落地**长期记忆技术方案**（`memory.md` 索引 + 主题文件 + `memory.json` KV + SQLite FTS5 检索 + 双轨写入 + 时效衰减 + 容量淘汰 + 安全）。
