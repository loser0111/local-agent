package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"wails-tmp/internal/store"
)

// ===== todo 工具（引擎侧）=====
//
// 用内存实现替掉会话存储：这一层要验的是参数解析、动作分派、错误信息与渲染，
// 落盘行为在 internal/store 与 internal/app 的用例里各自覆盖。

// fakeTodoStore 内存版 TodoStore
type fakeTodoStore struct {
	items   []store.TodoItem
	nextID  int
	addErr  error
	updErr  error
	delErr  error
	listErr error
}

func (f *fakeTodoStore) Todos(string) ([]store.TodoItem, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]store.TodoItem(nil), f.items...), nil
}

func (f *fakeTodoStore) AddTodo(_, text string) (store.TodoItem, error) {
	if f.addErr != nil {
		return store.TodoItem{}, f.addErr
	}
	f.nextID++
	it := store.TodoItem{ID: "todo-t" + string(rune('0'+f.nextID)), Text: text, Status: store.TodoPending}
	f.items = append(f.items, it)
	return it, nil
}

func (f *fakeTodoStore) UpdateTodo(_, id, text, status string) (store.TodoItem, error) {
	if f.updErr != nil {
		return store.TodoItem{}, f.updErr
	}
	for i := range f.items {
		if f.items[i].ID != id {
			continue
		}
		if text != "" {
			f.items[i].Text = text
		}
		if status != "" {
			f.items[i].Status = status
		}
		return f.items[i], nil
	}
	return store.TodoItem{}, errNotFound(id)
}

func (f *fakeTodoStore) RemoveTodo(_, id string) (store.TodoItem, error) {
	if f.delErr != nil {
		return store.TodoItem{}, f.delErr
	}
	for i := range f.items {
		if f.items[i].ID == id {
			removed := f.items[i]
			f.items = append(f.items[:i], f.items[i+1:]...)
			return removed, nil
		}
	}
	return store.TodoItem{}, errNotFound(id)
}

func (f *fakeTodoStore) ClearTodos(string) (int, error) {
	n := len(f.items)
	f.items = nil
	return n, nil
}

func errNotFound(id string) error { return &todoNotFound{id: id} }

type todoNotFound struct{ id string }

func (e *todoNotFound) Error() string { return "未找到待办: " + e.id }

func newTestTodoTool(store TodoStore, sessionID string) ToolInterface {
	return NewTodoTool(TodoToolContext{Store: store, SessionID: sessionID})
}

func execTodo(t *testing.T, tl ToolInterface, args map[string]interface{}) (string, error) {
	t.Helper()
	return tl.Execute(context.Background(), args)
}

// 正常路径：add → list → update → remove → clear
func TestTodoToolLifecycle(t *testing.T) {
	st := &fakeTodoStore{}
	tl := newTestTodoTool(st, "s1")

	// add
	out, err := execTodo(t, tl, map[string]interface{}{"action": "add", "text": "写文档"})
	if err != nil {
		t.Fatalf("add 失败: %v", err)
	}
	if !strings.Contains(out, "写文档") {
		t.Errorf("add 回执应包含内容，实际 %q", out)
	}

	// add 第二项
	if _, err := execTodo(t, tl, map[string]interface{}{"action": "add", "text": "跑测试"}); err != nil {
		t.Fatalf("第二次 add 失败: %v", err)
	}

	// list
	out, err = execTodo(t, tl, map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatalf("list 失败: %v", err)
	}
	for _, want := range []string{"写文档", "跑测试", "2 项", "0/2"} {
		if !strings.Contains(out, want) {
			t.Errorf("list 输出应包含 %q，实际:\n%s", want, out)
		}
	}

	// update 状态
	id := st.items[0].ID
	out, err = execTodo(t, tl, map[string]interface{}{
		"action": "update", "id": id, "status": store.TodoDone,
	})
	if err != nil {
		t.Fatalf("update 失败: %v", err)
	}
	if st.items[0].Status != store.TodoDone {
		t.Errorf("状态应更新为 done，实际 %q", st.items[0].Status)
	}
	if !strings.Contains(out, "[x]") || !strings.Contains(out, "1/2") {
		t.Errorf("update 回执应显示勾选与进度，实际:\n%s", out)
	}

	// update 文本
	if _, err := execTodo(t, tl, map[string]interface{}{
		"action": "update", "id": id, "text": "写文档（改）",
	}); err != nil {
		t.Fatalf("改文本失败: %v", err)
	}
	if st.items[0].Text != "写文档（改）" {
		t.Errorf("文本应更新，实际 %q", st.items[0].Text)
	}

	// remove
	out, err = execTodo(t, tl, map[string]interface{}{"action": "remove", "id": id})
	if err != nil {
		t.Fatalf("remove 失败: %v", err)
	}
	if len(st.items) != 1 {
		t.Errorf("remove 后应剩 1 项，实际 %d", len(st.items))
	}
	if !strings.Contains(out, "已删除") {
		t.Errorf("remove 回执不符，实际 %q", out)
	}

	// clear
	out, err = execTodo(t, tl, map[string]interface{}{"action": "clear"})
	if err != nil {
		t.Fatalf("clear 失败: %v", err)
	}
	if len(st.items) != 0 {
		t.Errorf("clear 后应清空，实际 %d", len(st.items))
	}
	if !strings.Contains(out, "1 项") {
		t.Errorf("clear 回执应报告清掉的条数，实际 %q", out)
	}
}

