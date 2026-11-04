package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 步骤级自动重试（plan-execution-recovery）=====
//
// 这一组守的是"网络抖一下不需要用户介入"：可重试的失败在步骤内自动重来，
// 不可重试的立刻失败。所有用例都注入"不等待"的退避策略——真实退避是生产行为，
// 不该让单测白睡几秒。

// captureServer 记录每次请求的 system prompt，便于断言"重试注入了几次尝试的信息"。
type captureServer struct {
	mu      sync.Mutex
	plan    string
	reply   func(n int) (int, string)
	calls   int
	systems []string // 带 tools 的请求的系统提示词
}

func (s *captureServer) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req llm.LLMReq
	_ = json.Unmarshal(body, &req)

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(req.Tools) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(llm.LLMResp{Choices: []llm.LLMChoice{{
			FinishReason: llm.FinishReasonStop,
			Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: s.plan},
		}}})
		return
	}

	s.calls++
	if len(req.Messages) > 0 {
		s.systems = append(s.systems, req.Messages[0].Content)
	}
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

func (s *captureServer) snapshot() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sys := make([]string, len(s.systems))
	copy(sys, s.systems)
	return s.calls, sys
}

// withoutTransportRetry 把 llm 层（传输层）的自动重试临时关掉（MaxAttempts=1）。
//
// 计划层的重试只发生在**传输层已经放弃之后**：两层共享同一套"可重试"判定
// （见 llm.TestRetryableAgreesWithShouldRetry 的一致性用例），所以任何被计划层判为
// 可重试的错误，llm 层都会先自己重试 4 次、退避合计约 2.6s。不关掉它，每个用例
// 都要先白等这几秒，而且调用次数会变成 4 的倍数、断言全是噪音。
//
// 关掉之后，用例考察的正是本次要实现的那件事：传输层放弃之后，计划层是否按预期接管。
func withoutTransportRetry(t *testing.T) {
	t.Helper()
	old := llm.DefaultRetryPolicy
	llm.DefaultRetryPolicy = llm.RetryPolicy{MaxAttempts: 1}
	t.Cleanup(func() { llm.DefaultRetryPolicy = old })
}

// noWaitRetry 不等待的重试策略：只在用例里用。
func noWaitRetry(maxRetries int) *planRetryPolicy {
	return &planRetryPolicy{
		MaxRetries: maxRetries,
		Backoff:    func(int) time.Duration { return 0 },
	}
}

func newRetryFixture(t *testing.T, cs *captureServer) (*App, *store.Session) {
	t.Helper()
	cs.plan = `{"needPlan":true,"title":"重试测试","steps":[{"title":"第一步","detail":""},{"title":"第二步","detail":""}]}`
	srv := httptest.NewServer(http.HandlerFunc(cs.handler))
	t.Cleanup(srv.Close)

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &agent.RunRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(10)
	if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{Role: store.RoleUser, Content: "两步任务"}); err != nil {
		t.Fatal(err)
	}
	return app, sess
}

// TestPlanStepAutoRetriesTransientFailure 瞬时 5xx 自动重试，用户无感地跑完。
func TestPlanStepAutoRetriesTransientFailure(t *testing.T) {
	withoutTransportRetry(t)
	cs := &captureServer{}
	app, sess := newRetryFixture(t, cs)
	app.planRetry = noWaitRetry(2)
	// 第 1、2 次调用 502（server，可重试），第 3 次开始成功
	cs.reply = func(n int) (int, string) {
		if n <= 2 {
			return http.StatusBadGateway, `{"error":{"message":"upstream gone"}}`
		}
		return 200, "本步完成"
	}

	planned := app.ChatPlan(sess.ID, "两步任务", false)
	res := app.ExecutePlan(planned.Plan.ID, false)
	if res.Error != "" {
		t.Fatalf("瞬时故障应自动恢复，实际报错: %s", res.Error)
	}

	plan, _ := app.planStore.Get(planned.Plan.ID)
	if plan.Status != store.PlanCompleted {
		t.Fatalf("计划应完成，实际 %q", plan.Status)
	}
	if plan.Steps[0].Attempt != 3 {
		t.Fatalf("第 1 步应在第 3 次尝试成功（Attempt=3），实际 %d", plan.Steps[0].Attempt)
	}
	if plan.Steps[1].Attempt != 1 {
		t.Fatalf("第 2 步不该被重试，Attempt=%d", plan.Steps[1].Attempt)
	}

	// 重试不该在会话里留下三份步骤请求消息
	counts := stepMessages(t, app, sess.ID)
	if counts[0] != 1 {
		t.Fatalf("第 0 步的请求消息应只有 1 条，实际 %d", counts[0])
	}
}

