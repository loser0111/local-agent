# Plan & Execute（规划-执行）模式实现方案（开发依据）

> - 项目：`E:\learn\local-agent`（Wails v2 + Go 后端 + Vue 3 前端）
> - 状态：待实施
> - 用途：为 local-agent 引入 **Plan & Execute** 能力：复杂请求先规划成可审核的任务清单，用户批准后分步执行、进度可视。本文档为后续开发的唯一依据，包含机制解析、数据契约、关键代码、边界与验收标准。

---

## 一、背景与目标

### 1.1 需求

当前 local-agent 的执行方式是**单轮工具循环**：用户一句话 → 模型走一步看一步（最多 `MaxChatTurns=50` 轮工具调用）→ 最终回复。存在三个核心问题：

1. **不可预览**：长任务（如"升级依赖并跑测试"）开始执行前，用户不知道模型打算做什么；
2. **不可控**：审批是逐条工具调用噪音，无法在任务层整体把关、也无法改步骤；
3. **易失控**：多步任务没有任务状态，模型易偏题/重复；上下文随工具结果线性膨胀，**没有任何压缩机制**，长任务必然撑爆模型上下文窗口（报 400）。

Plan & Execute 把执行拆为「**规划 → 审核 → 分步执行 → 汇总**」四段，解决以上全部问题，同时为后续多 Agent（步骤执行者可替换为可委派 agent）铺路。

### 1.2 目标

| 编号 | 目标 | 优先级 |
| --- | --- | --- |
| G1 | 输入栏「计划」开关：开启后发送消息走规划流程，产出结构化计划 | P0 |
| G2 | 计划审核：PlanPane 展示步骤清单，支持编辑步骤文字、增删步骤后批准 | P0 |
| G3 | 分步执行：批准后逐步执行，每步=一次完整工具循环，步骤状态实时可视 | P0 |
| G4 | 执行过程复用现有事件管道：工具调用卡片、流式分片、diff 联动全部生效 | P0 |
| G5 | **最小上下文压缩**（确定性截断）：计划执行时旧工具结果不再全量重发 | P0 |
| G6 | 计划持久化：`~/.local-agent/plans/`，重启后可查看历史计划 | P1 |
| G7 | 取消执行：步骤边界响应取消，计划标记 cancelled | P1 |
| G8 | 平凡请求免计划：规划器判断无需计划时直接回复，不强制走计划 | P1 |
| G9 | 逐步确认模式：每步执行完暂停等用户批准再继续 | P2 |

### 1.3 非目标（本期不做）

- 步骤并行执行 / DAG 依赖编排（本期严格串行）
- 多 Agent 委派（本期每步执行者固定为会话当前模型 + 会话工具集）
- 计划模板库 / 跨会话计划复用
- LLM 摘要式上下文压缩（本期用确定性截断，LLM 摘要留待记忆管理阶段）
- 应用崩溃后「断点续跑」（重启时 running 计划直接标记失败，见 R12）

---

## 二、现状盘点

### 2.1 已有能力（可直接复用）

| 层 | 文件 | 现状 | 对 Plan & Execute 的价值 |
| --- | --- | --- | --- |
| 后端 | `chat.go` | `executeChat()`（L439）：加载会话→模型→工作区基线→组装 system prompt→工具循环；`MaxChatTurns=50` | **执行引擎本体**，抽出 `runToolLoop` 后按步复用 |
| 后端 | `chat.go` | `ChatEvent`（L384）：`tool_call_start/end / reply_delta / done / error / diff_update`，`Turn` 字段已存在 | 事件管道直接扩展计划事件 |
| 后端 | `chat.go` | `callLLM` / `callLLMStream`；`buildLLMMessages(session, query, systemPrompt)`（L396） | 规划器复用 callLLM；步骤执行复用消息构建 |
| 后端 | `chat.go` | `ChatResult{Reply, ToolCalls, Messages, Diff, Error}`（L92） | 增加 `Plan` 字段即可 |
| 后端 | `sessions.go` | `Message{ID, Role, Content, ToolCalls, ToolCallID, CreatedAt}`；`AppendMessage` 自动更新标题 | 步骤执行过程作为普通消息持久化，历史/diff 无缝衔接 |
| 后端 | `chat.go` L467-484 | system prompt 组装：基础人设 + 工作区 + Skills | 步骤执行在此之上追加「计划上下文」段 |
| 后端 | `sessions.go` | `Session.PermissionMode`（默认 `manual`，L126） | 计划批准是任务级闸门；工具级权限沿用现状 |
| 前端 | `panes/PlanPane.vue` | **已存在的演示占位组件**（硬编码 5 步骤 demo） | 直接重写为真实实现 |
| 前端 | `PaneContainer.vue` L63 | `plan: PlanPane` 已注册进面板映射 | 打开计划面板零成本：`paneStore.openPane(sid, 'plan')` |
| 前端 | `stores/chat.js` | `addLocalMessage / appendStreamContent / addToolCall / updateToolCall` | 步骤消息渲染复用 |
| 前端 | `ChatPane.vue` L228-231 | 「有 diff 自动打开右侧面板」模式 | 计划创建后自动打开 PlanPane，同一模式 |
| 前端 | `ChatPane.vue` 输入工具栏 | 「流式」开关按钮（上一期实现） | 「计划」开关完全同构 |
| 前端 | `api/session.js` | Wails / 浏览器 mock 双模式 | 计划 API 照此结构 |

### 2.2 差距

