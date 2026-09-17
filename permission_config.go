package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ===== 权限规则的三层配置 =====
//
// 加载顺序（后者覆盖前者的标量、追加前者的列表）：
//  1. 用户全局   <userDir>/permissions.json          （userDir = ~/.local-agent）
//  2. 项目级     <项目>/.local-agent/permissions.json        （随仓库提交，团队共享）
//  3. 项目本地   <项目>/.local-agent/permissions.local.json  （建议 gitignore，个人放宽）
//
// 失败方向：某一层读取或解析失败时，只跳过该层，并把问题作为 warning 抛给界面。
// 跳过一层的后果是"规则变少" —— 对 deny/allow 而言意味着询问变多（安全方向），
// 对 mode 而言意味着更严格（回退到下层或 manual），因此不存在静默放宽的路径。

// 规则桶名（同时用作配置文件字段名与前端参数）
const (
	RuleBucketDeny  = "deny"
	RuleBucketAsk   = "ask"
	RuleBucketAllow = "allow"
)

// 配置层名
const (
	PermissionScopeUser    = "user"    // 用户全局
	PermissionScopeProject = "project" // 项目级
	PermissionScopeLocal   = "local"   // 项目本地
)

// PermissionConfig 一份权限配置文件的内容
type PermissionConfig struct {
	Mode  string   `json:"mode,omitempty"`  // 该层建议的会话模式（标量，后层覆盖前层）
	Deny  []string `json:"deny,omitempty"`  // 拒绝规则
	Ask   []string `json:"ask,omitempty"`   // 询问规则
	Allow []string `json:"allow,omitempty"` // 放行规则
}

// RuleSource 一层规则的加载结果（供界面展示"规则来自哪个文件"）
type RuleSource struct {
	Scope string `json:"scope"` // user / project / local
	Path  string `json:"path"`
	Exist bool   `json:"exist"`
	Mode  string `json:"mode,omitempty"`
	Deny  int    `json:"deny"`
	Ask   int    `json:"ask"`
	Allow int    `json:"allow"`
}

// permissionConfigPath 返回某层配置文件的绝对路径
func permissionConfigPath(userDir, projectDir, scope string) (string, error) {
	switch scope {
	case PermissionScopeProject:
		if strings.TrimSpace(projectDir) == "" {
			return "", fmt.Errorf("会话未设置项目目录，无法定位项目级权限配置")
		}
		return filepath.Join(projectDir, ".local-agent", "permissions.json"), nil
	case PermissionScopeLocal:
		if strings.TrimSpace(projectDir) == "" {
			return "", fmt.Errorf("会话未设置项目目录，无法定位项目本地权限配置")
		}
		return filepath.Join(projectDir, ".local-agent", "permissions.local.json"), nil
	default:
		if strings.TrimSpace(userDir) == "" {
			return "", fmt.Errorf("用户数据目录未知，无法定位全局权限配置")
		}
		return filepath.Join(userDir, "permissions.json"), nil
	}
}

// loadPermissionConfig 读取单层配置。文件不存在返回 (nil, nil)；解析失败返回错误。
func loadPermissionConfig(path string) (*PermissionConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取权限配置失败: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, nil
	}
	var cfg PermissionConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析权限配置失败: %w", err)
	}
	return &cfg, nil
}

// LoadRuleSet 按 user → project → project-local 合并三层规则。
// 返回合并后的规则集、每层的加载情况、以及需要提示给用户的问题列表。
// 本函数不返回致命错误：任何一层的失败都降级为 warning，保证聊天主流程可用。
func LoadRuleSet(userDir, projectDir string) (*RuleSet, []RuleSource, []string) {
	set := &RuleSet{}
	sources := make([]RuleSource, 0, 3)
	warnings := []string{}

	scopes := []string{PermissionScopeUser, PermissionScopeProject, PermissionScopeLocal}
	mode := ""

	for _, scope := range scopes {
		path, err := permissionConfigPath(userDir, projectDir, scope)
		if err != nil {
			// 项目目录未设置的场景（如尚未选择工作区）不算问题，静默跳过
			if scope == PermissionScopeUser {
				warnings = append(warnings, err.Error())
			}
			continue
		}

		src := RuleSource{Scope: scope, Path: path}
		cfg, err := loadPermissionConfig(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s 层权限配置被跳过：%v", scopeLabel(scope), err))
			sources = append(sources, src)
			continue
		}
		if cfg == nil {
			sources = append(sources, src)
			continue
		}

		src.Exist = true
		src.Mode = cfg.Mode
		if strings.TrimSpace(cfg.Mode) != "" {
			mode = cfg.Mode // 标量覆盖：后层覆盖前层
		}

		src.Deny, warnings = appendRules(&set.Deny, cfg.Deny, path, warnings)
		src.Ask, warnings = appendRules(&set.Ask, cfg.Ask, path, warnings)
		src.Allow, warnings = appendRules(&set.Allow, cfg.Allow, path, warnings)
		sources = append(sources, src)
	}

	if mode != "" {
		// 模式非法时忽略（回退到会话自身字段），并提示用户
		if ValidMode(Mode(mode)) {
			set.Mode = mode
		} else {
			warnings = append(warnings, fmt.Sprintf("权限配置里的模式 %q 无法识别，已忽略", mode))
		}
	}

	if projectDir != "" {
		warnings = append(warnings, localConfigGitignoreWarning(projectDir)...)
	}
	return set, sources, warnings
}

