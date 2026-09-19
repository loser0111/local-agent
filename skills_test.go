package main

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== 测试辅助 =====

// newTestSkillStore 构造临时技能目录的 SkillStore（不写示例技能）
func newTestSkillStore(t *testing.T) *SkillStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "skills")
	return NewSkillStore(dir)
}

// writeSkill 往技能目录里塞一个 SKILL.md 并重扫
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

// writeFileAt 在技能目录内按相对路径写文件（用于造 L3 资源）
func writeFileAt(t *testing.T, store *SkillStore, id, rel, content string) {
	t.Helper()
	full := filepath.Join(store.Dir(), id, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// findSkill 取指定 ID 的元数据
func findSkill(t *testing.T, store *SkillStore, id string) *SkillMeta {
	t.Helper()
	m, ok := store.Get(id)
	if !ok {
		t.Fatalf("技能不存在: %s", id)
	}
	return m
}

// ===== frontmatter 解析 =====

// 标准 frontmatter：全部字段都能解析出来
func TestParseSkillFrontmatterStandard(t *testing.T) {
	md := "---\n" +
		"name: pdf-report\n" +
		"description: 从 CSV 生成 PDF 报表。当用户需要把表格数据导出为 PDF 时使用。\n" +
		"version: 1.0.0\n" +
		"license: MIT\n" +
		"author: 张三\n" +
		"allowed-tools: bash, read_file\n" +
		"disable-model-invocation: true\n" +
		"user-invocable: false\n" +
		"metadata:\n" +
		"  owner: team-reports\n" +
		"---\n\n# PDF\n\n步骤 1\n"

	fm, body, err := ParseSkillDocument(md)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "pdf-report" {
		t.Fatalf("name 解析错误: %q", fm.Name)
	}
	if !strings.HasPrefix(fm.Description, "从 CSV 生成 PDF 报表") {
		t.Fatalf("description 解析错误: %q", fm.Description)
	}
	if fm.Version != "1.0.0" || fm.License != "MIT" || fm.Author != "张三" {
		t.Fatalf("版本/许可/作者解析错误: %q %q %q", fm.Version, fm.License, fm.Author)
	}
	if len(fm.AllowedTools) != 2 || fm.AllowedTools[0] != "bash" || fm.AllowedTools[1] != "read_file" {
		t.Fatalf("allowed-tools 标量解析错误: %#v", fm.AllowedTools)
	}
	if !fm.DisableModelInvocation || fm.UserInvocable {
		t.Fatalf("布尔字段解析错误: disable=%v user=%v", fm.DisableModelInvocation, fm.UserInvocable)
	}
	if owner, _ := fm.Metadata["owner"].(string); owner != "team-reports" {
		t.Fatalf("嵌套 metadata 解析错误: %#v", fm.Metadata)
	}
	if !strings.HasPrefix(body, "# PDF") {
		t.Fatalf("正文未正确剥离 frontmatter: %q", body)
	}
}

// allowed-tools 写成序列
func TestParseAllowedToolsSequence(t *testing.T) {
	md := "---\nname: s\ndescription: d\nallowed-tools:\n  - bash\n  - read_file\n---\n\nbody\n"
	fm, _, err := ParseSkillDocument(md)
	if err != nil {
		t.Fatal(err)
	}
	if len(fm.AllowedTools) != 2 || fm.AllowedTools[0] != "bash" {
		t.Fatalf("序列形式的 allowed-tools 解析错误: %#v", fm.AllowedTools)
	}
}

// user-invocable 缺省应为 true
func TestUserInvocableDefaultsTrue(t *testing.T) {
	fm, _, err := ParseSkillDocument("---\nname: s\ndescription: d\n---\n\nbody\n")
	if err != nil {
		t.Fatal(err)
	}
	if !fm.UserInvocable {
		t.Fatal("user-invocable 缺省应为 true")
	}
}

// version 写成数字（YAML 里 1.0 不是字符串）
func TestParseVersionAsNumber(t *testing.T) {
	fm, _, err := ParseSkillDocument("---\nname: s\ndescription: d\nversion: 1.0\n---\n\nbody\n")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Version != "1" && fm.Version != "1.0" {
		t.Fatalf("数字形式的 version 应被转成字符串，得到 %q", fm.Version)
	}
}

// 多行 description（块标量）
func TestParseBlockScalarDescription(t *testing.T) {
	md := "---\nname: s\ndescription: >\n  第一行\n  第二行\n---\n\nbody\n"
	fm, _, err := ParseSkillDocument(md)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fm.Description, "第一行") || !strings.Contains(fm.Description, "第二行") {
		t.Fatalf("块标量 description 解析错误: %q", fm.Description)
	}
}

// 无 frontmatter：不报错，正文为全文，扫描后标记缺 description
func TestSkillParseNoFrontmatter(t *testing.T) {
	md := "# 纯 Markdown\n\n没有 frontmatter\n"
	fm, body, err := ParseSkillDocument(md)
	if err != nil {
		t.Fatalf("无 frontmatter 不应报错: %v", err)
	}
	if fm.Name != "" || fm.Description != "" || body != md {
		t.Fatalf("无 frontmatter 解析错误: %#v body=%q", fm, body)
	}
	store := newTestSkillStore(t)
	writeSkill(t, store, "plain", md)
	m := findSkill(t, store, "plain")
	if len(m.Errors) == 0 {
		t.Fatal("缺 description 应被标为阻断错误")
	}
}

// frontmatter 未闭合
func TestSkillParseUnclosed(t *testing.T) {
	_, _, err := ParseSkillDocument("---\nname: x\ndescription: y\n")
	if err == nil || !strings.Contains(err.Error(), "未闭合") {
		t.Fatalf("应报 frontmatter 未闭合，得到: %v", err)
	}
}

// CRLF 与 BOM
func TestParseCRLFAndBOM(t *testing.T) {
	md := "\ufeff---\r\nname: win\r\ndescription: windows 换行\r\n---\r\n\r\n正文\r\n"
	fm, body, err := ParseSkillDocument(md)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "win" || fm.Description != "windows 换行" {
		t.Fatalf("CRLF/BOM 解析错误: %#v", fm)
	}
	if !strings.Contains(body, "正文") {
		t.Fatalf("正文解析错误: %q", body)
	}
}

// description 里含 YAML 敏感字符仍能正确往返
func TestMarshalRoundTripSpecialChars(t *testing.T) {
	fm := &SkillFrontmatter{
		Name:          "quoted-skill",
		Description:   "含冒号: 与 # 号，还有\"引号\"的说明",
		UserInvocable: true,
	}
	text, err := MarshalSkillDocument(fm, "# 正文\n\n内容")
	if err != nil {
		t.Fatal(err)
	}
	back, body, err := ParseSkillDocument(text)
	if err != nil {
		t.Fatalf("序列化结果应能被自己解析回来: %v\n%s", err, text)
	}
	if back.Description != fm.Description {
		t.Fatalf("description 往返不一致: %q != %q", back.Description, fm.Description)
	}
	if !strings.Contains(body, "内容") {
		t.Fatalf("正文往返丢失: %q", body)
	}
}

// ===== 校验 =====

// description 超长属于阻断错误
func TestValidateDescriptionTooLong(t *testing.T) {
	fm := &SkillFrontmatter{Name: "s", Description: strings.Repeat("字", skillDescMaxLen+1), UserInvocable: true}
	errs, _ := ValidateSkillDocument("s", fm, "body")
	if len(errs) == 0 {
		t.Fatal("description 超长应报错")
	}
}

// name 与目录名不一致只告警、不阻断（本项目有意保留的偏离）
func TestValidateNameMismatchIsWarning(t *testing.T) {
	fm := &SkillFrontmatter{Name: "显示名", Description: "描述", UserInvocable: true}
	errs, warns := ValidateSkillDocument("dir-name", fm, "body")
	if len(errs) != 0 {
		t.Fatalf("name 与目录名不一致不应阻断: %v", errs)
	}
	var found bool
	for _, w := range warns {
		if strings.Contains(w, "不一致") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应给出 name 不一致的告警: %v", warns)
	}
}

// 未识别字段只告警
func TestValidateUnknownField(t *testing.T) {
	md := "---\nname: s\ndescription: d\nfuture-field: x\n---\n\nbody\n"
	fm, _, err := ParseSkillDocument(md)
	if err != nil {
		t.Fatal(err)
	}
	errs, warns := ValidateSkillDocument("s", fm, "body")
	if len(errs) != 0 {
		t.Fatalf("未识别字段不应阻断: %v", errs)
	}
	if len(warns) == 0 || !strings.Contains(strings.Join(warns, "|"), "future-field") {
		t.Fatalf("应告警未识别字段: %v", warns)
	}
}

// 两个开关同时关掉：技能不可达，给出告警
func TestValidateUnreachableSkill(t *testing.T) {
	fm := &SkillFrontmatter{
		Name:                   "s",
		Description:            "d",
		DisableModelInvocation: true,
		UserInvocable:          false,
	}
	_, warns := ValidateSkillDocument("s", fm, "body")
	if !strings.Contains(strings.Join(warns, "|"), "无法被任何方式调用") {
		t.Fatalf("应告警技能不可达: %v", warns)
	}
}

// ===== 路由与渐进式披露 =====

// L1 清单：停用、错误、disable-model-invocation 的技能都不进
func TestBuildSkillIndex(t *testing.T) {
	store := newTestSkillStore(t)
	writeSkill(t, store, "auto", "---\nname: auto\ndescription: 可自动触发\n---\n\n正文\n")
	writeSkill(t, store, "manual", "---\nname: manual\ndescription: 仅手动\ndisable-model-invocation: true\n---\n\n正文\n")
	writeSkill(t, store, "off", "---\nname: off\ndescription: 已停用\n---\n\n正文\n")
	writeSkill(t, store, "bad", "# 无 frontmatter\n")
	if err := store.SetEnabled("off", false); err != nil {
		t.Fatal(err)
	}

	idx := BuildSkillIndex(store.GetAll())
	if !strings.Contains(idx, "auto") {
		t.Fatalf("清单应包含可自动触发的技能: %s", idx)
	}
	if strings.Contains(idx, "manual") {
		t.Fatalf("disable-model-invocation 的技能不应进 L1 清单: %s", idx)
	}
	if strings.Contains(idx, "off") || strings.Contains(idx, "bad") {
		t.Fatalf("停用/出错技能不应进清单: %s", idx)
	}
}

// 描述过长在 L1 清单里被截断
func TestBuildSkillIndexTruncatesDescription(t *testing.T) {
	m := &SkillMeta{
		ID:          "long",
		Description: strings.Repeat("字", skillListMaxDesc+50),
		Enabled:     true,
	}
	out := BuildSkillIndex([]*SkillMeta{m})
	if len([]rune(out)) > skillListMaxDesc+30 {
		t.Fatalf("描述未截断，长度 %d", len([]rune(out)))
	}
}

// ID 校验
func TestValidSkillID(t *testing.T) {
	valid := []string{"abc", "a-b_c", "A1", "x-y-z"}
	invalid := []string{"", "a b", "中文", "a/b", strings.Repeat("a", skillNameMaxLen+1), "..", "."}
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

// 增删：SaveSkill 落盘标准 frontmatter，DeleteSkill 删除目录
func TestSkillSaveDelete(t *testing.T) {
	store := newTestSkillStore(t)
	draft := SkillDraft{
		Description:   "描述：含冒号",
		Version:       "1.2.3",
		UserInvocable: true,
		Body:          "# 正文\n",
	}
	if err := store.SaveSkill("my-skill", draft); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(store.Dir(), "my-skill", skillFileName))
	if err != nil {
		t.Fatalf("SKILL.md 未落盘: %v", err)
	}
	// name 缺省时应自动补成目录名
	if !strings.Contains(string(raw), "name: my-skill") {
		t.Fatalf("name 应补为目录名:\n%s", raw)
	}

	d, ok := store.GetDetail("my-skill")
	if !ok {
		t.Fatal("保存后查不到技能")
	}
	if d.Description != "描述：含冒号" || d.Version != "1.2.3" {
		t.Fatalf("详情字段错误: %#v", d)
	}
	if !strings.Contains(d.Body, "# 正文") {
		t.Fatalf("正文错误: %q", d.Body)
	}

	// 非法 ID 与空 description 都要拒绝
	if err := store.SaveSkill("bad id", draft); err == nil {
		t.Fatal("非法 ID 应被拒绝")
	}
	if err := store.SaveSkill("no-desc", SkillDraft{UserInvocable: true}); err == nil {
		t.Fatal("空 description 应被拒绝")
	}

	if err := store.DeleteSkill("my-skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), "my-skill")); !os.IsNotExist(err) {
		t.Fatal("技能目录应已删除")
	}
}

// 状态持久化：停用状态写入 skills_state.json，重扫后恢复
func TestSkillStatePersistence(t *testing.T) {
	store := newTestSkillStore(t)
	if err := store.SaveSkill("st", SkillDraft{Description: "描述", UserInvocable: true, Body: "正文"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled("st", false); err != nil {
		t.Fatal(err)
	}
	store.Refresh()
	m := findSkill(t, store, "st")
	if m.Enabled {
		t.Fatalf("停用状态未持久化: %#v", m)
	}
	// 重新打开一个指向同一目录的 store，状态应仍在
	again := NewSkillStore(store.Dir())
	if againM := findSkill(t, again, "st"); againM.Enabled {
		t.Fatal("重建 store 后停用状态丢失")
	}
}

// ===== 显式调用 =====

// 命令解析：只认消息开头的 /名称
func TestParseSkillCommand(t *testing.T) {
	cases := []struct {
		in       string
		wantID   string
		wantRest string
		wantOK   bool
	}{
		{"/pdf-report 帮我生成报表", "pdf-report", "帮我生成报表", true},
		{"  /a-b  ", "a-b", "", true},
		{"/pdf-report", "pdf-report", "", true},
		{"前面有字 /pdf-report", "", "", false},
		{"正文里的斜杠/不是命令", "", "", false},
		{"/", "", "", false},
		{"/zh-cn 中文技能名", "zh-cn", "中文技能名", true},
	}
	for _, c := range cases {
		id, rest, ok := ParseSkillCommand(c.in)
		if ok != c.wantOK || id != c.wantID || rest != c.wantRest {
			t.Fatalf("ParseSkillCommand(%q) = (%q, %q, %v)，期望 (%q, %q, %v)",
				c.in, id, rest, ok, c.wantID, c.wantRest, c.wantOK)
		}
	}
}

// 解析到不存在的技能时按普通消息处理；命中但不可用时给出原因
func TestResolveSkillCommand(t *testing.T) {
	store := newTestSkillStore(t)
	writeSkill(t, store, "ok-skill", "---\nname: ok-skill\ndescription: 可用\n---\n\n正文\n")
	writeSkill(t, store, "manual-skill", "---\nname: manual-skill\ndescription: 仅手动\ndisable-model-invocation: true\n---\n\n正文\n")
	writeSkill(t, store, "no-user", "---\nname: no-user\ndescription: 禁手动\nuser-invocable: false\n---\n\n正文\n")
	if err := store.SetEnabled("manual-skill", false); err != nil {
		t.Fatal(err)
	}

	// 不存在的技能：静默按普通消息处理（避免 /usr/bin 这类路径被当成技能名）
	res := store.ResolveSkillCommand("/not-exist 你好")
	if res.Ok || res.Reason != "" {
		t.Fatalf("不存在的技能应静默回落: %#v", res)
	}

	// 停用的技能：命中且给出原因
	res = store.ResolveSkillCommand("/manual-skill 干活")
	if res.Ok || !strings.Contains(res.Reason, "停用") {
		t.Fatalf("停用技能应给出原因: %#v", res)
	}

	// user-invocable: false 不允许显式调用
	res = store.ResolveSkillCommand("/no-user 干活")
	if res.Ok || !strings.Contains(res.Reason, "user-invocable") {
		t.Fatalf("user-invocable:false 应拒绝显式调用: %#v", res)
	}

	// 正常命中
	res = store.ResolveSkillCommand("/ok-skill 干活")
	if !res.Ok || res.Skill == nil || res.Skill.ID != "ok-skill" || res.Rest != "干活" {
		t.Fatalf("显式调用解析错误: %#v", res)
	}
}

// 显式调用注入段包含正文与技能目录
func TestBuildExplicitSkillBlock(t *testing.T) {
	m := &SkillMeta{ID: "demo", Dir: "/tmp/demo", Resources: []SkillResource{{Path: "references/a.md"}}}
	block := BuildExplicitSkillBlock(m, "# 说明正文")
	if !strings.Contains(block, "/demo") || !strings.Contains(block, "说明正文") {
		t.Fatalf("注入段缺少关键内容: %s", block)
	}
	if !strings.Contains(block, "references/a.md") {
		t.Fatalf("注入段应列出资源清单: %s", block)
	}
}

// ===== L2 / L3 工具 =====

// read_skill 与 read_skill_file 经 ToolManager 注册，并受白名单约束
func TestSkillToolsEndToEnd(t *testing.T) {
	ss := NewSkillStore(filepath.Join(t.TempDir(), "skills"))
	if err := ss.SaveSkill("alpha", SkillDraft{Description: "阿尔法", UserInvocable: true, Body: "# 阿尔法正文\n"}); err != nil {
		t.Fatal(err)
	}
	if err := ss.SaveSkill("beta", SkillDraft{Description: "贝塔", UserInvocable: true, Body: "# 贝塔正文\n"}); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, ss, "alpha", "references/guide.md", "参考资料内容")
	ss.Refresh()

	store := NewToolStore(filepath.Join(t.TempDir(), "tools.json"))
	tm := NewToolManager(store, ss)

	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})
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
	view2 := tm.BuildView(context.Background(), BuildOptions{EnabledSkills: []string{"beta"}, Enforcer: AllowAllEnforcer{}})
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
	view3 := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})
	list3, _ := view3.ExecuteTool("tool_router", map[string]interface{}{"action": "list"})
	if strings.Contains(list3, "read_skill") || strings.Contains(list3, "read_skill_file") {
		t.Fatalf("无可用技能时不应注册技能工具: %s", list3)
	}
}

