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
	// Files 本次调用改动的文件（相对工作区路径）。仅在文件工具产生写入时有值，
	// 让差异可以归因到具体是哪次工具调用改的。
	Files []string `json:"files,omitempty"`
	// Images 本次调用产出的图片附件 ID（目前只有 read_image 会产出）。
	//
	// 只存 ID，不存完整 Attachment：字节与元数据的**唯一真源**是那条 tool 结果消息的
	// Attachments（组装请求时读的是它）。这里放 ID 只是为了让工具卡片能画出缩略图——
	// 工具结果消息在前端是被过滤掉不展示的（工具输出都归到卡片里）。
	Images []string `json:"images,omitempty"`
}

// Message 会话中的一条聊天消息
type Message struct {
	ID         string     `json:"id"`
	Role       string     `json:"role"` // user / assistant / system / tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"` // role=tool 时对应的 tool_call_id
	CreatedAt  int64      `json:"createdAt"`            // Unix 毫秒
	// Attachments 本消息携带的附件（目前只有图片）。
	//
	// 用户贴的图挂在 user 消息上，工具读到的图挂在对应的 tool 结果消息上——
	// 两种来源共用同一个字段，于是"怎么存"（AttachmentStore）与"怎么发给模型"
	// （buildLLMMessages）各自只有一条实现，不需要为来源分叉。
	//
	// 这里存的是**引用**（ID/尺寸/媒体类型/相对路径），字节躺在附件目录里。
	// 会话文件因此保持轻量——它是每次追加消息都要整体重写的那份数据。
	Attachments []Attachment `json:"attachments,omitempty"`
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

// Session 一个完整会话，每个会话持久化为一个 JSON 文件。
//
// DiffBaseline / DiffTouched 是会话级 diff 状态：基线 commit 与本会话触碰过的文件
// （仓库相对路径）。落盘是为了重启后仍能算出「本会话改了哪些文件」，
// 而不是把整个工作区的改动都算进来（详见 DiffService 的注释）。
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
	Diffs          []DiffTurn      `json:"diffs,omitempty"` // 每轮对话产生的文件差异
	DiffBaseline   string          `json:"diffBaseline,omitempty"`
	DiffTouched    []string        `json:"diffTouched,omitempty"`
	EnabledTools   []string        `json:"enabledTools,omitempty"`  // 本会话可用的工具 ID 白名单（空=全部已启用工具）
	EnabledSkills  []string        `json:"enabledSkills,omitempty"` // 本会话可用的技能 ID 白名单（空=全部已启用技能）

	// 上下文压缩状态（P1-B）。摘要只在**构建请求时**生效，会话里的 Messages 一字不动——
	// 因此压缩是可逆的：清空这三个字段就回到全量上下文。
	ContextSummary     string `json:"contextSummary,omitempty"`     // 被摘要覆盖那部分的摘要正文
	ContextCoveredUpTo int    `json:"contextCoveredUpTo,omitempty"` // 摘要覆盖到第几条消息（不含）
	ContextSummaryAt   int64  `json:"contextSummaryAt,omitempty"`   // 摘要生成时间（Unix 毫秒）

	// 长期记忆闭环（跨会话知识）。召回块只在构建请求时注入，不写进 Messages。
	WorkingMemory        string   `json:"workingMemory,omitempty"`
	WorkingMemoryUpTo    int      `json:"workingMemoryUpTo,omitempty"`
	SurfacedMemoryIDs    []string `json:"surfacedMemoryIDs,omitempty"`
	IgnoreMemory         bool     `json:"ignoreMemory,omitempty"`
	LastExtractMessageID string   `json:"lastExtractMessageID,omitempty"`

	// ParentID 非空表示这是子代理的会话（由某个主会话派生）。
	// 它**不出现在会话列表里**——子代理是主会话的工作产物，不是用户的会话。
	ParentID string `json:"parentId,omitempty"`

	// Subagent 子代理会话专有：派给它的任务、跑完的结论与状态。
	// 落盘而不是只放内存：重启后面板仍要能列出跑过的子代理，而"回退它改过的文件"
	// 也必须靠这个会话（diff 轮次与 checkpoint ref 都按会话 ID 键控）。
	// 主会话的会话里它恒为 nil。
	Subagent *SubagentInfo `json:"subagent,omitempty"`

	// UsageTotals 会话累计 token 用量（跨轮累加，只增不减）。
	//
	// 用**指针**而不是值：统计上线前的会话文件里没有这个字段，nil 表示"这个会话没有统计数据"，
	// 界面据此显示空态而不是一排 0——值类型无法区分"没数据"与"数据恰好为 0"。
	UsageTotals *UsageTotals `json:"usageTotals,omitempty"`
}