// appendRules 解析并追加一组规则；非法行跳过并记入 warning（跳过只会让判定更严，不会放宽）
func appendRules(dst *[]Rule, lines []string, source string, warnings []string) (int, []string) {
	n := 0
	for _, line := range lines {
		r, ok := ParseRule(line, source)
		if !ok {
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				warnings = append(warnings, fmt.Sprintf("规则 %q 语法无效，已跳过（应为 Tool 或 Tool(specifier)）", strings.TrimSpace(line)))
			}
			continue
		}
		*dst = append(*dst, r)
		n++
	}
	return n, warnings
}

// localConfigGitignoreWarning 项目本地层的文件若没被忽略，提示用户（不代为修改仓库）
func localConfigGitignoreWarning(projectDir string) []string {
	localPath := filepath.Join(projectDir, ".local-agent", "permissions.local.json")
	if _, err := os.Stat(localPath); err != nil {
		return nil // 不存在则无需提醒
	}
	gitignore := filepath.Join(projectDir, ".gitignore")
	data, err := os.ReadFile(gitignore)
	if err == nil {
		text := string(data)
		if strings.Contains(text, "permissions.local.json") || strings.Contains(text, ".local-agent") {
			return nil
		}
	}
	return []string{"项目本地权限配置 permissions.local.json 未被 .gitignore 忽略，注意不要提交个人放宽设置"}
}

// scopeLabel 层的中文名
func scopeLabel(scope string) string {
	switch scope {
	case PermissionScopeProject:
		return "项目级"
	case PermissionScopeLocal:
		return "项目本地"
	default:
		return "用户全局"
	}
}

// SavePermissionConfig 写入一层配置（目录不存在时创建）
func SavePermissionConfig(path string, cfg *PermissionConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化权限配置失败: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入权限配置失败: %w", err)
	}
	return nil
}

// AddRuleToScope 往指定层的指定桶追加一条规则（已存在则不重复追加）
func AddRuleToScope(userDir, projectDir, scope, bucket, rule string) error {
	if bucket != RuleBucketDeny && bucket != RuleBucketAsk && bucket != RuleBucketAllow {
		return fmt.Errorf("未知的规则类型: %s", bucket)
	}
	if _, ok := ParseRule(rule, ""); !ok {
		return fmt.Errorf("规则语法无效: %s（应为 Tool 或 Tool(specifier)）", rule)
	}
	path, err := permissionConfigPath(userDir, projectDir, scope)
	if err != nil {
		return err
	}
	cfg, err := loadPermissionConfig(path)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &PermissionConfig{}
	}

	dst := bucketLines(cfg, bucket)
	for _, line := range *dst {
		if strings.TrimSpace(line) == strings.TrimSpace(rule) {
			return nil // 幂等
		}
	}
	*dst = append(*dst, strings.TrimSpace(rule))
	setBucketLines(cfg, bucket, *dst)
	return SavePermissionConfig(path, cfg)
}

// RemoveRuleFromScope 从指定层移除一条规则
func RemoveRuleFromScope(userDir, projectDir, scope, bucket, rule string) error {
	if bucket != RuleBucketDeny && bucket != RuleBucketAsk && bucket != RuleBucketAllow {
		return fmt.Errorf("未知的规则类型: %s", bucket)
	}
	path, err := permissionConfigPath(userDir, projectDir, scope)
	if err != nil {
		return err
	}
	cfg, err := loadPermissionConfig(path)
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("该层还没有权限配置文件")
	}

	lines := bucketLines(cfg, bucket)
	kept := make([]string, 0, len(*lines))
	removed := false
	for _, line := range *lines {
		if strings.TrimSpace(line) == strings.TrimSpace(rule) {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return fmt.Errorf("该层不存在规则: %s", rule)
	}
	setBucketLines(cfg, bucket, kept)
	return SavePermissionConfig(path, cfg)
}

// bucketLines 取某个桶的切片指针
func bucketLines(cfg *PermissionConfig, bucket string) *[]string {
	switch bucket {
	case RuleBucketDeny:
		return &cfg.Deny
	case RuleBucketAsk:
		return &cfg.Ask
	default:
		return &cfg.Allow
	}
}

// setBucketLines 写回某个桶
func setBucketLines(cfg *PermissionConfig, bucket string, lines []string) {
	switch bucket {
	case RuleBucketDeny:
		cfg.Deny = lines
	case RuleBucketAsk:
		cfg.Ask = lines
	default:
		cfg.Allow = lines
	}
}
