# P2 撤销 / P3 子代理 —— 实现级方案

> - 定位：设计文档（`docs/agent-gaps-implementation-plan.md` 第五、六章）给的是架构与取舍；**本文给的是可直接照着写码的规格**——文件、函数签名、改动顺序、测试清单、回退点。
> - 状态：未实施。所有签名都与当前代码对齐，所有 `文件:行` 引用都核对过。
> - 阅读顺序：先看第零节（实测结论），它修正了设计文档里两处不准确的说法，直接决定 P2 的算法形状。

## 零、已实机验证的前提

设计文档把这几条列为"实现前先确认"。已经在沙箱里用真实 git 仓库跑过了，结论如下——其中两条**修正了原文的说法**。

| 结论 | 验证方式 | 对方案的影响 |
| --- | --- | --- |
| `git stash create` **不含未跟踪文件** | 造未跟踪文件后 `stash create`，`git cat-file -e <sha>:<file>` 全部失败 | 撤销**不能复用** `TurnSnapshot`（`diff.go:275`），必须另造含未跟踪文件的快照 |
| plumbing 快照可行且**不污染真实索引** | `GIT_INDEX_FILE=<临时索引> git add -A` + `write-tree` + `commit-tree`，之后 `git status` 仍原样显示 ` M` 与 `??` | 快照实现确定用这条路径 |
| **不打 ref 的悬空 commit 会被回收** | `git gc --prune=now` 后 `git cat-file -e <sha>` 失败（第一次测时 `stash create` 返回空串、测试无效，重测才确认） | `update-ref` 钉住是**必需步骤**，不是加固 |
| 回退动作成立 | `git restore --source=<快照> -- <path>` 能恢复修改与删除；删除新增文件符合预期 | 算法可用 |
| 附加发现：**只有未跟踪改动时 `stash create` 返回空串** | 只新建未跟踪文件、不改已跟踪文件时，`git stash create` 无输出 | `Snapshot`（`diff.go:408`）此时会回落到 `rev-parse HEAD`——"该轮基线"的含义与预想不同，方案里要显式处理 |

**一个直接结论**：设计文档里"回退时按 `DiffFile.Status` 分派"的说法**不够**。因为 `Status` 是 `DiffScoped` 用 `turnBase`（不含未跟踪文件）算出来的，一个"轮次之前就存在、但一直未跟踪、本轮被改"的文件会被报成 `added`——只看 Status 就会把它删掉，而它恰恰是用户手写的文件。**必须用含未跟踪文件的 checkpoint 复核**，这正是 checkpoint 存在的理由。下面 1.5 的算法以此为准。

## 一、P2 撤销

### 1.1 改动清单

| 文件 | 改动 | 规模 |
| --- | --- | --- |
| `checkpoint.go` | **新增**：含未跟踪文件的快照、ref 钉住、回退原语 | ~220 行 |
| `diff.go` | `DiffTurn` 加 `Base` 与 `EndState`；`DiffService` 加 turn 级 checkpoint 的取用 | ~30 行 |
| `chat.go` | 轮次开头取 checkpoint；轮末记录 `Base` 与 `EndState` | ~20 行 |
| `app.go` | 新增 `ListCheckpoints` / `UndoDiffTurn` 两个 bound 方法 | ~90 行 |
| `sessions.go` | `AppendDiff` 签名扩参 | ~10 行 |
| `checkpoint_test.go` | **新增**测试（沙箱内可真跑） | ~300 行 |
| `frontend/src/panes/DiffPane.vue` | 轮次选择器旁加回退按钮与冲突确认 | ~60 行 |
| `frontend/src/stores/diff.js`、`api/session.js` | 调用新接口 | ~30 行 |
| `frontend/wailsjs/go/*` | 手工同步两个绑定 + models.ts 加类 | ~40 行 |

### 1.2 数据模型

`DiffTurn`（`diff.go:45`）加两个字段。**不新增存储文件**——会话 JSON 已经在存 `Diffs`，复用它的生命周期（会话删了，checkpoint 也该清理）：

```go
type DiffTurn struct {
	Turn      int        `json:"turn"`
	Label     string     `json:"label"`
	Files     []DiffFile `json:"files"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	CreatedAt int64      `json:"createdAt"`

	// Base 本轮开始时的 checkpoint 快照 sha（含未跟踪文件）。空表示该轮不可回退
	// （非 git 仓库、或取快照失败——失败不阻断对话，只是这一轮不提供撤销）。
	Base string `json:"base,omitempty"`
	// EndState 本轮结束时各触碰路径的内容状态：blob 哈希；空串表示"该轮结束时不存在"。
	// 用于回退前判断"这些文件在本轮之后是否又被改过"——是对用户手工改动的保护。
	EndState map[string]string `json:"endState,omitempty"`
}
```

```go
// CheckpointInfo 一轮的回退可用性（供前端决定按钮是否可点）
type CheckpointInfo struct {
	Turn      int      `json:"turn"`
	Label     string   `json:"label"`
	Available bool     `json:"available"` // 有 Base 且 ref 仍可解析
	Reason    string   `json:"reason,omitempty"` // 不可回退的原因
	Files     []string `json:"files"`             // 将被回退的路径
	Conflicts []string `json:"conflicts"`         // 本轮之后又被改过的路径
}