| 维度 | 目标形态 | 现状 | 差距 |
| --- | --- | --- | --- |
| 规划 | 结构化计划（JSON 步骤清单） | 无 | 需新增规划器 + JSON 解析 |
| 审核 | 可编辑步骤 + 批准闸门 | PlanPane 为假数据 | 需重写 + 计划 store |
| 执行 | 按步执行、状态回写 | 单次工具循环 | 需步骤调度器 + 事件 |
| 压缩 | 旧工具结果截断 | 全量历史重发 | 需压缩函数 |
| 持久化 | 计划落盘 | 无 | 需 PlanStore |
| 取消 | 步骤边界可取消 | 无（前端停止仅停止等待） | 需取消标志 |

---

## 三、机制解析（对标依据）

> Devin / Claude Code Plan Mode 未开源，以下为官方演示中可观测的行为；本方案只对齐**可观测行为**，实现细节为自主设计。

### 3.1 可观测的标准行为

```
用户: 帮我给这个仓库升级依赖并跑通测试
  1) 规划器输出步骤清单（不执行任何写操作）
     1. 检查 package.json 与依赖版本   2. 运行 npm outdated   3. 逐个升级
     4. 跑测试   5. 修复失败用例   6. 汇总变更
  2) 用户审核：可改文字 / 删步骤 / 直接批准
  3) 逐步执行：每步有状态（待办→执行中→完成/失败），过程对用户实时可见
  4) 每步产出结果摘要，作为后续步骤的上下文
  5) 全部完成后汇总回复
```

### 3.2 对标结论（本方案要实现的五件事）

1. **规划（Plan）**：一次独立 LLM 调用（无工具、低温度），产出 `{needPlan, title, steps:[{title, detail}]}`；
2. **审核（Review）**：计划落盘为 `awaiting_approval`，前端面板可编辑后保存；
3. **执行（Execute）**：调度器按序执行每步 = 一次 `runToolLoop`，把「计划上下文 + 当前步骤」作为输入；
4. **回写（Track）**：步骤状态与结果摘要实时回写计划并推送事件；
5. **压缩（Compact）**：执行期间对历史做确定性截断，防止上下文爆炸。

---

## 四、方案选型与总体设计

### 4.1 关键选型

| 决策点 | 候选 | **采用** | 理由 |
| --- | --- | --- | --- |
| 触发方式 | A. 模型自动判断；B. 输入栏显式开关 | **B**（+G8 兜底） | 显式开关可预期、零误判；规划器判断平凡请求后可直答，兼得 A 的顺滑 |
| 结构化输出 | A. function calling；B. 提示词 + JSON 解析 | **B** | 与工具协议解耦，弱模型可用；解析失败自动重试一次 |
| 步骤执行者 | A. 独立子 agent；B. 会话本体 | **B** | 复用会话模型/工具白名单/历史，上下文连续；A 留给多 Agent 阶段 |
| 步骤间上下文 | A. 每步全新会话；B. 共享会话历史 + 压缩 | **B** | 前后步可相互引用（如"用第 2 步得到的路径"）；压缩防爆炸 |
| 消息持久化 | A. 计划私有空间；B. 写入会话历史 | **B** | 步骤过程在聊天流完全可见（透明性）；diff/标题逻辑零改动 |
| 取消 | A. goroutine 强杀；B. 协作式标志位 | **B** | 在「步骤开始前 + 每轮工具循环前」检查，无并发风险 |

### 4.2 总体数据流

```
【规划】 ChatPane 输入栏「计划」开关开启 → 发送
  Chat(sessionID, query, useStream, usePlan=true)
    -> ChatPlan():
         1. AppendMessage(user 消息)            # 原文入库，标题正常生成
         2. planner LLM 调用（callLLM，无工具，temp=0.2）
            提示词要求输出严格 JSON：{needPlan, title, steps:[{title,detail}]}
         3. needPlan=false → 直接走 runToolLoop 普通回复（G8）
         4. needPlan=true  → PlanStore.Save(status=awaiting_approval)
         5. emit chat:event {type:"plan_update"}；返回 ChatResult{Plan}

【审核】 PlanPane 展示计划 → 用户编辑步骤 → SavePlan(plan) → 「批准执行」

【执行】 ExecutePlan(planID, useStream)   # 长耗时绑定，同 Chat 一样阻塞返回
    for step in steps:
        1. 检查取消标志 / stepApproval（G9）
        2. status=running，emit plan_update
        3. runToolLoop(sessionID, synthesizedQuery, planSystemPrompt(step), useStream)
           - synthesizedQuery = "【计划步骤 i/N】<title>\n<detail>"
           - planSystemPrompt = 现有组装 + 「## 当前计划」块（只许做本步）
           - 过程消息照常 AppendMessage；工具卡片/reply_delta/diff 事件照常推送
        4. 成功 → status=done + Summary=回复首段；失败 → status=failed 并终止计划
    5. 全部完成 → status=completed，emit plan_update；返回 ChatResult{Plan, Reply=汇总}

【前端】 stores/plan.js 接收事件维护计划状态；PlanPane 渲染进度
         ChatPane 在步骤 running 时为本步创建流式占位消息（复用现有逻辑）
```

### 4.3 设计原则

