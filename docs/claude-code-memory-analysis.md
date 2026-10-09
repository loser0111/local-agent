# Claude Code 记忆力（Memory）机制分析报告

> 分析对象：`Austin1serb/Anthropic-Leaked-Source-Code`（Claude Code 泄露源码，由 sourcemap 逆向还原）
> 本地克隆：`E:/learn/local-agent/claude-code-leak`（HEAD `3fa80a4`，1929 文件 / 38.8 MB）
> 说明：该仓库为 Anthropic 未授权泄露代码，仅供本地学习研究，请勿再分发。

---

## 1. 机制概览

Claude Code 没有数据库、没有内存态长期记忆，**一切记忆皆文件**。共四套彼此独立、分工明确的系统：

| 系统 | 作用域 | 落盘位置 | 生命周期 |
|---|---|---|---|
| ① Auto-Memory（`memdir/`） | 项目级，跨会话 | `~/.claude/projects/<git-root>/memory/` | 永久，语义化组织 |
| ② Session Memory | 单会话内 | `{projectDir}/{sessionId}/session-memory/summary.md` | 随会话，服务压缩 |
| ③ CLAUDE.md 层级 | 多级（管理/用户/项目/本地） | `/etc`、`~/.claude/`、项目根、`.claude/rules/` | 永久，人工维护 |
| ④ Transcript 记录 | 单会话 | `{projectDir}/{sessionId}.jsonl` | 永久，原始对话流 |

内存中只保留**指针与缓存**（如 `lastSummarizedMessageId`、`getMemoryFiles` memo 缓存），不保留记忆本体。

数据流总览：

```
对话 ──写入──> ① MEMORY.md + 主题文件（项目级记忆）
对话 ──写入──> ② summary.md（会话记忆，供压缩复用）
对话 ──归档──> ④ *.jsonl（transcript）
上下文将满 ──> 三级压缩（会话记忆压缩 → micro-compact → 完整摘要）
新会话 ──检索──> MEMORY.md 常驻 + Sonnet 语义筛选主题文件
```

---

## 2. 关键实现细节

### 2.1 存储位置与解析（`memdir/paths.ts`）

记忆目录以 **git root 的 sanitize 路径**为键，故同一仓库所有 worktree 共享一份记忆：

```ts
const AUTO_MEM_DIRNAME = 'memory'
const AUTO_MEM_ENTRYPOINT_NAME = 'MEMORY.md'
// getAutoMemPath(): <memoryBase>/projects/<sanitizePath(git-root)>/memory/
```

解析优先级：
1. 环境变量 `CLAUDE_COWORK_MEMORY_PATH_OVERRIDE`（整路径覆盖）
2. 设置项 `autoMemoryDirectory`（**仅** policy / local / user 来源）
3. 默认 `getMemoryBaseDir()` = `CLAUDE_CODE_REMOTE_MEMORY_DIR` 或 `~/.claude`

安全设计（`validateMemoryPath`）：拒绝相对路径、根路径、Windows 盘根、UNC、含空字符路径；**刻意排除 `projectSettings`**——否则恶意仓库可设 `autoMemoryDirectory: ~/.ssh` 借助文件写入豁免静默污染敏感目录。

### 2.2 存储格式

`MEMORY.md` 是**索引而非内容**（`memdir/memdir.ts`）：
- 每条：`- [Title](file.md) — one-line hook`，单行 ≤150 字符
- 硬上限：`MAX_ENTRYPOINT_LINES = 200`、`MAX_ENTRYPOINT_BYTES = 25_000`
- 超限按行→字节截断，并追加 `> WARNING: MEMORY.md is ...`（`truncateEntrypointContent`）

主题文件为 markdown + YAML frontmatter：

```markdown
---
name: {{memory name}}
description: {{one-line description — used to decide relevance}}
type: {{user, feedback, project, reference}}
---
```

四类封闭分类法（`memdir/memoryTypes.ts`）：`user` / `feedback` / `project` / `reference`。明确**排除**可从项目状态推导的内容（代码模式、架构、git 历史、文件路径）——见 `WHAT_NOT_TO_SAVE_SECTION`。

其余载体：
- 助手模式日志（KAIROS）：`memory/logs/YYYY/MM/YYYY-MM-DD.md`，仅追加，由夜间 `/dream` 蒸馏进 MEMORY.md
- 团队记忆：`join(getAutoMemPath(), 'team')`（`memdir/teamMemPaths.ts`）
- Agent 记忆（`tools/AgentTool/agentMemory.ts`）三档作用域：`user` → `~/.claude/agent-memory/<agentType>/`；`project` → `<cwd>/.claude/agent-memory/<agentType>/`；`local` → `<cwd>/.claude/agent-memory-local/<agentType>/`
- Session Memory（`utils/permissions/filesystem.ts`）

