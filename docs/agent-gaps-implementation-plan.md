# Agent 硬伤补齐技术方案（中断 / 上下文 / 撤销 / 子代理）

> - 状态：方案待实施（本文档只定设计，不含实现）
> - 依据：对当前工作区代码的逐条盘点，每个论断都标了 `文件:行`
> - 范围：四条硬伤。工具层与权限层不动（那两块已经够扎实）

## 一、已拍板的四个决策

| 决策点 | 结论 |
| --- | --- |
| 中断语义 | 两级：先协作式停止，再点一次升级为硬取消 |
| 上下文策略 | 摘要式压缩，自动触发 + 手动 `/compact` |
| 子代理能力 | 完整能力型（可写、可执行，权限继承父会话，需要弹窗时直接失败） |
| 撤销范围 | 按 diff 归因精准回退本会话在该轮改过的文件 |

## 二、现状盘点（四条硬伤的代码依据）

**中断** 完全缺失，而且缺在两个地方。`runToolLoop` 的取消回调 `checkCancel` 只被计划执行注册（`app.go` 的 `ExecutePlan` → `registerPlanCancel`），普通聊天在 `chat.go:497` 附近调用时传的是 `nil`，所以循环里那句 `if checkCancel != nil` 永远是假。前端「停止」按钮在非计划路径上只做了 `chatStore.isGenerating = false`（`frontend/src/panes/ChatPane.vue:375`），是个纯客户端标志位——后端那一轮循环还在跑，还在写文件、还在起子进程。`MaxChatTurns = 50`（`chat.go:107`）保证它不会无限转，但"点了停止还在动"这件事本身不可接受。

**上下文** 只在计划路径上有一点点。`compact` 参数为真时才对历史做 `compactMessages`（`chat.go:605`），而普通聊天传 `false`；`ExecutePlan` 传 `true`（`chat.go:1067`）。`compactMessages`（`plan.go:344`）是确定性截断早期工具结果，不是摘要。全仓库没有任何 token 计数，也没有对 API 的 "context length exceeded" 做识别与重试。`LLMResp`（`anthropic.go` 附近的响应结构）**连 `usage` 字段都没有解析**，所以连"上一次调用真实用了多少 token"这个最可靠的锚点都拿不到。

**撤销** 的数据基础已经很好，缺的是"可回退性"。`DiffService.Snapshot`（`diff.go:406`）用 **`git stash create`** 产出悬空 commit，**不修改工作区与暂存区**，这正是回退所需的快照形态。本轮基线也在循环开头取好了（`chat.go:600` 的 `turnBase`）。但 `DiffTurn`（`diff.go:45`）只存 `Files/Additions/Deletions/CreatedAt`，**没有存这一轮的 baseline sha**，所以重启后无法回退；而且悬空 commit 没有任何引用，`git gc` 会把它回收掉。

**子代理** 完全不存在。Go 侧搜不到任何 spawn/task 派生能力；`frontend/src/panes/SubagentPane.vue` 与 `TasksPane.vue` 是空壳——只 `import { ref } from 'vue'` 与 `PaneHeader`，没有任何 API 调用或数据源。

**一个意外的好消息**：ctx 的管道基本已经铺好了。`ToolInterface.Execute(ctx, args)` 本身就带 ctx（`tools.go` 的接口定义），`guardedTool.Execute` 也已经把 ctx 传给了 `Enforce` 和 `inner.Execute`（`permission_app.go:45`），`Enforcer.Enforce(ctx, tool, args)` 也接受 ctx。真正缺的是"为每次运行创建一个**可取消**的 ctx，并让它真的被传递和尊重"。

## 三、P1-A 两级中断

### 3.1 目标行为

用户点第一次「停止」：**协作式**。与现有计划取消语义一致——当前正在跑的 LLM 请求与工具调用跑完，下一轮循环不再开始。适用于"我觉得它要跑偏了，但当前这步跑完再停没危险"。

用户再点一次（或在正在停止的状态下再点）：**硬取消**。取消运行 ctx，正在进行的 HTTP 请求立即断开、正在跑的子进程被 kill、正在等待的权限询问/提问立即以"取消"结束。适用于"它正在 `rm -rf` 或者正在往我不想让它改的地方写"。

两种取消都要留下可审计的痕迹：会话里保留已执行完的工具调用记录（已经持久化的不动），未完成的那一轮在会话里落一条标记，并且**本轮产生的文件改动照常归因成一个 diff turn**——这样即使硬取消留下了半成品文件，用户能立刻用 P2 的撤销把它回退掉。这一条是两级设计能安全落地的前提。