// SessionConfig 创建会话时的配置
type SessionConfig struct {
	Title          string   `json:"title"`
	Project        string   `json:"project"`
	Model          string   `json:"model"`
	PermissionMode string   `json:"permissionMode"`
	ViewMode       string   `json:"viewMode"` // 视图模式：控制工具调用过程的展示粒度
	Environment    string   `json:"environment"`
	EnabledTools   []string `json:"enabledTools"`       // 本会话可用的工具 ID 白名单（空=全部已启用工具）
	EnabledSkills  []string `json:"enabledSkills"`      // 本会话可用的技能 ID 白名单（空=全部已启用技能）
	ParentID       string   `json:"parentId,omitempty"` // 非空=子代理会话（不出现在会话列表里）
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
		ParentID:       config.ParentID,
	}
	if session.Title == "" {
		session.Title = "新会话"
	}
	// 权限模式：空值与非法值一律归一化到最严格的 manual（fail closed）
	session.PermissionMode = string(NormalizeMode(session.PermissionMode))
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
	// 权限模式同样兜底：旧数据或手改过的文件可能是空值/脏值
	session.PermissionMode = string(NormalizeMode(session.PermissionMode))
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
		// 子代理的会话不进列表：它们是主会话的工作产物，不是用户的会话。
		// 直接引用它们会让会话列表被子代理任务塞满（一次对话可能派生好几个）。
		if session.ParentID != "" {
			continue
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

// ListSubagentSessions 列出某个主会话派生出的全部子代理会话（不含消息与 diff，减小数据量）。
//
// 单独一个方法而不是复用 ListSessions：后者**刻意**把子代理过滤掉了（它们不该出现在
// 用户的会话列表里），而面板恰恰要列出它们。两边的过滤条件相反，不能共用一处实现。
func (s *SessionStore) ListSubagentSessions(parentID string) ([]*Session, error) {
	if parentID == "" {
		return []*Session{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Session{}, nil
		}
		return nil, fmt.Errorf("读取会话目录失败: %w", err)
	}

	out := make([]*Session, 0, 4)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		session, err := s.loadSession(id)
		if err != nil {
			continue // 跳过损坏文件
		}
		if session.ParentID != parentID {
			continue
		}
		session.Messages = []Message{}
		session.Conversations = []*Conversation{}
		session.Diffs = nil
		out = append(out, session)
	}
	return out, nil
}

