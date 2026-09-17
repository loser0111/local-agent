# Claude Code 式 Skills（技能）能力实现方案（开发依据）

> **[已归档]** 本文档描述的是第一版实现方案，已被 `docs/skills-refactor.md` 中的标准对齐重构取代。
> 与本文件冲突之处（尤其是「强制注入正文」`alwaysInject`、手写 frontmatter 解析、单一技能根目录）一律以新文档为准。
> 保留本文件仅作为设计历史参考。

> - 项目：`E:\learn\local-agent`（Wails v2 + Go 后端 + Vue 3 前端）
> - 状态：待实施
> - 用途：为 local-agent 引入对标 Claude Code 的 **Skills（技能）** 能力。本文档为后续开发的唯一依据，包含机制解析、数据契约、完整代码、边界与验收标准。

---

## 一、背景与目标

### 1.1 需求

当前 local-agent 的能力扩展只有「工具（Tool）」一条路径：模型要么调用 `exec_shell`，要么调用用户配置的 CLI / API / MCP 工具。**没有一种「把一段可复用的操作知识（提示词 + 附带脚本/资料）打包、按需加载、由模型自主触发」的机制。**

Claude Code 的 Skills 正是这种机制：一个技能 = 一个文件夹（`SKILL.md` + 可选资源），模型按 `description` 判断是否需要它，需要时才读取正文、执行随附脚本。本方案在 local-agent 中实现等价能力。

### 1.2 目标

| 编号 | 目标 | 优先级 |
| --- | --- | --- |
| G1 | 支持「文件夹 + SKILL.md」式技能定义，启动时自动发现 | P0 |
| G2 | 三级渐进式披露：元数据常驻、正文按需加载、资源按需执行 | P0 |
| G3 | 会话级技能白名单（`EnabledSkills`），与既有 `EnabledTools` 并列 | P0 |
| G4 | 设置页可视化技能管理（新增 / 编辑 / 删除 / 启停 / 校验） | P1 |
| G5 | 新会话弹窗中选择本次可用技能 | P1 |
| G6 | 提供内置 `read_skill` 工具，让模型按需加载技能正文（L2） | P0 |
| G7 | 兼容「显式注入」模式：创建会话时把选中技能正文直接拼进 system prompt | P2 |

### 1.3 非目标（本期不做）

- 技能市场的在线下载 / 版本管理
- 技能之间的依赖解析与拓扑排序
- 技能的自动触发（关键词 / AI 路由）与权限沙箱（沿用既有 `PermissionMode`）
- 插件（Plugin）与连接器（Connector）体系

---

## 二、现状盘点

### 2.1 已有能力（可直接复用）

| 层 | 文件 | 现状 | 对 Skills 的价值 |
| --- | --- | --- | --- |
| 后端 | `chat.go` | `SystemPrompt` 为**硬编码常量**（第 114 行）；`buildLLMMessages()` 组装 `[system, ...history, user]` | **注入 L1 技能清单的唯一入口** |
| 后端 | `tools.go` | `ToolInterface` / `BaseTool` / `CLITool` / `MetaTool`（`tool_router`）；`ToolManager.BuildView()` 按会话白名单组装 `SessionView` | 技能可复用「注册工具」范式 |
| 后端 | `toolruntime.go` | `DynamicCLITool` / `DynamicAPITool` / MCP 工具 | 技能的执行层可复用 CLI 通道 |
| 后端 | `toolstore.go` | `ToolConfig` + JSON 持久化（`~/.local-agent/tools.json`） | 技能存储可照抄此范式 |
| 后端 | `sessions.go` | `Session.EnabledTools []string` 白名单已存在 | **`EnabledSkills` 直接对齐** |
| 后端 | `app.go` | `App` 聚合 store，向 Wails 暴露 bound 方法 | 技能 CRUD 照此暴露 |
| 前端 | `views/Settings.vue` | `tabs=[general,model,tool,permission,about]` + `activeTab` 切换 | **新增一个 `skill` tab 即可** |
| 前端 | `components/business/ToolSettings.vue` | 工具列表卡片 / 搜索 / 开关 / 编辑弹窗 | **SkillSettings.vue 的模板** |
| 前端 | `NewSessionDialog.vue` | 已有「可用工具」多选器 | 增加「可用技能」多选器 |
| 前端 | `stores/tools.js` + `api/tool.js` | Wails / 浏览器 mock 双模式 | 技能 store / api 照抄 |

### 2.2 现状与 Claude Code 的差距

| 维度 | Claude Code | local-agent 现状 | 差距 |
| --- | --- | --- | --- |
| 技能定义 | 文件夹 + `SKILL.md` | 无 | 需新增 |
| 常驻上下文 | 仅技能 `name`+`description` 清单 | system prompt 为固定常量 | 需动态拼接 |
| 正文加载 | 模型用 `Read` 工具按需读 | 无 `read_file` 类工具 | 需新增 `read_skill` |
| 资源执行 | 复用 `Bash` 工具 | 已有 `exec_shell` | **可直接复用** |
| 显式开关 | 插件 / 命令启用 | 已有 `EnabledTools` | 对齐即可 |
| 界面管理 | `+` 按钮 → Skills | 无 | 需新增 tab |

---

## 三、Claude Code Skills 机制解析（对标依据）

> 说明：Claude Code 未开源，以下「目录结构 / frontmatter / 渐进式披露」为官方文档明确的部分，「具体实现细节」为合理推断，本方案只对齐其**可观测行为**。

### 3.1 一个技能 = 一个文件夹

```
my-skill/
├── SKILL.md          # 必需：YAML frontmatter + Markdown 正文
├── reference.md      # 可选：参考资料
├── scripts/run.py    # 可选：可执行脚本
└── templates/x.html  # 可选：模板资源
```

### 3.2 SKILL.md 与 frontmatter

```markdown
---
name: pdf-report
description: 从 CSV 生成 PDF 报告。当用户需要把表格数据导出成 PDF 时使用。
---

# PDF 报告生成

## 步骤
1. 读取 CSV 文件
2. 运行 python scripts/run.py input.csv
3. 输出 PDF 保存到当前目录
```

