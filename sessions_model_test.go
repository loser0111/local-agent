package main

import (
	"path/filepath"
	"testing"
)

func TestSessionStore_CountSessionsByModel(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	// 创建 3 个会话，其中 2 个使用 "model-a"
	s1, _ := store.CreateSession(SessionConfig{Title: "s1", Model: "model-a"})
	s2, _ := store.CreateSession(SessionConfig{Title: "s2", Model: "model-a"})
	s3, _ := store.CreateSession(SessionConfig{Title: "s3", Model: "model-b"})
	_ = s1
	_ = s2
	_ = s3

	// model-a 应有 2 个引用
	count, err := store.CountSessionsByModel("model-a")
	if err != nil {
		t.Fatalf("CountSessionsByModel 失败: %v", err)
	}
	if count != 2 {
		t.Fatalf("model-a 引用数应为 2，实际: %d", count)
	}

	// model-b 应有 1 个引用
	count, err = store.CountSessionsByModel("model-b")
	if err != nil {
		t.Fatalf("CountSessionsByModel 失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("model-b 引用数应为 1，实际: %d", count)
	}

	// 不存在的模型应为 0
	count, err = store.CountSessionsByModel("nonexistent")
	if err != nil {
		t.Fatalf("CountSessionsByModel 失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("nonexistent 引用数应为 0，实际: %d", count)
	}
}

func TestSessionStore_RenameModelReference(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)

	// 创建会话
	store.CreateSession(SessionConfig{Title: "s1", Model: "model-a"})
	store.CreateSession(SessionConfig{Title: "s2", Model: "model-a"})
	store.CreateSession(SessionConfig{Title: "s3", Model: "model-b"})

	// 重命名 model-a → model-c
	updated, err := store.RenameModelReference("model-a", "model-c")
	if err != nil {
		t.Fatalf("RenameModelReference 失败: %v", err)
	}
	if updated != 2 {
		t.Fatalf("应更新 2 个会话，实际: %d", updated)
	}

	// 验证会话中的 model 已更新
	sessions, _ := store.ListSessions()
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

func TestApp_DeleteModelWithSessions(t *testing.T) {
	dir := t.TempDir()
	app := &App{
		modelStore:   NewModelStore(filepath.Join(dir, "models.json")),
		sessionStore: NewSessionStore(filepath.Join(dir, "sessions")),
	}

	// 添加模型
	app.modelStore.AddModel(Model{Name: "model-a", APIKey: "sk-123"})

	// 创建使用该模型的会话
	app.sessionStore.CreateSession(SessionConfig{Title: "s1", Model: "model-a"})

	// 删除模型应失败（有会话引用）
	err := app.DeleteModel("model-a")
	if err == nil {
		t.Fatal("有会话引用时删除模型应失败")
	}

	// 删除会话后再删模型应成功
	sessions, _ := app.sessionStore.ListSessions()
	for _, s := range sessions {
		app.sessionStore.DeleteSession(s.ID)
	}
	err = app.DeleteModel("model-a")
	if err != nil {
		t.Fatalf("删除模型失败: %v", err)
	}
}

func TestApp_UpdateModelRename(t *testing.T) {
	dir := t.TempDir()
	app := &App{
		modelStore:   NewModelStore(filepath.Join(dir, "models.json")),
		sessionStore: NewSessionStore(filepath.Join(dir, "sessions")),
	}

	// 添加模型
	app.modelStore.AddModel(Model{Name: "model-a", APIKey: "sk-123", Alias: "A"})

	// 创建使用该模型的会话
	app.sessionStore.CreateSession(SessionConfig{Title: "s1", Model: "model-a"})
	app.sessionStore.CreateSession(SessionConfig{Title: "s2", Model: "model-a"})

	// 重命名模型为 model-c
	err := app.UpdateModel("model-a", Model{Name: "model-c", Alias: "A-renamed", APIKey: "sk-123"})
	if err != nil {
		t.Fatalf("重命名失败: %v", err)
	}

	// 验证模型名称已更新
	_, err = app.modelStore.GetModelFull("model-c")
	if err != nil {
		t.Fatalf("model-c 应存在: %v", err)
	}
	_, err = app.modelStore.GetModelFull("model-a")
	if err == nil {
		t.Fatal("model-a 应不存在")
	}

	// 验证会话引用已级联更新
	sessions, _ := app.sessionStore.ListSessions()
	for _, s := range sessions {
		if s.Model != "model-c" {
			t.Fatalf("会话 %s 的 model 应为 model-c，实际: %s", s.ID, s.Model)
		}
	}
}

func TestApp_TestModelConnection_Validation(t *testing.T) {
	app := &App{}

	// 空模型名称
	err := app.TestModelConnection(Model{})
	if err == nil {
		t.Fatal("空模型名称应返回错误")
	}

	// 空 URL
	err = app.TestModelConnection(Model{Name: "test"})
	if err == nil {
		t.Fatal("空 URL 应返回错误")
	}
}