// UndoFileResult 逐文件结果：部分失败不能当成整体失败
type UndoFileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"` // restore | delete | skip
	Err    string `json:"err,omitempty"`
}

type UndoResult struct {
	Turn    int              `json:"turn"`
	Files   []UndoFileResult `json:"files"`
	Failed  int              `json:"failed"`
	Skipped int              `json:"skipped"`
}
```

### 1.3 checkpoint 层（新文件 `checkpoint.go`）

```go
// checkpointRef 每轮一个 ref，钉住悬空 commit 不被 gc 回收（实测必需）
func checkpointRef(sessionID string, turn int) string

// Checkpoint 造一份**含未跟踪文件**的工作区快照并打 ref 钉住，返回 sha。
// 全程只写临时索引，不碰真实索引与工作区。
// sessionID 为空或 turn<=0 时返回错误（不静默退化成无快照）。
func Checkpoint(dir, sessionID string, turn int) (string, error)

// ResolveCheckpoint 解析某轮的 checkpoint sha；ref 已被清理时返回 ok=false
func ResolveCheckpoint(dir, sessionID string, turn int) (string, bool)

// DropSessionCheckpoints 会话删除时清理它名下所有 checkpoint ref
func DropSessionCheckpoints(dir, sessionID string) error

// PruneCheckpoints 只保留最近 keep 轮，更早的删 ref 让 git 自然回收
func PruneCheckpoints(dir, sessionID string, keep int) error

// pathInCheckpoint 该路径在快照里是否存在（撤销算法区分"新建"与"改了未跟踪文件"的依据）
func pathInCheckpoint(dir, sha, rel string) bool

// blobHashAt 取快照里某路径的 blob 哈希；不存在返回 ""
func blobHashAt(dir, sha, rel string) string

// hashWorktreeFile 取工作区文件当前内容哈希；文件不存在返回 ""
func hashWorktreeFile(dir, rel string) string

// restoreFromCheckpoint 把某个路径恢复到快照状态
func restoreFromCheckpoint(dir, sha, rel string) error
```

**`Checkpoint` 实现要点**（伪代码，已实测）：

```
tmpIndex := <临时文件路径>            // 用 os.CreateTemp，别放在仓库里
env      := append(os.Environ(), "GIT_INDEX_FILE="+tmpIndex)
runGitEnv(dir, env, "add", "-A")
tree   := runGitEnv(dir, env, "write-tree")
commit := runGit(dir, "commit-tree", tree, "-p", "HEAD", "-m", "checkpoint")
runGit(dir, "update-ref", checkpointRef(sessionID, turn), commit)
```

四处必须注意：

1. **`GIT_INDEX_FILE` 必须显式设置**，这是"不污染真实索引"的关键；实测之后 `git status` 完全不变。
2. 临时索引文件要在 `defer` 里删掉，并且**别放进仓库目录**——放 `os.TempDir()`，否则会被 `add -A` 自己收进快照，形成自我指涉。
3. 仓库为空（没有 HEAD）时 `commit-tree -p HEAD` 会失败。**降级**：不带 `-p` 造一个无父 commit。
4. 这个函数的失败**不能阻断对话**——调用方只记日志、该轮 `Base` 留空、UI 显示"本轮不可回退"。

**为什么不在现有 `Snapshot` 上改**：`Snapshot`（`diff.go:408`）的形状被 `DiffScoped` 依赖（它另外单独处理未跟踪文件，`diff.go:318`）。若把 `Snapshot` 换成含未跟踪的完整快照，未跟踪文件会在两条路径上被**重复计算**，diff 面板立刻出问题。**两种快照各司其职、在同一时刻各取一份**，这是本方案的一条硬约束。

### 1.4 取快照的时机（`chat.go` 改动点）

`chat.go:637` 附近那段本来就在取 `turnBase`：

```go
	if isRepo {
		a.diffService.RestoreSession(sessionID, session.DiffBaseline, session.DiffTouched)
		a.diffService.EnsureBaseline(sessionID, dir)
		a.diffService.BeginTurn(sessionID)
		turnBase = a.diffService.TurnSnapshot(dir)   // ← 保持不变，继续用于算 diff
	}