// TestPlanStepRetryInjectsPreviousAttempt
// 重试时系统提示词要写明"这是第 N 次尝试"，并列出上次执行过的工具。
//
// 引擎没有事务，重试是从步骤开头重来的；模型不知道上次做了什么，
// 就会把同一个文件再写一遍、或重跑一次有副作用的命令。
func TestPlanStepRetryInjectsPreviousAttempt(t *testing.T) {
	withoutTransportRetry(t)
	cs := &captureServer{}
	app, sess := newRetryFixture(t, cs)
	app.planRetry = noWaitRetry(2)
	// 第 1 次：让模型先调一个工具（产生副作用），然后这一轮以 502 结束；
	// 简化起见直接用 502，工具摘要为空的路径另外由 appendPlanRetryContext 的单测覆盖。
	cs.reply = func(n int) (int, string) {
		if n == 1 {
			return http.StatusBadGateway, `{"error":{"message":"upstream gone"}}`
		}
		return 200, "完成"
	}

	planned := app.ChatPlan(sess.ID, "两步任务", false)
	if res := app.ExecutePlan(planned.Plan.ID, false); res.Error != "" {
		t.Fatalf("应自动恢复: %s", res.Error)
	}

	_, systems := cs.snapshot()
	if len(systems) < 2 {
		t.Fatalf("应有至少 2 次带 tools 的请求，实际 %d 次", len(systems))
	}
	if strings.Contains(systems[0], "这是第 2 次尝试") {
		t.Fatal("首次尝试不该带重试说明")
	}
	if !strings.Contains(systems[1], "这是第 2 次尝试") {
		t.Fatalf("重试的系统提示词应写明第几次尝试，实际末尾: %s", tail(systems[1], 400))
	}
	if !strings.Contains(systems[1], "已经完成的部分不要重复执行") {
		t.Fatalf("重试时应要求先核对既往操作，实际末尾: %s", tail(systems[1], 400))
	}
}

// TestAppendPlanRetryContextListsTools 单测：上次尝试的工具与失败原因都会被列出来。
func TestAppendPlanRetryContextListsTools(t *testing.T) {
	prev := &ChatResult{
		Error: "LLM 返回错误 (status=502)",
		ToolCalls: []store.ToolCall{
			{Name: "write_file", Result: "已写入 a.txt"},
			{Name: "exec_shell", Result: "ok\n"},
		},
	}
	got := appendPlanRetryContext("BASE", 2, prev)
	for _, want := range []string{"BASE", "这是第 2 次尝试", "write_file", "已写入 a.txt", "exec_shell", "502"} {
		if !strings.Contains(got, want) {
			t.Fatalf("重试上下文应包含 %q，实际：\n%s", want, got)
		}
	}

	// 首次尝试（没有 prev / 没有工具）也要给出一句实话，而不是空段落
	empty := appendPlanRetryContext("BASE", 2, nil)
	if !strings.Contains(empty, "没有执行任何工具") {
		t.Fatalf("没有工具时应说明，实际：\n%s", empty)
	}
}