// 边界：action 缺失 / 未知 / 大小写与空白
func TestTodoToolActionParsing(t *testing.T) {
	tl := newTestTodoTool(&fakeTodoStore{}, "s1")

	for _, action := range []interface{}{nil, "", "   ", "unknown", "delete"} {
		_, err := execTodo(t, tl, map[string]interface{}{"action": action})
		if err == nil {
			t.Errorf("action=%v 应报错", action)
		}
	}

	// 大小写与首尾空白应被容忍（模型常写成 List / ADD）
	for _, action := range []string{"LIST", " list ", "List"} {
		if _, err := execTodo(t, tl, map[string]interface{}{"action": action}); err != nil {
			t.Errorf("action=%q 应被接受，实际 %v", action, err)
		}
	}
}

// 边界：add 缺 text / text 全空白
func TestTodoToolAddRequiresText(t *testing.T) {
	st := &fakeTodoStore{}
	tl := newTestTodoTool(st, "s1")

	for _, text := range []interface{}{nil, "", "   ", "\t\n"} {
		_, err := execTodo(t, tl, map[string]interface{}{"action": "add", "text": text})
		if err == nil {
			t.Errorf("text=%v 应报错", text)
		}
	}
	if len(st.items) != 0 {
		t.Errorf("校验失败不该写入任何项，实际 %d", len(st.items))
	}
}

// 边界：update / remove 缺 id
func TestTodoToolRequiresIDForUpdateAndRemove(t *testing.T) {
	tl := newTestTodoTool(&fakeTodoStore{}, "s1")

	for _, action := range []string{"update", "remove"} {
		for _, args := range []map[string]interface{}{
			{"action": action},
			{"action": action, "id": ""},
			{"action": action, "id": "   "},
			{"action": action, "id": 123}, // 类型不对：取不到字符串
		} {
			if _, err := execTodo(t, tl, args); err == nil {
				t.Errorf("action=%s 缺 id（args=%v）应报错", action, args)
			}
		}
	}
}

// 边界：update 既不给 text 也不给 status
func TestTodoToolUpdateNeedsSomethingToChange(t *testing.T) {
	st := &fakeTodoStore{items: []store.TodoItem{{ID: "todo-x", Text: "a", Status: store.TodoPending}}}
	tl := newTestTodoTool(st, "s1")

	if _, err := execTodo(t, tl, map[string]interface{}{"action": "update", "id": "todo-x"}); err == nil {
		t.Error("update 既无 text 也无 status 应报错")
	}
	// 只有空白同样算"没给"
	if _, err := execTodo(t, tl, map[string]interface{}{
		"action": "update", "id": "todo-x", "text": "  ", "status": "",
	}); err == nil {
		t.Error("update 只给空白值应报错")
	}
}

// 边界：id 不存在时，update / remove 都把存储层的错误透出来
func TestTodoToolUnknownID(t *testing.T) {
	tl := newTestTodoTool(&fakeTodoStore{}, "s1")

	if _, err := execTodo(t, tl, map[string]interface{}{
		"action": "update", "id": "nope", "status": store.TodoDone,
	}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("update 未知 id 应报出该 id，实际 %v", err)
	}
	if _, err := execTodo(t, tl, map[string]interface{}{"action": "remove", "id": "nope"}); err == nil {
		t.Error("remove 未知 id 应报错")
	}
}

// 兼容：id 与 todo_id 两种写法都认（模型常按字段名写成 todo_id）
func TestTodoToolAcceptsTodoIDAlias(t *testing.T) {
	st := &fakeTodoStore{items: []store.TodoItem{{ID: "todo-y", Text: "a", Status: store.TodoPending}}}
	tl := newTestTodoTool(st, "s1")

	if _, err := execTodo(t, tl, map[string]interface{}{
		"action": "update", "todo_id": "todo-y", "status": store.TodoDone,
	}); err != nil {
		t.Fatalf("todo_id 别名应被接受，实际 %v", err)
	}
	if st.items[0].Status != store.TodoDone {
		t.Errorf("经别名更新应生效，实际 %q", st.items[0].Status)
	}
}

// 未就绪：没有存储或没有会话 id 时不注册（此处直接构造工具验证其报错，注册逻辑见 internal/app）
func TestTodoToolUnavailable(t *testing.T) {
	for name, tl := range map[string]ToolInterface{
		"无存储": NewTodoTool(TodoToolContext{SessionID: "s1"}),
		"无会话": NewTodoTool(TodoToolContext{Store: &fakeTodoStore{}}),
		"全为空": NewTodoTool(TodoToolContext{}),
	} {
		if _, err := execTodo(t, tl, map[string]interface{}{"action": "list"}); err == nil {
			t.Errorf("%s 时调用应报错", name)
		}
	}
}

