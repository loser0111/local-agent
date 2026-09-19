package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ===== 运行取消控制 =====
//
// 一次「运行」指一条完整的执行链：普通聊天的一轮「用户消息 → LLM → 工具循环 → 回复」，
// 或一个计划的执行。两类运行共用这一套取消机制——计划取消从原来自己那套 planCancels
// 收编到这里，不再两套并行（它们对"取消"的语义要求完全一样）。
//
// 两级语义（用户拍板）：
//
//	软取消 soft：关闭 soft 通道，循环在**下一轮开始前**退出。
//	             正在跑的 LLM 请求与工具调用会跑完——与既有的计划取消语义一致。
//	硬取消 hard：先软后硬。额外 cancel 运行 ctx，
//	             在途的 LLM 请求立即断开、子进程被 kill、等待中的提问立即以"取消"结束。
//
// 为什么硬取消要"先软一下"而不是直接 cancel：软信号先发出，循环就有机会走正常收尾
// 路径——把本轮所有工具都补上执行记录、冲刷残余的流式分片。如果直接在循环中途
// 掐断，可能留下 tool_calls 与 tool 消息不配对的断裂序列；那种序列**不会当次报错**，
// 而是在下一次构建请求时被 API 拒绝，排查成本极高。

var (
	// errRunCancelled 运行被用户取消（作为 ctx 的 cancel cause）
	errRunCancelled = errors.New("运行已被用户取消")
	// errRunFinished 运行正常结束，仅用于释放 ctx，不代表取消
	errRunFinished = errors.New("运行已结束")
)

// 取消种类取值
const (
	cancelKindSoft = "soft"
	cancelKindHard = "hard"
)

// runControl 一次运行的取消控制块
type runControl struct {
	runID     string
	sessionID string
	// planID 非空表示这是一次计划执行（普通聊天为空）。
	// 写成独立行而不是行尾注释：行尾注释要参与 gofmt 的列对齐，而插一条整行注释
	// 会打断对齐分组——两种写法在字段增删时都容易把相邻行带歪。
	planID    string
	startedAt time.Time

	ctx    context.Context
	cancel context.CancelCauseFunc

	mu        sync.Mutex
	soft      chan struct{}
	kind      string
	subagents int // 本次运行已派生的子代理数（上限见 subagentMaxTotal）
	// excluded 本次运行期间"不该归因给某会话"的路径，按会话 ID 分组。
	// 唯一的来源是子代理：它跑动期间改的文件会落在父会话的工具执行窗口里，
	// 被 DiffService.NoteActivity 顺手记到父会话账上（见 noteExcludedPaths）。
	excluded map[string][]string
}

// Ctx 返回运行 ctx。LLM 请求、工具执行、权限与提问等待都应基于它，
// 硬取消才能一次性中断所有在途操作。
func (r *runControl) Ctx() context.Context {
	if r == nil || r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

// Kind 当前取消种类；空串表示尚未取消
func (r *runControl) Kind() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.kind
}

// SoftRequested 是否已请求软取消。工具循环每轮开始前调用，返回 true 即退出。
func (r *runControl) SoftRequested() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.soft:
		return true
	default:
		return false
	}
}

// markSoft 请求软取消；返回是否由本次调用首次触发
func (r *runControl) markSoft() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.kind != "" {
		return false
	}
	r.kind = cancelKindSoft
	close(r.soft)
	return true
}

// markHard 请求硬取消：先发软信号，再 cancel ctx
func (r *runControl) markHard() bool {
	r.mu.Lock()
	if r.kind == cancelKindHard {
		r.mu.Unlock()
		return false
	}
	if r.kind == "" {
		close(r.soft) // 软信号先发，给循环一个走正常收尾的机会
	}
	r.kind = cancelKindHard
	r.mu.Unlock()
	// cancel 放在锁外：cancel cause 会触发下游的 Done() 回调，持锁调用有死锁风险
	r.cancel(errRunCancelled)
	return true
}

// claimSubagent 尝试占用一个子代理派生名额；超出上限返回 false。
//
// 上限是硬要求：没有它，一个跑飞的模型可以派生无限个子代理，成本与耗时都不可控。
// 计数放在 runControl 上而不是某个闭包里——计划执行是**按步骤**重建工具视图的，
// 放在闭包里会在每一步重置，"总数上限"就形同虚设。
func (r *runControl) claimSubagent(limit int) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.subagents >= limit {
		return false
	}
	r.subagents++
	return true
}

