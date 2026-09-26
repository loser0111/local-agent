# Prompt Cache 设计方案

> 目标:让同一会话的连续轮次复用上一轮已计算的前缀,把「系统提示词 + 工具定义 + 历史」这部分从每轮全价重算,降到按命中价计价。同时补齐 token 用量统计,并在前端可见。
>
> 本文覆盖传输层、提示词装配、用量统计与前端展示四部分。**不改变任何对话语义**;缓存失效时系统行为与今天完全一致。

---

## 0. 结论摘要

- 本项目支持两条协议路径(`isAnthropicEndpoint` 分岔),而**它们是两套完全不同的缓存机制**:Anthropic 是显式断点,OpenAI 系是自动前缀匹配。分开处理,不要互相套用。
- **Anthropic 侧**:改动集中在 `toAnthropicRequest` 一个函数加三个结构体,流式与非流式共用这条路径。核心是把 system 从 `string` 拆成「稳定段 + 动态段」,并在三段稳定内容末尾各打一个断点。
- **OpenAI 侧**:**一个字段都不用加**。它自动生效,但要求「从提示词开头算起的最长公共前缀」稳定。现在的装配方式(`buildLLMMessages` 把系统提示词放在消息数组第一条,而它含每轮变化的 L2 召回)**恰好把变量放在了第一个位置**,这是对它最坏的位置。所以要改的是**结构**,不是请求体。
- 两条路径的共同约束只有一句话:**每轮变化的内容必须严格待在最末尾**。
- 最容易翻车的不是断点,而是**前缀稳定性**:任何对已发出过的内容的非幂等变换,都会让缓存失效且**没有任何报错**。
- **先做统计、再开缓存**:没有命中率可观测,就无法区分「没生效」「结构不对」「网关不支持」。这与本项目历史上几次静默失败是同一种形状。
- ⚠️ **一个必须先修的既有缺陷**:`anthropic.go:391/647` 把 `usage.input_tokens` 直接映射成 `PromptTokens`,而 Anthropic 的 `input_tokens` **不含**缓存读写部分。一旦开启缓存,这个锚点会严重偏小 → 上下文占比被低估 → 压缩阈值 70% 该触发时不触发 → 撞 API 超限。详见 §5.6。

---

## 1. 现状(代码事实,均已核对)

| 位置 | 现状 |
|---|---|
| `anthropic.go:89` `anthropicReq.System` | **`string`** —— 无法承载 `cache_control`(Anthropic 要求 system 为 block 数组) |
| `anthropic.go:104` `anthropicTool` | 无 `cache_control` 字段 |
| `anthropic.go:112` `anthropicContentBlock` | 无 `cache_control` 字段 |
| `anthropic.go:172` `toAnthropicRequest` | 把多条 system 消息 `strings.Join(systemParts, "\n\n")` 合成一个字符串 |
| `anthropic.go:300` `toAnthropicTools` | 每轮全量转换工具 schema,无断点 |
| `anthropic.go:157` `anthropicUsage` | 只有 `input_tokens` / `output_tokens`,**无 cache 字段** |
| `anthropic.go:391` / `:647` | `PromptTokens: r.Usage.InputTokens` —— **语义陷阱**,见 §5.6 |
| `anthropic.go:24` | `anthropicDefaultMaxTokens = 8192` 对所有模型硬编码 |
| `chat.go:746` `buildLLMMessages` | **系统提示词作为 messages[0]** —— OpenAI 侧失效的根因 |
| `chat.go:160` `LLMReq` | 无 `MaxTokens` 字段,OpenAI 路径**完全不发**该参数 |
| `chat.go:175` `LLMUsage` | 只有 `prompt_tokens` / `completion_tokens`,**不解析 cached_tokens** |
| `chat.go:830` `buildBasePrompt` | 字符串拼接产出整个系统提示词 |
| `chat.go:898` `buildBasePromptWithSkill` | 末尾追加「显式技能正文」,再交给 `attachMemoryRecall` |
| `memoryinject.go:43` `attachMemoryRecall` | L2 召回块**追加在系统提示词末尾,且不落盘** |
| `filetools.go:42` `directToolOrder` | 注释已写明「固定顺序便于断言与提示缓存」—— 顺序基础已具备 |
| `contextmgmt.go:281` `applyToolResultBudget` | 用**常量** 24000 截断工具结果(常量 → 幂等,安全) |
| 前端 `ChatPane.vue:1090` | 上下文指示器,点击**触发压缩**;tooltip 只有 `≈N/M token` |
| 前端 `RequestPreviewDialog.vue:44` | 请求快照,显示估算值与工具 schema token |
| 前端 | **无任何用量明细界面** |
| 全仓库 | `cache_control` / `cached_tokens` / `cache_read` 等标识**零出现** |

**一个已知的误判来源**:`newAnthropicHTTPRequest` 在流式时设了 `Cache-Control: no-cache`。那是 **HTTP 传输层**指令(告诉中间代理不要缓冲 SSE 流),与 prompt caching 毫无关系。两个 "cache" 是同名不同物。

**关键的好消息**:动态内容(L2 召回、显式技能正文)本来就追加在**尾部**,稳定内容天然在前。这是缓存的理想形状,不需要重排大部分装配逻辑 —— 唯一要挪的就是系统提示词里那段变量(见 §7)。

---

## 2. 两套机制:Anthropic 显式断点 vs OpenAI 自动前缀

这是全篇的地基。两条路径的差异不是「配置项不同」,而是**机制形状不同**。

| | Anthropic | OpenAI 兼容 |
|---|---|---|
| 触发方式 | **显式**:请求里标 `cache_control` | **自动**:超过最小长度即生效,无需改代码 |
| 缓存什么 | 你标记的断点**之前(含)的整个前缀** | **从提示词开头算起的最长公共前缀** |
| 能否跳过中间变量 | **能** —— 变量放在断点之后即可 | **不能** —— 没有断点概念,变量出现在任何位置之前都会截断缓存 |
| 断点数量 | 最多 4 个 | 无此概念 |
| 命中回溯范围 | 约 20 个 block | 无限制(纯前缀匹配) |
| 最小长度 | 约 1024(部分模型 2048),低于阈值**静默忽略** | 约 1024,低于阈值静默忽略 |
| 写入计费 | 有溢价(5m TTL 为 1.25x,1h TTL 为 2x) | 通常无写入溢价 |
| 读取计费 | 0.1x | 老模型 0.5x,新模型 0.1x |
| TTL | 默认 5 分钟,可选 1 小时 | 约 5–10 分钟,新模型更长 |
| 是否计入速率限制 | cache read **不计入 ITPM**(Haiku 3.5 例外) | 视网关而定 |

