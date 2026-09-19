package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ===== P3 第 2 步：权限的"只判定不等待"入口 + 子代理网关 =====

// newTestSubagentEnforcer 造一个可用的子代理网关。
//
// 关键设计：把权限 broker 的超时压到 80ms。若实现错误地走了阻塞等待（即调了父的
// Enforce 而不是 Decide），测试会在毫秒级拿到"等待用户授权超时"——**错误因此快速
// 且可区分**，而不是把测试挂满默认的 5 分钟。这类"间歇性卡住"的错误最难查，
// 所以要在测试层面把它变成确定性失败。
func newTestSubagentEnforcer(t *testing.T, mode Mode, rules *RuleSet) (*App, *permissionEnforcer, *subagentEnforcer) {
	t.Helper()
	app := &App{permissionBroker: newPermissionBroker(80 * time.Millisecond)}
	app.ensurePermissionState()
	enf := &permissionEnforcer{
		app:        app,
		sessionID:  "parent-sess",
		projectDir: t.TempDir(),
		mode:       mode,
		rules:      rules,
	}
	return app, enf, &subagentEnforcer{parent: enf}
}

// ★ 本文件最重要的一条：需要用户授权的操作，子代理必须**立即**拒绝，而不是阻塞等待。
//
// 它同时排除了"调父 Enforce 导致等超时"这个最容易犯的错——那种实现下错误信息会是
// "等待用户授权超时"，而不是"需要用户授权"。两者含义完全不同：前者是系统故障，
// 后者是给模型的可用反馈（模型可以改路子或在结论里申请授权）。
func TestSubagentDeclinesWithoutBlocking(t *testing.T) {
	_, _, sub := newTestSubagentEnforcer(t, ModeManual, nil)
	tool := &CLITool{BaseTool: &BaseTool{Name: "exec_shell"}}

	start := time.Now()
	err := sub.Enforce(context.Background(), tool, map[string]interface{}{"cmd": "python fetch.py"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("需要用户授权的操作应被拒绝，而不是放行")
	}
	msg := err.Error()
	if !strings.Contains(msg, "需要用户授权") {
		t.Fatalf("错误信息应说明需要授权，实际: %v", err)
	}
	if strings.Contains(msg, "超时") {
		t.Fatalf("不该走阻塞等待（说明调了父的 Enforce 而不是 Decide）: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("应在毫秒级返回，实际耗时 %v", elapsed)
	}
	if got := sub.Declined(); len(got) != 1 || !strings.Contains(got[0], "exec_shell") {
		t.Fatalf("被挡下的操作应记入 Declined（用户要能看到它想做什么），实际 %v", got)
	}
}

// 父网关允许的，子代理照常放行
func TestSubagentAllowsWhatParentAllows(t *testing.T) {
	_, _, sub := newTestSubagentEnforcer(t, ModeManual, nil)
	tool := &CLITool{BaseTool: &BaseTool{Name: "exec_shell"}}

	if err := sub.Enforce(context.Background(), tool, map[string]interface{}{"cmd": "ls -la"}); err != nil {
		t.Fatalf("只读命令应放行: %v", err)
	}
	if got := sub.Declined(); len(got) != 0 {
		t.Fatalf("放行不该记入 Declined: %v", got)
	}
}

// 硬性拒绝与"缺授权"是两回事：前者不该进 Declined。
// 用户需要能区分"这东西被规则挡死了"和"子代理缺一次授权"——后者的处置方式完全不同。
func TestSubagentHardDenyIsNotDeclined(t *testing.T) {
	_, _, sub := newTestSubagentEnforcer(t, ModeManual, &RuleSet{
		Deny: []Rule{{Tool: "exec_shell", Source: "测试规则"}},
	})
	tool := &CLITool{BaseTool: &BaseTool{Name: "exec_shell"}}

	err := sub.Enforce(context.Background(), tool, map[string]interface{}{"cmd": "python fetch.py"})
	if err == nil || !strings.Contains(err.Error(), "权限拒绝") {
		t.Fatalf("命中 deny 规则应明确拒绝，实际: %v", err)
	}
	if got := sub.Declined(); len(got) != 0 {
		t.Fatalf("硬性拒绝不是缺授权，不该记入 Declined: %v", got)
	}
}

// 调用错误（如命令为空）不是权限结论，也不该记入 Declined
func TestSubagentCallErrorIsNotDeclined(t *testing.T) {
	_, _, sub := newTestSubagentEnforcer(t, ModeManual, nil)
	tool := &CLITool{BaseTool: &BaseTool{Name: "exec_shell"}}

	err := sub.Enforce(context.Background(), tool, map[string]interface{}{"cmd": "   "})
	if err == nil || !strings.Contains(err.Error(), "命令为空") {
		t.Fatalf("空命令应报调用错误，实际: %v", err)
	}
	if got := sub.Declined(); len(got) != 0 {
		t.Fatalf("调用错误不是缺授权: %v", got)
	}
}

// 没有父网关时 fail closed：宁可拒绝，也不能静默放行
func TestSubagentWithoutParentFailsClosed(t *testing.T) {
	tool := &CLITool{BaseTool: &BaseTool{Name: "exec_shell"}}
	sub := &subagentEnforcer{}
	if err := sub.Enforce(context.Background(), tool, map[string]interface{}{"cmd": "ls"}); err == nil {
		t.Fatal("没有父网关时应拒绝，不能放行")
	}
	// nil 接收者也要安全（构造期可能拿到 nil）
	var nilSub *subagentEnforcer
	if got := nilSub.Declined(); got != nil {
		t.Fatalf("nil 接收者的 Declined 应返回 nil，实际 %v", got)
	}
}

// Decide 与 Enforce 对同一调用的结论必须一致，且审计的归属明确：
// Decide 不写审计（由调用方决定怎么记），Enforce 写。
//
// 这条守的是重构本身——把 Enforce 拆成 Decide + 审计 + 分派之后，行为不能变。
func TestDecideMatchesEnforceAndAuditBelongs(t *testing.T) {
	app, enf, _ := newTestSubagentEnforcer(t, ModeManual, nil)
	tool := &CLITool{BaseTool: &BaseTool{Name: "exec_shell"}}

	subject, verdict, callErr := enf.Decide(tool, map[string]interface{}{"cmd": "ls"})
	if callErr != nil {
		t.Fatalf("只读命令不该是调用错误: %v", callErr)
	}
	if verdict.Decision != DecisionAllow {
		t.Fatalf("只读命令应判 allow，实际 %s（%s）", verdict.Decision, verdict.Reason)
	}
	if subject.Kind != SubjectCommand || subject.Tool != "exec_shell" {
		t.Fatalf("主体应保留工具名与类型，实际 %+v", subject)
	}
	if n := len(app.permissionAudit.List("parent-sess")); n != 0 {
		t.Fatalf("Decide 自己不该写审计（否则子代理会双重记账），实际 %d 条", n)
	}

	if err := enf.Enforce(context.Background(), tool, map[string]interface{}{"cmd": "ls"}); err != nil {
		t.Fatalf("Enforce 同样应放行: %v", err)
	}
	if n := len(app.permissionAudit.List("parent-sess")); n != 1 {
		t.Fatalf("Enforce 应写一条审计，实际 %d 条", n)
	}

	// deny 路径：两边结论也要一致
	_, denied, _ := newTestSubagentEnforcer(t, ModeManual, &RuleSet{
		Deny: []Rule{{Tool: "exec_shell", Source: "测试规则"}},
	})
	if _, v, _ := denied.Decide(tool, map[string]interface{}{"cmd": "python x.py"}); v.Decision != DecisionDeny {
		t.Fatalf("命中 deny 规则时 Decide 应判 deny，实际 %s", v.Decision)
	}
	if err := denied.Enforce(context.Background(), tool, map[string]interface{}{"cmd": "python x.py"}); err == nil {
		t.Fatal("命中 deny 规则时 Enforce 应报错")
	}
}

// 子代理挡下时写的审计，stage 必须能与"用户拒绝"区分开
func TestSubagentDeclineIsAuditedWithItsOwnStage(t *testing.T) {
	app, _, sub := newTestSubagentEnforcer(t, ModeManual, nil)
	tool := &CLITool{BaseTool: &BaseTool{Name: "exec_shell"}}

	if err := sub.Enforce(context.Background(), tool, map[string]interface{}{"cmd": "python fetch.py"}); err == nil {
		t.Fatal("应被拒绝")
	}
	entries := app.permissionAudit.List("parent-sess")
	if len(entries) != 1 {
		t.Fatalf("应写一条审计，实际 %d 条", len(entries))
	}
	if entries[0].Stage != StageSubagentDeclined {
		t.Fatalf("stage 应为 %s（否则与用户的真实拒绝混在一起），实际 %s",
			StageSubagentDeclined, entries[0].Stage)
	}
	if entries[0].Decision != DecisionDeny.String() {
		t.Fatalf("审计里的结论应为拒绝，实际 %s", entries[0].Decision)
	}
}

// ===== P3 第 3–5 步：spawn_agent 契约、隔离、取消级联、列表接口 =====

// fakeLLM 可编排的假网关：按顺序给出预设响应，并记下收到的每个请求体。
//
// 记请求体是这些测试的一半价值所在——"子代理的工具列表里没有 ask_user/spawn_agent"、
// "第一次请求带上了 user 消息"这两条，只有在 HTTP 边界上断言才作数。
type fakeLLM struct {
	server *httptest.Server

	mu        sync.Mutex
	responses []string
	bodies    []string
	block     bool
}

func newFakeLLM(t *testing.T, responses ...string) *fakeLLM {
	t.Helper()
	f := &fakeLLM{responses: responses}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeLLM) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	f.mu.Lock()
	f.bodies = append(f.bodies, string(body))
	block := f.block
	resp := ""
	if len(f.responses) > 0 {
		resp = f.responses[0]
		f.responses = f.responses[1:]
	}
	f.mu.Unlock()

	// 一直挂着直到客户端取消：测"父运行硬取消 → 子代理立即结束"要有一个卡住的在途请求
	if block {
		<-r.Context().Done()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(resp))
}

func (f *fakeLLM) setBlock(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block = v
}

func (f *fakeLLM) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func (f *fakeLLM) bodyAt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.bodies) {
		return ""
	}
	return f.bodies[i]
}