```

在其后**追加**（不是替换）：

```go
	// 撤销用的 checkpoint：含未跟踪文件，且必须打 ref 钉住。
	// 与 turnBase 刻意分开——那份用于算 diff，不含未跟踪文件；这份用于回退。
	// 取不到不算错误：本轮只是不可回退，对话照常。
	turnNo := len(session.Diffs) + 1
	if cp, err := Checkpoint(dir, sessionID, turnNo); err == nil {
		turnCheckpoint = cp
	} else {
		fmt.Printf("[checkpoint] 本轮快照失败，该轮不可回退: %v\n", err)
	}
```

轮末（`chat.go:964`）记录时把 `Base` 与 `EndState` 一起写进去：

```go
	turnNo, appendErr := a.sessionStore.AppendDiff(sessionID, DiffTurn{
		Files:     files,
		Additions: sumAdd(files),
		Deletions: sumDel(files),
		CreatedAt: time.Now().UnixMilli(),
		Base:      turnCheckpoint,
		EndState:  endStateOf(dir, files),   // 逐路径取当前哈希；不存在记 ""
	}, base, touched)
```

`endStateOf` 是新增的小helper：遍历 `files` 的 `Path` 调 `hashWorktreeFile`。注意 `deleted` 的文件会得到 `""`，这正是我们要的语义。

**`turnNo` 的计算依赖 `len(session.Diffs)+1`，与 `AppendDiff` 内部的编号规则一致**（`sessions.go` 里 `turn.Turn = len(session.Diffs) + 1`）。两处必须用同一规则，否则 checkpoint ref 的编号会和 `DiffTurn.Turn` 错位——建议把编号计算抽成一个函数，两处共用。

### 1.5 回退算法

输入 `sessionID`、`turn`、`force`。步骤：

**第一步：取材料。** `session.Diffs` 里找 `Turn == turn` 的那一项；`Base` 为空 → 返回"本轮不可回退（无快照）"。`ResolveCheckpoint` 校验 ref 仍在 → 不在则返回"快照已被清理"。

**第二步：冲突检查（对用户手工改动的保护）。** 对每个待回退路径，比对 `hashWorktreeFile(dir, path)` 与 `EndState[path]`：不一致 → 该路径在本轮之后又被改过，进 `Conflicts`。`force=false` 且 `Conflicts` 非空 → **不执行任何改动**，把 `CheckpointInfo` 回给前端让用户确认。

**第三步：逐路径分派。** 关键是用 **checkpoint 复核**，而不是只信 `Status`：

| 记录里的 Status | 快照里是否存在 | 动作 |
| --- | --- | --- |
| `added` | **存在** | `restoreFromCheckpoint` —— 它本轮之前就在，只是未跟踪。**不能删** |
| `added` | 不存在 | 删除文件；清理因此变空的目录 |
| `modified` | 存在 | `restoreFromCheckpoint` |
| `deleted` | 存在 | `restoreFromCheckpoint`（快照里有，能恢复） |
| `renamed` | — | 先按 `OldPath` 恢复，再按上面的规则处理 `Path` |

**第四步：逐文件记录结果。** 每个路径产出 `UndoFileResult`；单个失败（文件被占用、权限）只影响该项，**不中断整体**，最后汇总 `Failed`/`Skipped`。

**第五步：刷新状态。** `a.diffService` 的归因集合里移除这些路径（新增一个 `DropTouched(sessionID, paths)`），重算累计 diff 并 `EventsEmit("diff:update")`；`DiffTurn` 标记为已回退（加一个 `Undone bool`）而不是删除记录——用户可能要回退后重新执行，历史应留痕。

**幂等性**：连续回退同一轮应当无害（第一次已把文件恢复到位，第二次 `restoreFromCheckpoint` 是同结果写）。

### 1.6 接口与 UI

```go
// ListCheckpoints 列出会话各轮的回退可用性与冲突情况
func (a *App) ListCheckpoints(sessionID string) ([]CheckpointInfo, error)

