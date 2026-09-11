# Claude Code 桌面端界面结构调研报告

> 调研日期：2026-09-10
> 调研目标：分析 Claude Code 桌面应用的前端页面结构与各页面功能，为本地 Agent 项目（local-agent）的前端页面设计提供参考。

---

## 一、整体架构概览

Claude Desktop 应用采用 **三标签页（Tab）** 的顶层结构，每个标签页对应不同的使用场景：

| 标签页 | 用途 | 说明 |
|--------|------|------|
| **Chat** | 通用对话 | 日常问答、内容创作等非编码类对话 |
| **Cowork** | 长时 Agent 任务 | 用于 Dispatch 调度和长时间运行的 Agent 工作 |
| **Code** | 软件开发 | 核心编码工作区，本次调研重点 |

其中 **Code 标签页** 是面向开发者的核心界面，采用 **「左侧会话侧边栏 + 中央多面板工作区」** 的布局模式。

---

## 二、Code 标签页详细结构

### 2.1 左侧：会话管理侧边栏（Session Sidebar）

侧边栏是 Code 标签页的导航核心，负责管理所有会话（Session）。

**核心功能：**

- **并行会话列表**：展示所有活跃和最近的会话，支持同时运行多个 Agent 任务
- **会话隔离**：每个会话拥有独立的聊天历史、项目文件夹和代码变更
- **筛选与分组**：
  - 按状态筛选（活跃、已完成、归档）
  - 按项目或环境筛选
  - 按项目分组显示
- **自动归档**：当会话对应的 PR 合并或关闭时，会话自动归档，保持侧边栏聚焦
- **快速切换**：点击即可在会话间切换，不中断其他会话的运行

**会话启动配置**（发送第一条消息前需配置四项）：

| 配置项 | 选项 | 说明 |
|--------|------|------|
| **Environment（环境）** | Local / Cloud / SSH / WSL | 选择 Claude 运行的位置 |
| **Project folder（项目文件夹）** | 本地目录或仓库 | Claude 工作的代码目录 |
| **Model（模型）** | 模型下拉选择 | 可在会话中随时切换 |
| **Permission mode（权限模式）** | 见 2.3 节 | 控制 Claude 的自主程度 |

---

### 2.2 中央：多面板工作区（Pane System）

Code 标签页的核心设计理念是 **可拖拽、可调整大小的面板系统**。每个面板（Pane）承担独立功能，用户可自由组合布局，布局按项目保存。

**面板类型（共 8 种）：**

#### ① Chat 面板（对话面板）
- **始终存在，不可关闭**
- 主对话界面，所有提示词、回复和工具调用摘要在此展示
- 底部为 **Prompt Box（输入框）**，支持：
  - `@` 提及文件：输入 `@` + 文件名，将文件加入上下文
  - 附件：拖入图片、PDF 等文件
  - `+` 按钮：访问技能（Skills）、连接器（Connectors）、插件（Plugins）
  - 中断按钮：立即停止 Claude 的当前操作
  - 纠错发送：可在 Claude 运行时输入纠正信息

#### ② Diff 面板（差异查看面板）
- **快捷键**：`Cmd+Shift+D`（macOS）/ `Ctrl+Shift+D`（Windows）
- 按「轮次（turn）」展示差异，而非仅显示最终累积状态
- 左侧文件列表 + 右侧逐文件变更详情
- 支持 **行内评论**：点击任意行添加评论，Claude 读取后进行修改
- **Review code 功能**：让 Claude 扫描当前 diff，留下内嵌注释（聚焦编译错误、逻辑错误、安全漏洞、明显 Bug，不涉及风格问题）
- 针对大型变更集进行了性能优化

#### ③ Browser/Preview 面板（浏览器/预览面板）
- **快捷键**：`Cmd+Shift+B` / `Ctrl+Shift+B`
- 支持 **实时渲染 HTML**（文件变更自动更新，无需刷新）
- 支持内联打开 PDF、图片、视频
- 支持 **本地开发服务器预览**：Claude 可自动启动 dev server 并在此验证
- Claude 可在预览中进行截图、DOM 检查、点击、填表等验证操作
- 支持 **外部网站浏览**（标签页式浏览器），可打开文档、Issue 追踪器等
- **Persist sessions**：跨重启保留 Cookie 和认证状态

