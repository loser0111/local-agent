# MCP 与「工具」的概念分层设计（方案 · 待评审）

> 状态：**P0 – P3 全部实现**（2026-09-17）。详见文末「实施状态」。
> 起因：用户提问「当前设计中 MCP 和 tools 的概念是否设计得不清楚」——结论是**是**，且已有可观测代价（见第三节）。

---

## 一、结论摘要

现在只有一个概念 `ToolConfig`，它同时承担了三种角色：**接入来源**（MCP 服务器 / CLI 模板 / HTTP API / 内置实现）、**能力单元**（一个可被模型调用的工具）、**展示条目**（设置页列表里的一行）。三者粒度不同——对内置/CLI/API 是"一条配置 = 一个工具"，对 MCP 是"一条配置 = 一个服务器，下面是 N 个工具"——于是同一个结构体里出现"字段各只服务一类"和"白名单粒度随类型变化"这类现象。

建议把它显式拆成三层：**来源（ToolSource）→ 工具（Tool）→ 暴露策略（Exposure）**，并让存储格式与 MCP 生态（`mcpServers`）对齐、参数 schema 直通不压平。

---

## 二、现状盘点（均有代码依据）

### 2.1 数据结构：一条配置承载四种东西

`toolstore.go`：

```go
type ToolConfig struct {
    ID, Name, Label, Description string
    Type          string            // builtin | cli | mcp | api
    Icon          string
    Enabled       bool
    Builtin       bool              // 仅内置有意义
    Parameters    []ToolParamConfig // 仅 CLI/API 有意义
    Config        json.RawMessage   // 各类型特有配置（弱类型）
    DisabledTools []string          // 仅 MCP 有意义
    Discovered    []MCPToolMeta     // 仅 MCP 有意义
}
```

`Type` 的四个取值对应四种"来源"，但数据结构上它们与"工具"同层。`Config` 是 `json.RawMessage`，各类型自己再 `json.Unmarshal` 一次（`CLIToolConfig` / `APIToolConfig` / `MCPToolConfig`），编译期无法约束"type=mcp 就一定要有 transport"。

### 2.2 装配与暴露

`tools.go` 的 `BuildView` 按 `Type` 分支装配：内置 → `newBuiltinTool`，CLI/API → 模板类工具，MCP → 连接服务器后**为每个子工具生成一个工具**，命名 `mcp__<server>__<tool>`（`toolruntime.go:365`）。

暴露分两种：内置文件工具与 `ask_user` 走 `directToolOrder` 直出给模型；**MCP 子工具、CLI、API 只能经 `tool_router` 发现**。这是合理的（避免 prompt 膨胀），但它意味着"MCP 工具"和"内置工具"对模型呈现方式根本不同，而这一点在数据结构上没有任何体现。

### 2.3 白名单：对内置是工具级，对 MCP 是服务器级

`tools.go:377`：

```go
if len(whitelist) > 0 && !whitelist[cfg.ID] { continue }
```

白名单按 `cfg.ID` 过滤。对内置工具，`cfg.ID == "read_file"`，即工具级；对 MCP，`cfg.ID` 是服务器 ID，即**整台服务器进/出**。单个 MCP 子工具的开关则在另一个字段 `DisabledTools` 里（`BuildView` 里用它跳过子工具）。

UI 上的对应现象：`NewSessionDialog.vue:183` 的白名单选择器列的是 `toolsStore.enabledTools`，所以**MCP 服务器作为一项出现在"选择工具"列表里**；而设置页 `ToolSettings.vue:133` 又把它展开成子工具列表（只读展示 + "已禁用"标记）。

### 2.4 命名：同一个 MCP 子工具在三处叫不同名字

| 位置 | 名字形态 | 依据 |
|---|---|---|
| 设置页子工具列表 | 服务器上的原始名（如 `get_config_raw`） | `TestToolConnection` 回填 `MCPToolMeta{Name: t.Name}`（`app.go`），`ToolSettings.vue:137` 展示 |
| 模型看到的工具名 | `mcp__<server>__<tool>` | `mcpToolName()`（`toolruntime.go:365`） |
| 权限规则里要写的名字 | `mcp__<server>__<tool>`（必须带前缀） | 规则按工具名匹配（`permission.go` 的 `Rule.matchesUnit`） |

