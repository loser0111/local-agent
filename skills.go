package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	skillFileName     = "SKILL.md"
	skillScriptsDir   = "scripts"
	maxSkillBodyBytes = 256 * 1024 // 正文上限 256KB
	skillListMaxDesc  = 200        // L1 清单单条描述截断长度
)

// ===== 数据结构 =====

// SkillMeta 技能元数据（L1，会进入 system prompt）
type SkillMeta struct {
	ID           string `json:"id"`           // 目录名，唯一
	Name         string `json:"name"`         // frontmatter.name
	Description  string `json:"description"`  // frontmatter.description
	Dir          string `json:"dir"`          // 技能目录绝对路径
	Enabled      bool   `json:"enabled"`      // 是否全局启用
	AlwaysInject bool   `json:"alwaysInject"` // true=正文直接注入 system prompt
	Builtin      bool   `json:"builtin"`      // 内置不可删
	HasScripts   bool   `json:"hasScripts"`   // 是否存在 scripts/ 目录
	Error        string `json:"error"`        // 解析/校验错误（展示用）
}

// SkillDetail 技能详情（含正文，供界面预览与 read_skill 返回）
type SkillDetail struct {
	SkillMeta
	Body string `json:"body"`
}

// SkillState 开关状态（持久化到 skills_state.json）
type SkillState struct {
	ID           string `json:"id"`
	Enabled      bool   `json:"enabled"`
	AlwaysInject bool   `json:"alwaysInject"`
}

// ===== SkillStore =====

// SkillStore 扫描技能目录、维护开关状态
type SkillStore struct {
	mu        sync.RWMutex
	dir       string
	statePath string
	skills    []*SkillMeta
}

// Dir 返回技能根目录绝对路径（供界面「打开目录」）
func (s *SkillStore) Dir() string { return s.dir }

// NewSkillStore 创建技能存储并首次扫描目录
func NewSkillStore(dir string) *SkillStore {
	s := &SkillStore{
		dir:       dir,
		statePath: filepath.Join(filepath.Dir(dir), "skills_state.json"),
	}
	_ = os.MkdirAll(dir, 0o755)
	s.scan()
	return s
}

// scan 遍历技能目录，解析每个子目录下的 SKILL.md
func (s *SkillStore) scan() {
	s.mu.Lock()
	defer s.mu.Unlock()

	states := s.loadStatesLocked()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		s.skills = []*SkillMeta{}
		return
	}

	skills := make([]*SkillMeta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		meta := parseSkillDir(id, filepath.Join(s.dir, id))
		if st, ok := states[id]; ok {
			meta.Enabled = st.Enabled
			meta.AlwaysInject = st.AlwaysInject
		} else {
			meta.Enabled = true // 新技能默认启用
		}
		skills = append(skills, meta)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	s.skills = skills
}

// Refresh 重新扫描技能目录（用户手动改目录后调用）
func (s *SkillStore) Refresh() { s.scan() }

// parseSkillDir 解析单个技能目录
func parseSkillDir(id, dir string) *SkillMeta {
	meta := &SkillMeta{ID: id, Name: id, Dir: dir}
	data, err := os.ReadFile(filepath.Join(dir, skillFileName))
	if err != nil {
		meta.Error = "缺少 SKILL.md"
		return meta
	}
	name, desc, _, err := parseFrontmatter(string(data))
	if err != nil {
		meta.Error = err.Error()
		return meta
	}
	if name != "" {
		meta.Name = name
	}
	meta.Description = desc
	if desc == "" {
		meta.Error = "frontmatter 缺少 description"
	}
	if fi, err := os.Stat(filepath.Join(dir, skillScriptsDir)); err == nil && fi.IsDir() {
		meta.HasScripts = true
	}
	return meta
}

// parseFrontmatter 解析 SKILL.md 的 YAML frontmatter（仅支持 key: value 单行子集）
// 返回 name、description 与不含 frontmatter 的正文
func parseFrontmatter(text string) (name, desc, body string, err error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", "", text, nil // 无 frontmatter，宽松处理
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", "", fmt.Errorf("frontmatter 未闭合")
	}
	head := rest[:end]
	body = strings.TrimLeft(rest[end+len("\n---"):], "\n")

	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "name":
			name = v
		case "description":
			desc = v
		}
	}
	return name, desc, body, nil
}

