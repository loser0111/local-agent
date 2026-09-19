package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== MCP 导入 / 导出 + 落盘迁移 =====

func newMCPTestApp(t *testing.T) *App {
	t.Helper()
	store := NewToolStore(filepath.Join(t.TempDir(), "tools.json"))
	return &App{toolStore: store, toolManager: NewToolManager(store, nil)}
}

func findSource(t *testing.T, app *App, name string) *ToolSource {
	t.Helper()
	for _, s := range app.toolStore.GetAll() {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// 官方标准片段：粘贴即用，落盘后同为官方字段（type=streamable-http，不再是内部 transport）
func TestImportMCPServersOfficialShape(t *testing.T) {
	app := newMCPTestApp(t)
	const snippet = `{
  "mcpServers": {
    "qconfig": {
      "type": "streamable-http",
      "url": "https://qconfig-mcp-server-function.faas.ctripcorp.com/mcp",
      "headers": {"x-bbzai-mcp-token": "tok-123"}
    }
  }
}`

	res, err := app.ImportMCPServers(snippet)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(res.Imported) != 1 || res.Imported[0] != "qconfig" || len(res.Skipped) != 0 {
		t.Fatalf("导入结果异常: %+v", res)
	}

	src := findSource(t, app, "qconfig")
	if src == nil {
		t.Fatal("来源未落库")
	}
	if src.Kind != SourceMCP || !src.Enabled || src.Icon != "blocks" {
		t.Fatalf("来源字段异常: %+v", src)
	}
	if src.MCP == nil {
		t.Fatal("MCP 配置缺失")
	}
	// 存储即官方形状：导入不需要做取值翻译
	if src.MCP.Type != "streamable-http" {
		t.Errorf("type 应保持官方取值 streamable-http，实际 %q", src.MCP.Type)
	}
	if src.MCP.URL != "https://qconfig-mcp-server-function.faas.ctripcorp.com/mcp" {
		t.Errorf("url 未正确带入: %q", src.MCP.URL)
	}
	if src.MCP.Headers["x-bbzai-mcp-token"] != "tok-123" {
		t.Errorf("headers 未正确带入: %+v", src.MCP.Headers)
	}
}

// 三种输入形态 + 代码围栏包裹
func TestImportMCPServersForms(t *testing.T) {
	cases := []struct {
		label    string
		snippet  string
		wantName string
		wantType string
	}{
		{
			label:    "去掉外层包装的字典",
			snippet:  `{"mcp-a": {"type": "sse", "url": "https://a.example.com/sse"}}`,
			wantName: "mcp-a", wantType: "sse",
		},
		{
			label:    "单个服务器对象（带 name）",
			snippet:  `{"name": "mcp-b", "url": "https://b.example.com/mcp", "type": "http"}`,
			wantName: "mcp-b", wantType: "streamable-http", // http 归一为官方写法
		},
		{
			label:    "单个服务器对象（无 name，从 url 推断）",
			snippet:  `{"url": "https://qconfig-mcp-server-function.faas.ctripcorp.com/mcp", "type": "streamable-http"}`,
			wantName: "qconfig-mcp-server-function", wantType: "streamable-http",
		},
		{
			label:    "stdio（不写 type，靠 command 判断）",
			snippet:  `{"mcp-c": {"command": "npx", "args": ["-y", "some-mcp"], "env": {"K": "V"}}}`,
			wantName: "mcp-c", wantType: "stdio",
		},
		{
			label:    "代码围栏包裹",
			snippet:  "```json\n{\"mcp-d\": {\"url\": \"https://d.example.com/mcp\"}}\n```",
			wantName: "mcp-d", wantType: "streamable-http",
		},
	}

	for _, c := range cases {
		app := newMCPTestApp(t)
		res, err := app.ImportMCPServers(c.snippet)
		if err != nil {
			t.Errorf("%s：导入失败 %v", c.label, err)
			continue
		}
		if len(res.Imported) != 1 || res.Imported[0] != c.wantName {
			t.Errorf("%s：应导入 %s，实际 %+v", c.label, c.wantName, res)
			continue
		}
		src := findSource(t, app, c.wantName)
		if src == nil || src.MCP == nil {
			t.Errorf("%s：未落库", c.label)
			continue
		}
		if src.MCP.Type != c.wantType {
			t.Errorf("%s：type 应为 %s，实际 %s", c.label, c.wantType, src.MCP.Type)
		}
		if c.wantType == "stdio" && (src.MCP.Command != "npx" || len(src.MCP.Args) != 2 || src.MCP.Env["K"] != "V") {
			t.Errorf("%s：stdio 参数未正确带入 %+v", c.label, src.MCP)
		}
	}
}

// 非法/不完整的条目要如实报告原因，且不阻断其它条目
func TestImportMCPServersSkipsWithReasons(t *testing.T) {
	app := newMCPTestApp(t)
	snippet := `{"mcpServers": {
	  "good": {"url": "https://good.example.com/mcp"},
	  "bad-type": {"type": "websocket", "url": "https://x.example.com"},
	  "no-url": {"type": "streamable-http"},
	  "no-command": {"type": "stdio"}
	}}`

	res, err := app.ImportMCPServers(snippet)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(res.Imported) != 1 || res.Imported[0] != "good" {
		t.Fatalf("应只导入 good，实际 %+v", res.Imported)
	}
	if len(res.Skipped) != 3 {
		t.Fatalf("应跳过 3 条，实际 %+v", res.Skipped)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.Name] = s.Reason
	}
	if !strings.Contains(reasons["bad-type"], "不支持的 type") {
		t.Errorf("bad-type 的原因应指出 type 不支持，实际 %q", reasons["bad-type"])
	}
	if !strings.Contains(reasons["no-url"], "缺少 url") {
		t.Errorf("no-url 的原因应指出缺 url，实际 %q", reasons["no-url"])
	}
	if !strings.Contains(reasons["no-command"], "缺少 command") {
		t.Errorf("no-command 的原因应指出缺 command，实际 %q", reasons["no-command"])
	}

	for _, bad := range []string{"", "not json at all", "[]", `{"mcpServers": {}}`} {
		if _, err := app.ImportMCPServers(bad); err == nil {
			t.Errorf("输入 %q 应报错", bad)
		}
	}
}

// 同名重复导入 = 更新（幂等），不会堆出两条来源
func TestImportMCPServersIsIdempotent(t *testing.T) {
	app := newMCPTestApp(t)
	if _, err := app.ImportMCPServers(`{"mcpServers": {"qconfig": {"url": "https://a.example.com/mcp"}}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ImportMCPServers(`{"mcpServers": {"qconfig": {"url": "https://b.example.com/mcp"}}}`); err != nil {
		t.Fatal(err)
	}

	count := 0
	for _, s := range app.toolStore.GetAll() {
		if s.Name == "qconfig" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("重复导入同一名字应更新而不是新增，实际 %d 条", count)
	}
	if got := findSource(t, app, "qconfig"); got.MCP.URL != "https://b.example.com/mcp" {
		t.Errorf("重复导入应以最后一次为准，实际 %q", got.MCP.URL)
	}
}

// 导出 → 再导入：往返一致
func TestExportMCPServersRoundTrip(t *testing.T) {
	from := newMCPTestApp(t)
	if _, err := from.ImportMCPServers(`{"mcpServers": {
	  "qconfig": {"url": "https://q.example.com/mcp", "type": "streamable-http", "headers": {"t": "1"}},
	  "local-tools": {"command": "npx", "args": ["-y", "x"], "env": {"A": "B"}}
	}}`); err != nil {
		t.Fatal(err)
	}

	exported, err := from.ExportMCPServers(nil)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if !strings.Contains(exported, "mcpServers") || !strings.Contains(exported, "streamable-http") {
		t.Fatalf("导出应是官方形状（streamable-http），实际:\n%s", exported)
	}

	dst := newMCPTestApp(t)
	res, err := dst.ImportMCPServers(exported)
	if err != nil {
		t.Fatalf("导入导出结果失败: %v", err)
	}
	if len(res.Imported) != 2 || len(res.Skipped) != 0 {
		t.Fatalf("往返应导入 2 条、跳过 0 条，实际 %+v", res)
	}
	got := findSource(t, dst, "qconfig")
	if got.MCP.URL != "https://q.example.com/mcp" || got.MCP.Headers["t"] != "1" {
		t.Fatalf("往返后 HTTP 配置不一致: %+v", got.MCP)
	}
	got2 := findSource(t, dst, "local-tools")
	if got2.MCP.Command != "npx" || got2.MCP.Env["A"] != "B" {
		t.Fatalf("往返后 stdio 配置不一致: %+v", got2.MCP)
	}
}

// 按名字导出：支持来源名与工具名（mcp__server__tool）
func TestExportMCPServersByName(t *testing.T) {
	app := newMCPTestApp(t)
	if _, err := app.ImportMCPServers(`{"mcpServers": {
	  "alpha": {"url": "https://a.example.com/mcp"},
	  "beta": {"url": "https://b.example.com/mcp"}
	}}`); err != nil {
		t.Fatal(err)
	}

	out, err := app.ExportMCPServers([]string{"mcp__alpha__some_tool"})
	if err != nil {
		t.Fatalf("按工具名导出应可用: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("应只导出 alpha，实际:\n%s", out)
	}
	if _, err := app.ExportMCPServers([]string{"not-exist"}); err == nil {
		t.Fatal("导出不存在的名字应报错")
	}
	empty := newMCPTestApp(t)
	if _, err := empty.ExportMCPServers(nil); err == nil {
		t.Fatal("没有 MCP 配置时应报错")
	}
}

func TestDeriveAndSanitizeMCPName(t *testing.T) {
	if got := deriveMCPServerName(officialMCPServer{URL: "https://qconfig-mcp-server-function.faas.ctripcorp.com/mcp"}); got != "qconfig-mcp-server-function" {
		t.Errorf("应从 url 推断出服务器名，实际 %q", got)
	}
	if got := deriveMCPServerName(officialMCPServer{Command: "/usr/local/bin/my-mcp-server"}); got != "my-mcp-server" {
		t.Errorf("应从 command 推断出服务器名，实际 %q", got)
	}
	if got := sanitizeToolName("my mcp/server@1.0"); got != "my-mcp-server-1-0" {
		t.Errorf("非法字符应替换为连字符，实际 %q", got)
	}
	if got := normalizeMCPName("mcp__qconfig__changeqconfig"); got != "qconfig" {
		t.Errorf("工具名应归一为来源名，实际 %q", got)
	}
}

// ===== 落盘：v1 → v2 迁移（带备份）=====

func TestToolStoreMigratesV1File(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tools.json")
	// v1：裸数组 + type=api / mcp(transport=http) + 一个内置
	legacy := `[
	  {"id":"exec_shell","name":"exec_shell","label":"执行终端命令","description":"d","type":"builtin","icon":"terminal","enabled":true,"builtin":true},
	  {"id":"tool_weather","name":"weather","label":"天气","description":"查天气","type":"api","icon":"cloud","enabled":true,
	   "config":{"method":"GET","url":"https://x/weather","timeout":10}},
	  {"id":"qconfig","name":"qconfig","label":"QConfig","description":"d","type":"mcp","icon":"blocks","enabled":true,
	   "config":{"transport":"http","url":"https://q/mcp","headers":{"t":"1"}},
	   "disabledTools":["get_config_raw"],
	   "discovered":[{"name":"list_envs","description":"列环境"},{"name":"get_config_raw","description":"读配置"}]}
	]`
	if err := os.WriteFile(file, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewToolStore(file)

	// 内置照旧
	if _, ok := store.GetByName("exec_shell"); !ok {
		t.Fatal("内置来源应在迁移后保留")
	}
	// api → http，且配置进类型化字段
	weather, ok := store.GetByName("weather")
	if !ok || weather.Kind != SourceHTTP || weather.HTTP == nil {
		t.Fatalf("api 应迁移为 http 来源: %+v", weather)
	}
	if weather.HTTP.URL != "https://x/weather" || weather.HTTP.Method != "GET" {
		t.Fatalf("HTTP 配置迁移错误: %+v", weather.HTTP)
	}
	// mcp：transport=http → 官方 type=streamable-http；禁用名单与发现缓存保留
	qc, ok := store.GetByName("qconfig")
	if !ok || qc.Kind != SourceMCP || qc.MCP == nil {
		t.Fatalf("mcp 应迁移为 MCP 来源: %+v", qc)
	}
	if qc.MCP.Type != "streamable-http" || qc.MCP.URL != "https://q/mcp" || qc.MCP.Headers["t"] != "1" {
		t.Fatalf("MCP 配置迁移错误: %+v", qc.MCP)
	}
	if len(qc.Discovered) != 2 || len(qc.DisabledTools) != 1 {
		t.Fatalf("子工具状态应保留: %+v", qc)
	}
	if got := qc.AvailableSubTools(); len(got) != 1 || got[0].Name != "list_envs" {
		t.Fatalf("可用子工具应剔除禁用项，实际 %+v", got)
	}

	// 落盘升级为 v2
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var f ToolFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("迁移后应写成 v2 结构: %v", err)
	}
	if f.Version != ToolFileVersion {
		t.Fatalf("版本号应为 %d，实际 %d", ToolFileVersion, f.Version)
	}

	// 旧文件必须留备份（配置是用户资产，迁移要可回退）
	bak, err := os.ReadFile(file + ".bak")
	if err != nil {
		t.Fatalf("应保留旧文件备份: %v", err)
	}
	if !strings.Contains(string(bak), `"transport":"http"`) {
		t.Fatalf("备份应是迁移前的原始内容，实际:\n%s", string(bak))
	}

	// 再次加载不应重复迁移（备份不被覆盖）
	before, _ := os.Stat(file + ".bak")
	_ = NewToolStore(file)
	after, _ := os.Stat(file + ".bak")
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("重复加载不应覆盖已有备份")
	}
}

// ===== 子工具开关与暴露策略 =====

func TestSubToolToggleAndExposure(t *testing.T) {
	app := newMCPTestApp(t)
	if _, err := app.ImportMCPServers(`{"mcpServers": {"qconfig": {"url": "https://q/mcp"}}}`); err != nil {
		t.Fatal(err)
	}
	src := findSource(t, app, "qconfig")
	if err := app.toolStore.UpdateDiscovered(src.ID, []MCPToolMeta{
		{Name: "list_envs"}, {Name: "changeqconfig"},
	}); err != nil {
		t.Fatal(err)
	}

	// 停用其中一个子工具
	if err := app.SetSubToolEnabled(src.ID, "changeqconfig", false); err != nil {
		t.Fatalf("停用子工具失败: %v", err)
	}
	got := findSource(t, app, "qconfig")
	if avail := got.AvailableSubTools(); len(avail) != 1 || avail[0].Name != "list_envs" {
		t.Fatalf("停用后可用子工具应只剩 list_envs，实际 %+v", avail)
	}

	// 重复停用应幂等（不产生重复项）
	if err := app.SetSubToolEnabled(src.ID, "changeqconfig", false); err != nil {
		t.Fatal(err)
	}
	if n := len(findSource(t, app, "qconfig").DisabledTools); n != 1 {
		t.Fatalf("重复停用不应堆出重复项，实际 %d", n)
	}

	// 重新启用
	if err := app.SetSubToolEnabled(src.ID, "changeqconfig", true); err != nil {
		t.Fatal(err)
	}
	if n := len(findSource(t, app, "qconfig").DisabledTools); n != 0 {
		t.Fatalf("启用后禁用名单应为空，实际 %d", n)
	}

	// 暴露策略：默认 router；可改为 direct / internal；非法值拒绝
	if got.Exposure != ExposureRouter {
		t.Fatalf("MCP 默认暴露策略应为 router，实际 %s", got.Exposure)
	}
	if err := app.SetExposure(src.ID, string(ExposureDirect)); err != nil {
		t.Fatalf("设置暴露策略失败: %v", err)
	}
	if got := findSource(t, app, "qconfig").Exposure; got != ExposureDirect {
		t.Fatalf("暴露策略应已更新，实际 %s", got)
	}
	if err := app.SetExposure(src.ID, "nonsense"); err == nil {
		t.Fatal("非法暴露策略应报错")
	}

	// 对非 MCP 来源调子工具开关应报错
	if err := app.SetSubToolEnabled("exec_shell", "whatever", false); err == nil {
		t.Fatal("非 MCP 来源不支持子工具开关")
	}
}