```ts
// Path format: {projectDir}/{sessionId}/session-memory/summary.md
export function getSessionMemoryPath(): string {
  return join(getSessionMemoryDir(), 'summary.md')
}
```

目录权限 `0o700`、文件权限 `0o600`。

CLAUDE.md 四层（`utils/claudemd.ts` 头部注释）：Managed（`/etc/claude-code/CLAUDE.md`）→ User（`~/.claude/CLAUDE.md`）→ Project（`CLAUDE.md`、`.claude/CLAUDE.md`、`.claude/rules/*.md`）→ Local（`CLAUDE.local.md`）。单文件建议上限 `MAX_MEMORY_CHARACTER_COUNT = 40000`。

### 2.3 写入时机（双轨 + 主 Agent 互斥）

**轨一：主 Agent 直接写。** 系统提示注入完整保存指令（`buildMemoryLines`），`MEMORY.md` 始终在上下文中；用户明说「记住」时立即保存，「忘记」时找到并删除对应条目。

**轨二：后台提取 Agent**（`services/extractMemories/extractMemories.ts`）：
- 每个完整 query loop 结束时（模型产出无工具调用的最终回复）经 `handleStopHooks` 触发一次
- 用 `runForkedAgent` 完美分叉主对话以**共享 prompt cache**
- 与轨一**互斥**：若本轮主 Agent 已写记忆（`hasMemoryWritesSince` 检测 Write/Edit 命中 memory 路径），后台 Agent 跳过并推进游标

**轨三：Session Memory 提取**（`services/SessionMemory/sessionMemory.ts`）：
- 注册为 post-sampling hook，仅主 REPL 线程（`querySource === 'repl_main_thread'`）
- 阈值（`DEFAULT_SESSION_MEMORY_CONFIG`）：`minimumMessageTokensToInit: 10000`、`minimumTokensBetweenUpdate: 5000`、`toolCallsBetweenUpdates: 3`
- 触发条件：**(token 达标 且 工具调用达标) 或 (token 达标 且 末轮无工具调用)**，token 阈值**始终必需**
- 写入用 forked agent，权限被收窄到**只允许 Edit 那一个记忆文件**（`createMemoryFileCanUseTool`）
- 模板十节：Session Title / Current State / Task specification / Files and Functions / Workflow / Errors & Corrections / Codebase and System Documentation / Learnings / Key results / Worklog

### 2.4 检索时机

**常驻注入**：`MEMORY.md` 与 CLAUDE.md 系列经 `getMemoryFiles()` 进入系统提示，每轮都在。

**按需语义检索**（`memdir/findRelevantMemories.ts`）：

```
scanMemoryFiles(memoryDir)          // 扫描 .md，排除 MEMORY.md，上限 200
  → formatMemoryManifest()          // [type] filename (timestamp): description
  → sideQuery(Sonnet, json_schema)  // 选出最多 5 个最相关文件
```

- 用 **Sonnet** 作选择器，返回 `selected_memories`（≤5 个）
- 过滤 `alreadySurfaced`（前几轮已展示路径），把名额留给新候选
- 传入 `recentTools`，避免重复注入正在使用工具的参考文档

**时效性防护**（`memdir/memoryAge.ts`）：

```ts
export function memoryAge(mtimeMs: number): string {
  if (d === 0) return 'today'
  if (d === 1) return 'yesterday'
  return `${d} days ago`
}
```

源码注释原话：Models are poor at date arithmetic — a raw ISO timestamp does not trigger staleness reasoning the way 「47 days ago」 does。超过 1 天的记忆会附加 `<system-reminder>`，提示「这是时间点观察，代码行为与 file:line 引用可能已过期，断言前先核实」；配合 `MEMORY_DRIFT_CAVEAT` 与 `TRUSTING_RECALL_SECTION`（要求 grep 验证函数/文件是否存在）。

**历史回溯**：门控 `tengu_coral_fern` 开启时（`buildSearchingPastContextSection`），先 grep 记忆目录 `*.md`，**transcript `*.jsonl` 仅作最后手段**（大文件、慢）。

### 2.5 上下文窗口管理与压缩（`services/compact/`）

阈值体系（`autoCompact.ts`）：

```ts
const MAX_OUTPUT_TOKENS_FOR_SUMMARY = 20_000   // p99.99 摘要输出 17,387
export const AUTOCOMPACT_BUFFER_TOKENS = 13_000
export const WARNING_THRESHOLD_BUFFER_TOKENS = 20_000
export const MANUAL_COMPACT_BUFFER_TOKENS = 3_000
const MAX_CONSECUTIVE_AUTOCOMPACT_FAILURES = 3  // 熔断器
```

