package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"wails-tmp/internal/llm"
	"wails-tmp/internal/media"
	"wails-tmp/internal/permission"
	"wails-tmp/internal/procx"
)

// ===== 工具接口定义（借鉴 01agent 的 ToolInterface）=====

// ToolInterface 工具统一接口
type ToolInterface interface {
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
	GetName() string
	GetDescription() string
	GetParameters() map[string]*ToolArgDef
}

// ToolArgDef 工具参数定义
type ToolArgDef struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// BaseTool 工具基础结构
type BaseTool struct {
	Name        string
	Description string
	Parameters  map[string]*ToolArgDef
}

func (t *BaseTool) GetName() string                       { return t.Name }
func (t *BaseTool) GetDescription() string                { return t.Description }
func (t *BaseTool) GetParameters() map[string]*ToolArgDef { return t.Parameters }

// ===== CLI 内置工具 =====

// CLITool 命令行工具：在本机终端执行 shell 命令
type CLITool struct {
	*BaseTool
	// Dir 工作目录（会话项目目录）。为空时继承进程工作目录。
	// 设定它的意义：让相对路径有确定含义，权限判定的"项目目录内/外"才有依据。
	Dir string
	// required 来源配置里标了 Required 的参数名（exec_shell 的 cmd 就在其中）。
	// 必须实现 RequiredParams() 把它交给 schemaForTool，模型才知道哪个参数必填；
	// 否则 Execute 里那句"cmd 参数是必需的"只有代码自己知道，模型只能靠猜。
	required []string
}

// NewCLITool 构造内置终端工具（required 只能在此处注入，见字段说明）
func NewCLITool(name, description string, params map[string]*ToolArgDef, required []string, dir string) *CLITool {
	return &CLITool{
		BaseTool: &BaseTool{Name: name, Description: description, Parameters: params},
		Dir:      dir,
		required: required,
	}
}

// RequiredParams 透传来源配置声明的必填参数
func (t *CLITool) RequiredParams() []string { return t.required }

func (t *CLITool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	cmdStr, ok := args["cmd"].(string)
	if !ok {
		return "", fmt.Errorf("cmd 参数是必需的")
	}
	if ctx == nil {
		ctx = context.Background() // exec.CommandContext 不接受 nil ctx
	}
	// 必须用 CommandContext：否则硬取消杀不掉正在跑的命令，
	// "点了停止"要等到命令自己结束（可能是几分钟）才生效。
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-Command", cmdStr)
	} else {
		cmd = exec.CommandContext(ctx, "bash", "-c", cmdStr)
	}
	procx.HideConsoleWindow(cmd) // Windows 上不弹控制台窗口（见该函数说明）
	if t.Dir != "" {
		cmd.Dir = t.Dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// PermissionSubject 声明判定主体：命令类，判定发生在真正要执行的命令上（逐段分解）
func (t *CLITool) PermissionSubject(args map[string]interface{}) permission.Subject {
	cmdStr, _ := args["cmd"].(string)
	return permission.NewCommandSubject(t.GetName(), cmdStr)
}

// ===== 元工具：工具路由器（借鉴 01agent 的 MetaTool）=====

// MetaTool 元工具：支持 list（发现/搜索工具）、describe（查看参数）和 execute（执行工具）
type MetaTool struct {
	*BaseTool
	registry map[string]ToolInterface // 非元工具注册表（已按会话白名单过滤）
	typeMap  map[string]string        // 工具名 → 来源类型标签（builtin/cli/http/mcp）
	// hidden 暴露策略为 internal 的工具：模型完全看不到——list 不列出、describe/execute 一律当"未找到"。
	// 与"经路由器发现"（router）区分：router 是模型能看到但要先发现，internal 是根本不给模型看。
	hidden map[string]bool
}

func NewMetaTool(registry map[string]ToolInterface, typeMap map[string]string, hidden map[string]bool) *MetaTool {
	return &MetaTool{
		BaseTool: &BaseTool{
			Name: "tool_router",
			Description: "工具路由器。通过此工具发现和执行所有已启用的工具。" +
				"工具较多时用 action=\"list\" 配合 query 按关键字搜索；" +
				"用 action=\"describe\" 查看目标工具的完整参数定义；" +
				"最后用 action=\"execute\" 执行指定工具。" +
				"注意：工具列表里已直接给出的工具（如 read_file、exec_shell）无需先经此路由器，" +
				"直接调用即可；只有目标工具未出现在工具列表中时，才需要用本工具去发现。",
			Parameters: map[string]*ToolArgDef{
				"action":    {Type: "string", Description: "操作类型：list=列出/搜索可用工具；describe=查看工具参数定义；execute=执行指定工具"},
				"query":     {Type: "string", Description: "当 action=list 时可选，按工具名或描述的关键字过滤（如 文件、天气、mcp）"},
				"tool_name": {Type: "string", Description: "action=describe/execute 时必填，目标工具名称（从 list 结果中获取）"},
				"arguments": {Type: "object", Description: "当 action=execute 时必填，传递给目标工具的参数对象"},
			},
		},
		registry: registry,
		typeMap:  typeMap,
		hidden:   hidden,
	}
}

// visible 该工具是否对模型可见（internal 的不可见）
func (t *MetaTool) visible(name string) bool {
	return !t.hidden[name]
}

func (t *MetaTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	switch action {
	case "list":
		query, _ := args["query"].(string)
		return t.listTools(query)
	case "describe":
		return t.describeTool(args)
	case "execute":
		return t.executeTool(ctx, args)
	default:
		return "", fmt.Errorf("未知的 action: %s，可选值: list, describe, execute", action)
	}
}

// toolBrief list 返回的精简项（控制 token 消耗）
type toolBrief struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// toolDetail describe 返回的完整定义
type toolDetail struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"` // 完整 JSON Schema（有则直通）或简化参数表
}

