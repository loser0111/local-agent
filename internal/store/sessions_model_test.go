package store

import "testing"

func TestSessionStore_CountSessionsByModel(t *testing.T) {
	dir := t.TempDir()
	ss := NewSessionStore(dir)

	// 创建 3 个会话，其中 2 个使用 "model-a"
	s1, _ := ss.CreateSession(SessionConfig{Title: "s1", Model: "model-a"})
	s2, _ := ss.CreateSession(SessionConfig{Title: "s2", Model: "model-a"})
	s3, _ := ss.CreateSession(SessionConfig{Title: "s3", Model: "model-b"})
	_ = s1
	_ = s2
	_ = s3

	// model-a 应有 2 个引用
	count, err := ss.CountSessionsByModel("model-a")
	if err != nil {
		t.Fatalf("CountSessionsByModel 失败: %v", err)
	}
	if count != 2 {
		t.Fatalf("model-a 引用数应为 2，实际: %d", count)
	}

	// model-b 应有 1 个引用
	count, err = ss.CountSessionsByModel("model-b")
	if err != nil {
		t.Fatalf("CountSessionsByModel 失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("model-b 引用数应为 1，实际: %d", count)
	}

	// 不存在的模型应为 0
	count, err = ss.CountSessionsByModel("nonexistent")
	if err != nil {
		t.Fatalf("CountSessionsByModel 失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("nonexistent 引用数应为 0，实际: %d", count)
	}
}

func TestSessionStore_RenameModelReference(t *testing.T) {
	dir := t.TempDir()
	ss := NewSessionStore(dir)

	// 创建会话
	ss.CreateSession(SessionConfig{Title: "s1", Model: "model-a"})
	ss.CreateSession(SessionConfig{Title: "s2", Model: "model-a"})
	ss.CreateSession(SessionConfig{Title: "s3", Model: "model-b"})

	// 重命名 model-a → model-c
	updated, err := ss.RenameModelReference("model-a", "model-c")
	if err != nil {
		t.Fatalf("RenameModelReference 失败: %v", err)
	}
	if updated != 2 {
		t.Fatalf("应更新 2 个会话，实际: %d", updated)
	}

	// 验证会话中的 model 已更新
	sessions, _ := ss.ListSessions()
	modelCCount := 0
	modelACount := 0
	for _, s := range sessions {
		if s.Model == "model-c" {
			modelCCount++
		}
		if s.Model == "model-a" {
			modelACount++
		}
	}
	if modelCCount != 2 {
		t.Fatalf("model-c 引用数应为 2，实际: %d", modelCCount)
	}
	if modelACount != 0 {
		t.Fatalf("model-a 引用数应为 0，实际: %d", modelACount)
	}
}
