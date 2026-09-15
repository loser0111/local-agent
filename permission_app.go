package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ===== App 侧的权限接线 =====
//
// 分层：
//
//	PermissionEngine  纯决策逻辑（permission.go），不依赖 Wails
//	permissionBroker  询问-应答回路：后端阻塞等待，前端弹窗作答
//	App               持有 per-session 的授权 / 审计 / 引擎缓存
//
// 授权与审计刻意挂在 App 上而不是引擎里：设置页改动规则会导致引擎重建，
// 而「本会话允许」的授权与审计记录都应当跨重建保留。

// permissionAskTimeout 等待用户作答的上限；超时按拒绝处理（fail closed）
const permissionAskTimeout = 5 * time.Minute

// ===== 询问中转 =====

type permissionBroker struct {
	mu      sync.Mutex
	pending map[string]chan PermissionDecision

	// emit 发送事件给前端；返回 false 表示没有可用通道（无法询问）
	emit func(req *PermissionRequest) bool

	// persist 把「永久允许」落盘；失败不阻断本次放行，仅记录
	persist func(req PermissionRequest, dec PermissionDecision)

	timeout time.Duration
}

func newPermissionBroker() *permissionBroker {
	return &permissionBroker{
		pending: make(map[string]chan PermissionDecision),
		timeout: permissionAskTimeout,
	}
}

// Ask 实现 Asker：发事件给前端并阻塞等待作答。
//
// 三个 select 分支缺一不可 —— 没有超时与取消分支的话，
// 一次无人应答的询问会永久挂住后端 goroutine。
func (b *permissionBroker) Ask(ctx context.Context, req PermissionRequest) (PermissionDecision, error) {
	if b.emit == nil {
		return PermissionDecision{}, fmt.Errorf("无前端通道，无法发起授权询问")
	}

	ch := make(chan PermissionDecision, 1) // 缓冲 1，应答方不会阻塞
	b.mu.Lock()
	b.pending[req.RequestID] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, req.RequestID)
		b.mu.Unlock()
	}()

	if !b.emit(&req) {
		return PermissionDecision{}, fmt.Errorf("无前端通道，无法发起授权询问")
	}

	timer := time.NewTimer(b.timeout)
	defer timer.Stop()

	select {
	case dec := <-ch:
		if dec.Allow && dec.Scope == ScopeAlways && b.persist != nil {
			b.persist(req, dec)
		}
		return dec, nil
	case <-timer.C:
		return PermissionDecision{}, fmt.Errorf("等待用户授权超时（%s），已按拒绝处理", b.timeout)
	case <-ctx.Done():
		return PermissionDecision{}, fmt.Errorf("会话已取消")
	}
}

// Resolve 前端作答入口
func (b *permissionBroker) Resolve(requestID string, dec PermissionDecision) error {
	b.mu.Lock()
	ch, ok := b.pending[requestID]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("授权请求不存在或已超时: %s", requestID)
	}
	select {
	case ch <- dec:
		return nil
	default:
		return fmt.Errorf("授权请求已处理: %s", requestID)
	}
}

// CancelAll 取消全部待答请求（会话取消时调用），
// 令其立即以拒绝结束，避免残留挂起的 goroutine。
func (b *permissionBroker) CancelAll() {
	b.mu.Lock()
	pending := b.pending
	b.pending = make(map[string]chan PermissionDecision)
	b.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- PermissionDecision{Allow: false}:
		default:
		}
	}
}

// ===== App 侧状态 =====

// permissionState per-session 的授权与审计
type permissionState struct {
	grants *GrantStore
	audit  *AuditLog
}

// permStateFor 取（或创建）某会话的权限状态
func (a *App) permStateFor(sessionID string) *permissionState {
	a.permMu.Lock()
	defer a.permMu.Unlock()
	if st, ok := a.permStates[sessionID]; ok {
		return st
	}
	st := &permissionState{grants: NewGrantStore(), audit: NewAuditLog()}
	a.permStates[sessionID] = st
	return st
}

// permissionProjectDir 解析会话对应的项目目录（失败返回空串）
func (a *App) permissionProjectDir(sessionID string) string {
	dir, err := a.resolveProjectDir(sessionID)
	if err != nil {
		return ""
	}
	return dir
}

