package store

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
	"unicode/utf8"
	"wails-tmp/internal/diff"
	"wails-tmp/internal/media"
	"wails-tmp/internal/permission"
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
	Attachments []media.Attachment `json:"attachments,omitempty"`

	// [新增] 模型生成的思考过程（仅当 role=assistant 且启用 thinking 时）
	// 这是只读字段，不会被修改或重新处理，仅作历史记录供 UI 展示。
	Thinking string `json:"thinking,omitempty"`

	// ThinkingSignature 思考块签名（Anthropic 协议）。
	//
	// 为什么必须存：启用 thinking 且这一轮带工具调用时，下一次请求要把上一轮的
	// thinking 块**连同签名**回传（Anthropic 的硬性要求，缺了直接 400
	// "thinking.thinking: Field required"）。签名是服务端生成的校验值，无法自行构造，
	// 只能从响应里取回并原样保存。非 Anthropic 协议（OpenAI 系）没有签名，留空。
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
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
// 而不是把整个工作区的改动都算进来（详见 diff.DiffService 的注释）。
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
	Diffs          []diff.DiffTurn `json:"diffs,omitempty"` // 每轮对话产生的文件差异
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

	// Todos 主会话的待办清单（todo 工具维护）。落盘而不是只放内存：
	// 清单是"长任务做到哪了"的记录，重启后要能接着看；上下文被压缩后它也是
	// 模型重建进度的依据。子代理会话不用它（父会话的清单才是用户关心的那份）。
	Todos []TodoItem `json:"todos,omitempty"`

	// [新增] Thinking 相关配置
	ThinkingEnabled bool `json:"thinkingEnabled,omitempty"` // 是否启用 extended thinking
	ThinkingBudget  int  `json:"thinkingBudget,omitempty"`  // thinking token 预算（如 5000）
}

// TodoItem 待办清单的一项。
//
// ID 由服务端生成并保证在会话内唯一：模型只需要回传 id 就能改状态，
// 不必（也不该）靠"第几项"这种位置来定位——清单随时可能被增删。
type TodoItem struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"` // pending / in_progress / done
	// CreatedAt / UpdatedAt 便于界面排序与"最近改过什么"，Unix 毫秒。
	CreatedAt int64 `json:"createdAt,omitempty"`
	UpdatedAt int64 `json:"updatedAt,omitempty"`
}

// 待办状态取值。与 plan 的步骤状态（StepPending/...）刻意分开命名：
// 两者语义相近但生命周期完全不同（todo 不参与审批、不落 git）。
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoDone       = "done"
)

// ValidTodoStatus 校验状态取值
func ValidTodoStatus(s string) bool {
	switch s {
	case TodoPending, TodoInProgress, TodoDone:
		return true
	default:
		return false
	}
}

// SessionConfig 创建会话时的配置
type SessionConfig struct {
	Title string `json:"title"`
	// 项目地址
	Project string `json:"project"`
	// 模型配置
	Model string `json:"model"`
	// 权限管理
	PermissionMode string   `json:"permissionMode"`
	ViewMode       string   `json:"viewMode"` // 视图模式：控制工具调用过程的展示粒度
	Environment    string   `json:"environment"`
	EnabledTools   []string `json:"enabledTools"`  // 本会话可用的工具 ID 白名单（空=全部已启用工具）
	EnabledSkills  []string `json:"enabledSkills"` // 本会话可用的技能 ID 白名单（空=全部已启用技能）
	// 会话配置
	ParentID string `json:"parentId,omitempty"` // 非空=子代理会话（不出现在会话列表里）

	// Thinking 相关配置
	ThinkingEnabled bool `json:"thinkingEnabled,omitempty"` // 是否启用 extended thinking
	ThinkingBudget  int  `json:"thinkingBudget,omitempty"`  // thinking token 预算（如 5000）
}

