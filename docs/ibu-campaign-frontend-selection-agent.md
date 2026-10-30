# 前后端交互机制与选品子 Agent 实现详解

> 基于 `ibu_campaign_copilot` 项目源码整理，覆盖 AG-UI 协议交互、SSE 流式通信、interrupt/resume 确认机制，以及选品子 Agent 的完整状态机实现。

---

## 一、前后端交互机制

### 1.1 交互协议：AG-UI + SSE

前端使用 **CopilotKit** 组件库，后端通过 **AG-UI 协议** 以 **SSE（Server-Sent Events）** 流式输出。

```
前端 CopilotKit
      │
      │ POST /copilotkit  或  POST /campaign/agent/chat/stream
      │ Content-Type: application/json
      ▼
┌─────────────────────────────────┐
│  请求体（AG-UI 格式）            │
│  {                              │
│    "method": "agent/run",       │
│    "params": {                  │
│      "agentId": "campaign_chat" │
│    },                           │
│    "body": {                    │
│      "threadId": "test-001",    │ ← 会话标识，跨请求保持
│      "messages": [              │ ← 对话历史
│        {"role":"user","content":"帮我创建航司活动"}
│      ]                          │
│    }                            │
│  }                              │
└─────────────────────────────────┘
```

### 1.2 SSE 响应格式

响应为 `text/event-stream`，每个 `data:` 行是一个 JSON 对象：

```
data: {"type":"run_started","threadId":"test-001","runId":"abc123"}

data: {"type":"text_delta","content":"好的，"}

data: {"type":"text_delta","content":"我来帮你创建航司活动"}

data: {"type":"tool_call","tool":"create_campaign","input":{...}}

data: {"type":"custom","event":"on_interrupt","payload":{...}}

data: {"type":"done","runId":"abc123"}
```

**关键事件类型：**

| 事件类型 | 说明 |
|---------|------|
| `run_started` | Agent 开始执行 |
| `text_delta` | LLM 输出的文本片段（聊天气泡增量渲染） |
| `tool_call` | Agent 调用工具（MCP/SOA） |
| `custom: on_interrupt` | 中断确认请求（弹出确认卡片） |
| `done` | 本轮执行结束 |

### 1.3 请求处理链路

```python
# 入口：campaign/api/copilotkit_routes.py 和 chat_stream_routes.py
# 共用 handler：campaign/agent/stream_handler.py

POST /copilotkit
  → SSOAuthMiddleware          # SSO 认证，绑定 ContextVar
  → DeepAgentStreamHandler    # 核心处理类
      → unwrap_agui_body()    # 解析 AG-UI 请求体
      → build_graph_input()   # 构建 LangGraph 输入（首次/恢复）
      → 判断请求类型：
         - 首次请求：graph_input 是 dict（含 messages + user_brief）
         - 恢复请求：graph_input 是 Command(resume=...)（用户确认后恢复）
         - 连接重放：graph_input 是 None + method=agent/connect（前端重连后重放历史）
      → event_stream()        # SSE 流式输出
         - start_run()        # 交给 RunSupervisor（HTTP 断开不 cancel worker）
         - stream_build_full_campaign()  # 直接执行主图
```

### 1.4 三种请求模式

| 模式 | 触发条件 | graph_input 类型 | 处理 |
|------|---------|-----------------|------|
| **首次请求** | 用户发新消息 | `dict`（含 messages） | 主图从头执行 |
| **恢复请求** | 用户在 interrupt 弹窗确认 | `Command(resume=...)` | 主图从 interrupt 点继续 |
| **连接重放** | 前端断线重连 | `None` + method=`agent/connect` | 重放历史 SSE 帧 |

### 1.5 interrupt/resume 确认机制（核心）

这是前后端交互最关键的机制——Agent 在关键节点暂停，等用户确认后继续。

