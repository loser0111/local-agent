package main

import (
	"context"
	"fmt"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// App struct
type App struct {
	ctx          context.Context
	modelStore   *ModelStore
	sessionStore *SessionStore
	toolStore    *ToolStore
	toolManager  *ToolManager
	skillStore   *SkillStore
	skillInstall *SkillInstaller
	diffService  *DiffService
	planStore    *PlanStore

	// baseDir 本地数据目录（~/.local-agent）：权限配置的用户全局层也放在这里
	baseDir string

	// 权限管理：授权（内存，会话结束即失效）、审计、授权询问回路
	permissionGrants *GrantStore
	permissionAudit  *AuditLog
	permissionBroker *permissionBroker

	// 用户提问回路（ask_user 工具阻塞等待用户作答）
	askBroker *askBroker

	// 文件改动归因：文件工具写入时记录 before/after（内存）
	fileChanges *FileChangeLog

	// 运行取消注册表：普通聊天与计划执行共用，支持软/硬两级取消
	runs *runRegistry

	// subagents 运行中的子代理（内存态）。跑完的写回它自己的会话文件，
	// 列表接口把两者合起来（见 subagentregistry.go）。
	subagents *subagentTracker

	// reqLog 各会话最近一次真实发出的 LLM 请求快照（仅供界面查看/排障，内存态）
	reqLog *llmRequestLog
	// contextPrefs 上下文压缩策略（~/.local-agent/context.json）。
	// 独立于模型配置：压缩时留多少条原文是全局策略，不该逼用户去改模型条目。
	contextPrefs *ContextPrefsStore
	// tokenCalib 估算校准系数（~/.local-agent/token-calib.json）。
	// 与 contextPrefs 不同：那个是用户的意图，这个是**观测缓存**——由每次模型调用
	// 回传的 usage 自动更新，用户不需要也不应该手改它。
	tokenCalib *TokenCalibStore
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// 本地数据统一保存在用户主目录下的 .local-agent/
	baseDir, err := a.dataDir()
	if err != nil {
		fmt.Printf("[App] 获取数据目录失败: %v\n", err)
		baseDir = "."
	}
	// 初始化模型存储：~/.local-agent/models.json
	a.modelStore = NewModelStore(filepath.Join(baseDir, "models.json"))
	// 初始化会话存储：~/.local-agent/sessions/*.json
	a.sessionStore = NewSessionStore(filepath.Join(baseDir, "sessions"))
	// 初始化技能存储：~/.local-agent/skills/（文件夹 + SKILL.md）
	a.skillStore = NewSkillStore(filepath.Join(baseDir, "skills"))
	a.skillInstall = NewSkillInstaller(a.skillStore)
	a.skillStore.EnsureDefaultSkill() // 首次运行写入示例技能
	// 初始化工具配置存储与工具管理器（~/.local-agent/tools.json）
	a.toolStore = NewToolStore(filepath.Join(baseDir, "tools.json"))
	a.toolManager = NewToolManager(a.toolStore, a.skillStore)
	// 初始化差异服务（基于 git 快照计算工作区 diff）
	a.diffService = NewDiffService()
	// 初始化计划存储：~/.local-agent/plans/（重启时 running 计划自动置为失败）
	a.planStore = NewPlanStore(filepath.Join(baseDir, "plans"))
	// 初始化权限管理（授权存储 / 审计 / 授权询问回路）与文件改动归因
	a.baseDir = baseDir
	a.runs = &runRegistry{}
	a.reqLog = &llmRequestLog{}
	a.subagents = newSubagentTracker()
	a.ensurePermissionState()
	a.fileChanges = NewFileChangeLog(500)
	// 初始化上下文压缩策略：~/.local-agent/context.json（不存在则全用内置默认值）
	a.contextPrefs = NewContextPrefsStore(filepath.Join(baseDir, "context.json"))
	// 估算校准系数：不存在 = 还没观测过，一律按 1.0（纯字符估算）
	a.tokenCalib = NewTokenCalibStore(filepath.Join(baseDir, "token-calib.json"))
}

// dataDir 返回本地数据目录（不存在则创建）
func (a *App) dataDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(homeDir, ".local-agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Greet returns a greeting for the given name
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

// ===== 模型管理 =====
// GetModels 返回所有已配置的模型列表（APIKey 脱敏）
func (a *App) GetModels() []Model {
	return a.modelStore.GetModels()
}

// GetModelNames 返回所有模型的名称列表（用于下拉选择）
func (a *App) GetModelNames() []string {
	return a.modelStore.GetModelNames()
}

// AddModel 添加一个新模型配置，保存到本地 JSON 文件
func (a *App) AddModel(model Model) error {
	return a.modelStore.AddModel(model)
}

// UpdateModel 更新已有模型配置。支持重命名：若 model.Name 与旧 name 不同，
// 自动级联更新所有引用该模型名称的会话。
func (a *App) UpdateModel(name string, model Model) error {
	if err := a.modelStore.UpdateModel(name, model); err != nil {
		return err
	}
	// 检查是否发生了重命名
	newName := model.Name
	if newName == "" {
		newName = name
	}
	if newName != name {
		// 级联更新所有会话中的模型引用
		if _, err := a.sessionStore.RenameModelReference(name, newName); err != nil {
			return fmt.Errorf("模型已更新但级联更新会话引用失败: %w", err)
		}
	}
	return nil
}

// DeleteModel 根据模型名称删除配置。若有会话正在引用该模型则拒绝删除。
func (a *App) DeleteModel(name string) error {
	count, err := a.sessionStore.CountSessionsByModel(name)
	if err != nil {
		return fmt.Errorf("检查模型引用失败: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("有 %d 个会话正在使用该模型，请先切换或删除相关会话", count)
	}
	return a.modelStore.DeleteModel(name)
}

// GetModel 根据名称获取模型详情（APIKey 脱敏）
func (a *App) GetModel(name string) (Model, error) {
	return a.modelStore.GetModel(name)
}

// GetModelFull 根据名称获取模型完整信息（不脱敏，供编辑时回填）
func (a *App) GetModelFull(name string) (Model, error) {
	return a.modelStore.GetModelFull(name)
}

// TestModelConnection 测试模型连通性：发送一条简短消息验证 API 配置是否正确
func (a *App) TestModelConnection(model Model) error {
	if model.Name == "" {
		return fmt.Errorf("模型名称不能为空")
	}
	if model.URL == "" {
		return fmt.Errorf("模型 API URL 不能为空")
	}
	modelID := model.ModelID
	if modelID == "" {
		modelID = model.Name
	}
	req := &LLMReq{
		Model:       modelID,
		Temperature: 0,
		Messages: []LLMMessage{
			{Role: RoleSystem, Content: "You are a test assistant. Reply with: OK"},
			{Role: RoleUser, Content: "ping"},
		},
		Stream: false,
	}
	// 复用已有的协议适配逻辑（OpenAI / Anthropic 自动分发）
	// 连接测试没有运行上下文，用 Background 即可（它本身可以被取消也没意义）
	resp, err := callLLMForModel(context.Background(), &model, req)
	if err != nil {
		return err
	}
	if len(resp.Choices) == 0 {
		return fmt.Errorf("模型返回空响应")
	}
	return nil
}

// ===== 会话管理 =====
// CreateSession 创建新会话。
// 未显式指定权限模式时，采用权限配置里建议的默认模式（三层合并的 mode 字段）。
func (a *App) CreateSession(config SessionConfig) (*Session, error) {
	if strings.TrimSpace(config.PermissionMode) == "" {
		config.PermissionMode = string(a.defaultPermissionMode(config.Project))
	}
	return a.sessionStore.CreateSession(config)
}

// defaultPermissionMode 取权限配置建议的默认模式；未配置或不可识别时回退 manual
func (a *App) defaultPermissionMode(projectDir string) Mode {
	rules, _, _ := LoadRuleSet(a.baseDir, projectDir)
	if rules != nil {
		if m := Mode(strings.TrimSpace(rules.Mode)); ValidMode(m) {
			return m
		}
	}
	return ModeManual
}

// ListSessions 返回所有会话（元数据，按最近活跃倒序）
func (a *App) ListSessions() ([]*Session, error) {
	return a.sessionStore.ListSessions()
}

// GetSession 返回完整会话（含消息历史）
func (a *App) GetSession(id string) (*Session, error) {
	return a.sessionStore.GetSession(id)
}

// DeleteSession 删除指定会话
func (a *App) DeleteSession(id string) error {
	// 先连子代理会话一起清掉。子代理刻意不出现在会话列表里，主会话删掉之后再没有
	// 任何入口能发现它们——留下的不只是孤儿文件，还有它们的 checkpoint ref
	// （refs/local-agent/checkpoints/<子代理会话ID>）会永久残留在用户的 .git 里。
	children, err := a.sessionStore.ListSubagentSessions(id)
	if err != nil {
		// 列不出来不阻断主会话的删除，只记一笔——用户要点删除，就该删掉
		fmt.Printf("[App] 列出子代理会话失败: %v\n", err)
	}
	for _, child := range children {
		if dir, derr := a.resolveProjectDir(child.ID); derr == nil && dir != "" {
			_ = DropSessionCheckpoints(dir, child.ID)
		}
		a.diffService.ForgetBaseline(child.ID)
		a.reqLog.forget(child.ID)
		if derr := a.sessionStore.DeleteSession(child.ID); derr != nil {
			fmt.Printf("[App] 删除子代理会话 %s 失败: %v\n", child.ID, derr)
		}
	}
	// 清掉该会话的 checkpoint ref。这是本项目唯一会写入用户仓库 refs/ 的地方，
	// 必须在会话生命周期结束时清理干净，否则会在用户的 .git 里永久残留。
	if dir, err := a.resolveProjectDir(id); err == nil && dir != "" {
		_ = DropSessionCheckpoints(dir, id)
	}
	a.diffService.ForgetBaseline(id)
	a.reqLog.forget(id)
	return a.sessionStore.DeleteSession(id)
}

// ===== 工具配置管理 =====
// ListTools 返回全部工具配置及运行时状态（供工具配置界面渲染）
func (a *App) ListTools() []ToolInfo {
	sources := a.toolStore.GetAll()
	list := make([]ToolInfo, 0, len(sources))
	for _, src := range sources {
		info := ToolInfo{ToolSource: *src, Status: ToolRuntimeStatus{Connected: true}}
		if src.Kind == SourceMCP {
			connected, errMsg := a.toolManager.Pool().Status(src.ID)
			info.Status.Connected = connected
			info.Status.Error = errMsg
			// 已连接用实时子工具数；未连接展示上次发现缓存（都剔除已被单独禁用的子工具）
			if connected {
				if entry, e := a.toolManager.Pool().Connect(context.Background(), src); e == nil {
					disabled := map[string]bool{}
					for _, d := range src.DisabledTools {
						disabled[d] = true
					}
					count := 0
					for _, mt := range entry.tools {
						if !disabled[mt.Name] {
							count++
						}
					}
					info.Status.ToolCount = count
				}
			} else {
				info.Status.ToolCount = len(src.AvailableSubTools())
			}
		}
		list = append(list, info)
	}
	return list
}

// SaveTool 新增或更新工具配置；新工具无 ID 时自动生成
func (a *App) SaveTool(src ToolSource) (*ToolSource, error) {
	if strings.TrimSpace(src.Name) == "" {
		return nil, fmt.Errorf("工具名称不能为空")
	}
	if !validToolName(src.Name) {
		return nil, fmt.Errorf("工具名称只能包含字母、数字、下划线和连字符")
	}
	if !ValidSourceKind(src.Kind) {
		return nil, fmt.Errorf("未知的来源类型: %s", src.Kind)
	}
	// 名称唯一性校验（排除自身）
	for _, t := range a.toolStore.GetAll() {
		if t.Name == src.Name && t.ID != src.ID {
			return nil, fmt.Errorf("工具名称 %q 已存在", src.Name)
		}
	}
	if strings.TrimSpace(src.ID) == "" {
		src.ID = fmt.Sprintf("tool_%d", time.Now().UnixMilli())
	}
	if src.Icon == "" {
		src.Icon = defaultIconForSource(src.Kind)
	}
	if src.Label == "" {
		src.Label = src.Name
	}
	if err := a.toolStore.Save(&src); err != nil {
		return nil, err
	}
	// 配置变更后关闭旧 MCP 连接，下次对话/测试时按新配置重连
	if src.Kind == SourceMCP {
		a.toolManager.Pool().Close(src.ID)
	}
	return &src, nil
}

// SetSubToolEnabled 启用/停用某个 MCP 子工具（tool 传服务器上的原始名）
func (a *App) SetSubToolEnabled(id, tool string, enabled bool) error {
	return a.toolStore.SetSubToolEnabled(id, tool, enabled)
}

// SetExposure 设置来源的暴露策略：direct（直出给模型）/ router（经路由器发现）/ internal（模型不可见）
func (a *App) SetExposure(id string, exposure string) error {
	return a.toolStore.SetExposure(id, Exposure(strings.TrimSpace(exposure)))
}

// DeleteTool 删除工具配置（内置工具拒绝）
func (a *App) DeleteTool(id string) error {
	a.toolManager.Pool().Close(id)
	return a.toolStore.Delete(id)
}

// ToggleTool 启用/停用工具；停用时关闭其 MCP 连接
func (a *App) ToggleTool(id string, enabled bool) error {
	if err := a.toolStore.SetEnabled(id, enabled); err != nil {
		return err
	}
	if !enabled {
		a.toolManager.Pool().Close(id)
	}
	return nil
}

// TestToolConnection 测试 MCP server 连接并发现工具；成功后缓存发现结果。
// 传入未保存的配置（ID 为空）也可测试，使用临时连接不污染缓存。
func (a *App) TestToolConnection(cfg ToolSource) ([]MCPToolMeta, error) {
	if cfg.Kind != SourceMCP {
		return nil, fmt.Errorf("仅 MCP 来源支持连接测试")
	}
	if reason := cfg.MCP.Validate(); reason != "" {
		return nil, fmt.Errorf("%s", reason)
	}
	temp := cfg.ID == ""
	if temp {
		cfg.ID = fmt.Sprintf("__test_%d", time.Now().UnixMilli())
	} else {
		// 已保存工具：先断开旧连接，确保按最新配置测试
		a.toolManager.Pool().Close(cfg.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	entry, err := a.toolManager.Pool().Connect(ctx, &cfg)
	if temp {
		defer a.toolManager.Pool().Close(cfg.ID)
	}
	if err != nil {
		return nil, err
	}
	metas := make([]MCPToolMeta, 0, len(entry.tools))
	for _, t := range entry.tools {
		metas = append(metas, MCPToolMeta{Name: t.Name, Description: t.Description})
	}
	if !temp {
		_ = a.toolStore.UpdateDiscovered(cfg.ID, metas)
	}
	return metas, nil
}

// validToolName 校验 LLM 函数名：字母/数字/下划线/连字符
func validToolName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// ===== 技能管理（Skills）=====
// ListSkills 返回全部技能元数据（不含正文）
func (a *App) ListSkills() []*SkillMeta {
	list := a.skillStore.GetAll()
	if list == nil {
		return []*SkillMeta{}
	}
	return list
}

// GetSkill 返回含正文与完整 frontmatter 的技能详情
func (a *App) GetSkill(id string) (*SkillDetail, error) {
	d, ok := a.skillStore.GetDetail(id)
	if !ok {
		return nil, fmt.Errorf("技能不存在: %s", id)
	}
	return d, nil
}

// SaveSkill 新建或更新技能（写入 <id>/SKILL.md，走标准 frontmatter 序列化）
func (a *App) SaveSkill(id string, draft SkillDraft) error {
	return a.skillStore.SaveSkill(id, draft)
}

// DeleteSkill 删除技能目录（内置拒绝）
func (a *App) DeleteSkill(id string) error {
	return a.skillStore.DeleteSkill(id)
}

// ToggleSkill 启用/停用技能
func (a *App) ToggleSkill(id string, enabled bool) error {
	return a.skillStore.SetEnabled(id, enabled)
}

// RefreshSkills 重新扫描技能目录并返回最新元数据
func (a *App) RefreshSkills() []*SkillMeta {
	a.skillStore.Refresh()
	return a.ListSkills()
}

// SkillsDir 返回技能目录绝对路径（供界面「打开目录」）
func (a *App) SkillsDir() string {
	return a.skillStore.Dir()
}

// ===== 技能安装 / 卸载 / 更新 =====
// InstallSkillFromFolder 从本地文件夹安装（文件夹本身是技能，或是装着若干技能的父目录）
func (a *App) InstallSkillFromFolder(path string) ([]*SkillInstallResult, error) {
	return a.skillInstall.InstallFromFolder(path)
}

// InstallSkillFromZip 从 zip 压缩包安装
func (a *App) InstallSkillFromZip(path string) ([]*SkillInstallResult, error) {
	return a.skillInstall.InstallFromZip(path)
}

// InstallSkillFromGit 从 Git 仓库安装；ref 可为空，subdir 为仓库内子目录
func (a *App) InstallSkillFromGit(url, ref, subdir string) ([]*SkillInstallResult, error) {
	return a.skillInstall.InstallFromGit(url, ref, subdir)
}

// UpdateSkill 重新拉取 git 来源的技能；失败时旧版本保持原样
func (a *App) UpdateSkill(id string) (*SkillInstallResult, error) {
	return a.skillInstall.Update(id)
}

// ListSkillResources 列出技能目录内的可读资源（界面展示 L3 内容）
func (a *App) ListSkillResources(id string) ([]SkillResource, error) {
	return a.skillStore.ListSkillResources(id)
}

// PickSkillFolder 打开目录选择对话框，挑选技能来源文件夹
func (a *App) PickSkillFolder() (string, error) {
	return wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择技能文件夹（其中含 SKILL.md）",
	})
}

// PickSkillZip 打开文件选择对话框，挑选技能压缩包。
// 这里刻意不设 Filters：Wails 的 FileFilter 类型名无法在开发环境核实，
// 而过滤只是体验优化（安装器自己会校验是不是合法 zip），不值得为此冒编译风险。
// 确认 FileFilter 可用后可自行补上：
//
//	Filters: []wailsRuntime.FileFilter{{DisplayName: "Zip 压缩包 (*.zip)", Pattern: "*.zip"}}
func (a *App) PickSkillZip() (string, error) {
	return wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择技能压缩包",
	})
}

// enabledSkillsForSession 按会话白名单过滤技能；白名单为空=全部技能
func (a *App) enabledSkillsForSession(session *Session) []*SkillMeta {
	all := a.skillStore.GetAll()
	if len(session.EnabledSkills) == 0 {
		return all
	}
	wl := make(map[string]bool, len(session.EnabledSkills))
	for _, id := range session.EnabledSkills {
		wl[id] = true
	}
	out := make([]*SkillMeta, 0, len(all))
	for _, m := range all {
		if wl[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// ===== 目录选择 =====
// PickDirectory 打开系统目录选择对话框，返回所选目录路径；用户取消时返回空字符串
func (a *App) PickDirectory() (string, error) {
	return wailsRuntime.OpenDirectoryDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择工作区文件夹",
	})
}

// ===== 差异视图 =====
// resolveProjectDir 返回会话项目目录；为空时回退到进程工作目录
func (a *App) resolveProjectDir(sessionID string) (string, error) {
	s, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return "", err
	}
	if s.Project != "" {
		return s.Project, nil
	}
	return os.Getwd()
}

// GetDiff 返回会话工作区相对基线的累计差异
func (a *App) GetDiff(sessionID string) ([]DiffFile, error) {
	dir, err := a.resolveProjectDir(sessionID)
	if err != nil {
		return []DiffFile{}, err
	}
	if !a.diffService.IsRepo(dir) {
		return []DiffFile{}, nil // 非 git 仓库不报错，返回空列表
	}
	// 恢复会话里持久化的 diff 状态（基线 + 已归因的路径），再只算这些路径的差异。
	// 这样两个会话开在同一个项目里也不会互相看到对方的改动。
	if s, err := a.sessionStore.GetSession(sessionID); err == nil {
		a.diffService.RestoreSession(sessionID, s.DiffBaseline, s.DiffTouched)
	}
	files, err := a.diffService.DiffForSession(sessionID, dir)
	if err != nil {
		return []DiffFile{}, err
	}
	return files, nil
}

// GetDiffTurns 返回按轮次分组的差异，索引 0 为“累计”
func (a *App) GetDiffTurns(sessionID string) ([]DiffTurn, error) {
	s, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	turns := make([]DiffTurn, 0, len(s.Diffs)+1)
	if dir, err := a.resolveProjectDir(sessionID); err == nil && a.diffService.IsRepo(dir) {
		if files, err := a.GetDiff(sessionID); err == nil {
			turns = append(turns, DiffTurn{
				Turn:      0,
				Label:     "累计",
				Files:     files,
				Additions: sumAdd(files),
				Deletions: sumDel(files),
				CreatedAt: time.Now().UnixMilli(),
			})
		}
	}
	turns = append(turns, s.Diffs...)
	return turns, nil
}

// AppendMessage 向会话追加一条消息并持久化
func (a *App) AppendMessage(sessionID string, message Message) (*Message, error) {
	return a.sessionStore.AppendMessage(sessionID, message)
}

// AppendConversation 追加一轮完整对话记录
func (a *App) AppendConversation(sessionID string, conversation *Conversation) error {
	return a.sessionStore.AppendConversation(sessionID, conversation)
}

// UpdateSession 更新会话元数据（模型、权限模式、标题、状态等）
func (a *App) UpdateSession(id string, patch SessionPatch) (*Session, error) {
	return a.sessionStore.UpdateSession(id, patch)
}

// ===== 对话能力（融合 01agent 的核心对话流程）=====
// Chat 发送消息并获取 AI 回复（真正的 LLM 调用，支持多轮工具调用）
// 前端调用此方法前应先监听 "chat:event" 事件以接收工具调用中间状态
// useStream=true 时以 SSE 流式请求模型，文本分片通过 chat:event 的 reply_delta 事件推送
// usePlan=true 时走规划流程（ChatPlan）：产出可审核的结构化计划而非直接执行
func (a *App) Chat(sessionID string, query string, useStream bool, usePlan bool) (*ChatResult, error) {
	// /context-stat 是纯读操作：只报告用量与压缩状态，不改任何状态。
	// 同样放在最前面拦截——内置命令优先于同名技能。
	if cmd, _, ok := ParseSkillCommand(query); ok && cmd == contextStatCmd {
		return a.handleContextStatCommand(sessionID)
	}
	// 手动压缩：/compact 不是一次对话，而是对当前上下文的一次维护操作。
	// 放在最前面拦截——内置命令的优先级高于同名技能，技能不该能覆盖掉它。
	if cmd, _, ok := ParseSkillCommand(query); ok && cmd == contextCompactCmd {
		return a.handleCompactCommand(sessionID)
	}
	var result *ChatResult
	if usePlan {
		result = a.ChatPlan(sessionID, query, useStream)
	} else {
		result = a.executeChat(sessionID, query, useStream)
	}
	// 取消不走错误路径：它是用户的主动行为，结果照常返回（前端据 cancelled 字段
	// 显示"已停止"）。否则用户点一下停止会看到"发送失败：执行已取消"。
	if result.Error != "" && result.Reply == "" && !result.Cancelled {
		return result, fmt.Errorf("%s", result.Error)
	}
	return result, nil
}

// ===== 上下文用量与手动压缩（P1-B）=====
// GetContextStat 返回某会话当前的上下文用量，供界面显示。
// 这是**近似值**：不含系统提示与工具定义的精确开销（那两者只有运行期才知道），
// 用于给用户一个大致的占用比例，不用于任何判定。
func (a *App) GetContextStat(sessionID string) (*ContextStat, error) {
	st := a.contextStatForSession(sessionID)
	if st == nil {
		return nil, fmt.Errorf("会话不存在: %s", sessionID)
	}
	return st, nil
}

// contextStatForSession 组装界面用的上下文用量（会话不存在时返回 nil）
func (a *App) contextStatForSession(sessionID string) *ContextStat {
	session, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil
	}
	model, _ := a.modelStore.GetModelForCall(session.Model)
	messages := buildRunMessages(session, "", false)
	return contextStatOf(session, messages, contextWindowOf(&model), nil, 0, a.calibFor(session.Model))
}