### 3.2 数据结构与注册表

现在只有计划用的 `planCancels map[string]chan struct{}`（`app.go` 的 `registerPlanCancel` / `unregisterPlanCancel`）。新设计把它抽成通用的运行控制，计划取消变成它的一个调用方而不是并行的一套。

```go
// runControl 一次运行的取消控制。普通聊天与计划执行共用。
type runControl struct {
	RunID    string
	SessionID string
	mu       sync.Mutex
	soft     chan struct{}                    // 关闭 = 请求协作式停止
	ctx      context.Context                  // 硬取消的根 ctx
	cancel   context.CancelCauseFunc
	kind     string                           // "" | soft | hard
	done     chan struct{}
}

// App 侧新增字段（把现有 planCancels/planMu 收编进来）
type App struct {
	// runs 活跃运行：runID -> 控制块
	runs map[string]*runControl
	// sessionRun 会话当前运行：sessionID -> runID（「停止我的会话」走这条）
	sessionRun map[string]string
	runMu      sync.Mutex
}
```

`runID` 用递增序号加时间戳生成，避免同一会话短时间内两次运行撞键。`sessionRun` 是索引而不是主键，这样同一个会话上如果出现并发运行（计划步骤执行 + 用户新开一条聊天）也能各自独立取消。

对外语义：`Cancel(soft)` 只关闭 `soft`；`Cancel(hard)` 先关闭 `soft`（让循环有机会优雅收尾），再 `cancel(cause)`。**硬取消不撤掉软取消信号**，否则中间件可能看到矛盾的信号。

### 3.3 ctx 贯穿：三个洞

盘点下来只有三处需要改，其余路径已经通了。

第一个洞是 **LLM 调用根本没拿 ctx**。`callLLMForModel(model, req)`（`anthropic.go:69`）与 `callLLM(url, token, req)`（`chat.go:119`）签名里就没有 ctx；`callLLMStreamForModel(ctx, ...)`（`anthropic.go:76`）虽然有形参，但 `chat.go:655` 的调用点传的是 `context.Background()`。改法：给 `callLLMForModel` / `callLLM` 补上 ctx 形参，把 `chat.go:655`、`chat.go:658` 换成运行的 ctx，并且 HTTP 请求改用 `http.NewRequestWithContext` 构造（现在用的是固定 120s 超时的 client，硬取消必须能立刻断开在途请求，不能等超时）。`app.go:192` 的 `TestModelConnection` 没有运行 ctx，传 `context.Background()` 即可。

第二个洞是 **`exec_shell` 忽略 ctx**。`CLITool.Execute`（`tools.go:58`、`tools.go:60`）用的是 `exec.Command`，虽然收到了 ctx 却没用——**这是硬取消杀不掉正在跑的 shell 命令的直接原因**。改成 `exec.CommandContext(ctx, ...)` 即可。有意思的是动态 CLI 工具（`toolruntime.go:70`、`toolruntime.go:72`）已经用了 `CommandContext`，所以这是个不一致造成的漏点，不是设计缺陷。

第三个洞是 **两个阻塞等待不含 ctx**。权限询问的 `permissionBroker`（`permission_app.go:136` 起的 `register` / `Wait`）与提问回路 `askBroker.Wait`（`ask.go:176`）都是 `chan` + `timer` 的 select，**没有 ctx 分支**。硬取消如果只 cancel 了运行 ctx，这两个等待会一直挂到超时（权限默认 5 分钟、提问默认 10 分钟），用户点了停止却要再等十分钟——等于没取消。改法：两个 `Wait` 增加 ctx 形参并加入 `case <-ctx.Done()`；同时在硬取消时**主动把它们按"取消/拒绝"结掉**（复用现有的 `permissionStore.cancel` / `askStore.skip` 那条路），两条路都留着，谁先到算谁。

顺带确认不需要改的：`diff.go:71`、`filetools.go:285`、`skillinstall.go:117` 已经用了 `CommandContext`；`toolruntime.go:233` 的 MCP stdio 服务进程故意用 `exec.Command`——那是**服务生命周期**，不应该跟着单次工具调用一起死，保持原样。

### 3.4 阻塞点解锁

硬取消时必须处理的三个"正在等人"的点：

