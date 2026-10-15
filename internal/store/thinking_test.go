package store

import (
	"path/filepath"
	"testing"
)

// ===== thinking 配置的持久化（回归）=====
//
// 这两个字段曾经在 SessionConfig 里定义了、前端也在传，但 CreateSession 没把它们
// 落到 Session 上——于是界面上勾了"启用 Extended Thinking"却毫无效果（静默失效）。
// UpdateSession 同样忽略了 patch，会话中途改配置也不会生效。这里各钉一条。

// 创建会话时 thinking 配置必须落盘
func TestCreateSessionPersistsThinking(t *testing.T) {
	ss := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))

	s, err := ss.CreateSession(SessionConfig{
		Model:           "m",
		ThinkingEnabled: true,
		ThinkingBudget:  8000,
	})
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	if !s.ThinkingEnabled || s.ThinkingBudget != 8000 {
		t.Fatalf("CreateSession 应保留 thinking 配置，实际 enabled=%v budget=%d", s.ThinkingEnabled, s.ThinkingBudget)
	}

	// 从磁盘读回来也要在（真正落盘，而不只是内存对象）
	got, err := ss.GetSession(s.ID)
	if err != nil {
		t.Fatalf("GetSession 失败: %v", err)
	}
	if !got.ThinkingEnabled || got.ThinkingBudget != 8000 {
		t.Fatalf("thinking 配置未落盘，实际 enabled=%v budget=%d", got.ThinkingEnabled, got.ThinkingBudget)
	}
}

// 更新会话时 thinking 配置必须生效，且 nil 指针表示"不改"
func TestUpdateSessionAppliesThinkingPatch(t *testing.T) {
	ss := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	s, err := ss.CreateSession(SessionConfig{Model: "m"})
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}

	// 只改 enabled，budget 不动（nil 表示本次不改）
	on := true
	got, err := ss.UpdateSession(s.ID, SessionPatch{ThinkingEnabled: &on})
	if err != nil {
		t.Fatalf("UpdateSession 失败: %v", err)
	}
	if !got.ThinkingEnabled {
		t.Fatal("ThinkingEnabled patch 未生效")
	}

	// 再改 budget
	budget := 12000
	got, err = ss.UpdateSession(s.ID, SessionPatch{ThinkingBudget: &budget})
	if err != nil {
		t.Fatalf("UpdateSession 失败: %v", err)
	}
	if got.ThinkingBudget != 12000 {
		t.Fatalf("ThinkingBudget patch 未生效，实际 %d", got.ThinkingBudget)
	}
	if !got.ThinkingEnabled {
		t.Fatal("只改 budget 时不应把 enabled 冲掉")
	}

	// 关掉：enabled=false + budget=0（前端关掉时会同时清空）
	off := false
	zero := 0
	got, err = ss.UpdateSession(s.ID, SessionPatch{ThinkingEnabled: &off, ThinkingBudget: &zero})
	if err != nil {
		t.Fatalf("UpdateSession 失败: %v", err)
	}
	if got.ThinkingEnabled || got.ThinkingBudget != 0 {
		t.Fatalf("关闭 thinking 未生效，实际 enabled=%v budget=%d", got.ThinkingEnabled, got.ThinkingBudget)
	}
}

// 消息的 thinking 正文与签名要能落盘读回（签名是回传必需项）
func TestMessageThinkingRoundTrip(t *testing.T) {
	ss := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	s, err := ss.CreateSession(SessionConfig{Model: "m"})
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	if _, err := ss.AppendMessage(s.ID, Message{
		Role:              RoleAssistant,
		Content:           "答案",
		Thinking:          "思考过程",
		ThinkingSignature: "sig-1",
	}); err != nil {
		t.Fatalf("AppendMessage 失败: %v", err)
	}

	got, err := ss.GetSession(s.ID)
	if err != nil {
		t.Fatalf("GetSession 失败: %v", err)
	}
	var found bool
	for _, m := range got.Messages {
		if m.Role == RoleAssistant && m.Thinking != "" {
			found = true
			if m.Thinking != "思考过程" || m.ThinkingSignature != "sig-1" {
				t.Fatalf("thinking 往返不正确: %q / %q", m.Thinking, m.ThinkingSignature)
			}
		}
	}
	if !found {
		t.Fatal("未找到带 thinking 的 assistant 消息")
	}
}