// fakeCall 一次工具调用（arguments 是 JSON 文本）
type fakeCall struct {
	id   string
	name string
	args string
}

// fakeChatResp 造一个 OpenAI 非流式响应体。
// 用 json.Marshal 而不是手拼字符串：参数里的引号与换行很容易把 JSON 拼坏，
// 而拼坏的 JSON 表现为"模型什么都没说"，排查起来会绕远路。
func fakeChatResp(t *testing.T, content string, calls ...fakeCall) string {
	t.Helper()
	msg := map[string]interface{}{"role": "assistant", "content": content}
	finish := "stop"
	if len(calls) > 0 {
		arr := make([]map[string]interface{}, 0, len(calls))
		for _, c := range calls {
			arr = append(arr, map[string]interface{}{
				"id": c.id, "type": "function",
				"function": map[string]interface{}{"name": c.name, "arguments": c.args},
			})
		}
		msg["tool_calls"] = arr
		finish = "tool_calls"
	}
	body := map[string]interface{}{
		"choices": []map[string]interface{}{{"index": 0, "finish_reason": finish, "message": msg}},
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// subagentEnv 一次子代理测试的运行环境
type subagentEnv struct {
	app    *App
	dir    string
	model  Model
	parent *Session
	llm    *fakeLLM
}

// newSubagentTestEnv 造一个能真正跑一次子代理的环境：临时数据目录 + 假网关 + 真实会话存储。
// dir 传 newTestRepo(t) 就是一个真实的 git 仓库（diff 与回退都依赖它）。
func newSubagentTestEnv(t *testing.T, dir string, mode Mode, responses ...string) *subagentEnv {
	t.Helper()
	llm := newFakeLLM(t, responses...)
	baseDir := t.TempDir()
	app := &App{
		baseDir:      baseDir,
		sessionStore: NewSessionStore(filepath.Join(baseDir, "sessions")),
		toolStore:    NewToolStore(filepath.Join(baseDir, "tools.json")),
		diffService:  NewDiffService(),
		fileChanges:  NewFileChangeLog(50),
		reqLog:       &llmRequestLog{},
		runs:         &runRegistry{},
		subagents:    newSubagentTracker(),
	}
	app.toolManager = NewToolManager(app.toolStore, nil) // 技能为空：这些测试不涉及技能
	app.modelStore = NewModelStore(filepath.Join(baseDir, "models.json"))
	if err := app.modelStore.AddModel(Model{Name: "m1", URL: llm.server.URL, Protocol: "openai"}); err != nil {
		t.Fatalf("添加模型失败: %v", err)
	}
	model, err := app.modelStore.GetModelForCall("m1")
	if err != nil {
		t.Fatalf("取模型失败: %v", err)
	}
	parent, err := app.sessionStore.CreateSession(SessionConfig{
		Title:          "主会话",
		Project:        dir,
		Model:          "m1",
		PermissionMode: string(mode),
	})
	if err != nil {
		t.Fatalf("创建父会话失败: %v", err)
	}
	return &subagentEnv{app: app, dir: dir, model: model, parent: parent, llm: llm}
}

func toolDefNames(defs []LLMTool) map[string]bool {
	out := map[string]bool{}
	for _, d := range defs {
		out[d.Function.Name] = true
	}
	return out
}

// waitUntil 轮询等待条件成立（用于"等子代理真的发出请求"这类时序前提）。
// 名字不叫 waitFor：runcontrol_test.go 里已有一个同名但语义不同的辅助函数（等 int32 标志位）。
func waitUntil(t *testing.T, cond func() bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// 子代理工具的参数契约与结果载荷：
// 缺 task 要报错、max_turns 两种数字形态都要认、结论/文件/被挡下的操作都要回到主会话。
func TestSpawnAgentToolContract(t *testing.T) {
	var gotTask string
	var gotTurns int
	tool := newSpawnAgentTool(buildContext{
		spawner: func(ctx context.Context, task string, maxTurns int) (*SubagentResult, error) {
			gotTask, gotTurns = task, maxTurns
			return &SubagentResult{
				RunID:     "sub_1",
				Status:    subagentStatusCompleted,
				Summary:   "结论正文",
				Files:     []string{"x.go"},
				ToolCalls: 3,
				Declined:  []string{"exec_shell: python x.py"},
			}, nil
		},
	})

	if _, err := tool.Execute(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("缺 task 应报错")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"task": "   "}); err == nil {
		t.Fatal("空白 task 应报错")
	}

	// JSON 解析出来的是 float64（真实链路走的就是这一种）
	out, err := tool.Execute(context.Background(), map[string]interface{}{"task": "查一下", "max_turns": float64(7)})
	if err != nil {
		t.Fatalf("正常调用不应报错: %v", err)
	}
	if gotTask != "查一下" || gotTurns != 7 {
		t.Fatalf("参数应原样透传，实际 task=%q turns=%d", gotTask, gotTurns)
	}
	// 直接调用方可能给 int，也要认
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"task": "查一下", "max_turns": 3}); err != nil {
		t.Fatalf("int 形态的 max_turns 不应报错: %v", err)
	}
	if gotTurns != 3 {
		t.Fatalf("int 形态应被识别，实际 %d", gotTurns)
	}

	// 结果载荷：这三样是子代理回到主会话的**全部**信息，缺一样用户就少一条线索
	for _, want := range []string{"sub_1", "结论正文", "x.go", "exec_shell"} {
		if !strings.Contains(out, want) {
			t.Errorf("工具结果里应包含 %q，实际 %s", want, out)
		}
	}

	// 没有 spawner 时不注册——这正是"子代理不能再派生子代理"的实现方式
	if _, ok := newBuiltinTool(&ToolSource{Name: toolSpawnAgent, Kind: SourceBuiltin}, buildContext{}); ok {
		t.Fatal("没有 spawner 时 spawn_agent 不应被注册")
	}
}

