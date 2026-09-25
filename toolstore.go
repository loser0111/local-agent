package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ===== 工具来源的持久化（~/.local-agent/tools.json）=====
//
// 落盘结构见 toolmodel.go 的 ToolFile（version + sources）。
// 加载时自动处理两类旧文件，并各留一份 tools.json.bak —— 配置是用户的资产，改动必须可回退：
//   - v1（裸数组 + type/弱类型 config）→ 迁移为 v2 对象；
//   - v2 里被误判成 internal 的内置工具曝光策略 → 修回默认（见 repairBuiltinExposure）。

// ToolParamConfig 用户自定义参数（CLI/HTTP 来源）
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

// ToolRuntimeStatus 工具运行时状态（不持久化，随 ListTools 返回给前端）
type ToolRuntimeStatus struct {
	Connected bool   `json:"connected"` // MCP 是否已连接
	ToolCount int    `json:"toolCount"` // 可用子工具数量
	Error     string `json:"error"`     // 连接/校验错误信息
}

// ToolInfo 列表接口返回项：来源配置 + 运行时状态
type ToolInfo struct {
	ToolSource
	Status ToolRuntimeStatus `json:"status"`
}

// ToolStore 工具来源的 JSON 持久化
type ToolStore struct {
	mu       sync.RWMutex
	filePath string
	sources  []*ToolSource
}

// NewToolStore 创建存储并加载配置；文件不存在时写入内置默认来源
func NewToolStore(filePath string) *ToolStore {
	ts := &ToolStore{filePath: filePath, sources: []*ToolSource{}}
	if !ts.load() {
		ts.sources = defaultSources()
		_ = ts.save()
	}
	// 版本升级后新增的内置来源要补齐（老的 tools.json 里没有它们）
	ts.ensureBuiltins()
	return ts
}

