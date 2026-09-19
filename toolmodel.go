package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ===== 工具的概念分层（来源 / 工具 / 暴露策略）=====
//
// 旧模型把三种角色压在一个结构体里：接入来源（MCP 服务器 / CLI 模板 / HTTP API / 内置实现）、
// 能力单元（一个可被模型调用的工具）、展示条目（设置页列表的一行）。粒度不同——
// 对内置/CLI/HTTP 是"一条配置 = 一个工具"，对 MCP 是"一条配置 = 一台服务器 + N 个子工具"——
// 于是出现"字段各只服务一类""白名单粒度随类型变化""同一个子工具三个名字"这些现象。
//
// 现在显式分三层：
//  1. 来源 ToolSource：怎么连、能不能用（本文件定义，落盘到 tools.json）
//  2. 工具 ToolRef：  一个可被调用的能力单元（由来源派生，见 tools.go 的装配）
//  3. 暴露策略 Exposure：给不给模型看（direct 直出 / router 经路由器 / internal 不暴露）
//
// MCP 配置采用**官方形状**（`type: streamable-http|sse|stdio`），与 Claude Desktop / Cline /
// MCP 文档一致：导入导出因此几乎不需要翻译，用户也不必再理解两套字段名。

// SourceKind 来源类型
type SourceKind string

const (
	SourceBuiltin SourceKind = "builtin" // 编译进程序的实现
	SourceCLI     SourceKind = "cli"     // 命令模板
	SourceHTTP    SourceKind = "http"    // HTTP API 模板（旧数据里的 "api" 迁移到它）
	SourceMCP     SourceKind = "mcp"     // MCP 服务器（一台服务器 + N 个子工具）
)

// ValidSourceKind 校验来源类型
func ValidSourceKind(k SourceKind) bool {
	switch k {
	case SourceBuiltin, SourceCLI, SourceHTTP, SourceMCP:
		return true
	default:
		return false
	}
}

// Exposure 暴露策略：决定工具是否直接给模型看
type Exposure string

const (
	// ExposureDirect 直出给模型（省一轮发现往返；工具很多时 prompt 会变大）
	ExposureDirect Exposure = "direct"
	// ExposureRouter 经 tool_router 发现后执行（默认：避免 prompt 膨胀）
	ExposureRouter Exposure = "router"
	// ExposureInternal 不暴露给模型：list 不列出、describe/execute 一律当"未找到"。
	// 只应由配置显式指定，绝不要在"默认推导"里返回它——见 DefaultExposure 的说明
	// （旧注释写的是"如 read_skill 由技能机制触发"，但 read_skill 实际走路由器，
	// 这句误导性的举例正是 exec_shell 被误判成 internal 的思想源头）。
	ExposureInternal Exposure = "internal"
)

// ValidExposure 校验暴露策略
func ValidExposure(e Exposure) bool {
	switch e {
	case ExposureDirect, ExposureRouter, ExposureInternal:
		return true
	default:
		return false
	}
}

// DefaultExposure 各来源的默认暴露策略。
// 内置的常用工具直出（对齐既有行为：文件工具与 ask_user 直出），其余经路由器。
//
// 这里曾经写成「内置但不在直出名录里 → ExposureInternal」，结果把 exec_shell 整个从模型
// 视野里抹掉了：它是 defaultSources() 里唯一不在 directToolOrder 的内置工具，于是命中兜底
// 分支被标记 internal，listTools 跳过它、describe/execute 当"未找到"，模型只能报
// "找不到 exec_shell"。**internal 绝不能由"没配置"推断出来**——只能由配置显式指定，
// 否则以后任何一个新增内置工具，只要忘了加进 directToolOrder，就会对模型凭空消失。
func DefaultExposure(kind SourceKind, name string) Exposure {
	if kind != SourceBuiltin {
		return ExposureRouter
	}
	for _, n := range directToolOrder {
		if n == name {
			return ExposureDirect
		}
	}
	return ExposureRouter
}

// CLIConfig CLI 来源配置：命令模板（{{参数名}} 占位）
type CLIConfig struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // 秒；0 表示默认
}