1. **计划是数据不是魔法**：计划就是一个 JSON 文件，步骤就是普通字符串，用户可任意编辑；
2. **执行引擎唯一**：规划与执行复用同一个 `runToolLoop`，不出现第二套工具循环；
3. **透明性优先**：每步的用户消息、助手回复、工具调用全部进会话历史，聊天流即审计日志；
4. **失败即停**：任一步失败，后续步骤标记 skipped，计划置 failed，错误摘要给用户决策（重试/改计划）；
5. **零破坏**：普通聊天路径（usePlan=false）行为与现在完全一致，压缩只作用于计划执行。

---

## 五、数据契约

> 契约是前后端并行开发的分界点，**先冻结本节再开工**。

### 5.1 磁盘结构

```
~/.local-agent/
└── plans/
    ├── plan_<毫秒时间戳>_<随机4位>.json     # 每个计划一个文件
    └── ...
```

计划文件示例：

```json
{
  "id": "plan_1726000000000_a1b2",
  "sessionId": "sess_1725999900000_x9y8",
  "title": "升级依赖并跑通测试",
  "status": "running",
  "steps": [
    { "index": 0, "title": "检查 package.json 与依赖版本", "detail": "读取并总结当前依赖清单",
      "status": "done", "summary": "共 24 个依赖，3 个落后主版本", "startedAt": 1726000001000, "finishedAt": 1726000012000 },
    { "index": 1, "title": "运行 npm outdated", "detail": "", "status": "running" },
    { "index": 2, "title": "逐个升级落后依赖", "detail": "优先 patch/minor，major 需说明", "status": "pending" }
  ],
  "createdAt": 1726000000000,
  "updatedAt": 1726000012000
}
```

### 5.2 Go 侧结构体（`plan.go`）

```go
// PlanStepStatus 步骤状态
const (
    StepPending  = "pending"  // 待执行
    StepRunning  = "running"  // 执行中
    StepDone     = "done"     // 完成
    StepFailed   = "failed"   // 失败
    StepSkipped  = "skipped"  // 因前序失败跳过
)

// PlanStatus 计划状态
const (
    PlanAwaitingApproval = "awaiting_approval" // 待审核（可编辑）
    PlanRunning          = "running"           // 执行中
    PlanCompleted        = "completed"
    PlanFailed           = "failed"
    PlanCancelled        = "cancelled"
)

// PlanStep 计划步骤
type PlanStep struct {
    Index      int    `json:"index"`
    Title      string `json:"title"`
    Detail     string `json:"detail,omitempty"`
    Status     string `json:"status"`
    Summary    string `json:"summary,omitempty"`   // 执行结果摘要（done 后）
    Error      string `json:"error,omitempty"`     // 失败原因
    StartedAt  int64  `json:"startedAt,omitempty"`
    FinishedAt int64  `json:"finishedAt,omitempty"`
}

// Plan 计划
type Plan struct {
    ID        string      `json:"id"`
    SessionID string      `json:"sessionId"`
    Title     string      `json:"title"`
    Status    string      `json:"status"`
    Steps     []*PlanStep `json:"steps"`
    CreatedAt int64       `json:"createdAt"`
    UpdatedAt int64       `json:"updatedAt"`
}
```

### 5.3 契约变更点

| 位置 | 变更 |
| --- | --- |
| `ChatResult`（chat.go L92） | 新增 `Plan *Plan \`json:"plan,omitempty"\``（规划/执行返回时携带） |
| `ChatEvent`（chat.go L384） | 新增 `Plan *Plan \`json:"plan,omitempty"\`` 与 `StepIndex int \`json:"stepIndex,omitempty"\``；`Type` 新增取值 `plan_update`（计划创建/步骤状态变更/计划完成，**全量携带最新 Plan**） |
| `Chat` 绑定 | **签名不变**，新增第 4 参：`Chat(sessionID, query string, useStream, usePlan bool)`；`usePlan=true` 内部转 `ChatPlan` 流程 |
| 新增绑定 | `GetSessionPlan(sessionID) *Plan`（取会话当前计划，无则 nil）、`SavePlan(plan *Plan) error`（仅 `awaiting_approval` 可改，改 title/steps）、`ExecutePlan(planID string, useStream bool) *ChatResult`、`CancelPlan(planID) error`、`ListPlans(sessionID) []*Plan`（历史，P1） |

### 5.4 事件时序约定

| 事件 | 携带 | 时机 |
| --- | --- | --- |
| `plan_update` | `Plan`（全量）、`StepIndex`（触发步骤，可为 -1） | 计划创建 / 每步状态变更 / 计划终态 |
| `tool_call_start/end`、`reply_delta`、`diff_update` | 同现有 | 步骤执行期间照常推送（前端按「当前 running 步骤」归属到对应消息） |
| 返回值 | `ChatResult{Plan, Reply, ...}` | `ExecutePlan` 结束时 |

### 5.5 前端 JSDoc（`types/index.js` 增补）

```js
/**
 * 计划步骤
 * @typedef {Object} PlanStep
 * @property {number} index
 * @property {string} title
 * @property {string} [detail]
 * @property {'pending'|'running'|'done'|'failed'|'skipped'} status
 * @property {string} [summary]
 * @property {string} [error]
 * @property {number} [startedAt]
 * @property {number} [finishedAt]
 */

/**
 * 计划
 * @typedef {Object} Plan
 * @property {string} id
 * @property {string} sessionId
 * @property {string} title
 * @property {'awaiting_approval'|'running'|'completed'|'failed'|'cancelled'} status
 * @property {PlanStep[]} steps
 * @property {number} createdAt
 * @property {number} updatedAt
 */
```

---

## 六、后端实现

### 6.1 新增文件 `plan.go`（核心）

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"
)