// read_skill 对 disable-model-invocation 的技能不可读，但 read_skill_file 仍可读
func TestModelDisabledSkillSplit(t *testing.T) {
	ss := NewSkillStore(filepath.Join(t.TempDir(), "skills"))
	if err := ss.SaveSkill("manual", SkillDraft{
		Description:            "仅手动",
		DisableModelInvocation: true,
		UserInvocable:          true,
		Body:                   "正文",
	}); err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, ss, "manual", "references/x.md", "内容")
	ss.Refresh()

	store := NewToolStore(filepath.Join(t.TempDir(), "tools.json"))
	tm := NewToolManager(store, ss)
	view := tm.BuildView(context.Background(), BuildOptions{Enforcer: AllowAllEnforcer{}})

	if _, err := view.ExecuteTool("read_skill", map[string]interface{}{"id": "manual"}); err == nil {
		t.Fatal("disable-model-invocation 的技能不应能经 read_skill 读取")
	}
	// 用户显式调用后，正文里引用的资料仍要读得到
	if _, err := view.ExecuteTool("read_skill_file", map[string]interface{}{"id": "manual", "path": "references/x.md"}); err != nil {
		t.Fatalf("read_skill_file 不应受 disable-model-invocation 限制: %v", err)
	}
}

// 资源路径必须锁在技能目录内
func TestSkillResourcePathTraversal(t *testing.T) {
	base := t.TempDir()
	skillDir := filepath.Join(base, "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(secret, []byte("机密"), 0o644); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"../secret.txt",
		"../../secret.txt",
		"references/../../secret.txt",
		"/etc/passwd",
		"..",
		"",
	}
	for _, rel := range bad {
		if _, err := resolveSkillResource(skillDir, rel); err == nil {
			t.Fatalf("非法路径应被拒绝: %q", rel)
		}
	}
	if _, err := readSkillResource(skillDir, "../secret.txt"); err == nil {
		t.Fatal("越界读取应被拒绝")
	}

	// 正常相对路径可用
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "references", "a.md"), []byte("正常"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readSkillResource(skillDir, "references/a.md")
	if err != nil || got != "正常" {
		t.Fatalf("正常资源读取失败: %q %v", got, err)
	}
}

