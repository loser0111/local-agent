package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Model 表示一个可配置的 AI 模型
type Model struct {
	Name    string `json:"name"`    // 模型名称（唯一标识）
	Alias   string `json:"alias"`   // 模型别名（展示用）
	ModelID string `json:"modelId"` // 实际模型 ID（如 deepseek-chat），为空则用 Name
	APIKey  string `json:"apiKey"`  // API Key
	URL     string `json:"url"`     // API 端点 URL
}

// ModelStore 负责模型配置的 JSON 持久化
type ModelStore struct {
	mu       sync.RWMutex
	filePath string
	models   []Model
}

// NewModelStore 创建模型存储，并从指定文件加载已有配置
func NewModelStore(filePath string) *ModelStore {
	ms := &ModelStore{
		filePath: filePath,
		models:   []Model{},
	}
	ms.load()
	return ms
}

// load 从 JSON 文件加载模型配置；文件不存在则初始化为空列表
func (ms *ModelStore) load() {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	data, err := os.ReadFile(ms.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			ms.models = []Model{}
			return
		}
		fmt.Printf("[ModelStore] 读取配置文件失败: %v\n", err)
		ms.models = []Model{}
		return
	}

	if len(data) == 0 {
		ms.models = []Model{}
		return
	}

	var models []Model
	if err := json.Unmarshal(data, &models); err != nil {
		fmt.Printf("[ModelStore] 解析配置文件失败: %v\n", err)
		ms.models = []Model{}
		return
	}
	ms.models = models
}

// save 将当前模型列表写入 JSON 文件
func (ms *ModelStore) save() error {
	dir := filepath.Dir(ms.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	data, err := json.MarshalIndent(ms.models, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化模型配置失败: %w", err)
	}

	if err := os.WriteFile(ms.filePath, data, 0o644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}
	return nil
}

// GetModels 返回所有模型
func (ms *ModelStore) GetModels() []Model {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	result := make([]Model, len(ms.models))
	copy(result, ms.models)
	return result
}

// GetModelNames 返回所有模型名称
func (ms *ModelStore) GetModelNames() []string {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	names := make([]string, 0, len(ms.models))
	for _, m := range ms.models {
		names = append(names, m.Name)
	}
	return names
}

// AddModel 添加一个模型；若名称已存在则返回错误
func (ms *ModelStore) AddModel(model Model) error {
	if model.Name == "" {
		return fmt.Errorf("模型名称不能为空")
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	for _, m := range ms.models {
		if m.Name == model.Name {
			return fmt.Errorf("模型名称 %q 已存在", model.Name)
		}
	}

	ms.models = append(ms.models, model)
	if err := ms.save(); err != nil {
		// 回滚内存变更
		ms.models = ms.models[:len(ms.models)-1]
		return err
	}
	return nil
}

// DeleteModel 根据名称删除模型
func (ms *ModelStore) DeleteModel(name string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	idx := -1
	for i, m := range ms.models {
		if m.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("模型 %q 不存在", name)
	}

	removed := ms.models[idx]
	ms.models = append(ms.models[:idx], ms.models[idx+1:]...)
	if err := ms.save(); err != nil {
		// 回滚
		ms.models = append(ms.models, Model{})
		copy(ms.models[idx+1:], ms.models[idx:])
		ms.models[idx] = removed
		return err
	}
	return nil
}

// GetModel 根据名称获取模型；不存在时返回错误
func (ms *ModelStore) GetModel(name string) (Model, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	for _, m := range ms.models {
		if m.Name == name {
			return m, nil
		}
	}
	return Model{}, fmt.Errorf("模型 %q 不存在", name)
}
