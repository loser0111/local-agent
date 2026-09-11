package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
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

// ===== CLI 工具（借鉴 01agent 的 CliTool）=====

// CLITool 命令行工具：在本机终端执行 shell 命令
type CLITool struct {
	*BaseTool
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
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ===== 元工具：工具路由器（借鉴 01agent 的 MetaTool）=====

// MetaTool 元工具：支持 list（发现工具）和 execute（执行工具）
type MetaTool struct {
	*BaseTool
	registry map[string]ToolInterface // 非元工具注册表
}

func NewMetaTool(registry map[string]ToolInterface) *MetaTool {
	return &MetaTool{
		BaseTool: &BaseTool{
			Name:        "tool_router",
			Description: "工具路由器。通过此工具可以发现和执行所有可用工具。先调用 action=\"list\" 查看所有可用工具及其参数定义，然后调用 action=\"execute\" 执行指定工具。",
			Parameters: map[string]*ToolArgDef{
				"action":    {Type: "string", Description: "操作类型：list=列出所有可用工具；execute=执行指定工具"},
				"tool_name": {Type: "string", Description: "当 action=execute 时必填，要执行的工具名称（从 list 结果中获取）"},
				"arguments": {Type: "object", Description: "当 action=execute 时必填，传递给目标工具的参数对象"},
			},
		},
		registry: registry,
	}
}

func (t *MetaTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	switch action {
	case "list":
		return t.listTools()
	case "execute":
		return t.executeTool(ctx, args)
	default:
		return "", fmt.Errorf("未知的 action: %s，可选值: list, execute", action)
	}
}

// listTools 返回所有可用工具的名称、描述和参数定义
func (t *MetaTool) listTools() (string, error) {
	type toolInfo struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Parameters  map[string]interface{} `json:"parameters"`
	}

	names := make([]string, 0, len(t.registry))
	for name := range t.registry {
		names = append(names, name)
	}
	sort.Strings(names)

	tools := make([]toolInfo, 0, len(names))
	for _, name := range names {
		tt := t.registry[name]
		params := make(map[string]interface{})
		for paramName, arg := range tt.GetParameters() {
			params[paramName] = map[string]string{
				"type":        arg.Type,
				"description": arg.Description,
			}
		}
		tools = append(tools, toolInfo{
			Name:        name,
			Description: tt.GetDescription(),
			Parameters:  params,
		})
	}

	result, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化工具列表失败: %w", err)
	}
	return fmt.Sprintf("可用工具列表（共 %d 个）:\n%s", len(tools), string(result)), nil
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

// ===== 工具管理器（借鉴 01agent 的 ToolManager）=====

// ToolManager 工具管理器
type ToolManager struct {
	Tools        map[string]ToolInterface // 所有工具（含元工具）
	NonMetaTools map[string]ToolInterface // 非元工具（供元工具 list/execute 使用）
}

// NewToolManager 创建工具管理器并注册内置工具
func NewToolManager() *ToolManager {
	tm := &ToolManager{
		Tools:        make(map[string]ToolInterface),
		NonMetaTools: make(map[string]ToolInterface),
	}
	tm.registerBuiltinTools()
	return tm
}

// registerBuiltinTools 注册内置工具
func (tm *ToolManager) registerBuiltinTools() {
	// 注册 CLI 工具：执行终端命令
	cliTool := &CLITool{
		BaseTool: &BaseTool{
			Name:        "exec_shell",
			Description: "在本机终端执行shell/终端命令，用于查看文件、查询目录、执行系统指令",
			Parameters: map[string]*ToolArgDef{
				"cmd": {Type: "string", Description: "要执行的终端命令，linux/mac用bash指令，windows用cmd/powershell指令"},
			},
		},
	}
	tm.Tools["exec_shell"] = cliTool
	tm.NonMetaTools["exec_shell"] = cliTool

	// 注册元工具：工具路由器（注入非元工具注册表）
	metaTool := NewMetaTool(tm.NonMetaTools)
	tm.Tools["tool_router"] = metaTool
}

// ExecuteTool 根据名称查找并执行工具
func (tm *ToolManager) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	tool, ok := tm.Tools[name]
	if !ok {
		return "", fmt.Errorf("未找到工具: %s", name)
	}
	return tool.Execute(context.Background(), args)
}

// GetToolsForLLM 返回给 LLM 的工具定义（只暴露元工具，节省 token）
func (tm *ToolManager) GetToolsForLLM() []LLMTool {
	metaTool, ok := tm.Tools["tool_router"].(*MetaTool)
	if !ok {
		return nil
	}
	return []LLMTool{
		{
			Type: "function",
			Function: LLMToolDef{
				Name:        metaTool.GetName(),
				Description: metaTool.GetDescription(),
				Parameters: &LLMToolParams{
					Type:     "object",
					Required: []string{"action"},
					Properties: map[string]LLMToolProp{
						"action":    {Type: "string", Description: "操作类型：list=列出所有可用工具；execute=执行指定工具"},
						"tool_name": {Type: "string", Description: "当 action=execute 时必填，要执行的工具名称（从 list 结果中获取）"},
						"arguments": {Type: "object", Description: "当 action=execute 时必填，传递给目标工具的参数对象"},
					},
				},
			},
		},
	}
}