- `name` + `description` 是**唯一常驻上下文**的部分，`description` 直接决定模型能否选中该技能；
- 正文是给模型的「操作手册」，可引用同目录其它文件。

### 3.3 三级渐进式披露（核心）

| 级别 | 加载内容 | 时机 | 常驻成本 |
| --- | --- | --- | --- |
| **L1 元数据** | 所有技能的 `name` + `description` | **始终**在 system prompt | 每技能约几十 token |
| **L2 正文** | 命中技能的 `SKILL.md` 全文 | 模型判断相关时**用 Read 工具读入** | 仅命中时消耗 |
| **L3 资源** | 技能目录内的脚本 / 资料 | 执行 / 查阅时 | 按需，甚至不进上下文 |

价值：装 N 个技能，常驻上下文只增加「N 行标题」，正文与资源只在真正用到时才付费。

### 3.4 调用流程（推断）

```
用户: 把这份数据导成 PDF
  -> 模型看 L1 清单，发现 pdf-report.description 命中
  -> 调用 Read(~/.claude/skills/pdf-report/SKILL.md)   # L2
  -> 读到手冊，调用 Bash(python scripts/run.py data.csv) # L3
  -> 脚本输出返回，模型汇总答复
```

**关键点：整个机制复用已有的 `Read` / `Bash` 两个工具，Skills 并未引入新的执行引擎。**

### 3.5 对标结论（本方案要实现的四件事）

1. **注册**：扫描技能目录，只取出 `name` + `description`；
2. **注入（L1）**：把清单拼接进 system prompt；
3. **加载（L2）**：新增 `read_skill` 工具，模型按需取正文；
4. **执行（L3）**：复用现有 `exec_shell`，并在正文中告知模型可用它跑脚本。

---

## 四、方案选型与总体设计

### 4.1 加载策略选型

| 方案 | 原理 | 优点 | 缺点 |
| --- | --- | --- | --- |
| **A. 自动检索（Read 式）** | L1 清单常驻，模型自己决定是否调 `read_skill` 加载正文 | 上下文最省，最贴近 Claude Code | 依赖模型自觉；小模型可能不会调 |
| **B. 显式注入** | 会话创建时把选中技能正文全部拼进 system prompt | 确定性强，任何模型都生效 | 上下文成本高，技能多时爆 prompt |
| **C. A + B 混合（本期采用）** | 默认 A；同时提供开关，可将某些技能标记为「强制注入」 | 兼顾省 token 与可控性 | 实现略复杂 |

**结论：默认采用 A（L1 清单 + `read_skill` 工具），并保留 B 作为可选项（`SkillMeta.AlwaysInject`）。**

> 这样的好处：即使是能力较弱、不主动调工具的模型，只要把常用技能标为 `alwaysInject`，也能退化为 B 方案正常工作。

### 4.2 总体数据流

```
启动 startup()
  -> NewSkillStore(~/.local-agent/skills)   # 扫描目录，只解析 frontmatter -> []SkillMeta

用户发消息 Chat(sid, query)
  -> executeChat()
       -> 读 session.EnabledSkills 得到本次可用技能
       -> buildLLMMessages(session, query, skillIndex)   # L1：把技能清单拼进 system prompt
       -> ToolManager.BuildView(...) 时额外注册 read_skill 工具   # L2 通道
       -> 工具循环：模型可能调 read_skill(id) 取正文
                    -> 返回 SKILL.md 全文
                    -> 模型再调 exec_shell 跑技能脚本   # L3
```

### 4.3 设计原则

1. **零依赖**：不引入 YAML 库，frontmatter 用手写子集解析（只取 `name` / `description`）。
2. **文件夹即真相**：技能的正文与资源以磁盘文件为准，JSON 只存「开关状态」（enabled / alwaysInject）。
3. **渐进式披露**：常驻上下文仅 L1 清单；正文经 `read_skill` 按需加载。
4. **失败不阻断**：单个技能解析失败不影响启动与对话，错误仅在该技能卡片上展示。
5. **路径安全**：`read_skill` 只允许读取技能目录内以 `SKILL.md` 为根的内容，防止路径穿越。

---

## 五、数据契约

> 契约是前后端并行开发的分界点，**先冻结本节再开工**。

### 5.1 磁盘目录结构

```
~/.local-agent/
├── skills/
│   ├── pdf-report/
│   │   ├── SKILL.md
│   │   └── scripts/run.py
│   └── db-migrate/
│       └── SKILL.md
└── skills_state.json      # 仅存开关状态（新增文件）
```

`skills_state.json` 示例：

```json
[
  { "id": "pdf-report", "enabled": true, "alwaysInject": false },
  { "id": "db-migrate", "enabled": false, "alwaysInject": false }
]
```

### 5.2 Go 侧结构体（`skills.go`）

```go
// SkillMeta 技能元数据（L1，会进入 system prompt）
type SkillMeta struct {
	ID           string `json:"id"`           // 目录名，唯一
	Name         string `json:"name"`         // frontmatter.name
	Description  string `json:"description"`  // frontmatter.description
	Dir          string `json:"dir"`          // 技能目录绝路径
	Enabled      bool   `json:"enabled"`      // 是否全局启用
	AlwaysInject bool   `json:"alwaysInject"` // true=正文直接注入 system prompt
	Builtin      bool   `json:"builtin"`      // 内置不可删
	HasScripts   bool   `json:"hasScripts"`   // 是否存在 scripts/ 目录
	Error        string `json:"error"`        // 解析/校验错误（展示用）
}

// SkillDetail 技能详情（含正文，供界面预览与 read_skill 返回）
type SkillDetail struct {
	SkillMeta
	Body string `json:"body"`
}
```

### 5.3 前端 JSDoc 侧（`types/index.js` 增补）

```js
/**
 * 技能元数据
 * @typedef {Object} SkillMeta
 * @property {string} id
 * @property {string} name
 * @property {string} description
 * @property {string} dir
 * @property {boolean} enabled
 * @property {boolean} alwaysInject
 * @property {boolean} builtin
 * @property {boolean} hasScripts
 * @property {string} error
 */

/**
 * 技能详情
 * @typedef {SkillMeta & { body: string }} SkillDetail
 */
```

