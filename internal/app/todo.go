package app

import (
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== todo 的 App 侧 =====
//
// 引擎（工具本体、参数校验、清单渲染）在 internal/tool/todotool.go，
// 它只认 tool.TodoStore / tool.TodoEmit 两个接口，不认识 App。
// 这里只留必须碰 App 的三件事：把变化推给前端、提供绑定方法给面板读、以及工具名常量。

// toolTodo 工具名（直出给模型，见 internal/tool 的 DirectToolOrder）
const toolTodo = tool.ToolTodo

// emitTodo 把某会话的最新清单推给前端（todo_update 事件）。
//
// 推**全量清单**而不是增量：面板据此整表替换，删除与重排都不会与服务端状态漂移；
// 清单本身很短，全量推送的代价可以忽略。
func (a *App) emitTodo(sessionID string, items []store.TodoItem) {
	if a.ctx == nil || sessionID == "" {
		return
	}
	a.emitChatEvent(sessionID, "", ChatEvent{Type: ChatEventTodoUpdate, Todos: items})
}

// GetSessionTodos 返回某会话的待办清单（面板打开/切回该会话时读一次）。
// 返回空切片而不是 nil：前端可以直接 v-for，不必再判一次空值。
func (a *App) GetSessionTodos(sessionID string) []store.TodoItem {
	if a.sessionStore == nil || sessionID == "" {
		return []store.TodoItem{}
	}
	items, err := a.sessionStore.Todos(sessionID)
	if err != nil || items == nil {
		return []store.TodoItem{}
	}
	return items
}
