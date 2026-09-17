package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// T1 JSON 干净解析
func TestParsePlanDraftClean(t *testing.T) {
	d, err := parsePlanDraft(`{"needPlan":true,"title":"升级依赖","steps":[{"title":"检查依赖","detail":"读取 package.json"},{"title":"升级并测试","detail":""}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !d.NeedPlan || d.Title != "升级依赖" || len(d.Steps) != 2 {
		t.Fatalf("解析错误: %+v", d)
	}
	if d.Steps[0].Title != "检查依赖" || d.Steps[0].Detail != "读取 package.json" {
		t.Fatalf("步骤解析错误: %+v", d.Steps[0])
	}
}

// T2 脏输出（代码块包裹 + 前后杂讯）
func TestParsePlanDraftDirty(t *testing.T) {
	content := "好的，以下是计划：\n```json\n{\"needPlan\":true,\"title\":\"t\",\"steps\":[{\"title\":\"a\"},{\"title\":\"b\"}]}\n```\n希望有帮助"
	d, err := parsePlanDraft(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Steps) != 2 || d.Title != "t" {
		t.Fatalf("应解析出 2 步: %+v", d)
	}
}

// T3 needPlan=false（平凡请求）
func TestParsePlanDraftNoPlan(t *testing.T) {
	d, err := parsePlanDraft(`{"needPlan":false,"title":"","steps":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if d.NeedPlan {
		t.Fatal("needPlan 应为 false")
	}
}

// T4 needPlan=true 步骤为空 → 报错
func TestParsePlanDraftEmptySteps(t *testing.T) {
	if _, err := parsePlanDraft(`{"needPlan":true,"title":"t","steps":[]}`); err == nil {
		t.Fatal("应返回错误")
	}
}

// T5 PlanStore CRUD
func TestPlanStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	s := NewPlanStore(dir)
	p1 := &Plan{ID: "plan_1", SessionID: "sess_a", Title: "一", Status: PlanAwaitingApproval, CreatedAt: 100,
		Steps: []*PlanStep{{Index: 0, Title: "s1", Status: StepPending}}}
	p2 := &Plan{ID: "plan_2", SessionID: "sess_a", Title: "二", Status: PlanRunning, CreatedAt: 200,
		Steps: []*PlanStep{{Index: 0, Title: "s1", Status: StepRunning}}}
	if err := s.Save(p1); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(p2); err != nil {
		t.Fatal(err)
	}

	// 落盘检查
	if _, err := os.Stat(filepath.Join(dir, "plan_1.json")); err != nil {
		t.Fatalf("计划文件未落盘: %v", err)
	}

	got, err := s.Get("plan_1")
	if err != nil || got.Title != "一" {
		t.Fatalf("Get 错误: %+v %v", got, err)
	}

	if latest := s.GetBySession("sess_a"); latest == nil || latest.ID != "plan_2" {
		t.Fatalf("GetBySession 应取最新计划: %+v", latest)
	}
	if s.GetBySession("sess_b") != nil {
		t.Fatal("无计划会话应返回 nil")
	}

	list := s.ListBySession("sess_a")
	if len(list) != 2 || list[0].ID != "plan_2" || list[1].ID != "plan_1" {
		t.Fatalf("ListBySession 排序错误: %+v", list)
	}
}

// T6 启动恢复：running 计划置 failed（不支持断点续跑）
func TestPlanStoreStartupRecovery(t *testing.T) {
	dir := t.TempDir()
	raw := `{"id":"plan_r","sessionId":"s","title":"t","status":"running","steps":[{"index":0,"title":"a","status":"done"},{"index":1,"title":"b","status":"running"}],"createdAt":1,"updatedAt":2}`
	if err := os.WriteFile(filepath.Join(dir, "plan_r.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewPlanStore(dir)
	p, err := s.Get("plan_r")
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != PlanFailed {
		t.Fatalf("running 计划应置 failed: %s", p.Status)
	}
	if p.Steps[0].Status != StepDone {
		t.Fatalf("done 步骤不应被改动: %s", p.Steps[0].Status)
	}
	if p.Steps[1].Status != StepFailed || p.Steps[1].Error == "" {
		t.Fatalf("running 步骤应置 failed 并带原因: %+v", p.Steps[1])
	}
}

// T7 压缩：最近 20 条原样，更早 tool 结果截断，user/assistant 不动
func TestCompactMessages(t *testing.T) {
	long := strings.Repeat("工具结果内容", 100) // 600 字
	var msgs []Message
	for i := 0; i < 30; i++ {
		switch i % 3 {
		case 0:
			msgs = append(msgs, Message{Role: RoleUser, Content: fmt.Sprintf("u%d", i)})
		case 1:
			msgs = append(msgs, Message{Role: RoleAssistant, Content: fmt.Sprintf("a%d", i)})
		default:
			msgs = append(msgs, Message{Role: RoleTool, Content: long, ToolCallID: "x"})
		}
	}

	out := compactMessages(msgs)
	if len(out) != len(msgs) {
		t.Fatalf("消息数不应变化")
	}
	// 最近 20 条原样
	for i := 10; i < 30; i++ {
		if out[i].Content != msgs[i].Content {
			t.Fatalf("最近 20 条应原样: idx=%d", i)
		}
	}
	// 更早消息：tool 截断带省略标记，user/assistant 不动
	for i := 0; i < 10; i++ {
		if msgs[i].Role == RoleTool {
			if out[i].Content == msgs[i].Content || !strings.Contains(out[i].Content, "已省略") {
				t.Fatalf("早期 tool 结果应截断: idx=%d", i)
			}
		} else if out[i].Content != msgs[i].Content {
			t.Fatalf("user/assistant 不应改动: idx=%d", i)
		}
	}
	// 不超过 keepRecent 时原样返回
	if got := compactMessages(msgs[:5]); len(got) != 5 {
		t.Fatal("短历史不应压缩")
	}
}

// T8 步骤摘要：取首段且 ≤200 字
func TestExtractStepSummary(t *testing.T) {
	if got := extractStepSummary("第一段摘要。\n\n第二段细节。"); got != "第一段摘要。" {
		t.Fatalf("应取首段: %q", got)
	}
	long := strings.Repeat("长", 300)
	if got := extractStepSummary(long); got != strings.Repeat("长", 200)+"…" {
		t.Fatalf("应截断到 200 字: %d", len([]rune(got)))
	}
	if got := extractStepSummary("  单段  "); got != "单段" {
		t.Fatalf("应去空白: %q", got)
	}
}

// T9 计划上下文 system prompt：包含状态标记与已完成摘要
func TestBuildPlanSystemPrompt(t *testing.T) {
	plan := &Plan{Title: "升级计划", Steps: []*PlanStep{
		{Index: 0, Title: "检查", Status: StepDone, Summary: "共 3 个依赖落后"},
		{Index: 1, Title: "升级", Status: StepRunning},
		{Index: 2, Title: "测试", Status: StepPending},
	}}
	sys := buildPlanSystemPrompt("BASE", plan, plan.Steps[1])
	if !strings.HasPrefix(sys, "BASE") {
		t.Fatal("应保留基础提示词")
	}
	for _, want := range []string{
		"1. [已完成] 检查",
		"结果摘要：共 3 个依赖落后",
		"2. [当前步骤] 升级",
		"3. [待办] 测试",
		"本步任务：升级",
	} {
		if !strings.Contains(sys, want) {
			t.Fatalf("缺少片段 %q，实际:\n%s", want, sys)
		}
	}
}

// newPlanTestApp 构造基于临时目录的完整 App（mock 模型指向传入的测试服务器）
func newPlanTestApp(t *testing.T, srvURL string) (*App, *Session) {
	t.Helper()
	app := &App{}
	base := t.TempDir()
	app.sessionStore = NewSessionStore(filepath.Join(base, "sessions"))
	app.modelStore = NewModelStore(filepath.Join(base, "models.json"))
	app.planStore = NewPlanStore(filepath.Join(base, "plans"))
	app.toolStore = NewToolStore(filepath.Join(base, "tools.json"))
	app.toolManager = NewToolManager(app.toolStore, nil)
	app.diffService = NewDiffService()
	if err := app.modelStore.AddModel(Model{Name: "mock", URL: srvURL, APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	sess, err := app.sessionStore.CreateSession(SessionConfig{Title: "测试会话", Model: "mock", Project: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return app, sess
}

// T10 端到端（httptest mock LLM）：规划 → 执行，步骤全 done、消息顺序正确、状态机正确
func TestChatPlanAndExecutePlanEndToEnd(t *testing.T) {
	// mock LLM：无 tools 的请求视为规划器（返回计划 JSON）；
	// 带 tools 的请求按调用序次返回：第 1 次=工具调用，之后=各步骤收尾总结
	var stepCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req LLMReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		var resp LLMResp
		if len(req.Tools) == 0 {
			resp = LLMResp{Choices: []LLMChoice{{
				FinishReason: FinishReasonStop,
				Message:      LLMMessage{Role: RoleAssistant, Content: `{"needPlan":true,"title":"端到端计划","steps":[{"title":"步骤一","detail":"先做"},{"title":"步骤二","detail":""}]}`},
			}}}
		} else {
			n := atomic.AddInt32(&stepCalls, 1)
			if n == 1 {
				resp = LLMResp{Choices: []LLMChoice{{
					FinishReason: FinishReasonToolCalls,
					Message: LLMMessage{Role: RoleAssistant, ToolCalls: []LLMToolCall{{
						ID:       "call_1",
						Type:     ToolTypeFunction,
						Function: LLMToolFunction{Name: "exec_shell", Arguments: `{"cmd":"echo ok"}`},
					}}},
				}}}
			} else {
				resp = LLMResp{Choices: []LLMChoice{{
					FinishReason: FinishReasonStop,
					Message:      LLMMessage{Role: RoleAssistant, Content: fmt.Sprintf("步骤%d完成：结果正常", n-1)},
				}}}
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// 组装 App（工作区指向非 git 目录，跳过真实 git 操作）
	app, sess := newPlanTestApp(t, srv.URL)

	// 对齐前端行为：先持久化用户消息再发起对话
	if _, err := app.sessionStore.AppendMessage(sess.ID, Message{Role: RoleUser, Content: "帮我做个两步任务"}); err != nil {
		t.Fatal(err)
	}

	// --- 规划 ---
	planRes := app.ChatPlan(sess.ID, "帮我做个两步任务", false)
	if planRes.Error != "" {
		t.Fatalf("规划失败: %s", planRes.Error)
	}
	if planRes.Plan == nil {
		t.Fatal("应返回计划")
	}
	plan := planRes.Plan
	if plan.Status != PlanAwaitingApproval || len(plan.Steps) != 2 {
		t.Fatalf("计划状态/步骤错误: %+v", plan)
	}
	// 规划器过程消息不持久化：会话中只有 1 条用户消息
	s1, _ := app.sessionStore.GetSession(sess.ID)
	if len(s1.Messages) != 1 {
		t.Fatalf("规划阶段不应产生额外消息: %d", len(s1.Messages))
	}

	// --- 执行 ---
	execRes := app.ExecutePlan(plan.ID, false)
	if execRes.Error != "" {
		t.Fatalf("执行失败: %s", execRes.Error)
	}
	if execRes.Plan.Status != PlanCompleted {
		t.Fatalf("计划应完成: %+v", execRes.Plan)
	}
	for i, st := range execRes.Plan.Steps {
		if st.Status != StepDone || st.Summary == "" {
			t.Fatalf("步骤 %d 应完成并带摘要: %+v", i, st)
		}
	}
	if execRes.Plan.Steps[0].Summary != "步骤1完成：结果正常" {
		t.Fatalf("步骤1摘要错误: %q", execRes.Plan.Steps[0].Summary)
	}

	// 消息入库顺序：user(原始) → user(步骤1) → assistant(toolCalls) → tool → assistant → user(步骤2) → assistant
	s2, _ := app.sessionStore.GetSession(sess.ID)
	roles := make([]string, 0, len(s2.Messages))
	for _, m := range s2.Messages {
		roles = append(roles, m.Role)
	}
	want := []string{RoleUser, RoleUser, RoleAssistant, RoleTool, RoleAssistant, RoleUser, RoleAssistant}
	if !reflect.DeepEqual(roles, want) {
		t.Fatalf("消息序列错误: %v", roles)
	}

	// 完成后重复执行被拒绝（R10）
	if again := app.ExecutePlan(plan.ID, false); again.Error == "" {
		t.Fatal("completed 计划不应再执行")
	}
}

// T11 协作式取消：步骤中途取消 → 当前步骤落 done、后续 skipped；重试时跳过 done 续跑
func TestExecutePlanCancel(t *testing.T) {
	// mock LLM：无 tools → 返回 2 步计划；带 tools → 阻塞直到测试放行（模拟长步骤）
	var started int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req LLMReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		var resp LLMResp
		if len(req.Tools) == 0 {
			resp = LLMResp{Choices: []LLMChoice{{
				FinishReason: FinishReasonStop,
				Message:      LLMMessage{Role: RoleAssistant, Content: `{"needPlan":true,"title":"取消测试","steps":[{"title":"长步骤"},{"title":"永不该执行"}]}`},
			}}}
		} else {
			atomic.AddInt32(&started, 1)
			<-release // 阻塞：第一次执行卡在此处等待取消；close 后立即返回（含重试阶段）
			resp = LLMResp{Choices: []LLMChoice{{
				FinishReason: FinishReasonStop,
				Message:      LLMMessage{Role: RoleAssistant, Content: "步骤完成：结果正常"},
			}}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	if _, err := app.sessionStore.AppendMessage(sess.ID, Message{Role: RoleUser, Content: "做个两步任务"}); err != nil {
		t.Fatal(err)
	}

	planRes := app.ChatPlan(sess.ID, "做个两步任务", false)
	if planRes.Error != "" || planRes.Plan == nil {
		t.Fatalf("规划失败: %s", planRes.Error)
	}
	plan := planRes.Plan

	// 后台执行计划，主协程等首个工具循环启动后取消
	type execOutcome struct {
		res *ChatResult
	}
	done := make(chan execOutcome, 1)
	go func() {
		done <- execOutcome{res: app.ExecutePlan(plan.ID, false)}
	}()

	// 轮询等待步骤 1 进入工具循环（最多 5s）
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("等待步骤执行超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := app.CancelPlan(plan.ID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	close(release) // 放行被阻塞的 LLM 请求

	var execRes *ChatResult
	select {
	case out := <-done:
		execRes = out.res
	case <-time.After(5 * time.Second):
		t.Fatal("等待执行协程结束超时")
	}
	if execRes.Error != "" {
		t.Fatalf("协作式取消不应报错: %s", execRes.Error)
	}
	if execRes.Plan.Status != PlanCancelled {
		t.Fatalf("计划应置 cancelled: %s", execRes.Plan.Status)
	}
	if execRes.Plan.Steps[0].Status != StepDone || execRes.Plan.Steps[0].Summary == "" {
		t.Fatalf("当前步骤已跑完应落 done: %+v", execRes.Plan.Steps[0])
	}
	if execRes.Plan.Steps[1].Status != StepSkipped {
		t.Fatalf("后续步骤应置 skipped: %+v", execRes.Plan.Steps[1])
	}

	// 重试：done 步骤跳过，skipped 步骤重跑（release 已关闭，请求直接放行）
	execRes2 := app.ExecutePlan(plan.ID, false)
	if execRes2.Error != "" {
		t.Fatalf("重试失败: %s", execRes2.Error)
	}
	if execRes2.Plan.Status != PlanCompleted {
		t.Fatalf("重试后应完成: %s", execRes2.Plan.Status)
	}
	if execRes2.Plan.Steps[0].Status != StepDone {
		t.Fatalf("已完成步骤不应被重跑: %+v", execRes2.Plan.Steps[0])
	}
	if execRes2.Plan.Steps[1].Status != StepDone || execRes2.Plan.Steps[1].Summary == "" {
		t.Fatalf("原 skipped 步骤应重跑并完成: %+v", execRes2.Plan.Steps[1])
	}
}

// ===== 失败/中断后的恢复能力（修复「计划失败后无法修改进度」）=====

// 计划失败后必须能退回待审核，且已完成步骤的进度不丢
func TestPlanStoreReopen(t *testing.T) {
	s := NewPlanStore(t.TempDir())
	plan := &Plan{
		ID: "p1", SessionID: "s1", Title: "t", Status: PlanFailed, CreatedAt: 1,
		Steps: []*PlanStep{
			{Index: 0, Title: "已完成", Status: StepDone, Summary: "摘要"},
			{Index: 1, Title: "失败步骤", Status: StepFailed, Error: "网络错误"},
			{Index: 2, Title: "被跳过", Status: StepSkipped},
		},
	}
	if err := s.Save(plan); err != nil {
		t.Fatal(err)
	}

	got, err := s.Reopen("p1")
	if err != nil {
		t.Fatalf("重开失败: %v", err)
	}
	if got.Status != PlanAwaitingApproval {
		t.Fatalf("重开后应为待审核，实际 %s", got.Status)
	}
	if got.Steps[0].Status != StepDone || got.Steps[0].Summary != "摘要" {
		t.Fatalf("已完成步骤的进度应保留: %+v", got.Steps[0])
	}
	if got.Steps[1].Status != StepPending || got.Steps[1].Error != "" {
		t.Fatalf("失败步骤应重置为待执行并清掉原因: %+v", got.Steps[1])
	}
	if got.Steps[2].Status != StepPending {
		t.Fatalf("被跳过的步骤应重置为待执行: %+v", got.Steps[2])
	}

	// 幂等：已处于待审核时再次重开不应报错
	if _, err := s.Reopen("p1"); err != nil {
		t.Fatalf("重复重开应幂等: %v", err)
	}

	// 执行中不允许重开（有活跃执行者，应先取消）
	if err := s.Save(&Plan{ID: "p2", SessionID: "s1", Status: PlanRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reopen("p2"); err == nil {
		t.Fatal("running 计划应拒绝重开")
	}
	if _, err := s.Reopen("不存在"); err == nil {
		t.Fatal("不存在的计划应报错")
	}

	// 已完成/已取消的计划也允许重开（继续加步骤或重跑）
	for _, st := range []string{PlanCompleted, PlanCancelled} {
		if err := s.Save(&Plan{ID: "p3", SessionID: "s1", Status: st}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Reopen("p3"); err != nil {
			t.Fatalf("%s 计划应可重开: %v", st, err)
		}
	}
}

// 执行协程异常退出（计划停在 running、没有活跃取消通道）时，取消必须能强制收尾，
// 否则前端会永久停在"执行中"且取消失败，只能重启应用。
func TestCancelPlanRecoversStuckRunning(t *testing.T) {
	app := &App{planStore: NewPlanStore(t.TempDir())}
	if err := app.planStore.Save(&Plan{
		ID: "p1", SessionID: "s1", Status: PlanRunning,
		Steps: []*PlanStep{
			{Index: 0, Title: "进行中", Status: StepRunning},
			{Index: 1, Title: "待执行", Status: StepPending},
			{Index: 2, Title: "已完成", Status: StepDone},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := app.CancelPlan("p1"); err != nil {
		t.Fatalf("卡在 running 的计划应能强制取消: %v", err)
	}
	got, _ := app.planStore.Get("p1")
	if got.Status != PlanCancelled {
		t.Fatalf("应置为已取消，实际 %s", got.Status)
	}
	if got.Steps[0].Status != StepFailed || got.Steps[0].Error == "" {
		t.Fatalf("进行中的步骤应置失败并说明原因: %+v", got.Steps[0])
	}
	if got.Steps[1].Status != StepSkipped {
		t.Fatalf("待执行步骤应置为跳过: %+v", got.Steps[1])
	}
	if got.Steps[2].Status != StepDone {
		t.Fatalf("已完成步骤不应被改动: %+v", got.Steps[2])
	}

	// 非 running 且无通道：明确报错，不能误改状态
	if err := app.planStore.Save(&Plan{ID: "p2", SessionID: "s1", Status: PlanCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := app.CancelPlan("p2"); err == nil {
		t.Fatal("已完成计划不应被取消")
	}
	if p, _ := app.planStore.Get("p2"); p.Status != PlanCompleted {
		t.Fatalf("已完成计划的状态不应被改动: %s", p.Status)
	}
}

// 端到端回归：步骤执行遇到网络/接口失败 → 计划落到 failed（而不是卡在 running），
// 随后可以 ReopenPlan 修改并重新执行，已完成的步骤不重跑。
func TestExecutePlanFailureThenReopenAndResume(t *testing.T) {
	var failNext int32 = 1 // 1 = 步骤执行时让 LLM 接口报错
	var stepCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req LLMReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if len(req.Tools) == 0 {
			// 规划器：返回两步计划
			_ = json.NewEncoder(w).Encode(LLMResp{Choices: []LLMChoice{{
				FinishReason: FinishReasonStop,
				Message: LLMMessage{Role: RoleAssistant,
					Content: `{"needPlan":true,"title":"网络失败测试","steps":[{"title":"第一步"},{"title":"第二步"}]}`},
			}}})
			return
		}
		atomic.AddInt32(&stepCalls, 1)
		if atomic.LoadInt32(&failNext) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(LLMResp{Choices: []LLMChoice{{
			FinishReason: FinishReasonStop,
			Message:      LLMMessage{Role: RoleAssistant, Content: "步骤完成：结果正常"},
		}}})
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	if _, err := app.sessionStore.AppendMessage(sess.ID, Message{Role: RoleUser, Content: "两步任务"}); err != nil {
		t.Fatal(err)
	}
	planRes := app.ChatPlan(sess.ID, "两步任务", false)
	if planRes.Error != "" || planRes.Plan == nil {
		t.Fatalf("规划失败: %s", planRes.Error)
	}
	planID := planRes.Plan.ID

	// 第一次执行：步骤接口 500 → 计划必须落到 failed，而不是留在 running
	res := app.ExecutePlan(planID, false)
	if res.Error == "" {
		t.Fatal("接口失败时应返回错误")
	}
	plan, err := app.planStore.Get(planID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != PlanFailed {
		t.Fatalf("执行失败后计划应为 failed（不能卡在 running），实际 %s", plan.Status)
	}
	if plan.Steps[0].Status != StepFailed || plan.Steps[0].Error == "" {
		t.Fatalf("失败步骤应带原因: %+v", plan.Steps[0])
	}
	if plan.Steps[1].Status != StepSkipped {
		t.Fatalf("后续步骤应置为跳过: %+v", plan.Steps[1])
	}

	// 失败后必须能取消（这条路径以前会因为找不到活跃通道而失败）
	if err := app.CancelPlan(planID); err == nil {
		t.Fatal("已经 failed 的计划不应能被再次取消")
	}

	// 重开 → 修改状态 → 重新执行：应能跑到完成，且不再重跑已完成步骤
	reopened, err := app.ReopenPlan(planID)
	if err != nil {
		t.Fatalf("失败的计划应可重开: %v", err)
	}
	if reopened.Status != PlanAwaitingApproval {
		t.Fatalf("重开后应为待审核，实际 %s", reopened.Status)
	}
	atomic.StoreInt32(&failNext, 0) // 网络恢复
	res2 := app.ExecutePlan(planID, false)
	if res2.Error != "" {
		t.Fatalf("恢复后应执行成功: %s", res2.Error)
	}
	final, _ := app.planStore.Get(planID)
	if final.Status != PlanCompleted {
		t.Fatalf("恢复执行后应完成，实际 %s", final.Status)
	}
	for i, st := range final.Steps {
		if st.Status != StepDone {
			t.Fatalf("第 %d 步应为完成，实际 %s", i+1, st.Status)
		}
	}
	if got := atomic.LoadInt32(&stepCalls); got < 3 {
		t.Fatalf("两次执行至少应发生 3 次步骤调用（首次失败 + 重试两步），实际 %d", got)
	}
}