### 5.4 契约约定

| 约定 | 说明 |
| --- | --- |
| `id` | 等于技能目录名（如 `pdf-report`），全局唯一，前端以它为主键 |
| `name` / `description` | 缺失时回退：`name` 缺则用 `id`，`description` 缺则标 `Error` 并排除出 L1 |
| `enabled` | 全局总开关；会话白名单在其基础上再收窄 |
| `alwaysInject` | true 时正文直接拼进 system prompt（方案 B） |
| `body` | 仅 `getSkill` / `read_skill` 返回，**列表接口不返回**（避免上下文膨胀） |
| 空结果 | Go 端一律返回 `[]SkillMeta{}` 而非 `nil` |

---

## 六、后端实现

### 6.1 新增文件 `skills.go`

> 零外部依赖；frontmatter 采用手写子集解析（只识别 `name` / `description`），避免引入 YAML 库。

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "sync"
)

const (
    skillFileName     = "SKILL.md"
    skillScriptsDir   = "scripts"
    maxSkillBodyBytes = 256 * 1024 // 正文上限 256KB
    skillListMaxDesc  = 200        // L1 清单单条描述截断长度
)

// ===== 数据结构（对应 5.2）=====

// SkillMeta 技能元数据（L1，会进入 system prompt）
type SkillMeta struct {
    ID           string `json:"id"`
    Name         string `json:"name"`
    Description  string `json:"description"`
    Dir          string `json:"dir"`
    Enabled      bool   `json:"enabled"`
    AlwaysInject bool   `json:"alwaysInject"`
    Builtin      bool   `json:"builtin"`
    HasScripts   bool   `json:"hasScripts"`
    Error        string `json:"error"`
}

// SkillDetail 技能详情（含正文，供界面预览与 read_skill 返回）
type SkillDetail struct {
    SkillMeta
    Body string `json:"body"`
}

// SkillState 开关状态（持久化到 skills_state.json）
type SkillState struct {
    ID           string `json:"id"`
    Enabled      bool   `json:"enabled"`
    AlwaysInject bool   `json:"alwaysInject"`
}

// ===== SkillStore =====

// SkillStore 扫描技能目录、维护开关状态
type SkillStore struct {
    mu        sync.RWMutex
    dir       string
    statePath string
    skills    []*SkillMeta
}

// NewSkillStore 创建技能存储并首次扫描目录
func NewSkillStore(dir string) *SkillStore {
    s := &SkillStore{
        dir:       dir,
        statePath: filepath.Join(filepath.Dir(dir), "skills_state.json"),
    }
    _ = os.MkdirAll(dir, 0o755)
    s.scan()
    return s
}

// scan 遍历技能目录，解析每个子目录下的 SKILL.md
func (s *SkillStore) scan() {
    s.mu.Lock()
    defer s.mu.Unlock()

    states := s.loadStatesLocked()

    entries, err := os.ReadDir(s.dir)
    if err != nil {
        s.skills = []*SkillMeta{}
        return
    }

    skills := make([]*SkillMeta, 0, len(entries))
    for _, e := range entries {
        if !e.IsDir() {
            continue
        }
        id := e.Name()
        meta := parseSkillDir(id, filepath.Join(s.dir, id))
        if st, ok := states[id]; ok {
            meta.Enabled = st.Enabled
            meta.AlwaysInject = st.AlwaysInject
        } else {
            meta.Enabled = true // 新技能默认启用
        }
        skills = append(skills, meta)
    }
    sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
    s.skills = skills
}

// parseSkillDir 解析单个技能目录
func parseSkillDir(id, dir string) *SkillMeta {
    meta := &SkillMeta{ID: id, Name: id, Dir: dir}
    data, err := os.ReadFile(filepath.Join(dir, skillFileName))
    if err != nil {
        meta.Error = "缺少 SKILL.md"
        return meta
    }
    name, desc, _, err := parseFrontmatter(string(data))
    if err != nil {
        meta.Error = err.Error()
        return meta
    }
    if name != "" {
        meta.Name = name
    }
    meta.Description = desc
    if desc == "" {
        meta.Error = "frontmatter 缺少 description"
    }
    if fi, err := os.Stat(filepath.Join(dir, skillScriptsDir)); err == nil && fi.IsDir() {
        meta.HasScripts = true
    }
    return meta
}

// parseFrontmatter 解析 SKILL.md 的 YAML frontmatter（仅支持 key: value 单行子集）
// 返回 name、description 与不含 frontmatter 的正文
func parseFrontmatter(text string) (name, desc, body string, err error) {
    text = strings.ReplaceAll(text, "\r\n", "\n")
    if !strings.HasPrefix(text, "---\n") {
        return "", "", text, nil // 无 frontmatter，宽松处理
    }
    rest := text[len("---\n"):]
    end := strings.Index(rest, "\n---")
    if end < 0 {
        return "", "", "", fmt.Errorf("frontmatter 未闭合")
    }
    head := rest[:end]
    body = strings.TrimLeft(rest[end+len("\n---"):], "\n")

    for _, line := range strings.Split(head, "\n") {
        line = strings.TrimSpace(line)
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        k, v, ok := strings.Cut(line, ":")
        if !ok {
            continue
        }
        k = strings.ToLower(strings.TrimSpace(k))
        v = strings.Trim(strings.TrimSpace(v), `"'`)
        switch k {
        case "name":
            name = v
        case "description":
            desc = v
        }
    }
    return name, desc, body, nil
}

// ===== 读取 =====

// GetAll 返回全部技能元数据（副本）
func (s *SkillStore) GetAll() []*SkillMeta {
    s.mu.RLock()
    defer s.mu.RUnlock()
    out := make([]*SkillMeta, len(s.skills))
    copy(out, s.skills)
    return out
}

// GetDetail 返回含正文的技能详情
func (s *SkillStore) GetDetail(id string) (*SkillDetail, bool) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    for _, m := range s.skills {
        if m.ID == id {
            body, _ := readSkillBody(m.Dir)
            return &SkillDetail{SkillMeta: *m, Body: body}, true
        }
    }
    return nil, false
}

