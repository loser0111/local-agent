// 记忆工具的引擎侧：memory_search / memory_save / memory_forget 三个内置工具。
//
// 从根 memorytools.go 下沉而来，边界与其余域一致：
//   - 这里只有**引擎**（参数表、实现、与记忆库的读写），不认识 App，也不认识装配期的
//     buildContext；入参收窄成 ToolContext（记忆库句柄 + 三个会话级标量 + 一个回调）。
//   - 根包只剩一行 case：`memory.NewSearchTool(bc.memory)` 等（见根 tools.go 的 newBuiltinTool）。
//
// 注意本包与顶层 `wails-tmp/memory`（记忆库本体）同名：本文件把它显式别名成 memstore，
// 免得"package memory 里的 memory.X 指的是别人"这种事发生。
package memory

import (
	"context"
	"fmt"
	"strings"

	"wails-tmp/internal/tool"
	memstore "wails-tmp/memory"
)

// ToolContext 三个记忆工具的共享上下文。
//
// 与 tool.FileToolContext 同构：只装"引擎真正需要的东西"，不装装配期上下文。
// 字段全部导出，是为了让根包装配点能直接写字面量（与 FileToolContext 的用法一致）。
type ToolContext struct {
	// Store 记忆库句柄；为 nil 时三个工具都返回"记忆库不可用"（工具仍会装配，
	// 这样模型看到的工具列表是稳定的，失败原因也如实回报）。
	Store *memstore.MemoryStore
	// Project 项目 slug：project 作用域的检索按它隔离（user 作用域跨项目）。
	Project string
	// Source 溯源字段：记下这条记忆是哪个会话写的。
	Source string
	// Ignore 本会话已关闭记忆：search 一律不返回内容；save 仍可写（用户显式要求记住），
	// forget 也仍可用（否则关掉记忆后就删不掉旧记忆了）。
	Ignore bool
	// OnTouch 回调：把本轮命中的 id 交回会话侧记账（"已展示过"的不再重复召回）。可为 nil。
	OnTouch func([]string)
}

// NewSearchTool 构造 memory_search。
//
// query 以 mem_ 开头时按 id 精确取一条（并回报完整正文），否则走全文检索并按
// RecallTopK 截断；命中的 id 通过 OnTouch 回调交回调用方记账。
func NewSearchTool(mc ToolContext) tool.ToolInterface {
	return &memorySearchTool{
		BaseTool: &tool.BaseTool{
			Name: tool.ToolMemorySearch,
			Description: "检索跨会话长期记忆。query 可以是自然语言或记忆 id（mem_…）。" +
				"返回标题、分类、相对龄与正文。不要用它搜当前仓库——仓库用 grep / read_file。",
			Parameters: map[string]*tool.ToolArgDef{
				"query": {Type: "string", Description: "检索词或记忆 id"},
			},
		},
		mc: mc,
	}
}

type memorySearchTool struct {
	*tool.BaseTool
	mc ToolContext
}

func (t *memorySearchTool) RequiredParams() []string { return []string{"query"} }

func (t *memorySearchTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.mc.Store == nil {
		return "", fmt.Errorf("记忆库不可用")
	}
	if t.mc.Ignore {
		return "本会话已关闭记忆，memory_search 不返回内容。", nil
	}
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query 是必需的")
	}
	if strings.HasPrefix(query, "mem_") {
		e, err := t.mc.Store.Get(query)
		if err != nil {
			return "", err
		}
		t.touch(e.Meta.ID)
		return formatMemoryEntry(e), nil
	}
	entries, err := t.mc.Store.Retrieve(query, t.mc.Project, t.mc.Store.Config().RecallTopK)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "没有找到相关记忆。", nil
	}
	ids := make([]string, 0, len(entries))
	var sb strings.Builder
	for i, e := range entries {
		if e == nil {
			continue
		}
		if i > 0 {
			sb.WriteString("\n---\n")
		}
		sb.WriteString(formatMemoryEntry(e))
		ids = append(ids, e.Meta.ID)
	}
	t.touch(ids...)
	return sb.String(), nil
}

func (t *memorySearchTool) touch(ids ...string) {
	if t.mc.OnTouch != nil {
		t.mc.OnTouch(ids)
	}
}