// 二进制资源按文本读取应被拒绝
func TestSkillResourceBinaryRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSkillResource(dir, "logo.png"); err == nil {
		t.Fatal("二进制文件应被拒绝")
	}
}

// 资源枚举：归类与排除 SKILL.md
func TestListSkillResources(t *testing.T) {
	store := newTestSkillStore(t)
	writeSkill(t, store, "demo", "---\nname: demo\ndescription: d\n---\n\n正文\n")
	writeFileAt(t, store, "demo", "scripts/run.sh", "echo hi")
	writeFileAt(t, store, "demo", "references/a.md", "a")
	writeFileAt(t, store, "demo", "assets/logo.txt", "l")
	writeFileAt(t, store, "demo", ".hidden/x.txt", "h")
	store.Refresh()

	m := findSkill(t, store, "demo")
	var paths []string
	for _, r := range m.Resources {
		paths = append(paths, r.Path)
	}
	joined := strings.Join(paths, ",")
	if strings.Contains(joined, skillFileName) {
		t.Fatalf("资源清单不应包含 SKILL.md 自身: %s", joined)
	}
	if strings.Contains(joined, ".hidden") {
		t.Fatalf("资源清单不应包含隐藏目录: %s", joined)
	}
	if !strings.Contains(joined, "scripts/run.sh") || !strings.Contains(joined, "references/a.md") {
		t.Fatalf("资源清单缺少预期条目: %s", joined)
	}
	if !m.HasScripts {
		t.Fatal("存在 scripts/ 时 HasScripts 应为 true")
	}
	kinds := map[string]string{}
	for _, r := range m.Resources {
		kinds[r.Path] = r.Kind
	}
	if kinds["scripts/run.sh"] != "scripts" || kinds["references/a.md"] != "references" || kinds["assets/logo.txt"] != "assets" {
		t.Fatalf("资源归类错误: %#v", kinds)
	}
}

