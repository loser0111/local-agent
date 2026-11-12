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
)

// ===== 数据契约（docs/plan-execute-implementation-plan.md 5.2）=====

// PlanStepStatus 步骤状态
const (
	StepPending = "pending" // 待执行
	StepRunning = "running" // 执行中
	StepDone    = "done"    // 完成
	StepFailed  = "failed"  // 失败
	StepSkipped = "skipped" // 因前序失败/取消跳过
)

// StepInterrupted 步骤执行被外部原因打断（进程退出、运行登记丢失）。
//
// 与 StepFailed 刻意分开：失败是"这一步跑过并且错了"，中断是"这一步没跑完就没了"。
// 两者的恢复动作也不同——失败过后重跑要防重复副作用，中断则通常还没产生副作用。
//
// 单独一个 const 块而不并入上面那块：并进去会让 gofmt 重新对齐整块，
// 制造与本次改动无关的 diff。
const StepInterrupted = "interrupted"

// PlanStatus 计划状态
const (
	PlanAwaitingApproval = "awaiting_approval" // 待审核（可编辑）
	PlanRunning          = "running"           // 执行中
	PlanCompleted        = "completed"
	PlanFailed           = "failed"
	PlanCancelled        = "cancelled"
)

// 韧性相关的计划状态（能力规范见 openspec/specs/plan-execution-recovery）。
//
// 与上面那组分开放：上面那组是一次正常执行的生命周期，这三个解决的是
// "中断之后还能接着走"——混在一起会让人误以为 planning 是执行阶段的细分。
const (
	// PlanPlanning 已收到计划请求，正在调用规划器。
	//
	// 这个中间态必须落盘：规划失败时，它就是"用户的请求还没被处理完"的唯一凭据。
	// 在此之前规划失败是彻底的静默——用户消息已入库，却没有任何记录说明
	// 这条消息本该产出一份计划，于是用户只能把需求重打一遍。
	PlanPlanning = "planning"

	// PlanPlanFailed 规划失败（网络错误、规划器输出不可解析等）。
	//
	// 与 PlanFailed（执行失败）刻意分开：恢复动作完全不同——
	// plan_failed 是"拿着原始请求重新规划"，failed 是"接着执行剩下的步骤"。
	PlanPlanFailed = "plan_failed"

	// PlanInterrupted 执行被外部原因打断（应用退出、运行登记丢失）。
	//
	// 与 PlanFailed 分开：它不包含"这一步做错了"的判断，恢复动作是从断点续跑。
	// 应用重启时由 PlanStore.loadAll 统一置入，也会在读取计划时惰性对账置入。
	PlanInterrupted = "interrupted"
)

// PlanStep 计划步骤
type PlanStep struct {
	Index      int    `json:"index"`
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	Status     string `json:"status"`
	Summary    string `json:"summary,omitempty"` // 执行结果摘要（done 后）
	Error      string `json:"error,omitempty"`   // 失败原因
	StartedAt  int64  `json:"startedAt,omitempty"`
	FinishedAt int64  `json:"finishedAt,omitempty"`

	// Attempt 本步骤已开始的尝试次数（1 表示第一次执行）。
	//
	// 续跑与步骤内自动重试都靠它区分"这是新的一次尝试"；构造模型请求时也用它
	// 只保留最后一次尝试的 assistant/tool 消息（否则上下文里会堆满失败过程）。
	Attempt int `json:"attempt,omitempty"`

	// ContextMark 本步骤开始时会话里的消息条数。
	//
	// 它是"这一步从第几条消息开始"的锚点。不用时间戳：同一毫秒内可能写入多条消息，
	// 用它切片会切偏。
	ContextMark int `json:"contextMark,omitempty"`
}