// HTTPConfig HTTP API 来源配置（url/headers/body 支持 {{参数名}} 占位）
type HTTPConfig struct {
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}

// MCPConfig MCP 服务器配置，**字段与官方 mcpServers 条目一致**（type/url/headers/command/args/env），
// 这样导入导出只需搬字段、不需要翻译，用户也不必记两套名字。
type MCPConfig struct {
	Type    string            `json:"type,omitempty"` // streamable-http | sse | stdio
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// IsStdio 是否 stdio 接入（无 url、有 command；官方片段里 stdio 常省略 type）
func (c *MCPConfig) IsStdio() bool {
	if c == nil {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(c.Type))
	if t == "stdio" {
		return true
	}
	if t == "" && strings.TrimSpace(c.Command) != "" && strings.TrimSpace(c.URL) == "" {
		return true
	}
	return false
}

// Validate 校验配置完整性；返回空串表示合法
func (c *MCPConfig) Validate() string {
	if c == nil {
		return "缺少 MCP 配置"
	}
	if c.IsStdio() {
		if strings.TrimSpace(c.Command) == "" {
			return "stdio 接入缺少 command"
		}
		return ""
	}
	t := strings.ToLower(strings.TrimSpace(c.Type))
	if t != "" && t != "streamable-http" && t != "streamablehttp" && t != "http" && t != "sse" {
		return fmt.Sprintf("不支持的 type: %q（支持 streamable-http / sse / stdio）", c.Type)
	}
	if strings.TrimSpace(c.URL) == "" {
		return "缺少 url"
	}
	return ""
}

// ToolSource 接入来源：怎么连、能不能用。
// 一条来源对应：内置实现 / 一个 CLI 模板 / 一个 HTTP API 模板 / 一台 MCP 服务器（可含多个子工具）。
type ToolSource struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"` // 来源名；MCP 子工具名 = mcp__<Name>__<tool>
	Label       string     `json:"label,omitempty"`
	Description string     `json:"description,omitempty"`
	Kind        SourceKind `json:"kind"`
	Icon        string     `json:"icon,omitempty"`
	Enabled     bool       `json:"enabled"`
	Builtin     bool       `json:"builtin,omitempty"`  // 内置来源不可删除
	Exposure    Exposure   `json:"exposure,omitempty"` // 该来源的工具如何暴露给模型

	// CLI / HTTP 模板的参数声明
	Parameters []ToolParamConfig `json:"parameters,omitempty"`

	// 按 Kind 二选一（不再是一个弱类型的 json.RawMessage）
	CLI  *CLIConfig  `json:"cli,omitempty"`
	HTTP *HTTPConfig `json:"http,omitempty"`
	MCP  *MCPConfig  `json:"mcp,omitempty"`

	// MCP：子工具禁用名单与上次发现结果
	DisabledTools []string      `json:"disabledTools,omitempty"`
	Discovered    []MCPToolMeta `json:"discovered,omitempty"`
}

// ToolFile 落盘结构。版本号用于兼容读取旧格式（旧格式是裸数组）。
type ToolFile struct {
	Version int           `json:"version"`
	Sources []*ToolSource `json:"sources"`
}

// ToolFileVersion 当前落盘版本。
//
// 版本历史：
//
//	2  v1 裸数组迁移到 v2 对象（version + sources）
//	3  修复 v2 迁移期写入的错误曝光值：v2 的 DefaultExposure 把「不在 directToolOrder
//	   里的内置工具」判成 internal，于是 exec_shell 被持久化为 "exposure": "internal"，
//	   对模型彻底不可见。加载 v2 文件时由 repairBuiltinExposure 修回（见该函数）。
const ToolFileVersion = 3