// ===== 安装 =====

// 从文件夹安装单个技能，并记录来源
func TestInstallFromFolder(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "pdf-report"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "pdf-report", skillFileName),
		[]byte("---\nname: pdf-report\ndescription: 生成 PDF\n---\n\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := newTestSkillStore(t)
	installer := NewSkillInstaller(store)
	results, err := installer.InstallFromFolder(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "pdf-report" || results[0].Action != "installed" {
		t.Fatalf("安装结果错误: %#v", results)
	}
	m := findSkill(t, store, "pdf-report")
	if m.Install == nil || m.Install.SourceType != "folder" {
		t.Fatalf("未记录安装来源: %#v", m.Install)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), "pdf-report", skillFileName)); err != nil {
		t.Fatalf("技能未落盘: %v", err)
	}
	// 源目录里不应留下临时目录
	entries, _ := os.ReadDir(store.Dir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") || strings.HasPrefix(e.Name(), ".backup-") {
			t.Fatalf("残留临时目录: %s", e.Name())
		}
	}
}

// 安装的文件夹本身就是一个技能目录
func TestInstallFromFolderSingleSkill(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, skillFileName),
		[]byte("---\nname: solo\ndescription: 单技能\n---\n\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newTestSkillStore(t)
	results, err := NewSkillInstaller(store).InstallFromFolder(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != filepath.Base(src) {
		t.Fatalf("单技能安装结果错误: %#v", results)
	}
}

// 冲突：已存在同名技能时整体拒绝，且不留下半截目录
func TestInstallConflictRejected(t *testing.T) {
	store := newTestSkillStore(t)
	if err := store.SaveSkill("dup", SkillDraft{Description: "已存在", UserInvocable: true, Body: "正文"}); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "dup"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "dup", skillFileName),
		[]byte("---\nname: dup\ndescription: 新的\n---\n\n正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSkillInstaller(store).InstallFromFolder(src); err == nil {
		t.Fatal("同名技能应被拒绝")
	}
	// 原有技能内容不能被改动
	raw, _ := os.ReadFile(filepath.Join(store.Dir(), "dup", skillFileName))
	if !strings.Contains(string(raw), "已存在") {
		t.Fatalf("冲突时不应改动既有技能:\n%s", raw)
	}
}

