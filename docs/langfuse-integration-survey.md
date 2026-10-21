# Langfuse 接入调研（local-agent）

> 状态:**调研结论,尚未实现代码。**
> 需求:把 local-agent 的模型调用与工具调用导出到 Langfuse,以获得跨会话的 LLM 可观测性。
>
> 已定前提(用户 2026-10-22 确认):
> 1. **自建部署**(本地 docker compose,数据不出内网)
> 2. **采集含正文,但加开关、默认关**
> 3. **先只认 `~/.local-agent/tracing.json`,不做设置界面**
>
> 材料来源:本项目代码为「已核实」(标注了文件:行号);Langfuse / OTLP 相关为 2026-10-22 查公开资料,标注「据官方文档」。

---

## 0. 结论先行

1. **能接,而且是低成本接入。** 本项目目前**零**可观测性基础设施(全仓 grep `langfuse|opentelemetry|otel|tracing` 只命中 `docs/role-workflow-survey.md` 的正文提及;`go.mod` 无任何相关依赖),所以这不是改造既有埋点,而是**新增一条旁路导出**。

2. **接入点极好——只有 3 个钩子**,因为模型收发是单点收口的:
   - 模型调用:`internal/app/chat.go:857`(构造 `llm.LLMReq`)→ `:877`(流式)/ `:880`(非流式)→ `:963`(`noteUsage`)
   - 工具调用:`internal/app/chat.go:1384` `runOneTool`
   - trace 根:`internal/app/chat.go:692` `runToolLoop` 的起止
   
   子代理复用同一个 `runToolLoop`(`internal/app/subagent.go`),因此**子代理的嵌套 trace 天然覆盖**,不需要为子代理单独埋点。

3. **不建议引入官方 Go SDK。** 自写 OTLP/JSON exporter 约 300–400 行标准库代码即可;引 `go.opentelemetry.io/otel` + `otlptracehttp` 会带来 5 个新模块,并占用进程级全局 `TracerProvider`——这与本项目 `go.mod` 只列 5 个直接依赖的克制风格不符。

4. **最大的风险不是接入,而是自建部署本身。** Langfuse v3+ 是 ClickHouse + Postgres + Redis + MinIO 四件套,不是单容器;且社区有一个**「OTLP 返回 200 但数据不落库」**的未结 issue(见 §6)。这决定验收标准必须是「在 Langfuse UI 里看到 trace」,而不是「HTTP 返回 200」。

---

## 1. 现状盘点(已核实)

### 1.1 已有的排障能力都是「本地内存态」,不外发

| 组件 | 位置 | 形态 |
|---|---|---|
| 请求快照 | `internal/llm/requestlog.go` `RequestSnapshot` / `RequestLog` | 每会话**最近一次**真实发出的请求,仅内存,含 `RedactMessages` 图片脱敏 |
| 用量明细 | `internal/app/agentrun.go:95` `noteUsage` | 内存明细 + 累加落盘 + 推送前端三件事 |
| 用量累计 | `internal/store/usage.go` | 落盘到会话 JSON,含缓存命中判定 `CacheViewOf` |
| 上下文用量 | `agent.ContextStatOf` / `TokenAnchor` | 估算 + 真实锚点校准 |

**这些东西是派生的金矿**:接入 Langfuse 时几乎不需要新算任何指标——`llm.LLMUsage` 已经归一化了 cache_read / cache_write / reasoning(`internal/llm/llmtypes.go:208` 的 `LLMUsage`,含一条「PromptTokens 恒为输入总量」的不变式注释),`store.ToolCall` 已有 `Duration` / `Status` / `Files` / `Images`(`internal/app/chat.go:1390`)。

### 1.2 可复用的一处关键细节

`llm.RedactMessages`(`internal/llm/requestlog.go:99`)会把图片换成「图片已省略:1024×768,image/png,120 KB」的占位文本。**接入 Langfuse 时直接复用它**,绝不能把 base64 推上去——一张图 base64 后是 MB 级,既撑爆请求也毫无排障价值。

---

## 2. 协议事实(据官方文档,2026-10-22)

### 2.1 端点与鉴权

- 端点:`{base}/api/public/otel/v1/traces`
  - 自建:`http://localhost:3000/api/public/otel/v1/traces`(OTLP 端点需 Langfuse ≥ v3.22.0;当前稳定线是 v4)
  - 云版(仅作参考,本项目不用):`https://cloud.langfuse.com/...`(EU)/ `us.` / `jp.` / `hipaa.`
