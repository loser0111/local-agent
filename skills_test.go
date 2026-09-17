package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestSkillStore 构造临时技能目录的 SkillStore（不写示例）
func newTestSkillStore(t *testing.T) *SkillStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "skills")
	return NewSkillStore(dir)
}

func writeSkill(t *testing.T, store *SkillStore, id, content string) {
	t.Helper()
	skillDir := filepath.Join(store.Dir(), id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, skillFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	store.Refresh()
}

// T1 标准解析
func TestSkillParseStandard(t *testing.T) {
	md := "---\nname: pdf-report\ndescription: 从 CSV 生成 PDF 报告。\n---\n\n# PDF\n\n步骤 1\n"
	name, desc, body, err := parseFrontmatter(md)
	if err != nil {
		t.Fatal(err)
	}
	if name != "pdf-report" || desc != "从 CSV 生成 PDF 报告。" {
		t.Fatalf("frontmatter 解析错误: name=%q desc=%q", name, desc)
	}
	if !strings.HasPrefix(body, "# PDF") {
		t.Fatalf("正文未正确剥离 frontmatter: %q", body)
	}
}

// T2 无 frontmatter：不报错，body 为全文，扫描后 Error 提示缺 description
func TestSkillParseNoFrontmatter(t *testing.T) {
	md := "# 纯 Markdown\n\n没有 frontmatter\n"
	name, desc, body, err := parseFrontmatter(md)
	if err != nil {
		t.Fatalf("无 frontmatter 不应报错: %v", err)
	}
	if name != "" || desc != "" || body != md {
		t.Fatalf("无 frontmatter 解析错误: name=%q desc=%q body=%q", name, desc, body)
	}
	store := newTestSkillStore(t)
	writeSkill(t, store, "plain", md)
	var meta *SkillMeta
	for _, m := range store.GetAll() {
		if m.ID == "plain" {
			meta = m
		}
	}
	if meta == nil || meta.Error == "" {
		t.Fatal("缺 description 应标记 error")
	}
}

// T3 未闭合
func TestSkillParseUnclosed(t *testing.T) {
	_, _, _, err := parseFrontmatter("---\nname: x\ndescription: y\n")
	if err == nil || !strings.Contains(err.Error(), "frontmatter 未闭合") {
		t.Fatalf("应报 frontmatter 未闭合，得到: %v", err)
	}
}

// T4 引号去除
func TestSkillParseQuotedValue(t *testing.T) {
	md := "---\nname: s\ndescription: \"带引号的描述\"\n---\n\nbody\n"
	_, desc, _, err := parseFrontmatter(md)
	if err != nil {
		t.Fatal(err)
	}
	if desc != "带引号的描述" {
		t.Fatalf("引号未去除: %q", desc)
	}
}

// T5 CRLF（Windows 换行）
func TestSkillParseCRLF(t *testing.T) {
	md := "---\r\nname: win\r\ndescription: windows 换行\r\n---\r\n\r\n正文\r\n"
	name, desc, body, err := parseFrontmatter(md)
	if err != nil {
		t.Fatal(err)
	}
	if name != "win" || desc != "windows 换行" || body != "正文\n" {
		t.Fatalf("CRLF 解析错误: name=%q desc=%q body=%q", name, desc, body)
	}
}

// T6 L1 生成：禁用与错误技能被排除，描述截断生效
func TestBuildSkillIndex(t *testing.T) {
	store := newTestSkillStore(t)
	writeSkill(t, store, "a", "---\nname: 技能A\ndescription: 可用描述\n---\n\n正文\n")
	writeSkill(t, store, "b", "---\nname: 技能B\ndescription: 可用描述B\n---\n\n正文\n")
	writeSkill(t, store, "bad", "# 无 frontmatter\n")
	if err := store.SetEnabled("b", false); err != nil {
		t.Fatal(err)
	}
	all := store.GetAll()
	idx := BuildSkillIndex(all)
	if !strings.Contains(idx, "技能A") {
		t.Fatalf("清单缺少启用技能: %s", idx)
	}
	if strings.Contains(idx, "技能B") {
		t.Fatalf("清单不应包含停用技能: %s", idx)
	}
	if strings.Contains(idx, "bad") {
		t.Fatalf("清单不应包含错误技能: %s", idx)
	}
}

// T7 ID 校验
func TestValidSkillID(t *testing.T) {
	valid := []string{"abc", "a-b_c", "A1", "x-y-z"}
	invalid := []string{"", "a b", "中文", "a/b", strings.Repeat("a", 65)}
	for _, id := range valid {
		if !validSkillID(id) {
			t.Fatalf("应判定为合法: %q", id)
		}
	}
	for _, id := range invalid {
		if validSkillID(id) {
			t.Fatalf("应判定为非法: %q", id)
		}
	}
}

// T8 增删：SaveSkill 创建目录/SKILL.md，DeleteSkill 删除，列表同步
func TestSkillSaveDelete(t *testing.T) {
	store := newTestSkillStore(t)
	if err := store.SaveSkill("my-skill", "我的技能", "描述", "# 正文\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), "my-skill", skillFileName)); err != nil {
		t.Fatalf("SKILL.md 未落盘: %v", err)
	}
	d, ok := store.GetDetail("my-skill")
	if !ok || !strings.Contains(d.Body, "# 正文") || d.Description != "描述" {
		t.Fatalf("保存后详情错误: %+v ok=%v", d, ok)
	}
	// 非法 id 拒绝
	if err := store.SaveSkill("bad id", "n", "d", "b"); err == nil {
		t.Fatal("非法 id 应拒绝")
	}
	if err := store.DeleteSkill("my-skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), "my-skill")); !os.IsNotExist(err) {
		t.Fatal("技能目录应已删除")
	}
	if _, ok := store.GetDetail("my-skill"); ok {
		t.Fatal("删除后不应再查到")
	}
}

// 状态持久化：停用/强制注入写入 skills_state.json，重扫后恢复
func TestSkillStatePersistence(t *testing.T) {
	store := newTestSkillStore(t)
	if err := store.SaveSkill("st", "状态技能", "描述", "正文"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled("st", false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlwaysInject("st", true); err != nil {
		t.Fatal(err)
	}
	store.Refresh()
	var m *SkillMeta
	for _, x := range store.GetAll() {
		if x.ID == "st" {
			m = x
		}
	}
	if m == nil || m.Enabled || !m.AlwaysInject {
		t.Fatalf("状态未持久化恢复: %+v", m)
	}
}

// hasScripts 标记
func TestSkillHasScripts(t *testing.T) {
	store := newTestSkillStore(t)
	writeSkill(t, store, "with-scripts", "---\nname: s\ndescription: d\n---\n\nbody\n")
	if err := os.MkdirAll(filepath.Join(store.Dir(), "with-scripts", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	store.Refresh()
	m := store.GetAll()[0]
	if !m.HasScripts {
		t.Fatal("存在 scripts 目录时 HasScripts 应为 true")
	}
}

// read_skill 经 ToolManager.BuildView 注册并能取正文；停用/白名单外不可读
func TestReadSkillEndToEnd(t *testing.T) {
	skillDir := filepath.Join(t.TempDir(), "skills")
	ss := NewSkillStore(skillDir)
	if err := ss.SaveSkill("alpha", "阿尔法", "阿尔法技能描述", "# 阿尔法正文\n"); err != nil {
		t.Fatal(err)
	}
	if err := ss.SaveSkill("beta", "贝塔", "贝塔技能描述", "# 贝塔正文\n"); err != nil {
		t.Fatal(err)
	}
	store := NewToolStore(filepath.Join(t.TempDir(), "tools.json"))
	tm := NewToolManager(store, ss)

	// 全部技能可用：read_skill 已注册，list 可见，execute 取到正文
	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})
	list, err := view.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "read_skill") {
		t.Fatalf("工具清单缺少 read_skill: %s", list)
	}
	out, err := view.ExecuteTool("read_skill", map[string]interface{}{"id": "alpha"})
	if err != nil {
		t.Fatalf("读取技能失败: %v", err)
	}
	if !strings.Contains(out, "阿尔法正文") {
		t.Fatalf("技能正文错误: %s", out)
	}

	// 会话白名单只含 beta：alpha 不可读，beta 可读
	view2 := tm.BuildView(context.Background(), BuildOptions{EnabledSkills: []string{"beta"}, Enforcer: AllowAllEnforcer{}})
	if _, err := view2.ExecuteTool("read_skill", map[string]interface{}{"id": "alpha"}); err == nil {
		t.Fatal("白名单外技能不应可读")
	}
	if _, err := view2.ExecuteTool("read_skill", map[string]interface{}{"id": "beta"}); err != nil {
		t.Fatalf("白名单内技能应可读: %v", err)
	}

	// 全部停用：read_skill 不注册
	if err := ss.SetEnabled("alpha", false); err != nil {
		t.Fatal(err)
	}
	if err := ss.SetEnabled("beta", false); err != nil {
		t.Fatal(err)
	}
	view3 := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})
	list3, _ := view3.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if strings.Contains(list3, "read_skill") {
		t.Fatalf("无可用技能时不应注册 read_skill: %s", list3)
	}
}

// 强制注入块包含正文
func TestBuildAlwaysInjectBlock(t *testing.T) {
	store := newTestSkillStore(t)
	if err := store.SaveSkill("inj", "注入技能", "描述", "# 强制正文"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlwaysInject("inj", true); err != nil {
		t.Fatal(err)
	}
	block := BuildAlwaysInjectBlock(store.GetAll())
	if !strings.Contains(block, "强制正文") || !strings.Contains(block, "注入技能") {
		t.Fatalf("强制注入块内容错误: %s", block)
	}
}