一是权限询问。模型的工具调用正卡在 `Enforce` 里等用户点允许。硬取消要把这个请求以 `Decision: deny` 结掉，让 `Enforce` 返回，循环才能往下走并观察到取消。注意此时**不要**把这次拒绝写进审计当作真实拒绝，要标成 `cancelled`，否则审计日志会污染用户的决策记录。

二是 `ask_user`。同理，把挂起的问题按"用户未作答"结掉（`askStore.skip` 已有这条路径，前端「停止」现在就在调它）。

三是流式输出的残余分片。`chat.go:656` 的 `flushShutdown()` 负责把残余分片发完——硬取消路径上也要调它，否则前端会留着一个永远不结束的流式气泡。

### 3.5 对外接口与前端

```go
// StopChat 停止某会话当前的运行。hard=false 协作式，hard=true 硬取消。
// 返回是否真的命中了一个在跑的运行。
func (a *App) StopChat(sessionID string, hard bool) (bool, error)

// 计划取消改为委托，保持既有前端调用不变
func (a *App) CancelPlan(planID string) error
```

`ChatResult` 增加两个字段，让前端能区分"正常结束"与"被取消"：

```go
Cancelled  bool   `json:"cancelled"`
CancelKind string `json:"cancelKind,omitempty"` // soft | hard
```

前端 `stopGeneration`（`ChatPane.vue:361`）现在的分支顺序是先处理提问与授权、再处理计划、最后落到 `isGenerating=false`。改为：先按需结掉提问与授权（保持现状），然后**统一调 `StopChat(sessionId, hard)`**，其中 `hard` 由"是否已经在停止中"决定——即按钮在收到取消确认前保持"停止中"状态，此时再点就是硬取消。按钮的三态（运行中 / 停止中 / 已停止）本来就是 `chatStore.isGenerating` 加一个本地标志，不需要新的全局状态。

### 3.6 边界与失败模式

**半成品文件**是硬取消最需要正视的后果。杀在 `write_file` 写入一半、或杀在 `npm install` 中途，工作区就是不一致的。缓解手段就是我们已经在做的事：本轮仍归因成一个 diff turn，用户能一键回退（P2）。**不试图做事务性回滚**——对任意 shell 命令做事务性回滚是不可能的，承诺了反而危险。

**取消后消息序列的完整性**是第二风险。现在循环里是"先执行完所有工具，再持久化 assistant 消息与 tool 结果"（`chat.go:684` 起）。如果硬取消发生在工具执行中途，可能出现"assistant 消息含 3 个 tool_call，但只持久化了 1 个 tool 结果"的序列。这种断裂序列下次构建请求时会被 API 拒绝（tool_call 必须逐个配对）。设计上要求：**硬取消路径必须补齐缺失的 tool 结果**，内容是"（已取消）"，保证 `tool_calls` 与 `tool` 消息一一配对。这是实现里最容易漏、后果最严重的一点。

**取消与超时的区别**要保留：`callLLM` 自己 120s 超时导致的失败是 `Error`，不是 `Cancelled`。前端文案与后续动作都不同（超时可以让用户重试，取消不该自动重试）。

### 3.7 测试要点

关键是能在没有 UI 的情况下测。`StopChat` 的两个语义各一条：软取消要在"下一轮开始前"退出且已执行的工具记录完整；硬取消要在"一个长命令跑到一半"时把它杀掉（用 `exec_shell` 跑 `sleep 30`，断言在 1 秒内返回且 `Cancelled=true`）。第三条是序列完整性：硬取消后加载会话，断言每个 `tool_calls` 都有配对的 tool 消息。第四条是取消不污染审计：硬取消后审计里不该出现"用户拒绝"的记录。

## 四、P1-B 上下文管理（摘要式压缩）

### 4.1 目标行为

平时无感：估算用量没到阈值就什么都不做。接近阈值（建议按模型上下文窗口的 70%）时自动压缩：把早期对话交给模型压成一段结构化摘要，保留最近若干轮原文，然后继续这一轮对话——**对用户是透明的，不需要重开会话**。用户也可以用 `/compact` 手动触发（复用 P1 的 `/技能名` 前缀解析那套机制，`ParseSkillCommand` 已经给出了一个可参考的解析器形态）。

无上下文窗口信息可用时按保守默认值（如 32K）处理，并在设置里允许用户按模型配置窗口大小。

### 4.2 token 估算：锚点 + 增量