- 鉴权:`Authorization: Basic base64("<publicKey>:<secretKey>")`,即 `pk-lf-…:sk-lf-…`
- **必须带 `x-langfuse-ingestion-version: 4`**,否则直接 OTLP 数据**延迟最多 10 分钟**才可见(据官方文档)。这一条极容易被当成「没接上」——响应 200、UI 空的头号原因。
- **只支持 OTLP over HTTP(JSON 或 protobuf),gRPC 不支持。** 所以别考虑 `otlptracegrpc`。
- `POST /api/public/ingestion` 是 v4 已废弃的旧批量接口,不要走;自建 v4 默认写模式下它对 trace/span 事件直接返回 400。

### 2.2 OTLP/JSON 编码的三条硬约束(据 OTLP 规范原文)

这三条照 OTLP 规范原文核实过,写错会静默解析失败:

1. **`traceId` / `spanId` 是十六进制字符串**(大小写不敏感),**不是** base64——这是对 Protobuf 标准 JSON 映射的刻意偏离。
2. **枚举字段必须是整数值,枚举名字符串 MUST NOT 使用。** 所以 `span.kind` 要写 `1`(SPAN_KIND_INTERNAL)而不是 `"SPAN_KIND_INTERNAL"`;`status.code` 要写 `2` 而不是 `"STATUS_CODE_ERROR"`。这一条最坑,因为 Protobuf 标准 JSON 映射是允许枚举名的。
3. **对象键名是 lowerCamelCase**(`startTimeUnixNano`、`droppedAttributesCount`),`snake_case` 是非法的。
4. 64 位整数(`uint64` 等)按 Protobuf 规范编码为**十进制字符串**,但解码时数字也接受——**发送侧建议一律用字符串**,规避各实现差异。
5. `Content-Type: application/json`。

### 2.3 Langfuse v4 的属性映射(决定 UI 里能不能筛/聚合)

v4 是 **observations-first** 模型,与 v3 差别很大:

- **trace 级 `input`/`output` 已废弃**,要写在**根 span** 的 `langfuse.observation.input` / `.output`。旧属性 `langfuse.trace.input` 只为兼容旧评估器保留。
- 根 span 必须发,否则 trace 建不出来。
- `langfuse.session.id`(或 `session.id`):填本项目的 `sessionID`,**每个 span 都要带**。只挂在根 span 上不行——v4 的筛选与聚合主要作用于单条 observation 而非 trace。
- `langfuse.observation.type`:本方案用 `generation`(模型调用)/ `tool`(工具调用)/ `span`(根)。合法值:`span|generation|event|embedding|agent|tool|chain|retriever|guardrail|evaluator`。写了非法值不会被拒,但会回落到按 `gen_ai.operation.name` 推断、再回落到 `span`——表现为「类型不对但没报错」。
- 模型/token:`langfuse.observation.model.name` 与 `langfuse.observation.usage_details`(JSON 字符串)。
- **只有 `langfuse.trace.metadata.*` / `langfuse.observation.metadata.*` 前缀的 metadata 是可筛选的**;普通 attr 会掉进 `metadata.attributes` 兜底桶,UI 里查不到。

### 2.4 Langfuse 到底记什么、工具调用怎么记(据官方文档 2026-10-22)

**结论:工具调用是一等公民(`tool` 类型),但 Langfuse 只给 `generation` 开专属字段——`tool` 的全部内容都靠通用字段承载。**

#### 三层结构

`observation`(≈ OTEL span,单次步骤)→ `trace`(一次请求的全部 observation,按 trace_id 聚合)→ `session`(多次 trace 按会话聚合)。

#### ① 任何 observation 都能带的字段

| 字段 | 说明 |
|---|---|
| `name` | 名称。根 span 的名字还会成为 trace 名 |
| `type` | `span / generation / event / embedding / agent / tool / chain / retriever / guardrail / evaluator` |
| `input` / `output` | 该步骤的输入输出(JSON 字符串) |
| `level` | 严重级别 |
| `statusMessage` | 状态说明,错误一般放这里 |
| `metadata` | 任意键值;只有 `langfuse.observation.metadata.*` 前缀可筛选 |
| `version` / `environment` | 版本与环境 |