// SessionPatch 会话部分字段更新（指针为 nil 表示不更新）
type SessionPatch struct {
	Title          *string `json:"title,omitempty"`
	Model          *string `json:"model,omitempty"`
	PermissionMode *string `json:"permissionMode,omitempty"`
	ViewMode       *string `json:"viewMode,omitempty"`
	Status         *string `json:"status,omitempty"`
	Project        *string `json:"project,omitempty"`

	// Thinking 相关配置更新
	ThinkingEnabled *bool `json:"thinkingEnabled,omitempty"`
	ThinkingBudget  *int  `json:"thinkingBudget,omitempty"`
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
		// extended thinking 配置：前端在「新建会话」里选好后经 SessionConfig 传进来，
		// 这里必须落到 Session 上。此前漏了这两行，于是 buildThinkingConfig 永远读到
		// 零值、thinking 参数永远不发——配置项在界面上点了却毫无效果（静默失效）。
		ThinkingEnabled: config.ThinkingEnabled,
		ThinkingBudget:  config.ThinkingBudget,
	}
	if session.Title == "" {
		session.Title = "新会话"
	}
	// 权限模式：空值与非法值一律归一化到最严格的 manual（fail closed）
	session.PermissionMode = string(permission.NormalizeMode(session.PermissionMode))
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
	session.PermissionMode = string(permission.NormalizeMode(session.PermissionMode))
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

	// 工具结果在会话文件里被存了两份：assistant.toolCalls[i].result 与紧随其后的
	// tool 消息 content。实测两者逐字节相同，是同一个大文件被 read_file 读出来后
	// 复制了两遍（单个会话因此涨到 4.9 MB，其中 96% 是这种重复）。
	//
	// 这两份的**消费者不同**，所以不能删任一份：
	//   - tool 消息 content → 发给模型（buildLLMMessages），必须保持完整；
	//   - toolCalls[].result → 只给前端工具卡片渲染（ToolProcess.vue），
	//     而它从不展示完整正文，1 MB 的输出塞进 DOM 本身就是卡顿源。
	// 因此这里只截断前者之外的**持久化副本**，且只截 toolCalls 里那份。
	//
	// 注意必须深拷贝：msg 与内存里的 messages/executedToolCalls 共享同一个
	// []ToolCall 底层数组（chat.go 里 assistantMsg.ToolCalls 直接取自
	// executedToolCalls）。若原地改 Result，会把同一份执行记录的 Result 也改掉，
	// 进而污染随后的 tool 消息内容与 trace 上报。
	msg.ToolCalls = truncateToolCallResults(msg.ToolCalls)

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
// NextDiffTurn 下一轮的轮次号。
//
// 这个规则必须只有一处：AppendDiff 用它给 diff.DiffTurn 编号，chat.go 取 checkpoint 时
// 也用它——两处若各写一份 `len(Diffs)+1`，一旦有一边改了就会让 checkpoint 的 ref
// 编号与 diff.DiffTurn.Turn 错位，表现为"某几轮的回退按钮点了没反应"。
func NextDiffTurn(session *Session) int {
	if session == nil {
		return 1
	}
	return len(session.Diffs) + 1
}

