# 对话右侧栏 Diff 展示：实现方案（开发依据）

> - 项目：`E:\learn\local-agent`（Wails v2 + Go 后端 + Vue 3 前端）
> - 状态：待实施
> - 用途：后续开发的唯一依据，包含数据契约、完整代码、边界与验收标准

---

## 一、背景与目标

### 1.1 需求

在对话过程中，于界面**右侧栏**实时展示本次会话中被修改文件的 diff，便于用户随时审阅 AI 的代码改动。

### 1.2 目标

| 编号 | 目标 | 优先级 |
| --- | --- | --- |
| G1 | 右侧栏展示工作区相对基线的**真实 diff**（替换现有 mock） | P0 |
| G2 | 对话过程中一旦产生改动，右侧栏**自动打开并实时刷新** | P0 |
| G3 | 支持按**轮次**查看差异，并持久化，刷新后可回看 | P1 |
| G4 | 提供 **Review code** 入口，把 diff 交给 AI 审查 | P2 |
| G5 | 预留升级空间：新增 `write_file` / `edit_file` 工具后可精确归因 | P3 |

### 1.3 非目标（本期不做）

- 行内评论 / 批量提交评论
- 多项目聚合 diff
- 二进制文件的文本化 diff
- 逐字符级（word-level）diff 高亮

---

## 二、现状盘点

### 2.1 已有能力（可直接复用）

| 层 | 文件 | 现状 |
| --- | --- | --- |
| 后端 | `tools.go` | 仅有一个能力型工具 `exec_shell`（CLITool）+ 元工具 `tool_router`；**无文件编辑工具** |
| 后端 | `chat.go` | `executeChat()` 多轮工具循环；通过 `wailsRuntime.EventsEmit(ctx, "chat:event", ChatEvent{...})` 推送 `tool_call_start` / `tool_call_end` / `done` / `error` |
| 后端 | `sessions.go` | `Session{ Project, Messages, Conversations }`；`ToolCall{ID,Name,Args,Status,Duration,Result}` |
| 后端 | `app.go` | `App` 聚合 `modelStore` / `sessionStore` / `toolManager`，暴露 bound 方法给前端 |
| 前端 | `panes/DiffPane.vue` | **UI 完整**（文件列表 + 双行号 unified diff + add/del 配色），但数据是**写死的 mock** |
| 前端 | `components/layout/PaneContainer.vue` | 已把 `diff` 注册进右侧 Tab 面板体系 |
| 前端 | `stores/pane.js` | `openPane(sid, "diff")` / `closePane` / `setActivePane` 已可用 |
| 前端 | `panes/ChatPane.vue` | 已定义 `openDiff()` 但**无任何按钮调用**；`sendMessage()` 未联动 diff |
| 前端 | `types/index.js` | **`DiffFile` / `DiffLine` typedef 已就绪** |
| 前端 | `api/session.js` | 已有 `chat(sid, q, {onToolCallStart,...})` 事件桥接范式 |
| 文档 | `docs/frontend-design.md` | 已规划 `GetDiff(sessionId)` 与 DiffPane 的轮次切换 / 行内评论 / Review code |

### 2.2 关键约束

1. **没有文件编辑工具**：Agent 目前只能通过 `exec_shell` 改文件（重定向、`sed -i`、外部编辑器等），因此**无法从工具参数直接获得改动内容**，必须自行计算差异。
2. **项目是干净的 git 仓库**：`git -C E:\learn\local-agent status --porcelain` 输出为空；本机已安装 `git version 2.39.0.windows.2`。这使“基于 git 快照计算 diff”成为最省力的方案。
3. **会话的 `project` 字段通常为空**：`frontend/src/stores/session.js` 的 `init()` 调用 `createSession({title:"新会话"})`，不含 `project`，需在后端做兜底。
4. **`ChatEvent` 已有事件通道**：新增 `diff` 相关事件成本极低。

---

## 三、方案选型与总体设计

### 3.1 diff 数据源选型

| 方案 | 原理 | 优点 | 缺点 |
| --- | --- | --- | --- |
| **A. Git 工作区差异（本期主力）** | 每轮开始打 git 快照，之后 `git diff <baseline>` | 任何方式改的文件都能捕获（重定向 / sed / IDE）；无需改工具层；天然支持“按轮次” | 依赖项目是 git 仓库且装了 git |
| **B. 结构化文件工具** | 新增 `write_file` / `edit_file`，执行时记录 before/after | 精确到“哪个工具改了哪行”；不依赖 git | 模型若用 `exec_shell` 改文件则漏掉 |
| **C. A + B 结合（终态）** | A 兜底 + B 归因 | 最完整，接近 Claude Code | 工作量最大 |

**结论：本期采用 A 打通闭环；数据契约按 B 可复用设计（同一 `DiffFile` 结构），后续平滑升级到 C。**

### 3.2 总体数据流

```
ChatPane.sendMessage()
  -> chat(sid, text)                     [api/session.js]
       -> 后端 App.Chat()
            -> executeChat(sessionId, query)
                 [轮次开始] DiffService.TurnSnapshot(dir)          # 记录本轮基线
                 [工具循环] 原有逻辑不变
                 [轮次结束] DiffService.Diff(dir, turnBase)        # 得到 []DiffFile
                            sessionStore.AppendDiff(sid, turn)      # 持久化
                            EventsEmit("diff:update", {diff, turn}) # 事件推送
       <- ChatResult{ reply, messages, diff }
  -> diffStore.load(sid) / diffStore.applyUpdate(payload)
  -> PaneContainer 右侧 Diff Tab -> DiffPane.vue 渲染
```

### 3.3 设计原则