// permissionEngineFor 构造（或复用）某会话的权限引擎。
// 每次对话开始前调用；规则来自三层配置的合并结果。
func (a *App) permissionEngineFor(session *Session) *PermissionEngine {
	if session == nil {
		return nil
	}
	st := a.permStateFor(session.ID)
	projectDir := a.permissionProjectDir(session.ID)
	resolved := a.settingsStore.Load(projectDir)
	rules := RuleSet{Allow: resolved.Allow, Ask: resolved.Ask, Deny: resolved.Deny}

	a.permMu.Lock()
	defer a.permMu.Unlock()
	if eng, ok := a.engines[session.ID]; ok {
		// 复用引擎（保留授权与审计），仅刷新模式与规则
		eng.SetMode(resolved.Mode)
		eng.SetRules(rules)
		return eng
	}
	eng := NewPermissionEngine(session.ID, resolved.Mode, rules, st.grants, a.permBroker, st.audit)
	a.engines[session.ID] = eng
	return eng
}

// ===== Bound 方法（前端可调用）=====

// ResolvePermission 前端对权限询问作答。
// decision: allow | deny；scope: once | session | always；rule 为可选的编辑后规则文本
func (a *App) ResolvePermission(requestID string, decision string, scope string, rule string) error {
	if requestID == "" {
		return fmt.Errorf("requestId 不能为空")
	}
	dec := PermissionDecision{
		Allow: decision == "allow",
		Scope: scope,
		Rule:  rule,
	}
	if dec.Scope != ScopeOnce && dec.Scope != ScopeSession && dec.Scope != ScopeAlways {
		dec.Scope = ScopeOnce
	}
	return a.permBroker.Resolve(requestID, dec)
}

// CancelChat 取消某会话正在进行的对话（中断 LLM 调用与工具执行）
func (a *App) CancelChat(sessionID string) error {
	a.chatMu.Lock()
	cancel, ok := a.chatCancels[sessionID]
	a.chatMu.Unlock()
	if ok {
		cancel()
	}
	// 同时放开等待授权中的请求，否则会一直挂到超时
	a.permBroker.CancelAll()
	return nil
}

// GetPermissionConfig 返回权限配置全貌（供设置页渲染）
func (a *App) GetPermissionConfig(sessionID string) map[string]interface{} {
	projectDir := ""
	if sessionID != "" {
		projectDir = a.permissionProjectDir(sessionID)
	}
	resolved := a.settingsStore.Load(projectDir)
	builtin := BuiltinRules()

	mode := resolved.Mode
	if sessionID != "" {
		a.permMu.Lock()
		if eng, ok := a.engines[sessionID]; ok {
			mode = eng.Mode()
		}
		a.permMu.Unlock()
	}

	return map[string]interface{}{
		"mode":         string(mode),
		"defaultMode":  string(resolved.Mode),
		"allow":        resolved.Allow,
		"ask":          resolved.Ask,
		"deny":         resolved.Deny,
		"builtinDeny":  builtin.Deny,
		"builtinAsk":   builtin.Ask,
		"sources":      resolved.Sources,
		"errors":       resolved.Errors,
		"projectDir":   projectDir,
		"globalPath":   a.settingsStore.GlobalPath(),
		"askTimeoutMs": int(permissionAskTimeout / time.Millisecond),
	}
}

// SetPermissionMode 切换会话的权限模式（不落盘，仅本次会话）
func (a *App) SetPermissionMode(sessionID string, mode string) error {
	if !ValidPermissionMode(PermissionMode(mode)) {
		return fmt.Errorf("非法的权限模式: %s", mode)
	}
	a.permMu.Lock()
	eng, ok := a.engines[sessionID]
	a.permMu.Unlock()
	if !ok {
		return fmt.Errorf("会话尚未初始化权限引擎: %s", sessionID)
	}
	eng.SetMode(PermissionMode(mode))
	return nil
}

// SetDefaultPermissionMode 修改默认权限模式并落盘
func (a *App) SetDefaultPermissionMode(sessionID string, mode string, source string) error {
	projectDir := ""
	if sessionID != "" {
		projectDir = a.permissionProjectDir(sessionID)
	}
	return a.settingsStore.SetDefaultMode(projectDir, mode, source)
}

// AddPermissionRule 在指定层新增一条规则
// bucket: allow | ask | deny；source: user | project | local
func (a *App) AddPermissionRule(sessionID string, bucket string, rule string, source string) error {
	projectDir := ""
	if sessionID != "" {
		projectDir = a.permissionProjectDir(sessionID)
	}
	return a.settingsStore.AddRule(projectDir, bucket, rule, source)
}

