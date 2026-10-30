# IBU Campaign Copilot — 项目简介

> 携程 IBU Campaign 团队的 AI Agent 服务端平台，为运营同学提供基于自然语言的营销活动端到端搭建能力——"一句话搭建活动"。

---

## 一、项目概述

### 1.1 项目背景

携程 IBU（国际业务部）营销活动涉及航司、酒店、景区玩乐（TNT）等多条业务线，运营搭建一个营销活动需要经历"建壳 → 招商 → 选品 → 页面搭建 → 报备 → 广告位投放"等多个环节。传统流程中运营需要在多个后台系统间手动操作，流程长、效率低。

`campaign-ai` 平台通过 AI Agent 驱动，让运营用自然语言描述需求（如"帮我创建一个新加坡航空促销活动"），Agent 自动拆解任务、调用各业务系统接口、生成页面、选品、挂载券，完成端到端的活动搭建。

### 1.2 核心价值

| 价值 | 说明 |
|------|------|
| **自然语言驱动** | 运营无需学习各后台系统，一句话描述需求即可搭建 |
| **端到端自动化** | 覆盖建壳→招商→选品→页面→报备→广告位全流程 |
| **多 Agent 协作** | 主 Agent 调度子 Agent，各子 Agent 负责特定领域 |
| **人机交互确认** | 关键节点 interrupt 暂停，等用户确认后继续 |
| **流式输出** | AG-UI / SSE 实时推送进度，运营可见执行过程 |

---

## 二、技术栈

| 层次 | 技术选型 | 选型理由 |
|------|---------|---------|
| Web 框架 | FastAPI 0.116 + Uvicorn | 异步高性能，原生支持 SSE 流式 |
| Agent 引擎 | DeepAgent + LangGraph | DeepAgent 提供子 Agent 委派能力，LangGraph 支持图状态机和 interrupt/resume |
| LLM | Anthropic Claude（公司 AI 网关） | 通过 `langchain-anthropic` 兼容客户端调用，模型 ID 由 QConfig 动态配置 |
| Agent 编排 | LangChain / LangGraph | 协调者 `task()` 动态委派 CompiledSubAgent |
| 工具协议 | MCP（Model Context Protocol） | 标准化 Agent 与内网服务的工具调用，解耦 Agent 和工具实现 |
| 前端协议 | CopilotKit AG-UI 协议 | 前端 CopilotKit 组件库原生支持，SSE 流式输出 |
| 内网框架 | `py-frame-helper` | 携程内部统一框架，封装 QConfig/CRedis/QMQ/DAL/SOA |
| 可观测性 | OpenTelemetry + Prometheus + Loguru | 全链路 trace + 指标监控 + 结构化日志 |
| 认证 | 携程 SSO 2.0 中间件 | 统一身份认证，Cookie 透传到内网 MCP 服务 |
| 构建 | `uv` + `hatchling` | 快速依赖管理 + 打包 |
| Python 版本 | ≥ 3.11 | 异步语法、类型提示 |

### 技术栈选型理由（面试可说）

**为什么选 LangGraph 而不是 LangChain Agent？**
- LangGraph 支持图状态机，能精确控制 Agent 执行流程。
- 原生支持 interrupt/resume，用户确认场景必需（页面发布前需人工确认）。
- checkpointer 支持 Postgres/SQLite，会话可跨请求恢复。
- 子图（CompiledSubAgent）可独立编译、可被主图 `task()` 委派。

**为什么用 MCP 而不是直接写 Tool？**
- MCP 是标准化工具协议，工具一次开发多 Agent 复用。
- 内网服务封装为 MCP Server，Agent 通过 MCP 客户端调用，解耦 Agent 和工具实现。
- 工具白名单控制（`allowed_tools`），安全可控。

**为什么用 DeepAgent 而不是裸 LangGraph？**
- DeepAgent 封装了子 Agent 委派（`task()`）机制，不需要自己实现路由。
- Skills 文件系统（`campaign/skills/`）让 Agent 可读操作 SOP，不替代子 Agent 但提供指导。
- 与 LangGraph 无缝集成，compiled graph 仍由 LangGraph 管理。

---

## 三、整体架构

### 3.1 架构总览