// defaultSources 内置来源：终端命令 + 文件六件套 + 向用户提问 + 派生代理。
// 文件类工具让「文件操作」不必挤过 shell，权限判定因此能落到「工具 + 路径」上。
func defaultSources() []*ToolSource {
	return []*ToolSource{
		{
			ID: toolExecShell, Name: toolExecShell, Label: "执行终端命令",
			Description: "在本机终端执行shell/终端命令，用于运行构建、测试、git 等；读写文件请优先用文件工具",
			Kind:        SourceBuiltin, Icon: "terminal", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "cmd", Description: "要执行的终端命令，linux/mac用bash指令，windows用cmd/powershell指令", Required: true},
			},
		},
		{
			ID: "read_file", Name: "read_file", Label: "读取文件",
			Description: "读取工作区内某个文本文件的内容（带行号，支持 offset/limit 分片）",
			Kind:        SourceBuiltin, Icon: "file-text", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "path", Description: "文件路径（相对工作区目录或绝对路径）", Required: true},
				{Name: "offset", Description: "起始行号（从 1 开始，可选）"},
				{Name: "limit", Description: fmt.Sprintf("最多读取行数（默认 %d）", defaultReadLimit)},
			},
		},
		{
			ID: toolReadImage, Name: toolReadImage, Label: "查看图片",
			Description: "查看工作区内的一张图片（PNG/JPEG/GIF）：截图、报错图、设计稿、图表。图片会作为图像内容返回，可直接分析其中内容",
			Kind:        SourceBuiltin, Icon: "image", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "path", Description: "图片路径（相对工作区目录或绝对路径）", Required: true},
			},
		},
		{
			ID: "write_file", Name: "write_file", Label: "写入文件",
			Description: "新建或整体覆盖一个文本文件",
			Kind:        SourceBuiltin, Icon: "file-plus", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "path", Description: "文件路径", Required: true},
				{Name: "content", Description: "完整文件内容（会覆盖原有内容）", Required: true},
			},
		},
		{
			ID: "edit_file", Name: "edit_file", Label: "编辑文件",
			Description: "对文件做精确字符串替换（old_string 需唯一匹配）",
			Kind:        SourceBuiltin, Icon: "file-code", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "path", Description: "文件路径", Required: true},
				{Name: "old_string", Description: "被替换的原文（含缩进，需完全一致）", Required: true},
				{Name: "new_string", Description: "替换后的新文本", Required: true},
				{Name: "replace_all", Description: "true 时替换所有匹配处"},
			},
		},
		{
			ID: "glob", Name: "glob", Label: "查找文件",
			Description: "按 glob 模式查找文件（支持 ** 跨目录）",
			Kind:        SourceBuiltin, Icon: "search", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "pattern", Description: "匹配模式，如 *.go、src/**/*.ts", Required: true},
				{Name: "path", Description: "搜索起点目录（默认工作区根目录）"},
			},
		},
		{
			ID: "grep", Name: "grep", Label: "搜索内容",
			Description: "按正则搜索文件内容（只读，不解释 shell 语法）",
			Kind:        SourceBuiltin, Icon: "search", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "pattern", Description: "正则表达式", Required: true},
				{Name: "path", Description: "搜索起点目录或文件"},
				{Name: "glob", Description: "只搜索匹配该模式的文件，如 *.go"},
				{Name: "output_mode", Description: "files_with_matches（默认）/ content / count"},
				{Name: "case_insensitive", Description: "忽略大小写"},
			},
		},
		{
			ID: "ask_user", Name: "ask_user", Label: "向用户提问",
			Description: "模型缺少关键信息或有多种做法需要用户拍板时，向用户提问并等待作答",
			Kind:        SourceBuiltin, Icon: "message-square", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "questions", Description: "问题列表（1-4 个），每项含 question / header / options / multiSelect", Required: true},
			},
		},
		{
			ID: "list_dir", Name: "list_dir", Label: "列出目录",
			Description: "列出目录内容（目录在前，含文件大小）",
			Kind:        SourceBuiltin, Icon: "folder", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "path", Description: "要列出的目录（默认工作区根目录）"},
			},
		},
		{
			ID: toolMemorySearch, Name: toolMemorySearch, Label: "检索记忆",
			Description: "检索跨会话长期记忆。query 可以是自然语言或记忆 id。",
			Kind:        SourceBuiltin, Icon: "book", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "query", Description: "检索词或记忆 id", Required: true},
			},
		},
		{
			ID: toolMemorySave, Name: toolMemorySave, Label: "保存记忆",
			Description: "把对未来会话仍有用的事实写入长期记忆",
			Kind:        SourceBuiltin, Icon: "bookmark", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "content", Description: "记忆正文", Required: true},
				{Name: "title", Description: "短标题"},
				{Name: "type", Description: "user / feedback / project / reference"},
				{Name: "scope", Description: "user 或 project"},
				{Name: "tags", Description: "逗号分隔标签"},
			},
		},
		{
			ID: toolMemoryForget, Name: toolMemoryForget, Label: "删除记忆",
			Description: "删除一条长期记忆。优先传记忆 id。",
			Kind:        SourceBuiltin, Icon: "eraser", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "target", Description: "记忆 id 或检索词", Required: true},
			},
		},
		{
			// 名字用常量而不是字面量：newBuiltinTool 是按 src.Name 分派的（case toolSpawnAgent），
			// 两处一旦不一致，工具就会"注册了但认不出来"。
			ID: toolSpawnAgent, Name: toolSpawnAgent, Label: "派生代理",
			Description: "派生一个子代理独立完成一项任务，它有自己的上下文，只把结论回给你（中间过程不占用你的上下文）",
			Kind:        SourceBuiltin, Icon: "users", Enabled: true, Builtin: true,
			Parameters: []ToolParamConfig{
				{Name: "task", Description: "要子代理做的事（它看不到你的对话历史，越具体越好）", Required: true},
				{Name: "max_turns", Description: "可选：子代理最多跑几轮"},
			},
		},
	}
}

// ensureBuiltins 补齐缺失的内置来源（老配置里没有新内置工具时自动追加）
func (ts *ToolStore) ensureBuiltins() {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	existing := make(map[string]bool, len(ts.sources))
	for _, s := range ts.sources {
		existing[s.Name] = true
	}
	added := 0
	for _, def := range defaultSources() {
		if existing[def.Name] {
			continue
		}
		ts.sources = append(ts.sources, def)
		added++
	}
	if added == 0 {
		return
	}
	if err := ts.save(); err != nil {
		fmt.Printf("[ToolStore] 补齐内置来源失败: %v\n", err)
		return
	}
	fmt.Printf("[ToolStore] 已补齐 %d 个内置来源\n", added)
}

// load 读取配置；返回是否成功加载。旧格式会就地迁移并备份。
func (ts *ToolStore) load() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	data, err := os.ReadFile(ts.filePath)
	if err != nil || len(data) == 0 {
		return false
	}
	sources, migrated, err := parseToolFile(data)
	if err != nil {
		fmt.Printf("[ToolStore] %v\n", err)
		return false
	}
	ts.sources = sources
	if !migrated {
		return true
	}

	// 需要重写：先备份原文件（仅在还没有备份时写，避免覆盖上一次的好备份），再落当前版本
	bak := ts.filePath + ".bak"
	if _, statErr := os.Stat(bak); os.IsNotExist(statErr) {
		if err := os.WriteFile(bak, data, 0o600); err != nil {
			fmt.Printf("[ToolStore] 备份旧配置失败（继续迁移）: %v\n", err)
		}
	}
	if err := ts.save(); err != nil {
		fmt.Printf("[ToolStore] 写入新版工具配置失败: %v\n", err)
		return true
	}
	fmt.Printf("[ToolStore] 工具配置已重写为 v%d（原文件备份为 %s）\n", ToolFileVersion, filepath.Base(bak))
	return true
}