**后端发起 interrupt：**
```python
# 子 Agent 中调用 interrupt()
resume = interrupt({
    "type": "selection_draft_preview",       # interrupt 类型
    "payload": {
        "markdown": "草稿预览内容...",
        "drafted_pools": [{"name": "酒店池", "product_line": "hotel"}],
        "failed_pools": [{"pool_name": "机票池", "errors": ["缺少城市"]}],
    },
    "_subagent": {"graph_id": "subagent_selection", "phase": "preview"},
})
# → AG-UI 通过 SSE 推送 custom: on_interrupt 事件到前端
# → 前端展示确认卡片
# → Agent 在此处暂停，等待用户回复
```

**前端确认后恢复：**
```json
// 前端 POST /copilotkit，同一个 threadId
{
  "method": "agent/run",
  "params": {"agentId": "campaign_chat"},
  "body": {
    "threadId": "test-001",      // ★ 必须和首次请求相同
    "messages": [
      {"role": "user", "content": "确认创建"}  // 用户的回复
    ]
  }
}
```

```python
# 后端解析为 Command(resume=...)
graph_input = Command(resume={"approved": True, ...})

# → LangGraph checkpointer 恢复到 interrupt 点
# → interrupt() 返回 resume 值
# → 子 Agent 继续执行
```

**关键设计：**
- **thread_id 贯穿**：首次请求和恢复请求必须用同一个 `threadId`，LangGraph checkpoint 据此恢复。
- **checkpointer 持久化**：状态存 Postgres/SQLite，进程重启也能恢复。
- **HTTP 断开不取消**：`RunSupervisor` 把主 run 放到独立 worker，HTTP 断开不 cancel Agent，前端可重连续跑。

### 1.6 用户消息持久化

每次用户消息都落库（`ai_conversation_record` 表），用于历史回放和重连恢复：

```python
# stream_handler.py 中的 _persist_user_message_if_any()
# 1. 用户消息 INSERT → 拿到 record_id
# 2. record_id 覆写 raw_messages[0].id → 保证内存镜像和 DB 用同一个 id 去重
# 3. 更新 conversation 的 last_record_id
```

### 1.7 连接重放（Replay）

前端断线重连后，通过 `agent/connect` 方法重放历史 SSE 帧：

```python
# stream_handler.py
if is_connect_replay:
    # RunBus 存在 → 走 Redis-aware fast path（live tail 或回退 DAL）
    # RunBus 不存在 → 退化到 stream_agui_connect_replay（从 checkpoint 重放）
    async for frame in attach_or_replay(graph, thread_id, run_id, graph_config):
        yield frame
```

### 1.8 SSO 认证与上下文传播

```
SSO Cookie → SSOAuthMiddleware → ContextVar（请求级）
  → Agent / MCP / SOA 只读 getter
  → 不进入 LangGraph state / checkpoint / 进程全局
```

**跨进程传播规则：**
- `asyncio.to_thread()` 自动继承 ContextVar
- 原生 executor 线程需 `run_in_executor_with_context()`
- 子进程需显式传 `build_sso_subprocess_env()`
- 常驻 worker 需显式传 `SsoContextSnapshot` + `sso_context_scope()`

### 1.9 Resume 确认气泡渲染

用户在 interrupt 弹窗确认后，后端会生成一个 markdown 确认气泡，展示用户确认了哪些字段：

```python
# _format_resume_confirm_message()
# → 过滤协议字段（approved, _interrupt_id, feedback 等）
# → 用 QConfig 配置的字段标签 + 随机 icon
# → 生成 markdown：
# ✅ **Confirmation received**
# Here's a summary of what you confirmed:
# - ✨ **Campaign Type**: Airline Standard
# - 🟢 **Page Name**: Singapore Airline Promotion
# > Rolling forward with these settings now ✨
```

---

## 二、选品子 Agent 实现详解

### 2.1 概述

选品子 Agent（`subagent_selection`）负责选品池的**创建、更新、查询**，支持酒店/机票/景区(TNT)/跟团游多条业务线。

**核心文件：** `campaign/agent/subagents/selection/selection_handler.py`（1576 行）

### 2.2 状态定义