// 曝光与排除：主会话直出 spawn_agent，子代理视图里既没有它也没有 ask_user。
func TestSpawnAgentExposureAndExclusion(t *testing.T) {
	tm := NewToolManager(NewToolStore(filepath.Join(t.TempDir(), "tools.json")), nil)
	spawner := func(ctx context.Context, task string, maxTurns int) (*SubagentResult, error) {
		return &SubagentResult{Status: subagentStatusCompleted}, nil
	}

	// 主会话：系统提示词点名了 spawn_agent，工具列表里就必须有它的 schema——
	// 否则模型只能照着名字瞎猜参数（这是"提示词与工具列表不一致"的经典来源）
	main := tm.BuildView(context.Background(), BuildOptions{
		Enforcer:   AllowAllEnforcer{},
		SpawnAgent: spawner,
	})
	if !toolDefNames(main.GetToolsForLLM())[toolSpawnAgent] {
		t.Fatalf("spawn_agent 应直出给模型，实际 %v", main.GetToolsForLLM())
	}

	// 子代理：不给 spawner + 显式排除 ask_user
	sub := tm.BuildView(context.Background(), BuildOptions{
		Enforcer:     AllowAllEnforcer{},
		ExcludeTools: []string{toolAskUser},
	})
	names := toolDefNames(sub.GetToolsForLLM())
	for _, name := range []string{toolSpawnAgent, toolAskUser} {
		if names[name] {
			t.Errorf("%s 不该出现在子代理的工具列表里", name)
		}
		if _, err := sub.ExecuteTool(name, map[string]interface{}{}); err == nil {
			t.Errorf("%s 在子代理视图里不该能执行", name)
		}
	}
}