// UndoDiffTurn 回退某一轮；force=true 时忽略冲突照常覆盖
func (a *App) UndoDiffTurn(sessionID string, turn int, force bool) (*UndoResult, error)
```

UI 落在 `frontend/src/panes/DiffPane.vue`：轮次选择器（第 79-84 行那个 `<select>`）右侧加一个「回退本轮」按钮，绑定当前 `activeTurn`（`stores/diff.js:11`）。点开先调 `ListCheckpoints` 拿到该轮的 `Available`/`Conflicts`——不可回退时按钮置灰并用 `title` 说明原因；有冲突时用应用内确认框（`ui.ask`，注意 `danger: true`）列出冲突文件，确认后带 `force=true` 重发。**不要用原生 `confirm`**，这个项目里它被验证过会静默失效。

完成后用 `ui.notify` 汇报逐文件结果，并触发 diff 面板刷新。

### 1.7 改动顺序（四步，每步都能单独编译与验证）

1. **`checkpoint.go` + 它的测试。** 只加新文件，零改动既有代码。沙箱里就能真跑测试（见 1.9），这一步做完就已经有把握。
2. **数据模型与记录。** `DiffTurn` 加 `Base`/`EndState`；`AppendDiff` 扩参；`chat.go` 取快照并写入。此步之后功能不可见，但会话 JSON 里开始有 `base` 字段——**可以先用它验证快照确实被写下且 ref 可解析**。
3. **回退算法 + bound 方法。** `app.go` 的两个接口 + `DropTouched`。此时用 curl/前端控制台调 `UndoDiffTurn` 即可端到端验证，不必等 UI。
4. **前端 UI。** DiffPane 按钮 + store + wailsjs 同步。

### 1.8 回退点（每步怎么退）

第 1 步删文件即可。第 2 步只加字段，旧会话 JSON 反序列化后 `Base` 为空 → 自然走"不可回退"分支，无需数据迁移。第 3、4 步都是新增接口/入口，删掉即回到上一步行为。**没有任何一步会破坏既有 diff 功能**——这是把 checkpoint 与 `Snapshot` 分开的直接好处。

### 1.9 测试清单（`checkpoint_test.go`）

**先说清一件事：沙箱里没有 Go 工具链，所以这批 Go 测试跑不了。**
方案初稿里我写成"P2 能在沙箱真跑测试、因为它依赖 git 而不是 Go 工具链"——那句话是错的，Go 测试当然需要 Go。
真正成立的是**另一件事**：P2 依赖的外部行为（git 的几条机制）可以在沙箱里用 shell 独立验证，
所以**实现的风险面比 P1/P3 小得多**——逻辑层仍然要靠本地 `go test` 验收。

用一个 `t.TempDir()` 里 `git init` 的真实仓库（复用 `diff_test.go` 已有的 `newTestRepo`）：

1. `TestCheckpointIncludesUntracked` —— 造"已跟踪改动 + 未跟踪新文件"，断言两者都在快照里（数据丢失场景的正向验证）。
2. `TestCheckpointDoesNotTouchRealIndex` —— 取快照后 `git status --porcelain` 与之前**逐字节一致**。
3. `TestCheckpointSurvivesGC` —— 快照扛住 `git gc --prune=now`；并硬断言 ref 存在且指向它。
4. `TestUndoRestoresPreExistingUntrackedFile` —— **本方案最重要的回归测试**：轮次前就存在但未跟踪的文件、本轮被改，回退后内容**恢复**而不是被删除。
5. `TestUndoDeletesTrulyNewFile` —— 本轮真正新建的文件被删除，空目录被清理。
6. `TestUndoRestoresDeletedFile` / `TestUndoRestoresModifiedFile` —— 恢复到**快照**状态而不是 HEAD。
7. `TestUndoDetectsConflict` —— 之后手工改一下，断言进 `Conflicts` 且 `force=false` 时**一个字节都没动**，`force=true` 才覆盖。
8. `TestUndoIsIdempotent` —— 连做两次结果一致。
9. `TestUndoRejectsMissingSnapshot` —— 无 `Base`、轮次不存在、ref 被清理三种情形都明确报错。
10. `TestRecordedStatusForUntrackedFileIsAddedAndUndoRestores` —— **端到端前提验证**：走真实流程（`DiffScoped` + `turnBase` + checkpoint）跑一轮，断言预存在的未跟踪文件确实被报成 `added`，而回退仍能恢复它、真正新建的仍被删除。同样是 `added`、动作必须不同——这条把设计前提钉死。
11. `TestCheckpointOnEmptyRepo` / `TestCheckpointRejectsBadArgs` / `TestValidRefSegment` / `TestDropAndPruneCheckpoints` / `TestBlobHashAndPathInCheckpoint` / `TestRestoreFromCheckpoint` / `TestRemoveWorktreeFileCleansEmptyDirs` / `TestEndStateOf` —— 边界与参数校验。

### 1.10 边界与已知限制

**非 git 仓库不支持**（`IsRepo` 为假时 `ListCheckpoints` 直接返回空 + 原因）。**二进制文件与被 `maxDiffFileBytes`（1MB）跳过的文件不在回退范围**——`DiffFile` 里本来就没有它们，UI 上必须写清楚，不能让用户以为"点了回退就全都回去了"。**checkpoint 会在会话删除时清理**，但要额外接一处：`DeleteSession` 里调 `DropSessionCheckpoints`，否则 ref 会永久残留（这也是唯一会污染用户仓库 `refs/` 的地方，必须在文档和 UI 上都说明——它在 `refs/local-agent/` 下，不影响 `git log` 与推送）。

**回退不触碰未跟踪且本轮无关的文件**：算法只遍历 `turn.Files`，不会做 `git clean` 或整树 restore。

**硬取消（P1-A）留下的半成品同样走这条路回退**——两条功能在这里会师，是设计上的有意衔接。

## 二、P3 子代理

### 2.1 改动清单与前置判断

`runToolLoop` 有 **391 行**，直接依赖 `App` 的 **12 处**（实测统计）：`diffService`(10)、`sessionStore`(7)、`emitChatEvent`(4)、`ctx`(4)、`fileChanges`(3)、`compactSession`(2)、`toolManager`(1)、`startDeltaFlusher`(1)、`resolveProjectDir`(1)、`reqLog`(1)、`newPermissionEnforcer`(1)、`askUser`(1)。

这个规模决定了：**P3 必须先把"一次运行"的依赖抽出来，让主会话与子代理共用同一个循环**，否则要么复制一份循环（两份逻辑必然漂移），要么让子代理假装成一个会话（会把 UI 状态搞乱）。这一步是纯重构、**行为不变**，应当独立成一期并单独验收。

| 文件 | 改动 | 规模 |
| --- | --- | --- |
| `agentrun.go` | **新增**：`agentRun` 依赖结构 + `runRecorder` 接口 + 从 `runToolLoop` 迁出的循环主体 | ~420 行（大部分是从 `chat.go` 平移） |
| `chat.go` | `runToolLoop` 变成薄封装：组装 `agentRun` 后交给新循环 | 净减 ~350 行 |
| `subagent.go` | **新增**：`spawn_agent` 工具、子代理注册表、上限与并发控制、结果契约 | ~300 行 |
| `permission_app.go` | 判定与等待分离（`Decide` / `AuditDecision`）+ `subagentEnforcer` 包装 | ~120 行 |
| `permission.go` | 新增 `StageSubagentDeclined` 常量（加在 937 行起那组） | ~1 行 |
| `subagent_test.go` | **新增**测试 | ~350 行 |
| `frontend/src/panes/SubagentPane.vue` | 从空壳改成真实面板 | ~200 行 |
| `frontend/src/panes/TasksPane.vue` | **不动**（它是主会话待办清单的位置，属于另一件事） | 0 |

### 2.2 第一步：把执行核心参数化（纯重构）

目标形状：

```go
// agentRun 一次运行的完整依赖。主会话与子代理走同一条循环实现。
type agentRun struct {
	RunID     string
	SessionID string // 主会话 ID；子代理填父会话 ID（便于权限继承与归因）
	// WorkspaceKey 归因用的键：主会话=sessionID；子代理="<sessionID>#sub-<runID>"。
	// 独立归因让子代理的改动不污染父会话的 diff turn，同时各自可回退。
	WorkspaceKey string

	Messages     []Message // 本次运行的起始消息（子代理是独立序列）
	SystemPrompt string
	Model        *Model
	ToolView     *SessionView
	Ctx          context.Context
	Recorder     runRecorder
	Compact      bool
	MaxTurns     int
	IsRepo       bool
	Dir          string
}