// save 写入配置文件（落盘前统一排序，文件内容稳定可 diff）
func (ts *ToolStore) save() error {
	dir := filepath.Dir(ts.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	sortSources(ts.sources)
	data, err := json.MarshalIndent(&ToolFile{Version: ToolFileVersion, Sources: ts.sources}, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化工具配置失败: %w", err)
	}
	return os.WriteFile(ts.filePath, data, 0o644)
}

// GetAll 返回全部来源（拷贝切片，元素仍为指针，调用方不应就地改动）
func (ts *ToolStore) GetAll() []*ToolSource {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	out := make([]*ToolSource, len(ts.sources))
	copy(out, ts.sources)
	return out
}

// Get 按 ID 获取来源
func (ts *ToolStore) Get(id string) (*ToolSource, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	for _, s := range ts.sources {
		if s.ID == id {
			return s, true
		}
	}
	return nil, false
}

// GetByName 按名称获取来源（会话白名单、权限规则用的是名字）
func (ts *ToolStore) GetByName(name string) (*ToolSource, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	for _, s := range ts.sources {
		if s.Name == name {
			return s, true
		}
	}
	return nil, false
}

// Save 新增或更新来源：ID 相同则覆盖。
// 三个字段不允许被前端改写，一律沿用旧值：
//   - Builtin：内置标记
//   - Discovered：连接测试的发现缓存
//   - DisabledTools：子工具开关只能走 SetSubToolEnabled（避免编辑弹窗顺手清空）
func (ts *ToolStore) Save(src *ToolSource) error {
	if src == nil {
		return fmt.Errorf("来源不能为空")
	}
	if reason := normalizeSource(src); reason != "" {
		return fmt.Errorf("%s", reason)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	for i, old := range ts.sources {
		if old.ID != src.ID {
			continue
		}
		src.Builtin = old.Builtin
		src.Discovered = old.Discovered
		src.DisabledTools = old.DisabledTools
		ts.sources[i] = src
		return ts.save()
	}
	ts.sources = append(ts.sources, src)
	return ts.save()
}

// Delete 删除来源（内置来源拒绝删除）
func (ts *ToolStore) Delete(id string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for i, s := range ts.sources {
		if s.ID != id {
			continue
		}
		if s.Builtin {
			return fmt.Errorf("内置来源不可删除")
		}
		ts.sources = append(ts.sources[:i], ts.sources[i+1:]...)
		return ts.save()
	}
	return fmt.Errorf("来源不存在: %s", id)
}

// SetEnabled 切换来源启用状态（整台 MCP 服务器的启停走这里）
func (ts *ToolStore) SetEnabled(id string, enabled bool) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, s := range ts.sources {
		if s.ID == id {
			s.Enabled = enabled
			return ts.save()
		}
	}
	return fmt.Errorf("来源不存在: %s", id)
}

// SetExposure 设置暴露策略（direct / router / internal）
func (ts *ToolStore) SetExposure(id string, exposure Exposure) error {
	if !ValidExposure(exposure) {
		return fmt.Errorf("未知的暴露策略: %s", exposure)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, s := range ts.sources {
		if s.ID == id {
			s.Exposure = exposure
			return ts.save()
		}
	}
	return fmt.Errorf("来源不存在: %s", id)
}

// SetSubToolEnabled 启用/停用某个 MCP 子工具（tool 传服务器上的原始名）
func (ts *ToolStore) SetSubToolEnabled(id, tool string, enabled bool) error {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return fmt.Errorf("子工具名不能为空")
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, s := range ts.sources {
		if s.ID != id {
			continue
		}
		if s.Kind != SourceMCP {
			return fmt.Errorf("只有 MCP 来源支持子工具开关: %s", s.Name)
		}
		// 先剔除再按需追加：避免重复项，也让「启用」幂等
		kept := make([]string, 0, len(s.DisabledTools))
		for _, d := range s.DisabledTools {
			if d != tool {
				kept = append(kept, d)
			}
		}
		if !enabled {
			kept = append(kept, tool)
		}
		s.DisabledTools = kept
		return ts.save()
	}
	return fmt.Errorf("来源不存在: %s", id)
}

// UpdateDiscovered 更新 MCP 子工具的发现缓存
func (ts *ToolStore) UpdateDiscovered(id string, tools []MCPToolMeta) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, s := range ts.sources {
		if s.ID == id {
			s.Discovered = tools
			return ts.save()
		}
	}
	return fmt.Errorf("来源不存在: %s", id)
}
