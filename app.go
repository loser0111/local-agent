package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx          context.Context
	modelStore   *ModelStore
	sessionStore *SessionStore
	toolStore    *ToolStore
	toolManager  *ToolManager
	skillStore   *SkillStore
	diffService  *DiffService
	planStore    *PlanStore

	// 计划执行取消注册表：planID -> 取消信号（close 即取消）
	planCancels map[string]chan struct{}
	planMu      sync.Mutex
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
	a.skillStore.EnsureDefaultSkill() // 首次运行写入示例技能

	// 初始化工具配置存储与工具管理器（~/.local-agent/tools.json）
	a.toolStore = NewToolStore(filepath.Join(baseDir, "tools.json"))
	a.toolManager = NewToolManager(a.toolStore, a.skillStore)

	// 初始化差异服务（基于 git 快照计算工作区 diff）
	a.diffService = NewDiffService()

	// 初始化计划存储：~/.local-agent/plans/（重启时 running 计划自动置为失败）
	a.planStore = NewPlanStore(filepath.Join(baseDir, "plans"))
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
	resp, err := callLLMForModel(&model, req)
	if err != nil {
		return err
	}
	if len(resp.Choices) == 0 {
		return fmt.Errorf("模型返回空响应")
	}
	return nil
}

// ===== 会话管理 =====

// CreateSession 创建新会话
func (a *App) CreateSession(config SessionConfig) (*Session, error) {
	return a.sessionStore.CreateSession(config)
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
	a.diffService.ForgetBaseline(id)
	return a.sessionStore.DeleteSession(id)
}

// ===== 工具配置管理 =====

// ListTools 返回全部工具配置及运行时状态（供工具配置界面渲染）
func (a *App) ListTools() []ToolInfo {
	configs := a.toolStore.GetAll()
	list := make([]ToolInfo, 0, len(configs))
	for _, cfg := range configs {
		info := ToolInfo{ToolConfig: *cfg, Status: ToolRuntimeStatus{Connected: true}}
		if cfg.Type == ToolTypeMCP {
			connected, errMsg := a.toolManager.Pool().Status(cfg.ID)
			info.Status.Connected = connected
			info.Status.Error = errMsg
			// 已连接用实时工具数（剔除禁用项）；未连接展示上次发现缓存
			if connected {
				if entry, e := a.toolManager.Pool().Connect(context.Background(), cfg); e == nil {
					disabled := map[string]bool{}
					for _, d := range cfg.DisabledTools {
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
				info.Status.ToolCount = len(cfg.Discovered)
			}
		}
		list = append(list, info)
	}
	return list
}

// SaveTool 新增或更新工具配置；新工具无 ID 时自动生成
func (a *App) SaveTool(cfg ToolConfig) (*ToolConfig, error) {
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, fmt.Errorf("工具名称不能为空")
	}
	if !validToolName(cfg.Name) {
		return nil, fmt.Errorf("工具名称只能包含字母、数字、下划线和连字符")
	}
	// 名称唯一性校验（排除自身与 MCP 子工具前缀）
	for _, t := range a.toolStore.GetAll() {
		if t.Name == cfg.Name && t.ID != cfg.ID {
			return nil, fmt.Errorf("工具名称 %q 已存在", cfg.Name)
		}
	}
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("tool_%d", time.Now().UnixMilli())
	}
	if cfg.Icon == "" {
		cfg.Icon = defaultIconForType(cfg.Type)
	}
	if cfg.Label == "" {
		cfg.Label = cfg.Name
	}
	if err := a.toolStore.Save(&cfg); err != nil {
		return nil, err
	}
	// 配置变更后关闭旧 MCP 连接，下次对话/测试时按新配置重连
	if cfg.Type == ToolTypeMCP {
		a.toolManager.Pool().Close(cfg.ID)
	}
	return &cfg, nil
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
func (a *App) TestToolConnection(cfg ToolConfig) ([]MCPToolMeta, error) {
	if cfg.Type != ToolTypeMCP {
		return nil, fmt.Errorf("仅 MCP 类型工具支持连接测试")
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

// defaultIconForType 各工具类型的默认 Lucide 图标名
func defaultIconForType(t string) string {
	switch t {
	case ToolTypeCLI:
		return "terminal"
	case ToolTypeMCP:
		return "blocks"
	case ToolTypeAPI:
		return "webhook"
	default:
		return "zap"
	}
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

// GetSkill 返回含正文的技能详情
func (a *App) GetSkill(id string) (*SkillDetail, error) {
	d, ok := a.skillStore.GetDetail(id)
	if !ok {
		return nil, fmt.Errorf("技能不存在: %s", id)
	}
	return d, nil
}

// SaveSkill 新建或更新技能（写入 <id>/SKILL.md）
func (a *App) SaveSkill(id, name, description, body string) error {
	return a.skillStore.SaveSkill(id, name, description, body)
}

// DeleteSkill 删除技能目录（内置拒绝）
func (a *App) DeleteSkill(id string) error {
	return a.skillStore.DeleteSkill(id)
}

// ToggleSkill 启用/停用技能
func (a *App) ToggleSkill(id string, enabled bool) error {
	return a.skillStore.SetEnabled(id, enabled)
}

// SetSkillAlwaysInject 设置「强制注入正文」
func (a *App) SetSkillAlwaysInject(id string, v bool) error {
	return a.skillStore.SetAlwaysInject(id, v)
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
	base := a.diffService.GetBaseline(sessionID) // 只读取，不懒初始化
	files, err := a.diffService.Diff(dir, base)
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
	var result *ChatResult
	if usePlan {
		result = a.ChatPlan(sessionID, query, useStream)
	} else {
		result = a.executeChat(sessionID, query, useStream)
	}
	if result.Error != "" && result.Reply == "" {
		return result, fmt.Errorf("%s", result.Error)
	}
	return result, nil
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

// CancelPlan 取消执行中的计划：协作式取消，当前步骤跑完后在下一步边界停止
func (a *App) CancelPlan(planID string) error {
	a.planMu.Lock()
	defer a.planMu.Unlock()
	ch, ok := a.planCancels[planID]
	if !ok {
		return fmt.Errorf("计划未在执行中: %s", planID)
	}
	close(ch)
	delete(a.planCancels, planID)
	return nil
}

// registerPlanCancel 注册计划取消信号（ExecutePlan 开始时调用）
func (a *App) registerPlanCancel(planID string) chan struct{} {
	a.planMu.Lock()
	defer a.planMu.Unlock()
	if a.planCancels == nil {
		a.planCancels = map[string]chan struct{}{}
	}
	ch := make(chan struct{})
	a.planCancels[planID] = ch
	return ch
}

// unregisterPlanCancel 注销计划取消信号（ExecutePlan 结束时调用）
func (a *App) unregisterPlanCancel(planID string) {
	a.planMu.Lock()
	defer a.planMu.Unlock()
	delete(a.planCancels, planID)
}