用户在对的地方（设置页）看到的名字，和写规则要用的名字不是同一个。

### 2.5 权限：MCP 子工具没有 specifier 语义

`MCPTool` 没有实现 `SubjectProvider`，于是判定主体退化为 `newToolSubject`：`Units` 只有一段，内容是 `compactArgs` 的键值摘要（`permission.go:151`，形如 `appid=100049128 env=prod`，键名排序后拼接、截断 400 字符）。

后果：`read_file`/`exec_shell` 那套"工具 + 路径/命令段"的判定语义，MCP 工具完全没有；写 `mcp__qconfig__changeqconfig(appid=100049128)` 这种规则**看起来能命中**（前缀匹配恰好对上了排序后的第一段），但它依赖键名排序和字符串拼接格式，属于意外可用而非设计可用。

### 2.6 存储格式与生态不一致

MCP 官方与各家客户端（Claude Desktop、Cline）通用格式：

```json
{ "mcpServers": { "qconfig": { "url": "https://...", "type": "streamable-http", "headers": {...} } } }
```

本项目格式：

```json
{ "type": "mcp", "config": { "transport": "http", "url": "https://...", "headers": {...} } }
```

差异点：外层是数组而非 `mcpServers` 字典、`type` 的取值体系不同（`streamable-http` vs `http`）、传输字段名不同（`type` vs `config.transport`）。

**这个代价今天已经付过**：会话 `20260917_162303` 里，模型为了让 QConfig 接进来，自己在会话中完成了这层翻译，并写下"本系统传输字段为 transport，取值 http，等价于官方片段的 type: streamable-http"作为说明；`20260917_170140` 里它还专门去读了 `toolstore.go` 确认字段名。也就是说，**格式翻译的成本被转嫁给了每次接入的用户/模型**。

### 2.7 MCP 参数 schema 被压平（功能损失）

`paramsFromJSONSchema`（`toolruntime.go`）只取 schema 第一层的 `type` 与 `description`：

```go
argType := "string"
if t, ok := prop["type"].(string); ok { argType = t }
desc := ""
if d, ok := prop["description"].(string); ok { desc = d }
m[name] = &ToolArgDef{Type: argType, Description: desc}
```

丢掉的：`enum`（可选值）、`items`（数组元素类型）、嵌套 `properties`（对象结构）、`required`（是否必填）。且 `MCPTool` 未实现 `RequiredParams`（`grep` 计数为 0），所以必填信息也没了。

对简单工具影响不大；对 `changeqconfig` 这类带嵌套配置结构的工具，模型是在**猜参数形状**。结合 2.5，权限侧看到的也只是压平后的键值摘要。

---

## 三、问题清单与代价

| # | 问题 | 代价 |
|---|------|------|
| 1 | 一个结构体承载来源/工具/条目三种角色，字段各只服务一类 | 新增一种接入方式要动 `ToolConfig`；类型与配置的一致性只能靠约定 |
| 2 | 白名单粒度随类型变化（内置=工具级，MCP=服务器级），子工具开关另在 `DisabledTools` | 同一意图两个机制；"只让这个会话用某几个 MCP 工具"目前表达不出来 |
| 3 | 同一子工具三个名字（UI 原始名 / 模型前缀名 / 规则前缀名） | 用户写规则要猜；设置页看到的名字不能直接复制去写规则 |
| 4 | MCP 工具没有 specifier 语义，规则匹配依赖参数摘要的字符串形状 | 无法表达"只允许查 100049128 这个 appid 的配置"这类细粒度授权 |
| 5 | 存储格式与 MCP 生态不一致 | 每次接入都要人工/模型翻译（今天已发生两次）；无法直接粘贴官方片段 |
| 6 | MCP 参数 schema 压平，丢 enum/items/嵌套/required | 模型猜参数形状 → 参数名/结构写错 → 多轮返工（今天出现过 `command` vs `cmd` 一类错误） |