由此得出的**唯一一条通用设计律**:

> **每轮变化的内容必须严格待在提示词的最末尾。** 对 Anthropic 还要额外满足「在断点之后」。

这条律对 Anthropic 是充分条件(变量在断点后就安全),对 OpenAI 是必要条件(变量在末尾才可能命中)。

---

## 3. Anthropic 侧改动

### 3.1 结构体

```go
// cacheControl 提示缓存断点。nil 表示该块不打标。
// TTL 为空走 Anthropic 默认(5 分钟);"1h" 走扩展 TTL(写入计价 2x)。
type cacheControl struct {
	Type string `json:"type"`          // 固定 "ephemeral"
	TTL  string `json:"ttl,omitempty"` // "" 或 "1h"
}

// anthropicSystemBlock 系统提示词的块形式。
// 从 string 改数组的唯一理由就是要挂 cache_control。
type anthropicSystemBlock struct {
	Type         string        `json:"type"` // "text"
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}
```

- `anthropicReq.System`: `string` → `[]anthropicSystemBlock`
- `anthropicTool` 增加 `CacheControl *cacheControl`
- `anthropicContentBlock` 增加 `CacheControl *cacheControl`

注意 `anthropicContentBlock` 是**请求/响应共用**的(见该结构体注释),加字段只影响请求端序列化,响应端反序列化会忽略未知字段,安全。

### 3.2 system 拆两段

```
┌────────────────────── 稳定段(打在 P1 断点)──────────────────────┐
│ SystemPrompt(人设)                                                │
│ ## 工作区            ← 每会话固定                                  │
│ ## 工具使用偏好      ← 静态                                        │
│ ## 效率约定          ← 静态                                        │
│ ## 可用技能(L1 清单) ← 随会话技能白名单变化,会话内稳定             │
│ ## 长期记忆(L1 索引) ← 只在 memory_save/forget 后变化              │
└──────────────────────────────────────────────────────────────────┘
┌────────────────────── 动态段(断点之后,不缓存)──────────────────┐
│ BuildExplicitSkillBlock 正文 ← 用户写了 /技能名 才有,每轮可能不同  │
│ L2 召回块                    ← 见 §7,建议彻底移出系统提示词        │
└──────────────────────────────────────────────────────────────────┘
```

**为什么必须拆**:若不拆,L2 召回混在同一个字符串里,`System` 整体每轮都不同 → Anthropic 侧断点失效,OpenAI 侧前缀归零。这是最容易犯、也最不容易察觉的错(请求照样成功,只是钱白花)。

### 3.3 三个断点

| 断点 | 位置 | 缓存内容 | 稳定性 |
|---|---|---|---|
| **P1** | system 稳定段最后一个 block | 系统提示词全段 | 会话内稳定(记忆写入/技能切换后失效一次) |
| **P2** | `tools` 数组最后一个工具 | 全部工具 schema | 会话内稳定 |
| **P3** | messages 的**最后一条** | 全部历史 | 每轮滚动重建 |
| P4 | 备用 | —— | 单轮新增 block 过多时作中间断点(见 §10.3) |

P3 打「最后一条」而不是「倒数第二条」的理由:缓存存的是**断点之前(含断点)的整个前缀**。本轮把断点打在本轮最后一条上,下一轮请求的这个前缀原样还在(messages 只追加不修改),于是命中。断点每轮重打一次,把本轮新增内容也纳入缓存。

### 3.4 `toAnthropicRequest`

把「join 成一个字符串」改成「构造 block 数组,并在最后一块打标」:

```go
func toAnthropicRequest(req *LLMReq, policy *promptCachePolicy) *anthropicReq {
	out := &anthropicReq{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens, // 顺带修掉 §4.4 的硬编码
		Temperature: req.Temperature,
		Stream:      req.Stream,
		Tools:       toAnthropicTools(req.Tools, policy),
	}

	// 稳定段:打 P1
	if stable := strings.TrimSpace(req.SystemStable); stable != "" {
		out.System = append(out.System, anthropicSystemBlock{
			Type: "text", Text: stable,
			CacheControl: policy.control("system"),
		})
	}
	// 动态段:不打标
	if dyn := strings.TrimSpace(req.SystemDynamic); dyn != "" {
		out.System = append(out.System, anthropicSystemBlock{Type: "text", Text: dyn})
	}
	...
}
```

`LLMReq` 相应把单一 system 消息换成两个字段:

```go
type LLMReq struct {
	Model       string
	Messages    []LLMMessage
	// SystemStable 进缓存前缀;SystemDynamic 每轮变,必须排在断点之后。
	SystemStable  string
	SystemDynamic string
	Temperature   float64
	Stream        bool
	Tools         []LLMTool
	MaxTokens     int
}
```

> 备选(改动更小但更脆):保留单一字符串,用哨兵标记分隔两段再 split。**不推荐** —— 哨兵会随内容漂进提示词,一旦内容里出现同样字符串就静默错位。

### 3.5 `buildBasePromptWithSkill` 改签名

```go
type systemPrompt struct {
	Stable  string // 基础人设 / 工作区 / 工具偏好 / 效率约定 / 技能索引 / L1 记忆索引
	Dynamic string // 显式技能正文(+ 若保留自动召回,L2 也在这里)
}

func (a *App) buildBasePromptWithSkill(session *Session, dir, query string) (systemPrompt, error)
```

`attachMemoryRecall` 同步改为返回 `systemPrompt`,把召回块放进 `Dynamic` 而非拼接到整串末尾。

需同步改的调用点——**实测只有 4 处,比原先估的少**(均为签名适配,无逻辑变化):

