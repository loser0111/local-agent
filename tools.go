package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"wails-tmp/memory"
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
	hideConsoleWindow(cmd) // Windows 上不弹控制台窗口（见该函数说明）
	if t.Dir != "" {
		cmd.Dir = t.Dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// PermissionSubject 声明判定主体：命令类，判定发生在真正要执行的命令上（逐段分解）
func (t *CLITool) PermissionSubject(args map[string]interface{}) Subject {
	cmdStr, _ := args["cmd"].(string)
	return newCommandSubject(t.GetName(), cmdStr)
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

// ===== 工具管理器（配置驱动装配）=====

// ToolManager 持有工具配置存储、技能存储与 MCP 连接池，按会话构建工具视图
type ToolManager struct {
	store  *ToolStore
	skills *SkillStore
	pool   *MCPPool
}

func NewToolManager(store *ToolStore, skills *SkillStore) *ToolManager {
	return &ToolManager{store: store, skills: skills, pool: NewMCPPool()}
}

// Skills 暴露技能存储
func (tm *ToolManager) Skills() *SkillStore { return tm.skills }

// Pool 暴露 MCP 连接池（供连接测试使用）
func (tm *ToolManager) Pool() *MCPPool { return tm.pool }

// Store 暴露配置存储
func (tm *ToolManager) Store() *ToolStore { return tm.store }

// SessionView 某次会话的工具视图（已按启用状态、会话白名单与暴露策略过滤）
type SessionView struct {
	nonMeta map[string]ToolInterface
	meta    *MetaTool
	// direct 直出给模型的工具名，顺序稳定；由来源的 exposure 决定（不再写死在代码里）
	direct []string
}

// BuildOptions 装配工具视图的会话级参数
type BuildOptions struct {
	EnabledTools  []string       // 会话工具白名单；为空表示全部已启用工具
	EnabledSkills []string       // 会话技能白名单；为空表示全部已启用技能
	ProjectDir    string         // 会话工作区目录：工具的工作目录，也是权限判定的边界
	SessionID     string         // 会话 ID（文件改动的归因记录按会话隔离）
	Changes       *FileChangeLog // 文件改动记录；为 nil 时不记录
	Enforcer      Enforcer       // 权限网关；为空时装配 fail-closed 的拒绝网关
	// Ask 用户提问回路（ask_user 工具）：发事件给前端并阻塞等待作答。
	// 为 nil 时该工具仍会装配，但调用会返回"界面未就绪"——便于测试与降级。
	Ask func(ctx context.Context, req AskRequest) (AskAnswer, error)
	// ExcludeTools 显式排除的工具名（优先级高于白名单）。
	// 子代理用它去掉 ask_user——它没有 UI 通道，留着一个永远失败的工具只会浪费模型一轮。
	ExcludeTools []string
	// SpawnAgent 派生代理的回路（spawn_agent 工具）。
	// 为 nil 时该工具**不注册**——这正是"子代理不能再次派生"的实现方式：
	// 不给它 spawner，工具就不存在，比注册后再拦截更干净。
	SpawnAgent func(ctx context.Context, task string, maxTurns int) (*SubagentResult, error)
	// Memory 长期记忆库；为 nil 时不注册 memory_* 工具。
	Memory        *memory.MemoryStore
	MemoryProject string
	IgnoreMemory  bool
	OnMemoryTouch func([]string)
}

// buildContext 装配期传给内置工具构造函数的上下文
type buildContext struct {
	dir     string                                                       // 工作区目录（命令类工具的工作目录）
	fileCtx fileToolContext                                              // 文件类工具的上下文（目录 + 归因记录）
	asker   func(ctx context.Context, req AskRequest) (AskAnswer, error) // ask_user 的回路
	// spawner 派生代理的回路；为 nil 时 spawn_agent 不注册（子代理因此无法再派生）
	spawner func(ctx context.Context, task string, maxTurns int) (*SubagentResult, error)
	memory  memoryToolContext
}

// newBuiltinTool 构造内置工具。
// 返回 ok=false 表示该内置工具尚未实现（例如用户自定义的非内置项混进来）。
func newBuiltinTool(src *ToolSource, bc buildContext) (ToolInterface, bool) {
	switch src.Name {
	case toolExecShell:
		return &CLITool{
			BaseTool: &BaseTool{
				Name: src.Name, Description: src.Description, Parameters: paramsFromConfig(src.Parameters),
			},
			Dir:      bc.dir,
			required: requiredFromConfig(src.Parameters),
		}, true
	case toolReadFile:
		return newReadFileTool(bc), true
	case toolWriteFile:
		return newWriteFileTool(bc), true
	case toolEditFile:
		return newEditFileTool(bc), true
	case toolGlob:
		return newGlobTool(bc), true
	case toolGrep:
		return newGrepTool(bc), true
	case toolListDir:
		return newListDirTool(bc), true
	case toolAskUser:
		return newAskUserTool(bc), true
	case toolSpawnAgent:
		// 没有 spawner 就不注册：子代理视图刻意不给它，于是它无法再次派生
		if bc.spawner == nil {
			return nil, false
		}
		return newSpawnAgentTool(bc), true
	case toolMemorySearch:
		return newMemorySearchTool(bc.memory), true
	case toolMemorySave:
		return newMemorySaveTool(bc.memory), true
	case toolMemoryForget:
		return newMemoryForgetTool(bc.memory), true
	default:
		return nil, false
	}
}

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
func llmToolFor(t ToolInterface) LLMTool {
	return LLMTool{
		Type: ToolTypeFunction,
		Function: LLMToolDef{
			Name:        t.GetName(),
			Description: t.GetDescription(),
			Parameters:  schemaForTool(t),
		},
	}
}

// BuildView 依据配置、会话白名单与权限网关装配工具。
//
// 装配期统一装饰：registry 里每个工具都包一层 guardedTool，因此
//   - tool_router 的分发路径（MetaTool 持有同一个 map）会经过网关；
//   - 直调路径（SessionView.ExecuteTool）会经过网关；
//   - 新增工具类型只需往 registry 写一次，不会漏掉判定。
//
// tool_router 自身不装饰：它只做发现与分发（list / describe 无副作用，execute 落到
// 上面已装饰的工具上），装饰它反而会对同一次调用判两遍、弹两次窗。
//
// MCP server 连接失败不阻断装配，仅跳过其子工具（错误在设置页状态中展示）。
func (tm *ToolManager) BuildView(ctx context.Context, opts BuildOptions) *SessionView {
	registry := map[string]ToolInterface{}
	typeMap := map[string]string{}
	whitelist := map[string]bool{}
	for _, id := range opts.EnabledTools {
		whitelist[id] = true
	}

	// 工作目录只在真实存在时生效，避免把命令的 cwd 设到一个不存在的路径
	dir := ""
	if strings.TrimSpace(opts.ProjectDir) != "" {
		if st, err := os.Stat(opts.ProjectDir); err == nil && st.IsDir() {
			dir = opts.ProjectDir
		}
	}
	bc := buildContext{
		dir: dir,
		fileCtx: fileToolContext{
			root:      dir,
			sessionID: opts.SessionID,
			changes:   opts.Changes,
		},
		asker:   opts.Ask,
		spawner: opts.SpawnAgent,
		memory: memoryToolContext{
			store:   opts.Memory,
			project: opts.MemoryProject,
			source:  opts.SessionID,
			ignore:  opts.IgnoreMemory,
			onTouch: opts.OnMemoryTouch,
		},
	}
	// exposure：工具名 → 暴露策略（直出 / 经路由器 / 不可见）
	exposure := map[string]Exposure{}
	hidden := map[string]bool{}
	markExposure := func(kind SourceKind, toolName string, e Exposure) {
		if !ValidExposure(e) {
			// 没配置（或配置非法）时**按默认推断**，而不是一律降级成 router：
			// 内置的文件工具本该直出，若运行期降级，只要配置没被 normalize/migrate 过
			// （全新安装就是这种情况），它们就会悄悄退回"要经路由器发现"，
			// 与 DefaultExposure 的文档意图和 directToolOrder 的存在意义都不一致。
			e = DefaultExposure(kind, toolName)
		}
		exposure[toolName] = e
		if e == ExposureInternal {
			hidden[toolName] = true
			delete(exposure, toolName) // internal 不参与直出
		}
	}
	// 白名单按**工具名**判定：内置/CLI/HTTP 是工具名本身；MCP 子工具用完整名 mcp__server__tool
	// 排除名单优先于白名单：子代理要用它剔掉 ask_user（详见 BuildOptions.ExcludeTools）
	excluded := map[string]bool{}
	for _, n := range opts.ExcludeTools {
		excluded[n] = true
	}
	allowed := func(toolName string) bool {
		if excluded[toolName] {
			return false
		}
		return len(whitelist) == 0 || whitelist[toolName]
	}
	// 兼容 v2 之前的会话白名单：那时 MCP 是按来源 ID（= 服务器名）过滤的，
	// 现在白名单是工具级。老会话里存的服务器名若命中某台 MCP 来源，视为放行其全部子工具，
	// 这样旧会话不用迁移也不会突然丢失全部 MCP 工具。
	legacyMCPAllowed := map[string]bool{}
	for name := range whitelist {
		if src, ok := tm.store.GetByName(name); ok && src.Kind == SourceMCP {
			legacyMCPAllowed[src.Name] = true
		}
	}

	for _, src := range tm.store.GetAll() {
		if !src.Enabled || !ValidSourceKind(src.Kind) {
			continue
		}
		switch src.Kind {
		case SourceBuiltin:
			if !allowed(src.Name) {
				continue
			}
			t, ok := newBuiltinTool(src, bc)
			if !ok {
				continue
			}
			registry[src.Name] = t
			typeMap[src.Name] = string(SourceBuiltin)
			markExposure(src.Kind, src.Name, src.Exposure)
		case SourceCLI:
			if !allowed(src.Name) {
				continue
			}
			t := NewDynamicCLITool(src)
			t.Dir = dir
			registry[src.Name] = t
			typeMap[src.Name] = string(SourceCLI)
			markExposure(src.Kind, src.Name, src.Exposure)
		case SourceHTTP:
			if !allowed(src.Name) {
				continue
			}
			t := NewDynamicAPITool(src)
			registry[src.Name] = t
			typeMap[src.Name] = string(SourceHTTP)
			markExposure(src.Kind, src.Name, src.Exposure)
		case SourceMCP:
			// 连接失败不阻断装配：仅跳过其子工具（错误在设置页状态里展示）
			entry, err := tm.pool.Connect(ctx, src)
			if err != nil {
				fmt.Printf("[ToolManager] MCP 来源 %s 装配失败: %v\n", src.Name, err)
				continue
			}
			// 注意：这里必须遍历服务器返回的原始工具（带 InputSchema）来构造 MCPTool；
			// src.AvailableSubTools() 只是给界面用的摘要（名字+描述），信息不足以建工具。
			disabledSub := map[string]bool{}
			for _, d := range src.DisabledTools {
				disabledSub[d] = true
			}
			for _, mt := range entry.tools {
				if mt == nil || disabledSub[mt.Name] {
					continue
				}
				full := src.ToolNameFor(mt.Name)
				if !allowed(full) && !legacyMCPAllowed[src.Name] {
					continue
				}
				wrapped := NewMCPTool(tm.pool, src, mt)
				registry[full] = wrapped
				typeMap[full] = string(SourceMCP)
				markExposure(src.Kind, full, src.Exposure)
			}
		}
	}

	// 内置：技能加载工具（存在本会话可用技能时注册，经 tool_router 被发现）
	if tm.skills != nil {
		if rst := NewReadSkillTool(tm.skills, opts.EnabledSkills); rst.HasAvailable() {
			registry[rst.GetName()] = rst
			typeMap[rst.GetName()] = string(SourceBuiltin)
			markExposure(SourceBuiltin, rst.GetName(), ExposureRouter)
		}
		// L3：读取技能自带资源（不排除 disable-model-invocation 的技能——
		// 用户显式调用某技能后，正文里引用的资料仍要读得到）
		if rft := NewReadSkillFileTool(tm.skills, opts.EnabledSkills); rft.HasAvailable() {
			registry[rft.GetName()] = rft
			typeMap[rft.GetName()] = string(SourceBuiltin)
			markExposure(SourceBuiltin, rft.GetName(), ExposureRouter)
		}
	}

	// 统一装饰（必须在 NewMetaTool 之前：路由器持有的是同一个 map）
	enforcer := opts.Enforcer
	if enforcer == nil {
		// 装配缺失绝不等于放行：用拒绝网关兜底，宁可工具不可用也不能静默放行
		fmt.Printf("[ToolManager] 未提供权限网关，已装配拒绝网关（fail closed）\n")
		enforcer = DenyAllEnforcer{}
	}
	for name, t := range registry {
		registry[name] = &guardedTool{inner: t, enf: enforcer}
	}

	return &SessionView{
		nonMeta: registry,
		meta:    NewMetaTool(registry, typeMap, hidden),
		direct:  directToolNames(exposure),
	}
}

// directToolNames 计算直出名录：先按既有的稳定顺序（directToolOrder）列出内置文件工具等，
// 再补上配置里额外标为 direct 的来源（按名字排序，保证顺序稳定）。
func directToolNames(exposure map[string]Exposure) []string {
	out := make([]string, 0, len(exposure))
	seen := map[string]bool{}
	for _, name := range directToolOrder {
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

// GetToolsForLLM 返回给 LLM 的工具定义。
//
// 直出策略：常用文件工具（read_file / write_file / edit_file / glob / grep / list_dir）
// 直接暴露，模型不必先 list/describe 再 execute，少一轮往返；
// 其余工具（MCP / 自定义 CLI / API / read_skill）仍经 tool_router 发现，避免 prompt 膨胀。
//
// 两条路径（直调与经路由器）落到同一个 registry，因此都会被权限网关拦住。
func (v *SessionView) GetToolsForLLM() []LLMTool {
	defs := make([]LLMTool, 0, len(v.direct)+1)
	for _, name := range v.direct {
		if t, ok := v.nonMeta[name]; ok {
			defs = append(defs, llmToolFor(t))
		}
	}
	defs = append(defs, LLMTool{
		Type: "function",
		Function: LLMToolDef{
			Name:        v.meta.GetName(),
			Description: v.meta.GetDescription(),
			Parameters:  json.RawMessage(routerToolSchema),
		},
	})
	return defs
}

// ExecuteTool 在会话视图内执行工具
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
	tool, ok := v.nonMeta[name]
	if !ok {
		return "", fmt.Errorf("未找到工具: %s", name)
	}
	return tool.Execute(ctx, args)
}