// listTools 返回工具清单；query 非空时按名称/描述模糊匹配
func (t *MetaTool) listTools(query string) (string, error) {
	names := make([]string, 0, len(t.registry))
	for name := range t.registry {
		names = append(names, name)
	}
	sort.Strings(names)

	q := strings.ToLower(strings.TrimSpace(query))
	briefs := make([]toolBrief, 0, len(names))
	for _, name := range names {
		if !t.visible(name) {
			continue
		}
		tt := t.registry[name]
		desc := tt.GetDescription()
		if q != "" {
			hay := strings.ToLower(name + " " + desc)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		briefs = append(briefs, toolBrief{
			Name:        name,
			Type:        t.typeMap[name],
			Description: desc,
		})
	}

	result, err := json.MarshalIndent(briefs, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化工具列表失败: %w", err)
	}
	if q != "" {
		return fmt.Sprintf("匹配 \"%s\" 的工具（共 %d 个）:\n%s", query, len(briefs), string(result)), nil
	}
	return fmt.Sprintf("可用工具列表（共 %d 个）。使用 describe 查看参数，或直接 execute 执行:\n%s", len(briefs), string(result)), nil
}

// describeTool 返回单个工具的完整参数定义
func (t *MetaTool) describeTool(args map[string]interface{}) (string, error) {
	toolName, _ := args["tool_name"].(string)
	if toolName == "" {
		return "", fmt.Errorf("tool_name 参数是必需的")
	}
	tt, exists := t.registry[toolName]
	if !exists || !t.visible(toolName) {
		return "", fmt.Errorf("未找到工具: %s", toolName)
	}
	// 有完整 schema 的工具（如 MCP 子工具）直接回传原始 JSON Schema：
	// MCP 工具只能经路由器发现，describe 是模型了解其参数结构的唯一入口，
	// 这里压平就等于让模型猜参数形状。
	var params interface{}
	if sp, ok := tt.(SchemaProvider); ok {
		if raw := sp.JSONSchema(); len(raw) > 0 {
			var schema map[string]interface{}
			if err := json.Unmarshal(raw, &schema); err == nil && len(schema) > 0 {
				params = schema
			}
		}
	}
	if params == nil {
		flat := make(map[string]interface{})
		for paramName, arg := range tt.GetParameters() {
			flat[paramName] = map[string]string{
				"type":        arg.Type,
				"description": arg.Description,
			}
		}
		params = flat
	}
	detail := toolDetail{
		Name:        toolName,
		Type:        t.typeMap[toolName],
		Description: tt.GetDescription(),
		Parameters:  params,
	}
	result, _ := json.MarshalIndent(detail, "", "  ")
	return string(result), nil
}

// executeTool 根据名称查找并执行目标工具
func (t *MetaTool) executeTool(ctx context.Context, args map[string]interface{}) (string, error) {
	toolName, _ := args["tool_name"].(string)
	if toolName == "" {
		return "", fmt.Errorf("tool_name 参数是执行工具时必需的")
	}

	targetTool, exists := t.registry[toolName]
	if !exists || !t.visible(toolName) {
		available := make([]string, 0, len(t.registry))
		for name := range t.registry {
			if !t.visible(name) {
				continue
			}
			available = append(available, name)
		}
		sort.Strings(available)
		return "", fmt.Errorf("未找到工具: %s，可用工具: %v", toolName, available)
	}

	arguments, ok := args["arguments"].(map[string]interface{})
	if !ok {
		arguments = make(map[string]interface{})
	}

	result, err := targetTool.Execute(ctx, arguments)
	if err != nil {
		return "", fmt.Errorf("工具 %s 执行失败: %w", toolName, err)
	}
	return result, nil
}

// routerToolSchema 工具路由器的参数 schema（静态，直接以 JSON Schema 形式维护）
const routerToolSchema = `{
  "type": "object",
  "required": ["action"],
  "properties": {
    "action": {"type": "string", "description": "操作类型：list=列出/搜索可用工具；describe=查看工具参数定义；execute=执行指定工具"},
    "query": {"type": "string", "description": "action=list 时可选，按关键字过滤工具"},
    "tool_name": {"type": "string", "description": "describe/execute 时的目标工具名"},
    "arguments": {"type": "object", "description": "execute 时传递给目标工具的参数对象"}
  }
}`

// RequiredParams 工具可声明的必填参数。
// 仅用于"没有完整 schema"的工具（内置/CLI/API）——它们的参数只有类型与说明，
// 必填信息单独声明后由 buildJSONSchema 合成 schema。
type RequiredParams interface {
	RequiredParams() []string
}

// SchemaProvider 工具可提供**完整的 JSON Schema**。
// MCP 工具用它把服务器给的 inputSchema 原样带出来，避免 enum/items/嵌套/required 被压平。
type SchemaProvider interface {
	JSONSchema() json.RawMessage
}

// maxToolSchemaBytes 传给模型的单个 schema 上限。超过时降级为简化 schema（仍然合法），
// 避免个别 server 的超大 schema 把 prompt 撑爆。
const maxToolSchemaBytes = 16 << 10

// buildJSONSchema 由简化参数表构造 JSON Schema（用于没有完整 schema 的工具）
func buildJSONSchema(params map[string]*ToolArgDef, required []string) json.RawMessage {
	props := make(map[string]map[string]string, len(params))
	for name, arg := range params {
		prop := map[string]string{"type": arg.Type}
		if strings.TrimSpace(arg.Description) != "" {
			prop["description"] = arg.Description
		}
		props[name] = prop
	}
	schema := map[string]interface{}{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return b
}

// schemaForTool 取工具的 JSON Schema：优先用工具自报的完整 schema（直通），
// 否则由简化参数表合成。任何情况下都返回合法的 JSON Schema 对象。
func schemaForTool(t ToolInterface) json.RawMessage {
	if sp, ok := t.(SchemaProvider); ok {
		if raw := sp.JSONSchema(); len(raw) > 0 && len(raw) <= maxToolSchemaBytes {
			return raw
		}
	}
	var required []string
	if rp, ok := t.(RequiredParams); ok {
		required = rp.RequiredParams()
	}
	return buildJSONSchema(t.GetParameters(), required)
}

// llmToolFor 把装配好的工具转成给 LLM 的工具定义
func llmToolFor(t ToolInterface) llm.LLMTool {
	return llm.LLMTool{
		Type: llm.ToolTypeFunction,
		Function: llm.LLMToolDef{
			Name:        t.GetName(),
			Description: t.GetDescription(),
			Parameters:  schemaForTool(t),
		},
	}
}

// ===== 会话工具视图 =====

// SessionView 某次会话的工具视图（已按启用状态、会话白名单与暴露策略过滤）
type SessionView struct {
	nonMeta map[string]ToolInterface
	meta    *MetaTool
	// direct 直出给模型的工具名，顺序稳定；由来源的 exposure 决定（不再写死在代码里）
	direct []string
	// images 本次装配的产图通道：read_image 这类工具把读到的图片放进来，
	// 由工具循环在调用返回后取走并落到该次调用结果的消息上（见 media.ImageCollector）。
	images *media.ImageCollector
}

// NewSessionView 装配结果（字段不可导出：注册表与路由器必须成对构造，见 BuildView）
func NewSessionView(nonMeta map[string]ToolInterface, meta *MetaTool, direct []string, images *media.ImageCollector) *SessionView {
	return &SessionView{nonMeta: nonMeta, meta: meta, direct: direct, images: images}
}

// Lookup 按名字取工具（含被网关装饰后的实例）；未找到返回 nil
func (v *SessionView) Lookup(name string) ToolInterface {
	if v == nil {
		return nil
	}
	return v.nonMeta[name]
}

// DirectNames 直出给模型的工具名（顺序已稳定）
func (v *SessionView) DirectNames() []string {
	if v == nil {
		return nil
	}
	return v.direct
}

// TakeImages 取出本次工具调用**刚产出**的图片（read_image 之类"结果里带图"的工具）。
//
// 必须在**每次 ExecuteToolCtx 之后立刻**调用：一次装配的工具共用同一个收集器，
// 中间不取走的话，"这两张图是哪一次调用产的"就再也分不出来了。
// 循环是串行执行 tool_calls 的，所以"执行一次、取一次"能保证归因准确。
func (v *SessionView) TakeImages() []media.Attachment {
	if v == nil {
		return nil
	}
	return v.images.Take()
}

// GetToolsForLLM 返回给 LLM 的工具定义。
//
// 直出策略：常用文件工具（read_file / write_file / edit_file / glob / grep / list_dir）
// 直接暴露，模型不必先 list/describe 再 execute，少一轮往返；
// 其余工具（MCP / 自定义 CLI / API / read_skill）仍经 tool_router 发现，避免 prompt 膨胀。
//
// 两条路径（直调与经路由器）落到同一个 registry，因此都会被权限网关拦住。
func (v *SessionView) GetToolsForLLM() []llm.LLMTool {
	defs := make([]llm.LLMTool, 0, len(v.direct)+1)
	for _, name := range v.direct {
		if t, ok := v.nonMeta[name]; ok {
			defs = append(defs, llmToolFor(t))
		}
	}
	defs = append(defs, llm.LLMTool{
		Type: "function",
		Function: llm.LLMToolDef{
			Name:        v.meta.GetName(),
			Description: v.meta.GetDescription(),
			Parameters:  json.RawMessage(routerToolSchema),
		},
	})
	return defs
}

// ExecuteTool 执行工具（不带 ctx）。
//
// ⚠️ 仅供测试与一次性调用：它内部用 context.Background()，因此**不会被硬取消中断**
// ——权限等待、子进程、HTTP 在途请求都感知不到取消信号。
// 运行循环必须用 ExecuteToolCtx 把运行 ctx 传进来。
func (v *SessionView) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	return v.ExecuteToolCtx(context.Background(), name, args)
}

// ExecuteToolCtx 执行工具，并把 ctx 一路传下去。
//
// 这个 ctx 是硬取消能否真正切断在途操作的唯一通路，链路上每一个环节都必须传：
// guardedTool → Enforcer.Enforce → permissionBroker.Wait 的 ctx.Done 分支；
// guardedTool → inner.Execute → exec_shell 的 CommandContext、HTTP 工具的在途请求。
// 只要有一环退回 context.Background()，取消就会退化成"等它自己跑完"。
func (v *SessionView) ExecuteToolCtx(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if name == "tool_router" {
		return v.meta.Execute(ctx, args)
	}
	t, ok := v.nonMeta[name]
	if !ok {
		return "", fmt.Errorf("未找到工具: %s", name)
	}
	return t.Execute(ctx, args)
}

// DirectToolNames 计算直出名录：先按既有的稳定顺序（DirectToolOrder）列出内置文件工具等，
// 再补上配置里额外标为 direct 的来源（按名字排序，保证顺序稳定）。
func DirectToolNames(exposure map[string]Exposure) []string {
	out := make([]string, 0, len(exposure))
	seen := map[string]bool{}
	for _, name := range DirectToolOrder {
		if exposure[name] == ExposureDirect {
			out = append(out, name)
			seen[name] = true
		}
	}
	extra := make([]string, 0)
	for name, e := range exposure {
		if e == ExposureDirect && !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}
