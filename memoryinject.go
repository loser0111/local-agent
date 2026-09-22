package main

import (
	"strings"

	"wails-tmp/memory"
)

// memoryProjectSlug 会话工作区 → 记忆项目键。
func memoryProjectSlug(project string) string {
	return memory.ProjectSlug(project)
}

func (a *App) memoryEnabled() bool {
	return a != nil && a.memory != nil && a.memory.AutoEnabled()
}

func (a *App) sessionIgnoresMemory(session *Session) bool {
	return session != nil && session.IgnoreMemory
}

// memoryIndexBlock L1 索引（常驻系统提示）。空库或关闭时返回空串。
func (a *App) memoryIndexBlock(session *Session) string {
	if !a.memoryEnabled() {
		return ""
	}
	if a.sessionIgnoresMemory(session) {
		return "## 长期记忆\n用户要求本会话不使用记忆。不要引用或提及记忆内容。"
	}
	slug := ""
	if session != nil {
		slug = memoryProjectSlug(session.Project)
	}
	idx := a.memory.IndexForPrompt(slug)
	if idx == "" {
		return ""
	}
	return idx + "\n用户明确说记住或忘记时，立刻调用 memory_save / memory_forget，不要只口头答应。" +
		"记忆是写下时的观察；项目级超过 1 天的事实，引用文件或函数前先 grep/read。"
}

// attachMemoryRecall 叠加本轮 L2 召回。召回块不写进 Session.Messages。
func (a *App) attachMemoryRecall(session *Session, query, prompt string) string {
	if !a.memoryEnabled() || a.sessionIgnoresMemory(session) || session == nil {
		return prompt
	}
	if strings.TrimSpace(query) == "" {
		return prompt
	}
	entries, err := a.memory.Recall(query, memoryProjectSlug(session.Project), session.SurfacedMemoryIDs)
	if err != nil || len(entries) == 0 {
		return prompt
	}
	block := a.memory.FormatRecall(entries)
	if block == "" {
		return prompt
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e != nil && e.Meta.ID != "" {
			ids = append(ids, e.Meta.ID)
		}
	}
	if a.sessionStore != nil {
		_ = a.sessionStore.AddSurfacedMemoryIDs(session.ID, ids)
		if updated, err := a.sessionStore.GetSession(session.ID); err == nil {
			session.SurfacedMemoryIDs = updated.SurfacedMemoryIDs
		}
	}
	return prompt + "\n\n" + block
}