// normalizeSource 补齐默认值并校验；返回空串表示合法
func normalizeSource(src *ToolSource) string {
	if src == nil {
		return "来源为空"
	}
	src.Name = strings.TrimSpace(src.Name)
	src.ID = strings.TrimSpace(src.ID)
	if src.ID == "" {
		if src.Name == "" {
			return "来源 ID 与名称不能同时为空"
		}
		src.ID = src.Name
	}
	if src.Name == "" {
		src.Name = src.ID
	}
	if !ValidSourceKind(src.Kind) {
		return fmt.Sprintf("未知的来源类型: %q", src.Kind)
	}
	if !ValidExposure(src.Exposure) {
		src.Exposure = DefaultExposure(src.Kind, src.Name)
	}
	if src.Icon == "" {
		src.Icon = defaultIconForSource(src.Kind)
	}
	if src.Label == "" {
		src.Label = src.Name
	}
	switch src.Kind {
	case SourceCLI:
		if src.CLI == nil || strings.TrimSpace(src.CLI.Command) == "" {
			return "CLI 来源缺少命令模板"
		}
	case SourceHTTP:
		if src.HTTP == nil || strings.TrimSpace(src.HTTP.URL) == "" {
			return "HTTP 来源缺少 URL"
		}
	case SourceMCP:
		if reason := src.MCP.Validate(); reason != "" {
			return reason
		}
	}
	return ""
}

// defaultIconForSource 各来源的默认图标
func defaultIconForSource(kind SourceKind) string {
	switch kind {
	case SourceCLI:
		return "terminal"
	case SourceMCP:
		return "blocks"
	case SourceHTTP:
		return "webhook"
	default:
		return "zap"
	}
}

