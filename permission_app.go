package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ===== 权限网关：接口与装饰器 =====

// Enforcer 工具执行前的权限网关。判定需要用户确认时，实现内部负责把请求送到前端
// 并等待答复（可能阻塞数分钟），返回 error 表示拒绝执行（错误信息会回填给 LLM）。
type Enforcer interface {
	Enforce(ctx context.Context, tool ToolInterface, args map[string]interface{}) error
}

// AllowAllEnforcer 不做任何判定的网关。
// **仅供测试与非交互场景显式使用**：装配时绝不默认使用它，避免"忘了装配"变成静默放行。
type AllowAllEnforcer struct{}

// Enforce 永远放行
func (AllowAllEnforcer) Enforce(context.Context, ToolInterface, map[string]interface{}) error {
	return nil
}

// DenyAllEnforcer 拒绝一切的网关，作为装配缺失时的兜底（fail closed）
type DenyAllEnforcer struct{}

// Enforce 永远拒绝
func (DenyAllEnforcer) Enforce(context.Context, ToolInterface, map[string]interface{}) error {
	return fmt.Errorf("权限网关未装配，已拒绝执行（fail closed）")
}

// guardedTool 把工具包一层权限网关。
// 装饰发生在 registry 装配期，因此 tool_router 的分发路径与直调路径都会经过同一个网关，
// 不存在"某条路径忘了判定"的可能。
type guardedTool struct {
	inner ToolInterface
	enf   Enforcer
}

// Execute 先判定再执行
func (g *guardedTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if g.enf != nil {
		if err := g.enf.Enforce(ctx, g.inner, args); err != nil {
			return "", err
		}
	}
	return g.inner.Execute(ctx, args)
}

// GetName 透传工具名（tool_router 的 list/describe 依赖它）
func (g *guardedTool) GetName() string { return g.inner.GetName() }

// GetDescription 透传描述
func (g *guardedTool) GetDescription() string { return g.inner.GetDescription() }

// GetParameters 透传参数定义
func (g *guardedTool) GetParameters() map[string]*ToolArgDef { return g.inner.GetParameters() }

// ===== 授权请求 / 应答协议 =====

// PermissionAskRequest 送到前端的授权请求
type PermissionAskRequest struct {
	ID        string   `json:"id"`
	SessionID string   `json:"sessionId"`
	Tool      string   `json:"tool"`
	Subject   string   `json:"subject"`   // 待确认内容的单行摘要
	Units     []string `json:"units"`     // 命令类主体分解后的各段（供界面展示与生成规则）
	Stage     string   `json:"stage"`     // 判定落在管线的哪一步
	Reason    string   `json:"reason"`    // 为什么要问
	CreatedAt int64    `json:"createdAt"`
}

// PermissionAnswer 前端回传的授权答复。
// Scope 取值：once=仅此一次；session=本会话放行（逐段精确记忆）；rule=写入规则文件。
type PermissionAnswer struct {
	ID       string `json:"id"`
	Decision string `json:"decision"` // allow / deny
	Scope    string `json:"scope"`    // once / session / rule
	Rule     string `json:"rule"`     // scope=rule 时的规则文本
	Layer    string `json:"layer"`    // scope=rule 时的写入层：user / project / local
}

// ===== 询问回路（broker）=====

// permissionBroker 授权请求的等待-唤醒回路。
// 后端在工具循环里阻塞等待，前端弹窗把答复经 ResolvePermission 灌回来。
type permissionBroker struct {
	mu      sync.Mutex
	timeout time.Duration
	seq     int64
	pending map[string]*permissionPending
}

type permissionPending struct {
	sessionID string
	req       PermissionAskRequest
	ch        chan PermissionAnswer
}

// setRequest 补全挂起请求的本体（ID 生成后才能写回），供兜底查询使用
func (b *permissionBroker) setRequest(id string, req PermissionAskRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.pending[id]; ok {
		p.req = req
	}
}

// Pending 返回某会话挂起的授权请求；没有则返回 nil。
// 前端在模型运行期间轮询它作为兜底：事件万一没送达，弹窗仍能补上。
func (b *permissionBroker) Pending(sessionID string) *PermissionAskRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.pending {
		if p.sessionID == sessionID {
			req := p.req
			return &req
		}
	}
	return nil
}

// newPermissionBroker 创建回路；timeout<=0 时默认 5 分钟
func newPermissionBroker(timeout time.Duration) *permissionBroker {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &permissionBroker{timeout: timeout, pending: map[string]*permissionPending{}}
}

