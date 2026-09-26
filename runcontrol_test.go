package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ===== 运行取消控制（P1-A）=====
//
// 这些测试守的是「点了停止真的会停」这条底线。改造前前端只把
// chatStore.isGenerating 置 false（纯客户端标志位），后端那一轮循环仍在跑、
// 仍在写文件、仍在起子进程——所以这里既有单元级的语义断言，
// 也有"真起一个进程再取消它"的集成断言。

// 软取消：只置信号，不碰 ctx
func TestRunControlSoftCancel(t *testing.T) {
	reg := &runRegistry{}
	run := reg.begin("s1", "")

	if run.SoftRequested() {
		t.Fatal("刚创建的运行不应处于已取消状态")
	}
	if !run.markSoft() {
		t.Fatal("首次软取消应返回 true")
	}
	if !run.SoftRequested() {
		t.Fatal("软取消后 SoftRequested 应为 true")
	}
	if run.Kind() != cancelKindSoft {
		t.Fatalf("取消种类应为 soft，实际 %q", run.Kind())
	}
	// 软取消不该 cancel ctx：在途请求与工具要能跑完
	if run.Ctx().Err() != nil {
		t.Fatalf("软取消不应 cancel ctx，实际 %v", run.Ctx().Err())
	}
	if run.markSoft() {
		t.Fatal("重复软取消应返回 false（幂等）")
	}
}

// 硬取消：先置软信号，再 cancel ctx
func TestRunControlHardCancel(t *testing.T) {
	reg := &runRegistry{}
	run := reg.begin("s1", "")

	if !run.markHard() {
		t.Fatal("首次硬取消应返回 true")
	}
	if !run.SoftRequested() {
		t.Fatal("硬取消必须同时置软信号——循环要靠它走正常收尾路径")
	}
	if run.Kind() != cancelKindHard {
		t.Fatalf("取消种类应为 hard，实际 %q", run.Kind())
	}
	select {
	case <-run.Ctx().Done():
		// 期望：ctx 已取消
	case <-time.After(time.Second):
		t.Fatal("硬取消应 cancel 运行 ctx")
	}
	if run.markHard() {
		t.Fatal("重复硬取消应返回 false（幂等）")
	}
}

// 先软后硬：升级路径必须成立（前端"再点一次"依赖它）
func TestRunControlSoftThenHard(t *testing.T) {
	reg := &runRegistry{}
	run := reg.begin("s1", "")

	if !run.markSoft() {
		t.Fatal("软取消应成功")
	}
	if run.Kind() != cancelKindSoft {
		t.Fatalf("应为 soft，实际 %q", run.Kind())
	}
	if !run.markHard() {
		t.Fatal("软取消之后升级为硬取消应返回 true")
	}
	if run.Kind() != cancelKindHard {
		t.Fatalf("应升级为 hard，实际 %q", run.Kind())
	}
	select {
	case <-run.Ctx().Done():
	case <-time.After(time.Second):
		t.Fatal("升级后应 cancel ctx")
	}
}

// 注册表：按会话 / 按计划都能查到，end 之后注销且释放 ctx
func TestRunRegistryLookupAndEnd(t *testing.T) {
	reg := &runRegistry{}
	run := reg.begin("s1", "p1")

	if got := reg.bySession("s1"); got != run {
		t.Fatal("应按 sessionID 查到运行")
	}
	if got := reg.byPlan("p1"); got != run {
		t.Fatal("应按 planID 查到运行")
	}
	if reg.bySession("nope") != nil || reg.byPlan("nope") != nil {
		t.Fatal("不存在的会话/计划应返回 nil")
	}
	if n := reg.activeCount(); n != 1 {
		t.Fatalf("活跃运行数应为 1，实际 %d", n)
	}

	reg.end(run)
	if reg.bySession("s1") != nil || reg.byPlan("p1") != nil {
		t.Fatal("end 之后不应再查到运行")
	}
	if n := reg.activeCount(); n != 0 {
		t.Fatalf("end 之后活跃运行数应为 0，实际 %d", n)
	}
	// end 必须释放 ctx，否则派生 ctx 会一直挂着
	if run.Ctx().Err() == nil {
		t.Fatal("end 应释放运行 ctx")
	}
}