// handleCompactCommand 处理 /compact：压缩当前会话上下文，并把结果作为一条
// assistant 回复返回——前端照常渲染，用户能直接看到省下了多少。
func (a *App) handleCompactCommand(sessionID string) (*ChatResult, error) {
	// 与普通对话一样登记为可取消的运行：压缩要额外发一次 LLM 请求，
	// 硬取消同样应该能把它断掉，而不是让用户白等。
	run := a.runs.begin(sessionID, "")
	defer a.runs.end(run)
	out, err := a.compactSessionContext(run.Ctx(), sessionID)
	if err != nil {
		return nil, err
	}
	var reply string
	if out.Compressed {
		reply = fmt.Sprintf("已压缩上下文：摘要覆盖前 %d 条消息，摘要正文 %d 字，估算省下约 %d 个输入 token。\n原始对话记录仍完整保留，清空摘要即可回到全量上下文。",
			out.CoveredMsgs, out.SummaryChars, out.SavedTokens)
	} else {
		reply = "本次未压缩：" + out.Reason
	}
	// 落一条 assistant 消息：保持 user/assistant 成对，也让这次操作留在会话记录里
	result := &ChatResult{Reply: reply}
	if saved, serr := a.sessionStore.AppendMessage(sessionID, Message{Role: RoleAssistant, Content: reply}); serr == nil {
		result.Messages = []Message{*saved}
	}
	a.emitChatEvent(ChatEvent{Type: "done", Reply: reply})
	result.Context = a.contextStatForSession(sessionID)
	return result, nil
}