// SetSubagentInfo 写入/更新子代理会话的概况（任务、状态、结论）。
// 只对子代理会话有意义；主会话调用会被拒绝，避免把两份数据搞混。
func (s *SessionStore) SetSubagentInfo(id string, info *SubagentInfo) error {
	if info == nil {
		return fmt.Errorf("子代理概况不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.loadSession(id)
	if err != nil {
		return err
	}
	if session.ParentID == "" {
		return fmt.Errorf("会话 %s 不是子代理会话", id)
	}
	session.Subagent = info
	return s.saveSession(session)
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
		session.Title = truncateTitle(titleSource(msg.Content, msg.Attachments), 30)
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

// AppendDiff 追加一轮差异并落盘，轮次号自动递增，返回本轮轮次号。
// 同时把会话级 diff 状态（基线 + 触碰过的路径）一并落盘——与 diff 同一次写入，
// 避免为了同步状态再写一遍整个会话文件。
// nextDiffTurn 下一轮的轮次号。
//
// 这个规则必须只有一处：AppendDiff 用它给 DiffTurn 编号，chat.go 取 checkpoint 时
// 也用它——两处若各写一份 `len(Diffs)+1`，一旦有一边改了就会让 checkpoint 的 ref
// 编号与 DiffTurn.Turn 错位，表现为"某几轮的回退按钮点了没反应"。
func nextDiffTurn(session *Session) int {
	if session == nil {
		return 1
	}
	return len(session.Diffs) + 1
}

func (s *SessionStore) AppendDiff(id string, turn DiffTurn, baseline string, touched []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.loadSession(id)
	if err != nil {
		return 0, err
	}
	turn.Turn = nextDiffTurn(session)
	if turn.Label == "" {
		turn.Label = fmt.Sprintf("第 %d 轮", turn.Turn)
	}
	session.Diffs = append(session.Diffs, turn)
	if baseline != "" {
		session.DiffBaseline = baseline
	}
	if len(touched) > 0 {
		session.DiffTouched = touched
	}
	if err := s.saveSession(session); err != nil {
		return 0, err
	}
	return turn.Turn, nil
}

// SetContextSummary 写入上下文摘要（P1-B）。
// coveredUpTo 之前（不含）的消息在构建请求时会被摘要替换；会话里的原文完整保留。
func (s *SessionStore) SetContextSummary(id, summary string, coveredUpTo int, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.loadSession(id)
	if err != nil {
		return err
	}
	session.ContextSummary = summary
	session.ContextCoveredUpTo = coveredUpTo
	session.ContextSummaryAt = at
	// 压缩成功后旧召回已不在请求里，允许再召回（对标 Claude Code 扫附件：压缩后 naturally reset）。
	if strings.TrimSpace(summary) != "" {
		session.SurfacedMemoryIDs = nil
	}
	return s.saveSession(session)
}

// SetIgnoreMemory 本会话是否跳过记忆注入与提取。
func (s *SessionStore) SetIgnoreMemory(id string, ignore bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.loadSession(id)
	if err != nil {
		return err
	}
	session.IgnoreMemory = ignore
	return s.saveSession(session)
}

// AddSurfacedMemoryIDs 记下本会话已注入/读过正文的记忆，避免下一圈再灌一遍。
func (s *SessionStore) AddSurfacedMemoryIDs(id string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.loadSession(id)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(session.SurfacedMemoryIDs)+len(ids))
	for _, existing := range session.SurfacedMemoryIDs {
		seen[existing] = true
	}
	for _, add := range ids {
		add = strings.TrimSpace(add)
		if add == "" || seen[add] {
			continue
		}
		session.SurfacedMemoryIDs = append(session.SurfacedMemoryIDs, add)
		seen[add] = true
	}
	return s.saveSession(session)
}

// AddUsage 累加一轮 token 用量并落盘，返回累加后的总计。
//
// withCache 表示本轮请求**是否真的带了缓存断点**（即缓存功能已启用）。
// 它只影响"连续零命中"计数的推进，见 UsageTotals.Add 的说明。
//
// 与 AddSurfacedMemoryIDs 同为"读—改—写整个会话文件"的模式：会话是单一真源，
// 把用量拆到单独的文件会制造第二个真源（删除会话时容易漏删，且两处可能对不上）。
func (s *SessionStore) AddUsage(id string, u TokenUsage, now int64, withCache bool) (*UsageTotals, error) {
	if s == nil || id == "" {
		return nil, fmt.Errorf("会话 ID 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.loadSession(id)
	if err != nil {
		return nil, err
	}
	if session.UsageTotals == nil {
		session.UsageTotals = &UsageTotals{}
	}
	session.UsageTotals.Add(u, now, withCache)
	if err := s.saveSession(session); err != nil {
		return nil, err
	}
	out := *session.UsageTotals
	return &out, nil
}

// ClearSurfacedMemoryIDs 清空已展示集合（测试与手动重置用）。
func (s *SessionStore) ClearSurfacedMemoryIDs(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.loadSession(id)
	if err != nil {
		return err
	}
	session.SurfacedMemoryIDs = nil
	return s.saveSession(session)
}

// ClearContextSummary 清空摘要，让会话回到全量上下文。
// 这是"压缩可逆"的落地点：出了任何问题，调它就能回到压缩前的状态。
func (s *SessionStore) ClearContextSummary(id string) error {
	return s.SetContextSummary(id, "", 0, 0)
}

// MarkDiffUndone 标记某轮已被回退。
// 保留 DiffTurn 记录本身而不是删掉：用户可能回退后重新执行，历史应当留痕。
func (s *SessionStore) MarkDiffUndone(id string, turn int, undone bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.loadSession(id)
	if err != nil {
		return err
	}
	for i := range session.Diffs {
		if session.Diffs[i].Turn == turn {
			session.Diffs[i].Undone = undone
			return s.saveSession(session)
		}
	}
	return fmt.Errorf("轮次不存在: %d", turn)
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
		// 归一化而非报错：前端下拉与旧数据都可能带上不可识别的值，回退到最严格模式
		session.PermissionMode = string(NormalizeMode(*patch.PermissionMode))
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

// titleSource 会话标题的来源文本：正文优先，没有正文时用第一张附件的名字。
//
// 纯图片消息（"这张报错图怎么回事"是常见开场，用户可能一个字都不打）若直接拿空正文
// 当标题，会话列表里就会出现一行空白，用户根本认不出是哪个会话。
func titleSource(content string, atts []Attachment) string {
	if strings.TrimSpace(content) != "" {
		return content
	}
	for _, a := range atts {
		if strings.TrimSpace(a.Name) != "" {
			return a.Name
		}
	}
	if len(atts) > 0 {
		return "（图片）"
	}
	return content
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

// ListSessionsByModel 返回引用指定模型名称的**用户可见**会话（按最近活跃倒序，不带消息）。
//
// 与 CountSessionsByModel 只差一个过滤：子代理会话（ParentID != ""）不算。
// 它们刻意不进会话列表，前端也没有任何入口能看到、删除它们，所以它们不能成为
// 删除模型的阻挡者——否则用户会收到「有 N 个会话正在使用该模型」，却在列表里
// 怎么也找不到这 N 个会话，模型等于被永久锁死。
//
// 返回会话本身而不是数量：报错时才能点名「是哪几个会话在用」，用户可直接去处理。
func (s *SessionStore) ListSessionsByModel(modelName string) ([]*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取会话目录失败: %w", err)
	}

	refs := make([]*Session, 0, 4)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		session, err := s.loadSession(id)
		if err != nil {
			continue
		}
		if session.ParentID != "" || session.Model != modelName {
			continue
		}
		session.Messages = []Message{}
		session.Conversations = []*Conversation{}
		session.Diffs = nil
		refs = append(refs, session)
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].EndAt > refs[j].EndAt
	})
	return refs, nil
}

// ClearSubagentModelReference 把**子代理会话**中引用 modelName 的 model 字段清空，
// 返回清理条数。只在模型删除成功后调用。
//
// 子代理会话是历史产物（跑完就不会再被调度），其 model 字段只是一条展示用的名字。
// 不清掉的话，删掉模型后这些文件里会残留一个已不存在的模型名，日后排障时容易被
// 误读成「模型还在被使用」。用户可见的会话**一律不动**：它们要么已被 DeleteModel
// 拦下（说明确有引用，得由用户自己切换），要么本就没有引用。
func (s *SessionStore) ClearSubagentModelReference(modelName string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("读取会话目录失败: %w", err)
	}

	cleared := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		session, err := s.loadSession(id)
		if err != nil {
			continue
		}
		if session.ParentID == "" || session.Model != modelName {
			continue
		}
		session.Model = ""
		if err := s.saveSession(session); err != nil {
			return cleared, err
		}
		cleared++
	}
	return cleared, nil
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