#### ④ Terminal 面板（终端面板）
- **快捷键**：Ctrl + ` ` `
- 集成终端，运行在项目目录下
- 可在 Claude 会话运行的同时执行命令（运行测试、查看日志等）
- 与 Claude 共享相同的环境变量

#### ⑤ File Editor 面板（文件编辑面板）
- 点击对话或 diff 中的文件路径即可打开
- 轻量级代码编辑器，适合 **定点编辑（spot edits）**
- 保存立即写入磁盘
- 文件在磁盘上被外部修改时会发出警告
- 非完整 IDE，不适合大型结构重构

#### ⑥ Plan 面板（计划面板）
- 在 **Plan 模式** 下可见
- 以结构化列表展示 Claude 的当前计划
- 随 Claude 修订计划实时更新
- 只读视图，用于审核实现思路

#### ⑦ Tasks 面板（任务面板）
- 展示当前会话中的活跃和已完成任务列表
- 用于跟踪多步骤任务的进度

#### ⑧ Subagent 面板（子 Agent 面板）
- 展示正在运行的子 Agent 及其状态
- 显示每个子 Agent 正在执行的工具、是否等待输入、何时完成
- 便于监控并行 Agent 工作，无需轮询聊天记录

#### ⑨ iOS Simulator 面板（macOS 专属）
- 在 macOS 上可直接运行和测试 iOS 应用

---

### 2.3 权限模式（Permission Modes）

控制 Claude 在会话中的自主程度，可随时切换：

| 模式 | 行为 | 适用场景 |
|------|------|----------|
| **Manual（手动）** | 编辑文件或运行命令前均需确认，展示 diff 供接受/拒绝 | 需要逐一审阅变更 |
| **Accept edits（接受编辑）** | 自动接受文件编辑和常见文件系统命令（mkdir/touch/mv），但运行其他终端命令前仍询问 | 信任文件变更、追求快速迭代 |
| **Plan（计划）** | 只读探索并提出计划，不修改源代码 | 复杂任务先审核方案 |
| **Auto（自动）** | 后台安全检查验证对齐请求，减少权限提示同时保留监督 | 高效自主执行 |
| **Bypass permissions（绕过权限）** | 无权限提示运行（除少数强制审批的操作） | 仅在沙箱/虚拟机中使用 |

---

### 2.4 视图模式（View Modes）

调节界面的信息密度：

| 模式 | 展示内容 |
|------|----------|
| **Verbose（详细）** | 完整展示 Claude 的工具调用过程，全透明 |
| **Normal（正常）** | 平衡展示，默认模式 |
| **Summary（摘要）** | 仅展示结果，隐藏工具调用细节 |

---

### 2.5 侧边聊天（Side Chat）

- **快捷键**：`Cmd+;`（macOS）/ `Ctrl+;`（Windows）
- 从当前会话分支一个独立对话
- **继承主会话的上下文**，但 **不会回写** 到主会话历史
- 适合任务中途的快速提问（如"这个值是多少？"、"这个模式怎么用？"），避免污染主线程

---

### 2.6 PR 监控与 CI 集成

- 创建 PR 后，会话中出现 **CI 状态栏**
- **Auto-fix**：自动尝试修复失败的 CI 检查
- **Auto-merge**：所有检查通过后自动合并 PR（squash 方式）
- CI 完成时发送桌面通知
- 依赖 GitHub CLI（`gh`）

---

### 2.7 其他关键能力

| 能力 | 说明 |
|------|------|
| **Checkpoints（检查点）** | 每次变更前自动保存代码状态，可双按 Esc 或 `/rewind` 回滚 |
| **Subagents（子 Agent）** | 委派专门任务（如主 Agent 构建前端时子 Agent 搭建后端 API），支持并行开发 |
| **Hooks（钩子）** | 在特定节点自动触发动作（如代码变更后运行测试、提交前 lint） |
| **Background tasks（后台任务）** | 保持 dev server 等长时进程运行而不阻塞 Claude |
| **Connectors（连接器）** | 连接 GitHub、Slack、Linear 等外部工具 |
| **Computer use（电脑使用）** | Claude 可打开应用并控制屏幕 |

---

## 三、布局与交互特性

### 3.1 面板布局管理

- **拖拽重排**：拖拽面板标题栏移动位置
- **边缘拖拽**：拖拽面板边缘调整大小
- **关闭面板**：`Cmd+\` / `Ctrl+\` 关闭焦点面板
- **打开面板**：通过会话工具栏的 **Views 菜单**
- **布局持久化**：按项目保存布局，重新打开项目时恢复上次的面板排列

### 3.2 常用快捷键

| 快捷键 | 功能 |
|--------|------|
| `Cmd+Shift+D` / `Ctrl+Shift+D` | 打开 Diff 面板 |
| `Cmd+Shift+B` / `Ctrl+Shift+B` | 打开 Browser 面板 |
| `Ctrl+\`` | 打开 Terminal 面板 |
| `Cmd+\` / `Ctrl+\` | 关闭焦点面板 |
| `Cmd+;` / `Ctrl+;` | 打开侧边聊天 |
| `Cmd+/` / `Ctrl+/` | 查看全部快捷键 |

---

## 四、对 local-agent 项目的页面设计建议

基于 Claude Code 的界面结构，结合本项目（Go + Vue3 + Wails 桌面应用）的技术栈，建议的页面/组件结构如下：

### 推荐页面结构

```
┌─────────────────────────────────────────────────────┐
│  顶部导航栏：项目切换 / 模型选择 / 权限模式 / 设置    │
├──────────┬──────────────────────────────────────────┤
│          │                                          │
│  会话    │         主工作区（可切换/分栏）            │
│  侧边栏  │                                          │
│          │   ┌────────────────────────────────┐    │
│  - 会话  │   │  Chat 对话面板（核心）            │    │
│    列表  │   │  - 消息流                         │    │
│  - 新建  │   │  - 工具调用摘要                    │    │
│  - 筛选  │   └────────────────────────────────┘    │
│          │   ┌────────────────────────────────┐    │
│          │   │  Diff / Terminal / File 面板    │    │
│          │   │  （可切换 Tab 或分栏展示）        │    │
│          │   └────────────────────────────────┘    │
│          │                                          │
└──────────┴──────────────────────────────────────────┘
```

### 核心页面/组件清单

| 模块 | 对应 Claude Code 功能 | 优先级 |
|------|----------------------|--------|
| **会话管理侧边栏** | Session Sidebar | P0 |
| **对话面板（Chat）** | Chat Pane + Prompt Box | P0 |
| **差异查看（Diff）** | Diff Pane | P0 |
| **终端面板（Terminal）** | Terminal Pane | P1 |
| **文件编辑（File Editor）** | File Editor Pane | P1 |
| **权限模式选择** | Permission Modes | P1 |
| **模型选择** | Model Selector | P1 |
| **计划视图（Plan）** | Plan Pane | P2 |
| **任务列表（Tasks）** | Tasks Pane | P2 |
| **子 Agent 监控** | Subagent Pane | P2 |
| **预览面板（Preview）** | Browser/Preview Pane | P2 |
| **侧边聊天** | Side Chat | P3 |

---

## 五、参考资料

1. [Claude Code 官方桌面应用文档](https://code.claude.com/docs/en/desktop)
2. [Redesigning Claude Code on desktop for parallel agents](https://claude.com/blog/claude-code-desktop-redesign) - 2026年4月重构公告
3. [Claude Code Desktop App Guide - UitKit](https://github.com/UitbreidenOS/UitKit/blob/main/guides/desktop-app.md) - 面板系统详解
4. [Claude Code: GUI vs Terminal, Round 2](https://vanja.io/claude-code-gui-vs-terminal-round-2/) - 重构后功能对比
5. [Enabling Claude Code to work more autonomously](https://www.anthropic.com/news/enabling-claude-code-to-work-more-autonomously) - 子Agent、检查点、Hooks 介绍
