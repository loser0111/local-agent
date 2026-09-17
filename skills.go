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

// ===== 技能目录约定 =====
//
// 一个技能 = 一个目录 + 一个 SKILL.md（标准形状）：
//
//	~/.local-agent/skills/<id>/
//	├── SKILL.md            # 必需：frontmatter（name/description…）+ 正文
//	├── scripts/            # 可选：可执行脚本（L3，用 exec_shell 运行）
//	├── references/         # 可选：长文档（L3，用 read_skill_file 读取）
//	└── assets/             # 可选：模板/素材（L3）
//
// 三级渐进式披露：
//
//	L1  BuildSkillIndex   所有可自动触发技能的 name+description 常驻 system prompt
//	L2  ReadSkillTool     模型判定相关后调用 read_skill 读取正文（+ 资源清单）
//	L3  ReadSkillFileTool 按需读取技能内的 references/assets 文件；脚本走 exec_shell
const (
	skillFileName     = "SKILL.md"
	skillScriptsDir   = "scripts"
	maxSkillBodyBytes = 256 * 1024 // 正文上限，超出截断
	skillListMaxDesc  = 400        // L1 清单单条描述截断长度（标准上限 1024，为省常驻 token 折中）
)

// SkillMeta 技能元数据（L1 层：进入 system prompt 的只有 ID 与 Description）
type SkillMeta struct {
	ID          string `json:"id"`          // 目录名，唯一主键
	Name        string `json:"name"`        // frontmatter.name（缺失时回落为 ID）
	Description string `json:"description"` // frontmatter.description
	Dir         string `json:"dir"`         // 技能目录绝对路径
	Enabled     bool   `json:"enabled"`     // 全局启用开关
	Builtin     bool   `json:"builtin"`     // 内置技能不可删除

	// 标准可选字段（仅展示与路由控制）
	Version                string   `json:"version,omitempty"`
	License                string   `json:"license,omitempty"`
	Author                 string   `json:"author,omitempty"`
	AllowedTools           []string `json:"allowedTools,omitempty"`
	DisableModelInvocation bool     `json:"disableModelInvocation"` // true=只允许 /名称 显式调用
	UserInvocable          bool     `json:"userInvocable"`          // 默认 true；false=不允许 /名称 调用

	HasScripts bool            `json:"hasScripts"`          // 是否存在 scripts/ 目录
	Resources  []SkillResource `json:"resources,omitempty"`

	Errors   []string          `json:"errors,omitempty"`   // 阻断性错误（界面标红、不参与路由）
	Warnings []string          `json:"warnings,omitempty"` // 提示性告警（不影响使用）
	Install  *SkillInstallInfo `json:"install,omitempty"`  // 安装来源（手动创建的技能为空）
}

// Usable 是否可用（已启用且无阻断性错误）
func (m *SkillMeta) Usable() bool {
	return m.Enabled && len(m.Errors) == 0
}

// CanAutoTrigger 是否可由模型自动触发（进入 L1 清单）
func (m *SkillMeta) CanAutoTrigger() bool {
	return m.Usable() && !m.DisableModelInvocation
}

// CanUserInvoke 是否可由用户用 /技能名 显式调用
func (m *SkillMeta) CanUserInvoke() bool {
	return m.Usable() && m.UserInvocable
}

// SkillInstallInfo 安装来源信息（落盘到 skills_state.json）
type SkillInstallInfo struct {
	SourceType  string `json:"sourceType"`            // folder | zip | git | manual
	Source      string `json:"source,omitempty"`      // 原始路径或 git URL
	Ref         string `json:"ref,omitempty"`         // git 分支/标签（仅 git）
	Subdir      string `json:"subdir,omitempty"`      // 仓库内子目录（仅 git）
	InstalledAt int64  `json:"installedAt,omitempty"` // Unix 秒
	CanUpdate   bool   `json:"canUpdate"`             // git 来源可重新拉取更新
}