// LoadBody 读取技能正文（供 read_skill 工具）
func (s *SkillStore) LoadBody(id string) (string, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    for _, m := range s.skills {
        if m.ID == id {
            return readSkillBody(m.Dir)
        }
    }
    return "", fmt.Errorf("技能不存在: %s", id)
}

// readSkillBody 读取并截断 SKILL.md 正文
func readSkillBody(dir string) (string, error) {
    data, err := os.ReadFile(filepath.Join(dir, skillFileName))
    if err != nil {
        return "", fmt.Errorf("读取 SKILL.md 失败: %w", err)
    }
    if len(data) > maxSkillBodyBytes {
        data = data[:maxSkillBodyBytes]
    }
    _, _, body, _ := parseFrontmatter(string(data))
    return body, nil
}

// ===== 开关状态 =====

// loadStatesLocked 读取状态文件；调用方需持锁
func (s *SkillStore) loadStatesLocked() map[string]SkillState {
    m := map[string]SkillState{}
    data, err := os.ReadFile(s.statePath)
    if err != nil {
        return m
    }
    var list []SkillState
    if json.Unmarshal(data, &list) != nil {
        return m
    }
    for _, st := range list {
        m[st.ID] = st
    }
    return m
}

// saveStates 持久化开关状态
func (s *SkillStore) saveStates() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    list := make([]SkillState, 0, len(s.skills))
    for _, m := range s.skills {
        list = append(list, SkillState{ID: m.ID, Enabled: m.Enabled, AlwaysInject: m.AlwaysInject})
    }
    data, err := json.MarshalIndent(list, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(s.statePath, data, 0o644)
}

// SetEnabled 切换技能启用状态
func (s *SkillStore) SetEnabled(id string, enabled bool) error {
    s.mu.Lock()
    found := false
    for _, m := range s.skills {
        if m.ID == id {
            m.Enabled = enabled
            found = true
            break
        }
    }
    s.mu.Unlock()
    if !found {
        return fmt.Errorf("技能不存在: %s", id)
    }
    return s.saveStates()
}

// SetAlwaysInject 切换「强制注入正文」
func (s *SkillStore) SetAlwaysInject(id string, v bool) error {
    s.mu.Lock()
    found := false
    for _, m := range s.skills {
        if m.ID == id {
            m.AlwaysInject = v
            found = true
            break
        }
    }
    s.mu.Unlock()
    if !found {
        return fmt.Errorf("技能不存在: %s", id)
    }
    return s.saveStates()
}

// ===== 增删 =====

// SaveSkill 新建或更新技能：写入 <dir>/<id>/SKILL.md，然后重扫
func (s *SkillStore) SaveSkill(id, name, desc, body string) error {
    if !validSkillID(id) {
        return fmt.Errorf("技能 id 只能包含字母、数字、下划线、短横线")
    }
    if strings.TrimSpace(name) == "" {
        return fmt.Errorf("技能名称不能为空")
    }
    skillDir := filepath.Join(s.dir, id)
    if err := os.MkdirAll(skillDir, 0o755); err != nil {
        return fmt.Errorf("创建技能目录失败: %w", err)
    }
    content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n",
        name, strings.TrimSpace(desc), strings.TrimRight(body, "\n"))
    if err := os.WriteFile(filepath.Join(skillDir, skillFileName), []byte(content), 0o644); err != nil {
        return fmt.Errorf("写入 SKILL.md 失败: %w", err)
    }
    s.scan()
    return nil
}

// DeleteSkill 删除技能目录（内置拒绝）
func (s *SkillStore) DeleteSkill(id string) error {
    s.mu.RLock()
    var target *SkillMeta
    for _, m := range s.skills {
        if m.ID == id {
            target = m
            break
        }
    }
    s.mu.RUnlock()
    if target == nil {
        return fmt.Errorf("技能不存在: %s", id)
    }
    if target.Builtin {
        return fmt.Errorf("内置技能不可删除")
    }
    if err := os.RemoveAll(target.Dir); err != nil {
        return fmt.Errorf("删除技能目录失败: %w", err)
    }
    s.scan()
    return nil
}

// EnsureDefaultSkill 首次运行写入示例技能
func (s *SkillStore) EnsureDefaultSkill() {
    if len(s.GetAll()) > 0 {
        return
    }
    _ = s.SaveSkill("hello-skill", "示例技能",
        "演示用技能：当用户请求打招呼时给出固定问候。",
        "# 示例技能\n\n当用户请求打招呼时，直接回复：你好，我是 local-agent 的技能。\n")
}

// validSkillID 校验技能 id（同工具名规则）
func validSkillID(id string) bool {
    if id == "" || len(id) > 64 {
        return false
    }
    for _, r := range id {
        if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' ||
            r >= '0' && r <= '9' || r == '_' || r == '-' {
            return false
        }
    }
    return true
}

// ===== L1 清单 / 强制注入 =====

// BuildSkillIndex 生成 L1 技能清单（注入 system prompt）
func BuildSkillIndex(skills []*SkillMeta) string {
    var sb strings.Builder
    for _, m := range skills {
        if !m.Enabled || m.Error != "" {
            continue
        }
        desc := m.Description
        if r := []rune(desc); len(r) > skillListMaxDesc {
            desc = string(r[:skillListMaxDesc]) + "..."
        }
        sb.WriteString(fmt.Sprintf("- %s (id: %s): %s\n", m.Name, m.ID, desc))
    }
    return strings.TrimRight(sb.String(), "\n")
}

// BuildAlwaysInjectBlock 生成「强制注入」技能正文段（方案 B）
func BuildAlwaysInjectBlock(skills []*SkillMeta) string {
    var sb strings.Builder
    for _, m := range skills {
        if !m.Enabled || !m.AlwaysInject || m.Error != "" {
            continue
        }
        body, err := readSkillBody(m.Dir)
        if err != nil {
            continue
        }
        sb.WriteString(fmt.Sprintf("\n\n### 技能：%s\n%s", m.Name, body))
    }
    return sb.String()
}

// hasEnabledSkill 是否存在已启用且无错的技能
func hasEnabledSkill(skills []*SkillMeta) bool {
    for _, m := range skills {
        if m.Enabled && m.Error == "" {
            return true
        }
    }
    return false
}

