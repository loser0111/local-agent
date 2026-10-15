package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"wails-tmp/internal/memstore"
	"wails-tmp/internal/tool"
)

// 记忆三件套的引擎用例。
//
// 根 memory_test.go 走的是"经 ToolManager 装配 → SessionView.ExecuteTool"那条路
// （测的是装配与白名单）；这里直接构造工具、直接喂记忆库，钉住的是**工具自身**的语义：
// 参数表、id 与检索词两条分支、忽略态、以及"删不删"这条不可逆边界。

func newTestStore(t *testing.T) *memstore.MemoryStore {
	t.Helper()
	st, err := memstore.NewMemoryStore(
		filepath.Join(t.TempDir(), "memory"),
		memstore.MemoryConfig{StalenessCaveat: true},
	)
	if err != nil {
		t.Fatalf("建记忆库失败: %v", err)
	}
	return st
}

// runTool 调一次工具，把结果与错误都交回调用方（很多用例要断言"报错而不静默"）。
func runTool(t *testing.T, tl tool.ToolInterface, args map[string]interface{}) (string, error) {
	t.Helper()
	return tl.Execute(context.Background(), args)
}

func mustRun(t *testing.T, tl tool.ToolInterface, args map[string]interface{}) string {
	t.Helper()
	out, err := runTool(t, tl, args)
	if err != nil {
		t.Fatalf("工具调用失败: %v", err)
	}
	return out
}

func TestToolSchemas(t *testing.T) {
	cases := []struct {
		want     string // 工具名
		tl       tool.ToolInterface
		required string
		argCount int
	}{
		{tool.ToolMemorySearch, NewSearchTool(ToolContext{}), "query", 1},
		{tool.ToolMemorySave, NewSaveTool(ToolContext{}), "content", 5},
		{tool.ToolMemoryForget, NewForgetTool(ToolContext{}), "target", 1},
	}
	for _, c := range cases {
		if got := c.tl.GetName(); got != c.want {
			t.Errorf("工具名 = %q, want %q", got, c.want)
		}
		if c.tl.GetDescription() == "" {
			t.Errorf("%s 缺少描述（模型靠它决定何时调用）", c.want)
		}
		if got := c.tl.GetParameters(); len(got) != c.argCount {
			t.Errorf("%s 参数个数 = %d, want %d", c.want, len(got), c.argCount)
		}
		def, ok := c.tl.GetParameters()[c.required]
		if !ok || def == nil || def.Type != "string" {
			t.Errorf("%s 的必需参数 %q 未按 string 声明: %+v", c.want, c.required, def)
		}
		rp, ok := c.tl.(interface{ RequiredParams() []string })
		if !ok {
			t.Fatalf("%s 未实现 RequiredParams", c.want)
		}
		if got := rp.RequiredParams(); len(got) != 1 || got[0] != c.required {
			t.Errorf("%s RequiredParams = %v, want [%s]", c.want, got, c.required)
		}
	}
}

func TestSaveSearchForgetByID(t *testing.T) {
	st := newTestStore(t)
	var touched []string
	mc := ToolContext{
		Store:   st,
		Project: "proj",
		Source:  "sess-1",
		OnTouch: func(ids []string) { touched = append(touched, ids...) },
	}
	save, search, forget := NewSaveTool(mc), NewSearchTool(mc), NewForgetTool(mc)

	out := mustRun(t, save, map[string]interface{}{
		"content": "用户希望中文注释",
		"title":   "注释风格",
		"type":    "user",
		"scope":   "user",
		"tags":    "comment, 注释",
	})
	if !strings.Contains(out, "已保存") || !strings.Contains(out, "注释风格") {
		t.Fatalf("保存回执应含 id 与标题: %q", out)
	}

	entries, err := st.ListMemories(memstore.ScopeUser, "")
	if err != nil || len(entries) != 1 {
		t.Fatalf("落库条目 = %d, err=%v", len(entries), err)
	}
	id := entries[0].Meta.ID
	if !strings.HasPrefix(id, "mem_") {
		t.Fatalf("id 形状不对: %q", id)
	}
	// 溯源字段与 tags 都要真的落进去（tags 是检索加权项，丢了会影响召回）
	if entries[0].Meta.Source != "sess-1" {
		t.Errorf("Source = %q, want sess-1", entries[0].Meta.Source)
	}
	if len(entries[0].Meta.Tags) != 2 {
		t.Errorf("tags 应解析成 2 个: %v", entries[0].Meta.Tags)
	}

	// 检索词分支
	out = mustRun(t, search, map[string]interface{}{"query": "中文注释"})
	if !strings.Contains(out, "注释风格") || !strings.Contains(out, "用户希望中文注释") {
		t.Fatalf("检索应返回标题与正文: %q", out)
	}
	if len(touched) == 0 || touched[0] != id {
		t.Fatalf("命中 id 应经 OnTouch 交回（用于已展示记账）: %v", touched)
	}

	// id 分支
	out = mustRun(t, search, map[string]interface{}{"query": id})
	if !strings.Contains(out, "注释风格") {
		t.Fatalf("按 id 应能取到正文: %q", out)
	}

	// 删除（按 id）
	if out = mustRun(t, forget, map[string]interface{}{"target": id}); !strings.Contains(out, "已删除记忆 "+id) {
		t.Fatalf("删除回执: %q", out)
	}
	if entries, _ = st.ListMemories(memstore.ScopeUser, ""); len(entries) != 0 {
		t.Fatalf("删除后应清空, 实际 %d 条", len(entries))
	}
	if out = mustRun(t, search, map[string]interface{}{"query": "注释"}); !strings.Contains(out, "没有找到相关记忆") {
		t.Fatalf("空库检索应给可读结果: %q", out)
	}
}