// register 登记一个挂起请求，返回请求 ID 与等待通道
func (b *permissionBroker) register(sessionID string) (string, chan PermissionAnswer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := fmt.Sprintf("perm_%d_%d", time.Now().UnixMilli(), b.seq)
	ch := make(chan PermissionAnswer, 1)
	if b.pending == nil {
		b.pending = map[string]*permissionPending{}
	}
	b.pending[id] = &permissionPending{sessionID: sessionID, ch: ch}
	return id, ch
}

// PendingCount 当前挂起的请求数（测试与诊断用）
func (b *permissionBroker) PendingCount(sessionID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, p := range b.pending {
		if p.sessionID == sessionID {
			n++
		}
	}
	return n
}

// forget 注销挂起请求
func (b *permissionBroker) forget(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

// Resolve 前端答复。未知 ID 说明已超时或已被取消（迟到答复），返回错误供界面提示。
func (b *permissionBroker) Resolve(ans PermissionAnswer) error {
	b.mu.Lock()
	p, ok := b.pending[ans.ID]
	if ok {
		delete(b.pending, ans.ID)
	}
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("授权请求不存在或已超时: %s", ans.ID)
	}
	p.ch <- ans
	return nil
}

// CancelSession 取消某会话全部挂起的请求：灌入一个拒绝答复，让等待方立即返回。
// 返回被取消的请求数。
func (b *permissionBroker) CancelSession(sessionID string) int {
	b.mu.Lock()
	targets := make([]*permissionPending, 0, len(b.pending))
	for id, p := range b.pending {
		if p.sessionID == sessionID {
			targets = append(targets, p)
			delete(b.pending, id)
		}
	}
	b.mu.Unlock()
	for _, p := range targets {
		// 通道缓冲为 1 且此处已从 pending 摘除，不会阻塞
		p.ch <- PermissionAnswer{Decision: "deny", Scope: "once"}
	}
	return len(targets)
}

// Wait 等待答复。超时、会话取消、ctx 结束都返回错误，调用方一律按拒绝处理。
func (b *permissionBroker) Wait(id string, ch chan PermissionAnswer, ctx context.Context) (PermissionAnswer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(b.timeout)
	defer timer.Stop()

	select {
	case ans := <-ch:
		d, ok := ParseDecision(ans.Decision)
		if !ok || d != DecisionAllow {
			return PermissionAnswer{Decision: DecisionDeny.String(), Scope: ans.Scope}, nil
		}
		// 归一化决定字段的写法（避免 "Allow" 这类大小写差异被下游误判为拒绝）
		return PermissionAnswer{Decision: DecisionAllow.String(), Scope: ans.Scope, Rule: ans.Rule, Layer: ans.Layer}, nil
	case <-timer.C:
		b.forget(id)
		return PermissionAnswer{}, fmt.Errorf("等待用户授权超时（%s）", b.timeout)
	case <-ctx.Done():
		b.forget(id)
		return PermissionAnswer{}, fmt.Errorf("授权等待已取消")
	}
}

// answerAllows 答复是否构成放行。无法识别的决定一律视为不放行（fail closed）。
func answerAllows(ans PermissionAnswer) bool {
	d, ok := ParseDecision(ans.Decision)
	return ok && d == DecisionAllow
}

// ===== 会话级权限状态（供界面展示）=====

// RuleView 一条生效规则（含所在桶与来源）
type RuleView struct {
	Rule   string `json:"rule"`
	Bucket string `json:"bucket"` // deny / ask / allow
	Source string `json:"source"`
}

// PermissionState 会话的权限现状快照
type PermissionState struct {
	SessionID  string       `json:"sessionId"`
	Mode       string       `json:"mode"`
	ProjectDir string       `json:"projectDir"`
	Rules      []RuleView   `json:"rules"`
	Sources    []RuleSource `json:"sources"`
	Grants     []Grant      `json:"grants"`
	Warnings   []string     `json:"warnings"`
}

// ===== 权限网关实现 =====

// permissionEnforcer 把引擎与询问回路接到工具执行上。每次 runToolLoop 构造一个实例，
// 绑定了会话、工作目录、模式，以及**那一刻**的规则快照（改配置后下一条消息生效）。
type permissionEnforcer struct {
	app        *App
	sessionID  string
	projectDir string
	mode       Mode
	rules      *RuleSet
}