纯字符估算误差很大（中英混排能差一倍），而**模型 API 每次响应都会回真实用量**——这是免费的、精确的锚点。所以前置改造是给 `LLMResp` 补上 `usage` 解析（OpenAI 兼容路径的 `prompt_tokens` / `completion_tokens` 与 Anthropic 路径的等价字段）。这一步只有收益没有风险，且顺带解决了"用户看不到本次消耗"的体验问题。

有了锚点之后：每次拿到响应就记录真实的 `prompt_tokens` 作为该消息序列的准确长度；两次锚点之间新增的消息按字符估算增量。误差只在"最后一次锚点之后的那几条消息"上，不会累积。相比纯估算，这是数量级的改进。

新增一个小的估算器：

```go
// estimateTokens 字符级估算，仅用于两次真实锚点之间的增量。
// 中日韩字符按 1 token/字，其余按 4 字符/token 粗算，取两者之和。
func estimateTokens(text string) int
```

### 4.3 安全切点

压缩必须切在**合法边界**上，这是这一节最重要的约束。消息序列里 `assistant(tool_calls)` 与随后的 `tool` 结果是一组，被切开就会让下一次请求直接被 API 拒绝（tool 结果找不到对应的 call）。

所以切点只能落在"一条 user 消息之前"，且这条 user 消息不能是 tool 结果。实现上做成一个纯函数：从尾部往前找第 N 个满足条件的切点，返回索引；找不到合法切点就不压缩（宁可这次不压，也不能产生非法序列）。这个函数应当是可以单独测的。

### 4.4 摘要产物与存储

关键设计选择：**会话里保留完整原文，摘要只用在构建请求时**。理由是原文是用户资产——他可能想回看、想搜索、想回退；一旦压缩就丢掉原文，等于 agent 偷走了对话历史。所以：

- 会话文件继续存全量 `Messages`（不变）。
- 在 `Session` 上新增一个压缩状态，记录"前 N 条已被摘要覆盖"：

```go
// Session 新增字段
ContextSummary    string `json:"contextSummary,omitempty"`     // 覆盖部分的摘要
ContextCoveredUpTo int   `json:"contextCoveredUpTo,omitempty"` // 摘要覆盖到第几条消息（不含）
ContextSummaryAt  int64  `json:"contextSummaryAt,omitempty"`   // 摘要生成时间
```

构建请求时（`chat.go:604` 那段）：若 `ContextSummary` 非空且 `ContextCoveredUpTo > 0`，则把 `messages[0:ContextCoveredUpTo]` 换成一条带说明的 user/system 消息（"以下是此前对话的摘要……"），其余原样拼接。这样压缩行为完全可逆：把这三个字段清空就回到全量上下文。

### 4.5 触发与降级

触发点放在循环开头（`chat.go:629` 之后、构造请求之前），这样每轮都有机会压。自动触发条件是"`ContextCoveredUpTo` 之后的部分 + 系统提示 + 工具定义"估算超过阈值。

摘要本身要花一次 LLM 调用，所以有三条防线：摘要调用失败 → **降级为 `compactMessages` 的确定性截断**（已有实现，`plan.go:344`），保证这一轮一定能发出去；摘要结果为空或明显异常（比如比原文还长）→ 同样降级；API 返回 context 超长错误 → 强制压缩一次并重试**一次**，再失败就报错并把原始错误透出。

摘要 prompt 要求输出结构化几段（已完成的工作、当前状态、涉及的文件、待办与约束），而不是随便一段散文——后面这个摘要要承担"模型继续干活所需的全部背景"，散文摘要会丢关键状态。

### 4.6 单条工具结果预算

这一条独立于压缩，但属于同一个问题：一个 `read_file` 默认返回 2000 行（`filetools.go` 的 `defaultReadLimit`），`maxReadFileBytes` 是 2MB——**单个工具结果就能把窗口吃掉大半**。现有上限是"防止一次调用撑爆内存"，不是"控制上下文预算"。

建议在工具结果回填进 `messages` 之前加一道统一截断：单条工具结果超过阈值（如 24K 字符）就保留头尾、中间省略并明确标注"已省略 N 字符"，同时提示模型"需要更多内容请用 offset/limit 或 grep 定位"。截断必须**只影响发给模型的内容**，持久化仍存全量（与 4.4 同一个原则）。

### 4.7 边界

`ask_user` 与权限询问相关的消息不能被压掉——用户刚答的内容如果被摘要吃了，模型会接着问同一个问题。所以切点选择要把"最近一次用户交互"之后的部分无条件保留（这也符合"保留最近 N 轮"的策略，但要显式保证，不能依赖 N 的取值）。

