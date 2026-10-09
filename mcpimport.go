package main

import (
	"fmt"
	"sort"
	"strings"

	"wails-tmp/internal/tool"
)

// ===== 官方 mcpServers 格式的导入 / 导出 =====
//
// 内部存储的 MCP 配置已经是**官方形状**（type: streamable-http|sse|stdio + url/headers/command/args/env），
// 所以这里几乎不需要翻译：只做「套上/脱掉 mcpServers 外壳」与校验，剩下的字段原样搬。
// 用户可以放心粘贴官方文档 / Claude Desktop / Cline 里的片段，也能导出回去分享。
//
// **解析与转换的引擎在 `internal/tool/mcpimport.go`**，本文件只剩两个 `*App` 方法
// （`ImportMCPServers` 编排落库、`ExportMCPServers` 转发），它们各自只调一次 tool 包的函数。
//
// 两个结果类型**必须留在 main**：`ImportMCPServers` 是 Wails 绑定（前端
// `frontend/wailsjs/go/main/App.d.ts:98` 声明返回 `main.MCPImportResult`，
// `models.ts` 里生成对应的 `main.MCPImportSkip` / `main.MCPImportResult` 类）。
// 把它们挪进 `internal/tool` 会让绑定类型名变成 `tool.MCPImportResult`，前端要跟着重生成。
// 因此 internal/tool 里曾有的同名副本已删（那里从头到尾无人引用，是搬文件时留下的死代码），
// 这份是**唯一真源**。

// MCPImportSkip 一条被跳过的条目及原因（界面要如实展示，不能静默吞掉）
type MCPImportSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// MCPImportResult 导入结果
type MCPImportResult struct {
	Imported []string        `json:"imported"`
	Skipped  []MCPImportSkip `json:"skipped"`
}

// ImportMCPServers 解析官方 mcpServers 片段并落成 MCP 来源。
// 兼容三种输入形态：
//  1. {"mcpServers": {"名字": {...}}}  —— 官方文档/客户端的标准片段
//  2. {"名字": {...}}                  —— 去掉外层包装的字典
//  3. {"url": ..., "type": ...}        —— 单个服务器对象（名字取 name 字段或从 url/command 推断）
//
// 也容忍被 json 代码块包裹的内容（从文档/聊天里复制常见）。同名条目按更新处理（幂等）。
func (a *App) ImportMCPServers(raw string) (*MCPImportResult, error) {
	text := tool.StripCodeFence(raw)
	if text == "" {
		return nil, fmt.Errorf("内容为空")
	}

	servers, err := tool.ParseOfficialMCPServers(text)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("没有解析到任何 MCP 服务器配置")
	}

	// 按名字排序后处理：结果顺序稳定，便于界面展示与测试断言
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, strings.TrimSpace(n))
	}
	sort.Strings(names)

	res := &MCPImportResult{Imported: []string{}, Skipped: []MCPImportSkip{}}
	for _, name := range names {
		if name == "" {
			res.Skipped = append(res.Skipped, MCPImportSkip{Name: "(未命名)", Reason: "服务器名为空"})
			continue
		}
		cfg, reason := tool.OfficialToMCPConfig(servers[name])
		if reason != "" {
			res.Skipped = append(res.Skipped, MCPImportSkip{Name: name, Reason: reason})
			continue
		}
		cleanName := tool.SanitizeToolName(name)
		if cleanName == "" {
			res.Skipped = append(res.Skipped, MCPImportSkip{Name: name, Reason: "服务器名不合法（只允许字母、数字、下划线与连字符）"})
			continue
		}
		src := tool.ToolSource{
			ID:          cleanName,
			Name:        cleanName,
			Label:       cleanName,
			Description: tool.DescribeMCPConfig(cfg),
			Kind:        tool.SourceMCP,
			Icon:        "blocks",
			Enabled:     !servers[name].Disabled, // 兼容 Cline 的 disabled 字段
			MCP:         cfg,
		}
		// 复用 SaveTool：名称校验、默认值、唯一性、连接失效都在那一处
		if _, err := a.SaveTool(src); err != nil {
			res.Skipped = append(res.Skipped, MCPImportSkip{Name: name, Reason: err.Error()})
			continue
		}
		res.Imported = append(res.Imported, cleanName)
	}
	return res, nil
}

// ExportMCPServers 把 MCP 来源导出为官方 mcpServers 片段。
// names 为空表示导出全部；传入的名字可以是来源名，也可以是工具名（mcp__server__tool）。
func (a *App) ExportMCPServers(names []string) (string, error) {
	if a.toolStore == nil {
		return "", fmt.Errorf("工具存储未初始化")
	}
	return tool.BuildMCPServersJSON(a.toolStore.GetAll(), names)
}