// ★ 这个功能存在的理由：子代理跑完，主会话上下文里只多了一份结论。
// 顺带钉住三件容易做错的事：任务作为第一条 user 消息落库（否则请求里只剩 system，
// 网关会直接拒绝）、子代理的工具列表里没有 ask_user/spawn_agent、改动不归因给父会话。
func TestSubagentRunsIsolatedFromParentContext(t *testing.T) {
	dir := newTestRepo(t)
	env := newSubagentTestEnv(t, dir, ModeAcceptEdits,
		fakeChatResp(t, "", fakeCall{id: "c1", name: toolWriteFile, args: `{"path":"sub.txt","content":"hi"}`}),
		fakeChatResp(t, "已完成：写了 sub.txt"),
	)

	run := env.app.runs.begin(env.parent.ID, "")
	before, _ := env.app.sessionStore.GetSession(env.parent.ID)
	res, err := env.app.runSubagent(run, env.parent, dir, &env.model, "写一个 sub.txt", 0)
	if err != nil {
		t.Fatalf("派生子代理失败: %v", err)
	}
	if res.Status != subagentStatusCompleted {
		t.Fatalf("状态应为 completed，实际 %s（err=%s）", res.Status, res.Error)
	}
	if !strings.Contains(res.Summary, "已完成") {
		t.Fatalf("结论应回传，实际 %q", res.Summary)
	}
	if len(res.Files) != 1 || res.Files[0] != "sub.txt" {
		t.Fatalf("应报告它改过的文件，实际 %v", res.Files)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub.txt")); err != nil {
		t.Fatalf("子代理应真的写出了文件: %v", err)
	}

	// ① 主会话消息一条不多：中间过程留在子代理自己的会话里
	after, _ := env.app.sessionStore.GetSession(env.parent.ID)
	if len(after.Messages) != len(before.Messages) {
		t.Fatalf("主会话消息数不该变：%d → %d", len(before.Messages), len(after.Messages))
	}

	// ② 子代理会话：第一条必须是任务本身
	child, err := env.app.sessionStore.GetSession(res.RunID)
	if err != nil {
		t.Fatalf("子代理会话应存在: %v", err)
	}
	if child.ParentID != env.parent.ID {
		t.Fatalf("子代理会话应记住父会话，实际 %q", child.ParentID)
	}
	if len(child.Messages) < 2 {
		t.Fatalf("子代理会话不该是空的，实际 %d 条", len(child.Messages))
	}
	if child.Messages[0].Role != RoleUser || child.Messages[0].Content != "写一个 sub.txt" {
		t.Fatalf("第一条消息应是任务原文，实际 %+v", child.Messages[0])
	}
	if child.Subagent == nil || child.Subagent.Status != subagentStatusCompleted {
		t.Fatalf("跑完应把概况写回会话（重启后仍要能查看），实际 %+v", child.Subagent)
	}

	// ③ HTTP 边界：第一次请求必须带 messages，且工具列表里没有子代理禁用项
	var req struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(env.llm.bodyAt(0)), &req); err != nil {
		t.Fatalf("解析子代理的请求失败: %v", err)
	}
	if len(req.Messages) == 0 {
		t.Fatal("子代理的请求不能只有 system（网关会以 messages 不能为空拒绝）")
	}
	hasUser := false
	for _, m := range req.Messages {
		if m.Role == RoleUser {
			hasUser = true
		}
	}
	if !hasUser {
		t.Fatal("子代理的请求里必须有 user 消息，任务就是它")
	}
	for _, tool := range req.Tools {
		if tool.Function.Name == toolAskUser || tool.Function.Name == toolSpawnAgent {
			t.Errorf("子代理的工具列表里不该有 %s", tool.Function.Name)
		}
	}

	// ④ 归因隔离：它改的文件不能算父会话的改动，且必须被登记为待剔除
	if got := env.app.diffService.TurnTouchedSnapshot(env.parent.ID); len(got) != 0 {
		t.Errorf("父会话本轮不该有归因（子代理的改动不属于它），实际 %v", got)
	}
	if got := env.app.diffService.Touched(env.parent.ID); len(got) != 0 {
		t.Errorf("父会话累计归因里不该有子代理的文件，实际 %v", got)
	}
	excluded := run.takeExcludedPaths(env.parent.ID)
	if len(excluded) != 1 || excluded[0] != "sub.txt" {
		t.Fatalf("子代理改过、父会话没碰过的文件应登记为待剔除，实际 %v", excluded)
	}

	// 剔除落到 DiffService 上的效果：这两个集合都清掉之后，父会话的 diff 不再包含它
	env.app.diffService.DropTouched(env.parent.ID, excluded)
	if got := env.app.diffService.TurnTouchedSnapshot(env.parent.ID); len(got) != 0 {
		t.Errorf("剔除后父会话本轮归因应为空，实际 %v", got)
	}
}

