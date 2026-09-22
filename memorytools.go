package main

import (
	"context"
	"fmt"
	"strings"

	"wails-tmp/memory"
)

const (
	toolMemorySearch = "memory_search"
	toolMemorySave   = "memory_save"
	toolMemoryForget = "memory_forget"
)

// memoryToolContext 三个记忆工具的共享上下文。
type memoryToolContext struct {
	store   *memory.MemoryStore
	project string
	source  string
	ignore  bool
	onTouch func([]string)
}

func newMemorySearchTool(mc memoryToolContext) ToolInterface {
	return &memorySearchTool{
		BaseTool: &BaseTool{
			Name: toolMemorySearch,
			Description: "检索跨会话长期记忆。query 可以是自然语言或记忆 id（mem_…）。" +
				"返回标题、分类、相对龄与正文。不要用它搜当前仓库——仓库用 grep / read_file。",
			Parameters: map[string]*ToolArgDef{
				"query": {Type: "string", Description: "检索词或记忆 id"},
			},
		},
		mc: mc,
	}
}

type memorySearchTool struct {
	*BaseTool
	mc memoryToolContext
}

func (t *memorySearchTool) RequiredParams() []string { return []string{"query"} }

func (t *memorySearchTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.mc.store == nil {
		return "", fmt.Errorf("记忆库不可用")
	}
	if t.mc.ignore {
		return "本会话已关闭记忆，memory_search 不返回内容。", nil
	}
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query 是必需的")
	}
	if strings.HasPrefix(query, "mem_") {
		e, err := t.mc.store.Get(query)
		if err != nil {
			return "", err
		}
		t.touch(e.Meta.ID)
		return formatMemoryEntry(e), nil
	}
	entries, err := t.mc.store.Retrieve(query, t.mc.project, t.mc.store.Config().RecallTopK)
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
	if t.mc.onTouch != nil {
		t.mc.onTouch(ids)
	}
}

func newMemorySaveTool(mc memoryToolContext) ToolInterface {
	return &memorySaveTool{
		BaseTool: &BaseTool{
			Name: toolMemorySave,
			Description: "把对未来会话仍有用的事实写入长期记忆。" +
				"type 只能是 user / feedback / project / reference。" +
				"要记：用户身份与偏好、被纠正或被确认的做法、无法从当前仓库推出来的项目事实。" +
				"不记：代码模式、目录结构、git 历史、本会话 todo、计划步骤。" +
				"相对日期请改成绝对日期。先 memory_search 再决定是新写还是让系统合并。",
			Parameters: map[string]*ToolArgDef{
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
	*BaseTool
	mc memoryToolContext
}

func (t *memorySaveTool) RequiredParams() []string { return []string{"content"} }

func (t *memorySaveTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.mc.store == nil {
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
	meta := memory.MemoryMeta{
		Title:   strings.TrimSpace(title),
		Type:    strings.TrimSpace(typ),
		Scope:   strings.TrimSpace(scope),
		Project: t.mc.project,
		Tags:    splitTags(tagsRaw),
		Source:  t.mc.source,
	}
	got, err := t.mc.store.AddMemory(meta, content)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已保存记忆 %s（%s/%s）%s", got.ID, got.Scope, got.Type, got.Title), nil
}

func newMemoryForgetTool(mc memoryToolContext) ToolInterface {
	return &memoryForgetTool{
		BaseTool: &BaseTool{
			Name:        toolMemoryForget,
			Description: "删除一条长期记忆。优先传记忆 id；传入检索词时仅当恰好命中一条才删除，多条只列出。",
			Parameters: map[string]*ToolArgDef{
				"target": {Type: "string", Description: "记忆 id（mem_…）或检索词"},
			},
		},
		mc: mc,
	}
}

type memoryForgetTool struct {
	*BaseTool
	mc memoryToolContext
}

func (t *memoryForgetTool) RequiredParams() []string { return []string{"target"} }

func (t *memoryForgetTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	if t.mc.store == nil {
		return "", fmt.Errorf("记忆库不可用")
	}
	target, _ := args["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("target 是必需的")
	}
	if strings.HasPrefix(target, "mem_") {
		if err := t.mc.store.DeleteMemory(target); err != nil {
			return "", err
		}
		return "已删除记忆 " + target, nil
	}
	entries, err := t.mc.store.Retrieve(target, t.mc.project, 8)
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
	if err := t.mc.store.DeleteMemory(id); err != nil {
		return "", err
	}
	return "已删除记忆 " + id, nil
}

func formatMemoryEntry(e *memory.MemoryEntry) string {
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
