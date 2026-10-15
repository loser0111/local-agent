package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wails-tmp/internal/llm"
	"wails-tmp/internal/permission"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 工具调用的并发执行 =====
//
// 一轮里模型可以返回多个 tool_call（如同时读 5 个文件）。原先一律串行，N 次只读 I/O
// 要排成 N 个往返。这里守三件事：
//
//	① 只读工具确实**并发**执行（用可阻塞的假工具测总耗时，而不是只看结果对不对——
//	   串行实现下结果同样正确，只有耗时能区分）；
//	② 结果**按原始下标**回填，tool 结果顺序与 assistant.tool_calls 一致
//	   （协议硬要求，Anthropic 尤其严格）；
//	③ 需要授权的调用**退回串行**——前端每会话只有一个挂起授权槽位，并发弹窗会互相覆盖。

// blockingTool 一个可阻塞的只读假工具：记录并发峰值，并阻塞到 release 被关闭。
//
// 用真工具（read_file 等）测并发是不可靠的：本地磁盘读太快，串行与并发的耗时差不稳定。
// 这里用可控阻塞把"是否真的同时在跑"变成确定性事实。
//
// **峰值必须记在共享的 probe 上**：每个工具是独立实例，各自计数的话每个实例只被调用一次、
// 峰值恒为 1，测不出任何并发（这正是第一版测试写错的地方）。
type blockingTool struct {
	*tool.BaseTool
	probe   *concurrencyProbe
	release chan struct{}
	entered chan struct{}
}

// concurrencyProbe 跨工具实例共享的并发观测器。
type concurrencyProbe struct {
	mu     sync.Mutex
	active int
	peak   int
}

func (p *concurrencyProbe) enter() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active++
	if p.active > p.peak {
		p.peak = p.active
	}
}

func (p *concurrencyProbe) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
}

func (p *concurrencyProbe) Peak() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

func newBlockingTool(name string, probe *concurrencyProbe) *blockingTool {
	return &blockingTool{
		BaseTool: &tool.BaseTool{Name: name, Description: "阻塞式只读测试工具"},
		probe:    probe,
		release:  make(chan struct{}),
		entered:  make(chan struct{}, 16),
	}
}

// PermissionSubject 声明路径类只读主体。
//
// **必须实现**：真 read_file 之所以被判为只读，靠的正是它返回 Action=read 的 PathSubject；
// 少了它，权限引擎会把主体当成普通工具（SubjectTool），read_file 不在 readOnlyTools
// 白名单里，于是落到"询问"——测试会因此看到"只读调用也需要授权"的假象。
func (t *blockingTool) PermissionSubject(args map[string]interface{}) permission.Subject {
	path, _ := args["path"].(string)
	if path == "" {
		path = "/tmp/probe"
	}
	return permission.NewPathSubject(t.GetName(), permission.SubjectActionRead, path)
}

func (t *blockingTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	t.probe.enter()
	defer t.probe.leave()

	select {
	case t.entered <- struct{}{}:
	default:
	}
	select {
	case <-t.release:
	case <-ctx.Done():
	}
	return "ok", nil
}

// newParallelTestApp 造一个带真实工具循环的 App：模型指向 mock 网关，工具视图手工装配。
func newParallelTestApp(t *testing.T) (*App, *store.Session) {
	t.Helper()
	app, sess := newPlanTestApp(t, "http://127.0.0.1:1")
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(50)
	app.permissionGrants = permission.NewGrantStore()
	app.permissionAudit = permission.NewAuditLog(50)
	app.permissionBroker = permission.NewBroker(2 * time.Second)
	return app, sess
}