// 子代理改过、但父会话在派生之前就碰过的文件不算"待剔除"——否则父会话自己的改动
// 会从 diff 面板里消失。
func TestDelegatedPathsKeepsParentTouched(t *testing.T) {
	got := delegatedPaths([]string{"a.go"}, []string{"a.go", "b.go", ""})
	if len(got) != 1 || got[0] != "b.go" {
		t.Fatalf("只该剔除父会话没碰过的，实际 %v", got)
	}
	if delegatedPaths(nil, nil) != nil {
		t.Fatal("没有子代理改动时应返回 nil")
	}
	if got := delegatedPaths([]string{"a.go"}, []string{"a.go"}); len(got) != 0 {
		t.Fatalf("全都被父会话碰过时应为空，实际 %v", got)
	}
}

// 待剔除队列按会话分组：父会话与子代理共用同一个 runControl（取消要级联），
// 若做成一个共享队列，子代理的循环会先把父会话的那份取走，而这种丢失是静默的。
func TestExcludedPathsQueueIsPerSession(t *testing.T) {
	run := &runControl{soft: make(chan struct{})}
	run.noteExcludedPaths("parent", []string{"x.go"})
	run.noteExcludedPaths("child", []string{"y.go"})

	if got := run.takeExcludedPaths("child"); len(got) != 1 || got[0] != "y.go" {
		t.Fatalf("子代理的队列不该看到父会话的路径，实际 %v", got)
	}
	if got := run.takeExcludedPaths("parent"); len(got) != 1 || got[0] != "x.go" {
		t.Fatalf("父会话的路径应还在，实际 %v", got)
	}
	// 取走即清空，避免每次工具调用都重复剔除一遍
	if got := run.takeExcludedPaths("parent"); got != nil {
		t.Fatalf("取出后应清空，实际 %v", got)
	}
	// 空入参不建队列
	run.noteExcludedPaths("parent", nil)
	if got := run.takeExcludedPaths("parent"); got != nil {
		t.Fatalf("空入参不该留下记录，实际 %v", got)
	}

	// nil 接收者安全：测试里会直接构造零值 App，那条路径不能 panic
	var nilRun *runControl
	nilRun.noteExcludedPaths("s", []string{"z.go"})
	if got := nilRun.takeExcludedPaths("s"); got != nil {
		t.Fatalf("nil 接收者应返回 nil，实际 %v", got)
	}
}

