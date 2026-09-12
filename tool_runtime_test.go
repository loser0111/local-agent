package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestToolManager 构造基于临时目录的 ToolManager（内置 exec_shell）
func newTestToolManager(t *testing.T) (*ToolManager, string) {
	t.Helper()
	dir := t.TempDir()
	store := NewToolStore(filepath.Join(dir, "tools.json"))
	return NewToolManager(store), dir
}

// 模板渲染
func TestRenderTemplate(t *testing.T) {
	got := renderTemplate("echo {{input}} --count {{n}}", map[string]interface{}{
		"input": "hello",
		"n":     3,
	})
	if got != "echo hello --count 3" {
		t.Fatalf("模板渲染错误: %s", got)
	}
}

// 动态 CLI 工具保存 → 装配 → 经 tool_router 执行
func TestDynamicCLIToolEndToEnd(t *testing.T) {
	tm, _ := newTestToolManager(t)

	cfg := &ToolConfig{
		ID:          "tool_test_echo",
		Name:        "say_hello",
		Label:       "打招呼",
		Description: "输出打招呼内容",
		Type:        ToolTypeCLI,
		Icon:        "terminal",
		Enabled:     true,
		Parameters:  []ToolParamConfig{{Name: "input", Description: "内容", Required: true}},
	}
	// Windows PowerShell 的 echo 是 Write-Output 别名，两平台都能执行 echo
	cfg.Config, _ = json.Marshal(CLIToolConfig{Command: "echo {{input}}", Timeout: 10})
	if err := tm.Store().Save(cfg); err != nil {
		t.Fatalf("保存工具失败: %v", err)
	}

	view := tm.BuildView(context.Background(), nil)

	// list 应包含 exec_shell 和 say_hello
	out, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("list 失败: %v", err)
	}
	if !strings.Contains(out, "say_hello") || !strings.Contains(out, "exec_shell") {
		t.Fatalf("list 结果缺少工具: %s", out)
	}

	// query 搜索只返回匹配项
	searched, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "list",
		"query":  "打招呼",
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if !strings.Contains(searched, "say_hello") || strings.Contains(searched, "exec_shell") {
		t.Fatalf("搜索结果不正确: %s", searched)
	}

	// describe 返回参数定义
	desc, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action":    "describe",
		"tool_name": "say_hello",
	})
	if err != nil {
		t.Fatalf("describe 失败: %v", err)
	}
	if !strings.Contains(desc, "input") {
		t.Fatalf("describe 缺少参数定义: %s", desc)
	}

	// execute 经路由器执行 CLI 工具
	res, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action":    "execute",
		"tool_name": "say_hello",
		"arguments": map[string]interface{}{"input": "hello-world-xyz"},
	})
	if err != nil {
		t.Fatalf("执行 CLI 工具失败: %v", err)
	}
	if !strings.Contains(res, "hello-world-xyz") {
		t.Fatalf("CLI 输出不符合预期: %q", res)
	}
}

// 停用工具不参与装配
func TestDisabledToolExcluded(t *testing.T) {
	tm, _ := newTestToolManager(t)
	cfg := &ToolConfig{
		ID: "tool_off", Name: "off_tool", Description: "已停用", Type: ToolTypeCLI, Enabled: false,
		Config: json.RawMessage(`{"command":"echo x"}`),
	}
	if err := tm.Store().Save(cfg); err != nil {
		t.Fatal(err)
	}
	view := tm.BuildView(context.Background(), nil)
	out, _ := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if strings.Contains(out, "off_tool") {
		t.Fatalf("停用工具不应出现在列表中: %s", out)
	}
}

// 会话白名单：只暴露白名单内的顶层工具
func TestSessionWhitelist(t *testing.T) {
	tm, _ := newTestToolManager(t)
	cfg := &ToolConfig{
		ID: "tool_pick", Name: "picked", Description: "白名单工具", Type: ToolTypeCLI, Enabled: true,
		Config: json.RawMessage(`{"command":"echo pick"}`),
	}
	if err := tm.Store().Save(cfg); err != nil {
		t.Fatal(err)
	}
	// 白名单只含 exec_shell，picked 不出现
	view := tm.BuildView(context.Background(), []string{"exec_shell"})
	out, _ := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if strings.Contains(out, "picked") || !strings.Contains(out, "exec_shell") {
		t.Fatalf("白名单过滤错误: %s", out)
	}
	// 执行白名单外工具应报未找到
	if _, err := view.ExecuteTool("picked", map[string]interface{}{}); err == nil {
		t.Fatal("白名单外工具不应可执行")
	}
}

// API 工具配置的模板字段能正确序列化/反序列化
func TestAPIToolConfigRoundTrip(t *testing.T) {
	tm, dir := newTestToolManager(t)
	cfg := &ToolConfig{
		ID: "tool_api", Name: "weather", Label: "天气", Description: "查天气",
		Type: ToolTypeAPI, Icon: "cloud", Enabled: true,
		Parameters: []ToolParamConfig{{Name: "city", Description: "城市", Required: true}},
	}
	cfg.Config, _ = json.Marshal(APIToolConfig{
		Method:  "GET",
		URL:     "https://example.com/weather?city={{city}}",
		Headers: map[string]string{"X-Key": "secret"},
		Timeout: 15,
	})
	if err := tm.Store().Save(cfg); err != nil {
		t.Fatal(err)
	}

	// 重新从文件加载（验证持久化）
	data, err := os.ReadFile(filepath.Join(dir, "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	var loaded []*ToolConfig
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	var apiCfg APIToolConfig
	if err := json.Unmarshal(loaded[len(loaded)-1].Config, &apiCfg); err != nil {
		t.Fatal(err)
	}
	if apiCfg.URL != "https://example.com/weather?city={{city}}" || apiCfg.Headers["X-Key"] != "secret" {
		t.Fatalf("API 配置往返错误: %+v", apiCfg)
	}

	// 装配成功且 URL 模板渲染正确（不实际发请求，只验证字段）
	view := tm.BuildView(context.Background(), nil)
	tool := view.nonMeta["weather"]
	if tool == nil {
		t.Fatal("API 工具未装配")
	}
	if _, ok := tool.(*DynamicAPITool); !ok {
		t.Fatal("装配类型不是 DynamicAPITool")
	}
}

// 内置工具不可删除
func TestBuiltinCannotDelete(t *testing.T) {
	tm, _ := newTestToolManager(t)
	if err := tm.Store().Delete("exec_shell"); err == nil {
		t.Fatal("内置工具应拒绝删除")
	}
}