// ===== 数据结构（对应 5.2，此处省略重复定义）=====

// ===== PlanStore =====

// PlanStore 计划持久化与查询
type PlanStore struct {
    mu    sync.RWMutex
    dir   string
    plans map[string]*Plan
}

func NewPlanStore(dir string) *PlanStore {
    s := &PlanStore{dir: dir, plans: map[string]*Plan{}}
    _ = os.MkdirAll(dir, 0o755)
    s.loadAll()
    return s
}

// loadAll 启动时加载全部计划；running 状态的计划标记为失败（R12：无断点续跑）
func (s *PlanStore) loadAll() {
    entries, err := os.ReadDir(s.dir)
    if err != nil {
        return
    }
    for _, e := range entries {
        if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
            continue
        }
        data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
        if err != nil {
            continue
        }
        var p Plan
        if json.Unmarshal(data, &p) != nil {
            continue
        }
        if p.Status == PlanRunning {
            p.Status = PlanFailed
            for _, st := range p.Steps {
                if st.Status == StepRunning {
                    st.Status = StepFailed
                    st.Error = "应用重启，执行中断"
                }
            }
        }
        s.plans[p.ID] = &p
    }
}

func (s *PlanStore) saveLocked(p *Plan) error {
    p.UpdatedAt = time.Now().UnixMilli()
    data, err := json.MarshalIndent(p, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(filepath.Join(s.dir, p.ID+".json"), data, 0o644)
}

// Save 新建或更新计划
func (s *PlanStore) Save(p *Plan) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if err := s.saveLocked(p); err != nil {
        return err
    }
    s.plans[p.ID] = p
    return nil
}

// Get 按 ID 取计划（副本语义由调用方保证：执行器持引用串行修改）
func (s *PlanStore) Get(id string) (*Plan, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    p, ok := s.plans[id]
    if !ok {
        return nil, fmt.Errorf("计划不存在: %s", id)
    }
    return p, nil
}

// GetBySession 取会话当前（最新创建的）计划
func (s *PlanStore) GetBySession(sessionID string) *Plan {
    s.mu.RLock()
    defer s.mu.RUnlock()
    var latest *Plan
    for _, p := range s.plans {
        if p.SessionID == sessionID && (latest == nil || p.CreatedAt > latest.CreatedAt) {
            latest = p
        }
    }
    return latest
}

// ListBySession 返回会话全部计划（按创建时间倒序，P1 历史列表用）
func (s *PlanStore) ListBySession(sessionID string) []*Plan { /* 遍历过滤排序，略 */ }

// ===== 规划器 =====

const plannerSystemPrompt = `你是任务规划器。把用户的请求拆解为可逐步执行的计划。

要求：
1. 只输出一个 JSON 对象，不要输出任何其他文字或代码块标记。
2. JSON 格式：{"needPlan": bool, "title": string, "steps": [{"title": string, "detail": string}]}
3. needPlan 判断：简单的问答、闲聊、单步操作（如"查看当前目录"）返回 needPlan=false，
   并把 steps 置为空数组；需要多步操作、多文件修改、有先后依赖的任务返回 true。
4. 每个步骤必须是一个可独立验证完成的小任务；detail 补充该步的要点或注意事项，可为空字符串。
5. 步骤数量 2-8 个；不要把"汇总/汇报"设为步骤。`

// planDraft 规划器原始输出
type planDraft struct {
    NeedPlan bool `json:"needPlan"`
    Title    string `json:"title"`
    Steps    []struct {
        Title  string `json:"title"`
        Detail string `json:"detail"`
    } `json:"steps"`
}

// parsePlanDraft 解析规划器输出；容忍 ```json 包裹与前后杂讯
func parsePlanDraft(content string) (*planDraft, error) {
    text := strings.TrimSpace(content)
    text = strings.TrimPrefix(text, "```json")
    text = strings.TrimPrefix(text, "```")
    text = strings.TrimSuffix(text, "```")
    // 截取第一个 { 到最后一个 } 之间的内容
    start := strings.Index(text, "{")
    end := strings.LastIndex(text, "}")
    if start < 0 || end <= start {
        return nil, fmt.Errorf("规划输出中未找到 JSON 对象")
    }
    var d planDraft
    if err := json.Unmarshal([]byte(text[start:end+1]), &d); err != nil {
        return nil, fmt.Errorf("解析规划 JSON 失败: %w", err)
    }
    if d.NeedPlan && len(d.Steps) == 0 {
        return nil, fmt.Errorf("规划器返回 needPlan=true 但步骤为空")
    }
    return &d, nil
}

// ===== 步骤执行上下文 =====

// buildPlanStepQuery 合成步骤的用户消息（持久化进会话历史）
func buildPlanStepQuery(plan *Plan, step *PlanStep) string {
    return fmt.Sprintf("【计划步骤 %d/%d】%s", step.Index+1, len(plan.Steps), step.Title)
}