1. **零侵入既有工具链**：不改 `ToolManager` / `CLITool` 的执行语义。
2. **失败降级**：非 git 仓库、未装 git、超时等情况一律返回空 diff 并给出可读提示，**绝不阻断对话主流程**。
3. **单一数据契约**：前端 `types/index.js` 中已有的 `DiffFile` / `DiffLine` 即为契约本体，Go 侧字段名与之逐一对齐。
4. **轮次可追溯**：diff 按“轮次”（turn）分组并持久化到会话文件，同时提供“累计”视图。

---

## 四、数据契约

> 契约是前后端并行开发的分界点，**先冻结本节再开工**。

### 4.1 Go 侧（`diff.go`）

```go
// DiffLine 单行差异。add 行 OldLineNo=0；del 行 NewLineNo=0。
type DiffLine struct {
    Type      string `json:"type"`      // add | del | context
    OldLineNo int    `json:"oldLineNo"`
    NewLineNo int    `json:"newLineNo"`
    Content   string `json:"content"`
}

// DiffHunk 一个差异块，Header 形如 @@ -12,7 +14,9 @@
type DiffHunk struct {
    Header string     `json:"header"`
    Lines  []DiffLine `json:"lines"`
}

// DiffFile 单个文件的差异
type DiffFile struct {
    Path      string     `json:"path"`              // 相对项目根目录，正斜杠
    OldPath   string     `json:"oldPath,omitempty"` // 重命名时使用
    Status    string     `json:"status"`            // added | modified | deleted | renamed
    Additions int        `json:"additions"`
    Deletions int        `json:"deletions"`
    Hunks     []DiffHunk `json:"hunks"`
}

// DiffTurn 一轮对话产生的差异；Turn=0 表示“累计”
type DiffTurn struct {
    Turn      int        `json:"turn"`
    Label     string     `json:"label"`
    Files     []DiffFile `json:"files"`
    Additions int        `json:"additions"`
    Deletions int        `json:"deletions"`
    CreatedAt int64      `json:"createdAt"`
}
```

### 4.2 前端 TS / JSDoc 侧（`types/index.js` 增补）

```js
/**
 * 差异块
 * @typedef {Object} DiffHunk
 * @property {string} header
 * @property {DiffLine[]} lines
 */

/**
 * 差异文件
 * @typedef {Object} DiffFile
 * @property {string} path
 * @property {string} [oldPath]
 * @property {"added"|"modified"|"deleted"|"renamed"} status
 * @property {number} additions
 * @property {number} deletions
 * @property {DiffHunk[]} hunks
 */

/**
 * 轮次差异
 * @typedef {Object} DiffTurn
 * @property {number} turn
 * @property {string} label
 * @property {DiffFile[]} files
 * @property {number} additions
 * @property {number} deletions
 * @property {number} createdAt
 */
```

### 4.3 契约约定

| 约定 | 说明 |
| --- | --- |
| `type` 取值 | 仅 `add` / `del` / `context` 三种；hunk 头由前端渲染，不占 `type` |
| 行号 | `add` 行 `oldLineNo=0`，`del` 行 `newLineNo=0`，前端显示时把 0 渲染为空 |
| `path` | 统一相对项目根、正斜杠分隔，便于跨平台比较与去重 |
| 去重键 | 前端以 `path` 为 key 合并同一文件的多次改动 |
| 空结果 | 无改动时返回 `[]DiffFile{}`（Go 返回空切片而非 nil，避免前端拿到 `null`） |

---

## 五、后端实现

### 5.1 新增文件 `diff.go`

完整实现如下（含 git 封装、快照、diff 解析、未跟踪文件兜底）。