| 函数 | 调用点 | 说明 |
|---|---|---|
| `buildBasePromptWithSkill` | `chat.go:918`(`executeChat`)、`chat.go:1589`(`ChatPlan`) | 共 2 处 |
| `buildBasePrompt` | `chat.go:1004`(被上面那个调用)、`chat.go:1645`(`executePlan`) | 共 2 处,后者直接吃 string,只需取 `.Stable` |
| `attachMemoryRecall` | `chat.go:1006/1013/1023` 与 `chat.go:1732`(计划执行) | 前 3 处在 `buildBasePromptWithSkill` 内部,只有第 4 处需单独处理 |

⚠️ **计划执行路径是唯一需要动脑的一处,而且本文早先把它写反了。** 原先写的是「`buildPlanSystemPrompt`
应整体归入 `Stable`,因为它在一次 plan 执行内不变」——**这是错的**:`buildPlanSystemPrompt(base, plan, step)`
会把**当前计划进度与当前步骤标题/要点**追加到 base 之后(见 `plan.go`),而 `[已完成]/[当前步骤]`
标记与 `本步任务:…` 是**每步都在变**的。正确的切法是:

```
Stable  = buildBasePrompt 的产物(人设 + 工作区 + 工具偏好 + 效率约定 + 技能索引 + 记忆 L1)
Dynamic = 显式技能正文 + L2 召回 + 计划进度块
```

计划进度块必须进 `Dynamic`,否则计划执行期间每一轮前缀都变、缓存完全失效——而且这种失效最不容易被发现,
因为计划执行本身就是一段密集的多轮调用,用户只会觉得「慢」,不会想到是缓存没命中。

### 3.6 断点的产生与开关

```go
// promptCachePolicy 由配置构造。Enabled=false 时 control() 恒返回 nil,
// 于是所有代码路径退化成今天的行为——不需要在调用点写 if。
type promptCachePolicy struct {
	Enabled bool
	TTL     string // "" 或 "1h"
	Rolling bool   // 是否启用 P3
}

// control 返回该位置该打的断点(nil = 不打)。
func (p *promptCachePolicy) control(where string) *cacheControl {
	if p == nil || !p.Enabled {
		return nil
	}
	return &cacheControl{Type: "ephemeral", TTL: p.TTL}
}
```

**关键设计约束**:`Enabled=false` 必须是「全链路无副作用」的,不能有任何「先打标再判断」的分支残留。出问题时的回滚动作应该是把配置改成 false,而不是回滚代码。这与本项目「配置缺失不得推断出限制性默认值」的教训同源 —— 反过来这里要求的是:**关闭开关必须真的等于没这个功能**。

---

## 4. OpenAI 兼容侧改动

### 4.1 不加任何字段

OpenAI 的缓存是服务端自动的。**不要在 OpenAI 路径上塞 `cache_control`** —— 它没有这个机制,硬塞只会污染请求体,部分网关还会因此报 400。

### 4.2 但必须改结构

`buildLLMMessages` 现在这样开头:

```go
// chat.go:745
func buildLLMMessages(history []Message, systemPrompt string, loader imageLoader) []LLMMessage {
	messages := []LLMMessage{
		{Role: RoleSystem, Content: systemPrompt},   // ← 第一条
	}
```

而 `systemPrompt` 里含每轮变化的 L2 召回(`attachMemoryRecall` 的产物)。于是:

- OpenAI 缓存的「从开头算起的最长公共前缀」
- 在**第一条消息内部**就因为召回块不同而分岔
- 后面再稳定也救不回来 → **命中率恒为 0**

这是最坏的位置。解决办法只有一个:**把变量从系统提示词里拿出去**(见 §7)。

改造后 `buildLLMMessages` 的签名同步换成接收 `systemPrompt` 结构体,取 `Stable` 放进 messages[0];`Dynamic` 若仍需下发,应并入**本轮最后一条 user 消息**,**绝不能留在 system**。

### 4.3 「OpenAI 格式」≠「OpenAI 的缓存」

格式只是 JSON 形状,缓存完全是**服务端实现**的事。本项目走的是自建网关(`isAnthropicEndpoint` 之外的路径),网关背后可能是:

| 网关背后 | 是否有缓存 | usage 字段 |
|---|---|---|
| OpenAI 官方 | 有,自动 | `prompt_tokens_details.cached_tokens` |
| DeepSeek | 有,自动(磁盘级) | `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`,另有兼容别名 `prompt_tokens_details.cached_tokens` |
| 其他兼容端点 | 各家不同,可能没有 | 未知 |
| 纯转发代理 | 取决于是否透传上游 | 未知 |

**这一条只能实测,不能推理**。判定方法见 §12。

### 4.4 顺带修掉 `max_tokens` 缺失

`LLMReq`(`chat.go:160`)没有 `MaxTokens` 字段,OpenAI 路径直接 `json.Marshal(req)` 发出去,实际输出上限完全由网关默认值决定 —— 客户端不可控。这与 `anthropic.go:24` 的 8192 硬编码是同一个问题,改动面完全重合,建议一起把 `MaxTokens` 提到 `LLMReq` / `Model` 上,省得做两遍。

---

## 5. Token 统计字段全清单

本节是统计与展示的字段依据。**先落地统计,再开缓存。**

### 5.1 Anthropic

```jsonc
"usage": {
  "input_tokens": 2095,                    // ⚠️ 只含未命中缓存的输入,见 §5.4
  "cache_creation_input_tokens": 2051,     // 本轮写入缓存的量
  "cache_read_input_tokens": 0,            // 本轮命中缓存的量
  "output_tokens": 503,
  "cache_creation": {                      // 按 TTL 拆分的写入量,二选一非零
    "ephemeral_5m_input_tokens": 2051,
    "ephemeral_1h_input_tokens": 0
  },
  "server_tool_use": { "web_search_requests": 0 },  // 服务端工具(如联网搜索)调用数
  "service_tier": "standard"               // standard / priority / batch,影响计价
}
```

要点:`cache_creation` 的 TTL 拆分只在 Anthropic 有,且**同一时刻只有一个非零** —— 它正好回答「我这轮是按 1.25x 还是 2x 计的价」。这个字段应当展示出来,否则用户配了 `ttl: "1h"` 却看到写入量挂在 5m 那一栏时会以为配置没生效。

### 5.2 OpenAI 兼容