- 有效窗口 = `contextWindow − min(maxOutputTokens, 20000)`
- 自动压缩阈值 = 有效窗口 − 13000；阻塞上限 = 有效窗口 − 3000
- 熔断器：连续失败 3 次后本会话不再尝试（BQ 2026-03-10：1279 个会话曾 50+ 次连续失败，日均浪费约 25 万次 API 调用）

三级压缩，由廉价到昂贵（`autoCompactIfNeeded`）：

```
① trySessionMemoryCompaction()   // 无 API 调用，最廉价
② microcompactMessages()         // 清理旧工具结果
③ compactConversation()          // 完整 LLM 摘要
```

**① Session Memory 压缩**（`sessionMemoryCompact.ts`）——直接把已有 session memory 当摘要，只保留 `lastSummarizedMessageId` 之后的近期消息：

```ts
export const DEFAULT_SM_COMPACT_CONFIG = {
  minTokens: 10_000, minTextBlockMessages: 5, maxTokens: 40_000,
}
```

关键函数 `adjustIndexToPreserveAPIInvariants` 防止**拆散 tool_use / tool_result 对**与**丢失 thinking 块**（源码详述了两类真实的 orphan tool_result / thinking 丢失 bug 场景）。

**② Micro-compaction**（`microCompact.ts`）——仅清理 `COMPACTABLE_TOOLS`（Read/Bash/Grep/Glob/WebSearch/WebFetch/Edit/Write）的旧工具结果，替换为 `[Old tool result content cleared]`。

**③ 传统完整压缩**（`compact.ts`，1581 行）：
- 摘要前先跑 microcompact 降 token
- 摘要 prompt（`compact/prompt.ts`）以 `NO_TOOLS_PREAMBLE` 开头强制纯文本：`CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.`
- 要求先写 `<analysis>` 草稿块（`formatCompactSummary` 会剥离），再输出 9 节结构化 `<summary>`：Primary Request and Intent / Key Technical Concepts / Files and Code Sections / Errors and fixes / Problem Solving / **All user messages** / Pending Tasks / Current Work / Optional Next Step
- 另有 **partial compact**（`up_to` / `from` 两种方向）
- PTL 重试：`truncateHeadForPTLRetry`、`MAX_PTL_RETRIES = 3`

压缩后恢复预算：

```ts
export const POST_COMPACT_MAX_FILES_TO_RESTORE = 5
export const POST_COMPACT_TOKEN_BUDGET = 50_000
export const POST_COMPACT_MAX_TOKENS_PER_FILE = 5_000
export const POST_COMPACT_SKILLS_TOKEN_BUDGET = 25_000
```

重建：文件附件、plan 附件、skill 附件、`processSessionStartHooks('compact')` 重新注入 CLAUDE.md；`buildPostCompactMessages` 组装 `[boundaryMarker, summaryMessages, ..., messagesToKeep]`。

### 2.6 压缩后清理（`services/compact/postCompactCleanup.ts`）

每次压缩后重置：`resetMicrocompactState`、`resetContextCollapse`（仅主线程）、**`getUserContext.cache.clear()`**（关键：否则外层 memo 缓存会挡住内层 `getMemoryFiles` 的失效信号，导致已武装的 InstructionsLoaded hook 永不触发）、`resetGetMemoryFilesCache('compact')`、`clearSessionMessagesCache`。明确**保留** invoked skills（Skill content must survive across multiple compactions）。

另有 `CONTEXT_COLLAPSE` 模式：以 90% 提交 / 95% 阻塞自管上下文，此时**主动抑制 autocompact** 以免竞争。

### 2.7 清除 / 重置机制

**功能开关** `isAutoMemoryEnabled()` 优先级链：
1. `CLAUDE_CODE_DISABLE_AUTO_MEMORY`（1/true → 关，0/false → 开）
2. `CLAUDE_CODE_SIMPLE`（`--bare`）→ 关（系统提示中直接丢弃 memory 段）
3. 远程模式但无 `CLAUDE_CODE_REMOTE_MEMORY_DIR` → 关
4. 设置项 `autoMemoryEnabled`（支持项目级 opt-out）

**`/clear`**（`commands/clear/conversation.ts`）：`regenerateSessionId()` + `clearSessionCaches()` + 清 `loadedNestedMemoryPaths` + 清 plan slug —— **不删除任何记忆文件**（记忆是项目级，跨会话存活）。

**提示层遗忘**：让模型自行找到并删除对应条目。