```python
# campaign/agent/subagents/selection/state.py
class SelectionAgentState(DeepAgentState):
    soa_context: NotRequired[SoaContext]  # SOA 调用上下文（SSO Cookie 等）
```

继承 DeepAgentState，额外注入 `soa_context` 供工具调用时携带 SSO 认证。

### 2.3 图结构（状态机）

```
START → prepare → [路由] → build_draft_generate → [路由] → show_draft → wait_confirm → [路由] → execute → finalize → END
         │                      ↑                                │
         │                      └──── needs_clarify ─────────────┘
         │                              (failed 池澄清后回 generate)
         │
         ├── query 模式 → selection_query → finalize → END
         └── 无池/失败 → finalize → END
```

**完整节点和路由：**

| 节点 | 职责 |
|------|------|
| `prepare` | 从 user_brief 用 LLM 解析选品池；池名去重；活动上下文门禁 |
| `selection_query` | 查询模式：列出活动下所有选品池 / 查询池详情 |
| `build_draft_generate` | 调 `run_draft()` 生成各池 upsert 报文（只生成不创建）；幂等重入 |
| `show_draft` | 渲染草稿 Markdown（drafted + failed）并发送到前端 |
| `wait_confirm` | `interrupt` 等用户确认；解析意图（confirm/cancel/modify/clarify） |
| `build_draft_clarify` | `interrupt` 追问 failed 池（legacy 回路，兼容保留） |
| `execute` | 消费 drafts 调 SOA upsert 创建选品池；收集结果 |
| `finalize` | 发布 TaskSnapshot（SUCCEEDED/PARTIAL/FAILED/CANCELLED） |

### 2.5 各节点详解

#### prepare 节点（准备阶段）

```python
async def selection_prepare_node(state, config):
    # 0) 活动上下文门禁：main_campaign_id / sub_campaign_id 必须存在
    #    缺失 → emit_bubble 提示 + 返回 failed

    # 1) 从 user_brief 用 LLM 解析选品池
    brief_input = await _parse_brief_to_selection_input(brief, state)

    # 1.5) 检查是否为查询模式
    if brief_input.query_mode:
        return {"status": "query", "selection_query_mode": ...}

    # 2) 池名去重：brief create 池若与 artifacts 已创建成功的池重名
    #    追加随机后缀（-abcd），避免冲突
    brief_pools = _dedupe_brief_pool_names(brief_pools, existing_names)

    # 3) 无选品池 → 静默流转 finalize（不报错）
    if not brief_pools:
        return {"selection_pool_input": SelectionPoolInput(pools=[])}

    # 4) 有池 → 写入 state，标记 ready
    return {"selection_pool_input": ..., "status": "ready"}
```

**池名去重规则：**
- 仅 create 池重命名（update 池按 schema_id 匹配，名称保留）
- 失败 artifact 同名不改名（保留原名直接重试创建）
- 同一批 brief 内部重名也去重（后出现的改名）
- 改名后总长 ≤ 50 字符（SOA 限制），超长先截断

#### build_draft_generate 节点（草稿生成）

```python
async def build_draft_generate_node(state, config):
    # 幂等重入：从 state 读已持久化的 drafts
    # 只重生成 status=failed 的池（已 drafted 的不重跑）
    pending = [p for p in pools
               if pool_key not in drafts or drafts[pool_key].status == "failed"]
    # 排除用户已取消的池

    # 生成 pending 池的 upsert 报文 payload（只生成不创建）
    new_items = await run_draft(pools=pending, state=state, clarify=clarify, config=config)
    # 持久化 drafts 到 state

    # 有 failed 且未达 MAX_DRAFT_ROUNDS(5) → 标记 needs_clarify
    # 全 drafted 或达上限 → 标记 drafted → 进 show_draft
```

**关键设计——为什么 generate 和 clarify 分成两个节点？**
> interrupt 必须落在独立节点（clarify），否则 interrupt 暂停时本节点 return 不执行、drafts 丢失，导致 resume 后全部池重跑。generate 的 return 先持久化 drafts，clarify 的 interrupt 只读不写。