// handleContextStatCommand 处理 /context-stat：打印用量 / 窗口、被摘要覆盖的条数、
// 省下的 token、以及"是否存在 token 锚点"。
//
// 它是**排障命令**：用户看到用量突然掉一半时，第一个该问的就是这里。
// 与 /compact 同样落一条 assistant 消息——保持 user/assistant 成对，
// 也让这次查询留在会话记录里（调试记录本身也是证据）。
func (a *App) handleContextStatCommand(sessionID string) (*ChatResult, error) {
	st := a.contextStatForSession(sessionID)
	if st == nil {
		return nil, fmt.Errorf("会话不存在: %s", sessionID)
	}
	reply := FormatContextStatReport(st, a.keepRecentMsgs())
	result := &ChatResult{Reply: reply}
	if saved, serr := a.sessionStore.AppendMessage(sessionID, Message{Role: RoleAssistant, Content: reply}); serr == nil {
		result.Messages = []Message{*saved}
	}
	a.emitChatEvent(ChatEvent{Type: "done", Reply: reply})
	// 统计在落库之后重算：这条报告消息自己也占上下文，报出去的数字应包含它，
	// 否则用户紧接着再查一次会发现两边对不上。
	result.Context = a.contextStatForSession(sessionID)
	return result, nil
}