```jsonc
"usage": {
  "prompt_tokens": 1000,          // ✅ 含 cached,是总量
  "completion_tokens": 200,
  "total_tokens": 1200,
  "prompt_tokens_details": {
    "cached_tokens": 800,         // 命中缓存的输入
    "audio_tokens": 0
  },
  "completion_tokens_details": {
    "reasoning_tokens": 150,      // 推理 token,计费算输出但不进 content
    "audio_tokens": 0,
    "accepted_prediction_tokens": 0,
    "rejected_prediction_tokens": 0
  }
}
```

`reasoning_tokens` 对 agent 尤其重要:它是**看不见的成本** —— 用户只看到最终回复很短,却不知道背后花了 150 个输出 token 在思考。前端必须单独列出来。

### 5.3 DeepSeek

```jsonc
"usage": {
  "prompt_tokens": 1000,                    // = hit + miss,总量
  "completion_tokens": 200,
  "prompt_cache_hit_tokens": 800,           // 命中
  "prompt_cache_miss_tokens": 200,          // 未命中
  "prompt_tokens_details": { "cached_tokens": 800 },   // 兼容别名
  "completion_tokens_details": { "reasoning_tokens": 0 }
}
```

### 5.4 三家语义差异(最关键的一张表)

**这张表是 §5.6 那个 bug 的根源,也是任何跨网关实现最容易踩的坑。**

| | `prompt_tokens` / 总量语义 | 缓存字段 | 拿到输入总量的算法 |
|---|---|---|---|
| **Anthropic** | **不含**缓存读写,**只是未命中部分** | `cache_creation_input_tokens` + `cache_read_input_tokens` | 三者**相加** |
| **OpenAI** | **含**缓存(总量) | `prompt_tokens_details.cached_tokens` | 直接用 `prompt_tokens` |
| **DeepSeek** | **含**缓存(= hit + miss) | `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` | 直接用 `prompt_tokens` |

也就是说:**同一个字段名 `input_tokens` / `prompt_tokens`,在 Anthropic 和 OpenAI 系里含义正好相反。** 归一化层必须显式处理,不能指望调用方记住。

⚠️ 还有一个已知的行业级坑:**有网关把 Anthropic 响应转成 OpenAI 格式时,把 `prompt_tokens` 只填成 `input_tokens`(未命中部分),而不是三者之和。** 本项目正好同时满足「走网关」和「支持两种协议」两个条件,所以这条对我们不是理论风险,而是**必须实测确认**的项(见 §12)。

### 5.5 归一化结构体与派生指标

```go
// TokenUsage 一次 LLM 请求的完整用量。
// 字段名供应商中立,各协议在解析时归一化进来(归一化规则见 §5.4)。
type TokenUsage struct {
	// —— 输入侧 ——
	InputUncached int // 未命中缓存的输入
	CacheRead     int // 命中缓存读取
	CacheWrite    int // 写入缓存
	// CacheWrite5m / CacheWrite1h 仅 Anthropic 有值,用于区分写入计价(1h 是 2x)
	CacheWrite5m int
	CacheWrite1h int

	// —— 输出侧 ——
	Output          int
	OutputReasoning int // 推理 token(计费算输出,不进 content)

	// —— 其他 ——
	ServerToolUseWebSearch int
	ServiceTier            string
}

// TotalInput 输入总量。
// ⚠️ Anthropic 的 input_tokens 只是未命中部分,必须三者相加;
//    统一在这里做,避免每个调用方各写一遍再各写错一遍。
func (u TokenUsage) TotalInput() int {
	return u.InputUncached + u.CacheRead + u.CacheWrite
}

// CacheHitRate 缓存命中率(按输入 token 计)。
func (u TokenUsage) CacheHitRate() float64 {
	if t := u.TotalInput(); t > 0 {
		return float64(u.CacheRead) / float64(t)
	}
	return 0
}

// InputCostMultiplier 相对全价的输入成本倍数,用于直接回答"省了多少"。
// writeRate:本轮缓存写入的计价倍数(5m 为 1.25,1h 为 2.0)。
func (u TokenUsage) InputCostMultiplier(writeRate float64) float64 {
	t := u.TotalInput()
	if t == 0 {
		return 1
	}
	cost := float64(u.InputUncached) + float64(u.CacheWrite)*writeRate + float64(u.CacheRead)*0.1
	return cost / float64(t)
}
```

**会话累计**:

```go
// UsageTotals 会话累计用量。随 Session 落盘,跨重启保留。
type UsageTotals struct {
	Turns           int
	InputUncached   int
	CacheRead       int
	CacheWrite      int
	Output          int
	OutputReasoning int
	FirstAt         time.Time
	LastAt          time.Time
}

func (t *UsageTotals) Add(u TokenUsage) { /* 逐字段累加 + Turns++ + 更新时间戳 */ }

func (t UsageTotals) TotalInput() int { return t.InputUncached + t.CacheRead + t.CacheWrite }

func (t UsageTotals) CacheHitRate() float64 { /* 同上 */ }
```

### 5.6 ⚠️ 必须先修的既有缺陷:`tokenAnchor` 语义

`anthropic.go:391` 与 `:647` 现在这样映射:

```go
PromptTokens:     r.Usage.InputTokens,   // ← 开缓存后这里会变成"只有未命中部分"
CompletionTokens: r.Usage.OutputTokens,
```

而 `chat.go:1183` 用 `resp.Usage.PromptTokens` 作为上下文长度的**真实锚点**,`tokencalib.go` 的校准也依赖它:

```go
if resp.Usage.PromptTokens > 0 {
	anchor = &tokenAnchor{InputTokens: resp.Usage.PromptTokens, MsgCount: len(messages)}
}
```

**一旦开启缓存**,`input_tokens` 只剩下未命中部分(极端情况下可能只有几十个 token)。后果链条是:

```
锚点严重偏小 → 上下文占比被低估 → needCompact 的 70% 阈值永不触发
→ 一路不压缩直到撞 API 侧的超限错误 → 只能靠 isContextOverflowError 兜底重试
```

而这个失败**不会在开着缓存的会话里立刻显现** —— 它表现为"聊到很后面突然报上下文超限",很容易被误判成模型窗口配小了。所以这条必须**在开缓存之前**修掉:解析时改为 `Anthropic: InputTokens + CacheCreationInputTokens + CacheReadInputTokens`。

这条也解释了为什么实施顺序里「统计」排在「断点」之前 —— 不是保守,是因为不先归一化语义,后面所有判断都会建立在错数上。

### 5.7 落盘与接口