// NewSaveTool 构造 memory_save。
//
// 只用 Store.AddMemory（去重合并、PII 拦截、容量淘汰都在那一处），本层不重复实现。
func NewSaveTool(mc ToolContext) tool.ToolInterface {
	return &memorySaveTool{
		BaseTool: &tool.BaseTool{
			Name: tool.ToolMemorySave,
			Description: "把对未来会话仍有用的事实写入长期记忆。" +
				"type 只能是 user / feedback / project / reference。" +
				"要记：用户身份与偏好、被纠正或被确认的做法、无法从当前仓库推出来的项目事实。" +
				"不记：代码模式、目录结构、git 历史、本会话 todo、计划步骤。" +
				"相对日期请改成绝对日期。先 memory_search 再决定是新写还是让系统合并。",
			Parameters: map[string]*tool.ToolArgDef{
				"content": {Type: "string", Description: "记忆正文"},
				"title":   {Type: "string", Description: "短标题；可空，将从正文推导"},
				"type":    {Type: "string", Description: "user / feedback / project / reference，默认 project"},
				"scope":   {Type: "string", Description: "user（跨项目）或 project（本项目），默认 project"},
				"tags":    {Type: "string", Description: "逗号分隔标签，可选"},
			},
		},
		mc: mc,
	}
}

type memorySaveTool struct {
	*tool.BaseTool
	mc ToolContext
}

func (t *memorySaveTool) RequiredParams() []string { return []string{"content"} }

func (t *memorySaveTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.mc.Store == nil {
		return "", fmt.Errorf("记忆库不可用")
	}
	content, _ := args["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("content 是必需的")
	}
	title, _ := args["title"].(string)
	typ, _ := args["type"].(string)
	scope, _ := args["scope"].(string)
	tagsRaw, _ := args["tags"].(string)
	meta := memstore.MemoryMeta{
		Title:   strings.TrimSpace(title),
		Type:    strings.TrimSpace(typ),
		Scope:   strings.TrimSpace(scope),
		Project: t.mc.Project,
		Tags:    splitTags(tagsRaw),
		Source:  t.mc.Source,
	}
	got, err := t.mc.Store.AddMemory(meta, content)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已保存记忆 %s（%s/%s）%s", got.ID, got.Scope, got.Type, got.Title), nil
}

// NewForgetTool 构造 memory_forget。
//
// target 以 mem_ 开头时按 id 精确删除；否则先检索，**只有恰好命中一条才删**，
// 多条只列出让模型用 id 再来一次——删除是不可逆的，宁可多花一轮。
func NewForgetTool(mc ToolContext) tool.ToolInterface {
	return &memoryForgetTool{
		BaseTool: &tool.BaseTool{
			Name:        tool.ToolMemoryForget,
			Description: "删除一条长期记忆。优先传记忆 id；传入检索词时仅当恰好命中一条才删除，多条只列出。",
			Parameters: map[string]*tool.ToolArgDef{
				"target": {Type: "string", Description: "记忆 id（mem_…）或检索词"},
			},
		},
		mc: mc,
	}
}

type memoryForgetTool struct {
	*tool.BaseTool
	mc ToolContext
}

func (t *memoryForgetTool) RequiredParams() []string { return []string{"target"} }

func (t *memoryForgetTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.mc.Store == nil {
		return "", fmt.Errorf("记忆库不可用")
	}
	target, _ := args["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("target 是必需的")
	}
	if strings.HasPrefix(target, "mem_") {
		if err := t.mc.Store.DeleteMemory(target); err != nil {
			return "", err
		}
		return "已删除记忆 " + target, nil
	}
	entries, err := t.mc.Store.Retrieve(target, t.mc.Project, 8)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "没有找到可删除的记忆。", nil
	}
	if len(entries) > 1 {
		var sb strings.Builder
		sb.WriteString("命中多条，请用 id 精确删除：\n")
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("- %s  %s\n", e.Meta.ID, e.Meta.Title))
		}
		return sb.String(), nil
	}
	id := entries[0].Meta.ID
	if err := t.mc.Store.DeleteMemory(id); err != nil {
		return "", err
	}
	return "已删除记忆 " + id, nil
}

// formatMemoryEntry 把一条记忆渲染成给模型看的一行 + 正文。
//
// 正文为空时回落到 Description 这条规则只有一处实现才守得住——它与记忆库的
// FormatRecall / FormatInjection 是同一族"给人看的渲染"，但只在检索类工具里用。
func formatMemoryEntry(e *memstore.MemoryEntry) string {
	if e == nil {
		return ""
	}
	body := strings.TrimSpace(e.Content)
	if body == "" {
		body = e.Meta.Description
	}
	return fmt.Sprintf("[%s] %s（%s/%s, %s）\n%s",
		e.Meta.ID, e.Meta.Title, e.Meta.Scope, e.Meta.Type, e.Age, body)
}

func splitTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