计划执行路径现在传 `compact=true` 走确定性截断。新设计下两条路径应该统一走同一套上下文管理，`compact` 参数可以退化成"强制使用确定性截断"的调试开关，而不是两套并行逻辑。

### 4.8 测试要点

切点选择的正确性（构造含 `tool_calls` + `tool` 的序列，断言切点不落在组内）、估算器在纯中文/纯英文/混排下的量级、`ContextSummary` 生效后请求体里原文消失而摘要存在、摘要失败时降级为截断且请求仍能发出、以及"清空摘要字段即可回到全量上下文"这条可逆性。这些都不需要真实 LLM——摘要调用注入一个假实现即可。

## 五、P2 撤销（按归因精准回退）

### 5.1 可复用资产

三块现成的：`DiffService.Snapshot`（`diff.go:406`）用 `git stash create` 产出悬空 commit 且**不动工作区与暂存区**；每轮开头已经取好了本轮基线（`chat.go:600` 的 `turnBase`）；`TurnTouched` 给出了这一轮精准触碰过的路径集合。`DiffFile.Status`（`diff.go:38`）已经把每个文件标记成 `added / modified / deleted / renamed`，并且这份记录就存在 `DiffTurn.Files` 里——**回退算法应该直接读这份记录，而不是事后用 git 去猜当时的变更类型**。

### 5.2 先要补两个洞，否则撤销会时灵时不灵

**洞一：baseline 没有持久化。** `DiffTurn`（`diff.go:45`）只有 `Files/Additions/Deletions/CreatedAt`，没有存这一轮开始时的快照 sha。会话 JSON 里唯一存下来的是 `DiffBaseline`（会话级基线，`sessions.go`），那是**整个会话**的起点，不是某一轮的。所以重启后"回退到第 3 轮之前"无从下手。改法：`DiffTurn` 增加 `Base string json:"base,omitempty"`，写入时把 `turnBase` 一起存（`AppendDiff` 的签名要加一个参数）。

**洞二：悬空 commit 会被 gc 回收。** `git stash create` 产出的 commit **不在任何 ref 上**，`git gc` 或 `git prune` 之后它就是个不可达对象，随时可能被清掉。也就是说"今天能撤销、下周不能"这种间歇性故障一定会出现。改法：每轮快照生成后立刻用 `git update-ref` 把它钉住：

```
refs/local-agent/checkpoints/<sessionID>/<turn>  ->  <sha>
```

清理策略跟着会话走：删除会话时删掉它名下所有 checkpoint ref；同时保留最近 N 轮（建议 20），更早的 ref 删掉让 git 自然回收。

### 5.3 快照要覆盖未跟踪文件——这条不解决会有数据丢失

`git stash create` **默认不包含未跟踪文件**。由此产生一个不显眼但会丢数据的场景：某个文件在第 N 轮之前就已存在、但一直是未跟踪状态（新项目里很常见），这一轮 agent 改了它。改动记录里它会被标成 `added`（因为 `DiffScoped` 走的是"未跟踪文件按整文件新增"那条路，`buildAddedFile`，`diff.go:422`），而它**在快照里根本没有副本**。于是回退时按 `added` 处理 → 删掉这个文件 → **用户原本手写的文件没了，且无法恢复**。

所以不能直接复用 `TurnSnapshot`。设计上分成两种快照，各司其职：

- **用来算 diff 的**：保持 `Snapshot` 原样（`stash create`），不动。理由很直接——`DiffScoped` 已经围绕它做了一套未跟踪文件的补齐逻辑，改了它的形状会牵动整条 diff 链路，而那条链路是有测试且正在用的。不要为了新功能去动一个正在工作的机制。
- **用来做撤销的**：新增 `Checkpoint(dir, sessionID, turn)`，用 plumbing 造一个**完整**快照（含未跟踪文件），全程不碰真实索引与工作区：

```
GIT_INDEX_FILE=<临时索引> git add -A          # 只写临时索引
GIT_INDEX_FILE=<临时索引> git write-tree      # 得到 tree
git commit-tree <tree> -p HEAD                # 得到悬空 commit
git update-ref refs/local-agent/checkpoints/<sessionID>/<turn> <sha>
```

这样 `git restore --source=<sha> -- <path>` 对"修改/删除"一致可用，"新增"的判定也有据可依（文件在快照里找不到就真的是新增的）。

### 5.4 回退算法