// noteExcludedPaths 记下"这些路径不该归因给该会话"。
//
// 用途只有一个：子代理改过的文件不能算父会话的改动。归因是按**时间窗口扫全工作区**
// （DiffService.NoteActivity），子代理整个跑动都发生在父会话那次 spawn_agent 调用的
// 窗口之内，所以它的文件会被记到父会话账上——后果是父会话的 diff 面板里出现不是它改的
// 文件，回退父会话那一轮时还会连带把这些文件也回退掉。
//
// 剔除动作必须发生在窗口**结束之后**（那时归因才写入），所以这里只登记，由工具循环取走。
func (r *runControl) noteExcludedPaths(sessionID string, paths []string) {
	if r == nil || sessionID == "" || len(paths) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.excluded == nil {
		r.excluded = map[string][]string{}
	}
	r.excluded[sessionID] = append(r.excluded[sessionID], paths...)
}

// takeExcludedPaths 取出并清空某会话的待剔除路径。
//
// 按会话分组而不是用一个全局队列：子代理与父会话**共用同一个 runControl**
// （取消要级联，见 runSubagent），它自己每轮也会调用本方法；若做成一个共享队列，
// 子代理会先把父会话的那份取走，父会话就永远拿不到，而且这种丢失是静默的。
func (r *runControl) takeExcludedPaths(sessionID string) []string {
	if r == nil || sessionID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	paths := r.excluded[sessionID]
	delete(r.excluded, sessionID)
	return paths
}

// ===== 注册表 =====

// runRegistry 活跃运行的注册表。主键是 runID，另有两个索引：
// sessionID → runID（前端「停止本会话」走这条）、planID → runID（CancelPlan 走这条）。
//
// 所有方法对 nil 接收者安全：测试里会直接构造 &App{planStore: ...} 这种零值 App
// （见 plan_test.go 的 TestCancelPlanRecoversStuckRunning），那一路径必须先能正常工作。
type runRegistry struct {
	mu         sync.Mutex
	runs       map[string]*runControl
	sessionRun map[string]string
	seq        uint64
}

// begin 登记一次新运行。
//
// 对 nil 接收者安全：零值 App（测试里常见）没有注册表，此时返回一个可用但
// 不可被 stop 的控制块，而不是 panic —— 取消是可选能力，不该让无关路径崩掉。
func (g *runRegistry) begin(sessionID, planID string) *runControl {
	ctx, cancel := context.WithCancelCause(context.Background())
	r := &runControl{
		runID:     "run_unregistered",
		sessionID: sessionID,
		planID:    planID,
		startedAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
		soft:      make(chan struct{}),
	}
	if g == nil {
		return r
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.runs == nil {
		g.runs = map[string]*runControl{}
		g.sessionRun = map[string]string{}
	}
	g.seq++
	r.runID = fmt.Sprintf("run_%d_%d", time.Now().UnixMilli(), g.seq)
	g.runs[r.runID] = r
	// sessionID 为空时不建索引：避免多个"无会话"运行互相顶掉索引
	if sessionID != "" {
		g.sessionRun[sessionID] = r.runID
	}
	return r
}

// end 注销运行并释放 ctx。正常结束时也必须调用，否则 ctx 与其派生会一直挂着。
func (g *runRegistry) end(r *runControl) {
	if g == nil || r == nil {
		return
	}
	g.mu.Lock()
	if g.runs != nil {
		delete(g.runs, r.runID)
	}
	if g.sessionRun != nil && g.sessionRun[r.sessionID] == r.runID {
		delete(g.sessionRun, r.sessionID)
	}
	g.mu.Unlock()
	r.cancel(errRunFinished)
}

// bySession 取某会话当前运行；没有则返回 nil
func (g *runRegistry) bySession(sessionID string) *runControl {
	if g == nil || sessionID == "" {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessionRun == nil {
		return nil
	}
	return g.runs[g.sessionRun[sessionID]]
}

// byPlan 取某计划当前运行；没有则返回 nil
func (g *runRegistry) byPlan(planID string) *runControl {
	if g == nil || planID == "" {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.runs {
		if r.planID == planID {
			return r
		}
	}
	return nil
}

// stopSession 停止某会话当前运行。hard=false 为软取消。
// 返回 hit（是否命中一个在跑的运行）与 changed（本次调用是否真正触发了取消，
// 用于把"重复点击"与"首次点击"区分开——前端按钮状态依赖它）。
func (g *runRegistry) stopSession(sessionID string, hard bool) (hit, changed bool) {
	r := g.bySession(sessionID)
	if r == nil {
		return false, false
	}
	return true, g.applyStop(r, hard)
}

// stopPlan 按计划 ID 停止；语义与 stopSession 相同
func (g *runRegistry) stopPlan(planID string, hard bool) (hit, changed bool) {
	r := g.byPlan(planID)
	if r == nil {
		return false, false
	}
	return true, g.applyStop(r, hard)
}

// applyStop 施加取消
func (g *runRegistry) applyStop(r *runControl, hard bool) bool {
	if r == nil {
		return false
	}
	if hard {
		return r.markHard()
	}
	return r.markSoft()
}

// activeCount 当前活跃运行数（测试与诊断用）
func (g *runRegistry) activeCount() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.runs)
}