// 来源里没有 SKILL.md
func TestInstallNoSkillFound(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newTestSkillStore(t)
	if _, err := NewSkillInstaller(store).InstallFromFolder(src); err == nil {
		t.Fatal("没有 SKILL.md 时应报错")
	}
}

// 校验不通过的技能不得安装
func TestInstallRejectsInvalidSkill(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "broken", skillFileName),
		[]byte("---\nname: broken\n---\n\n没有 description\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newTestSkillStore(t)
	if _, err := NewSkillInstaller(store).InstallFromFolder(src); err == nil {
		t.Fatal("缺 description 的技能应被拒绝安装")
	}
	if _, ok := store.Get("broken"); ok {
		t.Fatal("被拒绝的技能不应落盘")
	}
}

// 从 zip 安装
func TestInstallFromZip(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "skills.zip")
	if err := makeSkillZip(zipPath, map[string]string{
		"my-skill/SKILL.md":         "---\nname: my-skill\ndescription: 压缩包技能\n---\n\n正文\n",
		"my-skill/scripts/run.sh":   "echo hi",
		"__MACOSX/._SKILL.md":       "junk",
		"my-skill/references/a.md":  "资料",
	}); err != nil {
		t.Fatal(err)
	}

	store := newTestSkillStore(t)
	results, err := NewSkillInstaller(store).InstallFromZip(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "my-skill" {
		t.Fatalf("zip 安装结果错误: %#v", results)
	}
	m := findSkill(t, store, "my-skill")
	if m.Install == nil || m.Install.SourceType != "zip" {
		t.Fatalf("未记录 zip 来源: %#v", m.Install)
	}
	if !m.HasScripts {
		t.Fatal("scripts/ 应被识别")
	}
	// __MACOSX 不应被解出来
	if _, err := os.Stat(filepath.Join(store.Dir(), "__MACOSX")); !os.IsNotExist(err) {
		t.Fatal("__MACOSX 应被跳过")
	}
}

