# local-agent 项目分析报告

> 项目路径：E:\learn\local-agent
> 技术栈：Go 1.25 + Wails v2.15 + Vue（前端模板）
> 报告类型：源代码结构与功能分析

## 一、项目概述

local-agent 是一个基于 Wails 框架构建的本地 AI Agent 桌面应用。后端使用 Go 编写，负责模型配置管理、会话持久化、LLM 调用与工具调用循环；前端使用 Wails 官方 Vue 模板构建交互界面。项目在会话与工具设计上明显借鉴了 "01agent" 的架构。

## 二、目录与文件结构

| 路径 | 说明 |
| --- | --- |
| main.go | 程序入口，创建 App 并以 1024x768 窗口启动 Wails |
| app.go | 应用结构体与前端桥接层（模型/会话/对话方法） |
| models.go | 模型配置的数据结构与 JSON 持久化存储 |
| sessions.go | 会话数据模型与按文件持久化的会话存储 |
| chat.go | LLM 请求/响应结构与多轮工具调用对话流程 |
| tools.go | 工具接口、CLI 工具、元工具路由器与工具管理器 |
| models_test.go / sessions_test.go | 单元测试 |
| frontend/ | Vue 前端工程（Wails 模板） |
| docs/ | 设计研究文档（claude-code-ui-research.md、frontend-design.md） |
| wails.json | Wails 构建配置 |

## 三、核心模块

### 1. app.go — 应用入口与桥接层
- App 结构体持有 ctx、modelStore、sessionStore、toolManager。
- startup 初始化用户目录下的 ~/.local-agent/，创建 models.json 与 sessions 目录。
- 向前端暴露的方法：模型管理（GetModels/AddModel/DeleteModel 等）、会话管理（CreateSession/ListSessions/AppendMessage 等）、对话能力（Chat）。

### 2. models.go — 模型配置存储
- Model 字段：Name、Alias、ModelID、APIKey、URL。
- ModelStore：基于 JSON 文件持久化，使用 sync.RWMutex 保证并发安全，增删查时带失败回滚。

### 3. sessions.go — 会话存储
- 数据结构：Session、Message、ToolCall、Conversation。
- SessionStore：每个会话一个 JSON 文件；支持追加消息、更新元数据、按最近活跃倒序列表。
- 首条用户消息自动作为会话标题（截断 30 字）。
- ID 生成规则：时间戳_随机 hex。

### 4. chat.go — LLM 对话与工具循环
- 定义 OpenAI 兼容的请求/响应结构。
- callLLM：非流式 HTTP 调用，超时 120s。
- executeChat：多轮工具调用状态机循环（MaxChatTurns = 50）。
- 通过 Wails 事件 chat:event 推送 tool_call_start / tool_call_end / done。
- 持久化所有中间消息（assistant+tool_calls、tool 结果、最终回复），保证后续上下文完整。

### 5. tools.go — 工具系统
- ToolInterface / BaseTool：统一的工具接口与参数定义。
- CLITool（exec_shell）：执行本机终端命令，Windows 用 powershell，其他系统用 bash。
- MetaTool（tool_router）：元工具，支持 list（发现工具）与 execute（执行工具）。
- ToolManager：注册内置工具，仅向 LLM 暴露元工具，以节省 token。

## 四、数据存储设计

| 路径 | 内容 |
| --- | --- |
| ~/.local-agent/models.json | 模型配置列表 |
| ~/.local-agent/sessions/<id>.json | 每个会话的完整数据（含消息历史） |

## 五、对话执行流程

用户消息 -> 构建 LLM messages -> 调用 LLM -> 判断 finish_reason
- tool_calls：执行工具，持久化 assistant(tool_calls) 与 tool 结果消息 -> 继续循环
- stop：持久化最终回复 -> 返回并推送 done 事件

## 六、前端可调用的主要方法

- 模型：GetModels / GetModelNames / GetModel / AddModel / DeleteModel
- 会话：CreateSession / ListSessions / GetSession / DeleteSession / AppendMessage / AppendConversation / UpdateSession
- 对话：Chat（监听 chat:event 接收工具调用中间状态）

## 七、观察与建议

1. 安全：exec_shell 可执行任意本机命令，建议增加命令白名单或执行前确认（PermissionMode 字段已定义但尚未落地）。
2. 密钥：APIKey 目前明文存储在 models.json，建议加密或接入系统凭证库。
3. 体验：当前为非流式调用，可考虑改造为 SSE 流式输出以提升交互感。
4. 设计：仅暴露元工具有效节省 token，但工具发现会多一次往返调用。
5. 并发：存储层为进程内锁，多实例同时写同一会话仍可能冲突。
6. 文档：README 仍为 Wails 模板默认内容，建议补充项目说明。

---
报告结束