- `Session` 增加 `UsageTotals` 字段(随 `sessions.go` 现有的整份覆盖写一起落盘)。
- 保留 `GetContextStat(sessionID)` 作为**轻量高频**接口(切会话即拉,服务于指示器),新增独立的 `GetUsageDetail(sessionID) UsageDetail` 供弹窗按需拉取 —— 不要把明细塞进 `ContextStat`,那会拖慢每次切会话。
- 每轮 LLM 响应后 `emitChatEvent` 一个 `chat:usage` 事件,带上本轮 `TokenUsage` 与会话累计,让开着的弹窗实时更新。

---

## 6. 前端「用量明细」弹窗

### 6.1 入口与指示器改造

现状(`ChatPane.vue:1090` 附近):指示器显示「上下文 {pct}%」,tooltip 只有 `≈N/M token(N 条消息)`,**点击直接触发压缩**(注释写明是有意设计)。

改造:

- 指示器**点击改为打开「用量明细」弹窗**;把「触发压缩」挪成一个独立的小按钮(与指示器并排),并在弹窗内也放一个「立即压缩」动作。
- 指示器上增加一个**缓存状态点**:未启用为灰、已命中为绿、已启用但本轮未命中为黄、疑似网关不支持为红。这样用户不用打开弹窗就能看出缓存是否在工作。
- 弹窗组件走 `stores/ui.js` 的对话框通道 —— **不要用组件内自己的 `ref`**。本项目有过「用宿主原生对话框导致删除按钮全线失效」的教训(见记忆 `native-dialogs-unreliable`),所有对话框统一走该通道。

### 6.2 弹窗分区

新增 `src/components/business/UsageDialog.vue`,五个分区:

**① 本轮(最近一次请求)**

四格并排:未命中输入 / 缓存读 / 缓存写 / 输出(括号内标注其中推理 token 占多少)。缓存写入格下方小字标注生效 TTL(5m 或 1h)——直接取自 `cache_creation` 的拆分,回答「我配的 1h 到底生效没有」。

**② 缓存**

- 命中率(用区分度的进度条,不是百分比数字了事)
- 相对成本倍数:`0.12x`(即比全价省 88%),由 `InputCostMultiplier` 得出
- 状态徽标(见 §6.3)
- 若命中率 > 0 但出现「本轮写入量大、命中量为 0」的抖动,给一句提示:「缓存可能因前缀变化被重建」——这正好对应 §10.1 的稳定性风险,让用户能自己发现。

**③ 会话累计**

轮数、输入总量、输出总量、累计命中率、首次/最后请求时间。数据来自 `UsageTotals`。

**④ 上下文构成**

系统提示词 / 工具定义 / 历史消息 / 压缩摘要 各占多少 token 与百分比。

这一块**数据已经存在**,只是从没集中展示过:`EstablishedTokens`(估算)、`ToolSchemaTokens`(工具定义,`requestlog.go` 已记)、`SummaryIndex` 与 `CoveredMsgs`(摘要覆盖范围)、`messageCount`。把它们并到一处,用户第一次能看清「我的上下文到底被谁吃了」。**这也是判断该不该压缩、该不该精简工具集的唯一依据。**

**⑤ 估算 vs 真实**

`tokencalib` 的当前校准系数、估算值与真实锚点的偏差、锚点对应的消息序号。

这一栏是本项目独有的资产(见 `tokencalib.go` 的 EWMA 校准),展示它对两个问题直接有用:一是让用户知道「上面的百分比有多可信」;二是当 §5.6 那类语义错误再次发生时,偏差会先在这里显形。建议对偏差设阈值,超过时给出警告而不是安静地显示一个离谱数字。

### 6.3 状态徽标与空态

徽标取值(对应「不许静默」这条原则):

| 状态 | 判定 | 展示 |
|---|---|---|
| 未启用 | 配置 `enabled: false` | 灰 · 「缓存未启用」 |
| 已启用未命中 | 本轮 `CacheRead == 0` 且 `CacheWrite > 0` | 黄 · 「本轮未命中」 |
| 已命中 | 本轮 `CacheRead > 0` | 绿 · 「命中 87%」 |
| 疑似不支持 | 已启用且连续 N 轮(`CacheRead + CacheWrite == 0`) | 红 · 「网关可能不支持缓存」+ 一键关闭开关 |

最后一条是关键:它把「静默按全价计费」变成一次显式提示。同时要注意 `CacheWrite == 0 && CacheRead == 0` 也可能是**未达最小长度**(约 1024 token),文案上应把两种可能都列出来,不要让用户误判为网关问题。

空态处理:

- 本轮四格全为 0 且累计轮数为 0 → 「本次请求未返回用量明细」,不显示一排 0。
- 老会话没有 `UsageTotals` 字段 → 「该会话早于用量统计上线,无历史数据」。
- 走的是未知网关、字段名对不上 → 在弹窗底部显示一个「原始 usage JSON」的可折叠区,供用户直接核对。**这个折叠区成本极低,但能省掉未来所有"到底是哪一层错了"的扯皮。**

### 6.4 数字格式化

统一走已有的 `src/utils/format.js`,新增 `formatTokens`:`1234 → 1.2k`,`1234567 → 3.4M`。百分比保留一位小数。**不要把精确值四舍五入掉** —— 精确值放进 tooltip,概览用缩写。

---

## 7. L2 召回的处置(关键决策)

这是两条路径**共同**的症结:`attachMemoryRecall` 把每轮变化的召回块拼进了系统提示词,而系统提示词位于整个提示词的头部。对 Anthropic 是「断点前有变量」,对 OpenAI 是「前缀从头部就分岔」。

三条出路:

**方案 A(推荐):取消自动召回,只保留 `memory_search` 工具。**

L1 索引留在系统提示词(会话内稳定),L2 交给模型主动调用已有的 `memory_search`。

理由:自动召回本质是拿「前缀稳定性」换「少一次工具往返」。在有缓存的系统里这个交换是亏的 —— 少一次往返省下的延迟,远小于每轮全价重算系统提示词与整个历史的代价。而且工具调用是可观测、可审计的,自动召回是隐式的。

**方案 B:召回块随消息落盘。**