// zip 里带目录穿越的条目必须被拒绝
func TestInstallZipSlipRejected(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	if err := makeSkillZip(zipPath, map[string]string{
		"../evil.txt":                "escaped",
		"my-skill/SKILL.md":          "---\nname: my-skill\ndescription: d\n---\n\n正文\n",
	}); err != nil {
		t.Fatal(err)
	}
	store := newTestSkillStore(t)
	if _, err := NewSkillInstaller(store).InstallFromZip(zipPath); err == nil {
		t.Fatal("含越界路径的 zip 应被拒绝")
	}
}

// git 安装的入参校验与不可更新场景
func TestInstallGitValidation(t *testing.T) {
	store := newTestSkillStore(t)
	installer := NewSkillInstaller(store)
	if _, err := installer.InstallFromGit("", "", ""); err == nil {
		t.Fatal("空仓库地址应报错")
	}

	// 非 git 来源的技能不能更新
	if err := store.SaveSkill("local", SkillDraft{Description: "本地", UserInvocable: true, Body: "正文"}); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.Update("local"); err == nil {
		t.Fatal("非 git 来源的技能不应可更新")
	}
	if _, err := installer.Update("not-exist"); err == nil {
		t.Fatal("不存在的技能应报错")
	}
}

// makeSkillZip 按 路径->内容 造一个 zip
func makeSkillZip(zipPath string, files map[string]string) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()
	w := zip.NewWriter(f)
	defer w.Close()
	for name, content := range files {
		entry, err := w.Create(name)
		if err != nil {
			return err
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			return err
		}
	}
	return nil
}
