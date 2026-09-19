package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ===== P0：参数 schema 直通（不压平）=====

// MCP 的 inputSchema 必须原样保留：enum / items / 嵌套 properties / required 一个都不能丢
func TestNormalizeInputSchemaKeepsFullStructure(t *testing.T) {
	raw := map[string]any{
		"type":     "object",
		"required": []any{"appid", "config"},
		"properties": map[string]any{
			"appid": map[string]any{"type": "string", "description": "目标 appid"},
			"env": map[string]any{
				"type": "string",
				"enum": []any{"pro", "fat", "uat"}, // enum 是重点：压平会丢
			},
			"config": map[string]any{
				"type": "object", // 嵌套结构也要保留
				"properties": map[string]any{
					"key":   map[string]any{"type": "string"},
					"value": map[string]any{"type": "string"},
				},
				"required": []any{"key"},
			},
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}

	got := normalizeInputSchema("mcp__qconfig__changeqconfig", raw)
	if len(got) == 0 {
		t.Fatal("应产出可直通的 schema")
	}

	var schema map[string]any
	if err := json.Unmarshal(got, &schema); err != nil {
		t.Fatalf("直通 schema 应是合法 JSON: %v", err)
	}
	props := schema["properties"].(map[string]any)

	if _, ok := props["env"].(map[string]any)["enum"]; !ok {
		t.Error("enum 被丢掉了")
	}
	if _, ok := props["tags"].(map[string]any)["items"]; !ok {
		t.Error("数组 items 被丢掉了")
	}
	cfg, ok := props["config"].(map[string]any)
	if !ok {
		t.Fatal("嵌套对象被丢掉了")
	}
	if _, ok := cfg["properties"].(map[string]any)["key"]; !ok {
		t.Error("嵌套 properties 被丢掉了")
	}
	req, _ := schema["required"].([]any)
	if len(req) != 2 {
		t.Errorf("required 应保留 2 项，实际 %v", schema["required"])
	}
}

// 兜底行为：非对象 / 缺 type / 体积超限
func TestNormalizeInputSchemaEdgeCases(t *testing.T) {
	if got := normalizeInputSchema("t", "just-a-string"); got != nil {
		t.Errorf("非对象应返回 nil（走简化表兜底），实际 %s", got)
	}
	if got := normalizeInputSchema("t", nil); got != nil {
		t.Error("nil 应返回 nil")
	}

	// 缺 type 时补上 type=object，其余不动
	got := normalizeInputSchema("t", map[string]any{"properties": map[string]any{"a": map[string]any{"type": "string"}}})
	var schema map[string]any
	if err := json.Unmarshal(got, &schema); err != nil {
		t.Fatalf("应合法: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("缺 type 时应补 object，实际 %v", schema["type"])
	}
	if _, ok := schema["properties"]; !ok {
		t.Error("补齐 type 时不应破坏其它字段")
	}

	// 体积超限 → 返回 nil（由调用方降级），避免把 prompt 撑爆
	big := map[string]any{"type": "object", "description": strings.Repeat("x", maxToolSchemaBytes+100)}
	if got := normalizeInputSchema("t", big); got != nil {
		t.Error("超过上限应返回 nil")
	}
}

// MCPTool 直通：模型看到的 schema 与服务器给的一致（而不是压平后的简化表）
func TestMCPToolSchemaPassthrough(t *testing.T) {
	tool := NewMCPTool(nil, &ToolSource{ID: "srv", Name: "qconfig", Kind: SourceMCP, MCP: &MCPConfig{Type: "streamable-http", URL: "https://x/mcp"}}, &mcp.Tool{
		Name:        "changeqconfig",
		Description: "修改配置",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"appid"},
			"properties": map[string]any{
				"appid": map[string]any{"type": "string"},
				"env":   map[string]any{"type": "string", "enum": []any{"pro", "fat"}},
			},
		},
	})

	if tool.GetName() != "mcp__qconfig__changeqconfig" {
		t.Fatalf("子工具命名异常: %s", tool.GetName())
	}
	raw := tool.JSONSchema()
	if len(raw) == 0 {
		t.Fatal("应能直通 inputSchema")
	}
	if !strings.Contains(string(raw), "enum") || !strings.Contains(string(raw), "required") {
		t.Fatalf("直通 schema 应保留 enum 与 required，实际 %s", raw)
	}

	// llmToolFor 用的是同一份 schema（直通，不是简化表）
	def := llmToolFor(tool)
	if string(def.Function.Parameters) != string(raw) {
		t.Errorf("llmToolFor 应直通原始 schema\nwant=%s\ngot =%s", raw, def.Function.Parameters)
	}

	// 简化表（GetParameters）仍然保留，作为体积超限时的兜底
	if len(tool.GetParameters()) != 2 {
		t.Errorf("兜底简化表应有 2 个参数，实际 %d", len(tool.GetParameters()))
	}
}

