package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ===== 权限配置的分层存储 =====
//
// 层级（高优先级在前）：
//
//	用户全局   ~/.local-agent/settings.json
//	项目级     <projectDir>/.local-agent/settings.json        （可提交版本控制）
//	项目本地   <projectDir>/.local-agent/settings.local.json  （不提交，「总是允许」写这里）
//
// 合并策略（对齐 Claude Code）：
//   - allow / ask / deny 三个**列表跨层合并**（取并集）—— 高层不能删掉低层的规则，
//     因此 deny 一旦写入就无法被上层撤销
//   - defaultMode 这类**标量由高优先级覆盖**

// 规则来源标识：用于审计与界面展示「这条规则来自哪一层」
const (
	SourceBuiltin = "builtin" // 代码内置，界面只读、不可撤销
	SourceUser    = "user"    // ~/.local-agent/settings.json
	SourceProject = "project" // <projectDir>/.local-agent/settings.json
	SourceLocal   = "local"   // <projectDir>/.local-agent/settings.local.json
)

// 规则桶
const (
	BucketAllow = "allow"
	BucketAsk   = "ask"
	BucketDeny  = "deny"
)

// PermissionSettings settings.json 的 permissions 段
type PermissionSettings struct {
	DefaultMode string   `json:"defaultMode,omitempty"`
	Allow       []string `json:"allow,omitempty"`
	Ask         []string `json:"ask,omitempty"`
	Deny        []string `json:"deny,omitempty"`
}

// Settings 一份 settings.json
type Settings struct {
	Permissions PermissionSettings `json:"permissions"`
}

// ResolvedRules 三层合并后的规则集
type ResolvedRules struct {
	Mode    PermissionMode `json:"mode"`
	Allow   []Rule         `json:"allow"`
	Ask     []Rule         `json:"ask"`
	Deny    []Rule         `json:"deny"`
	Sources []string       `json:"sources"` // 实际加载到的文件（供界面显示）
	Errors  []string       `json:"errors"`  // 规则解析错误，不阻断启动
}

// SettingsStore 权限配置的分层读写
type SettingsStore struct {
	mu         sync.RWMutex
	globalPath string
}

// NewSettingsStore 创建配置存储；globalDir 通常是 ~/.local-agent
func NewSettingsStore(globalDir string) *SettingsStore {
	return &SettingsStore{globalPath: filepath.Join(globalDir, "settings.json")}
}

// GlobalPath 全局配置文件路径
func (s *SettingsStore) GlobalPath() string { return s.globalPath }

// projectPaths 项目级与项目本地配置路径；projectDir 为空时返回空串
func (s *SettingsStore) projectPaths(projectDir string) (shared, local string) {
	if projectDir == "" {
		return "", ""
	}
	dir := filepath.Join(projectDir, ".local-agent")
	return filepath.Join(dir, "settings.json"), filepath.Join(dir, "settings.local.json")
}

// readSettings 读取一份配置文件；不存在时返回空配置且 ok=false
func readSettings(path string) (Settings, bool, error) {
	var st Settings
	if path == "" {
		return st, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, false, nil
		}
		return st, false, err
	}
	if len(data) == 0 {
		return st, false, nil
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, false, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return st, true, nil
}