// TestReadOnlyToolsRunConcurrently：只读工具必须真的并发。
func TestReadOnlyToolsRunConcurrently(t *testing.T) {
	app, sess := newParallelTestApp(t)

	// 造 3 个并发安全的只读工具实例（名字取自 parallelSafeTools）
	probe := &concurrencyProbe{}
	tools := map[string]*blockingTool{}
	registry := map[string]tool.ToolInterface{}
	for _, name := range []string{tool.ToolReadFile, tool.ToolGlob, tool.ToolGrep} {
		bt := newBlockingTool(name, probe)
		tools[name] = bt
		// 包一层 GuardedTool + 放行网关：与生产装配形状一致
		registry[name] = tool.NewGuardedTool(bt, tool.AllowAllEnforcer{})
	}
	view := tool.NewSessionView(registry, nil, []string{tool.ToolReadFile, tool.ToolGlob, tool.ToolGrep}, nil)

	run := app.runs.Begin(sess.ID, "")
	defer app.runs.End(run)

	calls := []llm.LLMToolCall{
		{ID: "c1", Type: llm.ToolTypeFunction, Function: llm.LLMToolFunction{Name: tool.ToolReadFile, Arguments: `{}`}},
		{ID: "c2", Type: llm.ToolTypeFunction, Function: llm.LLMToolFunction{Name: tool.ToolGlob, Arguments: `{}`}},
		{ID: "c3", Type: llm.ToolTypeFunction, Function: llm.LLMToolFunction{Name: tool.ToolGrep, Arguments: `{}`}},
	}

	ar := &agentRun{
		SessionID: sess.ID,
		Dir:       t.TempDir(),
		Changes:   app.fileChanges,
		Diff:      app.diffService,
		Recorder:  &appRecorder{app: app, sessionID: sess.ID},
		UsageLog:  &usageLog{},
	}

	done := make(chan struct{})
	var recs []store.ToolCall
	go func() {
		recs, _ = ar.executeToolCalls(run, view, sess.ID, t.TempDir(), calls)
		close(done)
	}()

	// 等三个调用都进入工具（若实现是串行，这里只能等到 1 个，测试会因超时而失败）
	for i := 0; i < 3; i++ {
		select {
		case <-tools[tool.ToolReadFile].entered:
		case <-tools[tool.ToolGlob].entered:
		case <-tools[tool.ToolGrep].entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("只读工具未并发执行：3 秒内只有 %d 个进入执行", i)
		}
	}
	// 三个工具同时在跑 → 峰值并发应为 3
	if got := probe.Peak(); got < 2 {
		t.Fatalf("只读工具应并发执行（峰值并发 ≥2），实际峰值 %d", got)
	}
	close(tools[tool.ToolReadFile].release)
	close(tools[tool.ToolGlob].release)
	close(tools[tool.ToolGrep].release)
	<-done

	// 结果按原始下标回填
	if len(recs) != 3 {
		t.Fatalf("应回填 3 条记录，实际 %d", len(recs))
	}
	for i, want := range []string{"c1", "c2", "c3"} {
		if recs[i].ID != want {
			t.Fatalf("结果必须按原始下标回填（协议要求同序），第 %d 条应为 %s，实际 %s", i, want, recs[i].ID)
		}
		if recs[i].Status != "success" {
			t.Fatalf("%s 应成功，实际 %s（%s）", recs[i].ID, recs[i].Status, recs[i].Result)
		}
	}
}

// TestWriteToolsStaySerial：写操作（会改工作区）必须保持串行——归因窗口依赖它。
func TestWriteToolsStaySerial(t *testing.T) {
	app, sess := newParallelTestApp(t)

	probe := &concurrencyProbe{}
	wt := newBlockingTool(tool.ToolWriteFile, probe)
	view := tool.NewSessionView(
		map[string]tool.ToolInterface{tool.ToolWriteFile: tool.NewGuardedTool(wt, tool.AllowAllEnforcer{})},
		nil, []string{tool.ToolWriteFile}, nil)

	run := app.runs.Begin(sess.ID, "")
	defer app.runs.End(run)

	ar := &agentRun{
		SessionID: sess.ID,
		Changes:   app.fileChanges,
		Diff:      app.diffService,
		Recorder:  &appRecorder{app: app, sessionID: sess.ID},
		UsageLog:  &usageLog{},
	}

	// 两个写调用：若被并行，第二个会在第一个阻塞时也进入执行（峰值 2）
	calls := []llm.LLMToolCall{
		{ID: "w1", Type: llm.ToolTypeFunction, Function: llm.LLMToolFunction{Name: tool.ToolWriteFile, Arguments: `{}`}},
		{ID: "w2", Type: llm.ToolTypeFunction, Function: llm.LLMToolFunction{Name: tool.ToolWriteFile, Arguments: `{}`}},
	}
	done := make(chan struct{})
	go func() {
		ar.executeToolCalls(run, view, sess.ID, t.TempDir(), calls)
		close(done)
	}()

	// 第一个进入后放行，观察是否出现第二个并发进入
	select {
	case <-wt.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("写工具未进入执行")
	}
	select {
	case <-wt.entered:
		t.Fatal("写工具不该并发执行：第二个调用在第一个未结束时进入了（归因窗口会被破坏）")
	case <-time.After(200 * time.Millisecond):
		// 符合预期：串行
	}
	close(wt.release)
	<-done

	if got := probe.Peak(); got != 1 {
		t.Fatalf("写工具的峰值并发必须为 1，实际 %d", got)
	}
}