若必须保留自动召回,就要保证「本轮发出的那条消息」与「下一轮作为历史重建的同一条消息」**逐字节一致**。现在 `attachMemoryRecall` 明确「召回块不写进 `Session.Messages`」,所以历史重建时那条消息没有召回块 → 前缀必然不一致。

必须改成:把实际发出的召回块持久化到该条消息上。代价是历史随轮次略增,且写入的是「当时那次召回」的快照 —— 这反而符合缓存语义。

**方案 C(不推荐):追加 trailing system 消息。**

OpenAI 的消息数组以 user 结尾,想在其后再塞内容只能追加一条 system。模型行为会变得不可预期,且部分网关不接受。**不采用。**

无论选哪个,L1 索引都必须保证**确定性输出**:同样的记忆集合必须产出同样的字符串。若 `IndexForPrompt` 内部按更新时间排序且时间戳进了正文,则每次调用都不同,缓存恒不命中。**需单独核对 `memory` 包**。

---

## 8. 收益估算

以基准输入价为 1(Anthropic 口径):

| 方案 | 首次(写) | 后续每次(读) | 回本点 |
|---|---|---|---|
| 不缓存 | 1.0 | 1.0 | —— |
| 5 分钟 TTL | 1.25 | 0.1 | **5 分钟内第 2 轮** |
| 1 小时 TTL | 2.0 | 0.1 | **1 小时内第 3 轮** |

对桌面 agent 的建议:**默认 1 小时 TTL**。人在阅读/思考/切窗口时很容易超过 5 分钟,而 5 分钟 TTL 一旦过期就要按 1.25 重写;一个正常编码会话动辄十几轮,1 小时的 2x 写入很快被 0.1x 的读取摊平。对成本敏感的场景可配成 5 分钟。

> ⚠️ 注意 TTL 也会被服务端单方面调整 —— 2026 年 3–4 月 Anthropic 把默认从 1 小时降到过 5 分钟,一批用户的缓存重建频率和账单同时上升。所以 §6.2 里「本轮生效 TTL」那一格不是装饰:它是发现这类变化的唯一手段。

OpenAI 侧无写入溢价,回本更快(新模型读价同为 0.1x,老模型 0.5x)。

**附带收益**:Anthropic 的 cache read **不计入 ITPM 限流**。对本项目当前零重试的 API 层(见差距分析)来说,这一条能显著降低撞 429 的概率 —— 缓存不只是省钱。

---

## 9. 配置

```jsonc
{
  "promptCache": {
    "enabled": false,      // 先默认关,人工验证命中后再改 true
    "ttl": "1h",           // "" | "1h",仅 Anthropic 生效
    "rolling": true,       // 是否启用 messages 滚动断点(P3/P4)
    "rollingGuardBlocks": 15,
    "usageDialog": true    // 用量明细弹窗;排障时可临时关掉
  }
}
```

默认 `enabled: false` 上线的理由:这个改动「错了不会报错、只会多花钱」,属于必须靠观测才能确认的一类。`ttl` / `rollingGuardBlocks` 对 OpenAI 侧无效,但配置项保留一份、不做协议区分,避免配置层过早复杂化。

---

## 10. 坑

### 10.1 前缀必须逐字节稳定

全部坑里最大的一个。历史上任何对**已发送过的内容**的变换,只要不是幂等的,就会让前缀每轮都变,缓存永久失效且**无任何报错**。需逐一确认:

- `applyToolResultBudget` —— 用常量 24000 截断,**安全**。但如果将来把预算改成「按当前上下文余量动态调整」,历史消息的截断结果就会随轮次变化,缓存立刻全废。**这条要写进该函数的注释里当红线。**
- `withContextSummary` —— 摘要在压缩时生成一次后应保持不变。当前实现是「原文永不删除,只在组装时替换前缀」,需确认替换后的摘要文本在多轮之间是同一份。
- `memoryIndexBlock`(L1) —— 见 §7 末尾。
- `BuildSkillIndex` —— 技能列表顺序必须稳定。

### 10.2 L2 的位置

拆分成 `Stable` / `Dynamic` 时,**最容易写错的一行**就是 L2 归入哪一段。误放进 `Stable` → 断点前内容每轮变 → 命中率 0。

### 10.3 相邻两轮之间新增 block 数超过回溯上限(仅 Anthropic)

Anthropic 缓存命中只向前回溯有限个 block(约 20)。本项目系统提示词明确**鼓励一轮批量发起多个工具调用**(见「效率约定」第 1 条),一轮新增的 block 可能远超 20:

```
assistant(tool_use × 8) + user(tool_result × 8)  → 一轮就新增 16+ 个 block
```

一旦超过回溯上限,上一轮的断点就「够不着」了,表现为**明明什么都没改却突然不命中**。

处理:P3 每轮仍打在最后一条(保证下一轮锚点一定在范围内),同时加守卫 —— 若本轮新增 block 数 > 阈值(取 15 留余量),用 P4 在本轮**中部**补一个中间断点。

```go
// markRolling 给 messages 打滚动断点。
// 正常只在最后一条打;本轮新增过多时,额外在中部补一个,避免超出回溯上限。
func markRolling(msgs []anthropicMessage, addedBlocks int, cc *cacheControl) {
	if cc == nil || len(msgs) == 0 {
		return
	}
	if addedBlocks > rollingGuardBlocks && len(msgs) >= 2 {
		markLastBlock(&msgs[len(msgs)/2], cc) // P4 中间断点
	}
	markLastBlock(&msgs[len(msgs)-1], cc) // P3
}
```

OpenAI 侧无回溯限制(纯前缀匹配),此坑不适用。

### 10.4 压缩与缓存的关系

压缩会让前缀**整体突变**,那一轮缓存必然全废,之后重建。损失一轮,可接受。但要注意两者不要互相打架:

- 缓存解决「TTL 内的重复前缀」;
- 压缩是有损的,解决「上下文确实装不下」和「跨很久缓存已失效」。

优先级应当是**缓存优先、压缩兜底** —— 能在 TTL 内命中的部分,缓存既便宜又保真(压缩会丢标识符,缓存逐字节还原)。压缩阈值(窗口 70%)**不必调整**,但值得观察:命中率高的会话 token 增长会变慢,压缩触发频率自然下降。

### 10.5 最小可缓存长度