---

## 四、目标模型：三层

### 4.1 概念

1. **来源 ToolSource**：能力从哪来、怎么连。四种：`builtin`（编译进程序的实现）、`cli`（命令模板）、`http`（HTTP API 模板）、`mcp`（MCP 服务器）。带启用状态、连接状态、凭据。
2. **工具 Tool**：一个可被调用的能力单元。名字、描述、**完整参数 schema**、归属来源、暴露策略。MCP 的一个子工具就是一个 `Tool`（`mcp__server__tool`），来源指向那台服务器。
3. **暴露策略 Exposure**：`direct`（直出给模型）/ `router`（经 tool_router 发现）/ `internal`（不暴露，如 `read_skill` 这类只给内部用）。这是"给不给模型看"的问题，与来源无关。

### 4.2 类型草案（示意，非最终）

```go
// 来源：只管"怎么连、能不能用"
type ToolSource struct {
    ID      string
    Name    string
    Kind    SourceKind      // builtin | cli | http | mcp
    Enabled bool
    Config  json.RawMessage // 按 Kind 解析；可加校验函数保证 Kind 与 Config 一致
}

// 工具：只管"是什么、怎么调"
type Tool struct {
    Name        string
    Description string
    SourceID    string
    Schema      json.RawMessage // 完整 JSON Schema，直通不改写
    Exposure    Exposure
}
```

### 4.3 要钉住的不变量

1. `Tool` 一定有 `SourceID`；MCP 子工具的 `Name` 一律是 `mcp__<server>__<tool>`，**UI 也显示这个名字**（原始名作为副标题）。
2. `Schema` 直通：不再经过 `ToolArgDef{Type, Description}` 这类有损结构；`required` 来自 schema 本身，`RequiredParams` 接口取消。
3. 白名单只针对 `Tool`（能力），不再有"服务器级"特例；MCP 服务器的"整台启停"用 `Source.Enabled` 表达，与白名单正交。
4. 权限判定主体按 `Tool` 的类别决定：命令类给命令段、路径类给路径、MCP 类给**结构化的参数对**（而不是拼接字符串），从而可以表达 `mcp__qconfig__changeqconfig(appid=100049128)`。

---

## 五、与生态对齐

### 5.1 MCP 配置：直接吃 `mcpServers`

外部文件（`~/.local-agent/mcp.json` 或项目内 `.local-agent/mcp.json`）直接用生态格式；`tools.json` 里的 MCP 条目改成同一形状（或干脆把 MCP 来源独立成文件）。目标是**粘贴官方片段即可生效**，并提供导出（复制成官方格式贴给别人）。

### 5.2 参数 schema 直通

MCP 的 `inputSchema` 原样保存并原样喂给模型（OpenAI 与 Anthropic 两条协议都支持完整 JSON Schema；Anthropic 的 `input_schema` 就是标准 schema）。`toAnthropicInputSchema` 需要改为接受完整 schema 而非 `LLMToolParams`。

---

## 六、迁移方案

1. **读取兼容**：加载 `tools.json` 时识别旧形状（`type: mcp` + `config.transport`）与新形状，旧数据在内存里映射成 `ToolSource`，不要求用户立刻改文件。
2. **落盘升级**：在"保存过任意工具配置"或"MCP 条目被编辑过"时写成新形状，并保留一份 `tools.json.bak`。
3. **白名单兼容**：旧会话的 `EnabledTools` 里存的是来源 ID；迁移时按"该来源当前发现的全部子工具"展开，或保留"来源 ID 命中即放行其全部子工具"的兼容分支（建议前者 + 一次性迁移，语义更干净）。
4. **权限规则**：新增 `mcp__server__tool(key=value)` 语法；旧规则（整工具名）继续有效，语义不变。
5. **UI**：设置页把"来源"和"工具"分成两级（来源列表 → 展开看它提供的工具与状态），白名单选择器改为按来源分组、勾选到工具粒度。