```
前端 / CopilotKit
      │
      ▼ POST /copilotkit 或 POST /campaign/agent/chat/stream
┌─────────────────────────────────┐
│    SSOAuthMiddleware            │  解析 SSO Cookie，绑定请求级 ContextVar
└────────────────┬────────────────┘
                 │
                 ▼
┌─────────────────────────────────┐
│  agent/stream_handler           │  AG-UI / SSE 流式输出 / resume
└────────────────┬────────────────┘
                 │
                 ▼
┌─────────────────────────────────┐
│  agent/main（根 Main DeepAgent）│  唯一根图 + 根 checkpoint
│    Main LLM (Claude)           │  理解需求 → task() 委派子 Agent
│    update_activity_plan        │  增量维护活动流程状态
└────────────────┬────────────────┘
                 │ task(subagent_name, ...)
                 ▼
┌─────────────────────────────────┐
│  agent/subagents + runners      │  CompiledSubAgent → SOA / MCP
│  Skills (campaign/skills/)      │  操作 SOP 可读；工具经 MCP / scripts
└─────────────────────────────────┘
```

### 3.2 编排范式：混合编排

项目采用**混合编排**，而非主图硬编码固定流程：

| 层 | 职责 |
|---|---|
| 根图 `agent/main/` | compiled Main DeepAgent，唯一根图与根 checkpoint |
| Main 模型 (Claude) | 判定调用哪个 `task(subagent_name, …)`，维护 `activity_plan` |
| CompiledSubAgent | 子图（sourcing / selection / page / image / coupon / …），可 `interrupt` |
| runners | 真正调 SOA / MCP、写 CampaignState |
| Skills | Filesystem SOP，指导怎么操作；**不替代** SubAgent |

**核心设计**：业务步骤顺序由 `CampaignState.activity_plan` 表达，Main 模型根据用户意图动态决定调用哪个子 Agent，不再用主图 `_route_*` 硬编码"建壳→招商→选品→…"。

### 3.3 活动流程状态机

`activity_plan` 是跨子 Agent 共享的权威状态，新建活动固定初始化五个顶层节点：

```
shell → sourcing → page → application-notify → ads
 建壳    招商     页面     报备              广告位
```

- 每个节点有 `status`（pending / in_progress / done / skip）。
- Main 模型通过 `update_activity_plan` 增量更新状态。
- 暂不执行的节点设为 `skip`，可恢复为 pending。

---

## 四、功能模块划分

### 4.1 SubAgent 子智能体（9 个）

| SubAgent | 目录 | 职责 |
|---------|------|------|
| **sourcing** | `subagents/sourcing/` | 招商规则管理（酒店/TNT/机票） |
| **selection** | `subagents/selection/` | 选品池管理（酒店/机票/TNT） |
| **page** | `subagents/page/` | Foxpage 页面搭建（骨架保存/模块落页/QA/发布） |
| **image** | `subagents/image/` | 营销图片生成（Spark/PETA） |
| **coupon** | `subagents/coupon/` | 优惠券创建与绑定（TCP 酒店/机票） |
| **strategy** | `subagents/strategy/` | 活动页面设计规划（布局/配色/Banner Prompt） |
| **visual_agent** | `subagents/visual_agent/` | 脚本驱动落地页构建（JSON Patch 批量写 Foxpage） |
| **application_notify** | `subagents/application_notify/` | 活动报备通知 |
| **ads** | `subagents/ads/` | 广告位投放 |

### 4.2 Skills 操作手册（21 个）

Skills 是 DeepAgent 可读写的**操作 SOP**，每个 Skill 是 `campaign/skills/` 下的独立目录：

| 分类 | Skills |
|------|--------|
| 活动搭建 | build-full-campaign, campaign-orchestrator, campaign-creator, zoo-campaign-orchestrator |
| 页面设计 | campaign-design, design-agent, visual-agent, build-liveshow-page |
| 选品 | flight-selection, hotel-selection, tnt-selection |
| 招商 | sourcing |
| 图片 | spark-banner-generation, spark-banner-qc, lm-generate-image |
| 优惠券 | tcp-coupon, coupon-param-infer |
| 其他 | basic-info, show-event-poi-bind, application-notification, flash-sale |

每个 Skill 目录结构：
```
campaign/skills/<skill-name>/
├── SKILL.md          # 核心系统提示词，描述任务流程和工具用法
├── references/       # 参考文档、知识库片段
├── workflows/        # 子工作流定义
├── scripts/          # 辅助 Python/JS/Shell 脚本
└── templates/        # 模板文件
```

### 4.3 MCP 服务（内网工具）

| MCP 服务器 | 用途 |
|-----------|------|
| `campaign-selection` | 选品池管理（酒店/机票/TNT） |
| `foxpage-page` | Foxpage 页面 CRUD |
| `campaign-foxpage-create` | Foxpage 文件注册 |
| `campaign-component-kb` | 活动组件知识库 |
| `foxpage-kb` | Foxpage DSL 知识库 |
| `image-generate` | Spark 图片生成 |
| `campaign-show-event-poi-bind` | 展演 POI 绑定 |

