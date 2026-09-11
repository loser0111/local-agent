package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// App struct
type App struct {
	ctx          context.Context
	modelStore   *ModelStore
	sessionStore *SessionStore
	toolManager  *ToolManager
	diffService  *DiffService
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

	// 初始化工具管理器（CLI 工具 + 元工具路由器）
	a.toolManager = NewToolManager()

	// 初始化差异服务（基于 git 快照计算工作区 diff）
	a.diffService = NewDiffService()
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

// GetModels 返回所有已配置的模型列表
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

// DeleteModel 根据模型名称删除配置
func (a *App) DeleteModel(name string) error {
	return a.modelStore.DeleteModel(name)
}

// GetModel 根据名称获取模型详情
func (a *App) GetModel(name string) (Model, error) {
	return a.modelStore.GetModel(name)
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
func (a *App) Chat(sessionID string, query string) (*ChatResult, error) {
	result := a.executeChat(sessionID, query)
	if result.Error != "" && result.Reply == "" {
		return result, fmt.Errorf("%s", result.Error)
	}
	return result, nil
}