// Enforce 判定一次工具调用。
func (e *permissionEnforcer) Enforce(ctx context.Context, tool ToolInterface, args map[string]interface{}) error {
	subject := e.subjectFor(tool, args)

	// 命令为空属于**调用错误**（常见于模型把参数写成 command 而不是 cmd），
	// 直接报错让模型改正即可——不该让用户为一条根本不存在的命令等授权弹窗。
	// 注意：只有"空"走这条；"有内容但解析不可信"仍然询问（fail closed），别混为一谈。
	if subject.Kind == SubjectCommand && strings.TrimSpace(subject.Raw) == "" {
		e.audit(subject, Verdict{Decision: DecisionDeny, Stage: StageInvalid, Reason: "命令为空（参数缺失）"})
		return fmt.Errorf("命令为空：请检查是否把参数名写成了 cmd")
	}

	verdict := Authorize(AuthorizeInput{
		Subject:    subject,
		Mode:       e.mode,
		Rules:      e.rules,
		Grants:     e.app.permissionGrants.List(e.sessionID),
		ProjectDir: e.projectDir,
	})
	e.audit(subject, verdict)

	switch verdict.Decision {
	case DecisionAllow:
		return nil
	case DecisionDeny:
		return fmt.Errorf("权限拒绝（%s）：%s", verdict.Stage, verdict.Reason)
	default:
		return e.askAndApply(ctx, subject, verdict)
	}
}

// subjectFor 构造判定主体。实现了 SubjectProvider 的工具（命令模板类）自报主体，
// 其余按"工具名 + 参数摘要"处理。
func (e *permissionEnforcer) subjectFor(tool ToolInterface, args map[string]interface{}) Subject {
	if tool == nil {
		return Subject{}
	}
	if sp, ok := tool.(SubjectProvider); ok {
		return sp.PermissionSubject(args)
	}
	return newToolSubject(tool.GetName(), args)
}

// audit 记录一条判定
func (e *permissionEnforcer) audit(s Subject, v Verdict) {
	if e.app == nil {
		return
	}
	e.app.permissionAudit.Append(AuditEntry{
		SessionID: e.sessionID,
		Tool:      s.Tool,
		Subject:   s.Summary(),
		Decision:  v.Decision.String(),
		Stage:     v.Stage,
		Reason:    v.Reason,
		Rule:      FormatRule(v.Rule),
		Source:    v.Rule.Source,
	})
}

// askAndApply 进入询问回路：发事件 → 等答复 → 按答复放行或拒绝
func (e *permissionEnforcer) askAndApply(ctx context.Context, subject Subject, verdict Verdict) error {
	if e.app == nil || e.app.permissionBroker == nil {
		return fmt.Errorf("权限未获授权：没有可用的授权回路")
	}
	// 没有 Wails 运行时上下文 = 没有可应答的界面（如无头运行/自动化测试）。
	// 此时不进入阻塞等待，直接按拒绝处理：既符合 fail closed，也避免白等一个超时周期。
	if e.app.ctx == nil {
		e.app.permissionAudit.Append(AuditEntry{
			SessionID: e.sessionID,
			Tool:      subject.Tool,
			Subject:   subject.Summary(),
			Decision:  DecisionDeny.String(),
			Stage:     "no-responder",
			Reason:    "当前没有可应答的界面",
		})
		return fmt.Errorf("权限未获授权：当前没有可应答的界面（%s）", verdict.Reason)
	}
	id, ch := e.app.permissionBroker.register(e.sessionID)
	req := PermissionAskRequest{
		ID:        id,
		SessionID: e.sessionID,
		Tool:      subject.Tool,
		Subject:   subject.Summary(),
		Units:     subject.Units,
		Stage:     verdict.Stage,
		Reason:    verdict.Reason,
		CreatedAt: time.Now().UnixMilli(),
	}
	e.app.permissionBroker.setRequest(id, req)
	e.app.emitPermissionRequest(req)

	ans, err := e.app.permissionBroker.Wait(id, ch, ctx)
	if err != nil {
		// 超时 / 取消 / 无人应答：一律按拒绝处理（fail closed）
		e.app.permissionAudit.Append(AuditEntry{
			SessionID: e.sessionID,
			Tool:      subject.Tool,
			Subject:   subject.Summary(),
			Decision:  DecisionDeny.String(),
			Stage:     "ask-unanswered",
			Reason:    err.Error(),
		})
		return fmt.Errorf("权限未获授权：%v", err)
	}

	if !answerAllows(ans) {
		e.app.permissionAudit.Append(AuditEntry{
			SessionID: e.sessionID,
			Tool:      subject.Tool,
			Subject:   subject.Summary(),
			Decision:  DecisionDeny.String(),
			Stage:     "ask-denied",
			Reason:    "用户拒绝了本次操作",
		})
		return fmt.Errorf("用户拒绝了本次操作：%s", subject.Summary())
	}

	// 放行，并按用户选择的记忆范围落账
	switch ans.Scope {
	case "session":
		// 逐段精确记忆：复合命令的每一段各记一条，保证放宽方向仍是"全部覆盖"
		for _, u := range subject.Units {
			if strings.TrimSpace(u) == "" {
				continue
			}
			e.app.permissionGrants.Add(e.sessionID, Grant{Tool: subject.Tool, Spec: u})
		}
	case "rule":
		if strings.TrimSpace(ans.Rule) == "" {
			return fmt.Errorf("已放行但规则为空，未写入配置文件（请重试并填写规则）")
		}
		layer := ans.Layer
		if layer == "" {
			layer = PermissionScopeLocal
		}
		if err := e.app.AddPermissionRule(e.sessionID, layer, RuleBucketAllow, ans.Rule); err != nil {
			return fmt.Errorf("已放行但规则写入失败：%w", err)
		}
	}
	return nil
}

