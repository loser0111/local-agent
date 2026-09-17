package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// ===== SKILL.md 的标准 frontmatter 解析与校验 =====
//
// 对齐 Agent Skills 开放标准（Claude Code / Codex 通用形状）：
//
//	---
//	name: pdf-report
//	description: 从 CSV 生成 PDF 报表。当用户需要把表格数据导出为 PDF 时使用。
//	version: 1.0.0
//	license: MIT
//	allowed-tools: bash, read_file
//	disable-model-invocation: false
//	user-invocable: true
//	metadata: {owner: team-reports}
//	---
//
// name / description 是唯一影响路由的两个字段：所有已启用技能的这两项会被注入
// 系统提示（L1），模型据此决定是否调用 read_skill 读取正文（L2）。
//
// 与标准的一处有意偏离：name 与目录名不一致时只告警、不阻断。
// 标准要求两者必须相同，但本项目的 ID 一律取目录名，name 充作展示名，
// 这样既有的中文 name 技能（如 name: 示例技能）不会被判死。

const (
	skillNameMaxLen = 64   // frontmatter.name 上限（标准）
	skillDescMaxLen = 1024 // frontmatter.description 上限（标准）
)

// skillKnownFields 标准字段集合；不在此列的顶层字段会被列为告警（不阻断）
var skillKnownFields = map[string]bool{
	"name":                     true,
	"description":              true,
	"version":                  true,
	"license":                  true,
	"author":                   true,
	"allowed-tools":            true,
	"disable-model-invocation": true,
	"user-invocable":           true,
	"metadata":                 true,
}

// yamlText 容忍把标量写成数字/布尔/空值的字段（例如 `version: 1.0`）
type yamlText string

// UnmarshalYAML 统一转成字符串
func (t *yamlText) UnmarshalYAML(value *yaml.Node) error {
	var raw interface{}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case nil:
		*t = ""
	case string:
		*t = yamlText(v)
	default:
		*t = yamlText(fmt.Sprintf("%v", v))
	}
	return nil
}

// skillStringList 兼容 allowed-tools 的两种常见写法：
// 标量（"bash, read_file" / "bash read_file"）与序列（["bash", "read_file"]）
type skillStringList []string

// UnmarshalYAML 标量与序列都接受
func (l *skillStringList) UnmarshalYAML(value *yaml.Node) error {
	var raw interface{}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case nil:
		*l = nil
	case string:
		*l = splitSkillList(v)
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		*l = out
	default:
		return fmt.Errorf("allowed-tools 需要字符串或字符串数组")
	}
	return nil
}

// splitSkillList 按逗号/分号/空白切分标量形式的列表
func splitSkillList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// SkillFrontmatter SKILL.md 的 frontmatter（标准字段 + 未识别字段）
type SkillFrontmatter struct {
	Name                   string                 `json:"name"`
	Description            string                 `json:"description"`
	Version                string                 `json:"version,omitempty"`
	License                string                 `json:"license,omitempty"`
	Author                 string                 `json:"author,omitempty"`
	AllowedTools           []string               `json:"allowedTools,omitempty"`
	DisableModelInvocation bool                   `json:"disableModelInvocation"`
	UserInvocable          bool                   `json:"userInvocable"`
	Metadata               map[string]interface{} `json:"metadata,omitempty"`
	Extra                  map[string]interface{} `json:"extra,omitempty"`
}

// splitFrontmatter 把 SKILL.md 全文拆成 frontmatter 原文与正文。
// has=false 表示没有 frontmatter（此时 body 为全文）。
func splitFrontmatter(text string) (head, body string, has bool, err error) {
	text = strings.TrimPrefix(text, "\ufeff") // 编辑器「另存为 UTF-8 BOM」很常见
	text = strings.ReplaceAll(text, "\r\n", "\n")

	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", text, false, nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}
		head = strings.Join(lines[1:i], "\n")
		body = strings.TrimLeft(strings.Join(lines[i+1:], "\n"), "\n")
		return head, body, true, nil
	}
	return "", "", false, fmt.Errorf("frontmatter 未闭合（缺少结束的 --- 行）")
}

// ParseSkillFrontmatter 解析 frontmatter 原文；head 为空时返回零值（UserInvocable 默认 true）
func ParseSkillFrontmatter(head string) (*SkillFrontmatter, error) {
	fm := &SkillFrontmatter{UserInvocable: true}
	if strings.TrimSpace(head) == "" {
		return fm, nil
	}

	var raw struct {
		Name                   string                 `yaml:"name"`
		Description            string                 `yaml:"description"`
		Version                yamlText               `yaml:"version"`
		License                yamlText               `yaml:"license"`
		Author                 yamlText               `yaml:"author"`
		AllowedTools           skillStringList        `yaml:"allowed-tools"`
		DisableModelInvocation bool                   `yaml:"disable-model-invocation"`
		UserInvocable          *bool                  `yaml:"user-invocable"`
		Metadata               map[string]interface{} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(head), &raw); err != nil {
		return nil, fmt.Errorf("frontmatter 不是合法 YAML: %w", err)
	}

	fm.Name = strings.TrimSpace(raw.Name)
	fm.Description = strings.TrimSpace(raw.Description)
	fm.Version = strings.TrimSpace(string(raw.Version))
	fm.License = strings.TrimSpace(string(raw.License))
	fm.Author = strings.TrimSpace(string(raw.Author))
	fm.AllowedTools = []string(raw.AllowedTools)
	fm.DisableModelInvocation = raw.DisableModelInvocation
	if raw.UserInvocable != nil {
		fm.UserInvocable = *raw.UserInvocable
	}
	fm.Metadata = raw.Metadata

	// 未识别字段：收集起来仅作告警，保证旧技能/新标准的前向兼容
	var all map[string]interface{}
	if err := yaml.Unmarshal([]byte(head), &all); err == nil {
		for k, v := range all {
			if skillKnownFields[k] {
				continue
			}
			if fm.Extra == nil {
				fm.Extra = map[string]interface{}{}
			}
			fm.Extra[k] = v
		}
	}
	return fm, nil
}