trace 级属性(`userId`、`sessionId`、`tags`、`release`、`public`、`metadata`)由 SDK 自动复制到**每条** observation 上——这也是为什么 §2.3 强调「只挂根 span 不生效」。

#### ② 只有 `generation` 有专属字段

| 字段 | 说明 |
|---|---|
| `model` | 模型名。**从任意 model 属性推断出 `generation` 只是兜底**,显式 type 优先 |
| `modelParameters` | 温度、max_tokens 等调用参数 |
| `usage` | token 用量,`usage_details` 的键必须与模型定义里的价格键**完全一致** |
| `cost` | USD 成本。未直接给出时由「model 名 → 模型定义(regex 匹配)→ 每单位价格」推断 |
| `prompt` | 关联 Langfuse 里托管的提示词版本 |
| `completionStartTime` | 首 token 时间(流式场景下等于 TTFT 的起点) |

#### ③ 工具调用具体记成什么

- `type: "tool"`,含义是「做了一件事的单个动作,如函数或 API 调用」。
- `input` 落 `langfuse.observation.input`;`output` 落 `langfuse.observation.output`。
- **若走 OTEL GenAI 语义约定**,Langfuse 也认 `gen_ai.tool.call.arguments` / `gen_ai.tool.call.result`,但这只是「没有别的 input/output 属性时的兜底」——本方案直接写 `langfuse.observation.*` 更可控。
- 耗时:由 span 的起止时间体现,无需额外字段。
- 父子嵌套(工具挂在触发它的那次 generation 下)会让 trace 呈树状;更重要的是——**trace 中出现除 `span`/`event`/`generation` 之外的任何类型(`tool`、`agent`、`chain` 等),Langfuse 就判定为 agentic,自动给出 Agent Graph 视图**,有「聚合 / 展开」两种模式。

#### ④ 映射到本项目(字段几乎都是现成的)

| Langfuse | 本项目来源 |
|---|---|
| `session.id` | `sessionID`(`agentRun.SessionID`) |
| generation `model` | `store.ModelCallID(model)`(`internal/store/models.go:270`) |
| generation `usage_details` | `llm.LLMUsage` 已归一化的 `PromptTokens` / `CompletionTokens` / `CacheRead` / `CacheWrite` / `ReasoningTokens`(`internal/llm/llmtypes.go:208`) |
| generation `modelParameters` | `LLMReq.Temperature` / `Thinking`(`budget_tokens`) |
| tool `name` | `store.ToolCall.Name`(`internal/app/chat.go:1390`) |
| tool `input` | `store.ToolCall.Args` |
| tool 耗时 | `store.ToolCall.Duration` |
| tool `metadata.*` | `Status` / `Files` / `Images` |
| tool `level` + `statusMessage` | `Status`("running" 之外的状态映射级别,失败放 statusMessage) |

#### ⑤ 三个本项目特有的坑

1. **工具结果必须先截断再上报。** 本项目的工具会返回终端输出、整份文件内容、`web_search` 的整页正文——原样塞进 `output` 会让单条 span 达到几十 KB 到 MB 级。建议截断到 2–4 KB 并在末尾标注「已截断,原长 N 字节」。
2. **成本算不出来是正常的。** 你在用内网网关(`ada-cli-go.ctripcorp.com`)+ 自定义模型名,Langfuse 内置价格表里没有,`cost` 会是空。要算成本得在 **Project Settings > Models** 里逐个建 model definition(regex 匹配模型名 + 每种 token 类型的单价),且 `usage_details` 的键名必须与之逐字一致。**首版建议直接放弃 cost**,token 量本身已经足够用。
3. **根 span 用 `agent` 而不是 `span`。** 根用 `span` 就只是普通 span;用 `agent`(语义是「决定应用流程、可借助 LLM 使用工具」)既符合本项目的定位,又能保证 Graph 视图出现在这个 agent 循环上。



```
runToolLoop (根 observation, type=agent)
├── turn 0: generation (type=generation, model, usage, input=messages, output=completion)
│   ├── tool (type=tool, name=read_file, input=args, output=结果摘要, duration)
│   └── tool (type=tool, name=web_search, ...)
├── turn 1: generation ...
└── ...
```

`sessionID` → `langfuse.session.id`(同会话多轮聚合);`RunID` 与 `turn` → observation metadata,便于排序。