---

## 七、分阶段实施

| 阶段 | 内容 | 验收 |
|---|---|---|
| **P0** | 参数 schema 直通（含 `required`/`enum`/嵌套），MCP 与内置一视同仁 | 用 `changeqconfig` 这类带嵌套参数的工具实测：模型一次就能给出正确形状；补单测断言 schema 不被裁剪 |
| **P1** | 导入/导出官方 `mcpServers` 格式（粘贴即用，可导出） | 把官方片段直接粘进设置页即可连通；不再需要模型翻译 |
| **P2** | 白名单统一到工具粒度；`Source.Enabled` 与白名单职责分离 | 一个会话可以只启用某台 MCP 服务器的两个子工具；旧会话迁移后行为不变 |
| **P3** | 概念分层落地（`ToolSource` / `Tool` / `Exposure`）与 UI 两级列表；权限规则补 MCP specifier | 设置页能分清"来源/工具"；`mcp__qconfig__changeqconfig(appid=100049128)` 可精确授权 |

P0/P1 是低风险高收益（不动存储结构即可做，P1 只加一个导入导出通道）；P2/P3 会动 `tools.json` 与 UI 结构，建议在 P0/P1 验收后再排。

---

## 八、风险与待拍板问题

1. **`tools.json` 是否要迁移**：只做兼容读取（旧形状长期保留）成本最低，但"两个形状并存"本身又是一种概念混乱。倾向前者 + 明确的"保存即升级"。
2. **MCP 是否独立成一个文件**：独立成 `mcp.json` 更贴生态（可直接复制别人分享的片段），但设置页要跨两个文件读写。
3. **`Tool.Exposure` 是否要开放给用户**：目前"直出 vs 经 router"是代码里写死的（`directToolOrder`）。若开放，用户可以自己决定哪些工具直出（省 token 还是少一轮往返）。
4. **权限规则语法扩展的兼容性**：`Tool(spec)` 目前对 MCP 是"参数摘要字符串"，若改成"结构化键值对"，旧的意外可用规则会失效——需要一次性的迁移提示。
5. **命名统一后 UI 是否更啰嗦**：设置页改显示 `mcp__server__tool` 会让列表变长（可考虑主标题用原始名 + 副标题显示完整名，但"复制去写规则"要复制完整名）。

---

## 九、附录

### 9.1 字段 ↔ 概念映射（现状 → 目标）

| 现字段 | 实际语义 | 目标归属 |
|---|---|---|
| `ToolConfig.Type` | 来源类型 | `ToolSource.Kind` |
| `ToolConfig.Config` | 来源特有配置 | `ToolSource.Config`（带 Kind 校验） |
| `ToolConfig.Enabled` / `Builtin` | 来源的启停 / 是否内置 | `ToolSource.Enabled` / `Kind == builtin` |
| `ToolConfig.DisabledTools` / `Discovered` | MCP 来源的子工具状态 | 由来源派生的 `Tool` 列表状态 |
| `ToolConfig.Parameters` | CLI/API 的参数声明 | `Tool.Schema`（统一成 JSON Schema） |
| `ToolConfig.Name/Description/Icon/Label` | 工具展示信息 | `Tool.Name/Description` + 展示层字段 |
| `Session.EnabledTools` | 按类型粒度不一的过滤 | 统一为工具白名单 |
| `directToolOrder` | 暴露策略（写死） | `Tool.Exposure` |

### 9.2 涉及面（预估）

新增：`toolsource.go`（来源模型与解析）、`toolschema.go`（schema 直通与两条协议的转换）、`mcpimport.go`（官方格式导入导出）。
修改：`toolstore.go`（存储与迁移）、`tools.go`（装配：来源 → 工具）、`toolruntime.go`（MCP 装配与子工具命名）、`permission.go`（MCP 主体结构化）、`app.go`（bound 方法与校验）、前端 `ToolSettings.vue` / `ToolEditDialog.vue` / `NewSessionDialog.vue` / 会话白名单语义。