// ===== 读取 =====

// GetAll 返回全部技能元数据（副本切片）
func (s *SkillStore) GetAll() []*SkillMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*SkillMeta, len(s.skills))
	copy(out, s.skills)
	return out
}

// GetDetail 返回含正文的技能详情
func (s *SkillStore) GetDetail(id string) (*SkillDetail, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.skills {
		if m.ID == id {
			body, _ := readSkillBody(m.Dir)
			return &SkillDetail{SkillMeta: *m, Body: body}, true
		}
	}
	return nil, false
}

// LoadBody 读取技能正文（供 read_skill 工具）
func (s *SkillStore) LoadBody(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.skills {
		if m.ID == id {
			return readSkillBody(m.Dir)
		}
	}
	return "", fmt.Errorf("技能不存在: %s", id)
}

// readSkillBody 读取并截断 SKILL.md 正文（去除 frontmatter）
func readSkillBody(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, skillFileName))
	if err != nil {
		return "", fmt.Errorf("读取 SKILL.md 失败: %w", err)
	}
	if len(data) > maxSkillBodyBytes {
		data = data[:maxSkillBodyBytes]
	}
	_, _, body, _ := parseFrontmatter(string(data))
	return body, nil
}

// ===== 开关状态 =====

// loadStatesLocked 读取状态文件；调用方需持锁
func (s *SkillStore) loadStatesLocked() map[string]SkillState {
	m := map[string]SkillState{}
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return m
	}
	var list []SkillState
	if json.Unmarshal(data, &list) != nil {
		return m
	}
	for _, st := range list {
		m[st.ID] = st
	}
	return m
}

// saveStates 持久化开关状态
func (s *SkillStore) saveStatesLocked() error {
	list := make([]SkillState, 0, len(s.skills))
	for _, m := range s.skills {
		list = append(list, SkillState{ID: m.ID, Enabled: m.Enabled, AlwaysInject: m.AlwaysInject})
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.statePath, data, 0o644)
}