// ===== 内置工具：read_skill（L2 按需加载）=====

// ReadSkillTool 让模型按需读取 SKILL.md 正文
type ReadSkillTool struct {
    *BaseTool
    store *SkillStore
}

func NewReadSkillTool(store *SkillStore) *ReadSkillTool {
    return &ReadSkillTool{
        BaseTool: &BaseTool{
            Name: "read_skill",
            Description: "读取指定技能的完整说明（SKILL.md 正文）。" +
                "当用户请求与某个技能的 description 匹配时，先调用本工具获取正文，" +
                "再按正文步骤执行；如需运行技能自带脚本，请使用 exec_shell。",
            Parameters: map[string]*ToolArgDef{
                "id": {Type: "string", Description: "技能 id（从可用技能清单获取）"},
            },
        },
        store: store,
    }
}

func (t *ReadSkillTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
    id, _ := args["id"].(string)
    if id == "" {
        return "", fmt.Errorf("id 参数是必需的")
    }
    detail, ok := t.store.GetDetail(id)
    if !ok {
        return "", fmt.Errorf("技能不存在: %s", id)
    }
    return fmt.Sprintf("# 技能：%s\n\n%s", detail.Name, detail.Body), nil
}

```

### 6.2 `tools.go`：ToolManager 接线 + 注册 read_skill

```go
// ToolManager 增加 skills 依赖
type ToolManager struct {
    store  *ToolStore
    skills *SkillStore // 新增
    pool   *MCPPool
}

func NewToolManager(store *ToolStore, skills *SkillStore) *ToolManager {
    return &ToolManager{store: store, skills: skills, pool: NewMCPPool()}
}
```

`BuildView()` 末尾（组装 `SessionView` 之前）追加：

```go
    // 内置：技能加载工具（存在已启用技能时注册）
    if tm.skills != nil && hasEnabledSkill(tm.skills.GetAll()) {
        rst := NewReadSkillTool(tm.skills)
        registry[rst.GetName()] = rst
        typeMap[rst.GetName()] = ToolTypeBuiltin
    }
```

> 这样 `read_skill` 会经 `MetaTool(tool_router)` 的 `list` 被发现，无需额外暴露。它不受会话工具白名单影响（与 `exec_shell` 同级）。

### 6.3 `chat.go`：注入 L1 清单 / 强制注入正文

**（1）`buildLLMMessages` 改为接受 system prompt 参数**

```go
// 旧：buildLLMMessages(session *Session, query string)
// 新：
func buildLLMMessages(session *Session, query string, systemPrompt string) []LLMMessage {
    messages := []LLMMessage{
        {Role: RoleSystem, Content: systemPrompt}, // 由调用方拼接技能上下文
    }
    // ...其余历史消息拼装逻辑不变...
}
```

**（2）`executeChat` 中组装技能上下文**（位于第 4 步 "构建 LLM messages" 处）

```go
    // 4. 组装技能上下文（L1 清单 + 强制注入正文）
    skillPrompt := SystemPrompt
    if a.skillStore != nil {
        enabled := a.enabledSkillsForSession(session)
        if idx := BuildSkillIndex(enabled); idx != "" {
            skillPrompt += "\n\n## 可用技能（Skills）\n" +
                "需要时先调用 read_skill 工具获取技能完整说明，再按说明执行：\n" + idx
        }
        if block := BuildAlwaysInjectBlock(enabled); block != "" {
            skillPrompt += "\n\n## 已直接加载的技能正文" + block
        }
    }
    messages := buildLLMMessages(session, query, skillPrompt)
```

**（3）新增辅助方法（`app.go` 或 `chat.go`）**

```go
// enabledSkillsForSession 按会话白名单过滤技能；白名单为空=全部已启用
func (a *App) enabledSkillsForSession(session *Session) []*SkillMeta {
    all := a.skillStore.GetAll()
    if len(session.EnabledSkills) == 0 {
        return all
    }
    wl := make(map[string]bool, len(session.EnabledSkills))
    for _, id := range session.EnabledSkills {
        wl[id] = true
    }
    out := make([]*SkillMeta, 0, len(all))
    for _, m := range all {
        if wl[m.ID] {
            out = append(out, m)
        }
    }
    return out
}
```

> `SystemPrompt` 常量本身不改，保持基础人设；技能信息运行时拼接。

### 6.4 `sessions.go`：会话级技能白名单

```go
// Session 新增字段
type Session struct {
    // ...原有字段...
    EnabledTools  []string `json:"enabledTools,omitempty"`
    EnabledSkills []string `json:"enabledSkills,omitempty"` // 新增
}

// SessionConfig 同步新增
type SessionConfig struct {
    // ...原有字段...
    EnabledTools  []string `json:"enabledTools"`
    EnabledSkills []string `json:"enabledSkills"` // 新增
}
```

`CreateSession` 中赋值 `EnabledSkills: config.EnabledSkills,` 即可（其余逻辑不变）。

### 6.5 `app.go`：初始化与 bound 方法

```go
type App struct {
    ctx          context.Context
    modelStore   *ModelStore
    sessionStore *SessionStore
    toolStore    *ToolStore
    toolManager  *ToolManager
    skillStore   *SkillStore // 新增
    diffService  *DiffService
}
```

`startup()` 中，在创建 `toolManager` **之前**初始化：

```go
    // 初始化技能存储：~/.local-agent/skills/
    a.skillStore = NewSkillStore(filepath.Join(baseDir, "skills"))
    a.skillStore.EnsureDefaultSkill() // 首次运行写入示例

    a.toolStore = NewToolStore(filepath.Join(baseDir, "tools.json"))
    a.toolManager = NewToolManager(a.toolStore, a.skillStore) // 传入 skillStore
```

新增 bound 方法（供前端调用）：

```go
// ===== 技能管理 =====

// ListSkills 返回全部技能元数据（不含正文）
func (a *App) ListSkills() []*SkillMeta {
    return a.skillStore.GetAll()
}