```go
package main

import (
    "bufio"
    "bytes"
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strconv"
    "strings"
    "sync"
    "time"
)

const (
    maxDiffFileBytes = 1 << 20 // 单文件超过 1MB 跳过
    maxDiffFiles     = 200     // 最多返回文件数
    gitTimeout       = 15 * time.Second
)

// ===== git 执行封装 =====
// 关键点：
//   -c core.quotepath=false  避免中文/非 ASCII 路径被转义成 \xxx，否则无法解析
//   --no-pager               防止进入分页
//   固定超时                 防止大仓库卡死对话
func runGit(dir string, args ...string) (string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
    defer cancel()

    full := append([]string{"-C", dir, "-c", "core.quotepath=false", "--no-pager"}, args...)
    cmd := exec.CommandContext(ctx, "git", full...)
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("git %s 失败: %v: %s",
            strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
    }
    return stdout.String(), nil
}

// ===== 差异服务 =====

// DiffService 负责会话级基线管理与 diff 计算
type DiffService struct {
    mu       sync.Mutex
    baseline map[string]string // sessionID -> 会话基线 commit sha
}

func NewDiffService() *DiffService {
    return &DiffService{baseline: make(map[string]string)}
}

// IsRepo 判断目录是否为 git 工作区
func (s *DiffService) IsRepo(dir string) bool {
    if dir == "" {
        return false
    }
    out, err := runGit(dir, "rev-parse", "--is-inside-work-tree")
    return err == nil && strings.TrimSpace(out) == "true"
}

// Snapshot 生成工作区快照 commit，不修改工作区与暂存区。
// 优先用 git stash create（有改动时返回悬空 commit）；无改动时回退 HEAD。
func (s *DiffService) Snapshot(dir string) (string, error) {
    if out, err := runGit(dir, "stash", "create"); err == nil {
        if sha := strings.TrimSpace(out); sha != "" {
            return sha, nil
        }
    }
    out, err := runGit(dir, "rev-parse", "HEAD")
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(out), nil
}

// EnsureBaseline 懒初始化会话基线
func (s *DiffService) EnsureBaseline(sessionID, dir string) string {
    s.mu.Lock()
    defer s.mu.Unlock()
    if sha, ok := s.baseline[sessionID]; ok {
        return sha
    }
    sha, _ := s.Snapshot(dir)
    s.baseline[sessionID] = sha
    return sha
}

// TurnSnapshot 每轮开始时取本轮基线
func (s *DiffService) TurnSnapshot(dir string) string {
    sha, _ := s.Snapshot(dir)
    return sha
}

// ForgetBaseline 会话删除时清理（可选）
func (s *DiffService) ForgetBaseline(sessionID string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    delete(s.baseline, sessionID)
}

// Diff 计算 dir 相对 baseline 的差异（含未跟踪文件）
func (s *DiffService) Diff(dir, baseline string) ([]DiffFile, error) {
    args := []string{"diff", "--no-color", "--unified=3", "--no-ext-diff"}
    if baseline != "" {
        args = append(args, baseline)
    }
    args = append(args, "--")

    out, err := runGit(dir, args...)
    if err != nil {
        return nil, err
    }
    files := ParseUnifiedDiff(out)

    // git diff 不含未跟踪文件，单独补齐
    if untracked, err := runGit(dir, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
        for _, rel := range strings.Split(untracked, "\x00") {
            rel = strings.TrimSpace(rel)
            if rel == "" {
                continue
            }
            if f, ok := buildAddedFile(dir, rel); ok {
                files = append(files, f)
            }
        }
    }

    if len(files) > maxDiffFiles {
        files = files[:maxDiffFiles]
    }
    if files == nil {
        files = []DiffFile{}
    }
    return files, nil
}

// buildAddedFile 把未跟踪文本文件构造成“全部新增”
func buildAddedFile(dir, rel string) (DiffFile, bool) {
    full := filepath.Join(dir, filepath.FromSlash(rel))
    info, err := os.Stat(full)
    if err != nil || info.IsDir() || info.Size() > maxDiffFileBytes {
        return DiffFile{}, false
    }
    data, err := os.ReadFile(full)
    if err != nil || bytes.IndexByte(data, 0) >= 0 { // 含 NUL 视为二进制
        return DiffFile{}, false
    }

    // 归一化 CRLF，避免“整文件被标记为改动”的噪音
    text := strings.ReplaceAll(string(data), "\r\n", "\n")
    lines := strings.Split(text, "\n")
    if n := len(lines); n > 0 && lines[n-1] == "" {
        lines = lines[:n-1]
    }

    f := DiffFile{
        Path:   rel,
        Status: "added",
        Hunks:  []DiffHunk{{Header: fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines))}},
    }
    for i, l := range lines {
        f.Hunks[0].Lines = append(f.Hunks[0].Lines, DiffLine{Type: "add", NewLineNo: i + 1, Content: l})
        f.Additions++
    }
    return f, true
}

// ===== unified diff 解析 =====

func trimABPrefix(p string) string {
    p = strings.TrimPrefix(p, "a/")
    p = strings.TrimPrefix(p, "b/")
    return p
}

// parseHunkHeader 解析 @@ -12,7 +14,9 @@
func parseHunkHeader(header string) (oldStart, newStart int) {
    rest := strings.TrimPrefix(header, "@@ ")
    parts := strings.SplitN(rest, " ", 2)

    oldPart := strings.TrimPrefix(parts[0], "-")
    if i := strings.IndexByte(oldPart, 0x2c); i >= 0 {
        oldPart = oldPart[:i]
    }
    oldStart, _ = strconv.Atoi(oldPart)

    if len(parts) == 2 {
        newPart := strings.TrimPrefix(parts[1], "+")
        if i := strings.IndexByte(newPart, 0x20); i >= 0 {
            newPart = newPart[:i]
        }
        if i := strings.IndexByte(newPart, 0x2c); i >= 0 {
            newPart = newPart[:i]
        }
        newStart, _ = strconv.Atoi(newPart)
    }
    return
}

// ParseUnifiedDiff 解析 git diff 输出为 []DiffFile。
// 用 inHunk 状态机区分“文件头的 --- / +++”与“内容里恰好以 --- 开头的行”。
func ParseUnifiedDiff(out string) []DiffFile {
    files := []DiffFile{}
    var cur *DiffFile
    var hunk *DiffHunk
    var oldNo, newNo int
    inHunk := false

    sc := bufio.NewScanner(strings.NewReader(out))
    sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // 支持长行

    for sc.Scan() {
        line := sc.Text()

        if strings.HasPrefix(line, "diff --git ") {
            if cur != nil {
                files = append(files, *cur)
            }
            cur = &DiffFile{Status: "modified", Hunks: []DiffHunk{}}
            hunk, inHunk = nil, false
            continue
        }
        if cur == nil {
            continue
        }

        if !inHunk {
            switch {
            case strings.HasPrefix(line, "new file mode"):
                cur.Status = "added"
            case strings.HasPrefix(line, "deleted file mode"):
                cur.Status = "deleted"
            case strings.HasPrefix(line, "rename from "):
                cur.Status = "renamed"
                cur.OldPath = strings.TrimSpace(line[len("rename from "):])
            case strings.HasPrefix(line, "rename to "):
                cur.Path = strings.TrimSpace(line[len("rename to "):])
            case strings.HasPrefix(line, "--- "):
                p := strings.TrimSpace(line[4:])
                if p == "/dev/null" {
                    if cur.Status == "modified" {
                        cur.Status = "added"
                    }
                } else {
                    cur.OldPath = trimABPrefix(p)
                }
            case strings.HasPrefix(line, "+++ "):
                if p := strings.TrimSpace(line[4:]); p != "/dev/null" {
                    cur.Path = trimABPrefix(p)
                }
            case strings.HasPrefix(line, "@@"):
                os_, ns := parseHunkHeader(line)
                oldNo, newNo = os_-1, ns-1
                cur.Hunks = append(cur.Hunks, DiffHunk{Header: line, Lines: []DiffLine{}})
                hunk = &cur.Hunks[len(cur.Hunks)-1]
                inHunk = true
            }
            continue
        }

        // hunk 内部
        switch {
        case strings.HasPrefix(line, "@@"):
            os_, ns := parseHunkHeader(line)
            oldNo, newNo = os_-1, ns-1
            cur.Hunks = append(cur.Hunks, DiffHunk{Header: line, Lines: []DiffLine{}})
            hunk = &cur.Hunks[len(cur.Hunks)-1]
        case line == "":
            // 忽略文件尾空行
        case line[0] == 0x2b: // +
            newNo++
            hunk.Lines = append(hunk.Lines, DiffLine{Type: "add", NewLineNo: newNo, Content: line[1:]})
            cur.Additions++
        case line[0] == 0x2d: // -
            oldNo++
            hunk.Lines = append(hunk.Lines, DiffLine{Type: "del", OldLineNo: oldNo, Content: line[1:]})
            cur.Deletions++
        case line[0] == 0x20: // 空格
            oldNo++
            newNo++
            hunk.Lines = append(hunk.Lines, DiffLine{Type: "context", OldLineNo: oldNo, NewLineNo: newNo, Content: line[1:]})
        case line[0] == 0x5c: // 反斜杠：“No newline at end of file”
            // 忽略
        }
    }

    if cur != nil {
        files = append(files, *cur)
    }
    return files
}

// ===== 统计工具 =====

func sumAdd(files []DiffFile) int {
    n := 0
    for _, f := range files {
        n += f.Additions
    }
    return n
}

func sumDel(files []DiffFile) int {
    n := 0
    for _, f := range files {
        n += f.Deletions
    }
    return n
}
```

