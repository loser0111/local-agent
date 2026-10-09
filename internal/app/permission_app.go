package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/permission"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 会话级权限状态（供界面展示）=====

// RuleView 一条生效规则（含所在桶与来源）
type RuleView struct {
	Rule   string `json:"rule"`
	Bucket string `json:"bucket"` // deny / ask / allow
	Source string `json:"source"`
}

// PermissionState 会话的权限现状快照
type PermissionState struct {
	SessionID  string                  `json:"sessionId"`
	Mode       string                  `json:"mode"`
	ProjectDir string                  `json:"projectDir"`
	Rules      []RuleView              `json:"rules"`
	Sources    []permission.RuleSource `json:"sources"`
	Grants     []permission.Grant      `json:"grants"`
	Warnings   []string                `json:"warnings"`
}

// ===== 装配：把引擎与界面接到一起 =====

// appResponder 把 *App 的界面能力适配成 permission.Responder。
//
// 引擎下沉到 internal/permission 后，它不再持有 *App（依赖单向：main → permission），
// 需要"弹窗问人"与"写规则文件"这两件根包才能做的事时，走这个 consumer-side 适配器。
type appResponder struct{ app *App }

// Available 当前是否有可应答的界面。没有 Wails 运行时上下文（无头运行 / 自动化测试）
// 就答不上话，此时网关会直接按拒绝处理，而不是白等一个超时周期。
func (r appResponder) Available() bool { return r.app != nil && r.app.ctx != nil }

// Emit 把授权请求推给前端
func (r appResponder) Emit(req permission.Request) { r.app.emitPermissionRequest(req) }

// AddAllowRule 把用户选择的规则写入配置层（引擎只负责判定"要不要写"，写哪里由这里决定）
func (r appResponder) AddAllowRule(sessionID, layer, rule string) error {
	return r.app.AddPermissionRule(sessionID, layer, permission.RuleBucketAllow, rule)
}

// toolEnforcer 把 permission.Enforcer 适配成 tool.Enforcer。
//
// 两边的签名只差工具参数的类型：引擎用的是**最小工具视图**（GetName）——因为
// internal/tool 已经 import 了 internal/permission，引擎反向 import 回去就成环；
// 而 tool.Enforcer 要求收 tool.ToolInterface。Go 的方法签名必须逐字匹配（参数类型是
// permission.ToolView 就不算实现 tool.Enforcer），所以这层适配是必需的，不是多余包装。
type toolEnforcer struct{ e *permission.Enforcer }

func (g toolEnforcer) Enforce(ctx context.Context, t tool.ToolInterface, args map[string]interface{}) error {
	return g.e.Enforce(ctx, t, args)
}

// ensurePermissionState 懒初始化权限相关组件（容忍测试里手工构造的 App）
func (a *App) ensurePermissionState() {
	if a.permissionGrants == nil {
		a.permissionGrants = permission.NewGrantStore()
	}
	if a.permissionAudit == nil {
		a.permissionAudit = permission.NewAuditLog(500)
	}
	if a.permissionBroker == nil {
		a.permissionBroker = permission.NewBroker(5 * time.Minute)
	}
	// 用户提问回路（ask_user）与权限回路共用这个懒初始化入口
	if a.askBroker == nil {
		a.askBroker = agent.NewAskBroker(10 * time.Minute)
	}
}

// bindEnforcer 把引擎绑到本 App 的授权存储 / 审计 / 询问回路上（规则快照由调用方补）。
func (a *App) bindEnforcer(sessionID string, mode permission.Mode) *permission.Enforcer {
	a.ensurePermissionState()
	return &permission.Enforcer{
		SessionID: sessionID,
		Mode:      mode,
		Grants:    a.permissionGrants,
		Audit:     a.permissionAudit,
		Broker:    a.permissionBroker,
		UI:        appResponder{app: a},
	}
}