// TestPlanStepDoesNotRetryAuthFailure 认证失败立刻停：重试只是把同样的失败再来几遍。
func TestPlanStepDoesNotRetryAuthFailure(t *testing.T) {
	withoutTransportRetry(t)
	cs := &captureServer{}
	app, sess := newRetryFixture(t, cs)
	app.planRetry = noWaitRetry(5) // 给足额度，验证"不重试"是分类决定的
	cs.reply = func(int) (int, string) {
		return http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`
	}

	planned := app.ChatPlan(sess.ID, "两步任务", false)
	res := app.ExecutePlan(planned.Plan.ID, false)
	if res.Error == "" {
		t.Fatal("认证失败应返回错误")
	}
	if res.ErrorKind != string(llm.ErrorKindAuth) {
		t.Fatalf("ErrorKind 应为 auth，实际 %q", res.ErrorKind)
	}
	if calls, _ := cs.snapshot(); calls != 1 {
		t.Fatalf("认证失败只应请求 1 次，实际 %d 次", calls)
	}
	plan, _ := app.planStore.Get(planned.Plan.ID)
	if plan.Status != store.PlanFailed || plan.Steps[0].Status != store.StepFailed {
		t.Fatalf("应落 failed，实际 plan=%q step=%q", plan.Status, plan.Steps[0].Status)
	}
	if plan.Steps[1].Status != store.StepPending {
		t.Fatalf("后续步骤应保持 pending: %+v", plan.Steps[1])
	}
}

// TestPlanStepRetryExhaustedThenResumable 重试耗尽 → 计划 failed，且随后仍能续跑。
func TestPlanStepRetryExhaustedThenResumable(t *testing.T) {
	withoutTransportRetry(t)
	cs := &captureServer{}
	app, sess := newRetryFixture(t, cs)
	app.planRetry = noWaitRetry(2) // 首次 + 2 次重试 = 3 次
	cs.reply = func(int) (int, string) {
		return http.StatusInternalServerError, `{"error":{"message":"boom"}}`
	}

	planned := app.ChatPlan(sess.ID, "两步任务", false)
	res := app.ExecutePlan(planned.Plan.ID, false)
	if res.Error == "" {
		t.Fatal("重试耗尽应返回错误")
	}
	if res.ErrorKind != string(llm.ErrorKindServer) {
		t.Fatalf("ErrorKind 应为 server，实际 %q", res.ErrorKind)
	}
	if calls, _ := cs.snapshot(); calls != 3 {
		t.Fatalf("首次 + 2 次重试应共请求 3 次，实际 %d 次", calls)
	}
	plan, _ := app.planStore.Get(planned.Plan.ID)
	if plan.Status != store.PlanFailed || plan.Steps[0].Attempt != 3 {
		t.Fatalf("应为 failed 且第 1 步尝试 3 次，实际 plan=%q attempt=%d", plan.Status, plan.Steps[0].Attempt)
	}

	// 网络恢复 → 续跑（沿用同一策略，这次第一次就成功）
	cs.mu.Lock()
	cs.reply = func(int) (int, string) { return 200, "完成" }
	cs.mu.Unlock()
	if resumed := app.ResumePlan(planned.Plan.ID, false); resumed.Error != "" {
		t.Fatalf("续跑应成功: %s", resumed.Error)
	}
	final, _ := app.planStore.Get(planned.Plan.ID)
	if final.Status != store.PlanCompleted {
		t.Fatalf("续跑后应完成，实际 %q", final.Status)
	}
	// 续跑是新的尝试（Attempt 继续自增），请求消息仍然只有一条
	if final.Steps[0].Attempt != 4 {
		t.Fatalf("续跑应记为第 4 次尝试，实际 %d", final.Steps[0].Attempt)
	}
	if counts := stepMessages(t, app, sess.ID); counts[0] != 1 {
		t.Fatalf("三次重试 + 一次续跑后，第 0 步的请求消息仍应只有 1 条，实际 %d", counts[0])
	}
}

// TestPlanRetryAbortsOnSoftCancel 退避期间点「停止」要立刻生效，而不是等退避走完。
//
// 软取消刻意不 cancel ctx（它只是"下一轮别再开始"），所以等待必须同时监听软取消信号，
// 否则用户会对着"点了没反应"的界面等满 8 秒。
func TestPlanRetryAbortsOnSoftCancel(t *testing.T) {
	withoutTransportRetry(t)
	cs := &captureServer{}
	app, sess := newRetryFixture(t, cs)
	// 退避 10s：若实现只监听 ctx，这个用例会卡到超时
	app.planRetry = &planRetryPolicy{
		MaxRetries: 5,
		Backoff:    func(int) time.Duration { return 10 * time.Second },
	}
	firstCall := make(chan struct{})
	var once sync.Once
	cs.reply = func(int) (int, string) {
		once.Do(func() { close(firstCall) })
		return http.StatusInternalServerError, `{"error":{"message":"boom"}}`
	}

	planned := app.ChatPlan(sess.ID, "两步任务", false)
	planID := planned.Plan.ID
	done := make(chan *ChatResult, 1)
	go func() { done <- app.ExecutePlan(planID, false) }()

	select {
	case <-firstCall:
	case <-time.After(5 * time.Second):
		t.Fatal("等待首次请求超时")
	}
	// 给它一点时间进入退避等待
	time.Sleep(50 * time.Millisecond)
	if err := app.CancelPlan(planID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("软取消后应立即结束，实际卡在退避等待里（等待只监听了 ctx）")
	}

	plan, _ := app.planStore.Get(planID)
	if plan.Status != store.PlanCancelled {
		t.Fatalf("取消后计划应为 cancelled，实际 %q", plan.Status)
	}
	if calls, _ := cs.snapshot(); calls != 1 {
		t.Fatalf("取消后不该再发请求，实际共 %d 次", calls)
	}
}

// tail 取字符串末尾 n 个字符（错误信息通常很长，只需看结尾那段）。
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