// SkillDetail 技能详情（L2：正文与完整 frontmatter，供界面预览与 read_skill 返回）
type SkillDetail struct {
	SkillMeta
	Body        string            `json:"body"`
	Frontmatter *SkillFrontmatter `json:"frontmatter,omitempty"`
}

// SkillState 落盘的技能状态（开关 + 安装来源）
type SkillState struct {
	ID      string            `json:"id"`
	Enabled bool              `json:"enabled"`
	Install *SkillInstallInfo `json:"install,omitempty"`
}

// ===== SkillStore =====

// SkillStore 扫描技能目录、维护开关与安装状态
type SkillStore struct {
	mu        sync.RWMutex
	dir       string
	statePath string
	skills    []*SkillMeta
}

// Dir 返回技能根目录绝对路径（供界面「打开目录」与安装落点）
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
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		id := e.Name()
		meta := parseSkillDir(id, filepath.Join(s.dir, id))
		if st, ok := states[id]; ok {
			meta.Enabled = st.Enabled
			meta.Install = st.Install
		} else {
			meta.Enabled = true // 新技能默认启用
		}
		skills = append(skills, meta)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	s.skills = skills

	// 清理已消失技能的残留状态，避免状态文件无限膨胀
	if len(states) != len(skills) {
		_ = s.saveStatesLocked()
	}
}

// Refresh 重新扫描技能目录（用户手工改动目录、或安装器落盘后调用）
func (s *SkillStore) Refresh() { s.scan() }