#### show_draft 节点（草稿预览）

```python
async def show_draft_node(state, config):
    # 加载 drafts，分离 active_drafted / active_failed
    # 渲染 drafted 草稿 Markdown（含指标解析后的可读值）
    # 渲染 failed 池错误 Markdown
    # 组合并 emit_bubble 发送到前端

    # 为 failed 池生成 LLM 智能建议（如"建议使用全量数据源"）
    failed_suggestions = await _generate_failed_pool_suggestions(active_failed)

    # 写入 preview_context 供 wait_confirm 使用
    return {"preview_context": {...}, "draft_preview_markdown": combined_markdown}
```

**LLM 智能建议生成：**
- 根据每个 failed 池的错误信息，调用 LLM 生成个性化的处理建议
- 建议不超过 50 词，与池名语言保持一致（中文/英文自动检测）

#### wait_confirm 节点（用户确认）

```python
async def wait_confirm_node(state, config):
    # interrupt 等待用户回复
    resume = interrupt({
        "type": "selection_draft_preview",
        "payload": {
            "markdown": combined_markdown,
            "drafted_pools": [...],   # 已生成的池
            "failed_pools": [...],    # 失败的池（含错误 + 建议）
        },
    })

    reply = serialize_chat_input_reply(resume)

    # 无 failed 池：LLM 分类用户意图
    if not active_failed:
        intent = await classify_confirm_intent(reply)
        # → confirm / cancel / modify
        if intent == "confirm": → execute
        if intent == "modify": → 回 generate（重生成被修改的池）
        if intent == "cancel": → finalize

    # 有 failed 池：LLM 解析澄清意图
    else:
        intent_result = await parse_clarify_intent(reply, failed_pool_names)
        # → 逐池 classify: clarify / cancel
        # 有 remaining_failed → 回 generate 重跑
        # 全处理完 → execute
```

**用户支持的回复类型：**

| 用户回复 | 示例 | 处理 |
|---------|------|------|
| 确认全部 | "确认""可以""yes" | → execute 创建所有池 |
| 取消全部 | "取消""算了""cancel all" | → finalize（CANCELLED） |
| 澄清 failed 池 | "酒店池用全量数据源" | → 回 generate 重跑该池 |
| 取消部分 failed 池 | "机票池不要了" | → 该池移入 cancelled，其余继续 |
| 修改 drafted 池 | "酒店池改名为XXX" | → 回 generate 重生成 |

#### execute 节点（执行创建）

```python
async def selection_execute_node(state, config):
    # 消费 drafts 直接调 SOA upsert 创建选品池
    output = await create_selection_pools(drafts, state, config)

    # 收集结果 → 合并 artifacts（成功 + 失败）
    artifacts = _merge_selection_artifacts(artifacts, list(output.pools))

    # 渲染结果 Markdown 并 emit_bubble
    emit_bubble(_render_execute_result(output.pools))

    # 发布 TaskSnapshot
    # 全成功 → SUCCEEDED
    # 部分成功 → PARTIAL
    # 全失败 → FAILED
    # 全取消 → CANCELLED
    return publish_task_snapshot(snapshot, artifacts=artifacts)
```

**artifacts 合并规则（schema_id 优先 / name 回退）：**
- 两侧都有 schema_id（更新场景）→ 按 schema_id 匹配，新覆盖旧
- 一侧无 schema_id（新增场景）→ 按 name 匹配
- 覆盖时双向清理残留（避免失败→成功过渡时重复记录）

### 2.6 路由函数

```python
def route_after_prepare(state):
    # query 模式 → selection_query
    # 失败/无池 → finalize
    # 有池 → build_draft_generate

def route_after_build_draft_generate(state):
    # 统一进 show_draft（无论是否有 failed）

def route_after_wait_confirm(state):
    # selection_confirmed=True → execute
    # status=needs_clarify → build_draft_generate（重跑 failed）
    # 其他（取消）→ finalize

def route_after_build_draft_clarify(state):
    # 总是回到 build_draft_generate（legacy 回路）
```

