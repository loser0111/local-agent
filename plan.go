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

// ===== 数据契约（docs/plan-execute-implementation-plan.md 5.2）=====

// PlanStepStatus 步骤状态
const (
	StepPending = "pending" // 待执行
	StepRunning = "running" // 执行中
	StepDone    = "done"    // 完成
	StepFailed  = "failed"  // 失败
	StepSkipped = "skipped" // 因前序失败/取消跳过
)

// PlanStatus 计划状态
const (
	PlanAwaitingApproval = "awaiting_approval" // 待审核（可编辑）
	PlanRunning          = "running"           // 执行中
	PlanCompleted        = "completed"
	PlanFailed           = "failed"
	PlanCancelled        = "cancelled"
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
}

// generatePlanID 生成计划 ID：plan_<毫秒时间戳>_<随机4位hex>
func generatePlanID() string {
	random := make([]byte, 2)
	_, _ = rand.Read(random)
	return fmt.Sprintf("plan_%d_%s", time.Now().UnixMilli(), hex.EncodeToString(random))
}

// markRemainingSkipped 将 fromIndex 起所有 pending 步骤置为 skipped
func markRemainingSkipped(plan *Plan, fromIndex int) {
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

// loadAll 启动时加载全部计划；running 状态的计划置为失败（应用重启不支持断点续跑）
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
		if p.Status == PlanRunning {
			p.Status = PlanFailed
			for _, st := range p.Steps {
				if st.Status == StepRunning {
					st.Status = StepFailed
					st.Error = "应用重启，执行中断"
				}
			}
		}
		s.plans[p.ID] = &p
	}
}

func (s *PlanStore) saveLocked(p *Plan) error {
	p.UpdatedAt = time.Now().UnixMilli()
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
	case PlanFailed, PlanCancelled, PlanCompleted:
		// 可重开
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

const plannerSystemPrompt = `你是任务规划器。把用户的请求拆解为可逐步执行的计划。

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

// planDraft 规划器原始输出
type planDraft struct {
	NeedPlan bool            `json:"needPlan"`
	Title    string          `json:"title"`
	Steps    []planDraftStep `json:"steps"`
}

// parsePlanDraft 解析规划器输出；容忍 ```json 包裹与前后杂讯
func parsePlanDraft(content string) (*planDraft, error) {
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
	var d planDraft
	if err := json.Unmarshal([]byte(text[start:end+1]), &d); err != nil {
		return nil, fmt.Errorf("解析规划 JSON 失败: %w", err)
	}
	if d.NeedPlan && len(d.Steps) == 0 {
		return nil, fmt.Errorf("规划器返回 needPlan=true 但步骤为空")
	}
	return &d, nil
}

// buildPlannerUserPrompt 规划器用户消息：原始请求 + 工作区路径
func buildPlannerUserPrompt(query, dir string) string {
	var sb strings.Builder
	sb.WriteString("用户请求：" + query)
	if dir != "" {
		sb.WriteString("\n\n当前工作区目录：" + dir)
	}
	return sb.String()
}

// ===== 步骤执行上下文 =====

// buildPlanStepQuery 合成步骤的用户消息（由执行器持久化进会话历史）
func buildPlanStepQuery(plan *Plan, step *PlanStep) string {
	query := fmt.Sprintf("【计划步骤 %d/%d】%s", step.Index+1, len(plan.Steps), step.Title)
	if step.Detail != "" {
		query += "\n" + step.Detail
	}
	return query
}

// buildPlanSystemPrompt 在既有 system prompt（人设+工作区+技能）之上追加计划上下文
func buildPlanSystemPrompt(base string, plan *Plan, step *PlanStep) string {
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

// extractStepSummary 取回复首段（≤200字）作为步骤摘要
func extractStepSummary(reply string) string {
	reply = strings.TrimSpace(reply)
	for _, sep := range []string{"\n\n", "\n"} {
		if idx := strings.Index(reply, sep); idx > 0 {
			reply = reply[:idx]
			break
		}
	}
	return truncateRunes(reply, 200)
}

// truncateRunes 按字符截断（多字节安全），超长追加省略号
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// ===== 上下文压缩（G5，确定性截断）=====

const (
	compactKeepRecent  = 20  // 最近 N 条消息保持原样
	compactMaxToolText = 200 // 更早的 tool 消息内容截断长度
)

// compactMessages 压缩历史：keepRecent 之外的工具结果截断，assistant/user 保留。
// 输入输出均为 []Message（会话持久化消息），仅在构造 LLM messages 前使用，不改动存储。
func compactMessages(msgs []Message) []Message {
	if len(msgs) <= compactKeepRecent {
		return msgs
	}
	cut := len(msgs) - compactKeepRecent
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := 0; i < cut; i++ {
		if out[i].Role == RoleTool && len(out[i].Content) > compactMaxToolText {
			out[i].Content = truncateRunes(out[i].Content, compactMaxToolText) + "\n…（早期工具结果已省略）"
		}
	}
	return out
}