func (s *SessionStore) AppendDiff(id string, turn diff.DiffTurn, baseline string, touched []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.loadSession(id)
	if err != nil {
		return 0, err
	}
	turn.Turn = NextDiffTurn(session)
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

// ===== 待办清单（todo 工具）=====

// Todos 读取本会话的待办清单（返回副本，调用方改它不影响存储）。
func (s *SessionStore) Todos(id string) ([]TodoItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, err := s.loadSession(id)
	if err != nil {
		return nil, err
	}
	return append([]TodoItem(nil), session.Todos...), nil
}

// AddTodo 追加一项待办，返回新项。
//
// 加锁跨"读—改—写"整段：待办是模型在同一轮里可能连续调用多次的工具，
// 只锁单次读写会让两次 add 互相覆盖（后写的那次把前一次的项丢掉）。
func (s *SessionStore) AddTodo(id, text string) (TodoItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return TodoItem{}, fmt.Errorf("待办内容不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.loadSession(id)
	if err != nil {
		return TodoItem{}, err
	}
	now := time.Now().UnixMilli()
	item := TodoItem{
		ID:        generateTodoID(),
		Text:      text,
		Status:    TodoPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	session.Todos = append(session.Todos, item)
	if err := s.saveSession(session); err != nil {
		return TodoItem{}, err
	}
	return item, nil
}

// UpdateTodo 改一项待办的文本与/或状态（text / status 为空表示不改该字段）。
func (s *SessionStore) UpdateTodo(id, todoID, text, status string) (TodoItem, error) {
	status = strings.TrimSpace(status)
	if status != "" && !ValidTodoStatus(status) {
		return TodoItem{}, fmt.Errorf("未知的待办状态: %s（可选 %s / %s / %s）", status, TodoPending, TodoInProgress, TodoDone)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.loadSession(id)
	if err != nil {
		return TodoItem{}, err
	}
	idx := indexOfTodo(session.Todos, todoID)
	if idx < 0 {
		return TodoItem{}, fmt.Errorf("未找到待办: %s", todoID)
	}
	item := &session.Todos[idx]
	if t := strings.TrimSpace(text); t != "" {
		item.Text = t
	}
	if status != "" {
		item.Status = status
	}
	item.UpdatedAt = time.Now().UnixMilli()
	if err := s.saveSession(session); err != nil {
		return TodoItem{}, err
	}
	return *item, nil
}

// RemoveTodo 删除一项待办，返回被删掉的项。
func (s *SessionStore) RemoveTodo(id, todoID string) (TodoItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.loadSession(id)
	if err != nil {
		return TodoItem{}, err
	}
	idx := indexOfTodo(session.Todos, todoID)
	if idx < 0 {
		return TodoItem{}, fmt.Errorf("未找到待办: %s", todoID)
	}
	removed := session.Todos[idx]
	session.Todos = append(session.Todos[:idx], session.Todos[idx+1:]...)
	if err := s.saveSession(session); err != nil {
		return TodoItem{}, err
	}
	return removed, nil
}

// ClearTodos 清空清单，返回被清掉的条数。
func (s *SessionStore) ClearTodos(id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.loadSession(id)
	if err != nil {
		return 0, err
	}
	n := len(session.Todos)
	session.Todos = nil
	if err := s.saveSession(session); err != nil {
		return 0, err
	}
	return n, nil
}

func indexOfTodo(items []TodoItem, todoID string) int {
	for i := range items {
		if items[i].ID == todoID {
			return i
		}
	}
	return -1
}

// generateTodoID 生成待办 id。短前缀便于模型在正文里引用，也便于人工辨认。
func generateTodoID() string {
	random := make([]byte, 3)
	_, _ = rand.Read(random)
	return "todo-" + hex.EncodeToString(random)
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
// 保留 diff.DiffTurn 记录本身而不是删掉：用户可能回退后重新执行，历史应当留痕。
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
		session.PermissionMode = string(permission.NormalizeMode(*patch.PermissionMode))
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
	// thinking 配置：指针字段，nil 表示"本次不改"（与 bool/int 零值区分开）。
	// 关掉 thinking 时前端会把 budget 置空/0，所以两者要各自独立判断，不能合并成一个条件。
	if patch.ThinkingEnabled != nil {
		session.ThinkingEnabled = *patch.ThinkingEnabled
	}
	if patch.ThinkingBudget != nil {
		session.ThinkingBudget = *patch.ThinkingBudget
	}
	session.EndAt = time.Now().UnixMilli()

	if err := s.saveSession(session); err != nil {
		return nil, err
	}
	return session, nil
}

// ===== 工具结果持久化截断 =====
//
// 背景：read_file 能返回任意大小的文件内容，而这份内容在会话里出现两次
// （toolCalls[].result 与 tool 消息 content）。实测一个会话因此到 4.9 MB，
// 其中前 4 条消息（两次 read_file）占了 4.7 MB。
//
// 这里只管 toolCalls[].result —— 前端工具卡片展示用。真正的原文仍完整保留在
// tool 消息的 content 里（模型靠它工作），所以截断不损失任何模型可见信息。
const (
	// MaxPersistedToolResult 单条工具结果在 toolCalls[].result 里保留的字节上限。
	// 16 KB 足够前端展示卡片与展开查看常规输出；更大的输出（整份源码、长日志）
	// 用户本来也不会在工具卡片里读完。
	MaxPersistedToolResult = 16 * 1024
)

// truncateToolCallResults 返回 ToolCalls 的深拷贝，其中过长的 Result 被截断。
//
// 必须深拷贝：入参的切片常与内存中的执行记录共享底层数组，原地修改会连带
// 改掉 tool 消息内容与 trace 上报所用的 Result。
//
// 完全在限额内时返回原切片（零分配），只读调用方可安全共享。
func truncateToolCallResults(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return calls
	}
	needCopy := false
	for i := range calls {
		if len(calls[i].Result) > MaxPersistedToolResult {
			needCopy = true
			break
		}
	}
	if !needCopy {
		return calls
	}
	out := make([]ToolCall, len(calls))
	copy(out, calls)
	for i := range out {
		if len(out[i].Result) > MaxPersistedToolResult {
			out[i].Result = truncateToolResult(out[i].Result)
		}
	}
	return out
}

// truncateToolResult 截断到 MaxPersistedToolResult，并附一行说明。
//
// 按 rune 边界回退，避免把一个多字节字符切成两半——切坏后前端会显示乱码方块，
// 且整份 JSON 里该字符串不再是合法 UTF-8。
//
// 回退判据用 DecodeLastRuneInString：对"尾部是不完整/非法序列"它返回
// (RuneError, 1)，据此逐字节回退直到尾部是一个完整字符。
// （不能只判断末字节是否为 RuneStart——那样会把以起始字节开头、但缺后续字节的
// 半个字符留在末尾，正是本函数最初写错的地方，已由 UTF8Boundary 测试覆盖。）
func truncateToolResult(s string) string {
	if len(s) <= MaxPersistedToolResult {
		return s
	}
	head := s[:MaxPersistedToolResult]
	for len(head) > 0 {
		r, size := utf8.DecodeLastRuneInString(head)
		// RuneError 且长度 1 = 非法/不完整的尾部，回退一字节再看。
		// 注意真正的 U+FFFD 编码为 3 字节，size > 1，属于合法内容，不能误退。
		if r != utf8.RuneError || size > 1 {
			break
		}
		head = head[:len(head)-1]
	}
	return head + fmt.Sprintf("\n\n[已截断：原文 %d 字节，此处仅保留前 %d 字节。完整内容见该工具调用的执行结果]",
		len(s), len(head))
}

// saveSession 调用方需持有锁。
//
// 写入采用「临时文件 + rename」的原子替换：
//
//	会话文件是这份数据的唯一副本，而它已经能到 MB 级（实测单个会话 4.9 MB），
//	原先的 os.WriteFile 会在写入过程中把原文件截断——此时崩溃/断电/磁盘满，
//	留下的是半个 JSON，会话直接打不开。rename 在同一分区上是原子的：
//	要么还是旧文件，要么已经是完整新文件，不存在中间态。
//
// 不缩进（json.Marshal 而非 MarshalIndent）：这份文件只给程序读，缩进纯属
//
//	浪费空间与序列化时间——实测去掉缩进可省约 15% 体积。
func (s *SessionStore) saveSession(session *Session) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("创建会话目录失败: %w", err)
	}
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("序列化会话失败: %w", err)
	}
	target := s.sessionPath(session.ID)
	// 临时文件放在同目录，保证 rename 不跨文件系统（跨分区 rename 会退化成拷贝，不原子）。
	// 加随机后缀：同名后缀在极端情况下（两次并发写同一会话）会互相覆盖。
	tmp, err := os.CreateTemp(s.dir, session.ID+".*.tmp")
	if err != nil {
		return fmt.Errorf("创建会话临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	// 任何一步失败都要清掉临时文件，否则崩溃残留会堆积在会话目录里。
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入会话临时文件失败: %w", err)
	}
	// Sync 之后再 rename：否则断电可能留下「rename 成功但内容还在页缓存里」的空文件。
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("同步会话临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭会话临时文件失败: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("替换会话文件失败: %w", err)
	}
	tmpPath = "" // rename 成功，临时文件已不存在，不必再删
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
func titleSource(content string, atts []media.Attachment) string {
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