// describe 是 MCP 工具唯一的发现入口，必须回传完整 schema 而不是简化表
func TestDescribeToolReturnsFullSchema(t *testing.T) {
	tool := NewMCPTool(nil, &ToolSource{ID: "srv", Name: "qconfig", Kind: SourceMCP, MCP: &MCPConfig{Type: "streamable-http", URL: "https://x/mcp"}}, &mcp.Tool{
		Name:        "list_envs",
		Description: "列出环境",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"appid": map[string]any{"type": "string", "enum": []any{"100049128", "100000001"}},
			},
		},
	})
	registry := map[string]ToolInterface{tool.GetName(): tool}
	typeMap := map[string]string{tool.GetName(): string(SourceMCP)}
	meta := NewMetaTool(registry, typeMap, nil)

	out, err := meta.Execute(nil, map[string]interface{}{"action": "describe", "tool_name": tool.GetName()})
	if err != nil {
		t.Fatalf("describe 失败: %v", err)
	}
	if !strings.Contains(out, "enum") || !strings.Contains(out, "100049128") {
		t.Fatalf("describe 应回传完整 schema（含 enum 取值），实际:\n%s", out)
	}

	// 对照：没有 SchemaProvider 的工具仍走简化表（不能退化成空）
	plain := &stubPlainTool{BaseTool: &BaseTool{Name: "plain", Description: "d", Parameters: map[string]*ToolArgDef{
		"path": {Type: "string", Description: "路径"},
	}}}
	plainMeta := NewMetaTool(map[string]ToolInterface{"plain": plain}, map[string]string{"plain": string(SourceBuiltin)}, nil)
	out2, err := plainMeta.Execute(nil, map[string]interface{}{"action": "describe", "tool_name": "plain"})
	if err != nil {
		t.Fatalf("describe 失败: %v", err)
	}
	if !strings.Contains(out2, "path") {
		t.Fatalf("简化表工具的参数不应丢失，实际:\n%s", out2)
	}
}

// 内置/CLI/API 工具仍由简化表合成 schema，且 required 不丢
func TestBuildJSONSchemaForSimpleTools(t *testing.T) {
	raw := buildJSONSchema(map[string]*ToolArgDef{
		"path":    {Type: "string", Description: "文件路径"},
		"content": {Type: "string"},
	}, []string{"path", "content"})

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("应是合法 JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("type 应为 object，实际 %v", schema["type"])
	}
	props := schema["properties"].(map[string]any)
	if props["path"].(map[string]any)["description"] != "文件路径" {
		t.Error("参数说明应保留")
	}
	if len(schema["required"].([]any)) != 2 {
		t.Errorf("required 应保留 2 项，实际 %v", schema["required"])
	}

	// 无 required 时不写这个字段（避免向网关发 "required": null）
	raw2 := buildJSONSchema(map[string]*ToolArgDef{"a": {Type: "string"}}, nil)
	if strings.Contains(string(raw2), "required") {
		t.Errorf("无必填参数时不应输出 required 字段: %s", raw2)
	}

	// schemaForTool：实现 SchemaProvider 的工具优先直通
	tool := &stubSchemaTool{BaseTool: &BaseTool{Name: "s", Parameters: map[string]*ToolArgDef{}}, schema: json.RawMessage(`{"type":"object","properties":{"x":{"enum":["a"]}}}`)}
	if got := schemaForTool(tool); !strings.Contains(string(got), "enum") {
		t.Errorf("应优先使用工具自报的 schema，实际 %s", got)
	}
}

// 传给 Anthropic 的 input_schema 直通，只兜底 type
func TestInputSchemaForAnthropic(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","required":["a"],"properties":{"a":{"type":"string","enum":["x","y"]}}}`)
	schema := inputSchemaFor(raw)
	if schema["type"] != "object" {
		t.Errorf("type 应保留，实际 %v", schema["type"])
	}
	if _, ok := schema["required"]; !ok {
		t.Error("required 应保留")
	}
	props := schema["properties"].(map[string]interface{})
	if _, ok := props["a"].(map[string]interface{})["enum"]; !ok {
		t.Error("enum 应保留（这正是压平会丢掉的东西）")
	}

	// 空 schema：给一个合法的空对象 schema，不能是 nil（Anthropic 要求 input_schema 存在）
	empty := inputSchemaFor(nil)
	if empty["type"] != "object" {
		t.Errorf("空 schema 应兜底为 object，实际 %v", empty)
	}
	if _, ok := empty["properties"]; !ok {
		t.Error("空 schema 也应有 properties（部分严格网关会拒绝缺字段的 schema）")
	}

	// 缺 type：补上，其余不动
	partial := inputSchemaFor(json.RawMessage(`{"properties":{"b":{"type":"string"}}}`))
	if partial["type"] != "object" {
		t.Errorf("缺 type 时应补 object，实际 %v", partial["type"])
	}
}

// stubSchemaTool 用于测试 SchemaProvider 优先级的桩
type stubSchemaTool struct {
	*BaseTool
	schema json.RawMessage
}

func (s *stubSchemaTool) JSONSchema() json.RawMessage { return s.schema }

func (s *stubSchemaTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "", nil
}

// stubPlainTool 没有完整 schema 的普通工具（走简化表）
type stubPlainTool struct {
	*BaseTool
}

func (s *stubPlainTool) Execute(context.Context, map[string]interface{}) (string, error) {
	return "", nil
}
