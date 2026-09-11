package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelStore_CRUD(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "models.json")

	store := NewModelStore(storePath)

	// 初始应为空
	if got := store.GetModels(); len(got) != 0 {
		t.Fatalf("初始模型列表应为空，实际: %v", got)
	}

	// 添加模型
	m1 := Model{Name: "claude-sonnet", Alias: "Sonnet", APIKey: "sk-123", URL: "https://api.example.com"}
	if err := store.AddModel(m1); err != nil {
		t.Fatalf("添加模型失败: %v", err)
	}

	// 重复名称应报错
	if err := store.AddModel(m1); err == nil {
		t.Fatal("重复名称应返回错误")
	}

	// 空名称应报错
	if err := store.AddModel(Model{}); err == nil {
		t.Fatal("空名称应返回错误")
	}

	// 查询
	models := store.GetModels()
	if len(models) != 1 || models[0].Name != "claude-sonnet" {
		t.Fatalf("模型列表异常: %v", models)
	}

	names := store.GetModelNames()
	if len(names) != 1 || names[0] != "claude-sonnet" {
		t.Fatalf("模型名称列表异常: %v", names)
	}

	// GetModel
	got, err := store.GetModel("claude-sonnet")
	if err != nil {
		t.Fatalf("GetModel 失败: %v", err)
	}
	if got.APIKey != "sk-123" {
		t.Fatalf("APIKey 不匹配: %s", got.APIKey)
	}

	// GetModel 不存在
	if _, err := store.GetModel("nonexistent"); err == nil {
		t.Fatal("不存在的模型应返回错误")
	}

	// 删除
	if err := store.DeleteModel("claude-sonnet"); err != nil {
		t.Fatalf("删除模型失败: %v", err)
	}
	if len(store.GetModels()) != 0 {
		t.Fatal("删除后模型列表应为空")
	}

	// 删除不存在的模型
	if err := store.DeleteModel("nonexistent"); err == nil {
		t.Fatal("删除不存在的模型应返回错误")
	}
}

func TestModelStore_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "models.json")

	// 第一个 store 添加模型
	store1 := NewModelStore(storePath)
	if err := store1.AddModel(Model{Name: "model-a", APIKey: "key-a"}); err != nil {
		t.Fatalf("添加失败: %v", err)
	}

	// 验证文件已创建
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		t.Fatal("配置文件未创建")
	}

	// 第二个 store 从同一文件加载，应能读到之前的模型
	store2 := NewModelStore(storePath)
	models := store2.GetModels()
	if len(models) != 1 || models[0].Name != "model-a" {
		t.Fatalf("持久化加载失败: %v", models)
	}
	if models[0].APIKey != "key-a" {
		t.Fatalf("APIKey 持久化失败: %s", models[0].APIKey)
	}
}
