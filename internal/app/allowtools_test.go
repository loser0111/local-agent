package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"wails-tmp/internal/tool"
)

// ===== allowed-tools 接入执行链路（#4）=====
//
// 这一层测的是**装配结果**：声明之后工具视图有没有真的被收窄、直出与经 tool_router
// 两条路径是否都被挡住。匹配语义本身（大小写/通配符/分隔符/兜底）在
// internal/tool/allowlist_test.go 里单独覆盖，这里只挑几种声明形态各验一遍接线。

// viewNames 装配后直出给模型的工具名集合
func viewNames(view *tool.SessionView) map[string]bool {
	names := map[string]bool{}
	for _, d := range view.GetToolsForLLM() {
		names[d.Function.Name] = true
	}
	return names
}

func buildViewWithAllowed(t *testing.T, tm *ToolManager, dir string, allowed []string) *tool.SessionView {
	t.Helper()
	return tm.BuildView(context.Background(), BuildOptions{
		ProjectDir:   dir,
		Enforcer:     tool.AllowAllEnforcer{},
		AllowedTools: allowed,
	})
}

// 默认策略：不声明 allowed-tools 时**不额外收窄**（改动前的行为，必须保持不变）
func TestAllowedToolsDefaultDoesNotNarrow(t *testing.T) {
	tm, dir := newTestToolManager(t)
	names := viewNames(buildViewWithAllowed(t, tm, dir, nil))

	for _, want := range []string{tool.ToolReadFile, tool.ToolWriteFile, tool.ToolExecShell} {
		if !names[want] {
			t.Errorf("未声明 allowed-tools 时 %s 应照常可用，实际工具列表 %v", want, names)
		}
	}
	// 空白项等同未声明：不该把技能锁死
	names = viewNames(buildViewWithAllowed(t, tm, dir, []string{"", "  "}))
	if !names[tool.ToolReadFile] {
		t.Errorf("allowed-tools 全是空白项时应视为未声明，实际 %v", names)
	}
}

// 显式声明：工具视图收窄到声明范围
func TestAllowedToolsNarrowsView(t *testing.T) {
	tm, dir := newTestToolManager(t)
	view := buildViewWithAllowed(t, tm, dir, []string{"read_file"})
	names := viewNames(view)

	if !names[tool.ToolReadFile] {
		t.Fatalf("read_file 已声明，应保留，实际 %v", names)
	}
	for _, gone := range []string{tool.ToolWriteFile, tool.ToolEditFile, tool.ToolExecShell, tool.ToolGrep} {
		if names[gone] {
			t.Errorf("%s 未声明，不应出现在工具列表里，实际 %v", gone, names)
		}
	}
}

// 拦截：经 tool_router 也不能绕到被收窄掉的工具（直出与路由器是同一份 registry）
func TestAllowedToolsBlocksViaRouter(t *testing.T) {
	tm, dir := newTestToolManager(t)
	view := buildViewWithAllowed(t, tm, dir, []string{"read_file"})

	// 1) 路由器 list 里看不到 write_file
	out, err := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatalf("tool_router list 失败: %v", err)
	}
	if strings.Contains(out, tool.ToolWriteFile) {
		t.Errorf("被收窄的 write_file 不该出现在路由器列表里:\n%s", out)
	}
	if !strings.Contains(out, tool.ToolReadFile) {
		t.Errorf("已声明的 read_file 应出现在路由器列表里:\n%s", out)
	}

	// 2) describe 被收窄的工具应报"未找到"
	if _, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "describe", "tool_name": tool.ToolWriteFile,
	}); err == nil {
		t.Error("describe 被收窄的工具应失败")
	}

	// 3) execute 被收窄的工具应失败，且不产生副作用
	target := dir + "/should_not_exist.txt"
	_, err = view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "execute", "tool_name": tool.ToolWriteFile,
		"arguments": map[string]interface{}{"path": target, "content": "boom"},
	})
	if err == nil {
		t.Error("经 tool_router 执行被收窄的 write_file 应失败")
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Errorf("被拦下的调用不应产生副作用，但 %s 被创建了", target)
	}

	// 4) 直调同样被挡住（Lookup 拿不到）
	if view.Lookup(tool.ToolWriteFile) != nil {
		t.Error("被收窄的工具不应还能直调")
	}
	if _, err := view.ExecuteTool(tool.ToolWriteFile, map[string]interface{}{
		"path": target, "content": "boom",
	}); err == nil {
		t.Error("直调被收窄的 write_file 应失败")
	}
}

// 声明形态：大小写、分隔符、通配符都要能命中
func TestAllowedToolsDeclarationForms(t *testing.T) {
	tm, dir := newTestToolManager(t)
	for _, pat := range []string{"READ_FILE", "read-file", "read*", "*"} {
		names := viewNames(buildViewWithAllowed(t, tm, dir, []string{pat}))
		if !names[tool.ToolReadFile] {
			t.Errorf("声明 %q 应命中 read_file，实际 %v", pat, names)
		}
	}
}

// 兜底：声明全是本仓没有的名字（外部生态技能常写 Read/Bash/Write）时放弃收窄，
// 而不是把技能锁死到零工具——那会让功能直接不可用，比"没约束"更糟。
func TestAllowedToolsUnknownNamesFallBackToNoNarrowing(t *testing.T) {
	tm, dir := newTestToolManager(t)
	names := viewNames(buildViewWithAllowed(t, tm, dir, []string{"Read", "Bash", "Write"}))

	if !names[tool.ToolReadFile] || !names[tool.ToolExecShell] {
		t.Fatalf("声明全未命中时应放弃收窄（退回默认），实际 %v", names)
	}
}

// 与会话白名单取交集：两边都放行才留下
func TestAllowedToolsIntersectsSessionWhitelist(t *testing.T) {
	tm, dir := newTestToolManager(t)

	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir:   dir,
		Enforcer:     tool.AllowAllEnforcer{},
		EnabledTools: []string{tool.ToolReadFile, tool.ToolWriteFile, tool.ToolExecShell},
		AllowedTools: []string{tool.ToolReadFile, tool.ToolGrep}, // grep 不在会话白名单里
	})
	names := viewNames(view)

	if !names[tool.ToolReadFile] {
		t.Errorf("交集应保留 read_file，实际 %v", names)
	}
	if names[tool.ToolGrep] {
		t.Errorf("grep 不在会话白名单里，不该因 allowed-tools 声明而出现，实际 %v", names)
	}
	if names[tool.ToolWriteFile] || names[tool.ToolExecShell] {
		t.Errorf("仅会话白名单放行、allowed-tools 未声明的工具应被收窄，实际 %v", names)
	}
}
