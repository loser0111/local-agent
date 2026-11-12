package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 断点续跑（plan-execution-recovery）=====
//
// 这一组守的是"网络抖一下之后还能接着跑"：已完成步骤不重跑、未执行步骤保持 pending、
// 续跑不需要重新批准、且步骤请求消息不会因为重跑而重复堆叠。
//
// 失败一律用 400 而不是 500：400 在 llm 重试层判定为不可重试，一次请求就结束。
// 用 500 会让每个失败用例白等约 2.6s 的退避（那些退避是生产行为，不该在单测里重复付）。

// scriptServer 按脚本返回的 mock 模型服务。
//
//   - 无 tools 的请求 = 规划器：返回 planContent 指定的计划 JSON；
//   - 带 tools 的请求 = 步骤执行：第 n 次调用交给 reply(n) 决定。
type scriptServer struct {
	mu          sync.Mutex
	planContent string
	reply       func(n int) (status int, body string)
	calls       int
}

func (s *scriptServer) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req llm.LLMReq
	_ = json.Unmarshal(body, &req)

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(req.Tools) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(llm.LLMResp{Choices: []llm.LLMChoice{{
			FinishReason: llm.FinishReasonStop,
			Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: s.planContent},
		}}})
		return
	}

	s.calls++
	status, respBody := 200, ""
	if s.reply != nil {
		status, respBody = s.reply(s.calls)
	}
	if status != 200 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(llm.LLMResp{Choices: []llm.LLMChoice{{
		FinishReason: llm.FinishReasonStop,
		Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: respBody},
	}}})
}

func (s *scriptServer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// newScriptedFixture 起服务并造 App+会话；steps 是规划器产出的步骤数。
func newScriptedFixture(t *testing.T, ss *scriptServer, steps int) (*App, *store.Session) {
	t.Helper()
	var titles []string
	for i := 1; i <= steps; i++ {
		titles = append(titles, `{"title":"步骤`+string(rune('0'+i))+`","detail":""}`)
	}
	ss.planContent = `{"needPlan":true,"title":"续跑测试","steps":[` + strings.Join(titles, ",") + `]}`

	srv := httptest.NewServer(http.HandlerFunc(ss.handler))
	t.Cleanup(srv.Close)

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &agent.RunRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(10)
	if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{Role: store.RoleUser, Content: "三步任务"}); err != nil {
		t.Fatal(err)
	}
	return app, sess
}

// stepMessages 返回会话里属于某个计划步骤的请求消息条数（按步骤索引分组）。
func stepMessages(t *testing.T, app *App, sessID string) map[int]int {
	t.Helper()
	s, err := app.sessionStore.GetSession(sessID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]int{}
	for _, m := range s.Messages {
		if m.Role == store.RoleUser && m.Attempt > 0 {
			out[m.PlanStepIndex]++
		}
	}
	return out
}