// 派生的总名额挂在**运行**上：计划执行按步骤重建工具视图，挂在闭包里会被逐步重置。
func TestSubagentTotalLimitPerRun(t *testing.T) {
	run := &runControl{soft: make(chan struct{})}
	for i := 0; i < subagentMaxTotal; i++ {
		if !run.claimSubagent(subagentMaxTotal) {
			t.Fatalf("第 %d 个名额不该被拒", i+1)
		}
	}
	if run.claimSubagent(subagentMaxTotal) {
		t.Fatal("超过上限后应拒绝派生")
	}
	var nilRun *runControl
	if nilRun.claimSubagent(subagentMaxTotal) {
		t.Fatal("nil 运行不该给出名额")
	}
}

// 轮次上限：max_turns 会被夹到 20/30 之间，循环靠它停下来（否则一个跑偏的子代理会一直烧）。
func TestSubagentTurnLimit(t *testing.T) {
	dir := newTestRepo(t)
	scripts := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		scripts = append(scripts, fakeChatResp(t, "", fakeCall{
			id: fmt.Sprintf("c%d", i), name: toolListDir, args: `{}`,
		}))
	}

	env := newSubagentTestEnv(t, dir, ModeAcceptEdits, scripts...)
	run := env.app.runs.begin(env.parent.ID, "")
	if _, err := env.app.runSubagent(run, env.parent, dir, &env.model, "一直列目录", 3); err != nil {
		t.Fatalf("派生子代理失败: %v", err)
	}
	if got := env.llm.count(); got != 3 {
		t.Fatalf("max_turns=3 时应只发 3 次请求，实际 %d", got)
	}

	// 超过硬上限的取值一律夹到默认轮次，而不是照单全收
	env2 := newSubagentTestEnv(t, dir, ModeAcceptEdits, scripts...)
	run2 := env2.app.runs.begin(env2.parent.ID, "")
	if _, err := env2.app.runSubagent(run2, env2.parent, dir, &env2.model, "一直列目录", 999); err != nil {
		t.Fatalf("派生子代理失败: %v", err)
	}
	if got := env2.llm.count(); got != subagentDefaultTurns {
		t.Fatalf("超上限应夹到 %d 轮，实际 %d", subagentDefaultTurns, got)
	}
}