**容量告警**：`getLargeMemoryFiles` 对 >40,000 字符文件告警；`MEMORY.md` 超限时截断且上下文留 WARNING，提示把细节移入主题文件。

**压缩后状态重置**：`setLastSummarizedMessageId(undefined)`（消息被裁剪后旧 UUID 已不存在）。

---

## 3. 代码位置索引

| 机制 | 文件 |
|---|---|
| 记忆目录解析与校验 | `memdir/paths.ts`、`memdir/memdir.ts` |
| 记忆分类法 / 类型 | `memdir/memoryTypes.ts` |
| 相关性检索 | `memdir/findRelevantMemories.ts`、`memdir/memoryScan.ts` |
| 时效衰减 | `memdir/memoryAge.ts` |
| 团队记忆 | `memdir/teamMemPaths.ts`、`memdir/teamMemPrompts.ts` |
| Agent 记忆 | `tools/AgentTool/agentMemory.ts` |
| 会话记忆提取 | `services/SessionMemory/sessionMemory.ts`、`sessionMemoryUtils.ts`、`prompts.ts` |
| 记忆写入 Agent | `services/extractMemories/extractMemories.ts` |
| 夜间蒸馏 | `services/autoDream/autoDream.ts`、`tasks/DreamTask/DreamTask.ts`、`skills/bundled/remember.ts` |
| 自动压缩决策 | `services/compact/autoCompact.ts` |
| 完整压缩 | `services/compact/compact.ts`、`compact/prompt.ts` |
| 会话记忆压缩 | `services/compact/sessionMemoryCompact.ts` |
| 微压缩 | `services/compact/microCompact.ts`、`apiMicrocompact.ts` |
| 压缩后清理 | `services/compact/postCompactCleanup.ts` |
| CLAUDE.md 加载 | `utils/claudemd.ts` |
| 记忆文件识别 | `utils/memoryFileDetection.ts` |
| 会话落盘 / transcript | `utils/sessionStorage.ts` |
| 命令入口 | `commands/{memory,compact,context,clear,remember}/` |

---

## 4. 局限与注意点

1. **本分析基于逆向还原源码**，非官方发布版本；sourcemap 还原可能丢失被 tree-shake 的 `feature(...)` 分支（如 KAIROS / TEAMMEM / PROACTIVE / CONTEXT_COLLAPSE 门控代码在外部构建中被死代码消除）。
2. **大量行为由远程配置决定**：`tengu_session_memory`、`tengu_sm_compact`、`tengu_coral_fern`、`tengu_scratch`、`tengu_sm_compact_config` 等 GrowthBook 门控，本地默认值未必等于线上行为。
3. **压缩是有损的**：完整压缩依赖 LLM 摘要，`All user messages` 一节虽要求列举全部用户消息，仍可能失真；三级压缩中只有第 ① 级是无损复用（无额外 API 调用）。
4. **记忆写入的边界全靠提示词约束**：分类法与排除规则（不存代码模式/架构/git 历史）是 prompt 级约束，模型可能违反；`MEMORY.md` 有硬性 200 行 / 25KB 上限，长项目易截断。
5. **时效陷阱已被官方修复但需知悉**：记忆是「写下时的观察」，代码会漂移；系统靠相对时间表述 + 漂移告警 + 要求 grep 复核来缓解。
6. **多 worktree 共享记忆**：以 git root 为键，同一仓库不同 worktree 共享一份记忆，可能造成上下文串味。
7. **法律与合规**：该源码为未授权泄露物，仅限本地研究，不应再分发或用于生产。

---

## 5. 结论

Claude Code 的记忆力本质是一套**以文件为唯一持久化介质、以「索引 + 主题文件」为组织方式、以 LLM 摘要为压缩手段**的工程化方案：

- **写**：主 Agent 即时写 + 后台 forked agent 兜底提取（互斥去重）+ 会话记忆 hook 定期提取，三轨并行；
- **存**：项目级 `MEMORY.md` 索引 + 分类主题文件，配 session memory、transcript、四层 CLAUDE.md；
- **取**：索引常驻 + Sonnet 语义筛选 ≤5 篇 + 相对时间衰减提示 + grep 复核要求；
- **压**：会话记忆压缩 → 微压缩 → 完整结构化摘要的三级递进，带 13k 缓冲阈值与 3 次失败熔断器，压缩后按 50k token 预算恢复关键文件与 CLAUDE.md；
- **删**：功能开关 + `/clear` 仅重置会话 + 提示层遗忘 + 容量告警。

其精髓不在于算法，而在于**把「什么值得记、什么时候记、什么时候必须重新核实」变成显式的提示词工程与阈值工程**。