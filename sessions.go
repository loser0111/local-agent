package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ===== 数据结构（借鉴 01agent 的 SessionHistory/Conversation 设计） =====

// ToolCall 一次工具调用记录
type ToolCall struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Args     map[string]interface{} `json:"args,omitempty"`
	Status   string                 `json:"status"`
	Duration float64                `json:"duration,omitempty"`
	Result   string                 `json:"result,omitempty"`
}

// Message 会话中的一条聊天消息
type Message struct {
	ID         string     `json:"id"`
	Role       string     `json:"role"` // user / assistant / system / tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"` // role=tool 时对应的 tool_call_id
	CreatedAt  int64      `json:"createdAt"`            // Unix 毫秒
}

// Conversation 单轮对话记录（用户提问 + AI 回答），借鉴 01agent
type Conversation struct {
	Index     int    `json:"index"`
	Query     string `json:"query"`
	Answer    string `json:"answer"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
	Error     string `json:"error,omitempty"`
}

// ===== 视图模式（会话级）=====
//
// 控制聊天流里「工具调用过程块」的展示粒度，取自 Claude Code 桌面端的视图模式设计：
//   - verbose：完整展示工具调用过程（过程块常展开，每张工具卡片默认展开参数与输出）
//   - normal ：平衡展示（默认）：执行中展开、完成后折叠，可手动展开
//   - summary：仅保留一行摘要，隐藏工具调用细节（工具报错时强制展开，避免漏掉失败）
const (
	ViewModeVerbose = "verbose"
	ViewModeNormal  = "normal"
	ViewModeSummary = "summary"
)

// DefaultViewMode 新建会话的默认视图模式
const DefaultViewMode = ViewModeNormal

// normalizeViewMode 把空值或非法值兜底为默认模式。
// 旧会话文件没有 viewMode 字段（反序列化为空串），走这里补齐为 normal。
func normalizeViewMode(mode string) string {
	switch mode {
	case ViewModeVerbose, ViewModeNormal, ViewModeSummary:
		return mode
	default:
		return DefaultViewMode
	}
}

// Session 一个完整会话，每个会话持久化为一个 JSON 文件
type Session struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Project        string          `json:"project"`
	Model          string          `json:"model"`
	PermissionMode string          `json:"permissionMode"`
	ViewMode       string          `json:"viewMode"` // 视图模式：控制工具调用过程的展示粒度
	Environment    string          `json:"environment"`
	Status         string          `json:"status"` // active / completed / archived
	StartAt        int64           `json:"startAt"`
	EndAt          int64           `json:"endAt"`
	Messages       []Message       `json:"messages"`
	Conversations  []*Conversation `json:"conversations"`
	Diffs          []DiffTurn      `json:"diffs,omitempty"`         // 每轮对话产生的文件差异
	EnabledTools   []string        `json:"enabledTools,omitempty"`  // 本会话可用的工具 ID 白名单（空=全部已启用工具）
	EnabledSkills  []string        `json:"enabledSkills,omitempty"` // 本会话可用的技能 ID 白名单（空=全部已启用技能）
}

// SessionConfig 创建会话时的配置
type SessionConfig struct {
	Title          string   `json:"title"`
	Project        string   `json:"project"`
	Model          string   `json:"model"`
	PermissionMode string   `json:"permissionMode"`
	ViewMode       string   `json:"viewMode"` // 视图模式：控制工具调用过程的展示粒度
	Environment    string   `json:"environment"`
	EnabledTools   []string `json:"enabledTools"`  // 本会话可用的工具 ID 白名单（空=全部已启用工具）
	EnabledSkills  []string `json:"enabledSkills"` // 本会话可用的技能 ID 白名单（空=全部已启用技能）
}

// SessionPatch 会话部分字段更新（指针为 nil 表示不更新）
type SessionPatch struct {
	Title          *string `json:"title,omitempty"`
	Model          *string `json:"model,omitempty"`
	PermissionMode *string `json:"permissionMode,omitempty"`
	ViewMode       *string `json:"viewMode,omitempty"`
	Status         *string `json:"status,omitempty"`
	Project        *string `json:"project,omitempty"`
}

// SessionStore 会话存储：每个会话一个 JSON 文件
type SessionStore struct {
	mu  sync.RWMutex
	dir string
}

// NewSessionStore 创建会话存储并确保目录存在
func NewSessionStore(dir string) *SessionStore {
	_ = os.MkdirAll(dir, 0o755)
	return &SessionStore{dir: dir}
}

func (s *SessionStore) sessionPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// CreateSession 创建新会话并写入文件
func (s *SessionStore) CreateSession(config SessionConfig) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	session := &Session{
		ID:             generateSessionID(),
		Title:          config.Title,
		Project:        config.Project,
		Model:          config.Model,
		PermissionMode: config.PermissionMode,
		ViewMode:       config.ViewMode,
		Environment:    config.Environment,
		Status:         "active",
		StartAt:        now,
		EndAt:          now,
		Messages:       []Message{},
		Conversations:  []*Conversation{},
		EnabledTools:   config.EnabledTools,
		EnabledSkills:  config.EnabledSkills,
	}
	if session.Title == "" {
		session.Title = "新会话"
	}
	if session.PermissionMode == "" {
		session.PermissionMode = "manual"
	}
	session.ViewMode = normalizeViewMode(session.ViewMode)
	if session.Environment == "" {
		session.Environment = "local"
	}

	if err := s.saveSession(session); err != nil {
		return nil, err
	}
	return session, nil
}

// GetSession 按 ID 加载完整会话（含消息）
func (s *SessionStore) GetSession(id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadSession(id)
}

func (s *SessionStore) loadSession(id string) (*Session, error) {
	data, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("会话 %q 不存在", id)
		}
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("解析会话失败: %w", err)
	}
	if session.Messages == nil {
		session.Messages = []Message{}
	}
	if session.Conversations == nil {
		session.Conversations = []*Conversation{}
	}
	// 旧会话文件没有 viewMode 字段，统一兜底，避免前端拿到空值
	session.ViewMode = normalizeViewMode(session.ViewMode)
	return &session, nil
}

// ListSessions 列出所有会话元数据（不含消息内容），按最近活跃倒序
func (s *SessionStore) ListSessions() ([]*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Session{}, nil
		}
		return nil, fmt.Errorf("读取会话目录失败: %w", err)
	}

	sessions := make([]*Session, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		session, err := s.loadSession(id)
		if err != nil {
			continue // 跳过损坏文件
		}
		// 列表不携带消息，减小数据量
		session.Messages = []Message{}
		session.Conversations = []*Conversation{}
		session.Diffs = nil
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].EndAt > sessions[j].EndAt
	})
	return sessions, nil
}

// DeleteSession 删除会话文件
func (s *SessionStore) DeleteSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.sessionPath(id)); os.IsNotExist(err) {
		return fmt.Errorf("会话 %q 不存在", id)
	}
	if err := os.Remove(s.sessionPath(id)); err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

// AppendMessage 向会话追加一条消息并持久化；自动更新标题与活跃时间
func (s *SessionStore) AppendMessage(id string, msg Message) (*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.loadSession(id)
	if err != nil {
		return nil, err
	}

	if msg.ID == "" {
		msg.ID = generateMessageID()
	}
	if msg.CreatedAt == 0 {
		msg.CreatedAt = time.Now().UnixMilli()
	}
	if msg.Role == "" {
		return nil, fmt.Errorf("消息角色不能为空")
	}

	session.Messages = append(session.Messages, msg)
	session.EndAt = msg.CreatedAt
	session.Status = "active"

	// 首条用户消息自动作为会话标题
	if msg.Role == "user" && (session.Title == "" || session.Title == "新会话") {
		session.Title = truncateTitle(msg.Content, 30)
	}

	if err := s.saveSession(session); err != nil {
		return nil, err
	}
	return &msg, nil
}

// AppendConversation 追加一轮完整对话（query+answer），借鉴 01agent 的 Conversations 记录
func (s *SessionStore) AppendConversation(id string, conv *Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.loadSession(id)
	if err != nil {
		return err
	}
	conv.Index = len(session.Conversations) + 1
	session.Conversations = append(session.Conversations, conv)
	if conv.EndTime > session.EndAt {
		session.EndAt = conv.EndTime
	}
	return s.saveSession(session)
}

// AppendDiff 追加一轮差异并落盘，轮次号自动递增，返回本轮轮次号
func (s *SessionStore) AppendDiff(id string, turn DiffTurn) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.loadSession(id)
	if err != nil {
		return 0, err
	}
	turn.Turn = len(session.Diffs) + 1
	if turn.Label == "" {
		turn.Label = fmt.Sprintf("第 %d 轮", turn.Turn)
	}
	session.Diffs = append(session.Diffs, turn)
	if err := s.saveSession(session); err != nil {
		return 0, err
	}
	return turn.Turn, nil
}

// UpdateSession 更新会话元数据
func (s *SessionStore) UpdateSession(id string, patch SessionPatch) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.loadSession(id)
	if err != nil {
		return nil, err
	}
	if patch.Title != nil {
		session.Title = *patch.Title
	}
	if patch.Model != nil {
		session.Model = *patch.Model
	}
	if patch.PermissionMode != nil {
		session.PermissionMode = *patch.PermissionMode
	}
	if patch.ViewMode != nil {
		session.ViewMode = normalizeViewMode(*patch.ViewMode)
	}
	if patch.Status != nil {
		session.Status = *patch.Status
	}
	if patch.Project != nil {
		session.Project = *patch.Project
	}
	session.EndAt = time.Now().UnixMilli()

	if err := s.saveSession(session); err != nil {
		return nil, err
	}
	return session, nil
}

// saveSession 调用方需持有锁
func (s *SessionStore) saveSession(session *Session) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("创建会话目录失败: %w", err)
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化会话失败: %w", err)
	}
	if err := os.WriteFile(s.sessionPath(session.ID), data, 0o644); err != nil {
		return fmt.Errorf("写入会话文件失败: %w", err)
	}
	return nil
}

// ===== ID 生成（与 01agent 保持一致：时间戳_随机hex） =====

func generateSessionID() string {
	t := time.Now()
	random := make([]byte, 4)
	_, _ = rand.Read(random)
	return fmt.Sprintf("%s_%s", t.Format("20060102_150405"), hex.EncodeToString(random))
}

func generateMessageID() string {
	t := time.Now()
	random := make([]byte, 3)
	_, _ = rand.Read(random)
	return fmt.Sprintf("msg-%d-%s", t.UnixMilli(), hex.EncodeToString(random))
}

func truncateTitle(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// CountSessionsByModel 返回引用指定模型名称的会话数量
func (s *SessionStore) CountSessionsByModel(modelName string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("读取会话目录失败: %w", err)
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		session, err := s.loadSession(id)
		if err != nil {
			continue
		}
		if session.Model == modelName {
			count++
		}
	}
	return count, nil
}

// RenameModelReference 将所有会话中引用 oldName 的 model 字段更新为 newName
func (s *SessionStore) RenameModelReference(oldName, newName string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("读取会话目录失败: %w", err)
	}

	updated := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		session, err := s.loadSession(id)
		if err != nil {
			continue
		}
		if session.Model == oldName {
			session.Model = newName
			if err := s.saveSession(session); err != nil {
				return updated, err
			}
			updated++
		}
	}
	return updated, nil
}