// SetEnabled 切换技能启用状态
func (s *SkillStore) SetEnabled(id string, enabled bool) error {
	s.mu.Lock()
	found := false
	for _, m := range s.skills {
		if m.ID == id {
			m.Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return fmt.Errorf("技能不存在: %s", id)
	}
	err := s.saveStatesLocked()
	s.mu.Unlock()
	return err
}

// SetAlwaysInject 切换「强制注入正文」
func (s *SkillStore) SetAlwaysInject(id string, v bool) error {
	s.mu.Lock()
	found := false
	for _, m := range s.skills {
		if m.ID == id {
			m.AlwaysInject = v
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return fmt.Errorf("技能不存在: %s", id)
	}
	err := s.saveStatesLocked()
	s.mu.Unlock()
	return err
}

// ===== 增删 =====

// SaveSkill 新建或更新技能：写入 <dir>/<id>/SKILL.md，然后重扫
func (s *SkillStore) SaveSkill(id, name, desc, body string) error {
	if !validSkillID(id) {
		return fmt.Errorf("技能 id 只能包含字母、数字、下划线、短横线")
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("技能名称不能为空")
	}
	skillDir := filepath.Join(s.dir, id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("创建技能目录失败: %w", err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n",
		name, strings.TrimSpace(desc), strings.TrimRight(body, "\n"))
	if err := os.WriteFile(filepath.Join(skillDir, skillFileName), []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入 SKILL.md 失败: %w", err)
	}
	s.scan()
	return nil
}

// DeleteSkill 删除技能目录（内置拒绝）
func (s *SkillStore) DeleteSkill(id string) error {
	s.mu.RLock()
	var target *SkillMeta
	for _, m := range s.skills {
		if m.ID == id {
			target = m
			break
		}
	}
	s.mu.RUnlock()
	if target == nil {
		return fmt.Errorf("技能不存在: %s", id)
	}
	if target.Builtin {
		return fmt.Errorf("内置技能不可删除")
	}
	if err := os.RemoveAll(target.Dir); err != nil {
		return fmt.Errorf("删除技能目录失败: %w", err)
	}
	s.scan()
	return nil
}

// EnsureDefaultSkill 首次运行写入示例技能
func (s *SkillStore) EnsureDefaultSkill() {
	if len(s.GetAll()) > 0 {
		return
	}
	_ = s.SaveSkill("hello-skill", "示例技能",
		"演示用技能：当用户请求打招呼时给出固定问候。",
		"# 示例技能\n\n当用户请求打招呼时，直接回复：你好，我是 local-agent 的技能。\n")
}

// validSkillID 校验技能 id（同工具名规则）
func validSkillID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// ===== L1 清单 / 强制注入 =====

// BuildSkillIndex 生成 L1 技能清单（注入 system prompt）
func BuildSkillIndex(skills []*SkillMeta) string {
	var sb strings.Builder
	for _, m := range skills {
		if !m.Enabled || m.Error != "" {
			continue
		}
		desc := m.Description
		if r := []rune(desc); len(r) > skillListMaxDesc {
			desc = string(r[:skillListMaxDesc]) + "..."
		}
		sb.WriteString(fmt.Sprintf("- %s (id: %s): %s\n", m.Name, m.ID, desc))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// BuildAlwaysInjectBlock 生成「强制注入」技能正文段（方案 B）
func BuildAlwaysInjectBlock(skills []*SkillMeta) string {
	var sb strings.Builder
	for _, m := range skills {
		if !m.Enabled || !m.AlwaysInject || m.Error != "" {
			continue
		}
		body, err := readSkillBody(m.Dir)
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n\n### 技能：%s\n%s", m.Name, body))
	}
	return sb.String()
}

// hasEnabledSkill 是否存在已启用且无错的技能
func hasEnabledSkill(skills []*SkillMeta) bool {
	for _, m := range skills {
		if m.Enabled && m.Error == "" {
			return true
		}
	}
	return false
}

// ===== 内置工具：read_skill（L2 按需加载）=====

// ReadSkillTool 让模型按需读取 SKILL.md 正文；
// 同时受全局开关与会话技能白名单约束，未授权 id 不可读取。
type ReadSkillTool struct {
	*BaseTool
	store   *SkillStore
	allowed func(*SkillMeta) bool
}

// NewReadSkillTool 构造 read_skill 工具。
// skillWhitelist 为空表示全部已启用技能可读；非空时仅白名单内技能可读。
func NewReadSkillTool(store *SkillStore, skillWhitelist []string) *ReadSkillTool {
	wl := map[string]bool{}
	for _, id := range skillWhitelist {
		wl[id] = true
	}
	t := &ReadSkillTool{
		BaseTool: &BaseTool{
			Name: "read_skill",
			Description: "读取指定技能的完整说明（SKILL.md 正文）。" +
				"当用户请求与某个技能的 description 匹配时，先调用本工具获取正文，" +
				"再按正文步骤执行；如需运行技能自带脚本，请使用 exec_shell。",
			Parameters: map[string]*ToolArgDef{
				"id": {Type: "string", Description: "技能 id（从可用技能清单获取）"},
			},
		},
		store: store,
	}
	t.allowed = func(m *SkillMeta) bool {
		if !m.Enabled || m.Error != "" {
			return false
		}
		if len(wl) > 0 {
			return wl[m.ID]
		}
		return true
	}
	return t
}

// HasAvailable 是否存在本会话可读取的技能（决定是否注册该工具）
func (t *ReadSkillTool) HasAvailable() bool {
	for _, m := range t.store.GetAll() {
		if t.allowed(m) {
			return true
		}
	}
	return false
}

func (t *ReadSkillTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id 参数是必需的")
	}
	for _, m := range t.store.GetAll() {
		if m.ID != id {
			continue
		}
		if !t.allowed(m) {
			return "", fmt.Errorf("技能在本会话不可用: %s", id)
		}
		body, err := readSkillBody(m.Dir)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("# 技能：%s\n\n%s", m.Name, body), nil
	}
	return "", fmt.Errorf("技能不存在: %s", id)
}

// DescribeOperation read_skill 是只读工具，限定符作用于技能 id
// （例如 read_skill(pdf-report)）
func (t *ReadSkillTool) DescribeOperation(args map[string]interface{}) PermissionSubject {
	id, _ := args["id"].(string)
	return PermissionSubject{
		ToolName:  t.GetName(),
		SpecValue: id,
		Raw:       args,
	}
}