// ===== 上下文压缩策略（可配置项）=====
// GetContextPrefs 返回当前上下文压缩偏好（供设置界面显示）
func (a *App) GetContextPrefs() ContextPrefs {
	if a.contextPrefs == nil {
		return ContextPrefs{KeepRecentMsgs: contextKeepRecentMsgs}
	}
	return a.contextPrefs.Get()
}

// SetContextKeepRecentMsgs 更新"压缩时保留最近多少条原文"，返回更新后的偏好。
func (a *App) SetContextKeepRecentMsgs(n int) (ContextPrefs, error) {
	if a.contextPrefs == nil {
		return ContextPrefs{}, fmt.Errorf("上下文配置存储未初始化")
	}
	if err := a.contextPrefs.SetKeepRecentMsgs(n); err != nil {
		return ContextPrefs{}, err
	}
	return a.contextPrefs.Get(), nil
}

// ===== 计划管理（Plan & Execute）=====
// GetSessionPlan 返回会话当前（最新创建的）计划；无计划时返回 nil
func (a *App) GetSessionPlan(sessionID string) *Plan {
	return a.planStore.GetBySession(sessionID)
}

// ListPlans 返回会话全部历史计划（按创建时间倒序）
func (a *App) ListPlans(sessionID string) []*Plan {
	return a.planStore.ListBySession(sessionID)
}