// buildPlanSystemPrompt 在既有 system prompt（人设+工作区+技能）之上追加计划上下文
func buildPlanSystemPrompt(base string, plan *Plan, step *PlanStep) string {
    var sb strings.Builder
    sb.WriteString(base)
    sb.WriteString("\n\n## 当前计划\n你正在按用户批准的计划分步执行，一次只做一步。\n")
    sb.WriteString("计划：" + plan.Title + "\n")
    for _, st := range plan.Steps {
        mark := "待办"
        switch st.Status {
        case StepDone:
            mark = "已完成"
        case StepRunning:
            mark = "当前步骤"
        case StepFailed, StepSkipped:
            mark = st.Status
        }
        sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", st.Index+1, mark, st.Title))
        if st.Status == StepDone && st.Summary != "" {
            sb.WriteString("   结果摘要：" + st.Summary + "\n")
        }
    }
    sb.WriteString(fmt.Sprintf("\n本步任务：%s\n", step.Title))
    if step.Detail != "" {
        sb.WriteString("任务要点：" + step.Detail + "\n")
    }
    sb.WriteString("完成后用一两句话总结本步结果（将作为该步骤的交付摘要），不要执行其他步骤。")
    return sb.String()
}

// extractStepSummary 取回复首段（≤200字）作为步骤摘要
func extractStepSummary(reply string) string {
    reply = strings.TrimSpace(reply)
    for _, sep := range []string{"\n\n", "\n"} {
        if idx := strings.Index(reply, sep); idx > 0 {
            reply = reply[:idx]
            break
        }
    }
    if r := []rune(reply); len(r) > 200 {
        reply = string(r[:200]) + "…"
    }
    return reply
}

// ===== 上下文压缩（G5，确定性截断）=====

const (
    compactKeepRecent  = 20  // 最近 N 条消息保持原样
    compactMaxToolText = 200 // 更早的 tool 消息内容截断长度
)

// compactMessages 压缩历史：keepRecent 之外的工具结果截断，assistant/user 保留
// 输入输出均为 []Message（会话持久化消息），仅在构造 LLM messages 前使用，不改动存储
func compactMessages(msgs []Message) []Message {
    if len(msgs) <= compactKeepRecent {
        return msgs
    }
    cut := len(msgs) - compactKeepRecent
    out := make([]Message, len(msgs))
    copy(out, msgs)
    for i := 0; i < cut; i++ {
        if out[i].Role == RoleTool && len(out[i].Content) > compactMaxToolText {
            out[i].Content = out[i].Content[:compactMaxToolText] + "\n…（早期工具结果已省略）"
        }
    }
    return out
}
```

### 6.2 `chat.go`：抽出 `runToolLoop` + 接入压缩

**（1）重构 `executeChat`（L439）**：把「第 6 步工具循环」整体抽为可复用函数，`executeChat` 与计划执行共用：

```go
// runToolRun 执行一次「构建请求→LLM→工具循环」的完整运行。
// 返回最终回复、工具调用记录、本次持久化的消息、错误。
// systemPrompt 由调用方组装（人设+工作区+技能，可再叠加计划上下文）。
// compact=true 时对历史消息做确定性压缩（计划执行专用，普通聊天不受影响）。
// cancel 检查函数每轮工具循环前调用，返回 true 则中止。
func (a *App) runToolRun(session *Session, model *ModelConfig, query, systemPrompt string,
    useStream, compact bool, checkCancel func() bool) *ChatResult
```

实现要点（均从现有 `executeChat` L485-循环体**原样搬移**，非重写）：

- `messages := buildLLMMessages(session, query, systemPrompt)` 前若 `compact`，先 `compactMessages` 历史段；
- 每轮循环开头：`if checkCancel != nil && checkCancel() { return 中止结果 }`；
- 流式 flusher、工具执行、`AppendMessage` 持久化、`tool_call_*`/`done`/`diff_update` 事件、轮末 diff 计算——全部保持现状。

`executeChat` 瘦身为：加载会话/模型 → 工作区基线 → 组装 prompt → `a.runToolRun(session, model, query, skillPrompt, useStream, false, nil)`。

> **重要**：`AppendMessage` 自动更新标题的逻辑依赖首条 user 消息；计划步骤合成的 `【计划步骤 i/N】` 消息不是会话首条消息（首条是规划前的原始请求），标题不受污染。

**（2）`ChatPlan`（规划流程）**：

```go
// ChatPlan 规划流程：入库用户消息 → 规划器 → 落盘计划（或平凡请求直答）
func (a *App) ChatPlan(sessionID, query string, useStream bool) *ChatResult {
    // 1. 会话/模型解析 + AppendMessage(user query)   —— 与 executeChat 第 1-3 步一致
    // 2. 规划器调用（不带工具、不流式、温度 0.2）：
    //    req := &LLMReq{Model: modelID, Temperature: 0.2,
    //        Messages: []LLMMessage{{Role: RoleSystem, Content: plannerSystemPrompt},
    //                               {Role: RoleUser, Content: 规划上下文(query+工作区路径)}}}
    //    resp, err := callLLM(model.URL, model.APIKey, req)
    //    失败重试一次；仍失败返回 ChatResult{Error}
    // 3. d, err := parsePlanDraft(resp.Choices[0].Message.Content)
    // 4. if !d.NeedPlan → return a.runToolRun(...) 普通回复（G8）
    // 5. 组装 Plan{ID: plan_<ms>_<rand>, SessionID, Title, Status: PlanAwaitingApproval, Steps}
    //    a.planStore.Save(plan)
    // 6. emit plan_update；return &ChatResult{Plan: plan}
}
```

> 规划器消息**不持久化**（规划过程对用户无信息量），会话历史里只有原始请求；计划本身在 PlanPane 呈现。

**（3）`ExecutePlan`（执行调度器）**：

```go
// ExecutePlan 逐步执行计划；长耗时，结束时返回
func (a *App) ExecutePlan(planID string, useStream bool) *ChatResult {
    plan, err := a.planStore.Get(planID)
    if plan.Status != PlanAwaitingApproval && plan.Status != PlanCancelled && plan.Status != PlanFailed {
        return &ChatResult{Error: "计划状态不允许执行: " + plan.Status}
    }
    cancel := a.registerPlanCancel(planID) // P1；返回检查函数与注销函数
    defer cancel.unregister()

    plan.Status = PlanRunning
    a.emitPlanUpdate(plan, -1)

    session, model := ... // 与 executeChat 相同的加载路径
    dir, isRepo := ...    // 工作区基线一次，全部步骤共用

    for _, step := range plan.Steps {
        if cancel.check() {
            plan.Status = PlanCancelled
            markRemainingSkipped(plan, step.Index)
            a.emitPlanUpdate(plan, step.Index)
            return &ChatResult{Plan: plan}
        }
        if step.Status == StepDone { continue } // 支持失败重试时跳过已完成步

        step.Status, step.StartedAt = StepRunning, now()
        a.emitPlanUpdate(plan, step.Index)

        base := a.buildBasePrompt(session, dir) // 现有 L467-484 的组装逻辑抽成方法
        sys := buildPlanSystemPrompt(base, plan, step)
        res := a.runToolRun(session, model, buildPlanStepQuery(plan, step), sys,
            useStream, true /*compact*/, cancel.check)

        if cancel.check() { /* 同上，置 cancelled 返回 */ }
        if res.Error != "" {
            step.Status, step.Error = StepFailed, res.Error
            plan.Status = PlanFailed
            markRemainingSkipped(plan, step.Index+1)
            a.emitPlanUpdate(plan, step.Index)
            return &ChatResult{Plan: plan, Error: "步骤执行失败: " + res.Error}
        }
        step.Status, step.Summary, step.FinishedAt = StepDone, extractStepSummary(res.Reply), now()
        a.planStore.Save(plan)
        a.emitPlanUpdate(plan, step.Index)
    }

    plan.Status = PlanCompleted
    a.planStore.Save(plan)
    a.emitPlanUpdate(plan, -1)
    return &ChatResult{Plan: plan, Reply: "计划执行完成"}
}
```

**（4）取消（G7）**——`app.go` 增加协作式取消注册表：

```go
type App struct { /* ... */
    planCancels map[string]chan struct{} // planID -> 取消信号
    planMu      sync.Mutex
}

