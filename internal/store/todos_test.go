package store

import (
	"path/filepath"
	"sync"
	"testing"
)

// ===== 待办清单（todo 工具的服务端存储）=====

func newTodoSession(t *testing.T) (*SessionStore, string) {
	t.Helper()
	ss := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	s, err := ss.CreateSession(SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	return ss, s.ID
}

func TestTodoCRUD(t *testing.T) {
	ss, sid := newTodoSession(t)

	// 新会话清单为空
	items, err := ss.Todos(sid)
	if err != nil {
		t.Fatalf("Todos 失败: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("新会话应无待办，实际 %v", items)
	}

	// add 两项：id 唯一、状态默认 pending
	a, err := ss.AddTodo(sid, "第一步")
	if err != nil {
		t.Fatalf("AddTodo 失败: %v", err)
	}
	b, err := ss.AddTodo(sid, "  第二步  ") // 首尾空白应被裁掉
	if err != nil {
		t.Fatalf("AddTodo 失败: %v", err)
	}
	if a.ID == "" || b.ID == "" || a.ID == b.ID {
		t.Fatalf("id 应非空且唯一，实际 %q / %q", a.ID, b.ID)
	}
	if a.Status != TodoPending {
		t.Errorf("新项状态应为 pending，实际 %q", a.Status)
	}
	if b.Text != "第二步" {
		t.Errorf("文本首尾空白应被裁掉，实际 %q", b.Text)
	}

	// update：改状态
	got, err := ss.UpdateTodo(sid, a.ID, "", TodoInProgress)
	if err != nil {
		t.Fatalf("UpdateTodo 失败: %v", err)
	}
	if got.Status != TodoInProgress || got.Text != "第一步" {
		t.Errorf("只改状态不该动文本，实际 %+v", got)
	}
	// update：改文本（状态保持）
	got, err = ss.UpdateTodo(sid, a.ID, "第一步（改）", "")
	if err != nil {
		t.Fatalf("UpdateTodo 失败: %v", err)
	}
	if got.Text != "第一步（改）" || got.Status != TodoInProgress {
		t.Errorf("只改文本不该动状态，实际 %+v", got)
	}
	if got.UpdatedAt < got.CreatedAt {
		t.Errorf("UpdatedAt 应不早于 CreatedAt，实际 %d / %d", got.UpdatedAt, got.CreatedAt)
	}

	// 顺序稳定（按加入顺序）
	items, _ = ss.Todos(sid)
	if len(items) != 2 || items[0].ID != a.ID || items[1].ID != b.ID {
		t.Fatalf("清单应保持加入顺序，实际 %v", items)
	}

	// remove
	removed, err := ss.RemoveTodo(sid, a.ID)
	if err != nil {
		t.Fatalf("RemoveTodo 失败: %v", err)
	}
	if removed.ID != a.ID {
		t.Errorf("应返回被删的项，实际 %+v", removed)
	}
	items, _ = ss.Todos(sid)
	if len(items) != 1 || items[0].ID != b.ID {
		t.Fatalf("删除后应只剩第二项，实际 %v", items)
	}

	// clear
	n, err := ss.ClearTodos(sid)
	if err != nil {
		t.Fatalf("ClearTodos 失败: %v", err)
	}
	if n != 1 {
		t.Errorf("应报告清掉 1 项，实际 %d", n)
	}
	if items, _ = ss.Todos(sid); len(items) != 0 {
		t.Errorf("清空后应为空，实际 %v", items)
	}
}

// 校验：空文本、未知状态、不存在的 id
func TestTodoValidation(t *testing.T) {
	ss, sid := newTodoSession(t)

	for _, text := range []string{"", "   ", "\t\n"} {
		if _, err := ss.AddTodo(sid, text); err == nil {
			t.Errorf("空文本 %q 应被拒绝", text)
		}
	}

	item, _ := ss.AddTodo(sid, "x")
	if _, err := ss.UpdateTodo(sid, item.ID, "", "bogus"); err == nil {
		t.Error("未知状态应被拒绝")
	}
	if _, err := ss.UpdateTodo(sid, "no-such-id", "", TodoDone); err == nil {
		t.Error("update 不存在的 id 应报错")
	}
	if _, err := ss.RemoveTodo(sid, "no-such-id"); err == nil {
		t.Error("remove 不存在的 id 应报错")
	}

	// 校验失败不该污染清单
	items, _ := ss.Todos(sid)
	if len(items) != 1 || items[0].Status != TodoPending {
		t.Errorf("失败的操作不该改动清单，实际 %v", items)
	}
}

// 未知状态取值：ValidTodoStatus 是唯一真源
func TestValidTodoStatus(t *testing.T) {
	for _, s := range []string{TodoPending, TodoInProgress, TodoDone} {
		if !ValidTodoStatus(s) {
			t.Errorf("%q 应是合法状态", s)
		}
	}
	for _, s := range []string{"", "PENDING", "running", "done ", "unknown"} {
		if ValidTodoStatus(s) {
			t.Errorf("%q 不该是合法状态", s)
		}
	}
}

// 落盘：清单要能跨 SessionStore 实例读回来（重启后仍看得到）
func TestTodoPersistsAcrossInstances(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	ss := NewSessionStore(dir)
	s, _ := ss.CreateSession(SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})

	item, err := ss.AddTodo(s.ID, "持久化的项")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ss.UpdateTodo(s.ID, item.ID, "", TodoDone); err != nil {
		t.Fatal(err)
	}

	// 换一个实例（模拟重启）
	ss2 := NewSessionStore(dir)
	items, err := ss2.Todos(s.ID)
	if err != nil {
		t.Fatalf("重启后读清单失败: %v", err)
	}
	if len(items) != 1 || items[0].Text != "持久化的项" || items[0].Status != TodoDone {
		t.Fatalf("清单应跨实例保留，实际 %v", items)
	}
}

// Todos 返回副本：调用方改它不该影响存储
func TestTodoReturnsCopy(t *testing.T) {
	ss, sid := newTodoSession(t)
	if _, err := ss.AddTodo(sid, "原始"); err != nil {
		t.Fatal(err)
	}

	items, _ := ss.Todos(sid)
	items[0].Text = "被篡改"

	again, _ := ss.Todos(sid)
	if again[0].Text != "原始" {
		t.Errorf("Todos 应返回副本，实际 %q", again[0].Text)
	}
}

// 并发 add：加锁跨"读—改—写"整段，因此不该丢项
//
// 这是最容易出错的点：模型在同一轮里可能连续多次调用 todo，
// 若只锁单次读写，两次 add 会互相覆盖（后写的那次把前一次丢掉）。
func TestTodoConcurrentAddDoesNotLoseItems(t *testing.T) {
	ss, sid := newTodoSession(t)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			if _, err := ss.AddTodo(sid, "并发项"); err != nil {
				t.Errorf("并发 add 失败: %v", err)
			}
		}(i)
	}
	wg.Wait()

	items, err := ss.Todos(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != n {
		t.Fatalf("并发 add %d 次应得到 %d 项，实际 %d（有项被覆盖）", n, n, len(items))
	}
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.ID] {
			t.Errorf("id 重复: %s", it.ID)
		}
		seen[it.ID] = true
	}
}

// 会话之间互不影响
func TestTodosAreSessionScoped(t *testing.T) {
	ss, sidA := newTodoSession(t)
	sB, _ := ss.CreateSession(SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})

	if _, err := ss.AddTodo(sidA, "A 的项"); err != nil {
		t.Fatal(err)
	}
	b, err := ss.Todos(sB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 0 {
		t.Errorf("B 会话不该看到 A 的待办，实际 %v", b)
	}
}

// 不存在的会话：报错而不是静默返回空
func TestTodoUnknownSession(t *testing.T) {
	ss, _ := newTodoSession(t)
	if _, err := ss.Todos("no-such-session"); err == nil {
		t.Error("读取不存在会话的清单应报错")
	}
	if _, err := ss.AddTodo("no-such-session", "x"); err == nil {
		t.Error("向不存在会话加待办应报错")
	}
}

// 无待办的会话，Todos 应返回 nil/空而不是报错（老会话文件里没有该字段）
func TestTodoMissingFieldIsEmptyNotError(t *testing.T) {
	ss, sid := newTodoSession(t)
	items, err := ss.Todos(sid)
	if err != nil {
		t.Fatalf("老会话（无 todos 字段）不该报错: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("应为空，实际 %v", items)
	}
}