**为什么根用 `agent`**:`tool` 的存在已经足以触发 Agent Graph;根再用 `agent` 能让这个循环在 Graph 里显式呈现为「agent 节点 + 其下的工具节点」,而不是一个匿名的通用 span。

---

## 3. 落地方案

### 3.1 新增包

```
internal/observability/
  langfuse.go   // Exporter:Config + 攒 span + POST /v1/traces,异步发送,失败只打日志
  tracecfg.go   // ~/.local-agent/tracing.json 的读写
  langfuse_test.go
```

`tracecfg.go` **照抄 `internal/store/contextprefs.go` 的失败方向**:文件不存在 / 解析失败一律回落到「关闭」,绝不因为一个坏配置把系统推向更外发数据的一侧。这与 `store/usage.go:278` `NewPromptCachePrefsStore` 的注释是同一条原则。

配置形状(建议):

```json
{
  "enabled": false,
  "endpoint": "http://localhost:3000/api/public/otel/v1/traces",
  "publicKey": "pk-lf-...",
  "secretKey": "sk-lf-...",
  "captureContent": false,
  "sampleRate": 1.0
}
```

- `enabled` 默认 `false`——与 `prompt-cache.json` 的默认关闭一致。
- `captureContent` 默认 `false`:关时只发元数据(模型、token、耗时、状态、工具名),**不发 messages / 系统提示词**;开时才发正文(仍走 `RedactMessages` 脱敏图片)。

### 3.2 接线(6 处,全部 nil-safe)

| # | 位置 | 改动 |
|---|---|---|
| 1 | `internal/app/app.go`(~187 附近,`promptCachePrefs` 旁边) | 新增 `tracer *observability.Exporter` 字段 + 初始化 |
| 2 | `internal/app/agentrun.go:29` `agentRun` | 加 `Tracer *observability.Exporter` 字段 |
| 3 | `internal/app/chat.go:692` `runToolLoop` | 起:开根 span;defer 结束 |
| 4 | `internal/app/chat.go:877` / `:880` | 包一层计时,拿到 `resp` 后落 generation span |
| 5 | `internal/app/chat.go:963` `noteUsage` 处 | 把 `resp.Usage` 直接喂给 generation span(已有归一化字段,无需重算) |
| 6 | `internal/app/chat.go:1384` `runOneTool` | 开 tool span,`rec.Duration`/`Status`/`Files` 直接落到属性 |

**两条硬约束**(与 `noteUsage` 的注释同源):

- **旁路**:任何一步失败只打日志,绝不能让这一轮对话失败。回复已经拿到了,为遥测去报错是本末倒置。
- **nil 安全**:`Tracer` 为 nil 时全部跳过。测试里会直接构造零值 `agentRun`,那一路径不该 panic——`ReqLog` / `UsageLog` 已经是这个模式,照抄即可。

### 3.3 生命周期

`main.go:31` 只有 `OnStartup`,**没有 `OnShutdown`/`OnBeforeClose` 钩子**(已核实)。三条路:

- **推荐**:让 exporter 在每次 flush 后自行关闭连接,不在进程级持有资源,不需要改 `main.go`。
- 或加 `OnShutdown: a.Shutdown`(会引入 `ctx` + `context.WithTimeout` 的等待,press 关闭时可能卡住最长达超时值)。
- **不建议**做「攒一批等进程退出一起发」——窗口关闭时未必有优雅退出,数据会丢。

### 3.4 工作量

| 项 | 估计 |
|---|---|
| `langfuse.go` exporter + 单测 | 0.5–1 天 |
| 6 处接线 + 单测 | 0.5 天 |
| 自建 Langfuse 起来并验收 | 0.5 天(取决于镜像拉取与机器) |

---

## 4. 自建部署的实际门槛(据官方文档)

- **不是单容器**:`langfuse-web` + `langfuse-worker` + ClickHouse + Postgres + Redis + MinIO,共 6 个。官方提供 `docker-compose.yml` 直接 `docker compose up`。
- 一把梭的验证命令(在宿主机上):
  ```bash
  curl -s -o /dev/null -w '%{http_code}\n' \
    -H "Authorization: Basic $(printf 'pk-lf-xxx:sk-lf-yyy' | base64)" \
    -H 'x-langfuse-ingestion-version: 4' \
    -H 'Content-Type: application/json' \
    -d '{"resourceSpans":[]}' \
    http://localhost:3000/api/public/otel/v1/traces
  ```
  返回 200 说明鉴权、端点、header 都通了(空 body 只验通,不落数据)。