// newPermissionEnforcer 为一次运行构造网关（规则快照取自当前配置）
func (a *App) newPermissionEnforcer(session *store.Session, projectDir string) *permission.Enforcer {
	rules, _, warnings := permission.LoadRuleSet(a.baseDir, projectDir)
	for _, w := range warnings {
		fmt.Printf("[permission] %s\n", w)
	}
	sessionID := ""
	mode := permission.ModeManual
	if session != nil {
		sessionID = session.ID
		mode = permission.NormalizeMode(session.PermissionMode)
	}
	e := a.bindEnforcer(sessionID, mode)
	e.ProjectDir = projectDir
	e.Rules = rules
	return e
}

// emitPermissionRequest 把授权请求推给前端。
// 归属取自请求自身（req.SessionID）：前端要据此判断"这是不是当前会话在等我"，
// 后台会话的请求只出列表标记、不弹窗（两个会话同时等人应答是合法状态）。
func (a *App) emitPermissionRequest(req permission.Request) {
	a.emitInteraction(req.SessionID, ChatEvent{
		Type:       ChatEventPermission,
		Permission: &req,
	})
}

// PendingInteraction 该会话当前挂起的交互请求（授权或提问）；没有则返回 nil。
//
// 前端在模型运行期间轮询它，作为事件通道的兜底：只要后端在等人，
// 即使事件因版本错配或时序问题没送达，弹窗也能补上。
type PendingInteraction struct {
	Kind       string              `json:"kind"` // permission | ask
	Permission *permission.Request `json:"permission,omitempty"`
	Ask        *agent.AskRequest   `json:"ask,omitempty"`
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
	if _, ok := permission.ParseDecision(decision); !ok {
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
			layer = permission.PermissionScopeLocal
		}
		switch layer {
		case permission.PermissionScopeUser, permission.PermissionScopeProject, permission.PermissionScopeLocal:
		default:
			return fmt.Errorf("未知的规则写入层: %s", layer)
		}
	}
	return a.permissionBroker.Resolve(permission.Answer{
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

	rules, sources, warnings := permission.LoadRuleSet(a.baseDir, dir)
	views := make([]RuleView, 0, len(rules.AllRules()))
	appendViews := func(bucket string, list []permission.Rule) {
		for _, r := range list {
			views = append(views, RuleView{Rule: permission.FormatRule(r), Bucket: bucket, Source: r.Source})
		}
	}
	appendViews(permission.RuleBucketDeny, rules.Deny)
	appendViews(permission.RuleBucketAsk, rules.Ask)
	appendViews(permission.RuleBucketAllow, rules.Allow)

	return &PermissionState{
		SessionID:  sessionID,
		Mode:       string(permission.NormalizeMode(session.PermissionMode)),
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
	if err := permission.AddRuleToScope(a.baseDir, projectDir, scope, bucket, rule); err != nil {
		return err
	}
	a.permissionAudit.Append(permission.AuditEntry{
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
	if err := permission.RemoveRuleFromScope(a.baseDir, projectDir, scope, bucket, rule); err != nil {
		return err
	}
	a.permissionAudit.Append(permission.AuditEntry{
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
func (a *App) GetPermissionAudit(sessionID string) []permission.AuditEntry {
	a.ensurePermissionState()
	list := a.permissionAudit.List(sessionID)
	if list == nil {
		return []permission.AuditEntry{}
	}
	return list
}

// SetSessionPermissionMode 设置会话权限模式（校验后落盘）
func (a *App) SetSessionPermissionMode(sessionID, mode string) (*store.Session, error) {
	m := permission.NormalizeMode(mode)
	if !permission.ValidMode(permission.Mode(strings.TrimSpace(mode))) {
		return nil, fmt.Errorf("未知的权限模式: %s", mode)
	}
	value := string(m)
	return a.sessionStore.UpdateSession(sessionID, store.SessionPatch{PermissionMode: &value})
}
