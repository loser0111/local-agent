package tool

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// ===== 官方 mcpServers 格式的导入 / 导出 =====
//
// 内部存储的 MCP 配置已经是**官方形状**（type: streamable-http|sse|stdio + url/headers/command/args/env），
// 所以这里几乎不需要翻译：只做「套上/脱掉 mcpServers 外壳」与校验，剩下的字段原样搬。
// 用户可以放心粘贴官方文档 / Claude Desktop / Cline 里的片段，也能导出回去分享。

// OfficialMCPServer 官方片段里的单个服务器
type OfficialMCPServer struct {
	Type    string            `json:"type,omitempty"` // streamable-http | sse | stdio
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// Name 非官方字段：仅在"单个服务器对象"形态下用来取名字
	Name string `json:"name,omitempty"`
	// Disabled 部分客户端（如 Cline）用它标记停用；识别并映射到内部 Enabled
	Disabled bool `json:"disabled,omitempty"`
}

// BuildMCPServersJSON 把 MCP 来源导出为官方 mcpServers 片段。
// names 为空表示导出全部；传入的名字可以是来源名，也可以是工具名（mcp__server__tool）。
func BuildMCPServersJSON(sources []*ToolSource, names []string) (string, error) {
	want := map[string]bool{}
	for _, n := range names {
		if s := NormalizeMCPName(n); s != "" {
			want[s] = true
		}
	}

	out := map[string]OfficialMCPServer{}
	for _, src := range sources {
		if src == nil || src.Kind != SourceMCP || src.MCP == nil {
			continue
		}
		if len(want) > 0 && !want[src.Name] {
			continue
		}
		// 字段名一致，直接搬
		out[src.Name] = OfficialMCPServer{
			Type:    src.MCP.Type,
			URL:     src.MCP.URL,
			Headers: src.MCP.Headers,
			Command: src.MCP.Command,
			Args:    src.MCP.Args,
			Env:     src.MCP.Env,
		}
	}
	if len(out) == 0 {
		return "", fmt.Errorf("没有可导出的 MCP 配置")
	}

	payload := map[string]interface{}{"mcpServers": out}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化失败: %w", err)
	}
	return string(b) + "\n", nil
}

// ===== 解析 =====

// StripCodeFence 去掉 json 代码块包裹与首尾空白
func StripCodeFence(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// ParseOfficialMCPServers 识别三种输入形态并返回「名字 → 服务器」
func ParseOfficialMCPServers(text string) (map[string]OfficialMCPServer, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &top); err != nil {
		return nil, fmt.Errorf("不是合法的 JSON：%v", err)
	}

	// 形态一：官方标准片段
	if raw, ok := top["mcpServers"]; ok {
		var m map[string]OfficialMCPServer
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("mcpServers 解析失败：%v", err)
		}
		return m, nil
	}

	// 形态三：顶层直接是一个服务器的字段集合
	if IsSingleServerObject(top) {
		var one OfficialMCPServer
		if err := json.Unmarshal([]byte(text), &one); err != nil {
			return nil, fmt.Errorf("服务器配置解析失败：%v", err)
		}
		name := strings.TrimSpace(one.Name)
		if name == "" {
			name = DeriveMCPServerName(one)
		}
		if name == "" {
			return nil, fmt.Errorf("这是单个服务器配置但推断不出名字；请补一个 \"name\" 字段，" +
				"或改用 {\"mcpServers\": {\"名字\": {...}}} 形式")
		}
		return map[string]OfficialMCPServer{name: one}, nil
	}

	// 形态二：去掉外层包装的「名字 → 服务器」字典
	var m map[string]OfficialMCPServer
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		return nil, fmt.Errorf("解析失败：%v", err)
	}
	return m, nil
}

// IsSingleServerObject 顶层是否直接出现服务器字段（用于区分"单个服务器"与"名字字典"）
func IsSingleServerObject(top map[string]json.RawMessage) bool {
	for _, k := range []string{"url", "command", "type", "headers", "args", "env"} {
		if _, ok := top[k]; ok {
			return true
		}
	}
	return false
}

// DeriveMCPServerName 从 url / command 推断一个合理的服务器名
func DeriveMCPServerName(s OfficialMCPServer) string {
	if u := strings.TrimSpace(s.URL); u != "" {
		if parsed, err := url.Parse(u); err == nil {
			host := parsed.Host
			if i := strings.Index(host, ":"); i > 0 {
				host = host[:i]
			}
			if parts := strings.Split(host, "."); len(parts) > 0 && parts[0] != "" {
				// qconfig-mcp-server-function.faas.ctripcorp.com → qconfig-mcp-server-function
				return SanitizeToolName(parts[0])
			}
		}
	}
	if c := strings.TrimSpace(s.Command); c != "" {
		base := filepath.Base(c)
		return SanitizeToolName(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	return ""
}

// SanitizeToolName 把推断出来的名字规整成合法工具名（字母数字下划线连字符）
func SanitizeToolName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// NormalizeMCPName 把工具名（mcp__server__tool）归一成来源名；其它形态原样返回
func NormalizeMCPName(n string) string {
	n = strings.TrimSpace(n)
	if strings.HasPrefix(n, "mcp__") {
		rest := strings.TrimPrefix(n, "mcp__")
		if i := strings.Index(rest, "__"); i > 0 {
			return rest[:i]
		}
	}
	return n
}

// ===== 转换 =====

// OfficialToMCPConfig 官方对象 → 内部 MCPConfig（字段同名，这里只做归一与校验）
func OfficialToMCPConfig(s OfficialMCPServer) (*MCPConfig, string) {
	cfg := &MCPConfig{
		Type:    NormalizeMCPType(s.Type, s.URL, s.Command),
		URL:     strings.TrimSpace(s.URL),
		Headers: s.Headers,
		Command: strings.TrimSpace(s.Command),
		Args:    s.Args,
		Env:     s.Env,
	}
	if reason := cfg.Validate(); reason != "" {
		return nil, reason
	}
	return cfg, ""
}

// NormalizeMCPType 归一官方 type 取值：http/streamable-http 统一成 streamable-http；
// 空值时按"有 command 且无 url"判为 stdio，否则按 HTTP。
func NormalizeMCPType(t, rawURL, command string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "streamable-http", "streamablehttp", "http":
		return "streamable-http"
	case "sse":
		return "sse"
	case "stdio":
		return "stdio"
	}
	if strings.TrimSpace(command) != "" && strings.TrimSpace(rawURL) == "" {
		return "stdio"
	}
	return "streamable-http"
}

// DescribeMCPConfig 生成给界面/模型看的来源描述（说明接入方式与目标地址）
func DescribeMCPConfig(cfg *MCPConfig) string {
	if cfg == nil {
		return "MCP 服务器"
	}
	desc := fmt.Sprintf("MCP 服务器（type=%s", cfg.Type)
	if cfg.URL != "" {
		desc += "，url=" + cfg.URL
	}
	if cfg.Command != "" {
		desc += "，command=" + cfg.Command
	}
	return desc + "）"
}