输入：`sessionID` 与目标 `turn`。步骤：

第一步，取出该轮的 `Base`（新增字段）与 `Files`；`Base` 为空说明这轮没产生 diff（或是不支持回退的旧数据），直接返回"不可回退"。

第二步，**安全检查**——这是比算法本身更重要的一步。对每个待回退的路径，比对"该轮记录的内容"与"工作区当前内容"：如果当前内容与那一轮结束时不同，说明**用户在该轮之后又手工改过这个文件**。此时不能默默覆盖，要停下来把冲突清单交给用户，让他选择"跳过这些文件"还是"仍然覆盖"。实现上不需要存哈希：从 `Files[].Hunks` 可以重建该轮结束时的内容，或者更省事——用 `git diff <checkpointSHA> -- <path>` 与当前 `git diff` 的路径集合做交叉判断。

第三步，按 `Status` 分派（`renamed` 要按 `OldPath` 一并处理，否则会留下孤儿文件）：

| Status | 回退动作 |
| --- | --- |
| `added` | 删除文件；顺带清理因此变空的目录 |
| `modified` | `git restore --source=<base> -- <path>` |
| `deleted` | `git restore --source=<base> -- <path>`（快照里有，能恢复） |
| `renamed` | 先 `restore` 旧路径，再删掉新路径 |

第四步，无论成功失败都要**逐文件**记录结果并回给前端，部分失败不能当成整体失败（比如某个文件被别的进程占用）。失败的项保持原状，不留半截状态。

第五步，回退之后要刷新状态：`DiffService` 的归因集合里移除这些路径、重算累计 diff、`DiffTurn` 里把这一轮标记成"已回退"（而不是删除记录——用户可能想回退后重新执行，历史应当留痕）。前端 `diff:update` 事件已经在用，复用即可。

### 5.5 接口与 UI

```go
// CheckpointInfo 一轮可回退的信息（供前端决定按钮是否可点）
type CheckpointInfo struct {
	Turn      int      `json:"turn"`
	Label     string   `json:"label"`
	Available bool     `json:"available"` // 有 Base 且 checkpoint ref 还在
	Conflicts []string `json:"conflicts,omitempty"` // 该轮之后又被改过的文件
}

// ListCheckpoints 列出可回退的轮次
func (a *App) ListCheckpoints(sessionID string) ([]CheckpointInfo, error)

// UndoDiffTurn 回退某一轮；force=true 时忽略冲突照常覆盖
func (a *App) UndoDiffTurn(sessionID string, turn int, force bool) (*UndoResult, error)
```

UI 放在 `DiffPane` 的轮次列表上：每一轮右侧一个「回退到此处之前」按钮，不可回退时置灰并给出原因（"本轮无改动" / "非 git 仓库" / "快照已被清理"）。有冲突时先弹应用内确认框列出冲突文件（`ui.ask` 已有，注意带 `danger` 样式），用户确认后才带 `force=true` 重发。

### 5.6 边界

非 git 仓库（`IsRepo` 为假）直接不支持并在 UI 说明——本项目的 diff 能力本身就建立在 git 上，不必为撤销单独造一套文件快照。二进制文件与超过 `maxDiffFileBytes`（1MB）的文件在 `DiffFile` 里本来就跳过了，所以它们**不在回退范围内**，这一点必须在 UI 上写清楚，不能让用户以为"点了回退就全都回去了"。硬取消（P1）留下的半成品同样走这条路回退，两条功能在这里会师。

### 5.7 测试要点

用真实的临时 git 仓库做集成测试，这是少见的"沙箱里也能真验"的部分（`git` 命令可用）：构造"新增 + 修改 + 删除 + 重命名"的混合轮次，回退后断言工作区与快照一致；断言未跟踪文件被正确删除（5.3 那个坑的回归测试）；断言"该轮之后手工改过的文件"被识别为冲突而不是被默默覆盖；断言 checkpoint ref 在 `git gc --prune=now` 之后仍然可解析（洞二的回归测试——这条不写测试就一定会复发）。

## 六、P3 子代理（完整能力型）

### 6.1 目标行为与不变量

主会话通过工具 `spawn_agent` 派生一个子代理，给它一个任务描述；子代理有**自己的消息序列与自己的上下文**，共享同一个工作区，可以读写文件、执行命令；它跑完后把**结构化结论**回给主会话，中间过程不进入主上下文。子代理运行期间在 `SubagentPane` 里可见。

三条不变量，实现时任何一条破了都会出大问题：