// TestResumePlanRunsOnlyRemainingSteps 续跑只跑剩余步骤，已完成步骤的进度一字不动。
func TestResumePlanRunsOnlyRemainingSteps(t *testing.T) {
	ss := &scriptServer{}
	app, sess := newScriptedFixture(t, ss, 3)
	// 第 1 次（步骤 1）成功，第 2 次（步骤 2）失败，之后恢复
	ss.reply = func(n int) (int, string) {
		if n == 2 {
			return http.StatusBadRequest, `{"error":{"message":"bad request"}}`
		}
		return 200, "本步完成"
	}

	planned := app.ChatPlan(sess.ID, "三步任务", false)
	if planned.Plan == nil || planned.Plan.Status != store.PlanAwaitingApproval {
		t.Fatalf("前置条件不成立: %+v", planned)
	}
	planID := planned.Plan.ID

	// 首次执行：第 1 步成功、第 2 步失败、第 3 步未执行
	first := app.ExecutePlan(planID, false)
	if first.Error == "" {
		t.Fatal("第 2 步失败时执行应返回错误")
	}
	failed, err := app.planStore.Get(planID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != store.PlanFailed {
		t.Fatalf("失败后应为 %q，实际 %q", store.PlanFailed, failed.Status)
	}
	if failed.Steps[0].Status != store.StepDone {
		t.Fatalf("第 1 步应已完成: %+v", failed.Steps[0])
	}
	if failed.Steps[1].Status != store.StepFailed {
		t.Fatalf("第 2 步应失败: %+v", failed.Steps[1])
	}
	if failed.Steps[2].Status != store.StepPending {
		t.Fatalf("第 3 步应保持待执行（失败不抹掉未执行步骤）: %+v", failed.Steps[2])
	}
	// 记下第 1 步的成果，续跑后必须一字不动
	doneSummary := failed.Steps[0].Summary
	doneFinishedAt := failed.Steps[0].FinishedAt
	callsBefore := ss.callCount()

	// 续跑（不需要重新批准）
	second := app.ResumePlan(planID, false)
	if second.Error != "" {
		t.Fatalf("续跑应成功: %s", second.Error)
	}
	final, _ := app.planStore.Get(planID)
	if final.Status != store.PlanCompleted {
		t.Fatalf("续跑后应完成，实际 %q", final.Status)
	}
	if final.Steps[0].Summary != doneSummary || final.Steps[0].FinishedAt != doneFinishedAt {
		t.Fatalf("已完成步骤的摘要与完成时间不应变化: 前 %q/%d，后 %q/%d",
			doneSummary, doneFinishedAt, final.Steps[0].Summary, final.Steps[0].FinishedAt)
	}
	if final.Steps[0].Attempt != 1 {
		t.Fatalf("已完成步骤不应被重跑（Attempt 应为 1），实际 %d", final.Steps[0].Attempt)
	}
	// 只多跑了第 2、3 步
	if got := ss.callCount() - callsBefore; got != 2 {
		t.Fatalf("续跑应只执行 2 个剩余步骤，实际调用了 %d 次", got)
	}
}

// TestResumePlanRejectsIneligibleStates 只有"没跑完"的状态能续跑。
func TestResumePlanRejectsIneligibleStates(t *testing.T) {
	ss := &scriptServer{}
	app, _ := newScriptedFixture(t, ss, 2)

	cases := []struct {
		status string
		want   string // 错误信息里应出现的关键词
	}{
		{store.PlanRunning, "正在执行"},
		{store.PlanCompleted, "已完成"},
		{store.PlanPlanning, "重试规划"},
		{store.PlanPlanFailed, "重试规划"},
		{store.PlanAwaitingApproval, "不允许继续执行"},
	}
	for _, c := range cases {
		p := &store.Plan{
			ID: "p_" + c.status, SessionID: "s_x", Status: c.status,
			Steps: []*store.PlanStep{{Index: 0, Title: "a", Status: store.StepPending}},
		}
		if err := app.planStore.Save(p); err != nil {
			t.Fatal(err)
		}
		res := app.ResumePlan(p.ID, false)
		if res.Error == "" {
			t.Fatalf("%s 状态不应允许续跑", c.status)
		}
		if !strings.Contains(res.Error, c.want) {
			t.Fatalf("%s 的错误信息应包含 %q，实际 %q", c.status, c.want, res.Error)
		}
	}
}

// TestExecutePlanOnlyAcceptsAwaitingApproval 执行入口收窄：未完成的计划要走续跑。
func TestExecutePlanOnlyAcceptsAwaitingApproval(t *testing.T) {
	ss := &scriptServer{}
	app, _ := newScriptedFixture(t, ss, 2)

	for _, status := range []string{store.PlanFailed, store.PlanInterrupted, store.PlanCancelled} {
		p := &store.Plan{
			ID: "p_" + status, SessionID: "s_x", Status: status,
			Steps: []*store.PlanStep{{Index: 0, Title: "a", Status: store.StepFailed}},
		}
		if err := app.planStore.Save(p); err != nil {
			t.Fatal(err)
		}
		res := app.ExecutePlan(p.ID, false)
		if res.Error == "" {
			t.Fatalf("%s 状态不应允许 ExecutePlan", status)
		}
		if !strings.Contains(res.Error, "继续执行") {
			t.Fatalf("错误信息应指引用户改用「继续执行」，实际 %q", res.Error)
		}
	}
}

// TestResumePlanKeepsStepRequestMessageIdempotent
// 同一步骤无论跑几次，会话里只有一条步骤请求消息。
func TestResumePlanKeepsStepRequestMessageIdempotent(t *testing.T) {
	ss := &scriptServer{}
	app, sess := newScriptedFixture(t, ss, 2)
	// 步骤 1 每次都失败 → 失败 + 两次续跑 = 该步骤共 3 次尝试
	ss.reply = func(n int) (int, string) {
		return http.StatusBadRequest, `{"error":{"message":"bad request"}}`
	}

	planned := app.ChatPlan(sess.ID, "三步任务", false)
	planID := planned.Plan.ID
	app.ExecutePlan(planID, false)
	app.ResumePlan(planID, false)
	app.ResumePlan(planID, false)

	counts := stepMessages(t, app, sess.ID)
	if counts[0] != 1 {
		t.Fatalf("第 0 步的请求消息应始终只有 1 条，实际 %d 条（计划步骤索引 0 最容易踩 omitempty 的坑）", counts[0])
	}
	if _, ok := counts[1]; ok {
		t.Fatalf("第 1 步从未开始，不该有请求消息，实际 %d 条", counts[1])
	}

	plan, _ := app.planStore.Get(planID)
	if plan.Steps[0].Attempt != 3 {
		t.Fatalf("三次尝试后 Attempt 应为 3，实际 %d", plan.Steps[0].Attempt)
	}
	if plan.Steps[0].ContextMark == 0 {
		t.Fatal("ContextMark 应记录本次尝试的消息起点")
	}
}

// TestResumePlanRecoversInterruptedPlan 中断的计划（进程退出留下的）可以接着跑。
func TestResumePlanRecoversInterruptedPlan(t *testing.T) {
	ss := &scriptServer{reply: func(int) (int, string) { return 200, "完成" }}
	app, sess := newScriptedFixture(t, ss, 2)

	planned := app.ChatPlan(sess.ID, "三步任务", false)
	planID := planned.Plan.ID
	// 模拟"执行到第 2 步时进程退出"：第 1 步已完成，第 2 步停在 running
	plan, _ := app.planStore.Get(planID)
	plan.Status = store.PlanInterrupted
	plan.Steps[0].Status = store.StepDone
	plan.Steps[0].Summary = "第一步的成果"
	plan.Steps[1].Status = store.StepInterrupted
	plan.Steps[1].Error = "应用重启，执行中断"
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}
	callsBefore := ss.callCount()

	res := app.ResumePlan(planID, false)
	if res.Error != "" {
		t.Fatalf("中断的计划应可续跑: %s", res.Error)
	}
	final, _ := app.planStore.Get(planID)
	if final.Status != store.PlanCompleted {
		t.Fatalf("续跑后应完成，实际 %q", final.Status)
	}
	if final.Steps[0].Summary != "第一步的成果" {
		t.Fatalf("已完成步骤的摘要应保留，实际 %q", final.Steps[0].Summary)
	}
	if got := ss.callCount() - callsBefore; got != 1 {
		t.Fatalf("应只重跑第 2 步，实际调用 %d 次", got)
	}
}