**实现要点**

1. `-c core.quotepath=false` 必须加，否则中文路径会变成 `"\344\270\255"` 无法解析。
2. 解析器必须用 `inHunk` 状态机，否则正文中形如 `--- xxx` 的行会被误判为文件头。
3. `git stash create` 不修改工作区与暂存区，安全。
4. 未跟踪文件必须单独用 `ls-files --others` 补齐。
5. 代码中比较字符时使用 `0x2b` / `0x2d` / `0x20` / `0x5c` 字节常量，避免转义字符在文档/复制过程中损坏。

---

### 5.2 `app.go` 接线

```go
type App struct {
    ctx          context.Context
    modelStore   *ModelStore
    sessionStore *SessionStore
    toolManager  *ToolManager
    diffService  *DiffService // 新增
}

// startup 中新增一行
func (a *App) startup(ctx context.Context) {
    // ...原有初始化...
    a.diffService = NewDiffService()
}

// resolveProjectDir 返回会话项目目录；为空时回退到进程工作目录
func (a *App) resolveProjectDir(sessionID string) (string, error) {
    s, err := a.sessionStore.GetSession(sessionID)
    if err != nil {
        return "", err
    }
    if s.Project != "" {
        return s.Project, nil
    }
    return os.Getwd()
}

// GetDiff 返回会话工作区相对基线的累计差异（Turn=0）
func (a *App) GetDiff(sessionID string) ([]DiffFile, error) {
    dir, err := a.resolveProjectDir(sessionID)
    if err != nil {
        return []DiffFile{}, err
    }
    if !a.diffService.IsRepo(dir) {
        return []DiffFile{}, nil // 非 git 仓库不报错
    }
    base := a.diffService.EnsureBaseline(sessionID, dir)
    files, err := a.diffService.Diff(dir, base)
    if err != nil {
        return []DiffFile{}, err
    }
    return files, nil
}

// GetDiffTurns 返回按轮次分组的差异，索引 0 为“累计”
func (a *App) GetDiffTurns(sessionID string) ([]DiffTurn, error) {
    s, err := a.sessionStore.GetSession(sessionID)
    if err != nil {
        return nil, err
    }
    turns := make([]DiffTurn, 0, len(s.Diffs)+1)

    if dir, err := a.resolveProjectDir(sessionID); err == nil && a.diffService.IsRepo(dir) {
        if files, err := a.GetDiff(sessionID); err == nil {
            turns = append(turns, DiffTurn{
                Turn:      0,
                Label:     "累计",
                Files:     files,
                Additions: sumAdd(files),
                Deletions: sumDel(files),
                CreatedAt: time.Now().UnixMilli(),
            })
        }
    }
    turns = append(turns, s.Diffs...)
    return turns, nil
}
```

> `app.go` 需新增 `import "time"`（若尚未引入）。
> 新增 bound 方法后**必须重新生成 Wails 绑定**，见 5.5。

### 5.3 `sessions.go` 增加轮次持久化

```go
type Session struct {
    // ...原有字段...
    Diffs []DiffTurn `json:"diffs,omitempty"` // 新增
}

// AppendDiff 追加一轮差异并落盘
func (s *SessionStore) AppendDiff(id string, turn DiffTurn) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    session, err := s.loadSession(id)
    if err != nil {
        return err
    }
    turn.Turn = len(session.Diffs) + 1
    session.Diffs = append(session.Diffs, turn)
    return s.saveSession(session)
}
```

**注意**：`ListSessions()` 目前会清空 `Messages` / `Conversations` 以减小列表体积，务必**同样清空 `Diffs`**，否则会话列表接口会随改动量线性变重：

```go
// ListSessions 内，原有两行之后追加
session.Diffs = nil
```

### 5.4 `chat.go` 每轮产出 diff

**（1）扩展事件与返回值结构**