system + tools 合计需达到最小 token 数(约 1024,部分模型 2048)才真正建立缓存,**低于阈值不报错、静默忽略**。⚠️ 实测:`buildBasePrompt` 里的静态中文合计 **≈930 token**(按本项目自己的 CJK=1 估算口径)。
也就是说**稳定段本身可能刚好卡在阈值之下**——技能索引与记忆 L1 索引都可能为空,工作区路径也可能很短。
加上工具定义(P2)通常能过线,但若将来支持「精简模式」或禁用技能,这个断点就会被静默忽略,
而症状只是「缓存怎么没生效」。**这正是 §6.3 那个红色徽标必须区分「未达长度」与「网关不支持」的原因。**

### 10.6 网关不支持时的表现

可能静默透传、也可能直接 400。前者靠 §6.3 的红色徽标兜底;后者需要在错误处理里识别「cache_control 相关」的 400 并自动关闭开关,而不是把整轮对话判死 —— 注意本项目 API 层当前**零重试**,一次 400 就是整轮失败。

### 10.7 统计本身的成本

`UsageTotals` 累加与 `chat:usage` 事件是每轮一次的轻量操作,但**不要把它做成每次工具调用都触发的事件** —— 那样一个 20 轮的运行会推 20 次无意义更新。粒度就定在「一次 LLM 响应」上。

---

## 11. 实施顺序(不可颠倒)

1. **统计先行**:加 `TokenUsage` / `UsageTotals` / 归一化层 / `chat:usage` 事件 / `GetUsageDetail`,并在前端做「用量明细」弹窗。**此时缓存命中率恒为 0,但基线可测。**
   - 同时**修掉 §5.6 的锚点语义缺陷**。这一步不做,后面所有关于「该不该压缩」的判断都建立在错数上。
2. **改结构**:按 §7 决策处理 L2,把系统提示词拆成 `Stable` / `Dynamic`。**此步单独验证 OpenAI 侧是否开始命中** —— 它不需要任何断点就能让自动缓存生效,是最纯粹的验证。
3. **上 Anthropic 的 P1 + P2**(静态前缀,`rolling: false`)。
4. **上 P3 / P4 滚动断点**。最容易翻车,单独一步、单独验证。
5. 最后做 TTL 与阈值调优,以及 `max_tokens` 收口。

第 1 步必须先行,且第 2 步先于第 3 步:第 2 步同时服务两条路径,能独立验证。

---

## 12. 验证清单

> 本沙箱无 Go 工具链,以下需在真机执行。

**构建与回归**

- [ ] `go build ./...` 与 `go test ./...` 通过(结构体改字段会影响 `anthropic_test` 相关断言)。
- [ ] 关闭开关后,请求体与改造前**逐字节一致**(可用 `requestlog.go` 的快照 diff 比对)。这是 §3.6 那条设计约束的验收点。

**统计字段(缓存未开时应先通过)**

- [ ] 同一会话连发 3 轮,**缓存全关**:`usedTokens` 占窗口比例应与前端此前显示一致(证明归一化改造没有改变既有语义)。
- [ ] 打开缓存后复测同一 3 轮:占比应**基本不变**。若明显变小,就是 §5.6 那个锚点问题没修干净。
- [ ] 用 DeepSeek 或 OpenAI 走的网关跑一轮,确认 `cached_tokens` / `prompt_cache_hit_tokens` 至少有一个被解析到。
- [ ] 弹窗底部的「原始 usage JSON」在不同网关下都能拉到,且与界面数字对得上。

**Anthropic 路径**

- [ ] 第 1 轮 `cache_creation_input_tokens > 0`(证明断点被接受)。
- [ ] 第 2、3 轮 `cache_read_input_tokens > 0`,且约等于第 1 轮的写入量。
- [ ] `cache_creation.ephemeral_5m_input_tokens` 与 `ephemeral_1h_input_tokens` 中,与配置 `ttl` 对应的那一项非零。
- [ ] 开 `rolling: false` 复测:确认只有静态前缀命中,以隔离 P3 的贡献。
- [ ] 一轮内批量发起 8+ 个工具调用,确认下一轮仍然命中(验证 §10.3 的守卫)。

**OpenAI 兼容路径**

- [ ] 先确认网关背后是谁(看响应里的 model 字段与 usage 字段名)。
- [ ] 完成 §11 第 2 步后,连发 3 轮:第 2、3 轮的 `cached_tokens`(或 `prompt_cache_hit_tokens`)应 > 0。
- [ ] 若始终为 0:依次排查 (a) 网关是否透传上游缓存;(b) 前缀是否真的稳定(打印两次请求的 messages[0] 做逐字节 diff);(c) 是否达到最小长度。

**交叉验证**

- [ ] 会话 A 与 B(不同工作区目录)交替发消息:A 仍能命中,不被 B 挤掉。
- [ ] 触发一次压缩,确认该轮命中归零、下一轮恢复,且弹窗的「上下文构成」里摘要占比同步变化。
- [ ] 推理模型跑一轮,确认 `reasoning_tokens` 被单独列出且不计入正文长度。
- [ ] 断网/网关异常路径复测:确认开关不影响任何错误处理分支。

---

## 13. 不做什么

- **不在 OpenAI 路径上塞 `cache_control`**。它没有这个机制。
- **不做自动 TTL 探测**。5 分钟 vs 1 小时的取舍是用户场景问题,不是能自动推断的。
- **不给每条历史消息打断点**。上限 4 个,且 P1 与 P3 内容有重叠,过多断点会让写入费用翻倍。
- **不做美元成本估算**。需要维护一份跨厂商、跨时期的单价表,且各家改价频繁(见 §8 的 TTL 变更);一旦表过期,展示的数字比没有更糟。本期只做 token 与「相对倍数」,倍数不依赖绝对价格。
- **不重构压缩策略**。缓存与压缩互补而非替代,本次只加 §10.1 的稳定性注释,不碰 `contextmgmt.go` 的压缩逻辑。
- **不为缓存调整系统提示词内容**。只调整**装配顺序**,不改写文案 —— 文案变更应走独立的提示词迭代流程,否则会混淆「缓存没生效」和「提示词改了」两类回归。


---

## 14. 实施状态

### 已完成：§11 第 1 步（统计先行 + 锚点修复）

**新增 `usage.go`** —— 归一化层与偏好存储：

