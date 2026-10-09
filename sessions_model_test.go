package main

import (
	"path/filepath"
	"testing"

	"wails-tmp/internal/store"
)

func TestApp_DeleteModelWithSessions(t *testing.T) {
	dir := t.TempDir()
	app := &App{
		modelStore:   store.NewModelStore(filepath.Join(dir, "models.json")),
		sessionStore: store.NewSessionStore(filepath.Join(dir, "sessions")),
	}

	// 添加模型
	app.modelStore.AddModel(store.Model{Name: "model-a", APIKey: "sk-123"})

	// 创建使用该模型的会话
	app.sessionStore.CreateSession(store.SessionConfig{Title: "s1", Model: "model-a"})

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
		modelStore:   store.NewModelStore(filepath.Join(dir, "models.json")),
		sessionStore: store.NewSessionStore(filepath.Join(dir, "sessions")),
	}

	// 添加模型
	app.modelStore.AddModel(store.Model{Name: "model-a", APIKey: "sk-123", Alias: "A"})

	// 创建使用该模型的会话
	app.sessionStore.CreateSession(store.SessionConfig{Title: "s1", Model: "model-a"})
	app.sessionStore.CreateSession(store.SessionConfig{Title: "s2", Model: "model-a"})

	// 重命名模型为 model-c
	err := app.UpdateModel("model-a", store.Model{Name: "model-c", Alias: "A-renamed", APIKey: "sk-123"})
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
	err := app.TestModelConnection(store.Model{})
	if err == nil {
		t.Fatal("空模型名称应返回错误")
	}

	// 空 URL
	err = app.TestModelConnection(store.Model{Name: "test"})
	if err == nil {
		t.Fatal("空 URL 应返回错误")
	}
}