一是**不允许嵌套派生**。子代理的工具集里没有 `spawn_agent`。否则一个跑飞的模型能派生无限层，成本与并发都不可控。

二是**子代理之间不共享上下文，只共享工作区**。这是它存在的意义——把"摸清这个模块"这类会产生几十万 token 中间过程的活隔离出去，主上下文只收一份结论。

三是**子代理不能阻塞等人**。它没有 UI 通道，任何需要用户交互的操作（权限询问、`ask_user`）都必须**立即失败**而不是等待，否则会挂满权限询问默认的 5 分钟（`permission_app.go:127`）或提问默认的 10 分钟（`ask.go:87`）。

### 6.2 执行核心参数化

现在的 `runToolLoop`（`chat.go:577`）深度绑定 `App`：它自己去取会话、自己发射事件、自己算 diff、自己持久化。子代理需要的是同一个循环但换掉一批依赖。所以第一步是把"一次运行"的依赖收成一个结构，让主会话与子代理走同一条代码路径：

```go
// agentRun 一次运行的完整依赖。主会话与子代理共用同一个循环实现。
type agentRun struct {
	RunID        string
	SessionID    string        // 主会话 ID；子代理用主会话 ID（便于权限与 diff 归因）
	WorkspaceKey string        // 归因用的键：主会话=sessionID，子代理="<sessionID>#sub-<runID>"
	Messages     []Message     // 本次运行的起始消息（子代理是独立的空序列 + 任务描述）
	SystemPrompt string
	Model        *Model
	ToolView     *SessionView
	Ctx          context.Context
	Recorder     runRecorder   // 事件与持久化的抽象：主会话写会话文件，子代理写自己的文件
	Compact      bool
	MaxTurns     int
}
```

`runRecorder` 是把"事件发射 + 消息持久化"抽出来的接口，主会话实现写 `SessionStore`，子代理实现写自己的文件并发到自己那条事件通道。这一步是 P3 里最主要的重构量，也是它必须排在 P1 之后的原因——P1 的 ctx 与 runControl 正好就是这个结构需要的那部分。

### 6.3 工具视图裁剪

子代理的工具视图由父会话装配好之后**减去两个工具**：`spawn_agent`（禁止嵌套）与 `ask_user`（不可交互）。其余照旧（决策是完整能力型，不额外限制写与执行）。

工作目录、会话工具白名单、技能白名单都继承父会话，这样"我在这个会话里只开了这些工具"的意图对子代理同样生效。

### 6.4 权限：不可交互的网关

新增一个包装器，套在父会话的 enforcer 外面：

```go
// subagentEnforcer 子代理的权限网关：允许就放行，需要询问的一律拒绝。
// 不能就地等待——子代理没有 UI 通道，等待只会挂到超时。
type subagentEnforcer struct {
	parent Enforcer
	declined []DeclineNote // 收集"本可询问但被拒"的操作，随结果回给主会话
}
```

判定分三种，必须区别对待：父网关判定 **allow** → 放行；父网关判定 **deny**（内置危险命令、用户显式规则）→ 拒绝，理由如实回给模型；父网关判定 **ask** → 拒绝，但**理由要说清"这需要用户授权，请在结论里说明"**，并且把这条记进 `declined`，最终浮到主会话让用户看到"子代理想做的事被卡在授权上"。

这个"把询问降级成带理由的拒绝"是关键设计：既保住了不阻塞，又不会让子代理的活动变成黑箱——用户能看到它想干什么、被什么挡住了。

### 6.5 并行与写冲突

多个子代理可以并行跑。写冲突**不加锁**，理由是加锁会引入排队与死锁，而收益有限（用户能看到谁改了什么）。改为事后检测：`agentRun.WorkspaceKey` 让每个子代理的改动独立归因，子代理结束时对比各方的文件触碰集合，若有交集就在主会话里发一条警告，并提示可以分别回退。这比"悄悄互相覆盖、事后难以定位"要好，也比加锁简单得多。

代价要诚实说明：两个子代理同时改同一个文件仍可能产生后写的覆盖先写的。所以系统提示里要引导模型"派生子代理时尽量避免让它们改同一批文件"。

### 6.6 结果回传契约

```go
type SubagentResult struct {
	RunID     string   `json:"runId"`
	Status    string   `json:"status"`  // completed | failed | cancelled | limit
	Summary   string   `json:"summary"` // 子代理产出的结论（唯一进入主上下文的部分）
	Files     []string `json:"files"`   // 它改过的文件
	ToolCalls int      `json:"toolCalls"`
	Declined  []string `json:"declined,omitempty"` // 被权限挡下的操作
	Error     string   `json:"error,omitempty"`
}
```