### 9.3 本次评审建议的默认路径

先做 **P0（schema 直通）+ P1（官方格式导入导出）**，两周内可验收且不动存储结构；P2/P3 视 P0/P1 的实际收益再决定。理由：问题 5、6 是**已经在产生返工**的（今天各发生一次），而问题 1–4 属于"结构性不清"，改动面大、需要更多验证时间。

---

## 十、实施状态（2026-09-17）

### 已实现

**P0 参数 schema 直通**

- `LLMToolDef.Parameters` 从 `*LLMToolParams`（只有 type/description 的结构体）改为 `json.RawMessage`：**完整 JSON Schema 原样直通**，两条协议都不再裁剪。
- 新增 `SchemaProvider` 接口（`tools.go`）；`MCPTool` 实现它，保存服务器给的原始 `inputSchema`（`toolruntime.go` 的 `normalizeInputSchema`：确保是对象、确保有 `type`，其余原样保留；非对象或超过 16KB 时返回 nil 并降级为简化表，同时打日志）。
- `tool_router` 的 `describe` 也优先回传完整 schema —— MCP 子工具只能经路由器发现，`describe` 是模型了解其参数结构的唯一入口，这里压平等于让模型猜。
- Anthropic 侧改为 `inputSchemaFor`：直接吃标准 JSON Schema（Anthropic 与 OpenAI 形状一致），只兜底「必须是对象、必须有 type」。
- 内置/CLI/API 工具仍由简化参数表合成 schema（`buildJSONSchema`），`required` 不再丢失，无必填时不再输出 `"required": null`。

**P1 官方 mcpServers 导入导出**（新增 `mcpimport.go`）

- `ImportMCPServers(raw)`：接受官方片段，兼容三种形态——`{"mcpServers": {...}}`、去掉外层包装的名字字典、单个服务器对象（名字取 `name` 字段，或从 url host / command 推断）；容忍被 json 代码块包裹的内容；`streamable-http|http|sse|stdio` 映射到内部 `transport`；识别 Cline 的 `disabled` 字段；同名重复导入 = 更新（幂等）。
- `ExportMCPServers(names)`：反向导出为官方片段（`http → streamable-http`），支持传服务器名或 `mcp__server__tool` 工具名。
- 逐条报告结果：`MCPImportResult{imported[], skipped[{name, reason}]}` —— 跳过的原因如实展示，不静默吞掉。
- 前端：设置页工具栏新增「导入 MCP」「导出 MCP」，弹窗支持粘贴+结果反馈、导出+一键复制（剪贴板被 webview 限制时降级为手动复制）。

### 刻意未动（属 P2/P3）

- **存储结构**：`tools.json` 仍是 `[{"type":"mcp","config":{"transport":...}}]`，导入是"官方形状 → 现有结构"的单向转换。P1 只让用户不必理解两套格式，不改变落盘形状（迁移方案见第六节）。
- **白名单语义**：`Session.EnabledTools` 仍按 `cfg.ID` 过滤，对 MCP 是服务器级。
- **权限主体**：`MCPTool` 仍未实现 `SubjectProvider`，规则里没有 MCP specifier 语义。
- **命名**：UI 里 MCP 子工具仍显示原始名，与模型/规则用的 `mcp__server__tool` 不一致。

### 验收要点

- 参数形状：用带嵌套/枚举参数的 MCP 工具（如 `changeqconfig`）实测，模型应一次给出正确形状；`schema_test.go` 断言 enum/items/嵌套/required 均不被裁剪。
- 格式互操作：把官方片段粘进设置页即可连通；`export → import` 往返一致（`mcpimport_test.go`）。
- 待本地执行：`go build ./... && go test ./...`（新增 `schema_test.go`、`mcpimport_test.go`，以及两处既有测试从 `Parameters.Required` 改为解析 raw schema）。

---

## 十一、P2 / P3 实施状态（2026-09-17，接第十节）