```go
type ChatEvent struct {
    Type     string     `json:"type"` // 新增取值 "diff_update"
    ToolCall *ToolCall  `json:"toolCall,omitempty"`
    Reply    string     `json:"reply,omitempty"`
    Error    string     `json:"error,omitempty"`
    Diff     []DiffFile `json:"diff,omitempty"` // 新增
    Turn     int        `json:"turn,omitempty"` // 新增
}

type ChatResult struct {
    Reply     string     `json:"reply"`
    ToolCalls []ToolCall `json:"toolCalls,omitempty"`
    Messages  []Message  `json:"messages,omitempty"`
    Diff      []DiffFile `json:"diff,omitempty"` // 新增
    Error     string     `json:"error,omitempty"`
}
```

**（2）`executeChat` 中两处插入**

轮次开始处（获取模型配置之后、工具循环之前）：

```go
// ★ 记录本轮基线
dir, _ := a.resolveProjectDir(sessionID)
isRepo := a.diffService.IsRepo(dir)
turnBase := ""
if isRepo {
    a.diffService.EnsureBaseline(sessionID, dir)
    turnBase = a.diffService.TurnSnapshot(dir)
}
```

`finish_reason == "stop"` 分支中（持久化最终回复之后、构造 `ChatResult` 之前）：

```go
// ★ 计算并推送本轮 diff
var diffFiles []DiffFile
if isRepo {
    if files, err := a.diffService.Diff(dir, turnBase); err == nil && len(files) > 0 {
        diffFiles = files
        turn := DiffTurn{
            Files:     files,
            Additions: sumAdd(files),
            Deletions: sumDel(files),
            CreatedAt: time.Now().UnixMilli(),
        }
        _ = a.sessionStore.AppendDiff(sessionID, turn)

        if a.ctx != nil {
            wailsRuntime.EventsEmit(a.ctx, "diff:update", ChatEvent{
                Type: "diff_update",
                Diff: files,
                Turn: turn.Turn,
            })
        }
    }
}

result := &ChatResult{
    Reply:     choice.Message.Content,
    ToolCalls: toolCallRecords,
    Messages:  persistedMsgs,
    Diff:      diffFiles, // ★ 新增
}
```

> 事件名用独立的 `diff:update`（而非复用 `chat:event`），与现有工具事件解耦，前端监听更干净。

### 5.5 重新生成 Wails 绑定

新增 `GetDiff` / `GetDiffTurns` 后，**必须**重新生成绑定，否则前端 `import` 得到 `undefined`：

```bash
cd E:\learn\local-agent
wails generate module      # 或直接 wails dev 一次
```

生成后确认 `frontend/wailsjs/go/main/App.js` 与 `App.d.ts` 中出现：

```js
export function GetDiff(arg1) { ... }
export function GetDiffTurns(arg1) { ... }
```

---

## 六、前端实现

### 6.1 `api/session.js`

```js
import { GetDiff, GetDiffTurns } from "@/../wailsjs/go/main/App"

// 浏览器开发模式（非 Wails）下的 mock，便于脱离桌面环境调试 UI
const MOCK_DIFF = [
  {
    path: "app.go",
    status: "modified",
    additions: 2,
    deletions: 1,
    hunks: [
      {
        header: "@@ -1,4 +1,5 @@",
        lines: [
          { type: "context", oldLineNo: 1, newLineNo: 1, content: "package main" },
          { type: "del", oldLineNo: 2, newLineNo: 0, content: "import fmt" },
          { type: "add", oldLineNo: 0, newLineNo: 2, content: "import (" },
          { type: "add", oldLineNo: 0, newLineNo: 3, content: "    os" },
        ],
      },
    ],
  },
]

export async function getDiff(sessionId) {
  if (isWails()) return await GetDiff(sessionId)
  return MOCK_DIFF
}

export async function getDiffTurns(sessionId) {
  if (isWails()) return await GetDiffTurns(sessionId)
  return [
    { turn: 0, label: "累计", files: MOCK_DIFF, additions: 2, deletions: 1, createdAt: Date.now() },
  ]
}

// 订阅 diff 实时更新，返回取消订阅函数
export function onDiffUpdate(cb) {
  if (!isWails()) return () => {}
  EventsOn("diff:update", cb)
  return () => EventsOff("diff:update")
}
```

> `EventsOn` / `EventsOff` 已在文件顶部从 `@/../wailsjs/runtime/runtime` 引入，无需重复引入。

### 6.2 新增 `stores/diff.js`

```js
import { defineStore } from "pinia"
import { ref, computed } from "vue"
import { getDiffTurns } from "@/api/session"

export const useDiffStore = defineStore("diff", () => {
  const turns = ref([])
  const activeTurn = ref(0)
  const loading = ref(false)
  const loadedSessionId = ref(null)

  const currentTurn = computed(
    () => turns.value.find((t) => t.turn === activeTurn.value) || turns.value[0] || null
  )
  const files = computed(() => currentTurn.value?.files || [])
  const additions = computed(() => currentTurn.value?.additions || 0)
  const deletions = computed(() => currentTurn.value?.deletions || 0)

  /** 加载会话全部轮次差异，返回当前轮文件数 */
  async function load(sessionId) {
    if (!sessionId) {
      turns.value = []
      loadedSessionId.value = null
      return 0
    }
    loading.value = true
    try {
      turns.value = (await getDiffTurns(sessionId)) || []
      loadedSessionId.value = sessionId
      const last = turns.value[turns.value.length - 1]
      activeTurn.value = last ? last.turn : 0
      return files.value.length
    } catch (e) {
      console.error("加载 diff 失败:", e)
      turns.value = []
      return 0
    } finally {
      loading.value = false
    }
  }

  /** 处理 diff:update 事件 */
  function applyUpdate(payload) {
    const { diff, turn } = payload || {}
    if (!diff || !diff.length) return
    const add = diff.reduce((s, f) => s + (f.additions || 0), 0)
    const del = diff.reduce((s, f) => s + (f.deletions || 0), 0)
    const item = {
      turn,
      label: turn === 0 ? "累计" : "第 " + turn + " 轮",
      files: diff,
      additions: add,
      deletions: del,
      createdAt: Date.now(),
    }
    const idx = turns.value.findIndex((t) => t.turn === turn)
    if (idx > -1) turns.value[idx] = item
    else turns.value.push(item)
    activeTurn.value = turn
  }

  function setActiveTurn(t) {
    activeTurn.value = t
  }

  function clear() {
    turns.value = []
    activeTurn.value = 0
    loadedSessionId.value = null
  }

  return {
    turns,
    activeTurn,
    loading,
    loadedSessionId,
    currentTurn,
    files,
    additions,
    deletions,
    load,
    applyUpdate,
    setActiveTurn,
    clear,
  }
})
```