// runRecorder 事件与持久化的抽象。主会话写 SessionStore + wails 事件；
// 子代理写自己的文件 + 自己那条事件通道。循环只依赖这个接口。
type runRecorder interface {
	AppendMessage(sessionID string, msg Message) (*Message, error)
	Emit(ev ChatEvent)
	// RecordLLMRequest 只在主会话实现；子代理忽略即可
	RecordLLMRequest(snap *LLMRequestSnapshot)
	FlushStream() (enqueue func(string), shutdown func())
}
```

迁移顺序（每步保持可编译、测试全绿）：

1. 加 `agentRun`/`runRecorder` 与 `mainSessionRecorder`（把现有 `a.sessionStore` + `a.emitChatEvent` + `a.startDeltaFlusher` 包进去）。
2. 把循环主体整体搬到 `agentrun.go`，签名改为接收 `*agentRun`；`chat.go` 的 `runToolLoop` 变成组装参数 + 调用。**此步不做任何行为改动**，跑既有测试应全绿。
3. 把 `a.diffService` / `a.fileChanges` / `a.reqLog` / `a.compactSession` 的调用改成通过 `agentRun` 的字段或 recorder 传递。
4. 把 `a.newPermissionEnforcer` 与 `a.askUser` 改成 `agentRun` 上的构造回调——这两处是子代理唯一需要换实现的地方（见 2.5）。

**验收标准**：主会话行为与改造前完全一致（跑既有 `chat_stream_test` / `plan_test` / `contextmgmt_test`），且 `runToolLoop` 里不再出现 `a.` 前缀的调用。

### 2.3 `spawn_agent` 工具契约

```go
// 参数
{
  task:        string  // 必填。要子代理做的事，越具体越好
  maxTurns:    int     // 可选，默认 20，硬上限 30
  // 刻意不做"能力声明"：按已定决策是完整能力型，能力集固定，
  // 少一个参数就少一种配置漂移
}
// 返回：SubagentResult 序列化后的文本（Summary 截断到 4K 字符）
```

上限（**缺任何一条都会出现不可控的账单**）：单次运行最大并发子代理 4；单 run 总派生数 16；单个子代理最大轮次 30；`Summary` 4K 字符。超限时 `spawn_agent` 直接返回错误并说明原因，让模型自己收敛，而不是排队等待。

### 2.4 子代理的工具视图

由父会话的 `SessionView` 减去两个工具：`spawn_agent`（**禁止嵌套派生**——否则一个跑飞的模型能派生无限层）与 `ask_user`（不可交互，留着只会挂到超时）。其余照旧。父会话的工具白名单与技能白名单照常继承，这样"我这个会话只开了这些工具"的意图对子代理同样生效。

实现上不要复用 `SessionView` 的 map（它是装配好的、被 `guardedTool` 包过的共享结构）。**用 `BuildView` 重新装配一份**，`BuildOptions` 里传 `EnabledTools` 减去那两个名字 + 子代理自己的 `SessionID`/`ProjectDir`/`Enforcer`/`Changes`。重装配的成本是可控的（MCP 连接有池化缓存）。

### 2.5 权限：不可交互的网关

**先看清父网关的结构**（`permission_app.go` 的 `permissionEnforcer.Enforce`，实测形态）：

```go
func (e *permissionEnforcer) Enforce(ctx, tool, args) error {
	subject := e.subjectFor(tool, args)
	if subject.Kind == SubjectCommand && strings.TrimSpace(subject.Raw) == "" {
		e.audit(subject, Verdict{Decision: DecisionDeny, ...})
		return fmt.Errorf("命令为空：请检查是否把参数名写成了 cmd")
	}
	verdict := Authorize(AuthorizeInput{Subject: subject, Mode: e.mode, Rules: e.rules,
		Grants: e.app.permissionGrants.List(e.sessionID), ProjectDir: e.projectDir})
	e.audit(subject, verdict)
	switch verdict.Decision {
	case DecisionAllow:
		return nil
	case DecisionDeny:
		return fmt.Errorf("权限拒绝（%s）：%s", verdict.Stage, verdict.Reason)
	default:
		return e.askAndApply(ctx, subject, verdict)   // ← 只有这一支会阻塞
	}
}
```

阻塞只发生在 `askAndApply`（内部等 `permissionBroker.Wait`，`permission_app.go:358`，默认 5 分钟）。所以**包装器绝不能调父的 `Enforce`**——那样照样挂住，而且表现为间歇性的"子代理偶尔卡 5 分钟"，极难复现。

**需要两处小改造**，都在 `permission_app.go`：

```go
// Decide 只判定、不等待：把 ask 也原样返回给调用方，由它决定怎么办。
// 子代理用它——它没有 UI 通道，等待只会挂满超时。
// 刻意不含 audit：调用方拿到 verdict 后自己决定怎么记，避免双重记账。
func (e *permissionEnforcer) Decide(ctx context.Context, tool ToolInterface, args map[string]interface{}) (Subject, Verdict)