// registerPlanCancel 注册取消信号；check() 在步骤边界与每轮工具循环前被调用
// cancel 事件由 CancelPlan(planID) 绑定触发（close(channel)）
```

> 取消是协作式的：正在进行的单次 LLM 调用/工具执行会跑完，**下一步不再开始**。G9（逐步确认）复用同一机制：`RequireStepApproval=true` 时每步 done 后把计划置回 `awaiting_approval`（记录 `resumeFrom`），前端批准后再次 `ExecutePlan`。

**（5）`emitPlanUpdate`**：`wailsRuntime.EventsEmit(a.ctx, "chat:event", ChatEvent{Type: "plan_update", Plan: plan, StepIndex: i})`——前端 `session.js` 事件分发加一个 case 即可。

### 6.3 `app.go`：初始化与绑定清单

```go
type App struct { /* ...原有字段... */
    planStore   *PlanStore
}

// startup() 中，sessionStore 之后：
a.planStore = NewPlanStore(filepath.Join(baseDir, "plans"))

// 新增绑定（生成绑定后前端可见）：
// Chat(sessionID, query string, useStream, usePlan bool) *ChatResult  // 签名扩展
// GetSessionPlan(sessionID string) *Plan
// SavePlan(plan *Plan) error
// ExecutePlan(planID string, useStream bool) *ChatResult
// CancelPlan(planID string) error
// ListPlans(sessionID string) []*Plan
```

### 6.4 重新生成 Wails 绑定

```bash
cd E:\learn\local-agent
wails generate module
```

确认 `frontend/wailsjs/go/main/App.js` 出现 `GetSessionPlan / SavePlan / ExecutePlan / CancelPlan / ListPlans`，且 `Chat` 变为四参。

---

## 七、前端实现

### 7.1 新增 `frontend/src/api/plan.js`

Wails / mock 双模式（照 `api/session.js` 结构）：

- `getPlan(sessionId)` → `GetSessionPlan`；mock 从 `local-agent:plan:<sid>` 读
- `savePlan(plan)` → `SavePlan`；mock 写回
- `executePlan(planId, useStream, handlers)` → `ExecutePlan`；**执行期间监听 `chat:event`**（与 `chat()` 相同模式），分发 `plan_update / tool_call_* / reply_delta`；mock 用 `setTimeout` 模拟：每步 1.2s，状态 pending→running→done，回填假 summary
- `cancelPlan(planId)` → `CancelPlan`
- `listPlans(sessionId)` → `ListPlans`

### 7.2 新增 `frontend/src/stores/plan.js`

```js
export const usePlanStore = defineStore('plan', () => {
  const plan = ref(null)          // 当前会话最新计划
  const executing = ref(false)    // 计划执行中（与 chat.isGenerating 互斥置位）

  async function loadForSession(sessionId)   // 切换会话时调 GetSessionPlan
  function applyUpdate(p)                    // plan_update 事件落库
  async function save(p)                     // 审核编辑保存
  async function execute(planId, useStream, handlers)  // 批准执行
  async function cancel(planId)
  function reset()
})
```

### 7.3 重写 `frontend/src/panes/PlanPane.vue`

替换演示数据为真实实现（保留现有 SCSS 骨架与三态样式 `done/running/pending`）：

| 区块 | 内容 |
| --- | --- |
| 空态 | 「本会话还没有计划。在聊天输入框开启『计划』并发送复杂任务以生成。」 |
| 标题区 | `plan.title` + 状态徽标（待审核/执行中/已完成/失败/已取消，复用现有 warning/success/error 色板） |
| 步骤列表 | 每步：状态图标 + 标题；`awaiting_approval` 时变**可编辑**（每步一个输入框 + 删除按钮 + 「添加步骤」）；done 步骤下方展示 `summary`（灰色小字） |
| 底部按钮 | 待审核：`批准并执行`（主按钮）/ `保存修改`；执行中：`取消执行`；终态：`重新生成计划`（回到聊天重新发送） |

事件流：`onMounted` 调 `planStore.loadForSession`；组件内订阅 `plan_update`（经 store 统一分发）。

### 7.4 修改 `ChatPane.vue`（3 处增量）

1. **输入工具栏**：仿照「流式」按钮增加「计划」开关（`planMode` ref 或并入 settingStore——**建议本地 ref**，计划是单次意图不是全局偏好）；
2. **sendMessage 分支**：`planMode` 时调 `chat(sid, text, { plan: true, ... })`；返回 `result.plan` 时 `planStore.applyUpdate(result.plan)` + `paneStore.openPane(sid, 'plan')`（同 diff 联动模式）；
3. **事件处理**：`plan_update` 事件 → `planStore.applyUpdate`；步骤 `running` 时为本步创建流式占位消息（`addLocalMessage` + 现有 `appendStreamContent/addToolCall` 链路，`reply_delta` 归属到最新占位消息）。

### 7.5 修改 `api/session.js`

- `chat()` 第四参透传：Wails 分支 `Chat(sessionId, query, !!stream, !!plan)`；
- mock 分支：`plan=true` 时返回 `{ plan: { 模拟 3 步骤, status: 'awaiting_approval' } }`；
- 事件分发增加 `case 'plan_update': onPlanUpdate?.(eventData.plan)`。

### 7.6 互斥规则

`planStore.executing` 或 `chatStore.isGenerating` 为 true 时：输入框禁用发送、计划开关禁用切换、PlanPane 的编辑态锁定。执行期间 ChatPane 照常滚动显示步骤消息（只读体验与普通生成一致）。

---

## 八、边界情况与风险清单

| 编号 | 风险 / 场景 | 处理策略 |
| --- | --- | --- |
| R1 | 规划器输出非法 JSON / 混杂文字 | `parsePlanDraft` 容错（剥代码块 + 首尾大括号截取）；失败重试 1 次；仍失败报错给用户 |
| R2 | 规划器把简单请求也出计划 | 提示词明确 `needPlan=false` 条件；G8 兜底直答 |
| R3 | 规划器把步骤拆得过粗/过细 | 步骤可编辑（审核阶段解决）；提示词约束 2-8 步 |
| R4 | 步骤执行中途模型又想"顺手"做后面的步骤 | 计划上下文明确「一次只做一步」+ 步骤摘要回写；越界行为由下一步的对账自然纠正 |
| R5 | 某步失败 | 置 failed、后续 skipped、计划 failed；用户可编辑计划后**重新 ExecutePlan**（done 步骤自动跳过 → 天然支持"从失败处重试"） |
| R6 | 上下文爆炸 | G5 确定性压缩：最近 20 条原样、更早 tool 结果截断 200 字；只在计划执行开启（`compact=true`），普通聊天零影响 |
| R7 | 长步骤超过 MaxChatTurns=50 | 单步沿用现有上限，超限该步按失败处理（现有行为一致） |
| R8 | 执行中用户切走/关闭面板 | 执行在后端继续，事件丢失由 `GetSessionPlan` 兜底（重新打开面板全量拉取） |
| R9 | 执行中应用退出 | R12：启动时 running 计划标记 failed（loadAll 已处理） |
| R10 | 并发执行同一计划 | `ExecutePlan` 入口校验状态必须为 `awaiting_approval/failed/cancelled`；`running` 直接拒绝 |
| R11 | 取消时机 | 协作式：当前 LLM 调用与工具执行跑完，下一轮循环/下一步不开始；无需强杀 goroutine |
| R12 | 断点续跑 | 本期不支持，明确置失败（比静默丢状态更诚实） |
| R13 | 计划文件损坏 | loadAll 单文件 Unmarshal 失败即跳过，不影响其他计划 |
| R14 | 步骤消息污染会话标题 | 步骤消息非会话首条，`AppendMessage` 标题逻辑不受影响 |
| R15 | 浏览器 mock 模式 | api/plan.js 全量 mock（生成/执行/事件），可脱离桌面端开发 UI |

---

## 九、分阶段实施计划与验收

| 阶段 | 内容 | 验收标准 |
| --- | --- | --- |
| **P0 后端** | `plan.go`（模型/Store/规划器/解析/压缩/提示词）+ `chat.go` 抽 `runToolRun` + `ChatPlan`/`ExecutePlan`/事件 + `app.go` 绑定 + 绑定重生成 + `plan_test.go` | 单测全绿；curl/绑定层验证：ChatPlan 产出合法计划文件；ExecutePlan 逐步置状态、失败即停、done 步骤摘要回写 |
| **P1 前端** | `api/plan.js` + `stores/plan.js` + `PlanPane.vue` 重写 + `ChatPane` 计划开关/分支/事件 + mock | 浏览器 mock：生成计划→编辑步骤→批准→模拟逐步完成；桌面端真实模型走通端到端 |
| **P2 增强** | `CancelPlan` + 取消按钮 + `ListPlans` 历史列表 + G9 逐步确认（`RequireStepApproval`） | 执行中取消后状态 cancelled、剩余步骤 skipped；历史计划可回看 |

---

## 十、测试方案

### 10.1 后端单测（`plan_test.go`）

| 用例 | 输入 | 断言 |
| --- | --- | --- |
| T1 JSON 干净解析 | `{"needPlan":true,...}` | 步骤数、标题正确 |
| T2 脏输出 | 带注释文字 / \`\`\`json 包裹 | 剥离后解析成功 |
| T3 needPlan=false | steps 为空 | 不报错，走直答分支 |
| T4 needPlan=true 步骤为空 | — | 返回错误 |
| T5 PlanStore CRUD | Save/Get/GetBySession/ListBySession | 落盘、最新计划选取正确 |
| T6 启动恢复 | 文件中 status=running | 加载后置 failed，running 步骤置 failed |
| T7 压缩 | 30 条消息（含长 tool 结果） | 最近 20 条原样；更早 tool 截断且带省略标记；assistant/user 不动 |
| T8 步骤摘要 | 带换行的长回复 | 取首段 ≤200 字 |
| T9 计划上下文 | plan + 各状态步骤 | system prompt 含标记与已完成摘要 |
| T10 端到端（httptest） | mock LLM 先返回计划 JSON，再对每步返回 tool_calls→结果→总结 | ExecutePlan 后步骤全 done、消息入库顺序正确、事件序列完整 |

### 10.2 手动端到端（桌面端）

```bat
:: 1) wails dev 启动，新建会话（配置有效模型）
:: 2) 开启「计划」开关，输入：把 docs 下所有 md 文件名整理成一个目录文件 index.md
:: 3) PlanPane 应自动弹出，展示 3-5 个步骤，状态「待审核」
:: 4) 编辑某步骤文字 → 保存 → 批准执行
:: 5) 观察：步骤逐个变绿；ChatPane 出现每步的【计划步骤 i/N】消息、工具卡片、流式输出
:: 6) 完成后验证 index.md 真实生成；会话历史含全部步骤过程；diff 面板可看新增文件
:: 7) 中途点「取消执行」→ 计划置 cancelled，剩余步骤 skipped
```

### 10.3 浏览器 mock 验证

纯 `vite dev` 下验证 PlanPane 全状态机（空态/待审核编辑/执行中进度/失败/取消/完成）与 ChatPane 联动，无需真实模型。

---

## 十一、附录

### 11.1 改动文件清单

| 层 | 文件 | 动作 |
| --- | --- | --- |
| Go | `plan.go` | **新增**：Plan/PlanStep/PlanStore + 规划器 + 解析 + 压缩 + 步骤提示词 |
| Go | `plan_test.go` | **新增**：T1-T10 |
| Go | `chat.go` | `ChatResult` 增 `Plan`；`ChatEvent` 增 `Plan/StepIndex`；`executeChat` 抽出 `runToolRun`；新增 `ChatPlan`/`ExecutePlan`/`emitPlanUpdate`/`buildBasePrompt` |
| Go | `app.go` | `planStore` 初始化、`planCancels` 注册表、`Chat` 四参、新增 5 个绑定 |
| 自动 | `frontend/wailsjs/go/main/App.js`、`App.d.ts` | 重新生成绑定 |
| JS | `frontend/src/api/plan.js` | **新增** |
| JS | `frontend/src/stores/plan.js` | **新增** |
| JS | `frontend/src/api/session.js` | `chat()` 四参透传 + `plan_update` 分发 + mock |
| Vue | `frontend/src/panes/PlanPane.vue` | **重写**（替换演示数据） |
| Vue | `frontend/src/panes/ChatPane.vue` | 计划开关 + sendMessage 分支 + plan_update 处理（增量） |
| JSDoc | `frontend/src/types/index.js` | 增补 `Plan` / `PlanStep` |
| 数据 | `~/.local-agent/plans/` | 运行时生成（非代码文件） |

### 11.2 关键命令速查

```bash
wails generate module        # 绑定重生成（新增绑定后必须）
wails dev                    # 桌面端
cd frontend && npm run dev   # 浏览器 mock
go test ./... -run Plan      # 计划相关单测
```

### 11.3 与后续方向的衔接

- **记忆管理**：步骤摘要（`PlanStep.Summary`）是天然的"做过什么"事实源，跨会话记忆可优先从历史计划中提炼；
- **多 Agent**：`runToolRun` 的执行者参数化（当前=会话本体）后，每个步骤可委派给不同 agent；工具白名单机制直接变成 agent 级隔离。

### 11.4 术语

| 术语 | 含义 |
| --- | --- |
| 规划器（Planner） | 产出计划 JSON 的一次独立 LLM 调用（无工具、低温） |
| 步骤（Step） | 计划的最小执行单元，一次 `runToolRun` 对应一步 |
| 审核闸门 | `awaiting_approval` 状态下才可编辑/批准 |
| 协作式取消 | 只在步骤/循环边界生效的取消标志，不强杀进行中的调用 |
| 确定性压缩 | 不依赖 LLM 的历史截断（tool 结果 200 字），仅计划执行启用 |

---

（完）