// ParseSkillDocument 解析 SKILL.md 全文，返回 frontmatter 与正文
func ParseSkillDocument(text string) (*SkillFrontmatter, string, error) {
	head, body, has, err := splitFrontmatter(text)
	if err != nil {
		return nil, "", err
	}
	if !has {
		// 没有 frontmatter：不报错，交给 ValidateSkillDocument 判定「缺 description」
		return &SkillFrontmatter{UserInvocable: true}, body, nil
	}
	fm, err := ParseSkillFrontmatter(head)
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxSkillBodyBytes {
		body = body[:maxSkillBodyBytes]
	}
	return fm, body, nil
}

// ValidateSkillDocument 依据标准校验（目录名为 id）。返回阻断性错误与提示性告警。
func ValidateSkillDocument(id string, fm *SkillFrontmatter, body string) (errs, warns []string) {
	if fm == nil {
		return []string{"frontmatter 解析失败"}, nil
	}

	descLen := utf8.RuneCountInString(fm.Description)
	if descLen == 0 {
		errs = append(errs, "frontmatter 缺少 description：模型靠它判断何时使用本技能")
	} else if descLen > skillDescMaxLen {
		errs = append(errs, fmt.Sprintf("description 超长：%d > %d 字符", descLen, skillDescMaxLen))
	}

	switch {
	case fm.Name == "":
		warns = append(warns, "缺少 frontmatter.name：已用目录名代替")
	case utf8.RuneCountInString(fm.Name) > skillNameMaxLen:
		errs = append(errs, fmt.Sprintf("name 超长：%d > %d 字符", utf8.RuneCountInString(fm.Name), skillNameMaxLen))
	default:
		if !isKebabCase(fm.Name) {
			warns = append(warns, "name 建议只用小写字母、数字与短横线（如 pdf-report）")
		}
		if fm.Name != id {
			warns = append(warns, fmt.Sprintf("frontmatter.name=%q 与目录名 %q 不一致：以目录名为 ID", fm.Name, id))
		}
	}

	if fm.DisableModelInvocation && !fm.UserInvocable {
		warns = append(warns, "disable-model-invocation 与 user-invocable:false 同时生效：该技能将无法被任何方式调用")
	}
	if len(fm.Extra) > 0 {
		warns = append(warns, "存在未识别的 frontmatter 字段："+strings.Join(sortedMapKeys(fm.Extra), ", "))
	}
	if strings.TrimSpace(body) == "" {
		warns = append(warns, "正文为空：技能被触发后没有可执行的说明")
	}
	if len(body) >= maxSkillBodyBytes {
		warns = append(warns, fmt.Sprintf("正文超过 %dKB：读取时会被截断", maxSkillBodyBytes/1024))
	}
	return errs, warns
}

// MarshalSkillDocument 生成标准格式的 SKILL.md 文本
func MarshalSkillDocument(fm *SkillFrontmatter, body string) (string, error) {
	if fm == nil {
		fm = &SkillFrontmatter{UserInvocable: true}
	}
	out := struct {
		Name                   string                 `yaml:"name"`
		Description            string                 `yaml:"description"`
		Version                string                 `yaml:"version,omitempty"`
		License                string                 `yaml:"license,omitempty"`
		Author                 string                 `yaml:"author,omitempty"`
		AllowedTools           []string               `yaml:"allowed-tools,omitempty"`
		DisableModelInvocation bool                   `yaml:"disable-model-invocation,omitempty"`
		UserInvocable          *bool                  `yaml:"user-invocable,omitempty"`
		Metadata               map[string]interface{} `yaml:"metadata,omitempty"`
	}{
		Name:                   fm.Name,
		Description:            fm.Description,
		Version:                fm.Version,
		License:                fm.License,
		Author:                 fm.Author,
		AllowedTools:           fm.AllowedTools,
		DisableModelInvocation: fm.DisableModelInvocation,
		Metadata:               fm.Metadata,
	}
	// user-invocable 默认即为 true，只在显式关掉时落盘，避免文件里塞满噪音字段
	if !fm.UserInvocable {
		disabled := false
		out.UserInvocable = &disabled
	}

	head, err := yaml.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("序列化 frontmatter 失败: %w", err)
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(head)
	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimRight(body, "\n"))
	sb.WriteString("\n")
	return sb.String(), nil
}

// isKebabCase 是否 kebab-case（小写字母/数字/单个短横线，首尾非短横线）
func isKebabCase(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		return false
	}
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			prevDash = false
		case r == '-':
			if prevDash {
				return false
			}
			prevDash = true
		default:
			return false
		}
	}
	return true
}

// sortedMapKeys 稳定排序的键列表（告警信息要求稳定输出，否则测试与界面会抖动）
func sortedMapKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