// GetSkill 返回含正文的技能详情
func (a *App) GetSkill(id string) (*SkillDetail, error) {
    d, ok := a.skillStore.GetDetail(id)
    if !ok {
        return nil, fmt.Errorf("技能不存在: %s", id)
    }
    return d, nil
}

// SaveSkill 新建或更新技能
func (a *App) SaveSkill(id, name, description, body string) error {
    return a.skillStore.SaveSkill(id, name, description, body)
}

// DeleteSkill 删除技能
func (a *App) DeleteSkill(id string) error {
    return a.skillStore.DeleteSkill(id)
}

// ToggleSkill 启用/停用技能
func (a *App) ToggleSkill(id string, enabled bool) error {
    return a.skillStore.SetEnabled(id, enabled)
}

// SetSkillAlwaysInject 设置「强制注入正文」
func (a *App) SetSkillAlwaysInject(id string, v bool) error {
    return a.skillStore.SetAlwaysInject(id, v)
}

// RefreshSkills 重新扫描技能目录
func (a *App) RefreshSkills() []*SkillMeta {
    return a.skillStore.GetAll()
}

// SkillsDir 返回技能目录绝对路径（供界面「打开目录」）
func (a *App) SkillsDir() string {
    return a.skillStore.dir
}
```

### 6.6 重新生成 Wails 绑定

新增 bound 方法后**必须**重新生成，否则前端 `import` 得到 `undefined`：

```bash
cd E:\learn\local-agent
wails generate module      # 或直接 wails dev 一次
```

确认 `frontend/wailsjs/go/main/App.js` 与 `App.d.ts` 中出现 `ListSkills` / `GetSkill` / `SaveSkill` / `DeleteSkill` / `ToggleSkill` / `SetSkillAlwaysInject` / `RefreshSkills` / `SkillsDir`。

---

## 七、前端实现

### 7.1 新增 `frontend/src/api/skill.js`

照搬 `api/tool.js` 的「Wails / 浏览器 mock 双模式」结构。

```js
import {
  ListSkills,
  GetSkill,
  SaveSkill,
  DeleteSkill,
  ToggleSkill,
  SetSkillAlwaysInject,
  RefreshSkills,
  SkillsDir,
} from '@/../wailsjs/go/main/App'

function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

// ===== 浏览器 dev mock（localStorage）=====
const MOCK_KEY = 'local-agent:skills'

function defaultMockSkills() {
  return [
    {
      id: 'hello-skill',
      name: '示例技能',
      description: '演示用技能：当用户请求打招呼时给出固定问候。',
      dir: '~/.local-agent/skills/hello-skill',
      enabled: true,
      alwaysInject: false,
      builtin: false,
      hasScripts: false,
      error: '',
    },
  ]
}

function getMockSkills() {
  try {
    const raw = localStorage.getItem(MOCK_KEY)
    if (!raw) {
      const d = defaultMockSkills()
      localStorage.setItem(MOCK_KEY, JSON.stringify(d))
      return d
    }
    return JSON.parse(raw)
  } catch {
    return defaultMockSkills()
  }
}

function saveMockSkills(list) {
  localStorage.setItem(MOCK_KEY, JSON.stringify(list))
}

export async function fetchSkills() {
  if (isWails()) return (await ListSkills()) || []
  return getMockSkills()
}

export async function getSkill(id) {
  if (isWails()) return await GetSkill(id)
  const m = getMockSkills().find((s) => s.id === id)
  if (!m) throw new Error('技能不存在')
  return { ...m, body: '# ' + m.name + '\n\n（mock 正文）' }
}

export async function saveSkill(skill) {
  if (isWails()) return await SaveSkill(skill.id, skill.name, skill.description, skill.body || '')
  const list = getMockSkills()
  const idx = list.findIndex((s) => s.id === skill.id)
  if (idx > -1) list[idx] = { ...list[idx], ...skill }
  else list.push({ ...skill, enabled: true, alwaysInject: false, builtin: false, error: '' })
  saveMockSkills(list)
}

export async function deleteSkill(id) {
  if (isWails()) return await DeleteSkill(id)
  saveMockSkills(getMockSkills().filter((s) => s.id !== id))
}

export async function toggleSkill(id, enabled) {
  if (isWails()) return await ToggleSkill(id, enabled)
  const list = getMockSkills()
  const t = list.find((s) => s.id === id)
  if (t) t.enabled = enabled
  saveMockSkills(list)
}

export async function setSkillAlwaysInject(id, v) {
  if (isWails()) return await SetSkillAlwaysInject(id, v)
  const list = getMockSkills()
  const t = list.find((s) => s.id === id)
  if (t) t.alwaysInject = v
  saveMockSkills(list)
}

export async function refreshSkills() {
  if (isWails()) return (await RefreshSkills()) || []
  return getMockSkills()
}

export async function skillsDir() {
  if (isWails()) return await SkillsDir()
  return '~/.local-agent/skills'
}
```

### 7.2 新增 `frontend/src/stores/skills.js`

对照 `stores/tools.js`：

```js
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  fetchSkills, getSkill, saveSkill, deleteSkill, toggleSkill, setSkillAlwaysInject, refreshSkills,
} from '@/api/skill'