// ===== App：装配与对外方法 =====

// ensurePermissionState 懒初始化权限相关组件（容忍测试里手工构造的 App）
func (a *App) ensurePermissionState() {
	if a.permissionGrants == nil {
		a.permissionGrants = NewGrantStore()
	}
	if a.permissionAudit == nil {
		a.permissionAudit = NewAuditLog(500)
	}
	if a.permissionBroker == nil {
		a.permissionBroker = newPermissionBroker(5 * time.Minute)
	}
	// 用户提问回路（ask_user）与权限回路共用这个懒初始化入口
	if a.askBroker == nil {
		a.askBroker = newAskBroker(10 * time.Minute)
	}
}

// newPermissionEnforcer 为一次运行构造网关（规则快照取自当前配置）
func (a *App) newPermissionEnforcer(session *Session, projectDir string) *permissionEnforcer {
	a.ensurePermissionState()
	rules, _, warnings := LoadRuleSet(a.baseDir, projectDir)
	for _, w := range warnings {
		fmt.Printf("[permission] %s\n", w)
	}
	sessionID := ""
	mode := ModeManual
	if session != nil {
		sessionID = session.ID
		mode = NormalizeMode(session.PermissionMode)
	}
	return &permissionEnforcer{
		app:        a,
		sessionID:  sessionID,
		projectDir: projectDir,
		mode:       mode,
		rules:      rules,
	}
}

// emitPermissionRequest 把授权请求推给前端
func (a *App) emitPermissionRequest(req PermissionAskRequest) {
	a.emitInteraction(ChatEvent{
		Type:       ChatEventPermission,
		Permission: &req,
	})
}

// PendingInteraction 该会话当前挂起的交互请求（授权或提问）；没有则返回 nil。
//
// 前端在模型运行期间轮询它，作为事件通道的兜底：只要后端在等人，
// 即使事件因版本错配或时序问题没送达，弹窗也能补上。
type PendingInteraction struct {
	Kind       string                `json:"kind"` // permission | ask
	Permission *PermissionAskRequest `json:"permission,omitempty"`
	Ask        *AskRequest           `json:"ask,omitempty"`
}

// GetPendingInteraction 返回该会话挂起的交互请求（bound 方法）
func (a *App) GetPendingInteraction(sessionID string) *PendingInteraction {
	a.ensurePermissionState()
	if p := a.permissionBroker.Pending(sessionID); p != nil {
		return &PendingInteraction{Kind: "permission", Permission: p}
	}
	if q := a.askBroker.Pending(sessionID); q != nil {
		return &PendingInteraction{Kind: "ask", Ask: q}
	}
	return nil
}

// ResolvePermission 前端应答授权请求
func (a *App) ResolvePermission(id, decision, scope, rule, layer string) error {
	a.ensurePermissionState()
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("授权请求 ID 不能为空")
	}
	if _, ok := ParseDecision(decision); !ok {
		return fmt.Errorf("未知的授权决定: %s", decision)
	}
	if scope == "" {
		scope = "once"
	}
	switch scope {
	case "once", "session", "rule":
	default:
		return fmt.Errorf("未知的授权范围: %s", scope)
	}
	if scope == "rule" {
		if layer == "" {
			layer = PermissionScopeLocal
		}
		switch layer {
		case PermissionScopeUser, PermissionScopeProject, PermissionScopeLocal:
		default:
			return fmt.Errorf("未知的规则写入层: %s", layer)
		}
	}
	return a.permissionBroker.Resolve(PermissionAnswer{
		ID:       id,
		Decision: decision,
		Scope:    scope,
		Rule:     rule,
		Layer:    layer,
	})
}

