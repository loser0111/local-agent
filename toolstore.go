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
	ToolTypeBuiltin = "builtin" // 内置工具（exec_shell + 文件六件套）
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
	// 版本升级后新增的内置工具要补齐（老的 tools.json 里没有它们）
	ts.ensureBuiltins()
	return ts
}

// defaultTools 内置工具：终端命令 + 文件六件套。
// 文件类工具让「文件操作」不必挤过 shell，权限判定因此能落到「工具 + 路径」上。
func defaultTools() []*ToolConfig {
	return []*ToolConfig{
		{
			ID:          "exec_shell",
			Name:        "exec_shell",
			Label:       "执行终端命令",
			Description: "在本机终端执行shell/终端命令，用于运行构建、测试、git 等；读写文件请优先用文件工具",
			Type:        ToolTypeBuiltin,
			Icon:        "terminal",
			Enabled:     true,
			Builtin:     true,
			Parameters: []ToolParamConfig{
				{Name: "cmd", Description: "要执行的终端命令，linux/mac用bash指令，windows用cmd/powershell指令", Required: true},
			},
		},
		{
			ID:          "read_file",
			Name:        "read_file",
			Label:       "读取文件",
			Description: "读取工作区内某个文本文件的内容（带行号，支持 offset/limit 分片）",
			Type:        ToolTypeBuiltin,
			Icon:        "file-text",
			Enabled:     true,
			Builtin:     true,
			Parameters: []ToolParamConfig{
				{Name: "path", Description: "文件路径（相对工作区目录或绝对路径）", Required: true},
				{Name: "offset", Description: "起始行号（从 1 开始，可选）"},
				{Name: "limit", Description: fmt.Sprintf("最多读取行数（默认 %d）", defaultReadLimit)},
			},
		},
		{
			ID:          "write_file",
			Name:        "write_file",
			Label:       "写入文件",
			Description: "新建或整体覆盖一个文本文件",
			Type:        ToolTypeBuiltin,
			Icon:        "file-plus",
			Enabled:     true,
			Builtin:     true,
			Parameters: []ToolParamConfig{
				{Name: "path", Description: "文件路径", Required: true},
				{Name: "content", Description: "完整文件内容（会覆盖原有内容）", Required: true},
			},
		},
		{
			ID:          "edit_file",
			Name:        "edit_file",
			Label:       "编辑文件",
			Description: "对文件做精确字符串替换（old_string 需唯一匹配）",
			Type:        ToolTypeBuiltin,
			Icon:        "file-code",
			Enabled:     true,
			Builtin:     true,
			Parameters: []ToolParamConfig{
				{Name: "path", Description: "文件路径", Required: true},
				{Name: "old_string", Description: "被替换的原文（含缩进，需完全一致）", Required: true},
				{Name: "new_string", Description: "替换后的新文本", Required: true},
				{Name: "replace_all", Description: "true 时替换所有匹配处"},
			},
		},
		{
			ID:          "glob",
			Name:        "glob",
			Label:       "查找文件",
			Description: "按 glob 模式查找文件（支持 ** 跨目录）",
			Type:        ToolTypeBuiltin,
			Icon:        "search",
			Enabled:     true,
			Builtin:     true,
			Parameters: []ToolParamConfig{
				{Name: "pattern", Description: "匹配模式，如 *.go、src/**/*.ts", Required: true},
				{Name: "path", Description: "搜索起点目录（默认工作区根目录）"},
			},
		},
		{
			ID:          "grep",
			Name:        "grep",
			Label:       "搜索内容",
			Description: "按正则搜索文件内容（只读，不解释 shell 语法）",
			Type:        ToolTypeBuiltin,
			Icon:        "search",
			Enabled:     true,
			Builtin:     true,
			Parameters: []ToolParamConfig{
				{Name: "pattern", Description: "正则表达式", Required: true},
				{Name: "path", Description: "搜索起点目录或文件"},
				{Name: "glob", Description: "只搜索匹配该模式的文件，如 *.go"},
				{Name: "output_mode", Description: "files_with_matches（默认）/ content / count"},
				{Name: "case_insensitive", Description: "忽略大小写"},
			},
		},
		{
			ID:          "ask_user",
			Name:        "ask_user",
			Label:       "向用户提问",
			Description: "模型缺少关键信息或有多种做法需要用户拍板时，向用户提问并等待作答",
			Type:        ToolTypeBuiltin,
			Icon:        "message-square",
			Enabled:     true,
			Builtin:     true,
			Parameters: []ToolParamConfig{
				{Name: "questions", Description: "问题列表（1-4 个），每项含 question / header / options / multiSelect", Required: true},
			},
		},
		{
			ID:          "list_dir",
			Name:        "list_dir",
			Label:       "列出目录",
			Description: "列出目录内容（目录在前，含文件大小）",
			Type:        ToolTypeBuiltin,
			Icon:        "folder",
			Enabled:     true,
			Builtin:     true,
			Parameters: []ToolParamConfig{
				{Name: "path", Description: "要列出的目录（默认工作区根目录）"},
			},
		},
	}
}

// ensureBuiltins 补齐缺失的内置工具（老版本 tools.json 里没有新内置工具时自动追加）
func (ts *ToolStore) ensureBuiltins() {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	existing := make(map[string]bool, len(ts.tools))
	for _, t := range ts.tools {
		existing[t.Name] = true
	}
	added := false
	for _, def := range defaultTools() {
		if existing[def.Name] {
			continue
		}
		ts.tools = append(ts.tools, def)
		added = true
	}
	if !added {
		return
	}
	if err := ts.save(); err != nil {
		fmt.Printf("[ToolStore] 补齐内置工具失败: %v\n", err)
		return
	}
	fmt.Printf("[ToolStore] 已补齐 %d 个内置工具配置\n", len(defaultTools())-len(existing))
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
