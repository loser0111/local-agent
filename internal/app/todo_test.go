package app

import (
	"context"
	"strings"
	"testing"

	"wails-tmp/internal/permission"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== todo 的 App 侧接线 =====

// 没有清单存储时不注册该工具（与 read_image 缺附件、spawn_agent 缺 spawner 同一条约定）
func TestTodoNotRegisteredWithoutStore(t *testing.T) {
	tm, dir := newTestToolManager(t)

	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		Enforcer:   tool.AllowAllEnforcer{},
	})
	if view.Lookup(tool.ToolTodo) != nil {
		t.Fatal("没有 TodoStore 时不该注册 todo")
	}
	for _, d := range view.GetToolsForLLM() {
		if d.Function.Name == tool.ToolTodo {
			t.Fatal("没有 TodoStore 时 todo 不该出现在工具列表里")
		}
	}
}

// 有存储但没会话 id 时同样不注册（清单必须归属某条会话）
func TestTodoNotRegisteredWithoutSession(t *testing.T) {
	tm, dir := newTestToolManager(t)
	ss := store.NewSessionStore(t.TempDir())

	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		Enforcer:   tool.AllowAllEnforcer{},
		Todos:      ss, // 故意不给 SessionID
	})
	if view.Lookup(tool.ToolTodo) != nil {
		t.Fatal("没有会话 id 时不该注册 todo")
	}
}

// 装配齐备时：直出 + 两条路径都能执行 + 落盘 + 回调推事件
func TestTodoAssembledAndExecutable(t *testing.T) {
	tm, dir := newTestToolManager(t)
	ss := store.NewSessionStore(t.TempDir())
	s, err := ss.CreateSession(store.SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})
	if err != nil {
		t.Fatal(err)
	}

	var emitted []store.TodoItem
	emitCalls := 0
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		SessionID:  s.ID,
		Enforcer:   tool.AllowAllEnforcer{},
		Todos:      ss,
		OnTodoChange: func(sid string, items []store.TodoItem) {
			emitCalls++
			if sid != s.ID {
				t.Errorf("回调会话 id 不对: %q", sid)
			}
			emitted = items
		},
	})

	// 直出：todo 必须在工具列表里，且必填项是 action
	var def *tool.ToolInterface
	names := map[string]bool{}
	for _, d := range view.GetToolsForLLM() {
		names[d.Function.Name] = true
		if d.Function.Name == tool.ToolTodo {
			tl := view.Lookup(tool.ToolTodo)
			def = &tl
		}
	}
	if !names[tool.ToolTodo] {
		t.Fatalf("todo 应直出给模型，实际 %v", names)
	}
	if def == nil {
		t.Fatal("todo 应可直调")
	}

	// 直调 add
	out, err := view.ExecuteTool(tool.ToolTodo, map[string]interface{}{"action": "add", "text": "接线验证"})
	if err != nil {
		t.Fatalf("直调 todo 失败: %v", err)
	}
	if !strings.Contains(out, "接线验证") {
		t.Errorf("回执应包含内容，实际 %q", out)
	}

	// 落盘：从存储侧读得到
	items, err := ss.Todos(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Text != "接线验证" {
		t.Fatalf("待办应写入会话存储，实际 %v", items)
	}

	// 经 tool_router 也能执行
	if _, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "execute", "tool_name": tool.ToolTodo,
		"arguments": map[string]interface{}{"action": "add", "text": "经路由器"},
	}); err != nil {
		t.Fatalf("经 tool_router 调 todo 失败: %v", err)
	}
	items, _ = ss.Todos(s.ID)
	if len(items) != 2 {
		t.Fatalf("两条路径都应生效，实际 %d 项", len(items))
	}

	// 回调应推了两次，且最后一次带全量清单
	if emitCalls != 2 {
		t.Errorf("两次改动应推两次，实际 %d 次", emitCalls)
	}
	if len(emitted) != 2 {
		t.Errorf("事件应带全量清单，实际 %v", emitted)
	}
}

// 只读放行：todo 不该触发权限询问（它是模型自己的草稿纸，对用户可见）
func TestTodoIsPermissionFree(t *testing.T) {
	app := &App{}
	app.ensurePermissionState() // 有 broker，但 ctx 为 nil（没有可应答的界面）
	dir := t.TempDir()

	enf := app.newPermissionEnforcer(
		&store.Session{ID: "s1", PermissionMode: string(permission.ModeManual)}, dir)

	// 走 Decide（只判定、不等待）：结论应是 allow，而不是"需要询问"
	_, verdict, err := enf.Decide(toolViewStub{name: "todo"}, map[string]interface{}{
		"action": "add", "text": "x",
	})
	if err != nil {
		t.Fatalf("判定失败: %v", err)
	}
	if verdict.Decision != permission.DecisionAllow {
		t.Fatalf("todo 是只读工具，应直接放行，实际 %s（%s）", verdict.Decision, verdict.Reason)
	}

	// 端到端：装配后直调 todo 不该因为权限而失败（无界面可应答时也能过）
	tm, tmDir := newTestToolManager(t)
	ss := store.NewSessionStore(t.TempDir())
	s, _ := ss.CreateSession(store.SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: tmDir,
		SessionID:  s.ID,
		Enforcer:   toolEnforcer{enf},
		Todos:      ss,
	})
	if _, err := view.ExecuteTool(tool.ToolTodo, map[string]interface{}{"action": "add", "text": "x"}); err != nil {
		t.Fatalf("todo 不该被权限拦下: %v", err)
	}
}

// toolViewStub 最小工具视图（判定只需要"名字"）
type toolViewStub struct{ name string }

func (t toolViewStub) GetName() string { return t.name }

// GetSessionTodos：空会话/未知会话都返回空切片而不是 nil（前端可直接 v-for）
func TestGetSessionTodosReturnsEmptySlice(t *testing.T) {
	a := &App{sessionStore: store.NewSessionStore(t.TempDir())}

	if got := a.GetSessionTodos("no-such-session"); got == nil || len(got) != 0 {
		t.Errorf("未知会话应返回空切片，实际 %#v", got)
	}
	if got := a.GetSessionTodos(""); got == nil || len(got) != 0 {
		t.Errorf("空 id 应返回空切片，实际 %#v", got)
	}

	s, _ := a.sessionStore.CreateSession(store.SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})
	if _, err := a.sessionStore.AddTodo(s.ID, "读取验证"); err != nil {
		t.Fatal(err)
	}
	got := a.GetSessionTodos(s.ID)
	if len(got) != 1 || got[0].Text != "读取验证" {
		t.Errorf("应读回清单，实际 %v", got)
	}
}

// emitTodo：ctx 未就绪（未启动）时静默返回，不该 panic
func TestEmitTodoWithoutContextIsSafe(t *testing.T) {
	a := &App{}
	a.emitTodo("s1", []store.TodoItem{{ID: "a", Text: "x"}}) // 不该 panic
	a.emitTodo("", nil)
}
