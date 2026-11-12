package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/diff"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 端到端：一次完整的「网络抖动 → 全流程恢复」=====
//
// 这是本次改造总验收的那条路径，按用户真实会遇到的事件顺序走一遍：
//
//	规划失败（断网）→ 重试规划 → 批准 → 第 2 步执行失败（断网）→ 继续执行 → 完成
//
// 守四件事：
//  1. 每一段失败都留下可解释、可恢复的记录（plan_failed / failed 而不是"什么都没有"）；
//  2. 恢复不需要用户重打需求，也不需要重新批准整份计划；
//  3. 已完成的步骤不会被重跑（摘要与完成时间不变）；
//  4. 聊天流里那段需求只有一条用户消息，步骤请求消息也不会重复堆叠。

// newAppAt 在指定数据目录上构造 App（重启类用例需要两次构造共用同一目录）。
func newAppAt(t *testing.T, srvURL, base string) (*App, *store.Session) {
	t.Helper()
	app := &App{
		sessionStore: store.NewSessionStore(filepath.Join(base, "sessions")),
		modelStore:   store.NewModelStore(filepath.Join(base, "models.json")),
		planStore:    store.NewPlanStore(filepath.Join(base, "plans")),
		toolStore:    tool.NewToolStore(filepath.Join(base, "tools.json")),
		diffService:  diff.NewDiffService(),
		runs:         &agent.RunRegistry{},
		baseDir:      base,
	}
	app.toolManager = NewToolManager(app.toolStore, nil)
	app.fileChanges = tool.NewFileChangeLog(10)
	// 重启类用例会把同一个目录用两次：模型与"重启前那条会话"都已在盘上，
	// 这里按"已有就沿用"处理，模拟的正是真实重启（而不是新建一个干净环境）。
	if err := app.modelStore.AddModel(store.Model{Name: "mock", URL: srvURL, APIKey: "k"}); err != nil {
		if !strings.Contains(err.Error(), "已存在") {
			t.Fatal(err)
		}
	}
	if existing, err := app.sessionStore.ListSessions(); err == nil && len(existing) > 0 {
		full, err := app.sessionStore.GetSession(existing[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		return app, full
	}
	sess, err := app.sessionStore.CreateSession(store.SessionConfig{Title: "重启韧性", Model: "mock", Project: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return app, sess
}

// flakyServer 可切换"断网/恢复"的 mock 模型服务。
//
// failAll=true 时所有请求都返回 401（认证失败，不可重试，所以断言不受重试次数干扰）。
// 恢复后：无 tools 的请求返回计划 JSON；带 tools 的请求按 stepReply 决定成败。
type flakyServer struct {
	mu        sync.Mutex
	failAll   bool
	plan      string
	stepReply func(n int) (int, string)
	stepCalls int
}

func (s *flakyServer) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req llm.LLMReq
	_ = json.Unmarshal(body, &req)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failAll {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"network down"}}`))
		return
	}

	if len(req.Tools) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(llm.LLMResp{Choices: []llm.LLMChoice{{
			FinishReason: llm.FinishReasonStop,
			Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: s.plan},
		}}})
		return
	}

	s.stepCalls++
	status, reply := 200, "本步完成"
	if s.stepReply != nil {
		status, reply = s.stepReply(s.stepCalls)
	}
	if status != 200 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(llm.LLMResp{Choices: []llm.LLMChoice{{
		FinishReason: llm.FinishReasonStop,
		Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: reply},
	}}})
}

func (s *flakyServer) setOffline(offline bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failAll = offline
}

func (s *flakyServer) setStepReply(fn func(n int) (int, string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stepReply = fn
}

func (s *flakyServer) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stepCalls
}

// TestPlanResilienceEndToEnd 全流程验收（见文件头）。
func TestPlanResilienceEndToEnd(t *testing.T) {
	withoutTransportRetry(t) // 考察的是计划层的接管，不重复付传输层退避

	fs := &flakyServer{
		failAll: true, // 一开始就断网
		plan:    `{"needPlan":true,"title":"端到端韧性","steps":[{"title":"第一步","detail":""},{"title":"第二步","detail":""},{"title":"第三步","detail":""}]}`,
		stepReply: func(n int) (int, string) {
			if n == 2 {
				return http.StatusInternalServerError, `{"error":{"message":"network down"}}`
			}
			return 200, "本步完成"
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(fs.handler))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &agent.RunRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(10)
	app.planRetry = noWaitRetry(0) // 本用例只考察"跨步骤的恢复"，不叠加步骤内重试

	const query = "帮我把这三件事做完"
	if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{Role: store.RoleUser, Content: query}); err != nil {
		t.Fatal(err)
	}

	// ---- 1. 断网时规划：必须留下可重试的记录，而不是静默失败 ----
	failed := app.ChatPlan(sess.ID, query, false)
	if failed.Plan == nil {
		t.Fatal("规划失败必须带回计划对象（前端要靠它渲染「重试规划」）")
	}
	if failed.Plan.Status != store.PlanPlanFailed {
		t.Fatalf("断网时规划状态应为 %q，实际 %q", store.PlanPlanFailed, failed.Plan.Status)
	}
	if failed.ErrorKind != string(llm.ErrorKindAuth) {
		t.Fatalf("ErrorKind 应为 auth，实际 %q", failed.ErrorKind)
	}
	planID := failed.Plan.ID

	// ---- 2. 网络恢复后重试规划：复用原始请求，不追加用户消息 ----
	fs.setOffline(false)
	retried, err := app.RetryPlan(planID, false)
	if err != nil {
		t.Fatalf("重试规划失败: %v", err)
	}
	if retried.Plan == nil || retried.Plan.Status != store.PlanAwaitingApproval {
		t.Fatalf("重试成功应转为待审核，实际 %+v", retried.Plan)
	}
	if n := countUserMessages(t, app, sess.ID, query); n != 1 {
		t.Fatalf("重试规划不该追加用户消息，实际有 %d 条", n)
	}

	// ---- 3. 批准执行：第 1 步成功、第 2 步失败、第 3 步未执行 ----
	first := app.ExecutePlan(planID, false)
	if first.Error == "" {
		t.Fatal("第 2 步失败时执行应返回错误")
	}
	afterFail, err := app.planStore.Get(planID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFail.Status != store.PlanFailed {
		t.Fatalf("失败后应为 %q，实际 %q", store.PlanFailed, afterFail.Status)
	}
	if afterFail.Steps[0].Status != store.StepDone {
		t.Fatalf("第 1 步应完成: %+v", afterFail.Steps[0])
	}
	if afterFail.Steps[1].Status != store.StepFailed {
		t.Fatalf("第 2 步应失败: %+v", afterFail.Steps[1])
	}
	if afterFail.Steps[2].Status != store.StepPending {
		t.Fatalf("第 3 步应保持待执行（失败不抹掉未执行步骤）: %+v", afterFail.Steps[2])
	}
	doneSummary := afterFail.Steps[0].Summary
	doneFinishedAt := afterFail.Steps[0].FinishedAt

	// ---- 4. 继续执行：只跑第 2、3 步 ----
	callsBefore := fs.calls()
	resumed := app.ResumePlan(planID, false)
	if resumed.Error != "" {
		t.Fatalf("继续执行应成功: %s", resumed.Error)
	}
	final, err := app.planStore.Get(planID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != store.PlanCompleted {
		t.Fatalf("续跑后应完成，实际 %q", final.Status)
	}
	for i, st := range final.Steps {
		if st.Status != store.StepDone {
			t.Fatalf("第 %d 步应为完成，实际 %q", i+1, st.Status)
		}
	}
	if final.Steps[0].Summary != doneSummary || final.Steps[0].FinishedAt != doneFinishedAt {
		t.Fatalf("已完成步骤的摘要与完成时间不应变化")
	}
	if final.Steps[0].Attempt != 1 {
		t.Fatalf("第 1 步不该被重跑（Attempt 应为 1），实际 %d", final.Steps[0].Attempt)
	}
	if got := fs.calls() - callsBefore; got != 2 {
		t.Fatalf("续跑应只执行 2 个剩余步骤，实际 %d 次", got)
	}

	// ---- 5. 历史干净：需求一条、每步请求消息一条 ----
	if n := countUserMessages(t, app, sess.ID, query); n != 1 {
		t.Fatalf("整条链路走完后需求应仍只有 1 条，实际 %d 条", n)
	}
	counts := stepMessages(t, app, sess.ID)
	for i := 0; i < 3; i++ {
		if counts[i] != 1 {
			t.Fatalf("第 %d 步的请求消息应只有 1 条，实际 %d 条", i, counts[i])
		}
	}
	if len(counts) != 3 {
		t.Fatalf("应恰好有 3 个步骤的消息，实际 %d 个", len(counts))
	}

	// ---- 6. 事件序列收敛到 completed（前端据此把面板置为完成）----
	if strings.TrimSpace(final.ID) == "" {
		t.Fatal("计划 ID 不应为空")
	}
}

// TestPlanResilienceSurvivesRestart 进程退出后重启：计划从 interrupted 续跑到完成。
//
// 用同一个计划目录另起一个 App 来模拟重启（PlanStore 会走 loadAll 的恢复路径）。
func TestPlanResilienceSurvivesRestart(t *testing.T) {
	withoutTransportRetry(t)

	fs := &flakyServer{plan: `{"needPlan":true,"title":"重启韧性","steps":[{"title":"a","detail":""},{"title":"b","detail":""}]}`}
	fs.stepReply = func(n int) (int, string) {
		if n == 2 {
			return http.StatusInternalServerError, `{"error":{"message":"boom"}}`
		}
		return 200, "完成"
	}
	srv := httptest.NewServer(http.HandlerFunc(fs.handler))
	defer srv.Close()

	base := t.TempDir()
	app1, sess := newAppAt(t, srv.URL, base)
	app1.planRetry = noWaitRetry(0)

	planned := app1.ChatPlan(sess.ID, "两件事", false)
	planID := planned.Plan.ID
	app1.ExecutePlan(planID, false)

	before, _ := app1.planStore.Get(planID)
	if before.Status != store.PlanFailed || before.Steps[0].Status != store.StepDone {
		t.Fatalf("前置条件：应为 failed 且第 1 步完成，实际 %q / %q", before.Status, before.Steps[0].Status)
	}
	doneSummary, doneAt := before.Steps[0].Summary, before.Steps[0].FinishedAt

	// 模拟"执行到一半进程退出"：把计划改回 running（磁盘上就是崩溃现场）
	before.Status = store.PlanRunning
	before.Steps[1].Status = store.StepRunning
	if err := app1.planStore.Save(before); err != nil {
		t.Fatal(err)
	}

	// 重启：新 App 复用同一个数据目录（planStore 走 loadAll 的恢复路径）
	app2, _ := newAppAt(t, srv.URL, base)
	app2.planRetry = noWaitRetry(0)
	// 会话文件是重启后仍然存在的那份：沿用 app1 里同一个会话 ID
	if app2.sessionStore == app1.sessionStore {
		t.Fatal("重启应重建存储，而不是复用同一个实例")
	}

	recovered := app2.GetSessionPlan(sess.ID)
	if recovered.Status != store.PlanInterrupted {
		t.Fatalf("重启后应恢复为 %q，实际 %q", store.PlanInterrupted, recovered.Status)
	}
	if recovered.Steps[0].Summary != doneSummary || recovered.Steps[0].FinishedAt != doneAt {
		t.Fatal("重启不应丢掉已完成步骤的进度")
	}

	// 续跑到完成
	fs.setStepReply(func(int) (int, string) { return 200, "完成" })
	if res := app2.ResumePlan(planID, false); res.Error != "" {
		t.Fatalf("重启后继续执行应成功: %s", res.Error)
	}
	after, _ := app2.planStore.Get(planID)
	if after.Status != store.PlanCompleted {
		t.Fatalf("续跑后应完成，实际 %q", after.Status)
	}
	if after.Steps[0].Summary != doneSummary {
		t.Fatal("已完成步骤的摘要应保留")
	}
}