// SavePlan 保存审核阶段的计划编辑（仅 awaiting_approval 可改；只更新标题与步骤）
func (a *App) SavePlan(plan *Plan) error {
	if plan == nil || plan.ID == "" {
		return fmt.Errorf("计划不能为空")
	}
	cur, err := a.planStore.Get(plan.ID)
	if err != nil {
		return err
	}
	if cur.Status != PlanAwaitingApproval {
		return fmt.Errorf("计划当前状态不可编辑: %s", cur.Status)
	}
	if len(plan.Steps) == 0 {
		return fmt.Errorf("计划至少需要包含一个步骤")
	}
	cur.Title = plan.Title
	cur.Steps = plan.Steps
	for i, st := range cur.Steps {
		st.Index = i
		st.Title = strings.TrimSpace(st.Title)
		if st.Status == "" {
			st.Status = StepPending
		}
	}
	return a.planStore.Save(cur)
}

// ExecutePlan 逐步执行计划（长耗时，结束时返回；过程事件经 chat:event 推送）
func (a *App) ExecutePlan(planID string, useStream bool) *ChatResult {
	return a.executePlan(planID, useStream)
}

// ReopenPlan 把已结束的计划退回待审核（失败后「修改后重试」的入口）。
// 已完成的步骤保留，失败/跳过/中断的步骤重置为待执行。
func (a *App) ReopenPlan(planID string) (*Plan, error) {
	plan, err := a.planStore.Reopen(planID)
	if err != nil {
		return nil, err
	}
	a.emitPlanUpdate(plan, -1)
	return plan, nil
}