// stopSession 的返回值语义：hit 表示命中运行，changed 表示本次真的触发了取消
func TestRunRegistryStopSemantics(t *testing.T) {
	reg := &runRegistry{}
	reg.begin("s1", "")

	if hit, _ := reg.stopSession("nope", false); hit {
		t.Fatal("没有运行时 hit 应为 false")
	}
	hit, changed := reg.stopSession("s1", false)
	if !hit || !changed {
		t.Fatalf("首次软取消应为 hit=true changed=true，实际 %v %v", hit, changed)
	}
	hit, changed = reg.stopSession("s1", false)
	if !hit || changed {
		t.Fatalf("重复软取消应为 hit=true changed=false，实际 %v %v", hit, changed)
	}
	hit, changed = reg.stopSession("s1", true)
	if !hit || !changed {
		t.Fatalf("软→硬升级应为 hit=true changed=true，实际 %v %v", hit, changed)
	}
}

// 零值 App（测试里直接构造 &App{...}）没有注册表：
// 这些路径必须能用，而不是 panic —— plan_test.go 的
// TestCancelPlanRecoversStuckRunning 就依赖这一点。
func TestRunRegistryNilSafe(t *testing.T) {
	var reg *runRegistry

	run := reg.begin("s1", "p1")
	if run == nil {
		t.Fatal("nil 注册表的 begin 应返回可用的控制块，而不是 nil")
	}
	if run.SoftRequested() {
		t.Fatal("未取消时应为 false")
	}
	if hit, changed := reg.stopSession("s1", true); hit || changed {
		t.Fatal("nil 注册表不应命中任何运行")
	}
	if reg.bySession("s1") != nil || reg.byPlan("p1") != nil {
		t.Fatal("nil 注册表应返回 nil")
	}
	if n := reg.activeCount(); n != 0 {
		t.Fatalf("nil 注册表活跃数应为 0，实际 %d", n)
	}
	reg.end(run) // 不应 panic
	reg.end(nil)
}

// StopChat：没有活跃运行时报 false（前端据此复位按钮，避免"点了没反应"）
func TestStopChatWithoutRun(t *testing.T) {
	app := &App{runs: &runRegistry{}}
	hit, err := app.StopChat("no-such-session", false)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if hit {
		t.Fatal("没有活跃运行时应返回 false")
	}
	if _, err := app.StopChat("", false); err == nil {
		t.Fatal("空会话 ID 应报错")
	}
}

