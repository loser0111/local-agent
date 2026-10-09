package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/media"
	"wails-tmp/internal/memory"
	"wails-tmp/internal/skill"
	"wails-tmp/internal/tool"
	memstore "wails-tmp/memory"
)

// ===== 工具管理器（配置驱动装配）=====

// ToolManager 持有工具配置存储、技能存储与 MCP 连接池，按会话构建工具视图
type ToolManager struct {
	store  *tool.ToolStore
	skills *skill.SkillStore
	pool   *tool.MCPPool
}

func NewToolManager(store *tool.ToolStore, skills *skill.SkillStore) *ToolManager {
	return &ToolManager{store: store, skills: skills, pool: tool.NewMCPPool()}
}

// Skills 暴露技能存储
func (tm *ToolManager) Skills() *skill.SkillStore { return tm.skills }

// Pool 暴露 MCP 连接池（供连接测试使用）
func (tm *ToolManager) Pool() *tool.MCPPool { return tm.pool }

// Store 暴露配置存储
func (tm *ToolManager) Store() *tool.ToolStore { return tm.store }

// BuildOptions 装配工具视图的会话级参数
type BuildOptions struct {
	EnabledTools  []string            // 会话工具白名单；为空表示全部已启用工具
	EnabledSkills []string            // 会话技能白名单；为空表示全部已启用技能
	ProjectDir    string              // 会话工作区目录：工具的工作目录，也是权限判定的边界
	SessionID     string              // 会话 ID（文件改动的归因记录按会话隔离）
	Changes       *tool.FileChangeLog // 文件改动记录；为 nil 时不记录
	// Attachments 附件存储（read_image 用它把工作区里的图片存成会话附件）。
	// 为 nil 时 read_image **不注册**——没有地方存图，注册出来只会每次调用都失败。
	Attachments *media.AttachmentStore
	Enforcer    tool.Enforcer // 权限网关；为空时装配 fail-closed 的拒绝网关
	// Ask 用户提问回路（ask_user 工具）：发事件给前端并阻塞等待作答。
	// 为 nil 时该工具仍会装配，但调用会返回"界面未就绪"——便于测试与降级。
	Ask func(ctx context.Context, req agent.AskRequest) (agent.AskAnswer, error)
	// ExcludeTools 显式排除的工具名（优先级高于白名单）。
	// 子代理用它去掉 ask_user——它没有 UI 通道，留着一个永远失败的工具只会浪费模型一轮。
	ExcludeTools []string
	// SpawnAgent 派生代理的回路（spawn_agent 工具）。
	// 为 nil 时该工具**不注册**——这正是"子代理不能再次派生"的实现方式：
	// 不给它 spawner，工具就不存在，比注册后再拦截更干净。
	SpawnAgent func(ctx context.Context, task string, maxTurns int) (*agent.SubagentResult, error)
	// Memory 长期记忆库；为 nil 时不注册 memory_* 工具。
	Memory        *memstore.MemoryStore
	MemoryProject string
	IgnoreMemory  bool
	OnMemoryTouch func([]string)
}

// buildContext 装配期传给内置工具构造函数的上下文
type buildContext struct {
	dir     string                                                                   // 工作区目录（命令类工具的工作目录）
	fileCtx tool.FileToolContext                                                     // 文件类工具的上下文（目录 + 归因记录 + 附件）
	asker   func(ctx context.Context, req agent.AskRequest) (agent.AskAnswer, error) // ask_user 的回路
	// spawner 派生代理的回路；为 nil 时 spawn_agent 不注册（子代理因此无法再派生）
	spawner func(ctx context.Context, task string, maxTurns int) (*agent.SubagentResult, error)
	memory  memory.ToolContext
}

