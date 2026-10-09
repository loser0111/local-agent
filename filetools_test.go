package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/media"
	"wails-tmp/internal/tool"
)

// schemaRequired 从工具定义的原始 JSON Schema 里取 required。
// 参数 schema 现在是 json.RawMessage 直通（不再是有 Required 字段的结构体）。
// （本副本服务于本文件里需要根包装配的用例；纯工具侧的副本在 internal/tool。）
func schemaRequired(t *testing.T, params json.RawMessage) []string {
	t.Helper()
	var s struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(params, &s); err != nil {
		t.Fatalf("解析参数 schema 失败: %v（raw=%s）", err, string(params))
	}
	return s.Required
}

// writeTestFile 也留一份在根包：其它根包测试（testrepo_test.go 等）会用。
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ===== 装配与直出 =====

func TestFileToolsAssembledAndDirectExposed(t *testing.T) {
	tm, dir := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		SessionID:  "s1",
		Enforcer:   tool.AllowAllEnforcer{},
		// read_image 也是一样：它要在能存图时才注册（没有附件存储就用不了），
		// 不给这一项，下面的"directToolOrder 全员可见"断言就会因为"没装上"而失败，
		// 掩盖掉真正要守的东西。
		Attachments: media.NewAttachmentStore(t.TempDir()),
		// 给一个 spawner：directToolOrder 里有 spawn_agent，而没有 spawner 时它按设计
		// 根本不注册（子代理正是靠这个不注册来防止再派生）。此处传桩只为让它存在，
		// 下面的断言才能覆盖完整名录——否则失败原因是"工具没装上"，而不是"没直出"。
		SpawnAgent: func(context.Context, string, int) (*agent.SubagentResult, error) {
			return &agent.SubagentResult{}, nil
		},
	})

	// 直出名录里的工具 + tool_router 都必须真的出现在发给模型的定义里。
	// 这是"直出就一定要让模型看得见"的守卫：任何新增进 directToolOrder 的工具，
	// 只要装配成功，就不能对模型隐形（历史事故：exec_shell 曾整个消失）。
	defs := view.GetToolsForLLM()
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Function.Name] = true
	}
	for _, want := range append([]string{"tool_router"}, tool.DirectToolOrder...) {
		if !names[want] {
			t.Errorf("工具定义应包含 %s，实际 %v", want, names)
		}
	}
	// 直出定义要有必填参数信息（否则模型不知道哪些参数必填）
	for _, d := range defs {
		if d.Function.Name == tool.ToolWriteFile {
			req := map[string]bool{}
			for _, r := range schemaRequired(t, d.Function.Parameters) {
				req[r] = true
			}
			if !req["path"] || !req["content"] {
				t.Errorf("write_file 的必填参数应包含 path 与 content，实际 %v", schemaRequired(t, d.Function.Parameters))
			}
		}
	}

	// 直调与经路由器两条路径都能真正执行
	out, err := view.ExecuteTool(tool.ToolListDir, map[string]interface{}{})
	if err != nil {
		t.Fatalf("直调 list_dir 失败: %v", err)
	}
	if !strings.Contains(out, dir) && !strings.Contains(out, "个文件") {
		t.Fatalf("list_dir 输出异常: %s", out)
	}
	if _, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "execute", "tool_name": tool.ToolGlob, "arguments": map[string]interface{}{"pattern": "*.txt"},
	}); err != nil {
		t.Fatalf("经 tool_router 调 glob 失败: %v", err)
	}
}

