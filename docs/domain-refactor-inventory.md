# local-agent 后端「按域分层」整理 —— 平铺文件盘点（步骤 2 产出）

分支：`refactor/domain-layout`　基线：`bbc9964`（阶段2 基线）/ `467224d`（阶段1）
模块：`wails-tmp`　扫描范围：仓库根层级 + 一级目录（排除 `.git`、`build/` 构建产物、`frontend/` 前端）

## 结论速览

- 上一轮的分域重构**边界是干净的**：根目录与 `internal/*` 下所有同名文件（`imagefetch.go`、
  `requestlog.go`、`tokencalib.go`、`memorytools.go`、`tools.go`、`undo.go` 等）**没有一对逐字节相同**，
  根包是"App 层薄壳"，`internal/*` 才是引擎，不是搬运残留的重复副本。
- 根目录的 `*_test.go` 每份文件头都写了"纯引擎用例已迁到 `internal/x`，这里只留需要根包装配的用例"，
  属于有意保留的 App 层集成测试。
- 因此**该迁的 Go 引擎文件基本迁完了**；剩余待处理的是下面 E 节的少数项（顶层 `memory/` 包、
  游离文档、调试残留、App 层超大文件的进一步下沉）。

## A. 根目录 Go 源码（23 个，`package main`，均为 App 层/装配/绑定）

| 文件 | 大小 | 角色 | 目标 |
|---|---|---|---|
| main.go | 953B | 进程入口 + wails.Run/Bind | 留根 |
| app.go | 41KB / 1045 行 | `App` 结构体 + 67 个绑定方法 + startup | 留根（可再拆） |
| chat.go | 57KB / 1307 行 | 对话编排（App 侧） | 留根（超大，待评估下沉） |
| agentrun.go | 11KB / 248 行 | `agentRun` 依赖封装 | 留根 |
| tools.go | 12KB / 308 行 | `ToolManager` 装配 | 留根（待评估） |
| usage.go | 3.9KB | `UsageDetail` + 绑定 | 留根 |
| visiontest.go | 10KB / 247 行 | `/vision` 看图自检（App 侧） | 留根（待评估下沉） |
| mcpimport.go | 4.4KB | 2 个绑定（引擎已在 internal/tool） | 留根 |
| memoryapp.go | 4.4KB | 记忆命令（App 侧） | 留根 |
| memoryinject.go | 2.2KB | 记忆提示注入（App 侧） | 留根 |
| memorytools.go | 1.1KB | 3 个工具名常量（别名 internal/tool） | 留根 |
| imagefetch.go | 1.2KB | 1 个绑定（引擎已在 internal/media） | 留根 |
| requestlog.go | 2.8KB | 快照装配 + 绑定 | 留根 |
| tokencalib.go | 861B | `calibFor` 适配器 | 留根 |
| undo.go | 1.9KB | 2 个绑定 | 留根 |
| subagent.go | 10KB / 229 行 | `runSubagent`（App 侧） | 留根 |
| subagent_app.go | 5.3KB | 绑定 | 留根 |
| taskapp.go | 5.7KB | 18 个插件绑定 | 留根 |
| taskbootstrap.go | 1.8KB | 插件装配入口 | 留根 |
| taskhost.go | 10KB / 281 行 | `plugin.Host` 唯一实现（桥接层） | 留根（设计如此） |
| ask_app.go | 2.1KB | 绑定 | 留根 |
| contextmgmt_app.go | 2.1KB | 绑定 | 留根 |
| permission_app.go | 10KB | 13 个绑定 | 留根 |

## B. 根目录 Go 测试（22 个，`package main`，App 层集成测试）

ask_test.go(3) · chat_stream_test.go(5) · contextmgmt_test.go(16) · contextstat_test.go(9) ·
filetools_test.go(4) · imageinput_test.go(6) · mcpimport_test.go(9) · memory_test.go(6) ·
permission_test.go(22) · plan_test.go(14) · procexec_test.go(2) · requestlog_test.go(4) ·
runcontrol_test.go(12) · sessions_model_test.go(3) · skills_test.go(2) · subagent_test.go(18) ·
taskapp_e2e_test.go(2) · taskapp_test.go(4) · tokencalib_test.go(4) · tool_runtime_test.go(10) ·
undo_test.go(8)
（括号内为 `func Test*` 数量；合计约 163 个用例）
→ 均**留根**：文件头已声明"纯引擎用例已迁 internal/*，这里只留需根包装配的用例"。

## C. 根目录非 Go 文件

| 文件 | 归类 | 处理 |
|---|---|---|
| go.mod / go.sum | 构建配置 | 留根 |
| wails.json | Wails 配置 | 留根 |
| README.md | 文档 | 留根 |
| .gitignore | 配置 | 留根 |
| run-app.ps1 | 脚本 | 留根 |
| claude-code-memory-analysis.md | **文档（游离）** | → 建议移入 `docs/` |
| _probe.py / _probe.log / lt.log | **调试残留（未跟踪）** | 用户已确认：不提交，保留待清理 |

## D. 一级目录