// Plan 计划
type Plan struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionId"`
	Title     string      `json:"title"`
	Status    string      `json:"status"`
	Steps     []*PlanStep `json:"steps"`
	CreatedAt int64       `json:"createdAt"`
	UpdatedAt int64       `json:"updatedAt"`

	// Query 触发生成这份计划的原始用户请求。
	//
	// 落盘是为了恢复：规划失败时用户消息已经进了会话，但"这条请求是要做计划的"
	// 只存在于那次调用的参数里。有了它，「重试规划」才能复用原请求，
	// 不必让用户重打一遍，也不会在会话里留下第二条同样的用户消息。
	Query string `json:"query,omitempty"`

	// Error 计划级失败原因（planning / plan_failed 时为规划错误）。
	Error string `json:"error,omitempty"`

	// Revision 每次落盘自增的版本号。
	//
	// 唯一用途是让前端丢弃乱序到达的旧事件：事件是异步推送的，而"计划现在是什么状态"
	// 的权威来源是磁盘上的这份文件，版本号是两者之间的序。
	Revision int `json:"revision,omitempty"`
}

// GeneratePlanID 生成计划 ID：plan_<毫秒时间戳>_<随机4位hex>
func GeneratePlanID() string {
	random := make([]byte, 2)
	_, _ = rand.Read(random)
	return fmt.Sprintf("plan_%d_%s", time.Now().UnixMilli(), hex.EncodeToString(random))
}

// MarkRemainingSkipped 将 fromIndex 起所有 pending 步骤置为 skipped
func MarkRemainingSkipped(plan *Plan, fromIndex int) {
	for _, st := range plan.Steps {
		if st.Index >= fromIndex && st.Status == StepPending {
			st.Status = StepSkipped
		}
	}
}

// ===== PlanStore =====

// PlanStore 计划持久化与查询（~/.local-agent/plans/，每个计划一个 JSON 文件）
type PlanStore struct {
	mu    sync.RWMutex
	dir   string
	plans map[string]*Plan
}

func NewPlanStore(dir string) *PlanStore {
	s := &PlanStore{dir: dir, plans: map[string]*Plan{}}
	_ = os.MkdirAll(dir, 0o755)
	s.loadAll()
	return s
}

// loadAll 启动时加载全部计划；running 状态的计划置为中断（可从断点续跑）
func (s *PlanStore) loadAll() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var p Plan
		if json.Unmarshal(data, &p) != nil {
			continue // 损坏文件跳过，不影响其他计划
		}
		if p.Status == PlanPlanning {
			// 规划中途进程退出：这条记录会永远停在 planning——没有任何东西会把它推下去，
			// 界面上就是一个"永远规划中"的幽灵。转成 plan_failed 才既可解释又可重试
			// （Plan.Query 在落盘时就已保存）。
			p.Status = PlanPlanFailed
			p.Error = "规划中断（应用退出），可重试"
			_ = s.saveLocked(&p)
		}
		if p.Status == PlanRunning {
			// 进程退出/崩溃留下的 running 不代表失败，只代表"没人接着跑"。
			// 置为 interrupted（可从断点续跑）而不是 failed（要改计划再批准）。
			//
			// 必须立刻落盘：否则第二次重启会把这份文件重新读成 running，
			// 而它其实是上一次重启就该收尾的残留。
			p.Status = PlanInterrupted
			for _, st := range p.Steps {
				if st.Status == StepRunning {
					st.Status = StepInterrupted
					st.Error = "应用重启，执行中断"
					if st.FinishedAt == 0 {
						st.FinishedAt = time.Now().UnixMilli()
					}
				}
			}
			_ = s.saveLocked(&p)
		}
		s.plans[p.ID] = &p
	}
}

func (s *PlanStore) saveLocked(p *Plan) error {
	p.UpdatedAt = time.Now().UnixMilli()
	// 自增必须在写盘之前：版本号要与这次落盘的内容同属一份，
	// 否则前端可能收到"版本变新、内容还是旧的"的事件。
	p.Revision++
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, p.ID+".json"), data, 0o644)
}