// CancelPlan 取消执行中的计划：协作式取消（软），当前步骤跑完后在下一步边界停止。
//
// 找不到活跃运行时（执行协程已异常退出，而计划还停在 running），
// 强制把计划收尾为 cancelled —— 否则前端只能重启应用才能脱困。
// 计划本身已不在 running 时明确报错，且不改动它的状态。
func (a *App) CancelPlan(planID string) error {
	if hit, _ := a.runs.stopPlan(planID, false); hit {
		return nil
	}
	plan, err := a.planStore.Get(planID)
	if err != nil {
		return err
	}
	if plan.Status != PlanRunning {
		return fmt.Errorf("计划未在执行中: %s", planID)
	}
	for _, st := range plan.Steps {
		if st.Status == StepRunning {
			st.Status = StepFailed
			st.Error = "执行中断（已强制取消）"
			st.FinishedAt = time.Now().UnixMilli()
		}
	}
	markRemainingSkipped(plan, 0)
	plan.Status = PlanCancelled
	if err := a.planStore.Save(plan); err != nil {
		return err
	}
	a.emitPlanUpdate(plan, -1)
	return nil
}

// ===== 运行停止（普通聊天与计划执行共用）=====
// StopChat 停止某会话当前正在跑的运行。
//
//	hard=false 协作式：当前 LLM 请求与工具调用跑完，下一轮不再开始。
//	hard=true  硬取消：在协作式之上 cancel 运行 ctx —— 在途的 LLM 请求立即断开、
//	                   正在跑的子进程被 kill、等待中的提问立即以"取消"结束。
//
// 返回 hit=false 表示当前没有活跃运行，前端应据此复位按钮，
// 避免出现"点了停止却没反应"的假象。
//
// 注意：挂起的授权弹窗与提问由前端在调用本方法之前先自行结掉
// （CancelPermissionWait / CancelAskUser），这里不重复处理——
// 那两条路是"用户在弹窗上的选择"，与"停止运行"是两件事。
func (a *App) StopChat(sessionID string, hard bool) (bool, error) {
	if sessionID == "" {
		return false, fmt.Errorf("会话 ID 不能为空")
	}
	hit, _ := a.runs.stopSession(sessionID, hard)
	return hit, nil
}

// ===== 文件改动归因（供差异面板与工具卡片展示）=====
// GetToolFileChanges 返回本会话由文件工具产生的改动，可归因到具体工具调用。
// 记录里带统一 diff 文本，这里按差异面板的结构解析后返回。
func (a *App) GetToolFileChanges(sessionID string) []ToolFileChange {
	list := a.fileChanges.List(sessionID)
	out := make([]ToolFileChange, 0, len(list))
	for _, c := range list {
		// 差异文本里的文件名是临时的 before/after 占位，这里改回真实路径，
		// 便于界面直接按路径展示
		files := ParseUnifiedDiff(c.Diff)
		for i := range files {
			files[i].Path = c.Rel
			files[i].OldPath = ""
		}
		out = append(out, ToolFileChange{
			Time:    c.Time,
			Tool:    c.Tool,
			Path:    c.Path,
			Rel:     c.Rel,
			Action:  c.Action,
			Added:   c.Added,
			Removed: c.Removed,
			Files:   files,
		})
	}
	return out
}

// ClearToolFileChanges 清空本会话的文件改动记录
func (a *App) ClearToolFileChanges(sessionID string) error {
	a.fileChanges.Clear(sessionID)
	return nil
}
