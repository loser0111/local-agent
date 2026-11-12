package app

import (
	"sync"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/store"
)

// ===== 惰性对账与事件归属（plan-execution-recovery）=====

// eventRecorder 记录 App 发出的 chat:event，供断言"发了什么"。
type eventRecorder struct {
	mu     sync.Mutex
	events []ChatEvent
}

func (r *eventRecorder) emit(name string, data ...interface{}) {
	if name != "chat:event" || len(data) == 0 {
		return
	}
	ev, ok := data[0].(ChatEvent)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *eventRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

// planUpdates 返回已记录的 plan_update 事件。
func (r *eventRecorder) planUpdates() []ChatEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []ChatEvent
	for _, ev := range r.events {
		if ev.Type == "plan_update" {
			out = append(out, ev)
		}
	}
	return out
}

// newReconcileApp 造一个只有计划存储与运行注册表的 App（不碰模型与网络）。
// 第二个返回值是计划目录，供用例另起一个 PlanStore 验证"已落盘"。
func newReconcileApp(t *testing.T) (*App, *eventRecorder, string) {
	t.Helper()
	dir := t.TempDir()
	app := &App{
		planStore: store.NewPlanStore(dir),
		runs:      &agent.RunRegistry{},
	}
	rec := &eventRecorder{}
	app.emit = rec.emit
	return app, rec, dir
}

// TestGetSessionPlanReconcilesStaleRunning
// 计划停在 running 但没有任何活跃运行 → 读取时就地收尾为 interrupted。
//
// 这正是"界面永久显示执行中、取消又找不到活跃通道"的脱困路径：用户只要打开面板
// （前端会重新拉一次计划），状态就自动收敛，不需要重启应用。
func TestGetSessionPlanReconcilesStaleRunning(t *testing.T) {
	app, rec, dir := newReconcileApp(t)
	plan := &store.Plan{
		ID: "p_stale", SessionID: "s1", Status: store.PlanRunning,
		Steps: []*store.PlanStep{
			{Index: 0, Title: "完成了", Status: store.StepDone, Summary: "ok"},
			{Index: 1, Title: "跑一半", Status: store.StepRunning},
			{Index: 2, Title: "没跑", Status: store.StepPending},
		},
	}
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}

	got := app.GetSessionPlan("s1")
	if got == nil {
		t.Fatal("应返回计划")
	}
	if got.Status != store.PlanInterrupted {
		t.Fatalf("无活跃运行时应收敛为 %q，实际 %q", store.PlanInterrupted, got.Status)
	}
	if got.Steps[1].Status != store.StepInterrupted || got.Steps[1].Error == "" {
		t.Fatalf("运行中的步骤应置 interrupted 并带原因: %+v", got.Steps[1])
	}
	if got.Steps[0].Summary != "ok" || got.Steps[2].Status != store.StepPending {
		t.Fatalf("已完成与未执行的步骤都不该被动: %+v / %+v", got.Steps[0], got.Steps[2])
	}

	// 必须落盘：否则下次读取又回到 running，对账等于白做
	onDisk, err := store.NewPlanStore(dir).Get("p_stale")
	if err != nil {
		t.Fatalf("读取磁盘计划失败: %v", err)
	}
	if onDisk.Status != store.PlanInterrupted {
		t.Fatalf("对账结果未落盘，磁盘上仍是 %q", onDisk.Status)
	}

	// 推了事件，界面才能就地更新
	updates := rec.planUpdates()
	if len(updates) != 1 || updates[0].Plan == nil || updates[0].Plan.Status != store.PlanInterrupted {
		t.Fatalf("应推送一次 plan_update(interrupted)，实际 %d 次", len(updates))
	}

	// 幂等：再读一次不该再推事件（状态已不是 running）
	rec.reset()
	if again := app.GetSessionPlan("s1"); again.Status != store.PlanInterrupted {
		t.Fatalf("状态应保持 %q", store.PlanInterrupted)
	}
	if n := len(rec.planUpdates()); n != 0 {
		t.Fatalf("已收敛的计划不应重复推事件，实际 %d 次", n)
	}
}

// TestGetSessionPlanLeavesActiveRunAlone 真的在跑时不碰它。
func TestGetSessionPlanLeavesActiveRunAlone(t *testing.T) {
	app, rec, _ := newReconcileApp(t)
	plan := &store.Plan{
		ID: "p_live", SessionID: "s1", Status: store.PlanRunning,
		Steps: []*store.PlanStep{{Index: 0, Title: "跑着", Status: store.StepRunning}},
	}
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}
	run, err := app.runs.BeginExclusive("s1", "p_live")
	if err != nil {
		t.Fatalf("登记运行失败: %v", err)
	}
	defer app.runs.End(run)

	got := app.GetSessionPlan("s1")
	if got.Status != store.PlanRunning {
		t.Fatalf("有活跃运行时应保持 %q，实际 %q", store.PlanRunning, got.Status)
	}
	if n := len(rec.planUpdates()); n != 0 {
		t.Fatalf("不该推事件，实际 %d 次", n)
	}
}