// Save 新建或更新计划（内存与磁盘同步）
func (s *PlanStore) Save(p *Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.saveLocked(p); err != nil {
		return err
	}
	s.plans[p.ID] = p
	return nil
}

// Delete 删除一条计划（文件与内存条目一起删）。
//
// 目前只有一个用途：规划器判定"无需计划"时丢弃 planning 占位记录。
// 刻意不提供"删除失败计划"的能力——失败计划是可续跑、可重试的资产，不是垃圾。
func (s *PlanStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[id]; !ok {
		return fmt.Errorf("计划不存在: %s", id)
	}
	if err := os.Remove(filepath.Join(s.dir, id+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(s.plans, id)
	return nil
}

// Get 按 ID 取计划（执行器持引用串行修改，不做拷贝）
func (s *PlanStore) Get(id string) (*Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plans[id]
	if !ok {
		return nil, fmt.Errorf("计划不存在: %s", id)
	}
	return p, nil
}

// GetBySession 取会话当前（最新创建的）计划
func (s *PlanStore) GetBySession(sessionID string) *Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest *Plan
	for _, p := range s.plans {
		if p.SessionID == sessionID && (latest == nil || p.CreatedAt > latest.CreatedAt) {
			latest = p
		}
	}
	return latest
}

// ListBySession 返回会话全部计划（按创建时间倒序，历史列表用）
func (s *PlanStore) ListBySession(sessionID string) []*Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Plan, 0)
	for _, p := range s.plans {
		if p.SessionID == sessionID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// Reopen 把已结束的计划退回待审核，供「修改后重试」使用。
//
// 允许对 failed / cancelled / completed 调用；failed / skipped / running 的步骤重置为
// pending 并清掉失败原因，**已完成的步骤原样保留** —— 所以「接着上次的进度继续」是默认行为，
// 不会让已经做完的步骤重跑。running 状态拒绝（此时有活跃执行者，应先取消）。
func (s *PlanStore) Reopen(id string) (*Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.plans[id]
	if !ok {
		return nil, fmt.Errorf("计划不存在: %s", id)
	}
	switch p.Status {
	case PlanFailed, PlanCancelled, PlanCompleted, PlanInterrupted:
		// 可重开（interrupted 同样允许：用户可能想改完步骤再跑，而不是原地续跑）
	case PlanAwaitingApproval:
		return p, nil // 已经是可编辑状态，幂等
	case PlanRunning:
		return nil, fmt.Errorf("计划正在执行中，请先取消再修改")
	default:
		return nil, fmt.Errorf("计划状态不可修改: %s", p.Status)
	}

	for _, st := range p.Steps {
		if st.Status == StepDone {
			continue // 保留进度
		}
		st.Status = StepPending
		st.Error = ""
		st.StartedAt = 0
		st.FinishedAt = 0
	}
	p.Status = PlanAwaitingApproval
	if err := s.saveLocked(p); err != nil {
		return nil, err
	}
	return p, nil
}

// ===== 规划器 =====

const PlannerSystemPrompt = `你是任务规划器。把用户的请求拆解为可逐步执行的计划。

要求：
1. 只输出一个 JSON 对象，不要输出任何其他文字或代码块标记。
2. JSON 格式：{"needPlan": bool, "title": string, "steps": [{"title": string, "detail": string}]}
3. needPlan 判断：简单的问答、闲聊、单步操作（如"查看当前目录"）返回 needPlan=false，并把 steps 置为空数组；需要多步操作、多文件修改、有先后依赖的任务返回 true。
4. 每个步骤必须是一个可独立验证完成的小任务；detail 补充该步的要点或注意事项，可为空字符串。
5. 步骤数量 2-8 个；不要把"汇总/汇报"设为步骤。`

// planDraftStep 规划器输出的单个步骤
type planDraftStep struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// PlanDraft 规划器原始输出
type PlanDraft struct {
	NeedPlan bool            `json:"needPlan"`
	Title    string          `json:"title"`
	Steps    []planDraftStep `json:"steps"`
}

// ParsePlanDraft 解析规划器输出；容忍 ```json 包裹与前后杂讯
func ParsePlanDraft(content string) (*PlanDraft, error) {
	text := strings.TrimSpace(content)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	// 截取第一个 { 到最后一个 } 之间的内容
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("规划输出中未找到 JSON 对象")
	}
	var d PlanDraft
	if err := json.Unmarshal([]byte(text[start:end+1]), &d); err != nil {
		return nil, fmt.Errorf("解析规划 JSON 失败: %w", err)
	}
	if d.NeedPlan && len(d.Steps) == 0 {
		return nil, fmt.Errorf("规划器返回 needPlan=true 但步骤为空")
	}
	return &d, nil
}