// ===== 硬取消必须能杀掉正在跑的子进程 =====
//
// 这是 CLITool 从 exec.Command 改成 exec.CommandContext 的回归测试。
// 改造前 ctx 虽然传进来了却被忽略，"停止"要等命令自己跑完才生效。
func TestHardCancelKillsRunningCommand(t *testing.T) {
	tool := &CLITool{
		BaseTool: &BaseTool{
			Name:        "exec_shell",
			Description: "测试用",
			Parameters: map[string]*ToolArgDef{
				"cmd": {Type: "string", Description: "命令"},
			},
		},
		Dir: t.TempDir(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := tool.Execute(ctx, map[string]interface{}{"cmd": "sleep 30"})
		done <- err
	}()

	// 给命令一点时间真正跑起来，否则测的是"还没开始就被取消"
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("命令应因取消而失败")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("取消后应在秒级返回，实际 %v —— 说明 ctx 没传给 exec", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("取消后命令仍在运行：CLITool 没有把 ctx 传给 exec.CommandContext")
	}
}

// 提问回路的等待必须能被 ctx 取消：否则用户点了停止还要再等 10 分钟超时
func TestAskWaitCancelledByCtx(t *testing.T) {
	b := newAskBroker(time.Minute)
	id, ch := b.register("s1", AskRequest{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	ans, err := b.Wait(ctx, id, ch)
	if err != nil {
		t.Fatalf("取消不应返回错误（应把'用户未作答'回给模型）: %v", err)
	}
	if !ans.Cancelled {
		t.Fatal("ctx 取消后 Cancelled 应为 true")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("应在取消后立即返回，实际 %v", elapsed)
	}
}

// ===== 端到端：软取消在下一轮边界停下，且不跳过本轮已执行的工具 =====
func TestSoftCancelStopsAtNextTurnBoundary(t *testing.T) {
	var firstCall int32
	var started int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req LLMReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")

		var resp LLMResp
		if atomic.AddInt32(&firstCall, 1) == 1 {
			// 第一轮：阻塞住，让主协程有机会发出软取消
			atomic.StoreInt32(&started, 1)
			<-release
			resp = LLMResp{Choices: []LLMChoice{{
				FinishReason: FinishReasonToolCalls,
				Message: LLMMessage{Role: RoleAssistant, ToolCalls: []LLMToolCall{{
					ID:       "call_1",
					Type:     ToolTypeFunction,
					Function: LLMToolFunction{Name: "exec_shell", Arguments: `{"cmd":"echo ok"}`},
				}}},
			}}}
		} else {
			// 第二轮不该被发起：软取消应已生效
			resp = LLMResp{Choices: []LLMChoice{{
				FinishReason: FinishReasonStop,
				Message:      LLMMessage{Role: RoleAssistant, Content: "不该走到这里"},
			}}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	// 只关一次：中途显式 close 放行第一轮，收尾时若还没关再补一次
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &runRegistry{}
	app.baseDir = t.TempDir() // 权限规则从 baseDir 读，空路径会读到意外位置
	app.fileChanges = NewFileChangeLog(50)
	if _, err := app.sessionStore.AppendMessage(sess.ID, Message{Role: RoleUser, Content: "跑个命令"}); err != nil {
		t.Fatal(err)
	}

	done := make(chan *ChatResult, 1)
	go func() { done <- app.executeChat(sess.ID, "跑个命令", false) }()

	waitFor(t, &started, "等待首次 LLM 请求")
	hit, err := app.StopChat(sess.ID, false)
	if err != nil {
		t.Fatalf("软取消失败: %v", err)
	}
	if !hit {
		t.Fatal("软取消应命中正在跑的运行")
	}
	close(release) // 放行第一轮请求
	released = true

	res := waitResult(t, done)
	if !res.Cancelled || res.CancelKind != cancelKindSoft {
		t.Fatalf("应在下一轮边界以软取消结束，实际 cancelled=%v kind=%q err=%q",
			res.Cancelled, res.CancelKind, res.Error)
	}
	// 软取消不打断当前步：本轮工具已经执行完并留下记录
	if len(res.ToolCalls) == 0 {
		t.Fatal("软取消不应跳过本轮的工具执行")
	}
	if atomic.LoadInt32(&firstCall) != 1 {
		t.Fatal("软取消后不应再发起新的 LLM 请求")
	}
}

// ===== 端到端：硬取消断开在途请求并返回 Cancelled =====
func TestHardCancelAbortsInFlightRequest(t *testing.T) {
	var started int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt32(&started, 1)
		// 一直不回：硬取消必须靠 ctx 把在途请求断掉（而不是等它超时）
		<-r.Context().Done()
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &runRegistry{}
	app.baseDir = t.TempDir() // 权限规则从 baseDir 读，空路径会读到意外位置
	app.fileChanges = NewFileChangeLog(50)
	if _, err := app.sessionStore.AppendMessage(sess.ID, Message{Role: RoleUser, Content: "问个问题"}); err != nil {
		t.Fatal(err)
	}

	done := make(chan *ChatResult, 1)
	go func() { done <- app.executeChat(sess.ID, "问个问题", false) }()

	waitFor(t, &started, "等待首次 LLM 请求")
	start := time.Now()
	hit, err := app.StopChat(sess.ID, true)
	if err != nil || !hit {
		t.Fatalf("硬取消应命中运行: hit=%v err=%v", hit, err)
	}

	res := waitResult(t, done)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("硬取消后应在秒级返回，实际 %v", elapsed)
	}
	if !res.Cancelled || res.CancelKind != cancelKindHard {
		t.Fatalf("硬取消应返回 cancelled=true kind=hard，实际 cancelled=%v kind=%q err=%q",
			res.Cancelled, res.CancelKind, res.Error)
	}
}

// waitFor 轮询等待某个标志位被置起
func waitFor(t *testing.T, flag *int32, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(flag) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%s 超时", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitResult 等待运行结束
func waitResult(t *testing.T, done chan *ChatResult) *ChatResult {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(15 * time.Second):
		t.Fatal("运行未在合理时间内结束（取消可能没生效）")
		return nil
	}
}

// ===== 会话级并行与运行归属（多会话并行那一轮）=====
//
// 这一组守的是两件事：
//   ① 同一会话不能被并发启动两次（否则索引被覆盖、前一条停不掉）；
//   ② 不同会话可以同时跑，且各自的取消互不干扰。
// runID 的单调性单列一条用例，因为前端靠它丢弃"上一轮的迟到事件"。

// 同一会话第二次登记必须被拒绝
func TestBeginExclusiveRejectsSecondRunSameSession(t *testing.T) {
	reg := &runRegistry{}
	first, err := reg.beginExclusive("s1", "")
	if err != nil {
		t.Fatalf("首次登记应成功: %v", err)
	}

	second, err := reg.beginExclusive("s1", "")
	if err == nil {
		t.Fatal("同一会话第二次登记必须被拒绝")
	}
	if !errors.Is(err, errSessionBusy) {
		t.Fatalf("错误应能被 errors.Is 判定为 errSessionBusy，实际: %v", err)
	}
	if second != nil {
		t.Fatal("被拒绝时不应返回可用的运行控制块（调用方会照常 defer end）")
	}
	// 最关键的检查点：索引没被覆盖。被覆盖的后果是旧那条运行再也停不掉。
	if got := reg.bySession("s1"); got != first {
		t.Fatalf("sessionID 索引被覆盖：期望仍指向首条运行，实际 %p", got)
	}
	if hit, _ := reg.stopSession("s1", false); !hit {
		t.Fatal("首条运行应当仍可被停止")
	}

	// 结束后可以重新登记（用户点一次停止再发下一条是正常路径）
	reg.end(first)
	third, err := reg.beginExclusive("s1", "")
	if err != nil {
		t.Fatalf("前一条结束后应能重新登记: %v", err)
	}
	if third == nil {
		t.Fatal("重新登记应返回可用的控制块")
	}
}

// 不同会话必须能同时登记——这是"多会话并行"的前提
func TestBeginExclusiveAllowsDifferentSessions(t *testing.T) {
	reg := &runRegistry{}
	r1, err := reg.beginExclusive("s1", "")
	if err != nil {
		t.Fatalf("s1 登记失败: %v", err)
	}
	r2, err := reg.beginExclusive("s2", "")
	if err != nil {
		t.Fatalf("不同会话应当能并行登记，实际: %v", err)
	}
	if r1.runID == r2.runID {
		t.Fatal("两条运行应有不同的 runID（前端靠它区分归属）")
	}
	if got := reg.activeCount(); got != 2 {
		t.Fatalf("活跃运行数应为 2，实际 %d", got)
	}

	// 停一条不该影响另一条
	if hit, _ := reg.stopSession("s1", true); !hit {
		t.Fatal("停止 s1 应当命中")
	}
	if r1.Kind() != cancelKindHard {
		t.Fatal("s1 应被硬取消")
	}
	if r2.Kind() != "" || r2.Ctx().Err() != nil {
		t.Fatal("停止 s1 不应影响 s2 的运行")
	}

	got := reg.activeSessions()
	if len(got) != 2 || got[0] != "s1" || got[1] != "s2" {
		t.Fatalf("activeSessions 应返回稳定排序的 [s1 s2]，实际 %v", got)
	}
}

// 无会话归属的运行不参与互斥，也不出现在 activeSessions 里。
// 这类运行来自测试与工具场景（零值 App、临时构造的控制块），对它们做互斥没有意义。
func TestBeginExclusiveIgnoresEmptySessionID(t *testing.T) {
	reg := &runRegistry{}
	if _, err := reg.beginExclusive("", ""); err != nil {
		t.Fatalf("无会话归属的运行不应被互斥拦下: %v", err)
	}
	if _, err := reg.beginExclusive("", ""); err != nil {
		t.Fatalf("无会话归属的运行不应被互斥拦下（第二次）: %v", err)
	}
	if got := reg.activeSessions(); len(got) != 0 {
		t.Fatalf("无会话归属的运行不应出现在 activeSessions，实际 %v", got)
	}
}

// runID 必须严格单调递增：前端用它丢弃上一轮运行留下的迟到事件。
// 顺序由「毫秒 + 进程内自增序号」两段共同决定，因此这里按 (毫秒, 序号) 逐段比较，
// 不能只比字符串（位数不同时字符串比较会得出错误结论）。
func TestRunIDMonotonicForStalenessFilter(t *testing.T) {
	reg := &runRegistry{}
	r1, err := reg.beginExclusive("s1", "")
	if err != nil {
		t.Fatalf("登记失败: %v", err)
	}
	r2, err := reg.beginExclusive("s2", "")
	if err != nil {
		t.Fatalf("登记失败: %v", err)
	}
	ms1, seq1, ok1 := parseRunID(r1.runID)
	ms2, seq2, ok2 := parseRunID(r2.runID)
	if !ok1 || !ok2 {
		t.Fatalf("runID 格式应为 run_<毫秒>_<序号>，实际 %q / %q", r1.runID, r2.runID)
	}
	if ms2 < ms1 || (ms2 == ms1 && seq2 <= seq1) {
		t.Fatalf("runID 必须严格递增（后登记的更大）：%q → %q", r1.runID, r2.runID)
	}
}

// parseRunID 解析 run_<毫秒>_<序号>；解析失败时 ok=false。
// 测试用；生产侧只有格式化没有解析（前端另有一份等价实现）。
func parseRunID(id string) (ms int64, seq uint64, ok bool) {
	n, err := fmt.Sscanf(id, "run_%d_%d", &ms, &seq)
	if err != nil || n != 2 {
		return 0, 0, false
	}
	return ms, seq, true
}