// 空清单的 list 输出要有明确文案（模型据此判断"还没建清单"）
func TestTodoToolListEmpty(t *testing.T) {
	out, err := execTodo(t, newTestTodoTool(&fakeTodoStore{}, "s1"), map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatalf("list 失败: %v", err)
	}
	if !strings.Contains(out, "为空") {
		t.Errorf("空清单应有明确文案，实际 %q", out)
	}
}

// 存储层报错要原样透出（不要吞掉）
func TestTodoToolPropagatesStoreError(t *testing.T) {
	st := &fakeTodoStore{addErr: errNotFound("boom")}
	_, err := execTodo(t, newTestTodoTool(st, "s1"), map[string]interface{}{"action": "add", "text": "x"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("存储层错误应透出，实际 %v", err)
	}
}

// 清单变化时回调一次，且带全量清单（前端整表替换的依据）
func TestTodoToolEmitsFullListOnChange(t *testing.T) {
	st := &fakeTodoStore{}
	var got []store.TodoItem
	calls := 0
	tl := NewTodoTool(TodoToolContext{
		Store: st, SessionID: "s1",
		OnChange: func(sid string, items []store.TodoItem) {
			calls++
			if sid != "s1" {
				t.Errorf("回调的会话 id 不对: %q", sid)
			}
			got = items
		},
	})

	if _, err := execTodo(t, tl, map[string]interface{}{"action": "add", "text": "a"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("add 后应回调 1 次，实际 %d", calls)
	}
	if len(got) != 1 || got[0].Text != "a" {
		t.Errorf("回调应带全量清单，实际 %v", got)
	}

	// list 是只读动作：不该触发回调
	if _, err := execTodo(t, tl, map[string]interface{}{"action": "list"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("list 不该触发回调，实际 %d 次", calls)
	}

	// 出错的动作也不该触发（校验失败发生在写入之前）
	if _, err := execTodo(t, tl, map[string]interface{}{"action": "add", "text": " "}); err == nil {
		t.Fatal("空文本应报错")
	}
	if calls != 1 {
		t.Errorf("校验失败不该触发回调，实际 %d 次", calls)
	}
}

// clear 之后回调应收到空清单
func TestTodoToolClearEmitsEmpty(t *testing.T) {
	st := &fakeTodoStore{items: []store.TodoItem{{ID: "a", Text: "x", Status: store.TodoPending}}}
	got := []store.TodoItem{{ID: "sentinel", Text: "x"}}
	tl := NewTodoTool(TodoToolContext{
		Store: st, SessionID: "s1",
		OnChange: func(_ string, items []store.TodoItem) { got = items },
	})

	if _, err := execTodo(t, tl, map[string]interface{}{"action": "clear"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("clear 后应回调空清单，实际 %v", got)
	}
}

// 未注入回调时不应 panic（OnChange 可为 nil）
func TestTodoToolNilOnChange(t *testing.T) {
	tl := newTestTodoTool(&fakeTodoStore{}, "s1")
	if _, err := execTodo(t, tl, map[string]interface{}{"action": "add", "text": "a"}); err != nil {
		t.Fatalf("未注入回调时不该失败: %v", err)
	}
}

// 参数表：必填项与工具名（直出给模型的 schema 由此生成）
func TestTodoToolSchemaAndName(t *testing.T) {
	tl := newTestTodoTool(&fakeTodoStore{}, "s1")

	if tl.GetName() != ToolTodo {
		t.Errorf("工具名应为 %q，实际 %q", ToolTodo, tl.GetName())
	}
	rp, ok := tl.(RequiredParams)
	if !ok {
		t.Fatal("todo 工具应实现 RequiredParams（直出时要生成 schema）")
	}
	required := rp.RequiredParams()
	if len(required) != 1 || required[0] != "action" {
		t.Errorf("只有 action 是必填，实际 %v", required)
	}

	params := tl.GetParameters()
	for _, want := range []string{"action", "text", "id", "status"} {
		if _, ok := params[want]; !ok {
			t.Errorf("参数表应包含 %q，实际 %v", want, params)
		}
	}

	// 直出：todo 必须在 DirectToolOrder 里（系统提示词点名了它）
	found := false
	for _, n := range DirectToolOrder {
		if n == ToolTodo {
			found = true
		}
	}
	if !found {
		t.Errorf("todo 应在 DirectToolOrder 中，实际 %v", DirectToolOrder)
	}
}

// TodoJSON 输出合法 JSON，nil 也应是 []（前端可直接解析）
func TestTodoJSON(t *testing.T) {
	if got := TodoJSON(nil); got != "[]" {
		t.Errorf("nil 应序列化为 []，实际 %q", got)
	}
	items := []store.TodoItem{{ID: "a", Text: "x", Status: store.TodoDone}}
	var back []store.TodoItem
	if err := json.Unmarshal([]byte(TodoJSON(items)), &back); err != nil {
		t.Fatalf("输出应是合法 JSON: %v", err)
	}
	if len(back) != 1 || back[0].ID != "a" {
		t.Errorf("往返后数据不一致: %v", back)
	}
}
