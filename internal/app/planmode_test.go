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

// ===== 规划流程（session-plan-mode）=====
//
// 这一组用例守的是同一件事：**规划失败不再是静默的**——
// 请求本身要留痕（planning 记录 + Plan.Query），失败要可解释（plan_failed + Error），
// 而且能一键重试（RetryPlan），不必让用户把需求重打一遍。

// plannerServer 造一个可切换"规划器失败/成功/无需计划"的 mock 模型服务。
//
// planningObserved 在每次规划请求到达时记录"当时磁盘上这条会话的计划状态"——
// 这是"先落 planning 记录、再调规划器"这条顺序唯一能直接观测到的证据。
type plannerServer struct {
	mu       sync.Mutex
	failPlan bool     // true: 规划请求返回 401
	noPlan   bool     // true: 规划器返回 needPlan=false
	planJSON string   // 规划器返回的计划 JSON
	observed []string // 每次规划请求到达时的计划状态
	calls    int      // 规划请求次数
}

func (s *plannerServer) handler(t *testing.T, app **App, sessID *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req llm.LLMReq
		_ = json.Unmarshal(body, &req)

		s.mu.Lock()
		defer s.mu.Unlock()

		// 无 tools = 规划器请求（摘要器也走这条，但本组用例不触发摘要）。
		// 观测与计数放在失败分支**之前**：否则规划失败时观察不到"调用当时的状态"，
		// 而"先落 planning 记录、再调规划器"这条顺序正是靠它证明的。
		isPlanner := len(req.Tools) == 0
		if isPlanner {
			s.calls++
			if *app != nil && *sessID != "" {
				st := ""
				if p := (*app).planStore.GetBySession(*sessID); p != nil {
					st = p.Status
				}
				s.observed = append(s.observed, st)
			}
		}

		// failPlan 对**所有**请求回 401：规划请求与普通对话请求都要失败，
		// 否则"普通对话失败仍返回 Go error"那条用例会因为带 tools 的分支回了正常回复而失去意义。
		if s.failPlan {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
			return
		}

		if isPlanner {
			content := s.planJSON
			if s.noPlan {
				content = `{"needPlan":false,"title":"","steps":[]}`
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(llm.LLMResp{Choices: []llm.LLMChoice{{
				FinishReason: llm.FinishReasonStop,
				Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: content},
			}}})
			return
		}

		// 带 tools = 普通对话请求（needPlan=false 的直答路径）
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(llm.LLMResp{Choices: []llm.LLMChoice{{
			FinishReason: llm.FinishReasonStop,
			Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: "这是普通回复"},
		}}})
	}
}

func (s *plannerServer) observedStatuses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.observed))
	copy(out, s.observed)
	return out
}

// newPlannerFixture 起服务、造 App 与会话，并给会话补一条用户消息（模仿前端先落库）。
func newPlannerFixture(t *testing.T, ps *plannerServer) (*App, *store.Session) {
	t.Helper()
	var (
		appRef  *App
		sessRef string
	)
	srv := httptest.NewServer(ps.handler(t, &appRef, &sessRef))
	t.Cleanup(srv.Close)

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &agent.RunRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(10)
	appRef, sessRef = app, sess.ID
	return app, sess
}

// appendUser 模仿前端：用户消息先落库，再发起规划。
func appendUser(t *testing.T, app *App, sessID, text string) {
	t.Helper()
	if _, err := app.sessionStore.AppendMessage(sessID, store.Message{Role: store.RoleUser, Content: text}); err != nil {
		t.Fatal(err)
	}
}

