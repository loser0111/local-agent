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
	return NewToolManager(store, nil), dir
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

	src := &ToolSource{
		ID:          "tool_test_echo",
		Name:        "say_hello",
		Label:       "打招呼",
		Description: "输出打招呼内容",
		Kind:        SourceCLI,
		Icon:        "terminal",
		Enabled:     true,
		Parameters:  []ToolParamConfig{{Name: "input", Description: "内容", Required: true}},
		// Windows PowerShell 的 echo 是 Write-Output 别名，两平台都能执行 echo
		CLI: &CLIConfig{Command: "echo {{input}}", Timeout: 10},
	}
	if err := tm.Store().Save(src); err != nil {
		t.Fatalf("保存工具失败: %v", err)
	}

	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})

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
	src := &ToolSource{
		ID: "tool_off", Name: "off_tool", Description: "已停用", Kind: SourceCLI, Enabled: false,
		CLI: &CLIConfig{Command: "echo x"},
	}
	if err := tm.Store().Save(src); err != nil {
		t.Fatal(err)
	}
	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})
	out, _ := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if strings.Contains(out, "off_tool") {
		t.Fatalf("停用工具不应出现在列表中: %s", out)
	}
}

// 会话白名单：只暴露白名单内的顶层工具
func TestSessionWhitelist(t *testing.T) {
	tm, _ := newTestToolManager(t)
	src := &ToolSource{
		ID: "tool_pick", Name: "picked", Description: "白名单工具", Kind: SourceCLI, Enabled: true,
		CLI: &CLIConfig{Command: "echo pick"},
	}
	if err := tm.Store().Save(src); err != nil {
		t.Fatal(err)
	}
	// 白名单只含 exec_shell，picked 不出现
	view := tm.BuildView(context.Background(), BuildOptions{EnabledTools: []string{"exec_shell"}, Enforcer: AllowAllEnforcer{}})
	out, _ := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if strings.Contains(out, "picked") || !strings.Contains(out, "exec_shell") {
		t.Fatalf("白名单过滤错误: %s", out)
	}
	// 执行白名单外工具应报未找到
	if _, err := view.ExecuteTool("picked", map[string]interface{}{}); err == nil {
		t.Fatal("白名单外工具不应可执行")
	}
}

// HTTP 来源配置的模板字段能正确落盘并读回（同时验证 v2 落盘结构）
func TestHTTPToolSourceRoundTrip(t *testing.T) {
	tm, dir := newTestToolManager(t)
	src := &ToolSource{
		ID: "tool_api", Name: "weather", Label: "天气", Description: "查天气",
		Kind: SourceHTTP, Icon: "cloud", Enabled: true,
		Parameters: []ToolParamConfig{{Name: "city", Description: "城市", Required: true}},
		HTTP: &HTTPConfig{
			Method:  "GET",
			URL:     "https://example.com/weather?city={{city}}",
			Headers: map[string]string{"X-Key": "secret"},
			Timeout: 15,
		},
	}
	if err := tm.Store().Save(src); err != nil {
		t.Fatal(err)
	}

	// 重新从文件加载：确认落盘是 v2 结构（version + sources），且配置为类型化字段
	data, err := os.ReadFile(filepath.Join(dir, "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f ToolFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("落盘应为 v2 结构: %v", err)
	}
	if f.Version != ToolFileVersion {
		t.Fatalf("落盘版本应为 %d，实际 %d", ToolFileVersion, f.Version)
	}
	var found *ToolSource
	for _, s := range f.Sources {
		if s.Name == "weather" {
			found = s
		}
	}
	if found == nil || found.Kind != SourceHTTP || found.HTTP == nil {
		t.Fatalf("HTTP 来源未正确落盘: %+v", found)
	}
	if found.HTTP.URL != "https://example.com/weather?city={{city}}" || found.HTTP.Headers["X-Key"] != "secret" {
		t.Fatalf("HTTP 配置往返错误: %+v", found.HTTP)
	}

	// 装配成功且 URL 模板渲染正确（不实际发请求，只验证字段）
	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})
	// 装配成功且被权限网关装饰（拆除装饰后应为 DynamicAPITool）
	tool := view.nonMeta["weather"]
	if tool == nil {
		t.Fatal("API 工具未装配")
	}
	guarded, ok := tool.(*guardedTool)
	if !ok {
		t.Fatalf("装配结果应被权限网关装饰，实际类型 %T", tool)
	}
	if _, ok := guarded.inner.(*DynamicAPITool); !ok {
		t.Fatalf("装饰内层应为 DynamicAPITool，实际 %T", guarded.inner)
	}
}

// 内置工具不可删除
func TestBuiltinCannotDelete(t *testing.T) {
	tm, _ := newTestToolManager(t)
	if err := tm.Store().Delete("exec_shell"); err == nil {
		t.Fatal("内置工具应拒绝删除")
	}
}

