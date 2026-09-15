package main

import (
	"path/filepath"
	"testing"
)

func TestSessionStore_FullFlow(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))

	// 初始列表为空
	list, err := store.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions 失败: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("初始会话列表应为空，实际: %d", len(list))
	}

	// 创建会话
	session, err := store.CreateSession(SessionConfig{
		Project: "e:/test",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	if session.ID == "" || session.Title != "新会话" {
		t.Fatalf("会话初始化异常: %+v", session)
	}

	// 追加用户消息，标题应自动取消息内容
	_, err = store.AppendMessage(session.ID, Message{Role: "user", Content: "帮我实现登录功能"})
	if err != nil {
		t.Fatalf("AppendMessage 失败: %v", err)
	}

	// 追加 AI 消息
	_, err = store.AppendMessage(session.ID, Message{Role: "assistant", Content: "好的，我来帮你"})
	if err != nil {
		t.Fatalf("AppendMessage 失败: %v", err)
	}

	// 追加一轮对话记录
	err = store.AppendConversation(session.ID, &Conversation{
		Query:  "帮我实现登录功能",
		Answer: "好的，我来帮你",
	})
	if err != nil {
		t.Fatalf("AppendConversation 失败: %v", err)
	}

	// 完整加载验证
	loaded, err := store.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession 失败: %v", err)
	}
	if loaded.Title != "帮我实现登录功能" {
		t.Fatalf("标题应自动取首条消息，实际: %s", loaded.Title)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("消息数应为 2，实际: %d", len(loaded.Messages))
	}
	if len(loaded.Conversations) != 1 || loaded.Conversations[0].Index != 1 {
		t.Fatalf("对话记录异常: %+v", loaded.Conversations)
	}
	if loaded.Messages[0].ID == "" || loaded.Messages[0].CreatedAt == 0 {
		t.Fatal("消息 ID 和时间戳应自动生成")
	}

	// 列表应返回元数据但不含消息
	list, _ = store.ListSessions()
	if len(list) != 1 {
		t.Fatalf("列表应有 1 个会话，实际: %d", len(list))
	}
	if len(list[0].Messages) != 0 {
		t.Fatal("列表不应携带消息内容")
	}
	if list[0].Title != "帮我实现登录功能" {
		t.Fatalf("列表标题异常: %s", list[0].Title)
	}

	// 更新元数据
	model := "new-model"
	updated, err := store.UpdateSession(session.ID, SessionPatch{Model: &model})
	if err != nil {
		t.Fatalf("UpdateSession 失败: %v", err)
	}
	if updated.Model != "new-model" {
		t.Fatalf("模型更新失败: %s", updated.Model)
	}

	// 删除
	if err := store.DeleteSession(session.ID); err != nil {
		t.Fatalf("DeleteSession 失败: %v", err)
	}
	list, _ = store.ListSessions()
	if len(list) != 0 {
		t.Fatal("删除后列表应为空")
	}
	if err := store.DeleteSession("nonexistent"); err == nil {
		t.Fatal("删除不存在的会话应报错")
	}
}