### 6.3 改造 `panes/DiffPane.vue`

**改动点：把 mock 数据源替换为 store，并把 `lines` 扁平化渲染（hunk 头也作为一行）。**

```js
<script setup>
import { ref, computed, onMounted, watch } from "vue"
import PaneHeader from "@/components/layout/PaneHeader.vue"
import { useDiffStore } from "@/stores/diff"
import { useSessionStore } from "@/stores/session"
import { useChatStore } from "@/stores/chat"

const diffStore = useDiffStore()
const sessionStore = useSessionStore()
const chatStore = useChatStore()

const selectedFile = ref(0)

const diffFiles = computed(() => diffStore.files)
const currentFile = computed(() => diffFiles.value[selectedFile.value] || null)

// 把 hunks 扁平成可渲染行：hunk 头 + 内容行
const flatLines = computed(() => {
  const f = currentFile.value
  if (!f) return []
  const rows = []
  for (const h of f.hunks || []) {
    rows.push({ type: "hunk", content: h.header })
    rows.push(...h.lines)
  }
  return rows
})

onMounted(() => diffStore.load(sessionStore.currentSessionId))
watch(
  () => sessionStore.currentSessionId,
  (id) => {
    selectedFile.value = 0
    diffStore.load(id)
  }
)
watch(
  () => diffFiles.value.length,
  () => {
    if (selectedFile.value >= diffFiles.value.length) selectedFile.value = 0
  }
)

const statusBadge = (s) => ({ added: "A", deleted: "D", renamed: "R" }[s] || "")

function reviewCode() {
  const f = currentFile.value
  if (!f) return
  chatStore.requestPrompt(
    "请审查以下改动：" + f.path + "（+" + f.additions + " -" + f.deletions + "）"
  )
}
</script>
```

模板中的四处调整：

```html
<!-- 1) 顶部：轮次切换 + Review code -->
<PaneHeader type="diff" :extra="`+${diffStore.additions} -${diffStore.deletions}`">
  <template #extra>
    <select
      v-if="diffStore.turns.length"
      class="turn-select"
      :value="diffStore.activeTurn"
      @change="diffStore.setActiveTurn(Number($event.target.value))"
    >
      <option v-for="t in diffStore.turns" :key="t.turn" :value="t.turn">
        {{ t.label || (t.turn === 0 ? "累计" : "第 " + t.turn + " 轮") }}
      </option>
    </select>
    <button class="btn btn-ghost btn-sm" @click="reviewCode">Review code</button>
  </template>
</PaneHeader>

<!-- 2) 文件列表：增加状态标记 -->
<span v-if="statusBadge(f.status)" class="file-badge" :class="`badge-${f.status}`">
  {{ statusBadge(f.status) }}
</span>

<!-- 3) 差异内容：改用 flatLines 与 hunk 行 -->
<tbody>
  <tr v-for="(line, i) in flatLines" :key="i" :class="`line-${line.type}`">
    <td class="line-no old">{{ line.oldLineNo || "" }}</td>
    <td class="line-no new">{{ line.newLineNo || "" }}</td>
    <td class="line-sign">
      {{ line.type === "add" ? "+" : line.type === "del" ? "-" : (line.type === "hunk" ? "" : " ") }}
    </td>
    <td class="line-content">{{ line.content }}</td>
  </tr>
</tbody>

<!-- 4) 空态 -->
<div v-if="!diffFiles.length" class="diff-empty">
  {{ diffStore.loading ? "正在计算差异…" : "本轮对话暂无文件改动" }}
</div>
```

样式补充（沿用 `variables.scss` 中的变量）：

```scss
.turn-select {
  font-size: $font-size-xs;
  background-color: $color-bg-tertiary;
  color: $color-text-primary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  padding: 1px 4px;
}

.line-hunk {
  background-color: rgba(59, 130, 246, 0.12);
  .line-content { color: $color-info; }
}

.file-badge {
  font-size: 10px;
  padding: 0 4px;
  border-radius: 3px;
  &.badge-added { color: $color-success; }
  &.badge-deleted { color: $color-error; }
  &.badge-renamed { color: $color-warning; }
}

.diff-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: $color-text-muted;
  font-size: $font-size-sm;
}
```

> `PaneHeader` 的 `extra` 插槽已在组件中定义（`<template #extra>`），可同时展示统计与按钮。

---

### 6.4 `panes/ChatPane.vue`

**（1）订阅 diff 事件**

```js
import { onMounted, onUnmounted } from "vue"
import { onDiffUpdate } from "@/api/session"
import { useDiffStore } from "@/stores/diff"

const diffStore = useDiffStore()
let offDiff = null

onMounted(() => {
  offDiff = onDiffUpdate((payload) => diffStore.applyUpdate(payload))
})
onUnmounted(() => {
  if (offDiff) offDiff()
})
```

**（2）发送完成后自动打开右栏**

在 `sendMessage()` 的 `try` 末尾（`sessionStore.touchSession(sid)` 之后）追加：

```js
// 联动 DiffPane：有改动则自动展开右侧面板
try {
  const changed = await diffStore.load(sid)
  if (changed > 0) paneStore.openPane(sid, "diff")
} catch (e) {
  console.warn("加载 diff 失败:", e)
}
```