// ===== 曝光策略：exec_shell 消失事故的回归防线 =====
//
// 背景：v2 期的 DefaultExposure 把「内置但不在 directToolOrder 里」判成 ExposureInternal，
// 而 exec_shell 当时是 defaultSources() 里唯一这样的工具，于是它被标记为对模型不可见
// （list 不列出、describe/execute 当"未找到"），模型只能报"找不到 exec_shell"。
// 下面三个测试分别守住：默认值不再产生 internal、全新安装的推导正确、存量错误值能被修复。
//
// 后续 exec_shell 被加进了 directToolOrder（buildBasePrompt 点名了它，直出才对得上），
// 于是这里的期望值从 router 变成 direct——防线只增强不减弱：router 是"模型要先发现"，
// direct 是"模型直接看得见"，两者都远好于曾经事故里的 internal。

// 默认曝光绝不能把任何内置工具判成 internal
func TestDefaultExposureNeverHidesBuiltins(t *testing.T) {
	for _, src := range defaultSources() {
		if got := DefaultExposure(src.Kind, src.Name); got == ExposureInternal {
			t.Fatalf("内置工具 %s 的默认曝光不应是 internal——那会让它对模型彻底消失", src.Name)
		}
	}
	if got := DefaultExposure(SourceBuiltin, toolExecShell); got != ExposureDirect {
		t.Fatalf("exec_shell 默认应直出（buildBasePrompt 点名了它），实际 %q", got)
	}
	if got := DefaultExposure(SourceCLI, "whatever"); got != ExposureRouter {
		t.Fatalf("非内置来源默认应经路由器，实际 %q", got)
	}
}

// 全新安装（配置里没有 exposure 字段）时，运行期必须按 DefaultExposure 推断：
// 文件工具与 exec_shell 都直出。此前运行期对空值一律降级成 router，
// 结果连文件工具也退回了"要先经路由器发现"。
func TestFreshInstallExposureMatchesDefaults(t *testing.T) {
	tm, _ := newTestToolManager(t) // 无配置文件 → defaultSources()
	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})

	direct := map[string]bool{}
	for _, n := range view.direct {
		direct[n] = true
	}
	if !direct[toolReadFile] {
		t.Fatalf("全新安装时 read_file 应直出，实际直出名录: %v", view.direct)
	}
	if !direct[toolExecShell] {
		t.Fatal("exec_shell 应直出（buildBasePrompt 点名了它）")
	}

	// 关键回归点：exec_shell 必须能被模型发现
	out, err := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatalf("list 失败: %v", err)
	}
	if !strings.Contains(out, "exec_shell") {
		t.Fatalf("exec_shell 应可经 tool_router 发现: %s", out)
	}
	if _, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "describe", "tool_name": "exec_shell",
	}); err != nil {
		t.Fatalf("exec_shell 应可 describe: %v", err)
	}
}

// 修复函数的作用域：只碰「内置 且 internal」的条目
func TestRepairBuiltinExposureScope(t *testing.T) {
	sources := []*ToolSource{
		{ID: "exec_shell", Name: "exec_shell", Kind: SourceBuiltin, Exposure: ExposureInternal},
		{ID: "read_file", Name: "read_file", Kind: SourceBuiltin, Exposure: ExposureRouter},
		{ID: "hidden_cli", Name: "hidden_cli", Kind: SourceCLI, Exposure: ExposureInternal},
		{ID: "nil_src"},
	}
	sources = append(sources, nil)

	fixed := repairBuiltinExposure(sources)
	if len(fixed) != 1 || fixed[0] != "exec_shell" {
		t.Fatalf("只应修复「内置 + internal」的条目，实际修复: %v", fixed)
	}
	if sources[0].Exposure != ExposureDirect {
		t.Fatalf("exec_shell 应被修复为直出，实际 %q", sources[0].Exposure)
	}
	if sources[1].Exposure != ExposureRouter {
		t.Fatal("已配好的内置条目不应被改动")
	}
	if sources[2].Exposure != ExposureInternal {
		t.Fatal("非内置来源的 internal 是用户显式配置，必须保留")
	}
}

// 存量 v2 配置：加载时修复被误判的曝光值、落盘为当前版本，且模型能重新发现 exec_shell
func TestRepairHiddenBuiltinOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	v2 := `{
  "version": 2,
  "sources": [
    {"id":"exec_shell","name":"exec_shell","kind":"builtin","enabled":true,"builtin":true,"exposure":"internal"}
  ]
}`
	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewToolStore(path)

	src, ok := store.GetByName("exec_shell")
	if !ok {
		t.Fatal("加载后应存在 exec_shell")
	}
	if src.Exposure != ExposureDirect {
		t.Fatalf("被误判为 internal 的 exec_shell 应修复为直出，实际 %q", src.Exposure)
	}

	// 文件应已被重写为当前版本
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f ToolFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("重写后的配置应可解析: %v", err)
	}
	if f.Version != ToolFileVersion {
		t.Fatalf("落盘版本应为 %d，实际 %d", ToolFileVersion, f.Version)
	}

	// 模型要能重新发现它
	tm := NewToolManager(store, nil)
	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})
	out, err := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatalf("list 失败: %v", err)
	}
	if !strings.Contains(out, "exec_shell") {
		t.Fatalf("修复后 exec_shell 应能被模型发现: %s", out)
	}
}