// RemovePermissionRule 删除指定层的一条规则。
//
// 除了改文件，还会**同步撤销内存里等价的会话授权** —— 否则会出现
// 「规则删掉了、本会话却仍然放行」的错觉（「永久允许」会同时写入规则与授权）。
func (a *App) RemovePermissionRule(sessionID string, bucket string, rule string, source string) error {
	projectDir := ""
	if sessionID != "" {
		projectDir = a.permissionProjectDir(sessionID)
	}
	if err := a.settingsStore.RemoveRule(projectDir, bucket, rule, source); err != nil {
		return err
	}
	// 只有 allow 桶的规则与「放行」相关，其余桶没有对应授权
	if bucket == BucketAllow {
		if parsed, err := ParseRule(rule, source); err == nil {
			a.revokeMatchingGrants(sessionID, parsed)
		}
	}
	return nil
}

// revokeMatchingGrants 撤销与规则等价的会话授权，返回撤销条数
func (a *App) revokeMatchingGrants(sessionID string, r Rule) int {
	a.permMu.Lock()
	st, ok := a.permStates[sessionID]
	a.permMu.Unlock()
	if !ok {
		return 0
	}
	return st.grants.RemoveMatching(r)
}

// RevokePermissionGrant 逐条撤销会话授权。
//
// 若该授权是「永久」级别，它当初同时被写入了配置文件的 allow 规则，
// 这里会一并删掉那条规则 —— 否则下次加载时规则会把它重新带回来。
func (a *App) RevokePermissionGrant(sessionID string, toolName string, spec string, isPrefix bool, kind string) error {
	a.permMu.Lock()
	st, ok := a.permStates[sessionID]
	a.permMu.Unlock()
	if !ok {
		return fmt.Errorf("该会话还没有权限状态")
	}
	gr, found, wasAlways := st.grants.RemoveOne(toolName, spec, isPrefix, SpecKind(kind))
	if !found {
		return fmt.Errorf("未找到该授权（可能已被撤销）")
	}
	if wasAlways {
		// 规则当初可能落在任一层：写入时若没有项目目录会回退到用户全局层
		ruleText := gr.Rule().String()
		projectDir := a.permissionProjectDir(sessionID)
		if _, removed := a.settingsStore.RemoveRuleAnyLayer(projectDir, BucketAllow, ruleText); !removed {
			return fmt.Errorf("已撤销会话授权，但未在配置文件中找到对应规则 %q，请到规则列表里手动确认", ruleText)
		}
	}
	return nil
}

// GetPermissionAudit 返回某会话的权限决策审计
func (a *App) GetPermissionAudit(sessionID string) []AuditEntry {
	a.permMu.Lock()
	st, ok := a.permStates[sessionID]
	a.permMu.Unlock()
	if !ok {
		return []AuditEntry{}
	}
	return st.audit.List()
}

// GetPermissionGrants 返回某会话当前的授权列表
func (a *App) GetPermissionGrants(sessionID string) []Grant {
	a.permMu.Lock()
	st, ok := a.permStates[sessionID]
	a.permMu.Unlock()
	if !ok {
		return []Grant{}
	}
	return st.grants.List()
}

// ClearPermissionGrants 清空某会话的授权（用户想重新逐个确认时用）
func (a *App) ClearPermissionGrants(sessionID string) error {
	a.permMu.Lock()
	st, ok := a.permStates[sessionID]
	a.permMu.Unlock()
	if ok {
		st.grants.Clear()
	}
	return nil
}

// ===== 启动接线 =====

// initPermission 在 startup 里调用：装配 broker 的收发通道
func (a *App) initPermission() {
	a.permStates = make(map[string]*permissionState)
	a.engines = make(map[string]*PermissionEngine)

	a.permBroker.emit = func(req *PermissionRequest) bool {
		if a.ctx == nil {
			return false
		}
		wailsRuntime.EventsEmit(a.ctx, "chat:event", ChatEvent{
			Type:       "permission_request",
			Permission: req,
		})
		return true
	}
	a.permBroker.persist = func(req PermissionRequest, dec PermissionDecision) {
		projectDir := ""
		if req.SessionID != "" {
			projectDir = a.permissionProjectDir(req.SessionID)
		}
		rule := dec.Rule
		if rule == "" {
			rule = req.SuggestRule
		}
		if rule == "" {
			return
		}
		// 「永久允许」写入项目本地层；无项目目录时 pathForSource 会回退到用户全局层
		if err := a.settingsStore.AddRule(projectDir, BucketAllow, rule, SourceLocal); err != nil {
			fmt.Printf("[Permission] 写入永久授权失败: %v\n", err)
		}
	}
}