// CancelPermissionWait 取消会话内挂起的授权等待（前端「停止」按钮调用）。
// 与计划取消的协作式语义一致：进行中的调用会收到拒绝并立刻返回。
func (a *App) CancelPermissionWait(sessionID string) error {
	a.ensurePermissionState()
	n := a.permissionBroker.CancelSession(sessionID)
	if n == 0 {
		return fmt.Errorf("当前没有等待授权的操作")
	}
	return nil
}

// GetPermissionState 返回会话的权限现状（模式、生效规则、来源、会话授权、提示）
func (a *App) GetPermissionState(sessionID string) (*PermissionState, error) {
	a.ensurePermissionState()
	session, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	dir, _ := a.resolveProjectDir(sessionID)

	rules, sources, warnings := LoadRuleSet(a.baseDir, dir)
	views := make([]RuleView, 0, len(rules.AllRules()))
	appendViews := func(bucket string, list []Rule) {
		for _, r := range list {
			views = append(views, RuleView{Rule: FormatRule(r), Bucket: bucket, Source: r.Source})
		}
	}
	appendViews(RuleBucketDeny, rules.Deny)
	appendViews(RuleBucketAsk, rules.Ask)
	appendViews(RuleBucketAllow, rules.Allow)

	return &PermissionState{
		SessionID:  sessionID,
		Mode:       string(NormalizeMode(session.PermissionMode)),
		ProjectDir: dir,
		Rules:      views,
		Sources:    sources,
		Grants:     a.permissionGrants.List(sessionID),
		Warnings:   warnings,
	}, nil
}

// AddPermissionRule 往指定层追加一条规则（bucket: deny / ask / allow）
func (a *App) AddPermissionRule(sessionID, scope, bucket, rule string) error {
	a.ensurePermissionState()
	projectDir := ""
	if sessionID != "" {
		if s, err := a.sessionStore.GetSession(sessionID); err == nil {
			projectDir = s.Project
		}
	}
	if err := AddRuleToScope(a.baseDir, projectDir, scope, bucket, rule); err != nil {
		return err
	}
	a.permissionAudit.Append(AuditEntry{
		SessionID: sessionID,
		Tool:      "-",
		Subject:   rule,
		Decision:  "config",
		Stage:     "rule-added",
		Reason:    fmt.Sprintf("新增 %s 规则（层：%s）", bucket, scope),
	})
	return nil
}

// RemovePermissionRule 从指定层移除一条规则
func (a *App) RemovePermissionRule(sessionID, scope, bucket, rule string) error {
	a.ensurePermissionState()
	projectDir := ""
	if sessionID != "" {
		if s, err := a.sessionStore.GetSession(sessionID); err == nil {
			projectDir = s.Project
		}
	}
	if err := RemoveRuleFromScope(a.baseDir, projectDir, scope, bucket, rule); err != nil {
		return err
	}
	a.permissionAudit.Append(AuditEntry{
		SessionID: sessionID,
		Tool:      "-",
		Subject:   rule,
		Decision:  "config",
		Stage:     "rule-removed",
		Reason:    fmt.Sprintf("移除 %s 规则（层：%s）", bucket, scope),
	})
	return nil
}

// ClearPermissionGrants 清空会话授权（"忘掉本会话的放行记录"）
func (a *App) ClearPermissionGrants(sessionID string) error {
	a.ensurePermissionState()
	a.permissionGrants.Clear(sessionID)
	a.permissionAudit.Clear(sessionID)
	return nil
}

// GetPermissionAudit 返回会话的判定审计记录
func (a *App) GetPermissionAudit(sessionID string) []AuditEntry {
	a.ensurePermissionState()
	list := a.permissionAudit.List(sessionID)
	if list == nil {
		return []AuditEntry{}
	}
	return list
}

// SetSessionPermissionMode 设置会话权限模式（校验后落盘）
func (a *App) SetSessionPermissionMode(sessionID, mode string) (*Session, error) {
	m := NormalizeMode(mode)
	if !ValidMode(Mode(strings.TrimSpace(mode))) {
		return nil, fmt.Errorf("未知的权限模式: %s", mode)
	}
	value := string(m)
	return a.sessionStore.UpdateSession(sessionID, SessionPatch{PermissionMode: &value})
}
