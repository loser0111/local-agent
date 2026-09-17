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
}

func (t *CLITool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	cmdStr, ok := args["cmd"].(string)
	if !ok {
		return "", fmt.Errorf("cmd 参数是必需的")
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", cmdStr)
	} else {
		cmd = exec.Command("bash", "-c", cmdStr)
	}
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
	typeMap  map[string]string        // 工具名 → 类型标签（builtin/cli/mcp/api）
}

func NewMetaTool(registry map[string]ToolInterface, typeMap map[string]string) *MetaTool {
	return &MetaTool{
		BaseTool: &BaseTool{
			Name: "tool_router",
			Description: "工具路由器。通过此工具发现和执行所有已启用的工具。" +
				"工具较多时用 action=\"list\" 配合 query 按关键字搜索；" +
				"用 action=\"describe\" 查看目标工具的完整参数定义；" +
				"最后用 action=\"execute\" 执行指定工具。",
			Parameters: map[string]*ToolArgDef{
				"action":    {Type: "string", Description: "操作类型：list=列出/搜索可用工具；describe=查看工具参数定义；execute=执行指定工具"},
				"query":     {Type: "string", Description: "当 action=list 时可选，按工具名或描述的关键字过滤（如 文件、天气、mcp）"},
				"tool_name": {Type: "string", Description: "action=describe/execute 时必填，目标工具名称（从 list 结果中获取）"},
				"arguments": {Type: "object", Description: "当 action=execute 时必填，传递给目标工具的参数对象"},
			},
		},
		registry: registry,
		typeMap:  typeMap,
	}
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
	Name        string                 `json:"name"`
	Type        string                 `json:"type"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
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
	if !exists {
		return "", fmt.Errorf("未找到工具: %s", toolName)
	}
	params := make(map[string]interface{})
	for paramName, arg := range tt.GetParameters() {
		params[paramName] = map[string]string{
			"type":        arg.Type,
			"description": arg.Description,
		}
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
	if !exists {
		available := make([]string, 0, len(t.registry))
		for name := range t.registry {
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

// SessionView 某次会话的工具视图（已按启用状态和会话白名单过滤）
type SessionView struct {
	nonMeta map[string]ToolInterface
	meta    *MetaTool
}

// BuildOptions 装配工具视图的会话级参数
type BuildOptions struct {
	EnabledTools  []string       // 会话工具白名单；为空表示全部已启用工具
	EnabledSkills []string       // 会话技能白名单；为空表示全部已启用技能
	ProjectDir    string         // 会话工作区目录：工具的工作目录，也是权限判定的边界
	SessionID     string         // 会话 ID（文件改动的归因记录按会话隔离）
	Changes       *FileChangeLog // 文件改动记录；为 nil 时不记录
	Enforcer      Enforcer       // 权限网关；为空时装配 fail-closed 的拒绝网关
}

// buildContext 装配期传给内置工具构造函数的上下文
type buildContext struct {
	dir     string          // 工作区目录（命令类工具的工作目录）
	fileCtx fileToolContext // 文件类工具的上下文（目录 + 归因记录）
}

// newBuiltinTool 构造内置工具。
// 返回 ok=false 表示该内置工具尚未实现（例如用户自定义的非内置项混进来）。
func newBuiltinTool(name string, cfg *ToolConfig, bc buildContext) (ToolInterface, bool) {
	switch name {
	case "exec_shell":
		return &CLITool{
			BaseTool: &BaseTool{
				Name: cfg.Name, Description: cfg.Description, Parameters: paramsFromConfig(cfg.Parameters),
			},
			Dir: bc.dir,
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
	default:
		return nil, false
	}
}

// RequiredParams 工具可声明的必填参数（GetParameters 里没有 required 概念，
// 直出给模型时需要补上，否则模型不知道哪些参数必填）
type RequiredParams interface {
	RequiredParams() []string
}

// llmToolFor 把装配好的工具转成给 LLM 的工具定义（参数名排序，保证定义稳定）
func llmToolFor(t ToolInterface) LLMTool {
	names := make([]string, 0, len(t.GetParameters()))
	for n := range t.GetParameters() {
		names = append(names, n)
	}
	sort.Strings(names)

	props := make(map[string]LLMToolProp, len(names))
	for _, n := range names {
		arg := t.GetParameters()[n]
		props[n] = LLMToolProp{Type: arg.Type, Description: arg.Description}
	}
	params := &LLMToolParams{Type: "object", Properties: props}
	if rp, ok := t.(RequiredParams); ok {
		params.Required = rp.RequiredParams()
	}
	return LLMTool{
		Type: ToolTypeFunction,
		Function: LLMToolDef{
			Name:        t.GetName(),
			Description: t.GetDescription(),
			Parameters:  params,
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
	}

	for _, cfg := range tm.store.GetAll() {
		if !cfg.Enabled {
			continue
		}
		if len(whitelist) > 0 && !whitelist[cfg.ID] {
			continue
		}
		switch cfg.Type {
		case ToolTypeBuiltin:
			t, ok := newBuiltinTool(cfg.Name, cfg, bc)
			if !ok {
				continue
			}
			registry[cfg.Name] = t
			typeMap[cfg.Name] = ToolTypeBuiltin
		case ToolTypeCLI:
			t := NewDynamicCLITool(cfg)
			t.Dir = dir
			registry[cfg.Name] = t
			typeMap[cfg.Name] = ToolTypeCLI
		case ToolTypeAPI:
			t := NewDynamicAPITool(cfg)
			registry[cfg.Name] = t
			typeMap[cfg.Name] = ToolTypeAPI
		case ToolTypeMCP:
			entry, err := tm.pool.Connect(ctx, cfg)
			if err != nil {
				fmt.Printf("[ToolManager] MCP 工具 %s 装配失败: %v\n", cfg.Name, err)
				continue
			}
			disabled := map[string]bool{}
			for _, name := range cfg.DisabledTools {
				disabled[name] = true
			}
			for _, mt := range entry.tools {
				if disabled[mt.Name] {
					continue
				}
				mt2 := mt
				wrapped := NewMCPTool(tm.pool, cfg, mt2)
				registry[wrapped.GetName()] = wrapped
				typeMap[wrapped.GetName()] = ToolTypeMCP
			}
		}
	}

	// 内置：技能加载工具（存在本会话可用技能时注册，经 tool_router 被发现）
	if tm.skills != nil {
		if rst := NewReadSkillTool(tm.skills, opts.EnabledSkills); rst.HasAvailable() {
			registry[rst.GetName()] = rst
			typeMap[rst.GetName()] = ToolTypeBuiltin
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
		meta:    NewMetaTool(registry, typeMap),
	}
}

// GetToolsForLLM 返回给 LLM 的工具定义。
//
// 直出策略：常用文件工具（read_file / write_file / edit_file / glob / grep / list_dir）
// 直接暴露，模型不必先 list/describe 再 execute，少一轮往返；
// 其余工具（MCP / 自定义 CLI / API / read_skill）仍经 tool_router 发现，避免 prompt 膨胀。
//
// 两条路径（直调与经路由器）落到同一个 registry，因此都会被权限网关拦住。
func (v *SessionView) GetToolsForLLM() []LLMTool {
	defs := make([]LLMTool, 0, len(directToolOrder)+1)
	for _, name := range directToolOrder {
		if t, ok := v.nonMeta[name]; ok {
			defs = append(defs, llmToolFor(t))
		}
	}
	defs = append(defs, LLMTool{
		Type: "function",
		Function: LLMToolDef{
			Name:        v.meta.GetName(),
			Description: v.meta.GetDescription(),
			Parameters: &LLMToolParams{
				Type:     "object",
				Required: []string{"action"},
				Properties: map[string]LLMToolProp{
					"action":    {Type: "string", Description: "操作类型：list=列出/搜索可用工具；describe=查看工具参数定义；execute=执行指定工具"},
					"query":     {Type: "string", Description: "action=list 时可选，按关键字过滤工具"},
					"tool_name": {Type: "string", Description: "describe/execute 时的目标工具名"},
					"arguments": {Type: "object", Description: "execute 时传递给目标工具的参数对象"},
				},
			},
		},
	})
	return defs
}

// ExecuteTool 在会话视图内执行工具
func (v *SessionView) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	if name == "tool_router" {
		return v.meta.Execute(context.Background(), args)
	}
	tool, ok := v.nonMeta[name]
	if !ok {
		return "", fmt.Errorf("未找到工具: %s", name)
	}
	return tool.Execute(context.Background(), args)
}