// TestParallelDispatchSkipsAskTools：需要授权的调用必须退回串行。
//
// 用 manual 模式 + 一个需要询问的工具：它虽在 parallelSafeTools 里（read_file），
// 但 deny 规则让它落到 ask → 预检应把它挡在并行组之外。
func TestParallelDispatchSkipsAskTools(t *testing.T) {
	app, sess := newParallelTestApp(t)

	probe := &concurrencyProbe{}
	bt := newBlockingTool(tool.ToolReadFile, probe)
	enf := app.bindEnforcer(sess.ID, permission.ModeManual)
	enf.ProjectDir = t.TempDir()
	// 显式 ask 规则：read_file 的任意调用都要询问
	enf.Rules = &permission.RuleSet{Ask: []permission.Rule{{Tool: tool.ToolReadFile, Source: "测试"}}}

	view := tool.NewSessionView(
		map[string]tool.ToolInterface{tool.ToolReadFile: tool.NewGuardedTool(bt, toolEnforcer{enf})},
		nil, []string{tool.ToolReadFile}, nil)

	// 预检应回答"需要询问"
	if view.ParallelSafeTool(tool.ToolReadFile, map[string]interface{}{"path": "/tmp/x"}) {
		t.Fatal("需要授权的调用不该被判为可并发（并发弹窗会互相覆盖）")
	}
	// 无规则时同一工具应可并发
	enf.Rules = nil
	if !view.ParallelSafeTool(tool.ToolReadFile, map[string]interface{}{"path": "/tmp/x"}) {
		t.Fatal("只读且无需授权的调用应可并发")
	}
}

// TestParallelToolResultsKeepProtocolOrder：并发执行后，落库的 tool 结果顺序
// 必须与 assistant.tool_calls 一致，且每个 tool_call 都有配对结果。
func TestParallelToolResultsKeepProtocolOrder(t *testing.T) {
	dir := newTestRepo(t)
	// 造两个只读文件，模型一轮里同时读它们
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("AAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("BBB"), 0o644); err != nil {
		t.Fatal(err)
	}

	var chatCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req llm.LLMReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		var resp llm.LLMResp
		if atomic.AddInt32(&chatCalls, 1) == 1 {
			// 一轮里返回**两个**只读调用（顺序 a 然后 b）
			resp = llm.LLMResp{Choices: []llm.LLMChoice{{
				FinishReason: llm.FinishReasonToolCalls,
				Message: llm.LLMMessage{Role: store.RoleAssistant, ToolCalls: []llm.LLMToolCall{
					{ID: "r1", Type: llm.ToolTypeFunction, Function: llm.LLMToolFunction{
						Name: tool.ToolReadFile, Arguments: `{"path":"a.txt"}`}},
					{ID: "r2", Type: llm.ToolTypeFunction, Function: llm.LLMToolFunction{
						Name: tool.ToolReadFile, Arguments: `{"path":"b.txt"}`}},
				}},
			}}}
		} else {
			resp = llm.LLMResp{Choices: []llm.LLMChoice{{
				FinishReason: llm.FinishReasonStop,
				Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: "两个文件都读完了"},
			}}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(50)
	if _, err := app.sessionStore.UpdateSession(sess.ID, store.SessionPatch{Project: &dir}); err != nil {
		t.Fatal(err)
	}

	res := app.executeChat(sess.ID, "读两个文件", false)
	if res.Error != "" {
		t.Fatalf("不该报错: %s", res.Error)
	}
	if res.Reply != "两个文件都读完了" {
		t.Fatalf("应拿到最终回复，实际 %q", res.Reply)
	}

	saved, err := app.sessionStore.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 找出 assistant(tool_calls) 与其后的 tool 结果
	var wantOrder []string
	var gotOrder []string
	for _, m := range saved.Messages {
		if m.Role == store.RoleAssistant {
			for _, tc := range m.ToolCalls {
				wantOrder = append(wantOrder, tc.ID)
			}
		}
		if m.Role == store.RoleTool {
			gotOrder = append(gotOrder, m.ToolCallID)
		}
	}
	if len(wantOrder) != 2 {
		t.Fatalf("应有 2 个 tool_call，实际 %v", wantOrder)
	}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("每个 tool_call 都应有配对结果：call=%v result=%v", wantOrder, gotOrder)
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("tool 结果顺序必须与 tool_calls 一致（协议要求）：want=%v got=%v", wantOrder, gotOrder)
		}
	}
	// 内容也要对得上：r1 必须是 a.txt 的内容
	for _, m := range saved.Messages {
		if m.Role == store.RoleTool && m.ToolCallID == "r1" {
			if !strings.Contains(m.Content, "AAA") {
				t.Fatalf("r1 应拿到 a.txt 内容，实际 %q", m.Content)
			}
		}
	}
}
