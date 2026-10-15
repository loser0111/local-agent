package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
)

// ===== 未完成 todo 注入系统提示词 =====

func TestWithTodoContextListsOpenItems(t *testing.T) {
	sess := &store.Session{Todos: []store.TodoItem{
		{ID: "1", Text: "写迁移脚本", Status: store.TodoDone},
		{ID: "2", Text: "跑集成测试", Status: store.TodoInProgress},
		{ID: "3", Text: "更新文档", Status: store.TodoPending},
	}}
	got := withTodoContext("BASE", sess)
	if !strings.Contains(got, "未完成的待办") {
		t.Fatalf("应追加待办段: %q", got)
	}
	if !strings.Contains(got, "[~] 跑集成测试") || !strings.Contains(got, "[ ] 更新文档") {
		t.Fatalf("未完成项应列出且带状态标记: %q", got)
	}
	if strings.Contains(got, "写迁移脚本") {
		t.Fatalf("已完成项不该列出: %q", got)
	}
}

func TestWithTodoContextNoopWhenAllDoneOrEmpty(t *testing.T) {
	base := "BASE"
	if got := withTodoContext(base, &store.Session{}); got != base {
		t.Fatalf("无清单应原样返回: %q", got)
	}
	allDone := &store.Session{Todos: []store.TodoItem{{ID: "1", Text: "x", Status: store.TodoDone}}}
	if got := withTodoContext(base, allDone); got != base {
		t.Fatalf("全部完成应原样返回: %q", got)
	}
	if got := withTodoContext(base, nil); got != base {
		t.Fatalf("nil 会话应原样返回: %q", got)
	}
}

// ===== 悬空 tool_call 配平 =====

func toolCallMsg(ids ...string) llm.LLMMessage {
	calls := make([]llm.LLMToolCall, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, llm.LLMToolCall{ID: id, Type: llm.ToolTypeFunction,
			Function: llm.LLMToolFunction{Name: "exec_shell", Arguments: "{}"}})
	}
	return llm.LLMMessage{Role: store.RoleAssistant, ToolCalls: calls}
}

func toolResultMsg(id string) llm.LLMMessage {
	return llm.LLMMessage{Role: store.RoleTool, Content: "ok", ToolCallID: id}
}

// 正常序列（每个 tool_call 都有结果）应原样返回，不做任何改动。
func TestRepairDanglingNoopWhenPaired(t *testing.T) {
	in := []llm.LLMMessage{
		{Role: store.RoleUser, Content: "hi"},
		toolCallMsg("c1"),
		toolResultMsg("c1"),
		{Role: store.RoleAssistant, Content: "done"},
	}
	got := repairDanglingToolCalls(in)
	if len(got) != len(in) {
		t.Fatalf("配平不该改变条数: %d -> %d", len(in), len(got))
	}
}

// 悬空（assistant 有 tool_calls、无结果）应补一条占位，且补在该 assistant 之后。
func TestRepairDanglingAddsPlaceholder(t *testing.T) {
	in := []llm.LLMMessage{
		{Role: store.RoleUser, Content: "hi"},
		toolCallMsg("c1"), // 结果在中断时丢失
	}
	got := repairDanglingToolCalls(in)
	if len(got) != 3 {
		t.Fatalf("应补一条占位结果: %+v", got)
	}
	last := got[len(got)-1]
	if last.Role != store.RoleTool || last.ToolCallID != "c1" {
		t.Fatalf("占位结果应配对 c1: %+v", last)
	}
	if !strings.Contains(last.Content, "中断") {
		t.Fatalf("占位正文应说明中断: %q", last.Content)
	}
}

// 一轮多个 tool_call、只落了部分结果：只补缺的那些，且排在既有结果之后、保持原序。
func TestRepairDanglingPartialPreservesOrder(t *testing.T) {
	in := []llm.LLMMessage{
		toolCallMsg("c1", "c2", "c3"),
		toolResultMsg("c1"), // 只有 c1 落了盘
	}
	got := repairDanglingToolCalls(in)
	if len(got) != 4 {
		t.Fatalf("应补 2 条占位: %+v", got)
	}
	wantIDs := []string{"c1", "c2", "c3"}
	for i, id := range wantIDs {
		if got[i+1].ToolCallID != id {
			t.Fatalf("tool_result 应与 tool_calls 同序，位置 %d 期望 %s，实际 %s", i, id, got[i+1].ToolCallID)
		}
	}
}

// 没有 assistant(tool_calls) 的普通序列不受影响。
func TestRepairDanglingNoToolCalls(t *testing.T) {
	in := []llm.LLMMessage{
		{Role: store.RoleUser, Content: "a"},
		{Role: store.RoleAssistant, Content: "b"},
	}
	got := repairDanglingToolCalls(in)
	if len(got) != 2 {
		t.Fatalf("无 tool_calls 不该改动: %+v", got)
	}
}

// ===== 端到端：中断留下的悬空 tool_call 会被补上，请求能正常发出 =====

// 构造"assistant 已落盘、tool 结果未落盘"的会话（模拟进程在两步之间被杀），
// 跑一次 executeChat，断言真正发往 LLM 的请求里那个 tool_call 有了配对的 tool 结果。
func TestDanglingToolCallRepairedEndToEnd(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"继续完成"}}]}`))
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	// 中断残留：assistant 带 tool_calls，但没有配对的 tool 结果消息
	if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{Role: store.RoleUser, Content: "跑一下"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{
		Role:      store.RoleAssistant,
		ToolCalls: []store.ToolCall{{ID: "call_x", Name: "exec_shell"}},
	}); err != nil {
		t.Fatal(err)
	}
	// 未完成的待办：应被注入系统提示词
	if _, err := app.sessionStore.AddTodo(sess.ID, "接着做第二件事"); err != nil {
		t.Fatal(err)
	}

	res := app.executeChat(sess.ID, "继续", false)
	if res.Error != "" {
		t.Fatalf("不应报错（悬空 tool_call 应被配平）: %s", res.Error)
	}
	if len(bodies) == 0 {
		t.Fatal("mock 未收到请求")
	}
	last := bodies[len(bodies)-1]
	if !strings.Contains(last, "call_x") {
		t.Fatalf("请求里应带上原来的 tool_call: %s", last)
	}
	if !strings.Contains(last, "中断") {
		t.Fatalf("悬空 tool_call 应补上占位结果（正文含'中断'）: %s", last)
	}
	if !strings.Contains(last, "接着做第二件事") {
		t.Fatalf("未完成的待办应被注入系统提示词: %s", last)
	}
}