// BuildPlannerUserPrompt 规划器用户消息：原始请求 + 工作区路径
func BuildPlannerUserPrompt(query, dir string) string {
	var sb strings.Builder
	sb.WriteString("用户请求：" + query)
	if dir != "" {
		sb.WriteString("\n\n当前工作区目录：" + dir)
	}
	return sb.String()
}

// ===== 步骤执行上下文 =====

// BuildPlanStepQuery 合成步骤的用户消息（由执行器持久化进会话历史）
func BuildPlanStepQuery(plan *Plan, step *PlanStep) string {
	query := fmt.Sprintf("【计划步骤 %d/%d】%s", step.Index+1, len(plan.Steps), step.Title)
	if step.Detail != "" {
		query += "\n" + step.Detail
	}
	return query
}

// BuildPlanSystemPrompt 在既有 system prompt（人设+工作区+技能）之上追加计划上下文
func BuildPlanSystemPrompt(base string, plan *Plan, step *PlanStep) string {
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\n## 当前计划\n你正在按用户批准的计划分步执行，一次只做一步。\n")
	sb.WriteString("计划：" + plan.Title + "\n")
	for _, st := range plan.Steps {
		mark := "待办"
		switch st.Status {
		case StepDone:
			mark = "已完成"
		case StepRunning:
			mark = "当前步骤"
		case StepFailed, StepSkipped:
			mark = st.Status
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", st.Index+1, mark, st.Title))
		if st.Status == StepDone && st.Summary != "" {
			sb.WriteString("   结果摘要：" + st.Summary + "\n")
		}
	}
	sb.WriteString(fmt.Sprintf("\n本步任务：%s\n", step.Title))
	if step.Detail != "" {
		sb.WriteString("任务要点：" + step.Detail + "\n")
	}
	sb.WriteString("完成后用一两句话总结本步结果（将作为该步骤的交付摘要），不要执行其他步骤。")
	return sb.String()
}

// ExtractStepSummary 取回复首段（≤200字）作为步骤摘要
func ExtractStepSummary(reply string) string {
	reply = strings.TrimSpace(reply)
	for _, sep := range []string{"\n\n", "\n"} {
		if idx := strings.Index(reply, sep); idx > 0 {
			reply = reply[:idx]
			break
		}
	}
	return TruncateRunes(reply, 200)
}

// ===== 上下文压缩（G5，确定性截断）=====

const (
	compactKeepRecent  = 20  // 最近 N 条消息保持原样
	compactMaxToolText = 200 // 更早的 tool 消息内容截断长度
)

// CompactMessages 压缩历史：keepRecent 之外的工具结果截断，assistant/user 保留。
// 输入输出均为 []Message（会话持久化消息），仅在构造 LLM messages 前使用，不改动存储。
func CompactMessages(msgs []Message) []Message {
	if len(msgs) <= compactKeepRecent {
		return msgs
	}
	cut := len(msgs) - compactKeepRecent
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := 0; i < cut; i++ {
		if out[i].Role == RoleTool && len(out[i].Content) > compactMaxToolText {
			out[i].Content = TruncateRunes(out[i].Content, compactMaxToolText) + "\n…（早期工具结果已省略）"
		}
	}
	return out
}