// 直出给模型的工具，其必填参数必须真的出现在 schema 里。
//
// 这道防线针对的是一类静默缺陷：装配期会给每个工具包一层 guardedTool，而 schemaForTool
// 是对装饰后的对象做接口断言（RequiredParams / SchemaProvider）。装饰器少转发一个方法，
// 断言就失败，required 静默变空——模型看不到哪个参数必填，只能从描述里猜，猜错就吃一个
// 参数校验错误、白烧一轮。exec_shell 还额外绕过一层：它的 cmd 必填来自来源配置的
// Required 标记，必须经 requiredFromConfig 单独传导，光有 paramsFromConfig 会丢掉。
// 因此这里逐个钉死期望值：新增直出工具若忘了实现 RequiredParams()，本测试立刻失败。
func TestDirectToolsCarryRequiredParams(t *testing.T) {
	tm, dir := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		SessionID:  "s1",
		Enforcer:   tool.AllowAllEnforcer{},
		SpawnAgent: func(context.Context, string, int) (*agent.SubagentResult, error) {
			return &agent.SubagentResult{}, nil
		},
	})

	want := map[string][]string{
		tool.ToolReadFile:  {"path"},
		tool.ToolWriteFile: {"path", "content"},
		tool.ToolEditFile:  {"path", "old_string", "new_string"},
		tool.ToolGlob:      {"pattern"},
		tool.ToolGrep:      {"pattern"},
		toolAskUser:        {"questions"},
		tool.ToolExecShell: {"cmd"},
		toolMemorySearch:   {"query"},
		toolMemorySave:     {"content"},
		toolMemoryForget:   {"target"},
	}
	got := map[string][]string{}
	for _, d := range view.GetToolsForLLM() {
		got[d.Function.Name] = schemaRequired(t, d.Function.Parameters)
	}
	for name, exp := range want {
		req, ok := got[name]
		if !ok {
			t.Errorf("%s 应直出给模型，实际工具列表 %v", name, keysOf(got))
			continue
		}
		if !sameStrings(req, exp) {
			t.Errorf("%s 的 required 应为 %v，实际 %v（装饰器必须透传 tool.RequiredParams）", name, exp, req)
		}
	}
}

// sameStrings 比较两个字符串集合（忽略顺序）
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// 文件工具也必须过权限网关（两条路径都不能绕过）
func TestFileToolsAreGuarded(t *testing.T) {
	tm, dir := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{ProjectDir: dir, Enforcer: tool.DenyAllEnforcer{}})

	if _, err := view.ExecuteTool(tool.ToolReadFile, map[string]interface{}{"path": "a.txt"}); err == nil {
		t.Fatal("直调 read_file 必须经过权限网关")
	}
	if _, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "execute", "tool_name": tool.ToolWriteFile,
		"arguments": map[string]interface{}{"path": "a.txt", "content": "x"},
	}); err == nil {
		t.Fatal("经 tool_router 调 write_file 必须经过权限网关")
	}
	// 发现工具仍不应被拦
	if _, err := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"}); err != nil {
		t.Fatalf("发现工具不应被拦截: %v", err)
	}
}

// ===== 内置工具配置的补齐 =====

func TestToolStoreEnsureBuiltins(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "tools.json")

	// 模拟老版本：只有 exec_shell
	writeTestFile(t, filePath, `[{"id":"exec_shell","name":"exec_shell","type":"builtin","enabled":true,"builtin":true}]`)

	toolStore := tool.NewToolStore(filePath)
	names := map[string]bool{}
	for _, c := range toolStore.GetAll() {
		names[c.Name] = true
	}
	for _, want := range append([]string{"exec_shell"}, tool.DirectToolOrder...) {
		if !names[want] {
			t.Errorf("补齐后应包含 %s，实际 %v", want, names)
		}
	}
	// 已存在的配置不应被重复追加
	if len(toolStore.GetAll()) != len(tool.DefaultSources()) {
		t.Fatalf("工具数应为 %d，实际 %d", len(tool.DefaultSources()), len(toolStore.GetAll()))
	}
	// 补齐结果应落盘（再次加载不再变化）
	again := tool.NewToolStore(filePath)
	if len(again.GetAll()) != len(toolStore.GetAll()) {
		t.Fatal("补齐结果应持久化")
	}
}