// AuditDecision 把一次判定写进审计（子代理也要留痕，否则它的活动会是黑箱）
func (a *App) AuditDecision(sessionID string, subject Subject, verdict Verdict)
```

`Enforce` 随之变成"`Decide` + `AuditDecision` + 原来的 switch"，行为完全不变。

**子代理的网关**：

```go
// subagentEnforcer 子代理的权限网关：允许就放行，需要询问的一律拒绝。
// 不等待——子代理没有 UI 通道，等待只会挂满权限询问默认的 5 分钟。
type subagentEnforcer struct {
	parent   *permissionEnforcer
	app      *App
	sessionID string
	mu       sync.Mutex
	declined []string // "工具 + 主体摘要"，随结果回给主会话展示
}

func (e *subagentEnforcer) Enforce(ctx context.Context, tool ToolInterface, args map[string]interface{}) error {
	subject, verdict := e.parent.Decide(ctx, tool, args)
	switch verdict.Decision {
	case DecisionAllow:
		e.app.AuditDecision(e.sessionID, subject, verdict)
		return nil
	case DecisionDeny:
		e.app.AuditDecision(e.sessionID, subject, verdict)
		return fmt.Errorf("权限拒绝（%s）：%s", verdict.Stage, verdict.Reason)
	default: // 需要询问 → 降级为带理由的拒绝
		verdict.Stage = StageSubagentDeclined   // 新增取值，让审计能区分两种拒绝
		e.app.AuditDecision(e.sessionID, subject, verdict)
		e.record(subject)
		return fmt.Errorf("该操作需要用户授权，子代理无法代为确认。请改用不需要授权的做法，"+
			"或在结论里说明需要用户批准：%s", subject.Raw)
	}
}
```

判定分三种、**不能合并**：父网关 allow → 放行；父网关 deny（内置危险命令、用户显式规则）→ 拒绝，理由如实回给模型；父网关 ask → 拒绝，但理由要说清"这需要用户授权，请在结论里说明"，并记进 `declined`。

把询问降级成"带理由的拒绝"是这里的关键：既保住不阻塞，又不让子代理的活动变成黑箱——用户最终能看到它想做什么、被什么挡住了。

**审计里必须能区分"用户拒绝"与"子代理被挡下"**（新增 `StageSubagentDeclined = "subagent-declined"`，加在 `permission.go:937` 起那组 Stage 常量里，与现有 15 个同一写法），否则审计日志会与用户的真实决策记录混在一起。这与 P1-A 里"硬取消不写审计的拒绝记录"是同一条原则：**不要把系统的自动行为记成用户的决定**。

### 2.6 并发与写冲突

多个子代理并行跑，每个一个独立 `WorkspaceKey`。写冲突**不加锁**——加锁会引入排队与死锁，而收益有限（用户能看到谁改了什么）。改为事后检测：子代理结束时对比各方的文件触碰集合，有交集就在主会话里发一条警告，提示可以分别回退。

代价要诚实说明：两个子代理同时改同一文件仍可能后写覆盖先写。所以系统提示里要引导模型"派生子代理时尽量避免让它们改同一批文件"。

### 2.7 取消级联

子代理的 ctx 是父运行 ctx 的子节点（`context.WithCancel`），父硬取消 → 子代理自动一起取消；软取消 → 子代理循环在下一轮观察到信号。`runRegistry`（`runcontrol.go:130`）要支持记录父子关系，`activeCount` 之外再加一个按父 runID 查询子运行的能力，用于级联与上限统计。

### 2.8 存储与 UI

子代理消息写**独立文件** `~/.local-agent/subagents/<runID>.json`，不进主会话文件——中间过程可能有几百条消息，塞进会话会让加载变慢，而主会话只需要 `SubagentResult`。

`frontend/src/panes/SubagentPane.vue`（现为空壳）改成：列出运行中与已完成的子代理（状态、当前工具、已执行步数、耗时），可展开看它的消息流；已完成的可一键回退它改过的文件（复用 P2 的 `UndoDiffTurn`，因为 `WorkspaceKey` 是独立归因——这是 P2 与 P3 的第二个衔接点）。

注意**不要**把子代理塞进 `frontend/src/panes/TasksPane.vue`：那个面板的位置对应"主会话的待办清单"，属于另一件能力（todo 工具）。两个空壳各有归属，混用会让后面做 todo 时无处安放。

### 2.9 改动顺序（五步）

1. **纯重构**：`agentRun` + `runRecorder`，`runToolLoop` 迁移。行为不变，既有测试必须全绿。**这一步单独验收**——它是后续所有步骤的地基。
2. `permissionEnforcer` 加"只判定不等待"入口 + `subagentEnforcer` + 它的单测。此步仍不影响任何既有行为。
3. `subagent.go`：注册表、上限、`spawn_agent` 工具、结果契约。此步之后可端到端跑（暂时只有后备方案能触发）。
4. 取消级联 + 独立归因（`WorkspaceKey`）+ 与 P2 的衔接。
5. `frontend/src/panes/SubagentPane.vue`。

### 2.10 测试清单

1. `TestAgentRunRefactorKeepsBehavior` —— 第 1 步的验收：既有测试全绿即通过（无需新测试，但要跑全量）。
2. `TestSubagentEnforcerDeclinesWithoutBlocking` —— 注入一个"总是 ask"的假父网关，**断言在 100ms 内返回拒绝**而不是挂住，且 `declined` 里有记录。这条守的是 2.5 里最容易做错的地方。
3. `TestSubagentToolViewExcludesSpawnAndAsk` —— 断言子代理的工具清单里没有 `spawn_agent` 与 `ask_user`。
4. `TestSubagentCancelCascade` —— 父硬取消后子代理在 1 秒内结束。
5. `TestSubagentLimits` —— 并发上限、总派生数上限、单代理轮次上限各一条。
6. `TestSubagentContextIsolation` —— **这个功能存在的理由**：跑完一个子代理后，断言主会话新增的消息里**没有**子代理的中间过程，只有 `SubagentResult`。
7. `TestSubagentWorkspaceIsolation` —— 子代理的改动独立归因，父会话的 diff turn 不含它的文件；且它自己的文件可被单独回退。

### 2.11 风险

**第 1 步是最大风险点**：391 行代码搬家、12 处依赖改传参，改动面大而收益不可见（行为不变）。缓解是把它独立成一期、只跑既有测试、不掺任何新功能。若第 1 步做完发现既有测试无法覆盖某条路径，**先补覆盖再往下**——否则后面的问题会难以定位。

**第 2 步的"只判定不等待"入口**是第二个风险点：如果偷懒直接调父 `Enforce`，表现为"子代理偶尔挂满 5 分钟才失败"，而且是间歇性的、难复现。测试 2 就是为它准备的。

**并发写冲突**接受残留风险（事后检测 + 提示），不试图根治。

## 三、附：P2 实施记录（2026-09-17）

P2 已按本文档 1.7 的四步全部实现，新增 `checkpoint.go`／`checkpoint_test.go`／`undo.go`／`undo_test.go`（共 1281 行）加前端。落地时有七处与方案不同，都是往更简单或更正确的一边偏：

**一、回退算法放在新文件 `undo.go`，不在 `app.go`。** 方案里写的是 app.go（约 90 行），实际单开一个文件更内聚，`app.go` 也不再继续膨胀。

**二、`AppendDiff` 的签名压根不用改。** 方案说"签名扩参（~10 行）"，但 `Base` 与 `EndState` 本来就是 `DiffTurn` 的字段，跟着结构体走即可——省掉了一次涉及三处调用点的签名变更。真正新增的是 `SessionStore.MarkDiffUndone`。

**三、`runGit` 被抽成 `runGitEnv`，`diff.go` 因此掉了两个 import。** checkpoint 需要传 `GIT_INDEX_FILE`，所以把 git 调用封装改成"可附加环境变量"的版本、让 `runGit` 委托过去（全项目只留一份）。连带后果：`diff.go` 里的 `context` 与 `os/exec` 只有原 `runGit` 用到，委托之后立刻变成**未使用 import**——这两行必须同时删掉，否则编译失败。

**四、回退后不推 `diff:update` 事件。** 前端的 `applyUpdate` 有一句 `if (!diff || !diff.length) return`，会**丢弃空 diff**；而回退后这一轮的 diff 恰好经常变成空，靠事件刷新会留下过期数据。改为由前端在回退成功后重新 `load`（`stores/diff.js` 的 `undo` 就这么做的）。

**五、重复回退同一轮会触发冲突检查——这是有意的。** 第一次回退后，文件内容已不再等于当时记录的 `EndState`，所以第二次会被判为"本轮之后又被改过"。这样保住的是"用户在这期间手改过"的保护；代价是重复回退需要确认一次。测试里用 `force=true` 走这条路，并把原因写在注释里。

**六、轮次编号规则收敛到一处。** 方案提示过"`len(Diffs)+1` 两处各写一份会漂移"，实现时加了 `nextDiffTurn(session)`，`AppendDiff` 与取 checkpoint 两处共用。测试 `endTurn` 里还断言了"快照 ref 的编号与 `DiffTurn.Turn` 必须相等"，把这条约束钉死。

**七、测试清单做了两处调整。** 方案里的 `TestPruneCheckpoints` 与 ref 增删合并成了 `TestDropAndPruneCheckpoints`；`TestUndoPartialFailureReported` 没单独写（构造"部分失败"需要 `chmod` 或指向目录，在 temp 目录上跨平台不稳定，收益不抵脆弱性）——逐文件结果的结构已由 `UndoResult.Files` 保障，且 `TestUndoRejectsMissingSnapshot` 覆盖了失败路径的报错。另外**补了一条方案里没有的**：`TestRecordedStatusForUntrackedFileIsAddedAndUndoRestores`，走真实 `DiffScoped` 流程验证"预存在的未跟踪文件确实被报成 added、而同为 added 的两个文件动作必须不同"——这条把整个设计的前提钉死了。

**仍未验证**：沙箱无 Go 工具链，这 1281 行 Go 代码**未编译、未跑测试**。静态检查（括号配平、未使用 import、跨文件重名、结构体字面量字段名、未使用局部变量、BOM、列对齐）全过，新符号的调用点也逐一核对过；前端经 `@vue/compiler-sfc` 编译与 `node --check` 通过。请本地 `go build ./... && go test ./...`，并顺手 `gofmt -w .`。