export const useSkillsStore = defineStore('skills', () => {
  const skills = ref([])
  const loading = ref(false)
  const loaded = ref(false)

  // 已启用的顶层技能（新会话选择器用）
  const enabledSkills = computed(() => skills.value.filter((s) => s.enabled && !s.error))

  async function load(force = false) {
    if (loaded.value && !force) return
    loading.value = true
    try {
      skills.value = (await fetchSkills()) || []
      loaded.value = true
    } catch (e) {
      console.error('加载技能列表失败:', e)
      skills.value = []
    } finally {
      loading.value = false
    }
  }

  async function detail(id) {
    return await getSkill(id)
  }

  async function save(skill) {
    await saveSkill(skill)
    await load(true)
  }

  async function remove(id) {
    await deleteSkill(id)
    skills.value = skills.value.filter((s) => s.id !== id)
  }

  async function toggle(id, enabled) {
    await toggleSkill(id, enabled)
    const s = skills.value.find((x) => x.id === id)
    if (s) s.enabled = enabled
  }

  async function setAlwaysInject(id, v) {
    await setSkillAlwaysInject(id, v)
    const s = skills.value.find((x) => x.id === id)
    if (s) s.alwaysInject = v
  }

  async function refresh() {
    await load(true)
  }

  function clear() {
    skills.value = []
    loaded.value = false
  }

  return {
    skills, loading, loaded, enabledSkills,
    load, detail, save, remove, toggle, setAlwaysInject, refresh, clear,
  }
})
```

### 7.3 新增 `frontend/src/components/business/SkillSettings.vue`

以 `ToolSettings.vue` 为模板，差异点：

| 项 | ToolSettings | SkillSettings |
| --- | --- | --- |
| 类型标签 | builtin/cli/mcp/api | 无（技能不分型） |
| 卡片副标题 | `t.name` | `id` + `dir` 工具提示 |
| 错误展示 | MCP 连接状态 | `s.error`（frontmatter 校验） |
| 额外开关 | 无 | 「强制注入」`alwaysInject` |
| 附加信息 | `discovered` 子工具 | `hasScripts` 标记 |

核心模板骨架（省略样式，直接复用 `.tool-settings` 的类名）：

```vue
<script setup>
import { ref, computed, onMounted } from 'vue'
import { Plus, Pencil, Trash2, RefreshCw, Loader2, FolderOpen } from 'lucide-vue-next'
import { useSkillsStore } from '@/stores/skills'
import SkillEditDialog from './SkillEditDialog.vue'
import { skillsDir } from '@/api/skill'

const store = useSkillsStore()
const keyword = ref('')
const dialogVisible = ref(false)
const editing = ref(null)
const dir = ref('')

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return store.skills
  return store.skills.filter(
    (s) => s.id.toLowerCase().includes(kw) ||
           (s.name || '').toLowerCase().includes(kw) ||
           (s.description || '').toLowerCase().includes(kw)
  )
})

onMounted(async () => {
  store.load()
  try { dir.value = await skillsDir() } catch {}
})

function openAdd() { editing.value = null; dialogVisible.value = true }
function openEdit(s) { editing.value = s; dialogVisible.value = true }
</script>
```

### 7.4 新增 `SkillEditDialog.vue`

表单字段：`id`（新增时可编辑，编辑时禁用）、`name`、`description`、`body`（多行）。保存调 `store.save({ id, name, description, body })`。可参考 `ToolEditDialog.vue` 的弹窗与表单样式。

### 7.5 `views/Settings.vue` 增加 tab

```js
import SkillSettings from '@/components/business/SkillSettings.vue'

const tabs = [
  { key: 'general', label: '通用设置' },
  { key: 'model', label: '模型配置' },
  { key: 'tool', label: '工具配置' },
  { key: 'skill', label: '技能配置' }, // 新增
  { key: 'permission', label: '权限配置' },
  { key: 'about', label: '关于' },
]
```

模板中新增（与 `ToolSettings` 并列）：

```vue
<SkillSettings v-if="activeTab === 'skill'" />
```

### 7.6 `NewSessionDialog.vue` 增加「可用技能」多选

与现有「可用工具」完全同构：

```js
import { useSkillsStore } from '@/stores/skills'

const skillsStore = useSkillsStore()
const selectedSkillIds = ref([])
const allSkillsSelected = computed(
  () => selectedSkillIds.value.length === skillsStore.enabledSkills.length
)

onMounted(async () => {
  await skillsStore.load()
  selectedSkillIds.value = skillsStore.enabledSkills.map((s) => s.id)
})

function toggleSkill(id) {
  const i = selectedSkillIds.value.indexOf(id)
  if (i > -1) selectedSkillIds.value.splice(i, 1)
  else selectedSkillIds.value.push(id)
}