### 落盘升级到 v2（新增 `toolmodel.go`）

- 类型换成 **`ToolSource`（来源）**：`kind`（builtin/cli/http/mcp）替代原 `type`（原 `api` 迁移为 `http`）；**类型化配置字段** `cli` / `http` / `mcp` 取代弱类型 `config json.RawMessage`，编译期即可约束「kind=mcp 就必须有 mcp 配置」。
- **MCP 配置改成官方形状**（`type: streamable-http|sse|stdio` + url/headers/command/args/env），与 Claude Desktop / Cline / MCP 文档一致 —— 导入导出因此几乎不需翻译（`mcpimport.go` 只剩搬字段与校验）。
- 落盘结构：`{"version":2,"sources":[...]}`；加载时识别 v1 裸数组并**就地迁移**，迁移前写一份 `tools.json.bak`（已存在则不覆盖），迁移后落 v2。
- 新增 `Exposure`（暴露策略）：`direct` 直出 / `router` 经路由器（默认）/ `internal` 模型完全不可见（连 `list`/`describe` 都不出现）。原意是"内置的既有行为由 `DefaultExposure` 复现"，改为可在编辑弹窗里调整——**但这条当时没做到**，`DefaultExposure` 与运行期的取值都不对，见下节「曝光策略的两处推导缺陷与修复」。

### 白名单改为工具粒度

`Session.EnabledTools` 现在按**工具名**过滤（内置/CLI/HTTP 是工具名本身，MCP 子工具用完整名 `mcp__server__tool`）。兼容：老会话里存的是来源名（MCP 当时按服务器过滤），若命中某台 MCP 来源则视为放行其全部子工具，旧会话无需迁移。

### MCP 权限主体结构化

新增 `SubjectKind = mcp`：主体是**结构化的参数对**（每个参数一项 `k=v`，键名排序），而不是拼接后的摘要字符串。规则可以精确表达 `mcp__qconfig__changeqconfig(appid=100049128)`：
- 收紧方向（deny/ask）：**任一参数对命中**即成立。
- 放宽方向（allow/授权）：走 `coversMCPUnits` —— **规则侧每一条都必须被调用的参数满足**（与命令段相反；命令段是"主体每段都要被覆盖"）。
- MCP **不支持前缀规则**：`k=v` 这种短串上前缀极易误伤（`appid=1` 会命中 `appid=100049128`），需要精确授权就写完整参数对。写前缀则永不匹配 → 方向安全（询问）。

### UI：工具配置按来源分组，MCP 独立成区

- 列表分三组：**内置工具 / MCP 服务器 / 自定义工具（CLI·HTTP）**，组头带说明。粒度差异在界面上直接可见。
- MCP 卡片显示接入方式与地址（`streamable-http · https://…`）、连接状态与子工具数、暴露策略徽标、整台服务器的启停开关。
- 展开后**每个子工具带独立开关**（写入 `DisabledTools`；此前只有读展示，根本没有 setter —— 这是 P3 补上的能力），右侧显示完整名 `mcp__server__tool` 并**可一键复制**（此前 UI 显示原始名，写权限规则要自己拼前缀）。
- 编辑弹窗按 kind 给字段（MCP 用官方三取值 + 官方字段名），并新增暴露策略选择；导入/导出按钮保留在工具栏。

### 决策记录（原 §8 的开放问题）

1. **`tools.json` 迁移**：采用"兼容读取 + 保存即升级 + 备份"，不做双格式并存。
2. **MCP 不独立成文件**：仍在一个 `tools.json` 里，但 MCP 条目已是官方形状 —— 生态互导由导入/导出通道承担，不必再拆文件。
3. **Exposure 开放给用户**：可改（编辑弹窗），默认值复现既有行为。
4. **权限语法兼容**：MCP 从"参数摘要字符串"改为结构化参数对，此前"意外可用"的前缀规则会失效（方向安全，落到询问）；非 MCP 工具规则语义不变。
5. **UI 命名**：子工具行同时显示原始名与完整名，并提供复制按钮。