// 取消要级联：父运行硬取消后，子代理不能自己跑下去（它的 ctx 就是父运行的）。
func TestSubagentCancelCascade(t *testing.T) {
	dir := newTestRepo(t)
	env := newSubagentTestEnv(t, dir, ModeAcceptEdits, fakeChatResp(t, "不该跑到的回复"))
	env.llm.setBlock(true) // 请求一直挂着，只有取消能让它结束

	run := env.app.runs.begin(env.parent.ID, "")
	type outcome struct {
		res *SubagentResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := env.app.runSubagent(run, env.parent, dir, &env.model, "慢慢查", 0)
		done <- outcome{res: res, err: err}
	}()

	// 等它真的发出请求再取消：太早取消测到的就只是"还没开始就跑完了"
	if !waitUntil(t, func() bool { return env.llm.count() > 0 }, 2*time.Second) {
		t.Fatal("子代理没有发出请求，测试前提不成立")
	}
	run.markHard()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("取消不该作为错误返回: %v", got.err)
		}
		if got.res == nil {
			t.Fatal("取消后仍应返回结果")
		}
		if got.res.Status != subagentStatusCancelled {
			t.Fatalf("状态应为 cancelled，实际 %s（err=%s）", got.res.Status, got.res.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("父运行硬取消后子代理应在 2 秒内结束（级联没生效）")
	}
}

