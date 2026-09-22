package main

import (
	"fmt"
	"strings"

	"wails-tmp/memory"
)

const (
	memoryRememberCmd = "remember"
	memoryForgetCmd   = "forget"
	memoryListCmd     = "memory"
	memoryIgnoreCmd   = "ignore-memory"
)

func isMemoryCommand(cmd string) bool {
	switch cmd {
	case memoryRememberCmd, memoryForgetCmd, memoryListCmd, memoryIgnoreCmd:
		return true
	default:
		return false
	}
}

func (a *App) handleMemoryCommand(sessionID, cmd, rest string) (*ChatResult, error) {
	if a.memory == nil {
		return a.replyMemoryCommand(sessionID, "记忆库不可用。")
	}
	session, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	rest = strings.TrimSpace(rest)
	var reply string
	switch cmd {
	case memoryRememberCmd:
		if rest == "" {
			reply = "用法：/remember 要记住的内容"
			break
		}
		got, err := a.memory.AddMemory(memory.MemoryMeta{
			Scope:   memory.ScopeUser,
			Type:    memory.TypeUser,
			Project: memoryProjectSlug(session.Project),
			Source:  session.ID,
		}, rest)
		if err != nil {
			reply = "未能保存：" + err.Error()
			break
		}
		reply = fmt.Sprintf("已记住 %s：%s", got.ID, got.Title)
	case memoryForgetCmd:
		if rest == "" {
			reply = "用法：/forget 记忆id 或 检索词"
			break
		}
		reply = a.forgetMemory(session, rest)
	case memoryIgnoreCmd:
		if err := a.sessionStore.SetIgnoreMemory(sessionID, true); err != nil {
			return nil, err
		}
		reply = "本会话已关闭记忆注入与自动提取。/memory on 可恢复。/remember 仍可手动写入。"
	case memoryListCmd:
		switch strings.ToLower(rest) {
		case "on":
			if err := a.sessionStore.SetIgnoreMemory(sessionID, false); err != nil {
				return nil, err
			}
			reply = "本会话已恢复使用记忆。"
		case "off":
			if err := a.sessionStore.SetIgnoreMemory(sessionID, true); err != nil {
				return nil, err
			}
			reply = "本会话已关闭记忆注入与自动提取。"
		default:
			reply = a.listMemoriesForSession(session)
		}
	}
	return a.replyMemoryCommand(sessionID, reply)
}

func (a *App) forgetMemory(session *Session, target string) string {
	if strings.HasPrefix(target, "mem_") {
		if err := a.memory.DeleteMemory(target); err != nil {
			return "未能删除：" + err.Error()
		}
		return "已删除记忆 " + target
	}
	entries, err := a.memory.Retrieve(target, memoryProjectSlug(session.Project), 8)
	if err != nil {
		return "检索失败：" + err.Error()
	}
	if len(entries) == 0 {
		return "没有找到可删除的记忆。"
	}
	if len(entries) > 1 {
		var sb strings.Builder
		sb.WriteString("命中多条，请用 /forget mem_… 精确删除：\n")
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("- %s  %s\n", e.Meta.ID, e.Meta.Title))
		}
		return strings.TrimSpace(sb.String())
	}
	id := entries[0].Meta.ID
	if err := a.memory.DeleteMemory(id); err != nil {
		return "未能删除：" + err.Error()
	}
	return "已删除记忆 " + id
}

func (a *App) listMemoriesForSession(session *Session) string {
	slug := memoryProjectSlug(session.Project)
	user, _ := a.memory.ListMemories(memory.ScopeUser, "")
	proj, _ := a.memory.ListMemories(memory.ScopeProject, slug)
	if len(user) == 0 && len(proj) == 0 {
		return "还没有长期记忆。用 /remember 写下一条，或让我在对话里 memory_save。"
	}
	var sb strings.Builder
	sb.WriteString("## 长期记忆\n")
	if session.IgnoreMemory {
		sb.WriteString("（本会话已关闭注入）\n")
	}
	writeMemoryList(&sb, "用户", user)
	writeMemoryList(&sb, "项目 "+slug, proj)
	return strings.TrimSpace(sb.String())
}

func writeMemoryList(sb *strings.Builder, heading string, entries []*memory.MemoryEntry) {
	if len(entries) == 0 {
		return
	}
	sb.WriteString("### " + heading + "\n")
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("- %s  [%s] %s（%s）\n", e.Meta.ID, e.Meta.Type, e.Meta.Title, e.Age))
	}
}

func (a *App) replyMemoryCommand(sessionID, reply string) (*ChatResult, error) {
	result := &ChatResult{Reply: reply}
	if saved, err := a.sessionStore.AppendMessage(sessionID, Message{Role: RoleAssistant, Content: reply}); err == nil {
		result.Messages = []Message{*saved}
	}
	return result, nil
}