### 曝光策略的两处推导缺陷与修复（2026-09-17，exec_shell 消失事故）

跑起来后出现「模型报找不到 exec_shell」。根因不在技能机制，而在曝光策略的**推导**，两处缺陷叠加：

1. **默认值本身是错的。** `DefaultExposure` 写成「内置但不在 `directToolOrder` 里 → `ExposureInternal`」，而 `exec_shell` 恰好是 `defaultSources()` 里唯一不在该名录的内置工具。于是它被判成"模型完全不可见"：`listTools` 跳过、`describe`/`execute` 当"未找到"。**`internal` 不能由"没配置"推断出来**——否则以后任何新增内置工具，只要忘了加进 `directToolOrder`，就会对模型凭空消失。
2. **运行期不查默认值。** `markExposure` 对空/非法 exposure 一律降级成 `ExposureRouter`，而 `defaultSources()` 根本不设 `exposure` 字段（`omitempty` 落盘时被省略）、`load()` 也不做 normalize。结果是**全新安装下连文件六件套都不是直出的**，`directToolOrder` 那套"直出省一轮往返"的设计只在配置被 normalize 或 migrate 过之后才生效。这个回归其实已被既有测试 `TestFileToolsAssembledAndDirectExposed` 抓到（它断言 `tool_router` + 全部 `directToolOrder` 都直出），只是还没跑。

**为什么会被持久化：** `migrateLegacySource` 对每个 v1 条目执行 `src.Exposure = DefaultExposure(...)`，`load()` 迁移完又立刻 `save()`，于是 `"exposure": "internal"` 被写进 `tools.json`；在设置页保存任意工具走 `normalizeSource` 也会算出同一个值。**所以只改默认值不够，磁盘上已有的错值必须一并修。**

修复分三层：`DefaultExposure` 兜底改为 `ExposureRouter`，`internal` 只能由配置显式指定；`markExposure` 对空/非法值改为调用 `DefaultExposure(kind, name)` 而非硬编码 router（该闭包因此多收一个 `SourceKind` 参数，6 个调用点同步更新）；`ToolFileVersion` 2 → 3 并新增 `repairBuiltinExposure`，加载低版本文件时把「内置 且 `internal`」的条目恢复成默认曝光并落盘——只碰这一类，用户显式配的 direct/router 与非内置来源的 internal 一律不动。

新增回归测试（`tool_runtime_test.go`）：`DefaultExposure` 不得把任何内置工具判成 internal；全新安装下 `read_file` 直出、`exec_shell` 可经 `tool_router` 发现（`list` 与 `describe`）；`repairBuiltinExposure` 的作用域；以及加载 v2 文件后错值被修、版本落为 3、模型能重新发现 `exec_shell`。

**一个连带教训（技能侧）**：这次还有个放大因素——skills 重构把默认示例技能改成自带 `scripts/greet.sh`，并在 L1 清单、`read_skill` 返回值、显式调用注入段里都写了"脚本用 exec_shell 执行"。于是装上一个全新技能，模型立刻照着做、立刻撞上不可见的工具。**在提示词里点名某个工具前，先确认它在当前装配下真的可见**；跨子系统的名字引用不是自己能保证的。

### 验证边界（重要）

- 前端：30 个 SFC 全部编译通过、27 个 JS 文件语法通过（沙箱内可执行）。
- Go：**沙箱无 Go 工具链，未编译、未跑测试**。已做的是静态核对（括号平衡用项目既有 `/tmp/go_lex_check.py`、未使用 import、重复声明、字段/方法解析、`ToolSource`/`MCPConfig` 成员访问按变量名核对）。**必须本地 `go build ./... && go test ./...` 验收**。
- 新增/更新的测试：`mcpimport_test.go`（导入三形态、跳过原因、幂等、导出往返、v1→v2 迁移含备份校验、子工具开关与暴露策略）、`schema_test.go`（schema 直通）、`tool_runtime_test.go`（来源模型下的装配与白名单）。
