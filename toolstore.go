package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ===== 工具配置模型（持久化到 ~/.local-agent/tools.json）=====

const (
	ToolTypeBuiltin = "builtin" // 内置工具（exec_shell）
	ToolTypeCLI     = "cli"     // 自定义 CLI 命令工具
	ToolTypeMCP     = "mcp"     // MCP Server 工具
	ToolTypeAPI     = "api"     // HTTP API 工具
)

// ToolParamConfig 用户自定义参数（CLI/API 工具）
type ToolParamConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// MCPToolMeta MCP Server 发现到的子工具摘要（测试连接时缓存，供界面展示）
type MCPToolMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolConfig 单个工具的配置
type ToolConfig struct {
	ID            string            `json:"id"`            // 唯一 ID
	Name          string            `json:"name"`          // 暴露给 LLM 的工具名（MCP 子工具自动加 mcp__server__ 前缀）
	Label         string            `json:"label"`         // 界面显示名
	Description   string            `json:"description"`   // 给 LLM 的工具描述
	Type          string            `json:"type"`          // builtin | cli | mcp | api
	Icon          string            `json:"icon"`          // Lucide 图标名
	Enabled       bool              `json:"enabled"`       // 是否全局启用
	Builtin       bool              `json:"builtin"`       // 内置工具不可删除
	Risk          string            `json:"risk"`           // 风险级别覆盖：read/write/network/process（留空按类型推导）
	Parameters    []ToolParamConfig `json:"parameters"`    // CLI/API 工具的自定义参数
	Config        json.RawMessage   `json:"config"`        // 各类型特有配置（CLIConfig/APIConfig/MCPConfig）
	DisabledTools []string          `json:"disabledTools"` // MCP 子工具禁用名单
	Discovered    []MCPToolMeta     `json:"discovered"`    // MCP 已发现的子工具摘要
}

// CLIToolConfig CLI 工具配置：命令模板（{{参数名}} 占位）
type CLIToolConfig struct {
	Command string `json:"command"` // 例如 "ffmpeg {{input}}"
	Timeout int    `json:"timeout"` // 超时秒数，0 用默认 60s
}

// APIToolConfig HTTP API 工具配置（url/headers/body 支持 {{参数名}} 占位）
type APIToolConfig struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"` // 请求体模板（POST/PUT 时使用）
	Timeout int               `json:"timeout"`
}

// MCPToolConfig MCP Server 连接配置（兼容 Claude Desktop / Cline 的 mcpServers 格式）
type MCPToolConfig struct {
	Transport string            `json:"transport"` // stdio | http | sse
	Command   string            `json:"command"`   // stdio: 可执行文件
	Args      []string          `json:"args"`      // stdio: 参数
	Env       map[string]string `json:"env"`       // stdio: 环境变量
	URL       string            `json:"url"`       // http/sse: 服务地址
	Headers   map[string]string `json:"headers"`   // http/sse: 自定义请求头
}

// ToolRuntimeStatus 工具运行时状态（不持久化，随 ListTools 返回给前端）
type ToolRuntimeStatus struct {
	Connected bool   `json:"connected"` // MCP 是否已连接
	ToolCount int    `json:"toolCount"` // 可用子工具数量
	Error     string `json:"error"`     // 连接/校验错误信息
}

// ToolInfo 列表接口返回项：配置 + 运行时状态
type ToolInfo struct {
	ToolConfig
	Status ToolRuntimeStatus `json:"status"`
}

// ToolStore 工具配置 JSON 持久化
type ToolStore struct {
	mu       sync.RWMutex
	filePath string
	tools    []*ToolConfig
}

// NewToolStore 创建工具存储并加载配置；文件不存在时写入内置默认工具
func NewToolStore(filePath string) *ToolStore {
	ts := &ToolStore{filePath: filePath, tools: []*ToolConfig{}}
	if !ts.load() {
		ts.tools = defaultTools()
		_ = ts.save()
	}
	return ts
}

// defaultTools 首次启动的内置工具
func defaultTools() []*ToolConfig {
	return []*ToolConfig{
		{
			ID:          "exec_shell",
			Name:        "exec_shell",
			Label:       "执行终端命令",
			Description: "在本机终端执行shell/终端命令，用于查看文件、查询目录、执行系统指令",
			Type:        ToolTypeBuiltin,
			Icon:        "terminal",
			Enabled:     true,
			Builtin:     true,
			Parameters: []ToolParamConfig{
				{Name: "cmd", Description: "要执行的终端命令，linux/mac用bash指令，windows用cmd/powershell指令", Required: true},
			},
		},
	}
}

// load 读取配置文件，返回是否成功加载
func (ts *ToolStore) load() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	data, err := os.ReadFile(ts.filePath)
	if err != nil || len(data) == 0 {
		return false
	}
	var tools []*ToolConfig
	if err := json.Unmarshal(data, &tools); err != nil {
		fmt.Printf("[ToolStore] 解析配置文件失败: %v\n", err)
		return false
	}
	ts.tools = tools
	return true
}

// save 写入配置文件
func (ts *ToolStore) save() error {
	dir := filepath.Dir(ts.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	data, err := json.MarshalIndent(ts.tools, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化工具配置失败: %w", err)
	}
	return os.WriteFile(ts.filePath, data, 0o644)
}

// GetAll 返回全部工具配置（拷贝）
func (ts *ToolStore) GetAll() []*ToolConfig {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	result := make([]*ToolConfig, len(ts.tools))
	copy(result, ts.tools)
	return result
}

// Get 按 ID 获取工具
func (ts *ToolStore) Get(id string) (*ToolConfig, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	for _, t := range ts.tools {
		if t.ID == id {
			return t, true
		}
	}
	return nil, false
}

// Save 新增或更新工具（ID 相同则覆盖）
func (ts *ToolStore) Save(cfg *ToolConfig) error {
	if cfg.ID == "" {
		return fmt.Errorf("工具 ID 不能为空")
	}
	if cfg.Name == "" {
		return fmt.Errorf("工具名称不能为空")
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for i, t := range ts.tools {
		if t.ID == cfg.ID {
			cfg.Builtin = t.Builtin // 内置标记不可被前端改写
			ts.tools[i] = cfg
			return ts.save()
		}
	}
	ts.tools = append(ts.tools, cfg)
	return ts.save()
}

// Delete 删除工具（内置工具拒绝删除）
func (ts *ToolStore) Delete(id string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for i, t := range ts.tools {
		if t.ID == id {
			if t.Builtin {
				return fmt.Errorf("内置工具不可删除")
			}
			ts.tools = append(ts.tools[:i], ts.tools[i+1:]...)
			return ts.save()
		}
	}
	return fmt.Errorf("工具不存在: %s", id)
}

// SetEnabled 切换启用状态
func (ts *ToolStore) SetEnabled(id string, enabled bool) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, t := range ts.tools {
		if t.ID == id {
			t.Enabled = enabled
			return ts.save()
		}
	}
	return fmt.Errorf("工具不存在: %s", id)
}

// UpdateDiscovered 更新 MCP 工具发现结果
func (ts *ToolStore) UpdateDiscovered(id string, tools []MCPToolMeta) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, t := range ts.tools {
		if t.ID == id {
			t.Discovered = tools
			return ts.save()
		}
	}
	return fmt.Errorf("工具不存在: %s", id)
}
