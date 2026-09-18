package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Model 表示一个可配置的 AI 模型
type Model struct {
	Name     string `json:"name"`               // 模型名称（唯一标识）
	Alias    string `json:"alias"`              // 模型别名（展示用）
	ModelID  string `json:"modelId"`            // 实际模型 ID（如 deepseek-chat），为空则用 Name
	APIKey   string `json:"apiKey"`             // API Key
	URL      string `json:"url"`                // API 端点 URL
	Protocol string `json:"protocol,omitempty"` // 协议类型：openai / anthropic，留空自动推断

	// ContextWindow 模型上下文窗口（token 数）。0 表示未配置，按保守默认值（32K）处理。
	// 只影响「何时压缩上下文」，不影响请求本身；配小了会压得偏早（浪费一点 token），
	// 配大了只会更晚触发，撑爆时还有"超限报错→压缩重试"兜底。
	ContextWindow int `json:"contextWindow,omitempty"`
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

// GetModels 返回所有模型（APIKey 脱敏：仅保留后 4 位）
func (ms *ModelStore) GetModels() []Model {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	result := make([]Model, len(ms.models))
	copy(result, ms.models)
	for i := range result {
		result[i].APIKey = maskAPIKey(result[i].APIKey)
	}
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
		ms.models = append(ms.models, Model{})
		copy(ms.models[idx+1:], ms.models[idx:])
		ms.models[idx] = removed
		return err
	}
	return nil
}

// UpdateModel 根据 name 查找已有模型，用 newModel 的字段覆盖。
// 支持重命名：若 newModel.Name 与旧 name 不同，先检查新名称唯一性，再更新。
func (ms *ModelStore) UpdateModel(name string, newModel Model) error {
	if name == "" {
		return fmt.Errorf("模型名称不能为空")
	}

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

	newName := newModel.Name
	if newName == "" {
		newName = name // 不改名
	}
	if newName != name {
		// 检查新名称是否已被其他模型占用
		for i, m := range ms.models {
			if i != idx && m.Name == newName {
				return fmt.Errorf("模型名称 %q 已存在", newName)
			}
		}
	}

	old := ms.models[idx]
	ms.models[idx] = Model{
		Name:     newName,
		Alias:    newModel.Alias,
		ModelID:  newModel.ModelID,
		APIKey:   newModel.APIKey,
		URL:      newModel.URL,
		Protocol: newModel.Protocol,
	}
	// 若 APIKey 为空，表示前端未修改（脱敏后回填的占位值），保留旧值
	if ms.models[idx].APIKey == "" {
		ms.models[idx].APIKey = old.APIKey
	}

	if err := ms.save(); err != nil {
		ms.models[idx] = old
		return err
	}
	return nil
}

// GetModel 根据名称获取模型（**脱敏**）；不存在时返回错误。
//
// 仅供界面展示使用（模型列表、状态栏等）。**禁止**用于发起 LLM 请求：
// 返回值里的 APIKey 是 "****abcd" 形式的占位串，直接作为鉴权头发出会被
// 网关拒绝（401 Invalid API key format）。发请求请用 GetModelForCall。
func (ms *ModelStore) GetModel(name string) (Model, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	for _, m := range ms.models {
		if m.Name == name {
			copyM := m
			copyM.APIKey = maskAPIKey(copyM.APIKey)
			return copyM, nil
		}
	}
	return Model{}, fmt.Errorf("模型 %q 不存在", name)
}

// GetModelForCall 根据名称获取模型，返回**完整明文** APIKey，用于发起 LLM 请求。
// 这是聊天链路（executeChat / ChatPlan / ExecutePlan）唯一允许使用的取值入口，
// 与展示用的 GetModel 显式区分，避免脱敏 Key 泄漏到 HTTP 鉴权头。
func (ms *ModelStore) GetModelForCall(name string) (Model, error) {
	return ms.GetModelFull(name)
}

// GetModelFull 根据名称获取模型完整信息（不脱敏）；不存在时返回错误。
// 两类用途：一是发起 LLM 请求（推荐经 GetModelForCall）；二是编辑弹窗回填明文 Key。
// 注意：返回值含密钥，不要直接透传到日志或非本机界面。
func (ms *ModelStore) GetModelFull(name string) (Model, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	for _, m := range ms.models {
		if m.Name == name {
			return m, nil
		}
	}
	return Model{}, fmt.Errorf("模型 %q 不存在", name)
}

// maskAPIKey 脱敏 API Key：保留后 4 位，前面用 * 填充
func maskAPIKey(key string) string {
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	return strings.Repeat("*", len(key)-4) + key[len(key)-4:]
}