`spawn_agent` 工具的返回值就是这个结构序列化后的文本。注意 `Summary` 要**限制长度**（比如 4K 字符），否则一个话多的子代理会把主上下文又撑起来，这个功能的意义就没了。

### 6.7 取消级联与上限

取消必须级联：父运行硬取消时，子代理的 ctx 是父 ctx 的子节点，自动一起取消；软取消时子代理的循环也会在下一轮观察到信号。`runControl`（3.2）在这里要支持一个 ctx 树，而不是单层。

上限是硬要求，缺了任何一条都会出现不可控的账单：单次运行的最大并发子代理数（建议 4）、总派生数（建议 16）、单个子代理的最大轮次（复用 `MaxChatTurns`，建议更小如 20）。超限时 `spawn_agent` 直接返回错误并说明原因，让模型自己收敛。

### 6.8 存储与 UI

子代理的消息写**独立文件**（`~/.local-agent/subagents/<runID>.json`），不进主会话文件。理由：子代理的中间过程可能有几百条消息，塞进会话文件会让会话加载变慢，而且主会话根本不需要它们。主会话只保留 `SubagentResult`。

UI 上有个容易混淆的地方需要澄清：`SubagentPane` 放**子代理**（运行中/已完成的派生任务，可展开看它的消息流），`TasksPane` 更适合放**主会话的待办清单**——那是另一个能力（todo 工具），不要和子代理混在一个面板里。这两个空壳文件现在都存在，正好各归其位。

### 6.9 测试要点

权限降级是重点：注入一个"总是 ask"的假 enforcer，断言子代理拿到的是拒绝而不是挂住，并且 `Declined` 里有记录。其次是禁止嵌套：断言子代理的工具清单里没有 `spawn_agent` 与 `ask_user`。第三是取消级联：父硬取消后子代理在 1 秒内结束。第四是并发上限与总数上限。第五是**上下文隔离**：跑完一个子代理后断言主会话新增的消息里没有子代理的中间过程（这是这个功能存在的理由，必须测）。

## 七、风险、依赖与验证边界

**依赖关系**：P1-A 是 P3 的前置（子代理要靠 ctx 树做级联取消），P1-B 与 P2 相互独立，但 P2 的"回退硬取消留下的半成品"依赖 P1-A 成立。所以顺序建议是 P1-A → P1-B → P2 → P3；如果只做一件，做 P1-A。

**P1-A 的主要风险**是硬取消打断了消息序列。设计里已经要求"补齐缺失的 tool 结果"，实现时这是最容易漏的一步，而且漏了之后的表现是**下一次请求被 API 拒绝**（不是当次报错），排查成本高。建议在循环里加一个不变式断言：任何返回路径上，`tool_calls` 与 `tool` 消息数量必须配对。

**P1-B 的主要风险**是摘要丢信息导致长任务后期"失忆"。缓解是明确的保留策略（最近 N 轮原文不动 + 用户交互之后的部分无条件保留）加上"原文始终在会话里、清空摘要字段即可回退到全量"这条可逆性。**不要**做有损且不可逆的压缩。

**P2 的主要风险**是 5.3 那个未跟踪文件被误删的场景，以及 5.2 洞二的 checkpoint 被 gc。两条都有对应的回归测试，必须写。

**P3 的主要风险**是并发写同一文件与上限失控。前者靠提示引导 + 事后检测（接受残留风险并让用户可见），后者靠硬上限。

**验证边界（重要）**：本方案是对着当前工作区代码读出来的设计，所有 `文件:行` 引用都是读码所得，但**方案本身未实现、未编译、未运行验证**（开发沙箱没有 Go 工具链）。实现阶段有两处需要在目标环境先确认一次：`git stash create` 确实不含未跟踪文件（5.3 的前提，用 `git stash create` 后 `git cat-file -p <sha>:<一个未跟踪文件>` 验证）、以及 plumbing 快照命令（`GIT_INDEX_FILE` + `add -A` + `write-tree` + `commit-tree`）在目标 git 版本上的行为一致。

一个意外的好消息：P2 是这四条里**唯一能在当前沙箱里真跑测试**的部分——它依赖的是 git 命令而不是 Go 工具链，只要有 git 就能把回退算法验证到位。

