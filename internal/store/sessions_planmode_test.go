package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSessionPlanModePatch 计划模式是会话字段：能落盘、能单独改、旧会话按 false。
func TestSessionPlanModePatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	ss := NewSessionStore(dir)

	s, err := ss.CreateSession(SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	if s.PlanMode {
		t.Fatalf("新会话默认不应开启计划模式")
	}

	on := true
	updated, err := ss.UpdateSession(s.ID, SessionPatch{PlanMode: &on})
	if err != nil {
		t.Fatalf("UpdateSession 失败: %v", err)
	}
	if !updated.PlanMode {
		t.Fatalf("开启后 PlanMode 应为 true")
	}

	// 重新从磁盘读：这条状态必须跨进程存活，否则"刷新就丢"的问题没被修掉
	reloaded, err := ss.GetSession(s.ID)
	if err != nil {
		t.Fatalf("GetSession 失败: %v", err)
	}
	if !reloaded.PlanMode {
		t.Fatalf("PlanMode 未持久化")
	}

	// 只改别的字段时不能顺手把它关掉（指针 nil = 本次不改）
	title := "改名了"
	untouched, err := ss.UpdateSession(s.ID, SessionPatch{Title: &title})
	if err != nil {
		t.Fatalf("UpdateSession 失败: %v", err)
	}
	if !untouched.PlanMode {
		t.Fatalf("未传 planMode 时不应改动，实际被关掉了")
	}

	off := false
	closed, err := ss.UpdateSession(s.ID, SessionPatch{PlanMode: &off})
	if err != nil {
		t.Fatalf("UpdateSession 失败: %v", err)
	}
	if closed.PlanMode {
		t.Fatalf("关闭后 PlanMode 应为 false")
	}
}

// TestSessionLegacyPlanFields 早于本次改动的会话文件：没有 planMode，
// 消息上没有 planStepIndex / attempt —— 读出来必须是零值，不能报错。
func TestSessionLegacyPlanFields(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	ss := NewSessionStore(dir)

	legacy := `{
  "id": "legacy",
  "title": "旧会话",
  "model": "m",
  "permissionMode": "manual",
  "messages": [
    {"id": "m1", "role": "user", "content": "老消息", "createdAt": 1700000000000}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "legacy.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("写入旧会话文件失败: %v", err)
	}

	s, err := ss.GetSession("legacy")
	if err != nil {
		t.Fatalf("读取旧会话失败: %v", err)
	}
	if s.PlanMode {
		t.Fatalf("旧会话没有 planMode 字段，应为 false（即老行为）")
	}
	if len(s.Messages) != 1 {
		t.Fatalf("消息条数异常: %d", len(s.Messages))
	}
	if s.Messages[0].PlanStepIndex != 0 || s.Messages[0].Attempt != 0 {
		t.Fatalf("旧消息的计划步骤字段应为 0，实际: index=%d attempt=%d",
			s.Messages[0].PlanStepIndex, s.Messages[0].Attempt)
	}

	// 新字段能正常落盘并读回（步骤归属与尝试次数）
	_, err = ss.AppendMessage("legacy", Message{
		Role: "user", Content: "【计划步骤 1/2】检查依赖", PlanStepIndex: 0, Attempt: 1,
	})
	if err != nil {
		t.Fatalf("AppendMessage 失败: %v", err)
	}
	reloaded, err := ss.GetSession("legacy")
	if err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	last := reloaded.Messages[len(reloaded.Messages)-1]
	if last.Attempt != 1 || last.PlanStepIndex != 0 {
		t.Fatalf("计划步骤字段未持久化，实际: index=%d attempt=%d", last.PlanStepIndex, last.Attempt)
	}
}
