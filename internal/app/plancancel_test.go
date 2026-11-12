package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 规划期的取消（PlanPane 的「取消」按钮走这条路）=====
//
// 规划登记的是会话级运行（BeginExclusive(sessionID, "")，不带 planID），
// 因此按 planID 找不到它。用户看到的都是"这个计划还没完成，我要停掉"，
// 不该因为内部把规划与执行分成两种运行就让「取消」在规划期失效。

// TestCancelPlanWhilePlanningStopsSessionRun
// 规划进行中调用 CancelPlan：命中会话级运行（软取消），规划协程自行收尾。
func TestCancelPlanWhilePlanningStopsSessionRun(t *testing.T) {
	app := &App{
		planStore: store.NewPlanStore(t.TempDir()),
		runs:      &agent.RunRegistry{},
	}
	plan := &store.Plan{ID: "p_plan", SessionID: "s1", Status: store.PlanPlanning, Query: "q"}
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}
	// 模拟规划协程的登记方式
	run, err := app.runs.BeginExclusive("s1", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := app.CancelPlan(plan.ID); err != nil {
		app.runs.End(run)
		t.Fatalf("规划中应可取消: %v", err)
	}
	if !run.SoftRequested() {
		app.runs.End(run)
		t.Fatal("应发出软取消信号（由规划协程自行收尾）")
	}
	app.runs.End(run)

	// 计划状态由规划协程负责收尾，CancelPlan 不应越权改动它
	got, _ := app.planStore.Get(plan.ID)
	if got.Status != store.PlanPlanning {
		t.Fatalf("有活跃规划时不该由 CancelPlan 直接改状态，实际 %q", got.Status)
	}
}

// TestCancelPlanRecoversStuckPlanning
// 规划协程已经消失（崩溃/被强杀）而记录还停在 planning：就地收尾为 cancelled。
func TestCancelPlanRecoversStuckPlanning(t *testing.T) {
	app := &App{
		planStore: store.NewPlanStore(t.TempDir()),
		runs:      &agent.RunRegistry{},
	}
	plan := &store.Plan{ID: "p_stuck", SessionID: "s1", Status: store.PlanPlanning, Query: "q"}
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}

	if err := app.CancelPlan(plan.ID); err != nil {
		t.Fatalf("无活跃规划时应能强制收尾: %v", err)
	}
	got, _ := app.planStore.Get(plan.ID)
	if got.Status != store.PlanCancelled {
		t.Fatalf("应置为 cancelled，实际 %q", got.Status)
	}
	if got.Error == "" {
		t.Fatal("应写明原因")
	}
	if got.Query != "q" {
		t.Fatalf("原始请求应保留，实际 %q", got.Query)
	}
}

// TestCancelPlanPlanningCancelledIsNotRetryable
// 取消过的 planning 不该被当成「可重试规划」——用户刚刚就是不要它了。
func TestCancelPlanPlanningCancelledIsNotRetryable(t *testing.T) {
	app, _, _ := newReconcileApp(t)
	plan := &store.Plan{ID: "p_c", SessionID: "s1", Status: store.PlanPlanning, Query: "q"}
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}
	if err := app.CancelPlan(plan.ID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	if _, err := app.RetryPlan(plan.ID, false); err == nil {
		t.Fatal("已取消的规划不应允许重试")
	}
}

// ===== 规划流程在真实模型下的取消路径 =====

// TestChatPlanCancelLeavesCancelledRecord
// 规划被停止：记录落 cancelled（而不是 failed），且带 Cancelled=true 让前端显示"已停止"。
func TestChatPlanCancelLeavesCancelledRecord(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if !once {
			once = true
			close(started)
		}
		<-release // 卡住，等测试发出取消
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(llm.LLMResp{Choices: []llm.LLMChoice{{
			FinishReason: llm.FinishReasonStop,
			Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: `{"needPlan":true,"title":"t","steps":[{"title":"a"}]}`},
		}}})
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &agent.RunRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(10)

	done := make(chan *ChatResult, 1)
	go func() { done <- app.ChatPlan(sess.ID, "帮我升级依赖", false) }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("等待规划请求超时")
	}
	// 规划登记的是会话级运行，按会话取消（前端「停止」按钮正是这么做的）
	if _, changed := app.runs.StopSession(sess.ID, false); !changed {
		t.Fatal("应命中规划期的运行")
	}
	close(release)

	var res *ChatResult
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("等待规划结束超时")
	}
	if !res.Cancelled {
		t.Fatalf("取消后结果应带 Cancelled=true，实际 %+v", res)
	}
	plan, err := app.planStore.Get(res.Plan.ID)
	if err != nil {
		t.Fatalf("取计划失败: %v", err)
	}
	if plan.Status != store.PlanCancelled {
		t.Fatalf("规划取消后记录应为 %q，实际 %q", store.PlanCancelled, plan.Status)
	}
	if plan.Query != "帮我升级依赖" {
		t.Fatalf("原始请求应保留，实际 %q", plan.Query)
	}
}