认证方式：Bearer Token（KMS 获取）+ SSO Cookie 透传。

### 4.4 核心模块

| 模块 | 路径 | 职责 |
|------|------|------|
| **App 入口** | `campaign/app.py` | FastAPI 应用，lifespan 编译图 |
| **API 路由** | `campaign/api/` | HTTP 路由（chat/stream、CopilotKit） |
| **Agent 运行时** | `campaign/agent/` | Main + SubAgents + runners + stream_handler |
| **配置中心** | `campaign/config/` | app_config / KMS / QConfig / MCP 动态拼装 |
| **MCP 网关** | `campaign/config/mcp_gateway.py` | QConfig 读取 MCP URL/Token，SSO 透传 |
| **SSO 中间件** | `campaign/middleware/` | SSO 2.0 认证 + ContextVar 绑定 |
| **SOA 调用** | `campaign/soa/` | SOA 泛化调用（Campaign Manager 等） |
| **数据访问** | `campaign/repository/` | DAL（MySQL）/ CRedis / QMQ |
| **数据模型** | `campaign/schemas/` | CampaignState / ActivityPlan / TaskRequest/Snapshot |
| **会话管理** | `campaign/services/` | 会话 CRUD / 对话记忆 |
| **可观测性** | `campaign/observability/` | 日志上下文 / Metrics / Tracing |
| **知识库** | `campaign/knowledge_base/` | 按 CampaignType 组织的活动知识 Markdown |

---

## 五、设计思路

### 5.1 核心设计理念：薄主图 + DeepAgent 协调者

- **薄主图**：根 Main Agent 不硬编码业务流程，只做理解需求 + `task()` 委派 + 状态维护。
- **DeepAgent 协调者**：Main 模型根据用户意图动态决定调用哪个子 Agent，不按固定轨道路由。
- **CompiledSubAgent**：每个子 Agent 是独立的 compiled graph，有自己的 prompt/state/tools，可 `interrupt` 等用户确认。

### 5.2 Skills 与 SubAgent 的关系

| 概念 | 作用 | 类比 |
|------|------|------|
| Skills | 操作 SOP（Markdown），指导 Agent 怎么做 | 操作手册 |
| SubAgent | 执行引擎，真正调工具、写状态 | 执行者 |
| MCP | 工具协议，Agent 通过 MCP 调内网服务 | API 接口 |

**Skills 不替代 SubAgent**——Skills 提供操作指导，SubAgent 负责执行。业务步骤是否执行由协调者 `task()` 决定。

### 5.3 状态管理：CampaignState + ActivityPlan

```
CampaignState（跨子 Agent 共享的权威状态）
  ├── activity_plan        ← 活动流程状态机（shell→sourcing→page→...→ads）
  ├── artifacts            ← 执行产物（page_files 等）
  ├── campaign_id          ← 活动 ID
  ├── campaign_type        ← 活动类型（航司/酒店/景区...）
  └── team                 ← 团队（影响差异规则）
```

- `activity_plan` 是权威状态，恢复会话时以它为准。
- Main 模型通过 `update_activity_plan` 增量原子更新。
- 子 Agent 通过 `TaskSnapshot` 返回结果，不直接写 `activity_plan`。

### 5.4 人机交互：interrupt / resume

```
用户："帮我创建航司活动"
  → Main 理解需求 → task("campaign-creator") 建壳
  → 子 Agent interrupt："确认活动类型：航司方向2？"
  → 前端展示确认卡片
  → 用户点击"确认"
  → 同一 thread_id resume → 继续执行
```

- 关键节点（建壳、页面发布）用 LangGraph `interrupt` 暂停。
- 同一 `thread_id` 跨请求恢复，checkpointer 支持 Postgres 持久化。
- AG-UI 通过 `CUSTOM on_interrupt` 交给前端展示确认 UI。

### 5.5 MCP 动态配置

- MCP 服务器 URL / Token / Knowledge ID 通过 **QConfig（携程配置中心）** 下发。
- `mcp_gateway.py` 运行时动态拼装 MCP 连接信息。
- SSO Cookie 从请求上下文提取，透传到需要鉴权的内网 MCP 服务。
- Campaign Manager 建壳/查询主路径优先走 **SOA**（`campaign/soa`），而非 MCP。

### 5.6 可观测性

| 维度 | 实现 |
|------|------|
| **Trace** | OpenTelemetry，每次 `/chat/stream` 生成一条 Trace，涵盖 Skill 选择、MCP 工具调用、LLM 推理耗时 |
| **Metrics** | Prometheus：input/output tokens、latency、tool_calls（按 Skill/Tool 分组） |
| **日志** | Loguru + ScenarioMarkerBuilder，结构化 JSON，含 conversation_id / eid / skill_name / tool_name |
| **Langfuse** | LLM 调用追踪和评估 |

