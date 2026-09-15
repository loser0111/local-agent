package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
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
}

// cliDefaultTimeout 内置终端命令的默认超时（秒）。
// 修复前 CLITool 忽略传入的 ctx 且无超时：授权通过后命令仍可能永久挂住，
// 用户的「停止生成」也中断不了它。
const cliDefaultTimeout = 60

func (t *CLITool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	cmdStr, ok := args["cmd"].(string)
	if !ok {
		return "", fmt.Errorf("cmd 参数是必需的")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, cliDefaultTimeout*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "powershell", "-Command", cmdStr)
	} else {
		cmd = exec.CommandContext(cctx, "bash", "-c", cmdStr)
	}
	out, err := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("命令执行超时（%ds）", cliDefaultTimeout)
	}
	if cctx.Err() == context.Canceled {
		return string(out), fmt.Errorf("命令已取消")
	}
	return string(out), err
}

// DescribeOperation 工具自述要执行什么，供权限规则匹配。
// Risk 留空由 riskOf(cfg) 推导（尊重 ToolConfig.Risk 覆盖）。
func (t *CLITool) DescribeOperation(args map[string]interface{}) PermissionSubject {
	cmdStr, _ := args["cmd"].(string)
	return PermissionSubject{
		ToolName: t.GetName(),
		Command:  cmdStr,
		Raw:      args,
	}
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

// BuildView 依据配置和会话白名单装配工具。
// enabledWhitelist 为空表示启用全部已开启工具；非空时只装配白名单内的工具 ID。
// skillWhitelist 同理作用于技能（决定 read_skill 可读范围）；为空=全部已启用技能。
// MCP server 连接失败不阻断装配，仅跳过其子工具（错误在设置页状态中展示）。
//
// eng 为权限引擎；传 nil 表示不启用权限管控（既有单测与不需要管控的场景直通）。
// 每个工具都会被 wrapTool 包一层装饰器 —— registry 同时喂给 nonMeta 与 MetaTool，
// 因此包一次即可覆盖「直调」与「经 tool_router 路由」两条执行路径。
func (tm *ToolManager) BuildView(ctx context.Context, eng *PermissionEngine, enabledWhitelist, skillWhitelist []string) *SessionView {
	registry := map[string]ToolInterface{}
	typeMap := map[string]string{}
	whitelist := map[string]bool{}
	for _, id := range enabledWhitelist {
		whitelist[id] = true
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
			if cfg.Name == "exec_shell" {
				t := &CLITool{BaseTool: &BaseTool{
					Name: cfg.Name, Description: cfg.Description, Parameters: paramsFromConfig(cfg.Parameters),
				}}
				registry[cfg.Name] = wrapTool(t, cfg, ToolTypeBuiltin, eng)
				typeMap[cfg.Name] = ToolTypeBuiltin
			}
		case ToolTypeCLI:
			registry[cfg.Name] = wrapTool(NewDynamicCLITool(cfg), cfg, ToolTypeCLI, eng)
			typeMap[cfg.Name] = ToolTypeCLI
		case ToolTypeAPI:
			registry[cfg.Name] = wrapTool(NewDynamicAPITool(cfg), cfg, ToolTypeAPI, eng)
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
				registry[wrapped.GetName()] = wrapTool(wrapped, cfg, ToolTypeMCP, eng)
				typeMap[wrapped.GetName()] = ToolTypeMCP
			}
		}
	}

	// 内置：技能加载工具（存在本会话可用技能时注册，经 tool_router 被发现）
	if tm.skills != nil {
		if rst := NewReadSkillTool(tm.skills, skillWhitelist); rst.HasAvailable() {
			// read_skill 没有对应的工具配置，构造一份最小配置供权限层推导风险级别
			cfg := &ToolConfig{
				ID:    "read_skill",
				Name:  rst.GetName(),
				Label: "读取技能正文",
				Type:  ToolTypeBuiltin,
			}
			registry[rst.GetName()] = wrapTool(rst, cfg, ToolTypeBuiltin, eng)
			typeMap[rst.GetName()] = ToolTypeBuiltin
		}
	}

	return &SessionView{
		nonMeta: registry,
		meta:    NewMetaTool(registry, typeMap),
	}
}

// ===== 权限装饰器 =====

// guardedTool 在真实工具执行前插入权限决策。
type guardedTool struct {
	inner    ToolInterface
	cfg      *ToolConfig
	toolType string
	eng      *PermissionEngine
}

// wrapTool 按需包装：eng 为 nil 时返回原工具，不引入任何额外开销
func wrapTool(t ToolInterface, cfg *ToolConfig, toolType string, eng *PermissionEngine) ToolInterface {
	if eng == nil {
		return t
	}
	return &guardedTool{inner: t, cfg: cfg, toolType: toolType, eng: eng}
}

func (g *guardedTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	sub := buildSubject(g.inner, g.cfg, g.toolType, args)
	action, reason := g.eng.Authorize(ctx, sub)

	switch action {
	case DecisionAllow:
		return g.inner.Execute(ctx, args)
	case DecisionDeny:
		// 以 error 返回：chat.go 会写成「执行失败: ...」并作为 role=tool 消息回填，
		// 模型因此能看到拒绝原因并调整策略（复用既有回填路径，无需改动）。
		// 用哨兵错误包裹，让上层能把「被拒绝」与「执行报错」区分开。
		return "", &PermissionDeniedError{Reason: reason}
	default:
		// DecisionAsk 未被 Asker 消化（无人可问）—— 视为拒绝
		return "", &PermissionDeniedError{Reason: "需要用户授权但未获得应答"}
	}
}

func (g *guardedTool) GetName() string { return g.inner.GetName() }

func (g *guardedTool) GetDescription() string { return g.inner.GetDescription() }

func (g *guardedTool) GetParameters() map[string]*ToolArgDef { return g.inner.GetParameters() }

// LLMToolDef 返回给 LLM 的工具定义（只暴露 tool_router，具体工具经路由器发现，节省 token）
func (v *SessionView) GetToolsForLLM() []LLMTool {
	return []LLMTool{
		{
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
		},
	}
}

// ExecuteTool 在会话视图内执行工具。
// ctx 会被透传到权限引擎（用于授权等待的超时与取消）与工具实现（用于中断）。
func (v *SessionView) ExecuteTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
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