func TestSearchIgnoredButSaveAndForgetStillWork(t *testing.T) {
	st := newTestStore(t)
	if _, err := runTool(t, NewSaveTool(ToolContext{Store: st}), map[string]interface{}{
		"content": "secret-fact", "title": "机密", "scope": "user",
	}); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}
	entries, _ := st.ListMemories(memstore.ScopeUser, "")
	if len(entries) != 1 {
		t.Fatalf("准备数据 = %d 条", len(entries))
	}
	id := entries[0].Meta.ID

	// 本会话关闭记忆：search 必须闭嘴，且**不能**顺带 grep 出正文
	ignored := ToolContext{Store: st, Ignore: true}
	out := mustRun(t, NewSearchTool(ignored), map[string]interface{}{"query": "secret"})
	if !strings.Contains(out, "已关闭记忆") {
		t.Fatalf("忽略态应如实说明: %q", out)
	}
	if strings.Contains(out, "secret-fact") {
		t.Fatalf("忽略态不得返回正文: %q", out)
	}
	// 连按 id 也不返回（否则等于绕过"关闭"）
	out = mustRun(t, NewSearchTool(ignored), map[string]interface{}{"query": id})
	if strings.Contains(out, "secret-fact") {
		t.Fatalf("忽略态按 id 也不得返回正文: %q", out)
	}

	// 但"写"与"删"不能被 Ignore 挡住：用户显式要求记住要能记，
	// 而关掉记忆之后若连删都删不了，旧记忆就永远清不掉了。
	if out = mustRun(t, NewSaveTool(ignored), map[string]interface{}{
		"content": "用户显式要求记住的事实", "title": "显式", "scope": "user",
	}); !strings.Contains(out, "已保存") {
		t.Fatalf("忽略态仍应可写: %q", out)
	}
	if out = mustRun(t, NewForgetTool(ignored), map[string]interface{}{"target": id}); !strings.Contains(out, "已删除记忆") {
		t.Fatalf("忽略态仍应可删: %q", out)
	}
}

func TestForgetListsMultipleHitsWithoutDeleting(t *testing.T) {
	st := newTestStore(t)
	mc := ToolContext{Store: st}
	save := NewSaveTool(mc)
	for _, c := range []struct{ content, title, typ string }{
		{"用户希望中文注释", "注释风格", "user"},
		{"另一条也谈注释规范", "注释规范补充", "feedback"},
	} {
		mustRun(t, save, map[string]interface{}{"content": c.content, "title": c.title, "type": c.typ, "scope": "user"})
	}

	out := mustRun(t, NewForgetTool(mc), map[string]interface{}{"target": "注释"})
	if !strings.Contains(out, "命中多条") {
		t.Fatalf("多条命中不应直接删: %q", out)
	}
	entries, _ := st.ListMemories(memstore.ScopeUser, "")
	if len(entries) != 2 {
		t.Fatalf("多条命中时不得删除任何一条, 实际剩 %d 条", len(entries))
	}
	for _, e := range entries {
		if !strings.Contains(out, e.Meta.ID) {
			t.Errorf("列出的候选应带 id（模型要用它精确删除）: %q 缺 %s", out, e.Meta.ID)
		}
	}
	// 无命中：也要给可读结果，而不是报错
	if out = mustRun(t, NewForgetTool(mc), map[string]interface{}{"target": "完全不存在的词"}); !strings.Contains(out, "没有找到可删除的记忆") {
		t.Fatalf("无命中回执: %q", out)
	}
}

func TestToolsFailClosedWithoutStore(t *testing.T) {
	// 未接线（Store 为 nil）时三个工具都必须报错，绝不静默成功
	cases := []struct {
		name string
		tl   tool.ToolInterface
		args map[string]interface{}
	}{
		{"search", NewSearchTool(ToolContext{}), map[string]interface{}{"query": "x"}},
		{"save", NewSaveTool(ToolContext{}), map[string]interface{}{"content": "x"}},
		{"forget", NewForgetTool(ToolContext{}), map[string]interface{}{"target": "x"}},
	}
	for _, c := range cases {
		out, err := runTool(t, c.tl, c.args)
		if err == nil || !strings.Contains(err.Error(), "记忆库不可用") {
			t.Errorf("%s: out=%q err=%v, want 记忆库不可用", c.name, out, err)
		}
	}
}

func TestRequiredArgsAndStoreErrors(t *testing.T) {
	st := newTestStore(t)
	mc := ToolContext{Store: st}
	for _, c := range []struct {
		tl   tool.ToolInterface
		args map[string]interface{}
		want string
	}{
		{NewSearchTool(mc), map[string]interface{}{"query": "   "}, "query 是必需的"},
		{NewSearchTool(mc), map[string]interface{}{}, "query 是必需的"},
		{NewSaveTool(mc), map[string]interface{}{"content": "\n\t "}, "content 是必需的"},
		{NewForgetTool(mc), map[string]interface{}{"target": ""}, "target 是必需的"},
	} {
		_, err := runTool(t, c.tl, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v, want %s", c.tl.GetName(), err, c.want)
		}
	}

	// id 分支要如实透出记忆库的错误（"不存在"不能被吞成空结果）
	if _, err := runTool(t, NewSearchTool(mc), map[string]interface{}{"query": "mem_nope"}); err == nil {
		t.Error("按不存在的 id 检索应报错")
	}
	if _, err := runTool(t, NewForgetTool(mc), map[string]interface{}{"target": "mem_nope"}); err == nil {
		t.Error("按不存在的 id 删除应报错")
	}
}