---

## 5. 与「不做 UI」的一致性检查

用户选择先只认 `tracing.json`。**这有一个可见的后果需要在实现时处理**:如果配置错了(端点写错、key 过期),用户唯一的线索是启动日志。建议在应用启动时**打一行**:

```
[tracing] 已启用：POST http://localhost:3000/api/public/otel/v1/traces（含正文）/ 仅元数据
```

并在首次导出失败时打一次显式警告(带 HTTP 状态码与响应体前 200 字节),而不是静默吞掉。这一条比什么都重要——它决定配置错误是「30 秒发现」还是「半天发现」。

---

## 6. 已知风险(必须写进验收标准)

1. **社区未结 issue:自建 v4 上 OTLP 返回 200 但数据不落库**(langfuse#19003,2026-10-19 提,至今 open)。同实例上官方 SDK 走 `/api/public/ingestion` 能正常落库,只有 OTLP 路径不生效。维护者给的排查方向是三类环境变量:
   - `QUEUE_CONSUMER_OTEL_INGESTION_QUEUE_IS_ENABLED` / `..._SECONDARY_QUEUE_IS_ENABLED` 是否被覆盖成 false
   - `LANGFUSE_OTEL_INGESTION_QUEUE_SHARD_COUNT` 在 web 与 worker 是否一致(不一致会让消息停在 worker 不消费的队列上)
   - `LANGFUSE_MIGRATION_V4_WRITE_MODE`(默认 `events_only`)与 `LANGFUSE_MIGRATION_V4_NATIVE_OTEL_BEHAVIOUR`(默认 `direct`)——**在默认值下 `traces`/`observations` 旧表是空的属正常,数据应在 `events_full`**
   
   → **验收标准必须是「Langfuse UI 里能看到 trace」,而不是「HTTP 200」或「容器 healthy」。** 顺带:排查时别去 `traces` 表数行数,会误判。
2. **`x-langfuse-ingestion-version: 4` 漏了会延迟 10 分钟**——UI 空但数据最终会来,极易误判为接不通。
3. **v3→v4 属性语义变化**(trace input/output 废弃、属性传播到每个 span)。若照 v3 时代的教程写,会出现「trace 有了但 input/output 是空的」。
4. **合规**:已选自建,对话正文不出内网。但注意 `captureContent` 打开后,系统提示词(含长期记忆召回内容、`AGENTS.md` 内容)会一并入库——这些是本机文件的副本,应当视为**工作区内容的二次留存**。

---

## 7. 备选方案(如果自建太重)

如果最后觉得 6 个容器太重,按「成本递增」有三条退路:

1. **只发 metadata 到一个自建的极简 OTLP collector**(otelcol-contrib 单容器 + 文件输出)。失去 Langfuse 的 UI 与聚合,但拿到原始 trace JSON。
2. **不做远端,先把已有的 `RequestSnapshot` 扩成「历史 N 次」并落盘**。零新增依赖,但只能单机看,无跨会话聚合——这是最低成本、最高确定性的一档。
3. **用现成的 `/api/public/ingestion` 而非 OTLP**。这也**不推荐**:该接口在 v4 已是废弃路径,且自建 v4 默认写模式下对 trace/span 事件返回 400。

---

## 8. 尚未确定、实现前需要拍板的点

1. **`captureContent` 的粒度**:是「整份 messages」还是「只发最后一条 user + 模型回复」?后者能覆盖 90% 排障需求,数据量小一个数量级。
2. **抽样**:`sampleRate` 是否需要?本地单用户场景可能不需要,但留字段更省事。
3. **失败重试**:OTLP 规范定义了可重试状态码(429/502/503/504)。要不要做指数退避?**建议首版不做**——遥测丢了就丢了,不值得为它拖住对话线程;只要打日志即可。
4. **是否复用 `RequestSnapshot`**:它已经是「真实发出的那一份」的权威快照,generation span 的 input 可以直接用它,省掉一次装配。这是本方案里最省事的一处复用,但要注意它只在 `sessionID` 非空且有记录时存在。