// AvailableSubTools 该来源当前可用的子工具（剔除禁用项）
func (s *ToolSource) AvailableSubTools() []MCPToolMeta {
	out := make([]MCPToolMeta, 0, len(s.Discovered))
	disabled := map[string]bool{}
	for _, d := range s.DisabledTools {
		disabled[d] = true
	}
	for _, m := range s.Discovered {
		if disabled[m.Name] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// ToolNameFor 子工具暴露给模型的完整名（MCP 加前缀）
func (s *ToolSource) ToolNameFor(sub string) string {
	if s.Kind == SourceMCP {
		return fmt.Sprintf("mcp__%s__%s", s.Name, sub)
	}
	return s.Name
}

// ===== v1 → v2 迁移 =====

// legacyToolConfig v1 落盘的条目形状（裸数组 + type + 弱类型 config）
type legacyToolConfig struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Label         string            `json:"label"`
	Description   string            `json:"description"`
	Type          string            `json:"type"`
	Icon          string            `json:"icon"`
	Enabled       bool              `json:"enabled"`
	Builtin       bool              `json:"builtin"`
	Parameters    []ToolParamConfig `json:"parameters"`
	Config        json.RawMessage   `json:"config"`
	DisabledTools []string          `json:"disabledTools"`
	Discovered    []MCPToolMeta     `json:"discovered"`
}

// legacyMCPConfig v1 的 MCP 配置（transport + url/command…），迁移时转成官方形状
type legacyMCPConfig struct {
	Transport string            `json:"transport"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
}

// migrateLegacySource 把 v1 条目转成 v2 来源；返回 nil 表示无法迁移（调用方跳过并告警）
func migrateLegacySource(legacy *legacyToolConfig) *ToolSource {
	if legacy == nil {
		return nil
	}
	src := &ToolSource{
		ID:            legacy.ID,
		Name:          legacy.Name,
		Label:         legacy.Label,
		Description:   legacy.Description,
		Icon:          legacy.Icon,
		Enabled:       legacy.Enabled,
		Builtin:       legacy.Builtin,
		Parameters:    legacy.Parameters,
		DisabledTools: legacy.DisabledTools,
		Discovered:    legacy.Discovered,
	}
	switch strings.ToLower(strings.TrimSpace(legacy.Type)) {
	case "builtin":
		src.Kind = SourceBuiltin
	case "cli":
		src.Kind = SourceCLI
		var cfg CLIConfig
		if len(legacy.Config) > 0 {
			_ = json.Unmarshal(legacy.Config, &cfg)
		}
		src.CLI = &cfg
	case "api", "http":
		src.Kind = SourceHTTP // v1 的 "api" 一律迁移为 "http"
		var cfg HTTPConfig
		if len(legacy.Config) > 0 {
			_ = json.Unmarshal(legacy.Config, &cfg)
		}
		src.HTTP = &cfg
	case "mcp":
		src.Kind = SourceMCP
		var old legacyMCPConfig
		if len(legacy.Config) > 0 {
			_ = json.Unmarshal(legacy.Config, &old)
		}
		src.MCP = &MCPConfig{
			Type:    legacyTransportToOfficial(old.Transport, old.URL, old.Command),
			URL:     old.URL,
			Headers: old.Headers,
			Command: old.Command,
			Args:    old.Args,
			Env:     old.Env,
		}
	default:
		return nil // 未知类型：宁可跳过并告警，也不要静默丢弃配置
	}
	src.Exposure = DefaultExposure(src.Kind, src.Name)
	return src
}

// legacyTransportToOfficial 内部 transport 取值 → 官方 type 取值
func legacyTransportToOfficial(transport, url, command string) string {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "sse":
		return "sse"
	case "stdio":
		return "stdio"
	case "http", "streamable-http":
		return "streamable-http"
	default:
		if strings.TrimSpace(command) != "" && strings.TrimSpace(url) == "" {
			return "stdio"
		}
		return "streamable-http"
	}
}

// repairBuiltinExposure v3 迁移：把被误判为 internal 的内置工具恢复成默认曝光策略。
//
// 只动「内置 且 当前是 internal」的条目——用户显式配成 direct / router 的一律不碰。
// 之所以敢这样修：内置工具里本就没有需要 internal 的（read_skill / read_skill_file 走
// 路由器发现，也不在这里），所以内置 + internal 只可能来自 v2 的错误推断。
// 返回被修复的工具名，供日志与测试断言。
func repairBuiltinExposure(sources []*ToolSource) []string {
	var fixed []string
	for _, src := range sources {
		if src == nil || src.Kind != SourceBuiltin || src.Exposure != ExposureInternal {
			continue
		}
		src.Exposure = DefaultExposure(src.Kind, src.Name)
		fixed = append(fixed, src.Name)
	}
	return fixed
}

// parseToolFile 解析 tools.json：v2/v3 对象直接读；v1 裸数组就地迁移。
// 返回 (来源列表, 是否需要重写配置文件, 错误)。
// 第二个返回值为 true 时调用方会备份并落盘一次，因此它同时覆盖「v1 迁移」与「v3 修复」。
func parseToolFile(data []byte) ([]*ToolSource, bool, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, false, nil
	}

	// v2/v3：{"version":N,"sources":[...]}
	if strings.HasPrefix(trimmed, "{") {
		var f ToolFile
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, false, fmt.Errorf("解析工具配置失败: %w", err)
		}
		// 低版本文件：修掉 v2 期写下的错误曝光值，落盘为当前版本
		if f.Version < ToolFileVersion {
			if fixed := repairBuiltinExposure(f.Sources); len(fixed) > 0 {
				fmt.Printf("[ToolStore] 恢复被隐藏的内置工具（曝光策略误判为 internal）: %s\n",
					strings.Join(fixed, ", "))
				return f.Sources, true, nil
			}
		}
		return f.Sources, false, nil
	}

	// v1：裸数组
	var legacy []*legacyToolConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, false, fmt.Errorf("解析旧版工具配置失败: %w", err)
	}
	out := make([]*ToolSource, 0, len(legacy))
	skipped := 0
	for _, l := range legacy {
		src := migrateLegacySource(l)
		if src == nil {
			skipped++
			fmt.Printf("[ToolStore] 跳过无法迁移的工具条目: %s（type=%q）\n", l.Name, l.Type)
			continue
		}
		out = append(out, src)
	}
	if skipped > 0 {
		fmt.Printf("[ToolStore] 迁移时跳过 %d 条无法识别的配置\n", skipped)
	}
	return out, true, nil
}

// sortSources 稳定排序：内置在前，其余按名称
func sortSources(sources []*ToolSource) {
	sort.SliceStable(sources, func(i, j int) bool {
		if (sources[i].Kind == SourceBuiltin) != (sources[j].Kind == SourceBuiltin) {
			return sources[i].Kind == SourceBuiltin
		}
		return sources[i].Name < sources[j].Name
	})
}