// writeSettings 写入一份配置文件（自动建目录）
func writeSettings(path string, st Settings) error {
	if path == "" {
		return fmt.Errorf("配置路径为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Load 加载三层配置并合并。
// 读取失败不阻断（记录到 Errors），避免一个坏掉的配置文件让整个应用起不来。
func (s *SettingsStore) Load(projectDir string) ResolvedRules {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sharedPath, localPath := s.projectPaths(projectDir)
	type layer struct {
		path   string
		source string
	}
	layers := []layer{
		{s.globalPath, SourceUser},
		{sharedPath, SourceProject},
		{localPath, SourceLocal},
	}

	var sources, errs []string
	mode := ModeDefault
	allow, ask, deny := []Rule{}, []Rule{}, []Rule{}

	for _, l := range layers {
		st, ok, err := readSettings(l.path)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if !ok {
			continue
		}
		sources = append(sources, l.path)
		// 标量：后加载的（更高优先级）覆盖先前的
		if st.Permissions.DefaultMode != "" && ValidPermissionMode(PermissionMode(st.Permissions.DefaultMode)) {
			mode = PermissionMode(st.Permissions.DefaultMode)
		}
		// 列表：跨层合并
		a, e := ParseRules(st.Permissions.Allow, l.source)
		allow = append(allow, a...)
		errs = append(errs, e...)
		a, e = ParseRules(st.Permissions.Ask, l.source)
		ask = append(ask, a...)
		errs = append(errs, e...)
		a, e = ParseRules(st.Permissions.Deny, l.source)
		deny = append(deny, a...)
		errs = append(errs, e...)
	}

	return ResolvedRules{
		Mode:    mode,
		Allow:   mergeRules(allow),
		Ask:     mergeRules(ask),
		Deny:    mergeRules(deny),
		Sources: sources,
		Errors:  errs,
	}
}

// pathForSource 按来源解析目标文件；项目层在无项目目录时回退到用户全局层
func (s *SettingsStore) pathForSource(projectDir, source string) string {
	sharedPath, localPath := s.projectPaths(projectDir)
	switch source {
	case SourceLocal:
		if localPath != "" {
			return localPath
		}
		return s.globalPath
	case SourceProject:
		if sharedPath != "" {
			return sharedPath
		}
		return s.globalPath
	default:
		return s.globalPath
	}
}

// AddRule 在指定层的指定桶里写入一条规则（会先校验语法）
func (s *SettingsStore) AddRule(projectDir, bucket, raw, source string) error {
	if source == SourceBuiltin {
		return fmt.Errorf("内置规则不可修改")
	}
	if _, err := ParseRule(raw, source); err != nil {
		return err
	}
	path := s.pathForSource(projectDir, source)

	s.mu.Lock()
	defer s.mu.Unlock()

	st, _, err := readSettings(path)
	if err != nil {
		return err
	}
	switch bucket {
	case BucketAllow:
		if containsString(st.Permissions.Allow, raw) {
			return nil
		}
		st.Permissions.Allow = append(st.Permissions.Allow, raw)
	case BucketAsk:
		if containsString(st.Permissions.Ask, raw) {
			return nil
		}
		st.Permissions.Ask = append(st.Permissions.Ask, raw)
	case BucketDeny:
		if containsString(st.Permissions.Deny, raw) {
			return nil
		}
		st.Permissions.Deny = append(st.Permissions.Deny, raw)
	default:
		return fmt.Errorf("未知的规则桶: %s", bucket)
	}
	return writeSettings(path, st)
}

// RemoveRule 从指定层的指定桶里删除一条规则。
//
// 规则不在该层时返回**明确错误**，而不是静默成功。静默成功会让界面显示
// 「已删除」而规则仍在文件里 —— 用户看到的就是「点了删除没用」，且毫无线索。
func (s *SettingsStore) RemoveRule(projectDir, bucket, raw, source string) error {
	removed, err := s.RemoveRuleIfPresent(projectDir, bucket, raw, source)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("该规则不在目标文件中，未做改动（已查找 %s）", s.pathForSource(projectDir, source))
	}
	return nil
}

// RemoveRuleIfPresent 删除规则并返回是否真的删掉了
func (s *SettingsStore) RemoveRuleIfPresent(projectDir, bucket, raw, source string) (bool, error) {
	if source == SourceBuiltin {
		return false, fmt.Errorf("内置规则不可删除")
	}
	path := s.pathForSource(projectDir, source)

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeFromFile(path, bucket, raw)
}

// RemoveRuleAnyLayer 依次在项目本地 / 项目级 / 用户全局三层里查找并删除该规则，
// 返回实际删除的层标识。用于「不确定规则当初落在哪一层」的场景 ——
// 例如撤销一条「永久允许」授权时，写入时可能因没有项目目录而回退到了全局层。
func (s *SettingsStore) RemoveRuleAnyLayer(projectDir, bucket, raw string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, source := range []string{SourceLocal, SourceProject, SourceUser} {
		path := s.pathForSource(projectDir, source)
		if removed, err := s.removeFromFile(path, bucket, raw); err != nil {
			continue // 某一层读不出来不影响继续找下一层
		} else if removed {
			return source, true
		}
	}
	return "", false
}

// removeFromFile 在单个文件里执行删除（调用方需持有锁）
func (s *SettingsStore) removeFromFile(path, bucket, raw string) (bool, error) {
	st, _, err := readSettings(path)
	if err != nil {
		return false, err
	}
	var list []string
	switch bucket {
	case BucketAllow:
		list = st.Permissions.Allow
	case BucketAsk:
		list = st.Permissions.Ask
	case BucketDeny:
		list = st.Permissions.Deny
	default:
		return false, fmt.Errorf("未知的规则桶: %s", bucket)
	}
	if !containsString(list, raw) {
		return false, nil
	}
	out := removeString(list, raw)
	switch bucket {
	case BucketAllow:
		st.Permissions.Allow = out
	case BucketAsk:
		st.Permissions.Ask = out
	case BucketDeny:
		st.Permissions.Deny = out
	}
	if err := writeSettings(path, st); err != nil {
		return false, err
	}
	return true, nil
}

// SetDefaultMode 设置默认权限模式
func (s *SettingsStore) SetDefaultMode(projectDir, mode, source string) error {
	if !ValidPermissionMode(PermissionMode(mode)) {
		return fmt.Errorf("非法的权限模式: %s", mode)
	}
	if source == SourceBuiltin {
		return fmt.Errorf("内置默认模式不可修改")
	}
	path := s.pathForSource(projectDir, source)

	s.mu.Lock()
	defer s.mu.Unlock()

	st, _, err := readSettings(path)
	if err != nil {
		return err
	}
	st.Permissions.DefaultMode = mode
	return writeSettings(path, st)
}

// RawFor 返回各层的原始配置（供设置页按层展示）
func (s *SettingsStore) RawFor(projectDir string) map[string]Settings {
	sharedPath, localPath := s.projectPaths(projectDir)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]Settings{}
	for source, path := range map[string]string{
		SourceUser:    s.globalPath,
		SourceProject: sharedPath,
		SourceLocal:   localPath,
	} {
		st, ok, err := readSettings(path)
		if err != nil || !ok {
			continue
		}
		out[source] = st
	}
	return out
}

// containsString / removeString 小工具
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func removeString(list []string, s string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// BuiltinRules 返回内置规则集（供界面只读展示）。
//
// 注意：内置 deny 的**实际判定**走的是 matchDangerousDeny（精确 / 前缀 / 包含
// 三种匹配），比规则语法更精确，避免「rm -rf /」的前缀写法误伤 rm -rf /tmp/x
// 这类正常操作。这里返回的是给用户看的等价描述。
func BuiltinRules() RuleSet {
	deny := make([]Rule, 0, len(builtinDenyExact)+len(builtinDenyPrefix)+len(builtinDenyContains))
	add := func(text string) {
		if r, err := ParseRule(text, SourceBuiltin); err == nil {
			deny = append(deny, r)
		}
	}
	for _, p := range builtinDenyExact {
		add("exec_shell(" + p.Pattern + ")")
	}
	for _, p := range builtinDenyPrefix {
		add("exec_shell(" + p + ":*)")
	}
	for _, p := range builtinDenyContains {
		add("exec_shell(" + p.Pattern + ")")
	}

	ask := make([]Rule, 0, len(builtinSensitivePaths))
	for _, p := range builtinSensitivePaths {
		if r, err := ParseRule("exec_shell("+p.Pattern+")", SourceBuiltin); err == nil {
			ask = append(ask, r)
		}
	}

	return RuleSet{
		Deny: mergeRules(deny),
		Ask:  mergeRules(ask),
	}
}