function confirm() {
  const enabledSkills = allSkillsSelected.value ? [] : [...selectedSkillIds.value]
  emit('confirm', { ...form, title: '新会话', enabledTools, enabledSkills })
}
```

### 7.7 `types/index.js` 增补 JSDoc

见 5.3。

### 7.8 无需改动

- `stores/session.js` 创建会话时 `enabledSkills` 直接透传（后端 `SessionConfig` 已支持）。
- `ChatPane.vue`：技能加载由模型自主调 `read_skill`，前端无需感知；如后续要做「技能调用卡片」，可监听 `chat:event` 的 `tool_call_*`（`read_skill` 会作为普通工具调用出现）。

---

## 八、边界情况与风险清单

| 编号 | 风险 / 场景 | 处理策略 |
| --- | --- | --- |
| R1 | 技能目录不存在 | `NewSkillStore` 自动 `MkdirAll`，首次返回空列表 |
| R2 | SKILL.md 缺失 / frontmatter 未闭合 | 标记 `error`，排除出 L1 清单，界面卡片展示错误，**不阻断启动** |
| R3 | `description` 缺失 | 标 `error` 并排除出 L1（description 是路由依据，不能省） |
| R4 | 技能数量很多 | L1 只带 name+description；建议超过 50 个时在描述上做聚合提示 |
| R5 | 正文过大 | `maxSkillBodyBytes=256KB` 截断 |
| R6 | 路径穿越（read_skill） | 只按 `id` 从已扫描列表查，**不接受任意路径**，天然防穿越 |
| R7 | 模型不主动调 read_skill | 提供 `alwaysInject` 退化为方案 B |
| R8 | 技能脚本执行 | 复用 `exec_shell`，权限受会话 `PermissionMode` 约束（与工具一致） |
| R9 | 并发读写状态文件 | `SkillStore` 用 `sync.RWMutex` 保护 |
| R10 | 用户手动改技能目录 | 提供 `RefreshSkills`；或监听目录变化（本期手动刷新即可） |
| R11 | 中文 / 非 ASCII | 全程 UTF-8，frontmatter 解析不依赖 ASCII 分隔 |
| R12 | Wails 绑定未更新 | 新增 bound 方法后必须重新生成（见 6.6） |
| R13 | 与工具重名 | `read_skill` 为保留名，需在 `validToolName` 侧禁止用户自定义同名工具（可选） |

---

## 九、分阶段实施计划与验收

| 阶段 | 内容 | 预估 | 验收标准 |
| --- | --- | --- | --- |
| **P0** | `skills.go` + `SkillStore` 扫描 + `read_skill` 工具 + `chat.go` 注入 L1 | 2–3h | 在 `~/.local-agent/skills/` 放一个技能，对话时模型能调 `read_skill` 拿到正文 |
| **P1** | `app.go` bound 方法 + `api/skill.js` + `stores/skills.js` + `SkillSettings.vue` | 2–3h | 设置页能新建 / 编辑 / 删除 / 启停技能，重扫后列表更新 |
| **P2** | `EnabledSkills` 会话白名单 + `NewSessionDialog` 技能选择 | 1h | 新会话可选技能，未选中的技能不出现在 L1 清单 |
| **P3（可选）** | `alwaysInject` 强制注入 + 脚本类示例技能（L3 跑通） | 1h | 标记 `alwaysInject` 的技能正文直接进 system prompt；技能脚本能经 `exec_shell` 执行 |

---

## 十、测试方案

### 10.1 后端单测（新增 `skills_test.go`）

| 用例 | 输入 | 断言 |
| --- | --- | --- |
| T1 标准解析 | 带 name/description 的 SKILL.md | `Name` / `Description` / `Body` 正确 |
| T2 无 frontmatter | 纯 Markdown | 不报错，`Body` 为全文，`Error` 提示缺 description |
| T3 未闭合 | 首行 `---` 但无结束 `---` | `Error == "frontmatter 未闭合"` |
| T4 引号去除 | `description: "带引号"` | 值不含引号 |
| T5 CRLF | Windows 换行 | 解析正常 |
| T6 L1 生成 | 含禁用 / 错误技能 | `BuildSkillIndex` 不包含禁用与错误项 |
| T7 ID 校验 | 含非法字符的 id | `validSkillID` 返回 false |
| T8 增删 | SaveSkill 后 DeleteSkill | 目录创建 / 删除，列表同步 |

### 10.2 手动验证（端到端）

```bat
:: 1) 创建示例技能目录
mkdir %USERPROFILE%\.local-agent\skills\pdf-report
:: 2) 写入 SKILL.md（带 frontmatter + 步骤）
:: 3) 启动应用，设置-技能配置应能看到 pdf-report
:: 4) 新建会话（勾选该技能），发一句「帮我把数据导成 PDF」
:: 5) 观察模型是否调 read_skill(id=pdf-report)，拿到正文后再执行
```

### 10.3 浏览器 mock 模式验证

纯 `vite dev` 下，`api/skill.js` 返回 mock 数据，可快速验证 `SkillSettings.vue` 的列表 / 表单 / 弹窗 / 空态，无需启动桌面端。

### 10.4 与 Claude Code 行为对齐的回归检查

| 检查 | 预期 |
| --- | --- |
| 常驻上下文大小 | 随技能数量增长接近线性，但与正文长度**无关** |
| 未命中技能 | 其正文**不**进入上下文 |
| 命中技能 | 正文经 `read_skill` 一次性读入，可引用脚本 |
| 脚本执行 | 经 `exec_shell` 跑通（L3） |

---

## 十一、附录

### 11.1 改动文件清单

| 层 | 文件 | 动作 |
| --- | --- | --- |
| Go | `skills.go` | **新增**：SkillMeta/Detail + SkillStore + frontmatter + read_skill |
| Go | `skills_test.go` | **新增**：解析 / 校验 / 生成单测 |
| Go | `tools.go` | `ToolManager` 增 `skills` 字段；`BuildView` 注册 `read_skill` |
| Go | `chat.go` | `buildLLMMessages` 增参数；`executeChat` 注入 L1 / 强制注入 |
| Go | `sessions.go` | `Session` / `SessionConfig` 增 `EnabledSkills`；`CreateSession` 透传 |
| Go | `app.go` | 新增 `skillStore`、`startup` 初始化、8 个 bound 方法、`enabledSkillsForSession` |
| 自动 | `frontend/wailsjs/go/main/App.js`、`App.d.ts` | 重新生成绑定 |
| JS | `frontend/src/api/skill.js` | **新增** |
| JS | `frontend/src/stores/skills.js` | **新增** |
| Vue | `frontend/src/components/business/SkillSettings.vue` | **新增** |
| Vue | `frontend/src/components/business/SkillEditDialog.vue` | **新增** |
| Vue | `frontend/src/views/Settings.vue` | 新增 `skill` tab |
| Vue | `frontend/src/components/business/NewSessionDialog.vue` | 新增「可用技能」多选 |
| JSDoc | `frontend/src/types/index.js` | 增补 `SkillMeta` / `SkillDetail` 注释 |
| 数据 | `~/.local-agent/skills/`、`skills_state.json` | 运行时生成（非代码文件） |

### 11.2 关键命令速查

```bash
# 生成 Wails 绑定
wails generate module

# 开发运行
wails dev

# 前端单独开发（mock 模式）
cd frontend && npm run dev

# 后端单测
go test ./... -run Skill
```

### 11.3 一个带脚本的示例技能（L3 验证用）

目录：`~/.local-agent/skills/report/`

`SKILL.md`：

```markdown
---
name: 表格统计
description: 对 CSV 文件做行列统计。当用户需要统计表格数据时使用。
---

# 表格统计

## 步骤
1. 确认用户提供的 CSV 路径
2. 调用 exec_shell 执行：python scripts/count.py <csv路径>
3. 将脚本输出汇总给用户
```

`scripts/count.py`：输出行数与列数（略）。

### 11.4 术语

| 术语 | 含义 |
| --- | --- |
| Skill（技能） | 一段可复用的操作知识 + 随附资源，以文件夹形式组织 |
| L1 / L2 / L3 | 渐进式披露的三个层级：元数据 / 正文 / 资源 |
| frontmatter | SKILL.md 顶部 `---` 包裹的元数据区 |
| `read_skill` | 内置工具，供模型按需加载技能正文（L2） |
| `alwaysInject` | 标记后正文直接注入 system prompt（方案 B） |
| 会话白名单 | `Session.EnabledSkills`，限定本会话可用技能 |

---

（完）