package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-tmp/internal/skill"
	"wails-tmp/internal/tool"
)

// writeFileAt 在技能目录内按相对路径写文件（用于造 L3 资源）
func writeFileAt(t *testing.T, store *skill.SkillStore, id, rel, content string) {
	t.Helper()
	full := filepath.Join(store.Dir(), id, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ===== L2 / L3 工具 =====
//
// 本文件只留「技能工具经 ToolManager 装配」的集成用例：它们要 main 的
// NewToolManager / BuildOptions，因此不能随 internal/skill 走。
// 技能自身的 frontmatter 解析、校验、命令解析与安装器用例已迁到
// internal/skill/skill_test.go。

// read_skill 与 read_skill_file 经 ToolManager 注册，并受白名单约束
func TestSkillToolsEndToEnd(t *testing.T) {
	ss := skill.NewSkillStore(filepath.Join(t.TempDir(), "skills"))
	if err := ss.SaveSkill("alpha", skill.SkillDraft{Description: "阿尔法", UserInvocable: true, Body: "# 阿尔法正文\n"}); err != nil {
		t.Fatal(err)
	}
	if err := ss.SaveSkill("beta", skill.SkillDraft{Description: "贝塔", UserInvocable: true, Body: "# 贝塔正文\n"}); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, ss, "alpha", "references/guide.md", "参考资料内容")
	ss.Refresh()

	store := tool.NewToolStore(filepath.Join(t.TempDir(), "tools.json"))
	tm := NewToolManager(store, ss)

	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: tool.AllowAllEnforcer{}})
	list, err := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "read_skill") || !strings.Contains(list, "read_skill_file") {
		t.Fatalf("工具清单缺少技能工具: %s", list)
	}

	// L2：正文 + 资源清单
	out, err := view.ExecuteTool("read_skill", map[string]interface{}{"id": "alpha"})
	if err != nil {
		t.Fatalf("读取技能失败: %v", err)
	}
	if !strings.Contains(out, "阿尔法正文") || !strings.Contains(out, "references/guide.md") {
		t.Fatalf("read_skill 返回缺内容: %s", out)
	}

	// L3：读资源
	res, err := view.ExecuteTool("read_skill_file", map[string]interface{}{"id": "alpha", "path": "references/guide.md"})
	if err != nil {
		t.Fatalf("读取技能资源失败: %v", err)
	}
	if !strings.Contains(res, "参考资料内容") {
		t.Fatalf("资源内容错误: %s", res)
	}

	// 白名单只含 beta：alpha 的两个工具都不可用
	view2 := tm.BuildView(context.Background(), BuildOptions{EnabledSkills: []string{"beta"}, Enforcer: tool.AllowAllEnforcer{}})
	if _, err := view2.ExecuteTool("read_skill", map[string]interface{}{"id": "alpha"}); err == nil {
		t.Fatal("白名单外的技能不应可读")
	}
	if _, err := view2.ExecuteTool("read_skill_file", map[string]interface{}{"id": "alpha", "path": "references/guide.md"}); err == nil {
		t.Fatal("白名单外的技能资源不应可读")
	}
	if _, err := view2.ExecuteTool("read_skill", map[string]interface{}{"id": "beta"}); err != nil {
		t.Fatalf("白名单内技能应可读: %v", err)
	}

	// 全部停用：两个工具都不注册
	if err := ss.SetEnabled("alpha", false); err != nil {
		t.Fatal(err)
	}
	if err := ss.SetEnabled("beta", false); err != nil {
		t.Fatal(err)
	}
	view3 := tm.BuildView(context.Background(), BuildOptions{Enforcer: tool.AllowAllEnforcer{}})
	list3, _ := view3.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if strings.Contains(list3, "read_skill") || strings.Contains(list3, "read_skill_file") {
		t.Fatalf("无可用技能时不应注册技能工具: %s", list3)
	}
}

// read_skill 对 disable-model-invocation 的技能不可读，但 read_skill_file 仍可读
func TestModelDisabledSkillSplit(t *testing.T) {
	ss := skill.NewSkillStore(filepath.Join(t.TempDir(), "skills"))
	if err := ss.SaveSkill("manual", skill.SkillDraft{
		Description:            "仅手动",
		DisableModelInvocation: true,
		UserInvocable:          true,
		Body:                   "正文",
	}); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, ss, "manual", "references/x.md", "内容")
	ss.Refresh()

	store := tool.NewToolStore(filepath.Join(t.TempDir(), "tools.json"))
	tm := NewToolManager(store, ss)
	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: tool.AllowAllEnforcer{}})

	if _, err := view.ExecuteTool("read_skill", map[string]interface{}{"id": "manual"}); err == nil {
		t.Fatal("disable-model-invocation 的技能不应能经 read_skill 读取")
	}
	// 用户显式调用后，正文里引用的资料仍要读得到
	if _, err := view.ExecuteTool("read_skill_file", map[string]interface{}{"id": "manual", "path": "references/x.md"}); err != nil {
		t.Fatalf("read_skill_file 不应受 disable-model-invocation 限制: %v", err)
	}
}
