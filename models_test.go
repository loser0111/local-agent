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

	// 查询（GetModels 脱敏）
	models := store.GetModels()
	if len(models) != 1 || models[0].Name != "claude-sonnet" {
		t.Fatalf("模型列表异常: %v", models)
	}
	// APIKey 应被脱敏
	if models[0].APIKey == "sk-123" {
		t.Fatalf("GetModels 返回的 APIKey 应被脱敏")
	}

	names := store.GetModelNames()
	if len(names) != 1 || names[0] != "claude-sonnet" {
		t.Fatalf("模型名称列表异常: %v", names)
	}

	// GetModel（脱敏）
	got, err := store.GetModel("claude-sonnet")
	if err != nil {
		t.Fatalf("GetModel 失败: %v", err)
	}
	if got.APIKey == "sk-123" {
		t.Fatalf("GetModel 返回的 APIKey 应被脱敏")
	}

	// GetModelFull（不脱敏）
	gotFull, err := store.GetModelFull("claude-sonnet")
	if err != nil {
		t.Fatalf("GetModelFull 失败: %v", err)
	}
	if gotFull.APIKey != "sk-123" {
		t.Fatalf("GetModelFull APIKey 不匹配: %s", gotFull.APIKey)
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
	// 使用 GetModelFull 验证原始 APIKey
	got, err := store2.GetModelFull("model-a")
	if err != nil {
		t.Fatalf("GetModelFull 失败: %v", err)
	}
	if got.APIKey != "key-a" {
		t.Fatalf("APIKey 持久化失败: %s", got.APIKey)
	}
}

func TestModelStore_Rename(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "models.json")
	store := NewModelStore(storePath)

	// 添加两个模型
	if err := store.AddModel(Model{Name: "model-a", Alias: "A", APIKey: "key-a"}); err != nil {
		t.Fatalf("添加 model-a 失败: %v", err)
	}
	if err := store.AddModel(Model{Name: "model-b", APIKey: "key-b"}); err != nil {
		t.Fatalf("添加 model-b 失败: %v", err)
	}

	// 重命名 model-a → model-c
	if err := store.UpdateModel("model-a", Model{Name: "model-c", Alias: "A-renamed", APIKey: "key-a"}); err != nil {
		t.Fatalf("重命名失败: %v", err)
	}

	// 验证旧名称不存在
	if _, err := store.GetModelFull("model-a"); err == nil {
		t.Fatal("旧名称应不存在")
	}
	// 验证新名称存在
	got, err := store.GetModelFull("model-c")
	if err != nil {
		t.Fatalf("获取重命名后的模型失败: %v", err)
	}
	if got.Alias != "A-renamed" {
		t.Fatalf("Alias 不匹配: %s", got.Alias)
	}

	// 重命名为已存在的名称应报错
	err = store.UpdateModel("model-c", Model{Name: "model-b"})
	if err == nil {
		t.Fatal("重命名为已存在的名称应报错")
	}

	// 不改名的更新（Name 为空）
	if err := store.UpdateModel("model-c", Model{Alias: "new-alias", APIKey: "key-a"}); err != nil {
		t.Fatalf("不改名更新失败: %v", err)
	}
	got2, _ := store.GetModelFull("model-c")
	if got2.Alias != "new-alias" {
		t.Fatalf("Alias 更新失败: %s", got2.Alias)
	}
}

func TestModelStore_UpdatePreservesAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "models.json")
	store := NewModelStore(storePath)

	if err := store.AddModel(Model{Name: "m1", APIKey: "sk-secret"}); err != nil {
		t.Fatalf("添加失败: %v", err)
	}

	// 更新时 APIKey 传空（前端脱敏后未修改场景）
	if err := store.UpdateModel("m1", Model{Name: "m1", Alias: "updated", APIKey: ""}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	got, _ := store.GetModelFull("m1")
	if got.APIKey != "sk-secret" {
		t.Fatalf("APIKey 应保留旧值，实际: %s", got.APIKey)
	}
}

func TestModelStore_UpdateContextWindow(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "models.json")
	store := NewModelStore(storePath)

	if err := store.AddModel(Model{Name: "m1", APIKey: "sk-1", ContextWindow: 200000}); err != nil {
		t.Fatalf("添加失败: %v", err)
	}

	// 更新时编辑上下文窗口：新值应生效
	if err := store.UpdateModel("m1", Model{Name: "m1", APIKey: "sk-1", ContextWindow: 128000}); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got, _ := store.GetModelFull("m1")
	if got.ContextWindow != 128000 {
		t.Fatalf("编辑后 ContextWindow 应更新为 128000，实际: %d", got.ContextWindow)
	}

	// 更新时未填上下文窗口（表单里被清空）：显式清空 = 回落默认窗口，
	// 与 contextWindowOf 的 ">0 才用配置值" 回退设计一致
	if err := store.UpdateModel("m1", Model{Name: "m1", Alias: "x", APIKey: "sk-1"}); err != nil {
		t.Fatalf("二次更新失败: %v", err)
	}
	got2, _ := store.GetModelFull("m1")
	if got2.ContextWindow != 0 {
		t.Fatalf("清空后 ContextWindow 应为 0（回落默认窗口），实际: %d", got2.ContextWindow)
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"ab", "**"},
		{"abc", "***"},
		{"abcd", "****"},
		{"abcde", "*bcde"},
		{"sk-1234567890", "*********7890"},
	}
	for _, tt := range tests {
		got := maskAPIKey(tt.input)
		if got != tt.expected {
			t.Errorf("maskAPIKey(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