// TestMarkPlanInterruptedKeepsPendingSteps 兜底收尾：只动运行中的步骤，不清空待执行步骤。
func TestMarkPlanInterruptedKeepsPendingSteps(t *testing.T) {
	ss := &scriptServer{}
	app, _ := newScriptedFixture(t, ss, 3)
	plan := &store.Plan{
		ID: "p_int", SessionID: "s1", Status: store.PlanRunning,
		Steps: []*store.PlanStep{
			{Index: 0, Title: "done", Status: store.StepDone, Summary: "ok"},
			{Index: 1, Title: "跑一半", Status: store.StepRunning},
			{Index: 2, Title: "没跑", Status: store.StepPending},
		},
	}
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}

	app.markPlanInterrupted(plan, "执行中断（未正常结束）")

	got, _ := app.planStore.Get("p_int")
	if got.Status != store.PlanInterrupted {
		t.Fatalf("应置为 %q，实际 %q", store.PlanInterrupted, got.Status)
	}
	if got.Steps[1].Status != store.StepInterrupted || got.Steps[1].Error == "" {
		t.Fatalf("运行中的步骤应置 interrupted 并带原因: %+v", got.Steps[1])
	}
	if got.Steps[1].FinishedAt == 0 {
		t.Fatal("中断的步骤应写入完成时间")
	}
	if got.Steps[2].Status != store.StepPending {
		t.Fatalf("未执行的步骤不应被改动（还能跑）: %+v", got.Steps[2])
	}
	if got.Steps[0].Summary != "ok" {
		t.Fatalf("已完成步骤的摘要不应被动: %+v", got.Steps[0])
	}
}

// TestResumePlanClearsStaleErrors 续跑成功后必须清掉上次的失败原因：
// 否则界面上会同时显示"失败"与"已完成"两个互相矛盾的结论。
func TestResumePlanClearsStaleErrors(t *testing.T) {
	ss := &scriptServer{reply: func(int) (int, string) { return 200, "完成" }}
	app, sess := newScriptedFixture(t, ss, 1)

	planned := app.ChatPlan(sess.ID, "三步任务", false)
	planID := planned.Plan.ID
	plan, _ := app.planStore.Get(planID)
	plan.Status = store.PlanFailed
	plan.Error = "步骤执行失败: 上一次的错误"
	plan.Steps[0].Status = store.StepFailed
	plan.Steps[0].Error = "上一次的错误"
	if err := app.planStore.Save(plan); err != nil {
		t.Fatal(err)
	}

	if res := app.ResumePlan(planID, false); res.Error != "" {
		t.Fatalf("续跑应成功: %s", res.Error)
	}
	final, _ := app.planStore.Get(planID)
	if final.Error != "" {
		t.Fatalf("完成后计划级错误应清空，实际 %q", final.Error)
	}
	if final.Steps[0].Error != "" {
		t.Fatalf("成功步骤不应残留失败原因，实际 %q", final.Steps[0].Error)
	}
}