// 需要授权的操作：子代理必须立即拒绝、把文件留着不动，并让用户看得见它想做什么。
func TestSubagentDeclinedSurfacesAndAudits(t *testing.T) {
	dir := newTestRepo(t)
	// manual 模式下项目内的写操作也要确认；子代理无法代为确认
	env := newSubagentTestEnv(t, dir, ModeManual,
		fakeChatResp(t, "", fakeCall{id: "c1", name: toolWriteFile, args: `{"path":"x.txt","content":"hi"}`}),
		fakeChatResp(t, "没写成功：需要用户授权"),
	)

	start := time.Now()
	res, err := env.app.runSubagent(env.app.runs.begin(env.parent.ID, ""), env.parent, dir, &env.model, "写一个 x.txt", 0)
	if err != nil {
		t.Fatalf("派生子代理失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("被挡下时应立即返回，不该等到权限超时，实际耗时 %v", elapsed)
	}
	if len(res.Declined) == 0 {
		t.Fatal("需要授权的操作应记进 Declined（用户要能看到它想做什么）")
	}
	if !strings.Contains(res.Declined[0], toolWriteFile) {
		t.Fatalf("Declined 里应指明是哪个工具，实际 %v", res.Declined)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err == nil {
		t.Fatal("被挡下的写操作不该真的落盘")
	}

	// 审计里能区分"子代理被系统挡下"与"用户拒绝"——不能把系统的自动行为记成用户的决定
	entries := env.app.permissionAudit.List(res.RunID)
	found := false
	for _, e := range entries {
		if e.Stage == StageSubagentDeclined {
			found = true
		}
	}
	if !found {
		t.Fatalf("应有一条 %s 的审计，实际 %+v", StageSubagentDeclined, entries)
	}
}

// 列表与详情接口：跑完的从会话文件里读、正在跑的从内存注册表读，两边合起来。
func TestListSubagentsAndMessages(t *testing.T) {
	dir := newTestRepo(t)
	env := newSubagentTestEnv(t, dir, ModeAcceptEdits, fakeChatResp(t, "查完了，一切正常"))

	if list, err := env.app.ListSubagents(env.parent.ID); err != nil || len(list) != 0 {
		t.Fatalf("还没派生过时应为空，实际 %v (err=%v)", list, err)
	}

	res, err := env.app.runSubagent(env.app.runs.begin(env.parent.ID, ""), env.parent, dir, &env.model, "查一下构建", 0)
	if err != nil {
		t.Fatalf("派生子代理失败: %v", err)
	}

	list, err := env.app.ListSubagents(env.parent.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("应列出 1 个子代理，实际 %v (err=%v)", list, err)
	}
	got := list[0]
	if got.RunID != res.RunID || got.ParentID != env.parent.ID {
		t.Fatalf("概况的归属不对: %+v", got)
	}
	if got.Status != subagentStatusCompleted || got.Summary == "" || got.EndedAt == 0 {
		t.Fatalf("跑完的概况应完整（状态/结论/结束时间）: %+v", got)
	}
	if got.Task != "查一下构建" || !strings.Contains(got.Title, "子代理") {
		t.Fatalf("概况应带上任务原文: %+v", got)
	}

	msgs, err := env.app.GetSubagentMessages(res.RunID)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("应能取到子代理的消息流，实际 %v (err=%v)", msgs, err)
	}
	if msgs[0].Role != RoleUser {
		t.Fatalf("第一条应是任务，实际 %s", msgs[0].Role)
	}
	if _, err := env.app.GetSubagentMessages(env.parent.ID); err == nil {
		t.Fatal("主会话不该被当成子代理会话")
	}

	// 应用在它跑动中被关掉：会话在、概况没写完 → 报"已中断"，
	// 而不是继续显示"运行中"（那会让人一直等一个不会回来的东西）
	orphan, err := env.app.sessionStore.CreateSession(SessionConfig{
		Title: "子代理：半途", Project: dir, ParentID: env.parent.ID,
	})
	if err != nil {
		t.Fatalf("创建孤儿子代理会话失败: %v", err)
	}
	list2, _ := env.app.ListSubagents(env.parent.ID)
	var orphanInfo *SubagentInfo
	for i := range list2 {
		if list2[i].RunID == orphan.ID {
			orphanInfo = &list2[i]
		}
	}
	if orphanInfo == nil || orphanInfo.Status != subagentStatusInterrupted {
		t.Fatalf("没有概况的子代理会话应报已中断，实际 %+v", orphanInfo)
	}

	// 删主会话要连子代理一起删：它们不出现在会话列表里，删完父会话就再没有入口能发现它们，
	// 留下的 checkpoint ref 会永久残留在用户的 .git 里
	if err := env.app.DeleteSession(env.parent.ID); err != nil {
		t.Fatalf("删除会话失败: %v", err)
	}
	for _, id := range []string{res.RunID, orphan.ID} {
		if _, err := env.app.sessionStore.GetSession(id); err == nil {
			t.Fatalf("删除主会话时应连子代理会话 %s 一起删掉", id)
		}
	}
}

// 运行中注册表：进度更新、按父会话过滤、交出去的必须是副本。
func TestSubagentTrackerProgressAndCopies(t *testing.T) {
	// nil 接收者安全（零值 App 的路径）
	var nilTracker *subagentTracker
	nilTracker.begin(&SubagentInfo{RunID: "r1"})
	nilTracker.finish("r1")
	if got := nilTracker.list("p"); got != nil {
		t.Fatalf("nil 注册表应返回空，实际 %v", got)
	}
	if got := nilTracker.get("r1"); got != nil {
		t.Fatalf("nil 注册表应返回 nil，实际 %v", got)
	}

	tr := newSubagentTracker()
	tr.begin(&SubagentInfo{RunID: "r1", ParentID: "p", Status: subagentStatusRunning})
	tr.toolStart("r1", toolReadFile)
	tr.toolStart("r1", toolGrep)

	snap := tr.get("r1")
	if snap.Step != 2 || snap.CurrentTool != toolGrep {
		t.Fatalf("进度应记到第 2 步且当前工具为 grep，实际 %+v", snap)
	}
	// 交出去的必须是副本：这份数据边跑边变，交指针会让"当时推给前端的那一份"事后跟着变
	snap.Step = 99
	if got := tr.get("r1"); got.Step != 2 {
		t.Fatalf("取到的应是副本，实际 %+v", got)
	}

	// 别的会话的在跑任务不该混进来
	tr.begin(&SubagentInfo{RunID: "r2", ParentID: "other"})
	if got := tr.list("p"); len(got) != 1 {
		t.Fatalf("只应列出本会话的，实际 %v", got)
	}
	tr.toolEnd("r1")
	if got := tr.get("r1"); got.CurrentTool != "" {
		t.Fatalf("工具跑完应清掉当前工具，实际 %q", got.CurrentTool)
	}
	tr.finish("r1")
	if got := tr.get("r1"); got != nil {
		t.Fatalf("跑完应摘掉，实际 %+v", got)
	}
}