// countUserMessages 数会话里内容等于 text 的 user 消息条数。
func countUserMessages(t *testing.T, app *App, sessID, text string) int {
	t.Helper()
	s, err := app.sessionStore.GetSession(sessID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range s.Messages {
		if m.Role == store.RoleUser && m.Content == text {
			n++
		}
	}
	return n
}

func planJSON2Steps() string {
	return `{"needPlan":true,"title":"升级依赖","steps":[{"title":"检查依赖","detail":"读 package.json"},{"title":"跑测试","detail":""}]}`
}

// TestChatPlanPersistsPlanningRecordBeforeCallingPlanner
// 规划请求发出时，磁盘上必须已经有一条 planning 记录。
//
// 这是"意图留痕"的最小证据：规划失败后能靠它重试，全靠它在调用之前就存在。
func TestChatPlanPersistsPlanningRecordBeforeCallingPlanner(t *testing.T) {
	ps := &plannerServer{failPlan: true}
	app, sess := newPlannerFixture(t, ps)
	appendUser(t, app, sess.ID, "帮我升级依赖并跑通测试")

	res := app.ChatPlan(sess.ID, "帮我升级依赖并跑通测试", false)

	if got := ps.observedStatuses(); len(got) != 1 || got[0] != store.PlanPlanning {
		t.Fatalf("规划请求到达时应已是 %q，实际 %v", store.PlanPlanning, got)
	}
	if res.Plan == nil {
		t.Fatal("规划失败必须把计划对象带回来（前端要靠它渲染重试按钮）")
	}
	if res.Plan.Status != store.PlanPlanFailed {
		t.Fatalf("失败后状态应为 %q，实际 %q", store.PlanPlanFailed, res.Plan.Status)
	}
	if res.Plan.Query != "帮我升级依赖并跑通测试" {
		t.Fatalf("原始请求必须落盘，实际 %q", res.Plan.Query)
	}
	if !strings.Contains(res.Plan.Error, "规划失败") {
		t.Fatalf("失败原因应可读，实际 %q", res.Plan.Error)
	}
	if res.ErrorKind != string(llm.ErrorKindAuth) {
		t.Fatalf("ErrorKind 应为 auth，实际 %q", res.ErrorKind)
	}

	// 落盘：重启后仍然能看到这条失败记录
	reloaded, err := app.planStore.Get(res.Plan.ID)
	if err != nil {
		t.Fatalf("计划未落盘: %v", err)
	}
	if reloaded.Status != store.PlanPlanFailed || reloaded.Query == "" {
		t.Fatalf("磁盘上的记录不完整: %+v", reloaded)
	}
}

// TestChatPlanReusesSingleUnfinishedRecord 同一会话只留一条未完成任务记录。
func TestChatPlanReusesSingleUnfinishedRecord(t *testing.T) {
	ps := &plannerServer{failPlan: true}
	app, sess := newPlannerFixture(t, ps)

	appendUser(t, app, sess.ID, "第一次需求")
	res1 := app.ChatPlan(sess.ID, "第一次需求", false)
	appendUser(t, app, sess.ID, "第二次需求")
	res2 := app.ChatPlan(sess.ID, "第二次需求", false)

	if res1.Plan == nil || res2.Plan == nil {
		t.Fatal("两次都应有计划对象")
	}
	if res1.Plan.ID != res2.Plan.ID {
		t.Fatalf("未完成的记录应被复用，实际有两条：%s / %s", res1.Plan.ID, res2.Plan.ID)
	}
	list := app.planStore.ListBySession(sess.ID)
	if len(list) != 1 {
		t.Fatalf("会话里应只有 1 条计划，实际 %d 条", len(list))
	}
	if list[0].Query != "第二次需求" {
		t.Fatalf("复用时应把 Query 覆盖为最新请求，实际 %q", list[0].Query)
	}
}

// TestChatPlanDiscardsRecordWhenPlannerSaysNoPlan 平凡请求：丢弃占位记录、走普通直答。
func TestChatPlanDiscardsRecordWhenPlannerSaysNoPlan(t *testing.T) {
	ps := &plannerServer{noPlan: true}
	app, sess := newPlannerFixture(t, ps)
	appendUser(t, app, sess.ID, "现在几点了")

	res := app.ChatPlan(sess.ID, "现在几点了", false)

	if res.Error != "" {
		t.Fatalf("无需计划时应正常直答，实际报错 %q", res.Error)
	}
	if !strings.Contains(res.Reply, "普通回复") {
		t.Fatalf("应走普通工具循环拿到回复，实际 %q", res.Reply)
	}
	if p := app.planStore.GetBySession(sess.ID); p != nil {
		t.Fatalf("判定无需计划时应丢弃占位记录，实际留下 %+v", p)
	}
}

// TestRetryPlanReusesQueryWithoutDuplicatingMessage
// 重试规划：复用落盘的 Query，聊天流里那段需求仍然只有一条。
func TestRetryPlanReusesQueryWithoutDuplicatingMessage(t *testing.T) {
	ps := &plannerServer{failPlan: true, planJSON: planJSON2Steps()}
	app, sess := newPlannerFixture(t, ps)
	const query = "帮我升级依赖并跑通测试"
	appendUser(t, app, sess.ID, query)

	failed := app.ChatPlan(sess.ID, query, false)
	if failed.Plan == nil || failed.Plan.Status != store.PlanPlanFailed {
		t.Fatalf("前置条件不成立：%+v", failed.Plan)
	}
	planID := failed.Plan.ID

	// 网络恢复
	ps.mu.Lock()
	ps.failPlan = false
	ps.mu.Unlock()

	res, err := app.RetryPlan(planID, false)
	if err != nil {
		t.Fatalf("重试规划失败: %v", err)
	}
	if res.Plan == nil || res.Plan.Status != store.PlanAwaitingApproval {
		t.Fatalf("重试成功应转为 %q，实际 %+v", store.PlanAwaitingApproval, res.Plan)
	}
	if len(res.Plan.Steps) != 2 {
		t.Fatalf("应产出 2 个步骤，实际 %d", len(res.Plan.Steps))
	}
	if res.Plan.ID != planID {
		t.Fatalf("重试应写回同一条记录，实际 %s != %s", res.Plan.ID, planID)
	}
	if n := countUserMessages(t, app, sess.ID, query); n != 1 {
		t.Fatalf("重试不应再追加用户消息，实际有 %d 条", n)
	}
	if list := app.planStore.ListBySession(sess.ID); len(list) != 1 {
		t.Fatalf("会话里应只有 1 条计划，实际 %d 条", len(list))
	}
}

// TestRetryPlanSecondFailureKeepsSameRecord 重试再次失败：仍是同一条记录、原因更新。
func TestRetryPlanSecondFailureKeepsSameRecord(t *testing.T) {
	ps := &plannerServer{failPlan: true}
	app, sess := newPlannerFixture(t, ps)
	appendUser(t, app, sess.ID, "需求")

	first := app.ChatPlan(sess.ID, "需求", false)
	second, err := app.RetryPlan(first.Plan.ID, false)
	if err != nil {
		t.Fatalf("重试不应返回 Go error（失败信息在结果里）: %v", err)
	}
	if second.Plan == nil || second.Plan.Status != store.PlanPlanFailed {
		t.Fatalf("再次失败应仍为 %q，实际 %+v", store.PlanPlanFailed, second.Plan)
	}
	if second.Plan.ID != first.Plan.ID {
		t.Fatalf("应写回同一条记录")
	}
	if second.Plan.Error == "" {
		t.Fatal("失败原因不能为空")
	}
	if ps.calls != 2 {
		t.Fatalf("应调用规划器 2 次，实际 %d 次", ps.calls)
	}
}

// TestRetryPlanRejectsNonFailedStatus 只有规划失败才可重试。
func TestRetryPlanRejectsNonFailedStatus(t *testing.T) {
	ps := &plannerServer{planJSON: planJSON2Steps()}
	app, sess := newPlannerFixture(t, ps)
	appendUser(t, app, sess.ID, "需求")

	ok := app.ChatPlan(sess.ID, "需求", false)
	if ok.Plan == nil || ok.Plan.Status != store.PlanAwaitingApproval {
		t.Fatalf("前置条件不成立：%+v", ok.Plan)
	}
	if _, err := app.RetryPlan(ok.Plan.ID, false); err == nil {
		t.Fatal("待审核的计划不该允许重试规划")
	}
}

// TestRetryPlanRejectsConcurrentRetry 会话被占用时拒绝重试（避免两条规划同写一条记录）。
func TestRetryPlanRejectsConcurrentRetry(t *testing.T) {
	ps := &plannerServer{failPlan: true}
	app, sess := newPlannerFixture(t, ps)
	appendUser(t, app, sess.ID, "需求")

	failed := app.ChatPlan(sess.ID, "需求", false)

	// 占住这条会话（模拟另一次运行正在进行）
	run, err := app.runs.BeginExclusive(sess.ID, "")
	if err != nil {
		t.Fatalf("登记运行失败: %v", err)
	}
	defer app.runs.End(run)

	if _, err := app.RetryPlan(failed.Plan.ID, false); err == nil {
		t.Fatal("会话正在运行时应拒绝重试")
	} else if !strings.Contains(err.Error(), "正在进行的任务") {
		t.Fatalf("错误信息应说明会话忙，实际 %q", err.Error())
	}
}

// TestChatReturnsPlanObjectOnPlanningFailure 经 App.Chat 走一遍：
// 规划失败不再变成 Go error，否则前端拿不到计划对象、也就没有重试按钮。
func TestChatReturnsPlanObjectOnPlanningFailure(t *testing.T) {
	ps := &plannerServer{failPlan: true}
	app, sess := newPlannerFixture(t, ps)
	appendUser(t, app, sess.ID, "需求")

	res, err := app.Chat(sess.ID, "需求", false, true)
	if err != nil {
		t.Fatalf("规划失败不应返回 Go error（前端需要计划对象）: %v", err)
	}
	if res.Plan == nil || res.Plan.Status != store.PlanPlanFailed {
		t.Fatalf("结果里应带 plan_failed 记录，实际 %+v", res.Plan)
	}
}

// TestChatStillReturnsErrorForNonPlanFailures 普通对话的失败仍然走 Go error（行为不变）。
func TestChatStillReturnsErrorForNonPlanFailures(t *testing.T) {
	ps := &plannerServer{failPlan: true}
	app, sess := newPlannerFixture(t, ps)
	appendUser(t, app, sess.ID, "需求")

	// usePlan=false：401 直接判失败，结果里没有计划对象
	if _, err := app.Chat(sess.ID, "需求", false, false); err == nil {
		t.Fatal("普通对话的失败应仍然返回 Go error")
	}
}

// TestSetSessionPlanMode 计划模式是会话字段：可设置、可持久化、运行中拒绝切换。
func TestSetSessionPlanMode(t *testing.T) {
	ps := &plannerServer{}
	app, sess := newPlannerFixture(t, ps)

	updated, err := app.SetSessionPlanMode(sess.ID, true)
	if err != nil {
		t.Fatalf("开启计划模式失败: %v", err)
	}
	if !updated.PlanMode {
		t.Fatal("返回值应反映已开启")
	}
	reloaded, err := app.sessionStore.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.PlanMode {
		t.Fatal("计划模式未持久化")
	}

	// 运行中拒绝切换
	run, err := app.runs.BeginExclusive(sess.ID, "")
	if err != nil {
		t.Fatalf("登记运行失败: %v", err)
	}
	if _, err := app.SetSessionPlanMode(sess.ID, false); err == nil {
		app.runs.End(run)
		t.Fatal("运行中不应允许切换计划模式")
	}
	app.runs.End(run)

	// 运行结束后可以正常关闭
	closed, err := app.SetSessionPlanMode(sess.ID, false)
	if err != nil {
		t.Fatalf("关闭计划模式失败: %v", err)
	}
	if closed.PlanMode {
		t.Fatal("应已关闭")
	}

	if _, err := app.SetSessionPlanMode("", true); err == nil {
		t.Fatal("空会话 ID 应被拒绝")
	}
	if _, err := app.SetSessionPlanMode("not-exist", true); err == nil {
		t.Fatal("不存在的会话应被拒绝")
	}
}