**（3）把已存在的 `openDiff()` 挂到工具栏**

在 `.prompt-toolbar` 内新增：

```html
<button class="tool-btn" title="查看差异" @click="openDiff">Diff</button>
```

**（4）消费 Review 提示词**

```js
import { watch } from "vue"

watch(
  () => chatStore.pendingPrompt,
  (t) => {
    if (t) input.value = chatStore.consumePrompt()
  }
)
```

### 6.5 `stores/chat.js` 增加提示词通道

```js
const pendingPrompt = ref(null)

function requestPrompt(text) {
  pendingPrompt.value = text
}

function consumePrompt() {
  const t = pendingPrompt.value
  pendingPrompt.value = null
  return t
}

// 追加到 return 对象中
return {
  // ...原有字段与函数...
  pendingPrompt,
  requestPrompt,
  consumePrompt,
}
```

### 6.6 无需改动

- `components/layout/PaneContainer.vue`：`diff` 已注册，`openPane("diff")` 即可出现 Tab。
- `stores/pane.js`：已具备所需 API。
- `types/index.js`：仅做 4.2 的注释增补。

---

## 七、边界情况与风险清单

| 编号 | 风险 / 场景 | 处理策略 |
| --- | --- | --- |
| R1 | 会话 `project` 为空 | `resolveProjectDir()` 回退 `os.Getwd()`；建议同时把 `sessionStore.init()` 的默认 `project` 设为当前目录 |
| R2 | 非 git 仓库 / 未安装 git | `IsRepo()` 返回 false，`GetDiff` 返回空切片；前端显示“该项目不是 git 仓库，暂不支持差异视图”，**不报错不崩** |
| R3 | 中文 / 非 ASCII 路径 | `runGit` 固定加 `-c core.quotepath=false` |
| R4 | CRLF 换行噪音 | 构造新增文件时归一化 CRLF 为 LF |
| R5 | 二进制文件 | 内容含 `NUL` 字节则跳过 |
| R6 | 超大文件 / 超多文件 | 单文件 > 1MB 跳过；文件数封顶 200；前端单文件渲染行数封顶（如 2000 行） |
| R7 | diff 计算阻塞对话 | 每轮**只**在结束时执行一次 git 调用；`gitTimeout=15s`，超时返回空并降级 |
| R8 | 进程重启后基线丢失 | `EnsureBaseline` 懒初始化；重启后退化为“相对 HEAD”，可接受 |
| R9 | 用户原有未提交改动被计入 | 属预期（等价于 `git diff`）。若需严格只看本会话，需在会话创建时立即打基线 |
| R10 | 并发多会话 | `DiffService.baseline` 由 `sync.Mutex` 保护；各 git 进程相互独立 |
| R11 | Wails 绑定未更新 | 新增 bound 方法后必须重新生成绑定（见 5.5），否则前端调用报 `undefined` |
| R12 | 解析器误判 | 必须使用 `inHunk` 状态机；配套 `diff_test.go` 覆盖“正文含 `---` 行”的用例 |
| R13 | JSON 序列化 `null` | Go 端保证返回 `[]DiffFile{}` 而非 `nil` |

---

## 八、分阶段实施计划与验收

| 阶段 | 内容 | 预估 | 验收标准 |
| --- | --- | --- | --- |
| **P0** | 后端 `diff.go` + `App.GetDiff` + 前端 `stores/diff.js` + `DiffPane` 接真实数据 | 1–2h | 打开 Diff 面板能看到工作区真实 diff，且与 `git status` 一致 |
| **P1** | `chat.go` 每轮 `diff:update` + `sessions.AppendDiff` 持久化 + 轮次切换 UI | 1–2h | 让 AI 用 `exec_shell` 改一个文件：右侧栏自动弹出并显示该轮 diff；刷新后仍可见 |
| **P2** | `ChatPane` 自动开面板 + 工具栏 Diff 按钮 + Review code 打通 | 0.5h | 点击 Review code，AI 收到审查请求并回复 |
| **P3（可选）** | 新增 `write_file` / `edit_file` 工具，工具层记录 before/after **（2026-09-17 已实现：文件六件套 + `FileChangeLog` 归因，工具卡片展示改动文件，hunks 经 `GetToolFileChanges` 提供）** | 1–2h | 工具卡片出现“查看差异”，且差异可归因到具体工具调用 |

**P3 的低成本实现技巧**：工具层不必自研 Myers 算法——把 before / after 各写一个临时文件，执行 `git diff --no-index --unified=3 old.tmp new.tmp`，**复用同一个 `ParseUnifiedDiff`**，零额外算法成本。

---

## 九、测试方案

### 9.1 后端单测（新增 `diff_test.go`）

| 用例 | 输入 | 断言 |
| --- | --- | --- |
| T1 基本解析 | 标准 `git diff` 输出（含 add/del/context） | `Additions` / `Deletions` 正确；行号正确 |
| T2 头尾歧义 | 正文中包含以 `---` / `+++` 开头的行 | 不被误判为文件头 |
| T3 新增文件 | `new file mode` + `/dev/null` | `Status == "added"` |
| T4 删除文件 | `deleted file mode` | `Status == "deleted"` |
| T5 重命名 | `rename from/to` | `Status == "renamed"`，`OldPath` 正确 |
| T6 多文件 | 两个 `diff --git` 段 | 返回 2 个 `DiffFile` |
| T7 中文路径 | `-c core.quotepath=false` 的真实输出 | `Path` 为可读中文 |

### 9.2 手动验证