- `TokenUsage`（供应商中立的一次请求用量，含 `CacheWrite5m/1h`、`OutputReasoning`、`ServiceTier`、`ServerToolUseWebSearch`）与派生指标 `TotalInput` / `CacheHitRate` / `InputCostMultiplier`。
- `UsageTotals`（会话累计，**只增不减**）与 `CacheZeroStreak`（连续零命中计数——必须落盘，否则「连续 3 轮」这个条件永远凑不齐）。
- `CacheState` 五态 + `CacheView` + `cacheViewOf`：缓存状态与其文案在后端**只定义一处**，前端只上色。
- `UsageDetail`：弹窗数据。刻意不进 `ContextStat`（后者是「切会话即拉」的高频接口）。
- `usageLog`：每会话最近一次用量（内存态，与 `llmRequestLog` 同定位）。
- `PromptCachePrefsStore`：`~/.local-agent/prompt-cache.json`，**默认关闭**。读失败一律回默认（保守方向）；TTL 与守卫阈值在写入时就归一，避免「界面显示成功、实际被静默改掉」。

**修掉 §5.6 的锚点缺陷**：

- `anthropicUsage` 补齐 `cache_creation_input_tokens` / `cache_read_input_tokens` / `cache_creation`（TTL 拆分）/ `service_tier` / `server_tool_use`。
- 新增**唯一**的 Anthropic → `LLMUsage` 映射 `llmUsageFromAnthropic`，非流式与流式共用（原先两处各写一份，正是「两处实现悄悄漂移」的高发形态）。`PromptTokens` 恒为**输入总量**。
- `LLMUsage` 补齐 `CacheRead/CacheWrite/CacheWrite5m/CacheWrite1h/ReasoningTokens/ServiceTier/ServerToolUseWebSearch`，以及 OpenAI 的嵌套形状与 DeepSeek 的扁平别名；新增幂等 `normalize()`。
- ⚠️ DeepSeek 的 `prompt_cache_miss_tokens` **刻意不映射为 CacheWrite** —— 未命中不是写入，混为一谈会让成本倍数凭空变贵。
- `RawUsage`：非流式从响应体原样取出（保留未建模字段）；流式把带 usage 的两帧拼成数组，两个来源都不丢（缓存字段只在 message_start，输出只在 message_delta）。

**累计、接口与事件**：

- `Session.UsageTotals *UsageTotals`（指针：nil = 统计上线前的会话 → 前端显示空态而非一排 0）。
- `SessionStore.AddUsage`；每轮模型调用后由 `agentRun.noteUsage` 完成「记内存 → 累加落盘 → 推送前端」三件事，**统计是旁路**：任一步失败只记日志，绝不让本轮对话失败。
- 注意 `noteUsage` 与锚点是**两个独立判断**：锚点只在 `PromptTokens>0` 时更新，而统计要覆盖「只有输出 token」的畸形响应——跟着锚点走会让那一轮的花费凭空消失。
- `ChatEvent.Usage` + `ChatEventUsage = "usage"`；`GetUsageDetail` / `GetPromptCachePrefs` / `SetPromptCachePrefs` 三个 App 方法；删会话时一并 `usageLog.forget`。

**前端**：

- `UsageDialog.vue`：五个分区（本轮 / 缓存 / 会话累计 / 上下文构成 / 估算可信度）+ 原始 usage 折叠区。打开时向后端拉完整明细，运行期间由 `live` 事件按**轮次**（不是时间戳——前后端时钟不同源）增量刷新。
- `ChatPane.vue`：指示器点击改为打开明细；压缩挪成独立的「压缩」按钮；新增**缓存状态点**（未启用时不渲染——显示灰点会让人误以为「开了但没命中」）。
- `eventbus` / `chat()` / `chatStore` 三处打通 `onUsage`；`format.js` 新增 `formatTokens` / `formatPercent`。
- `wailsjs/go/main/App.js` 与 `App.d.ts` **手工同步**（生成器在本环境跑不了）。

### 尚未做

- **缓存断点本身**（§3 的 P1/P2/P3/P4、§4 的 `Stable`/`Dynamic` 拆分）。开关与配置已就位，但还没有任何地方读它去打标——**现在即使把开关打开也不会有任何效果**。
- 设置页里的开关 UI（`GetPromptCachePrefs` / `SetPromptCachePrefs` 已可用，尚未接入界面）。
- 计划执行路径（`executePlan`）没有传 `onUsage`：累计值照常落盘，只是那条路径上弹窗不会实时刷新。

### 沙箱内已做的验证（可复现）

本环境无 Go 工具链，`vite build` 也因缺 rollup 的 linux 原生包而跑不起来。实际做了这些：

1. **语法解析**：31 个 `.js` + 35 个 `.vue` 的 `<script setup>` 全部通过 `node --check`。
2. **SFC 真编译**：用 `@vue/compiler-sfc` 对全部 35 个 `.vue` 跑 `compileScript` + `compileTemplate`，并用 `sass` 加上 `variables.scss` 前置后编译每个 style 块 —— script / template / scss 全通过。
3. **模板标识符核对**：抽出模板里所有表达式，与 `compileScript` 返回的 `bindings` 对照，确认没有引用未定义的标识符（含 `v-for` 别名与插槽参数）。
4. **前后端字段对齐**：从 Go 结构体提取 JSON 标签，与前端实际读取的字段逐个比对（覆盖 `?.` 与 `.value` 两种解包形态），无缺失。
5. Go 侧做了括号平衡与标识符定义点核对（`tokenUsageOf` / `cacheViewOf` / `noteUsage` / `AddUsage` / `GetUsageDetail` 各一处定义）。

### 真机必须验证的

`go build ./...` 与 `go test ./...` 仍需在真机执行。重点看这几条：

- [ ] **统计改造不能改变既有语义**：缓存全关时连发 3 轮，`usedTokens / windowTokens` 应与改造前一致。若明显变小，就是 §5.6 的锚点没修干净。
- [ ] 弹窗的「原始 usage」在 Anthropic 与 OpenAI 两种模型下都能拉到。
- [ ] 推理模型跑一轮，`reasoning_tokens` 应单独列出（注意：Anthropic 路径本身没有这个字段，只有 OpenAI 系网关会给）。
- [ ] 切换会话、删除会话后 `usageBySession` 不串台、不留残留。
- [ ] 新按钮布局在窄窗口下不挤压输入框。