### 2.7 意图解析（LLM 驱动）

选品子 Agent 有两套 LLM 意图解析器：

#### confirm_intent（确认意图）
```python
# confirm_intent.py
class SelectionConfirmIntent(BaseModel):
    action: Literal["confirm", "cancel", "modify"]
    confidence: float
    reason: str

# LLM 异常时降级为 cancel（保守，避免误创建）
```

#### clarify_intent（澄清意图）
```python
# clarify_intent.py
class ClarifyPoolIntent(BaseModel):
    pool_name: str        # 模糊匹配 failed 池名
    action: Literal["clarify", "cancel"]
    info: str             # 用户补充信息（仅 clarify 有效）

class ClarifyIntentResult(BaseModel):
    cancel_all: bool      # "全部取消"
    pools: list[ClarifyPoolIntent]

# LLM 失败时默认全部 clarify 并将整段回复作为补充信息
```

### 2.8 完整流程示例

```
用户："帮我创建一个酒店池和机票池"

1. prepare
   → LLM 解析出 2 个池：酒店池(create) + 机票池(create)
   → 池名去重
   → status = ready

2. build_draft_generate（第 1 轮）
   → run_draft() 生成 upsert payload
   → 酒店池 drafted ✅，机票池 failed ❌（缺少城市）
   → 有 failed，标记 needs_clarify

3. show_draft
   → 渲染 Markdown：酒店池草稿表格 + 机票池错误表格
   → LLM 为机票池生成建议："[优化建议] 请补充城市信息"
   → emit_bubble 发送到前端
   → 写入 preview_context

4. wait_confirm
   → interrupt 暂停，等用户回复
   → 用户回复："机票池城市改为新加坡"

5. wait_confirm 解析
   → parse_clarify_intent：机票池 action=clarify, info="城市改为新加坡"
   → remaining_failed = [机票池] → status=needs_clarify

6. 路由回 build_draft_generate（第 2 轮）
   → 只重跑机票池（酒店池已 drafted 不重跑）
   → 机票池 drafted ✅
   → 全 drafted → 进 show_draft

7. show_draft（第 2 轮）
   → 渲染两个池的草稿
   → emit_bubble

8. wait_confirm（第 2 轮）
   → interrupt 暂停
   → 用户回复："确认"

9. wait_confirm 解析
   → classify_confirm_intent："确认" → confirm
   → selection_confirmed = True

10. execute
    → SOA upsert 创建两个池
    → 酒店池成功 schema_id=12345 ✅
    → 机票池成功 schema_id=12346 ✅
    → 合并 artifacts
    → emit_bubble 渲染结果表格

11. finalize
    → TaskSnapshot(SUCCEEDED, "2/2 selection pools created")
    → 返回给 Main Agent
```

---

## 三、速记

| 主题 | 速记 |
|------|------|
| 前后端协议 | AG-UI + SSE，CopilotKit 前端，FastAPI 后端，text_delta 增量渲染 |
| 三种请求 | 首次(dict) / 恢复(Command(resume)) / 重放(agent/connect) |
| interrupt/resume | 子 Agent interrupt() 暂停 → SSE 推 on_interrupt → 前端确认 → 同一 threadId resume → checkpointer 恢复 |
| HTTP 断开 | RunSupervisor 独立 worker，断开不 cancel，可重连续跑 |
| 选品状态机 | prepare→generate→show_draft→wait_confirm→execute→finalize，failed 池循环重跑 |
| 选品确认 | LLM 解析用户意图（confirm/cancel/modify/clarify），无 failed 用 confirm_intent，有 failed 用 clarify_intent |
| 草稿生成 | generate 只生成 payload 不创建，幂等重入只重跑 failed，drafts 持久化到 state |
| artifacts 合并 | schema_id 优先 / name 回退，双向清理残留 |