```bat
:: 1) 确认基线可生成
git -C E:\learn\local-agent stash create
git -C E:\learn\local-agent rev-parse HEAD

:: 2) 取一份真实 diff 供解析器验证
git -C E:\learn\local-agent --no-pager -c core.quotepath=false diff HEAD --unified=3

:: 3) 端到端：在应用内让 AI 改一个文件，观察
::    a. 控制台收到 diff:update 事件
::    b. 右侧栏自动弹出 Diff Tab
::    c. 面板内容与 git diff 一致
::    d. 刷新页面后 diff 仍在
```

### 9.3 浏览器 mock 模式验证

在非 Wails 环境下（纯 `vite`），`api/session.js` 的 `getDiffTurns()` 返回 `MOCK_DIFF`，可快速验证 `DiffPane` 渲染、轮次切换、空态与样式，无需启动桌面端。

---

## 十、附录

### 10.1 改动文件清单

| 层 | 文件 | 动作 |
| --- | --- | --- |
| Go | `diff.go` | **新增**：数据结构 + DiffService + 解析器 |
| Go | `diff_test.go` | **新增**：解析器单测 |
| Go | `app.go` | 新增 `diffService` 字段、`resolveProjectDir`、`GetDiff`、`GetDiffTurns` |
| Go | `sessions.go` | `Session` 增 `Diffs`；新增 `AppendDiff`；`ListSessions` 清空 `Diffs` |
| Go | `chat.go` | `ChatEvent` / `ChatResult` 增字段；`executeChat` 两处插入 |
| 自动 | `frontend/wailsjs/go/main/App.js`、`App.d.ts` | 重新生成绑定 |
| JS | `frontend/src/api/session.js` | 增 `getDiff` / `getDiffTurns` / `onDiffUpdate` + mock |
| JS | `frontend/src/stores/diff.js` | **新增** |
| JS | `frontend/src/stores/chat.js` | 增 `pendingPrompt` 通道 |
| Vue | `frontend/src/panes/DiffPane.vue` | mock → store；hunk 渲染；轮次切换；空态 |
| Vue | `frontend/src/panes/ChatPane.vue` | 订阅事件、自动开面板、Diff 按钮、消费提示词 |
| JSDoc | `frontend/src/types/index.js` | 增补 `DiffHunk` / `DiffFile` / `DiffTurn` 注释 |

### 10.2 关键命令速查

```bash
# 生成 Wails 绑定
wails generate module

# 开发运行
wails dev

# 前端单独开发（mock 模式）
cd frontend && npm run dev
```

### 10.3 术语

| 术语 | 含义 |
| --- | --- |
| 基线（baseline） | 计算 diff 的参照点，本方案为 git commit（`git stash create` 产物） |
| 轮次（turn） | 一次用户提问到 AI 最终回复的完整过程 |
| 累计 | 相对会话基线的全部改动，`turn = 0` |

---

（完）

---

## 会话级差异（2026-09-17 实现）

**问题**：diff 面板会出现「别的会话改的东西」。根因在 `DiffService.Diff`——两条路径都不受会话约束：

1. 已跟踪文件走 `git diff <基线>`。基线虽然是每会话一份，但 git 的 diff 本身是**仓库级**的，只按时间过滤、不区分谁改的；于是会话 B 会看到会话 A 在 B 的基线之后做的改动。
2. 未跟踪文件走 `git ls-files --others`，**没有任何会话过滤**，仓库里所有未跟踪文件会出现在每个会话的面板里——这是最刺眼的一处。

**做法**：给每个会话维护「触碰过的路径」集合，diff 时用这些路径做 pathspec 过滤。

- **归因**：每次工具调用**前**后各扫一次工作区（`git status --porcelain -z --untracked-files=normal`），把「执行窗口内出现的改动」归给当前会话。用文件签名（mtime+size）判断变化——与 git 自身判定文件是否变化的信号同类，比读全文便宜。
- **扫描基线必须是全局的**：若每会话各存一份上次扫描，会话 A 空闲期间 B 改了文件，A 下次调用的起始扫描会把 B 的改动算成自己的（A 的基线还停在 B 改动之前）。全局基线保证每个变化只被「第一个看到它的扫描」消费掉；再配合**只在窗口之后归因**，空闲会话就不会沾上别人的改动。
- **过滤**：`DiffScoped(dir, baseline, paths)` 用 `git diff <基线> -- <paths>` 取已跟踪改动，再用 `ls-files --others -- <paths>` 补齐这些路径下的未跟踪文件（同一份 pathspec，因此新建的文件只出现在创建它的会话里）。
- **落盘**：`Session.DiffBaseline` / `DiffTouched` 随会话 JSON 保存，重启后仍能算出「本会话改了哪些文件」（此前基线只在内存，重启后 diff 直接为空）。`AppendDiff` 顺带写入这两个字段，不额外多一次落盘。
- **每轮 diff** 同样按会话过滤：`BeginTurn` / `TurnTouched` 维护本轮归因集合。

**为什么不用 `-uall`**：目录折叠（`-unormal`）把未跟踪目录记成一条（`newdir/`），扫描成本不随目录内文件数增长；pathspec 传目录时 `ls-files --others` 仍会列出内部文件，所以面板该显示的新文件不会少。`-uall` 在未被 gitignore 的大目录（如 node_modules）下会把每次扫描变成几万条。

**已知取舍**（写在这里以免将来被当成 bug）：

- 两个会话**同时在执行**工具时，某次改动可能被两边的执行窗口同时看到 → 会被同时归因。偏向多算，不会漏显示。
- 用户在某个工具调用的执行窗口内手动编辑文件，也会算进那个会话。
- 归因信号是 mtime+size；理论上「同尺寸且 mtime 未变」的改写无法识别（实际写入必然更新 mtime）。
- 面板现在只显示本会话改过的文件：如果你自己用编辑器改的文件没被任何会话碰过，它不会出现在面板里（这是会话级隔离的应有之义）。