---

## 六、面试介绍模板

### 一句话概述

> "Campaign Copilot 是携程 IBU 营销团队的 AI Agent 服务端平台，让运营用自然语言一句话搭建营销活动，覆盖建壳→招商→选品→页面→报备→广告位全流程。"

### STAR 结构

**Situation（背景）：**
> 携程 IBU 营销活动涉及航司、酒店、景区等多条业务线，运营搭建活动需要在多个后台手动操作，流程长效率低。

**Task（任务）：**
> 构建一个 AI Agent 平台，让运营用自然语言描述需求，Agent 自动拆解任务、调用各业务系统接口，端到端完成活动搭建。

**Action（行动）：**
> 技术选型上用 FastAPI + LangGraph + DeepAgent + Claude，采用"薄主图 + DeepAgent 协调者"的混合编排架构——根 Main Agent 不硬编码流程，通过 `task()` 动态委派 9 个子 Agent（招商/选品/页面/图片/券等），每个子 Agent 是独立的 compiled graph。子 Agent 通过 MCP 协议调用内网服务，关键节点用 LangGraph interrupt 暂停等用户确认。状态管理用 CampaignState + ActivityPlan 作为跨子 Agent 的权威状态。Skills 系统（21 个 SKILL.md）提供操作 SOP 指导子 Agent 执行。可观测性用 OpenTelemetry 全链路 trace + Prometheus 指标。

**Result（结果）：**
> 实现了"一句话搭建活动"，运营不再需要手动在多个后台操作。覆盖建壳→招商→选品→页面→报备→广告位完整流程闭环，支持航司/酒店/TNT 多业务线。

### 面试官可能追问

**Q: 为什么用 LangGraph 而不是 LangChain Agent？**
> LangGraph 支持图状态机精确控制流程，原生支持 interrupt/resume（页面发布前需用户确认），checkpointer 支持跨请求恢复会话。子图可独立编译、被主图 `task()` 委派。

**Q: 主 Agent 怎么决定调哪个子 Agent？**
> 不硬编码路由。Main 模型（Claude）根据用户意图和 CampaignState 中的 activity_plan，自主判断调用哪个 `task(subagent_name)`。activity_plan 维护了五个顶层节点（shell→sourcing→page→...→ads）的状态，Main 模型根据状态和用户输入决定下一步。

**Q: MCP 是什么？为什么用它？**
> MCP（Model Context Protocol）是标准化的工具协议。内网服务封装为 MCP Server，Agent 通过 MCP 客户端调用。好处是工具一次开发多 Agent 复用，工具白名单控制安全，Agent 和工具实现解耦。

**Q: Skills 和 SubAgent 什么关系？**
> Skills 是操作 SOP（Markdown），指导 Agent 怎么做，类比操作手册。SubAgent 是执行引擎，真正调工具、写状态。Skills 不替代 SubAgent——业务步骤由协调者 `task()` 决定，Skills 只提供操作指导。

**Q: 用户确认怎么做？**
> 关键节点用 LangGraph `interrupt` 暂停执行，AG-UI 通过 `CUSTOM on_interrupt` 交给前端展示确认卡片。用户确认后，同一 `thread_id` resume 继续执行，checkpointer 保证会话可跨请求恢复。

**Q: 状态怎么管理？**
> CampaignState 是跨子 Agent 共享的权威状态，核心是 activity_plan 活动流程状态机。Main 模型通过 `update_activity_plan` 增量原子更新状态。子 Agent 通过 TaskSnapshot 返回结果，不直接写 activity_plan，只有 Main 模型可以更新。

---

## 七、速记

| 主题 | 速记 |
|------|------|
| 一句话 | 自然语言一句话搭建营销活动，覆盖建壳→招商→选品→页面→报备→广告位 |
| 架构 | 薄主图 + DeepAgent 协调者 + 9 子 Agent + 21 Skills + 7 MCP 服务 |
| 技术栈 | FastAPI + LangGraph + DeepAgent + Claude + MCP + CopilotKit AG-UI |
| 编排 | 混合编排，不硬编码路由，Main 模型 task() 动态委派 |
| 状态 | CampaignState + ActivityPlan 五节点状态机，Main 模型增量更新 |
| 交互 | interrupt/resume + 同一 thread_id 跨请求恢复 |
| 工具 | MCP 标准化协议 + SOA 泛化调用 + Skills 操作 SOP |
| 可观测 | OpenTelemetry trace + Prometheus metrics + Langfuse LLM 评估 |
