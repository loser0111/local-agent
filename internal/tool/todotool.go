package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"wails-tmp/internal/store"
)

// ===== todo：主会话的待办清单 =====

// TodoStore 待办清单的读写接口（由根包注入会话存储的适配器）。
//
// 为什么收成接口而不是直接吃 *store.SessionStore：internal/tool 不依赖 internal/store
// 之外的东西，而这里真正需要的只有五个动作；接口也让引擎侧测试不必建真会话文件。
type TodoStore interface {
	Todos(sessionID string) ([]store.TodoItem, error)
	AddTodo(sessionID, text string) (store.TodoItem, error)
	UpdateTodo(sessionID, todoID, text, status string) (store.TodoItem, error)
	RemoveTodo(sessionID, todoID string) (store.TodoItem, error)
	ClearTodos(sessionID string) (int, error)
}

// TodoEmit 清单变化时的回调（把最新清单推给前端面板）。可为 nil。
type TodoEmit func(sessionID string, items []store.TodoItem)

// TodoToolContext todo 工具的上下文
type TodoToolContext struct {
	Store     TodoStore
	SessionID string
	// OnChange 每次清单真的发生变化后调用一次，携带**变化后的全量清单**。
	// 前端据此整表刷新：增量推送在"删除/重排"时容易与服务端状态漂移。
	OnChange TodoEmit
}

// NewTodoTool 构造 todo 工具。
func NewTodoTool(tc TodoToolContext) ToolInterface {
	params, required := FileToolParams([]FileArg{
		{Name: "action", Type: "string", Description: "add / update / remove / list / clear", Required: true},
		{Name: "text", Type: "string", Description: "action=add 时的待办内容；action=update 时用于改文本"},
		{Name: "id", Type: "string", Description: "action=update/remove 时的待办 id"},
		{Name: "status", Type: "string", Description: "action=update 时的状态：pending / in_progress / done"},
	})
	return &todoTool{
		BaseTool: &BaseTool{
			Name: ToolTodo,
			Description: "维护主会话的待办清单：长任务里先列出步骤，做完一项就勾掉一项。" +
				"清单会显示在侧边面板，也写进会话记录（上下文被压缩后仍看得到，是你重建进度的依据）。" +
				"用法：action=add 加一项（text 必填）；action=update 改状态或文本（id 必填，" +
				"status 取 pending / in_progress / done）；action=remove 删一项（id 必填）；" +
				"action=list 读回全量清单；action=clear 清空。" +
				"适合：多步骤、跨多轮、需要让用户看到进度的任务。不适合：一步就能做完的事（不要为了走流程而建清单）。",
			Parameters: params,
		},
		tc:       tc,
		required: required,
	}
}

type todoTool struct {
	*BaseTool
	tc       TodoToolContext
	required []string
}

func (t *todoTool) RequiredParams() []string { return t.required }

func (t *todoTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.tc.Store == nil || strings.TrimSpace(t.tc.SessionID) == "" {
		return "", fmt.Errorf("待办清单不可用（会话未就绪）")
	}
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return "", fmt.Errorf("action 参数是必需的：add / update / remove / list / clear")
	}

	switch action {
	case "list":
		items, err := t.tc.Store.Todos(t.tc.SessionID)
		if err != nil {
			return "", err
		}
		return formatTodoList(items), nil

	case "add":
		text, _ := args["text"].(string)
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("action=add 时 text 参数是必需的")
		}
		item, err := t.tc.Store.AddTodo(t.tc.SessionID, text)
		if err != nil {
			return "", err
		}
		items, _ := t.tc.Store.Todos(t.tc.SessionID)
		t.emit(items)
		return fmt.Sprintf("已添加待办 %s：%s\n\n%s", item.ID, item.Text, formatTodoList(items)), nil

	case "update":
		todoID := todoArgID(args)
		if todoID == "" {
			return "", fmt.Errorf("action=update 时 id 参数是必需的（先用 action=list 查 id）")
		}
		text, _ := args["text"].(string)
		status, _ := args["status"].(string)
		if strings.TrimSpace(text) == "" && strings.TrimSpace(status) == "" {
			return "", fmt.Errorf("action=update 需要 text 或 status 至少给一个")
		}
		item, err := t.tc.Store.UpdateTodo(t.tc.SessionID, todoID, text, status)
		if err != nil {
			return "", err
		}
		items, _ := t.tc.Store.Todos(t.tc.SessionID)
		t.emit(items)
		return fmt.Sprintf("已更新待办 %s：%s [%s]\n\n%s", item.ID, item.Text, item.Status, formatTodoList(items)), nil

	case "remove":
		todoID := todoArgID(args)
		if todoID == "" {
			return "", fmt.Errorf("action=remove 时 id 参数是必需的（先用 action=list 查 id）")
		}
		removed, err := t.tc.Store.RemoveTodo(t.tc.SessionID, todoID)
		if err != nil {
			return "", err
		}
		items, _ := t.tc.Store.Todos(t.tc.SessionID)
		t.emit(items)
		return fmt.Sprintf("已删除待办 %s：%s\n\n%s", removed.ID, removed.Text, formatTodoList(items)), nil

	case "clear":
		n, err := t.tc.Store.ClearTodos(t.tc.SessionID)
		if err != nil {
			return "", err
		}
		t.emit(nil)
		return fmt.Sprintf("已清空待办清单（%d 项）。", n), nil

	default:
		return "", fmt.Errorf("未知的 action: %s，可选值: add / update / remove / list / clear", action)
	}
}

func (t *todoTool) emit(items []store.TodoItem) {
	if t.tc.OnChange != nil {
		t.tc.OnChange(t.tc.SessionID, items)
	}
}

// todoArgID 取待办 id。兼容 id / todo_id 两种写法：
// 参数表里叫 id，但模型常按 store.TodoItem 的字段名写成 todo_id，
// 为此回一轮报错纯属浪费——两种都认。
func todoArgID(args map[string]interface{}) string {
	for _, key := range []string{"id", "todo_id"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// formatTodoList 把清单渲染成给模型看的文本（也便于它读回自己的进度）。
func formatTodoList(items []store.TodoItem) string {
	if len(items) == 0 {
		return "待办清单为空。"
	}
	done := 0
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("待办清单（%d 项）：\n", len(items)))
	for _, it := range items {
		mark := "[ ]"
		switch it.Status {
		case store.TodoInProgress:
			mark = "[~]"
		case store.TodoDone:
			mark = "[x]"
			done++
		}
		sb.WriteString(fmt.Sprintf("%s %s %s\n", mark, it.ID, it.Text))
	}
	sb.WriteString(fmt.Sprintf("进度：%d/%d 已完成。", done, len(items)))
	return sb.String()
}

// TodoJSON 待办清单的 JSON 形式（事件推送与 Wails 绑定共用，保证两处字段一致）
func TodoJSON(items []store.TodoItem) string {
	if items == nil {
		items = []store.TodoItem{}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}