| 目录 | 内容 | 处理 |
|---|---|---|
| internal/ | 13 个域：agent diff git llm media memory permission procx skill snapshot store task tool | 已是目标结构 |
| plugin/ | 桌面插件（Go 包，21 文件） | 顶层保留（设计上解耦） |
| memory/ | **顶层 Go 包 `wails-tmp/memory`：记忆库本体，1102 行** | 见 E-1 |
| cad/ | SolidWorks MCP（Python 资源/脚本） | 非 Go 后端；保留或归入 tools/ |
| docs/ | 设计文档 | 保留 |
| build/ | Wails 构建产物/图标/nsi | 排除 |
| frontend/ | 前端（含生成物 wailsjs/） | 排除 |
| .local-agent/ | 运行期数据（permissions.local.json） | 保留 |

## E. 待迁移 / 待处理清单（后续步骤的映射依据）

1. **顶层 `memory/` 包归位**：`memory/memorystore.go`（1102 行）+ `memorystore_test.go` 是记忆库本体，
   却未进 `internal/`，且与 `internal/memory/`（工具层）**同名**，靠 `memstore` 别名共存。
   候选：`internal/memory`（引擎）+ `internal/memory/tools`（工具），或 `internal/memstore` +
   保留 `internal/memory`（工具）。**需先定命名方案再动**（引用点：app.go / tools.go / memoryapp.go /
   memoryinject.go / memory_test.go）。
2. `claude-code-memory-analysis.md` → `docs/`。
3. 根包超大文件（app.go 1045 行、chat.go 1307 行）是否进一步按域下沉 App 层逻辑 —— 属"域划分"步骤判定。
4. `_probe.py` / `_probe.log` / `lt.log`：按用户决定暂留工作区，收尾时清理。

---

# F. 目标结构与文件映射（步骤 3 定稿）

## F.0 划分原则

- **域 = 引擎（`internal/<domain>/`，不认识 App/wails runtime）**；**App 层 = `package main`（Wails 绑定 + 装配 + 宿主适配）**。
  `internal/*` 对 wails runtime 的 import 数应保持为 0（既有不变式）。
- 不在 `internal/<domain>/` 下再套 `controllers/services/models` 一类子层：本仓是 Go 后端 + Wails 绑定，
  域内以**文件**切分职责即可，避免过度分层。
- 非 Go 资源分置于 `docs/`（文档）、`cad/`（Python 资源）、排除 `frontend/` `build/` `.local-agent/`。

## F.1 目标目录树

```
local-agent/
├── *.go                     package main — App 层（23 源码 + 22 测试），不迁
├── internal/
│   ├── agent/ diff/ git/ llm/ media/ memory/ permission/
│   │   procx/ skill/ snapshot/ store/ task/ tool/      # 已就位（13 域）
│   └── memstore/            # 新建：记忆库本体（原顶层 memory/）
├── plugin/                  顶层保留（插件域，设计上解耦）
├── docs/                    文档（+ 迁入 claude-code-memory-analysis.md + 本文件）
├── cad/ frontend/ build/ .local-agent/  非 Go / 排除 / 运行期
```

## F.2 逐文件映射

| 源 | 目标 | 动作 | 备注 |
|---|---|---|---|
| `memory/memorystore.go` | `internal/memstore/memorystore.go` | 移动 + `package memory`→`package memstore` | 记忆库本体 1102 行 |
| `memory/memorystore_test.go` | `internal/memstore/memorystore_test.go` | 移动 + 改包名 | |
| `(import) app.go / memoryapp.go / memoryinject.go / memory_test.go` | —— | 改 import 路径 + `memory.X`→`memstore.X` | 4 处 |
| `(import) tools.go` | —— | import 路径改为 `internal/memstore`；`memstore` 别名转正（可省别名） | 1 处 |
| `claude-code-memory-analysis.md` | `docs/claude-code-memory-analysis.md` | 移动 | |
| `app.go`(1045行) | 评估后按域下沉编排逻辑 → `internal/*`；绑定层留根 | 评估 + 择机拆分 | 见 F.3 |
| `chat.go`(1307行) | 同上 | 评估 + 择机拆分 | 见 F.3 |
| `_probe.py` / `_probe.log` / `lt.log` | 删除 | 收尾清理 | 未跟踪 |
| 其余 21 个根源码 + 22 个根测试 | 不动（App 层） | 保留 | 见 A/B 节 |

## F.3 app.go / chat.go 拆分（用户已同意「顺带评估并拆分」）

- 先**评估**：App 方法与 `internal/*` 的职责边界已由上一轮划定，根包应只剩「装配 + 绑定 + wails 适配」。
  逐方法判定：纯编排/无 App 依赖 → 候选下沉；触碰 `App` 字段（sessionStore/diffService/toolManager…）→ 留根。
- 拆分目标：把可下沉的**纯逻辑**移入对应 `internal/<domain>`，根侧仅保留薄包装（与 `task`/`memory` 已有通解一致）；
  绑定方法本身（`func (a *App)`）必须留根，否则 Wails 绑定失效。
- 风险：改动面大、易破坏绑定生成（`frontend/wailsjs`）；须 `go build` + `go test ./...` 每步验证。
- **待确认项（不在本步解决）**：若评估发现 app.go/chat.go 大部分方法都必须摸 App 字段（即本就是 App 层），
  则结果为「无需拆分，仅内部按职责分行整理」——届时回报用户再定。

## F.4 无法归类 / 待确认

- 无。全部平铺文件均已给出目标或明确「留根」；`cad/`（Python）、`plugin/`（顶层 Go 包）判为有意保留。