// parseSkillDir 解析单个技能目录（目录名即 ID）
func parseSkillDir(id, dir string) *SkillMeta {
	meta := &SkillMeta{ID: id, Name: id, Dir: dir, UserInvocable: true}

	data, err := os.ReadFile(filepath.Join(dir, skillFileName))
	if err != nil {
		meta.Errors = []string{"缺少 " + skillFileName}
		return meta
	}

	fm, body, err := ParseSkillDocument(string(data))
	if err != nil {
		meta.Errors = []string{err.Error()}
		return meta
	}
	if fm.Name != "" {
		meta.Name = fm.Name
	}
	meta.Description = fm.Description
	meta.Version = fm.Version
	meta.License = fm.License
	meta.Author = fm.Author
	meta.AllowedTools = fm.AllowedTools
	meta.DisableModelInvocation = fm.DisableModelInvocation
	meta.UserInvocable = fm.UserInvocable

	meta.Errors, meta.Warnings = ValidateSkillDocument(id, fm, body)

	if fi, err := os.Stat(filepath.Join(dir, skillScriptsDir)); err == nil && fi.IsDir() {
		meta.HasScripts = true
	}
	meta.Resources = listSkillResources(dir)
	return meta
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

// Get 按 ID 取技能元数据
func (s *SkillStore) Get(id string) (*SkillMeta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.skills {
		if m.ID == id {
			cp := *m
			return &cp, true
		}
	}
	return nil, false
}

// GetDetail 返回含正文与完整 frontmatter 的技能详情
func (s *SkillStore) GetDetail(id string) (*SkillDetail, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.skills {
		if m.ID != id {
			continue
		}
		detail := &SkillDetail{SkillMeta: *m}
		if fm, body, err := readSkillDocument(m.Dir); err == nil {
			detail.Frontmatter = fm
			detail.Body = body
		}
		return detail, true
	}
	return nil, false
}

// LoadBody 读取技能正文（供 read_skill 工具）
func (s *SkillStore) LoadBody(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.skills {
		if m.ID == id {
			_, body, err := readSkillDocument(m.Dir)
			return body, err
		}
	}
	return "", fmt.Errorf("技能不存在: %s", id)
}

// readSkillDocument 读取并解析技能目录下的 SKILL.md
func readSkillDocument(dir string) (*SkillFrontmatter, string, error) {
	data, err := os.ReadFile(filepath.Join(dir, skillFileName))
	if err != nil {
		return nil, "", fmt.Errorf("读取 %s 失败: %w", skillFileName, err)
	}
	fm, body, err := ParseSkillDocument(string(data))
	if err != nil {
		return nil, "", err
	}
	return fm, body, nil
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

// saveStatesLocked 持久化状态；调用方需持锁
func (s *SkillStore) saveStatesLocked() error {
	list := make([]SkillState, 0, len(s.skills))
	for _, m := range s.skills {
		list = append(list, SkillState{ID: m.ID, Enabled: m.Enabled, Install: m.Install})
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
	defer s.mu.Unlock()
	for _, m := range s.skills {
		if m.ID == id {
			m.Enabled = enabled
			return s.saveStatesLocked()
		}
	}
	return fmt.Errorf("技能不存在: %s", id)
}

// SetInstall 记录安装来源（安装器在技能落盘后调用）
func (s *SkillStore) SetInstall(id string, info *SkillInstallInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.skills {
		if m.ID == id {
			m.Install = info
			return s.saveStatesLocked()
		}
	}
	return fmt.Errorf("技能不存在: %s", id)
}

// ===== 增删 =====

// SaveSkillDraft 保存技能的入参（界面编辑器提交的表单）
type SkillDraft struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	Version                string   `json:"version,omitempty"`
	License                string   `json:"license,omitempty"`
	Author                 string   `json:"author,omitempty"`
	AllowedTools           []string `json:"allowedTools,omitempty"`
	DisableModelInvocation bool     `json:"disableModelInvocation,omitempty"`
	UserInvocable          bool     `json:"userInvocable"`
	Body                   string   `json:"body"`
}

// SaveSkill 新建或更新技能：写入 <dir>/<id>/SKILL.md，然后重扫
func (s *SkillStore) SaveSkill(id string, draft SkillDraft) error {
	if !validSkillID(id) {
		return fmt.Errorf("技能 ID 只能包含字母、数字、下划线、短横线，且不超过 64 字符")
	}
	if strings.TrimSpace(draft.Description) == "" {
		return fmt.Errorf("description 不能为空：模型靠它判断何时使用本技能")
	}
	if !s.canWrite(id) {
		return fmt.Errorf("技能 %s 由安装器管理，请先卸载后再编辑", id)
	}

	skillDir := filepath.Join(s.dir, id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return fmt.Errorf("创建技能目录失败: %w", err)
	}

	fm := &SkillFrontmatter{
		Name:                   strings.TrimSpace(draft.Name),
		Description:            strings.TrimSpace(draft.Description),
		Version:                strings.TrimSpace(draft.Version),
		License:                strings.TrimSpace(draft.License),
		Author:                 strings.TrimSpace(draft.Author),
		AllowedTools:           draft.AllowedTools,
		DisableModelInvocation: draft.DisableModelInvocation,
		UserInvocable:          draft.UserInvocable,
	}
	// name 缺省时用目录名补上：标准要求该字段存在
	if fm.Name == "" {
		fm.Name = id
	}
	content, err := MarshalSkillDocument(fm, draft.Body)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(skillDir, skillFileName), []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", skillFileName, err)
	}
	s.scan()
	return nil
}

// canWrite 手工编辑是否允许：安装来源的技能由安装器接管，避免本地改动被下次更新覆盖
func (s *SkillStore) canWrite(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.skills {
		if m.ID != id {
			continue
		}
		if m.Builtin {
			return false
		}
		if m.Install != nil && m.Install.SourceType == "git" {
			return false
		}
		return true
	}
	return true // 新技能
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

// EnsureDefaultSkill 首次运行写入示例技能（含 scripts/，便于验证 L3）
func (s *SkillStore) EnsureDefaultSkill() {
	if len(s.GetAll()) > 0 {
		return
	}
	err := s.SaveSkill("hello-skill", SkillDraft{
		Name:        "hello-skill",
		Description: "演示用技能：当用户请求打招呼、问好，或想了解技能机制如何运作时使用。",
		Version:     "1.0.0",
		License:     "MIT",
		UserInvocable: true,
		Body: "# 示例技能\n\n" +
			"1. 运行 `scripts/greet.sh` 生成问候语（路径相对于技能目录，可用 exec_shell 执行）。\n" +
			"2. 把脚本输出原样回复给用户。\n\n" +
			"本技能用于演示三级渐进式披露：清单里只有名称与描述，正文按需加载，脚本执行时才读取。\n",
	})
	if err != nil {
		return
	}
	scriptDir := filepath.Join(s.dir, "hello-skill", skillScriptsDir)
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(scriptDir, "greet.sh"),
		[]byte("#!/bin/sh\necho \"你好，我是 local-agent 的技能。\"\n"), 0o755)
	s.scan()
}

// validSkillID 校验技能 ID：字母/数字/下划线/短横线，≤64（同时也是目录名）
func validSkillID(id string) bool {
	if id == "" || len(id) > skillNameMaxLen {
		return false
	}
	for _, r := range id {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	// 排除 . / .. 这类会逃出技能根目录的名字
	return id != "." && id != ".."
}

// ===== L1：技能清单 =====

// BuildSkillIndex 生成 L1 技能清单（注入 system prompt）。
// 只列「可自动触发」的技能：停用、有阻断错误、以及声明了 disable-model-invocation 的都不进清单。
func BuildSkillIndex(skills []*SkillMeta) string {
	var sb strings.Builder
	for _, m := range skills {
		if !m.CanAutoTrigger() {
			continue
		}
		desc := m.Description
		if r := []rune(desc); len(r) > skillListMaxDesc {
			desc = string(r[:skillListMaxDesc]) + "…"
		}
		sb.WriteString(fmt.Sprintf("- %s: %s\n", m.ID, desc))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ListSkillResources 列出技能内可读资源（L3 提示用；未启用的技能也可读，便于界面预览）
func (s *SkillStore) ListSkillResources(id string) ([]SkillResource, error) {
	m, ok := s.Get(id)
	if !ok {
		return nil, fmt.Errorf("技能不存在: %s", id)
	}
	return listSkillResources(m.Dir), nil
}

// ===== 显式调用：/技能名 =====

// SkillCommandResult 显式技能调用的解析结果
type SkillCommandResult struct {
	Skill  *SkillMeta `json:"skill,omitempty"`
	Rest   string     `json:"rest"`             // 去掉命令后的剩余文本
	Ok     bool       `json:"ok"`               // 是否成功命中一个可调用的技能
	Reason string     `json:"reason,omitempty"` // 命中技能但不可调用时的原因
}

// ParseSkillCommand 解析消息开头的「/技能名 余下内容」。
// 只识别位于消息最前面的命令；ok=false 时 rest 原样返回（调用方按普通消息处理）。
func ParseSkillCommand(input string) (id, rest string, ok bool) {
	trimmed := strings.TrimLeft(input, " \t")
	if !strings.HasPrefix(trimmed, "/") {
		return "", input, false
	}
	body := trimmed[1:]
	i := 0
	for i < len(body) && isSkillIDChar(body[i]) {
		i++
	}
	if i == 0 || i > skillNameMaxLen {
		return "", input, false
	}
	return body[:i], strings.TrimSpace(body[i:]), true
}

// isSkillIDChar 与 validSkillID 的字符集保持一致
func isSkillIDChar(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ResolveSkillCommand 解析并校验显式调用。
// 只有命中已存在技能时才返回 Ok/Reason；未命中（例如消息只是以 / 开头的路径）
// 一律 Ok=false 且 Reason 为空，由调用方当普通消息处理，避免路径被误判成技能名。
func (s *SkillStore) ResolveSkillCommand(input string) SkillCommandResult {
	id, rest, isCmd := ParseSkillCommand(input)
	if !isCmd {
		return SkillCommandResult{Rest: input}
	}
	m, ok := s.Get(id)
	if !ok {
		return SkillCommandResult{Rest: input}
	}
	if len(m.Errors) > 0 {
		return SkillCommandResult{Rest: rest, Reason: fmt.Sprintf("技能 %s 不可用：%s", id, strings.Join(m.Errors, "；"))}
	}
	if !m.Enabled {
		return SkillCommandResult{Rest: rest, Reason: fmt.Sprintf("技能 %s 已被停用", id)}
	}
	if !m.UserInvocable {
		return SkillCommandResult{Rest: rest, Reason: fmt.Sprintf("技能 %s 声明了 user-invocable: false，不允许用户直接调用", id)}
	}
	return SkillCommandResult{Skill: m, Rest: rest, Ok: true}
}

// BuildExplicitSkillBlock 生成显式调用的注入段。
// 用户用 /技能名 调用时正文直接进本轮 system prompt——这是标准里
// disable-model-invocation 的配套语义：模型不参与「是否使用」的决策。
func BuildExplicitSkillBlock(m *SkillMeta, body string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n\n## 用户显式调用的技能：%s\n"+
		"用户在本轮通过 /%s 明确要求使用该技能，请直接按其说明执行，不要再去调用 read_skill。\n"+
		"技能目录：%s\n", m.ID, m.ID, m.Dir))
	if len(m.Resources) > 0 {
		sb.WriteString("可用资源（用 read_skill_file 读取；脚本用 exec_shell 运行）：\n")
		for _, r := range m.Resources {
			sb.WriteString(fmt.Sprintf("- %s\n", r.Path))
		}
	}
	sb.WriteString("\n### 技能说明\n")
	sb.WriteString(body)
	return sb.String()
}

// ===== 内置工具：read_skill（L2）=====

// ReadSkillTool 让模型按需读取 SKILL.md 正文（L2）；
// 同时受全局开关、本会话白名单与 disable-model-invocation 约束。
type ReadSkillTool struct {
	*BaseTool
	store   *SkillStore
	allowed func(*SkillMeta) bool
}

// NewReadSkillTool 构造 read_skill 工具。
// skillWhitelist 为空表示全部技能可读；非空时仅白名单内技能可读。
func NewReadSkillTool(store *SkillStore, skillWhitelist []string) *ReadSkillTool {
	wl := skillWhitelistSet(skillWhitelist)
	t := &ReadSkillTool{
		BaseTool: &BaseTool{
			Name: "read_skill",
			Description: "读取指定技能的完整说明（SKILL.md 正文）与它自带的资源清单。" +
				"当用户请求与可用技能清单里某个技能的描述匹配时，先调用本工具获取正文，" +
				"再按正文步骤执行；正文里引用的 references/ 或 assets/ 文件用 read_skill_file 读取，" +
				"scripts/ 下的脚本用 exec_shell 运行。",
			Parameters: map[string]*ToolArgDef{
				"id": {Type: "string", Description: "技能 ID（从可用技能清单获取）"},
			},
		},
		store: store,
	}
	t.allowed = func(m *SkillMeta) bool {
		if !m.Usable() || m.DisableModelInvocation {
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

// Execute 返回技能正文 + 资源清单
func (t *ReadSkillTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id 参数是必需的")
	}
	m, ok := t.store.Get(id)
	if !ok {
		return "", fmt.Errorf("技能不存在: %s", id)
	}
	if !t.allowed(m) {
		return "", fmt.Errorf("技能在本会话不可用: %s", id)
	}
	body, err := t.store.LoadBody(id)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 技能：%s\n技能目录：%s\n", m.Name, m.Dir))
	if len(m.AllowedTools) > 0 {
		// 标准字段，当前仅作提示：本项目尚未把它接入权限网关做硬约束
		sb.WriteString(fmt.Sprintf("该技能声明使用的工具：%s\n", strings.Join(m.AllowedTools, ", ")))
	}
	sb.WriteString("\n")
	sb.WriteString(body)
	if len(m.Resources) > 0 {
		sb.WriteString("\n\n## 该技能自带的资源\n")
		for _, r := range m.Resources {
			sb.WriteString(fmt.Sprintf("- %s（%s，%d 字节）\n", r.Path, r.Kind, r.Size))
		}
		sb.WriteString("读取方式：read_skill_file(id=\"" + id + "\", path=\"<上面的路径>\")；" +
			"scripts/ 下的脚本改用 exec_shell 执行。")
	}
	return sb.String(), nil
}
