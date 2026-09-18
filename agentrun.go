package main

import (
	"context"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ===== 一次运行的依赖封装（P3 第 1 步：纯重构）=====
//
// 为什么需要这一层：runToolLoop 原先直接依赖 *App（406 行里 33 处 a.X）。
// 子代理要用同一个循环，但它的会话、权限网关、事件通道都与主会话不同——
// 把这些收进 agentRun，循环就只依赖它，不必复制一份循环（复制出来的两份必然漂移）。
//
// **隔离是怎么来的**：子代理自建一个会话（配置从父会话复制），因此天然有独立的
// sessionID。消息、diff 归因、文件改动记录、checkpoint ref 全都按 sessionID 键控，
// 于是"不污染父会话"不需要额外的归因键字段——这是对原方案的一处简化
// （原方案设计了 WorkspaceKey，实测多余）。
//
// 与方案的另一处偏离：循环**没有搬到新文件**，而是留在 chat.go、只把依赖来源换掉。
// 理由是这一步要求"行为不变"，而 file move 会让 diff 里"移动"与"改动"混在一起、
// 无法逐行复核。分层是按依赖边界达成的，不是按文件位置。
type agentRun struct {
	RunID     string
	SessionID string // 消息与会话状态的归属；子代理填自己的会话 ID
	PlanID    string // 非空表示这是一次计划执行

	Dir          string       // 工作区目录
	IsRepo       bool         // 工作区是否为 git 仓库（决定要不要算 diff / 取 checkpoint）
	SystemPrompt string       // 本次运行的系统提示词
	Model        *Model       // 使用的模型
	ToolView     *SessionView // 工具视图：由调用方装配（子代理要用不同的权限网关与工具子集）
	Stream       bool         // 是否流式输出
	Compact      bool         // 强制走确定性截断（降级路径与调试开关）
	Control      *runControl  // 取消控制；为 nil 时循环自建一个不可取消的
	// MaxTurns 本次运行的轮次上限；<=0 时用全局的 MaxChatTurns。
	// 子代理用它把探索限制在有限轮内——没有上限的话，一个跑偏的子代理会一直烧下去。
	MaxTurns int

	// 依赖。全部来自 App，但循环只认这里的字段，不直接摸 App。
	Store   *SessionStore
	Diff    *DiffService
	Changes *FileChangeLog
	ReqLog  *llmRequestLog

	// Compactor 摘要式压缩。为 nil 表示本次运行不做摘要压缩——
	// 子代理应当关掉它：多条运行同时去改同一份会话的摘要字段会互相打架。
	Compactor func(ctx context.Context, session *Session, model *Model) (*CompactOutcome, error)

	Recorder runRecorder
}

// runRecorder 事件推送的抽象：主会话推给 wails 前端，子代理推给自己那条通道。
//
// 刻意**不**抽象消息持久化：主会话与子代理都走同一个 SessionStore（子代理有自己的
// 会话文件），多一层抽象只会多一处可能不一致的地方。
type runRecorder interface {
	// Emit 推送聊天事件（chat:event 通道）
	Emit(ev ChatEvent)
	// EmitDiff 推送差异更新。它与聊天事件走的是**不同通道**（diff:update），
	// 所以不能与 Emit 合并成一个方法。
	EmitDiff(files []DiffFile, turn int)
	// FlushStream 启动流式分片推送器；返回的 shutdown 保证可安全调用
	FlushStream() (enqueue func(string), shutdown func())
}

// appRecorder 主会话的 recorder：推给 wails 事件通道
type appRecorder struct {
	app *App
}

// Emit 推送聊天事件
func (r *appRecorder) Emit(ev ChatEvent) {
	if r == nil || r.app == nil {
		return
	}
	r.app.emitChatEvent(ev)
}

// EmitDiff 推送差异更新
func (r *appRecorder) EmitDiff(files []DiffFile, turn int) {
	if r == nil || r.app == nil || r.app.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(r.app.ctx, "diff:update", ChatEvent{
		Type: "diff_update",
		Diff: files,
		Turn: turn,
	})
}

// FlushStream 启动流式分片推送器
func (r *appRecorder) FlushStream() (func(string), func()) {
	if r == nil || r.app == nil {
		return func(string) {}, func() {}
	}
	return r.app.startDeltaFlusher()
}

// newMainAgentRun 组装主会话的一次运行。
//
// 工具视图、权限网关、提问回路都在这里装配——这三样是子代理唯一需要与主会话不同的地方，
// 所以装配点必须留在调用方（子代理会走另一个构造函数，最终交给同一个 runToolLoop）。
func (a *App) newMainAgentRun(run *runControl, session *Session, dir, systemPrompt string,
	model *Model, useStream, compact bool) *agentRun {

	sessionID, runID, planID := "", "", ""
	if run != nil {
		sessionID, runID, planID = run.sessionID, run.runID, run.planID
	}
	if session != nil && session.ID != "" {
		sessionID = session.ID
	}

	toolView := a.toolManager.BuildView(context.Background(), BuildOptions{
		EnabledTools:  session.EnabledTools,
		EnabledSkills: session.EnabledSkills,
		ProjectDir:    dir,
		SessionID:     sessionID,
		Changes:       a.fileChanges,
		Enforcer:      a.newPermissionEnforcer(session, dir),
		// ask_user 的回路：绑定当前会话（阻塞等待用户作答后把结果回填给模型）
		Ask: func(ctx context.Context, req AskRequest) (AskAnswer, error) {
			return a.askUser(ctx, sessionID, req)
		},
		// spawn_agent 的回路：把"派生一个子代理"这件事交回 App。
		// 子代理自己的视图刻意不设这个回调，于是它无法再次派生（禁止嵌套）。
		SpawnAgent: func(ctx context.Context, task string, maxTurns int) (*SubagentResult, error) {
			return a.runSubagent(run, session, dir, model, task, maxTurns)
		},
	})

	return &agentRun{
		RunID:        runID,
		SessionID:    sessionID,
		PlanID:       planID,
		Dir:          dir,
		IsRepo:       dir != "" && a.diffService.IsRepo(dir),
		SystemPrompt: systemPrompt,
		Model:        model,
		ToolView:     toolView,
		Stream:       useStream,
		Compact:      compact,
		Control:      run,
		Store:        a.sessionStore,
		Diff:         a.diffService,
		Changes:      a.fileChanges,
		ReqLog:       a.reqLog,
		Compactor:    a.compactSession,
		Recorder:     &appRecorder{app: a},
	}
}