// newBuiltinTool 构造内置工具。
// 返回 ok=false 表示该内置工具尚未实现（例如用户自定义的非内置项混进来）。
func newBuiltinTool(src *tool.ToolSource, bc buildContext) (tool.ToolInterface, bool) {
	switch src.Name {
	case tool.ToolExecShell:
		return tool.NewCLITool(
			src.Name, src.Description,
			tool.ParamsFromConfig(src.Parameters), tool.RequiredFromConfig(src.Parameters),
			bc.dir,
		), true
	case tool.ToolReadFile:
		return tool.NewReadFileTool(bc.fileCtx), true
	case tool.ToolReadImage:
		// 没有附件存储就不注册 read_image：存图这一步无处可去，
		// 注册出来只会每次调用都报错，白耗模型一轮。
		if bc.fileCtx.Attachments == nil {
			return nil, false
		}
		return tool.NewReadImageTool(bc.fileCtx), true
	case tool.ToolWriteFile:
		return tool.NewWriteFileTool(bc.fileCtx), true
	case tool.ToolEditFile:
		return tool.NewEditFileTool(bc.fileCtx), true
	case tool.ToolGlob:
		return tool.NewGlobTool(bc.fileCtx), true
	case tool.ToolGrep:
		return tool.NewGrepTool(bc.fileCtx), true
	case tool.ToolListDir:
		return tool.NewListDirTool(bc.fileCtx), true
	case toolAskUser:
		return agent.NewAskUserTool(bc.asker), true
	case toolSpawnAgent:
		// 没有 spawner 就不注册：子代理视图刻意不给它，于是它无法再次派生
		if bc.spawner == nil {
			return nil, false
		}
		return agent.NewSpawnAgentTool(bc.spawner), true
	// 记忆三件套：引擎在 internal/memory，只吃 memory.ToolContext（不认识 App / buildContext）
	case toolMemorySearch:
		return memory.NewSearchTool(bc.memory), true
	case toolMemorySave:
		return memory.NewSaveTool(bc.memory), true
	case toolMemoryForget:
		return memory.NewForgetTool(bc.memory), true
	default:
		return nil, false
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
func (tm *ToolManager) BuildView(ctx context.Context, opts BuildOptions) *tool.SessionView {
	registry := map[string]tool.ToolInterface{}
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
	// 产图通道：本次装配的所有工具共用同一个收集器，工具循环按调用顺序取走。
	// 一次装配对应一次运行，所以不存在跨会话串图的问题。
	images := &media.ImageCollector{}
	bc := buildContext{
		dir: dir,
		fileCtx: tool.FileToolContext{
			Root:        dir,
			SessionID:   opts.SessionID,
			Changes:     opts.Changes,
			Attachments: opts.Attachments,
			ImageOut:    images,
		},
		asker:   opts.Ask,
		spawner: opts.SpawnAgent,
		memory: memory.ToolContext{
			Store:   opts.Memory,
			Project: opts.MemoryProject,
			Source:  opts.SessionID,
			Ignore:  opts.IgnoreMemory,
			OnTouch: opts.OnMemoryTouch,
		},
	}
	// exposure：工具名 → 暴露策略（直出 / 经路由器 / 不可见）
	exposure := map[string]tool.Exposure{}
	hidden := map[string]bool{}
	markExposure := func(kind tool.SourceKind, toolName string, e tool.Exposure) {
		if !tool.ValidExposure(e) {
			// 没配置（或配置非法）时**按默认推断**，而不是一律降级成 router：
			// 内置的文件工具本该直出，若运行期降级，只要配置没被 normalize/migrate 过
			// （全新安装就是这种情况），它们就会悄悄退回"要经路由器发现"，
			// 与 tool.DefaultExposure 的文档意图和 tool.DirectToolOrder 的存在意义都不一致。
			e = tool.DefaultExposure(kind, toolName)
		}
		exposure[toolName] = e
		if e == tool.ExposureInternal {
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
		if src, ok := tm.store.GetByName(name); ok && src.Kind == tool.SourceMCP {
			legacyMCPAllowed[src.Name] = true
		}
	}

	for _, src := range tm.store.GetAll() {
		if !src.Enabled || !tool.ValidSourceKind(src.Kind) {
			continue
		}
		switch src.Kind {
		case tool.SourceBuiltin:
			if !allowed(src.Name) {
				continue
			}
			t, ok := newBuiltinTool(src, bc)
			if !ok {
				continue
			}
			registry[src.Name] = t
			typeMap[src.Name] = string(tool.SourceBuiltin)
			markExposure(src.Kind, src.Name, src.Exposure)
		case tool.SourceCLI:
			if !allowed(src.Name) {
				continue
			}
			t := tool.NewDynamicCLITool(src)
			t.Dir = dir
			registry[src.Name] = t
			typeMap[src.Name] = string(tool.SourceCLI)
			markExposure(src.Kind, src.Name, src.Exposure)
		case tool.SourceHTTP:
			if !allowed(src.Name) {
				continue
			}
			t := tool.NewDynamicAPITool(src)
			registry[src.Name] = t
			typeMap[src.Name] = string(tool.SourceHTTP)
			markExposure(src.Kind, src.Name, src.Exposure)
		case tool.SourceMCP:
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
			for _, mt := range entry.Tools {
				if mt == nil || disabledSub[mt.Name] {
					continue
				}
				full := src.ToolNameFor(mt.Name)
				if !allowed(full) && !legacyMCPAllowed[src.Name] {
					continue
				}
				wrapped := tool.NewMCPTool(tm.pool, src, mt)
				registry[full] = wrapped
				typeMap[full] = string(tool.SourceMCP)
				markExposure(src.Kind, full, src.Exposure)
			}
		}
	}

	// 内置：技能加载工具（存在本会话可用技能时注册，经 tool_router 被发现）
	if tm.skills != nil {
		if rst := skill.NewReadSkillTool(tm.skills, opts.EnabledSkills); rst.HasAvailable() {
			registry[rst.GetName()] = rst
			typeMap[rst.GetName()] = string(tool.SourceBuiltin)
			markExposure(tool.SourceBuiltin, rst.GetName(), tool.ExposureRouter)
		}
		// L3：读取技能自带资源（不排除 disable-model-invocation 的技能——
		// 用户显式调用某技能后，正文里引用的资料仍要读得到）
		if rft := skill.NewReadSkillFileTool(tm.skills, opts.EnabledSkills); rft.HasAvailable() {
			registry[rft.GetName()] = rft
			typeMap[rft.GetName()] = string(tool.SourceBuiltin)
			markExposure(tool.SourceBuiltin, rft.GetName(), tool.ExposureRouter)
		}
	}

	// 统一装饰（必须在 NewMetaTool 之前：路由器持有的是同一个 map）
	enforcer := opts.Enforcer
	if enforcer == nil {
		// 装配缺失绝不等于放行：用拒绝网关兜底，宁可工具不可用也不能静默放行
		fmt.Printf("[ToolManager] 未提供权限网关，已装配拒绝网关（fail closed）\n")
		enforcer = tool.DenyAllEnforcer{}
	}
	for name, t := range registry {
		registry[name] = tool.NewGuardedTool(t, enforcer)
	}

	return tool.NewSessionView(
		registry,
		tool.NewMetaTool(registry, typeMap, hidden),
		tool.DirectToolNames(exposure),
		images,
	)
}