// TestGetSessionPlanReconcilesStalePlanning
// 规划中途失去运行 → 转 plan_failed（可重试），而不是永远"规划中"。
func TestGetSessionPlanReconcilesStalePlanning(t *testing.T) {
	app, rec, _ := newReconcileApp(t)
	plan := &store.Plan{
		ID: "p_planning", SessionID: "s1", Status: store.PlanPlanning,
		Query: "帮我升级依赖",
	}
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}

	got := app.GetSessionPlan("s1")
	if got.Status != store.PlanPlanFailed {
		t.Fatalf("失去运行的 planning 应转 %q，实际 %q", store.PlanPlanFailed, got.Status)
	}
	if got.Error == "" {
		t.Fatal("应写明原因")
	}
	if got.Query != "帮我升级依赖" {
		t.Fatalf("原始请求必须保留（重试要用），实际 %q", got.Query)
	}
	if n := len(rec.planUpdates()); n != 1 {
		t.Fatalf("应推送一次事件，实际 %d 次", n)
	}
}

// TestGetSessionPlanPlanningWithActiveRunIsLeftAlone
// 规划真的在进行（会话有活跃运行）时不动它——对账不能把正在等的规划判死。
func TestGetSessionPlanPlanningWithActiveRunIsLeftAlone(t *testing.T) {
	app, rec, _ := newReconcileApp(t)
	if err := app.planStore.Save(&store.Plan{
		ID: "p_live_planning", SessionID: "s1", Status: store.PlanPlanning, Query: "q",
	}); err != nil {
		t.Fatal(err)
	}
	// 规划期的运行登记不带 planID（BeginExclusive(sessionID, "")）
	run, err := app.runs.BeginExclusive("s1", "")
	if err != nil {
		t.Fatalf("登记运行失败: %v", err)
	}
	defer app.runs.End(run)

	if got := app.GetSessionPlan("s1"); got.Status != store.PlanPlanning {
		t.Fatalf("规划进行中应保持 %q，实际 %q", store.PlanPlanning, got.Status)
	}
	if n := len(rec.planUpdates()); n != 0 {
		t.Fatalf("不该推事件，实际 %d 次", n)
	}
}

// TestGetSessionPlanIgnoresTerminalStates 终态与待审核一律不动（对账不能改写合法状态）。
func TestGetSessionPlanIgnoresTerminalStates(t *testing.T) {
	statuses := []string{
		store.PlanAwaitingApproval, store.PlanCompleted, store.PlanFailed,
		store.PlanCancelled, store.PlanInterrupted, store.PlanPlanFailed,
	}
	for _, status := range statuses {
		app, rec, _ := newReconcileApp(t)
		if err := app.planStore.Save(&store.Plan{
			ID: "p_" + status, SessionID: "s1", Status: status,
			Steps: []*store.PlanStep{{Index: 0, Title: "s", Status: store.StepPending}},
		}); err != nil {
			t.Fatal(err)
		}
		got := app.GetSessionPlan("s1")
		if got.Status != status {
			t.Fatalf("%s 不应被对账改写，实际 %q", status, got.Status)
		}
		if n := len(rec.planUpdates()); n != 0 {
			t.Fatalf("%s 不该推事件，实际 %d 次", status, n)
		}
	}
}

// TestGetSessionPlanWithoutPlan 没有计划时返回 nil，且不 panic。
func TestGetSessionPlanWithoutPlan(t *testing.T) {
	app, _, _ := newReconcileApp(t)
	if got := app.GetSessionPlan("no-such-session"); got != nil {
		t.Fatalf("无计划应返回 nil，实际 %+v", got)
	}
}

// TestEmitPlanUpdateCarriesRunID
// 事件要带 RunID：续跑会产生新的 RunID，前端据此把"旧运行的迟到事件"与
// "当前运行的事件"分开（权威顺序仍由 Plan.Revision 决定）。
func TestEmitPlanUpdateCarriesRunID(t *testing.T) {
	app, rec, _ := newReconcileApp(t)
	plan := &store.Plan{
		ID: "p_run", SessionID: "s1", Status: store.PlanRunning,
		Steps: []*store.PlanStep{{Index: 0, Title: "s", Status: store.StepRunning}},
	}
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}

	// 没有活跃运行时：RunID 为空
	app.emitPlanUpdate(plan, 0)
	updates := rec.planUpdates()
	if len(updates) != 1 || updates[0].RunID != "" {
		t.Fatalf("无活跃运行时 RunID 应为空，实际 %q", updates[0].RunID)
	}

	run, err := app.runs.BeginExclusive("s1", "p_run")
	if err != nil {
		t.Fatalf("登记运行失败: %v", err)
	}
	defer app.runs.End(run)

	app.emitPlanUpdate(plan, 0)
	updates = rec.planUpdates()
	if len(updates) != 2 {
		t.Fatalf("应累计 2 次事件，实际 %d", len(updates))
	}
	if updates[1].RunID != run.RunID {
		t.Fatalf("事件应携带活跃运行的 RunID %q，实际 %q", run.RunID, updates[1].RunID)
	}
	if updates[1].SessionID != "s1" {
		t.Fatalf("事件必须带会话归属，实际 %q", updates[1].SessionID)
	}
}
