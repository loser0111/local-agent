package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"wails-tmp/memory"
)

func newMemoryTestApp(t *testing.T) (*App, *Session) {
	t.Helper()
	app := &App{}
	base := t.TempDir()
	app.sessionStore = NewSessionStore(filepath.Join(base, "sessions"))
	app.modelStore = NewModelStore(filepath.Join(base, "models.json"))
	app.toolStore = NewToolStore(filepath.Join(base, "tools.json"))
	app.toolManager = NewToolManager(app.toolStore, nil)
	store, err := memory.NewMemoryStore(filepath.Join(base, "memory"), memory.MemoryConfig{StalenessCaveat: true})
	if err != nil {
		t.Fatal(err)
	}
	app.memory = store
	if err := app.modelStore.AddModel(Model{Name: "mock", URL: "http://127.0.0.1:9", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	sess, err := app.sessionStore.CreateSession(SessionConfig{Title: "记忆测试", Model: "mock", Project: filepath.Join(base, "proj")})
	if err != nil {
		t.Fatal(err)
	}
	return app, sess
}

func TestRememberForgetAndListCommands(t *testing.T) {
	app, sess := newMemoryTestApp(t)

	res, err := app.Chat(sess.ID, "/remember 我用 Go，回复别客套", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Reply, "已记住") {
		t.Fatalf("应确认已记住: %q", res.Reply)
	}

	list, err := app.Chat(sess.ID, "/memory", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Reply, "Go") && !strings.Contains(list.Reply, "别客套") && !strings.Contains(list.Reply, "mem_") {
		t.Fatalf("列表应含刚写下的记忆: %q", list.Reply)
	}

	other, err := app.sessionStore.CreateSession(SessionConfig{Title: "另一会话", Model: "mock", Project: sess.Project})
	if err != nil {
		t.Fatal(err)
	}
	prompt := app.buildBasePrompt(other, sess.Project)
	if !strings.Contains(prompt, "长期记忆索引") {
		t.Fatalf("新会话系统提示应含 L1 索引: %q", prompt)
	}

	// 取出 id 再删除
	entries, _ := app.memory.ListMemories(memory.ScopeUser, "")
	if len(entries) == 0 {
		t.Fatal("应有一条用户记忆")
	}
	id := entries[0].Meta.ID
	forget, err := app.Chat(sess.ID, "/forget "+id, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(forget.Reply, "已删除") {
		t.Fatalf("应删除: %q", forget.Reply)
	}
	prompt2 := app.buildBasePrompt(other, sess.Project)
	if strings.Contains(prompt2, id) {
		t.Fatalf("删除后索引不应再出现: %q", prompt2)
	}
}

func TestIgnoreMemorySkipsIndexAndRecall(t *testing.T) {
	app, sess := newMemoryTestApp(t)
	if _, err := app.Chat(sess.ID, "/remember 跨会话内容 ginkgo", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Chat(sess.ID, "/ignore-memory", false, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := app.sessionStore.GetSession(sess.ID)
	if err != nil || !loaded.IgnoreMemory {
		t.Fatalf("应置 IgnoreMemory: %+v %v", loaded, err)
	}
	prompt := app.buildBasePrompt(loaded, sess.Project)
	if !strings.Contains(prompt, "不使用记忆") {
		t.Fatalf("忽略时应提示关闭: %q", prompt)
	}
	if strings.Contains(prompt, "本轮召回") {
		t.Fatalf("忽略时不应召回正文: %q", prompt)
	}
	recalled := app.attachMemoryRecall(loaded, "ginkgo", prompt)
	if strings.Contains(recalled, "本轮召回") {
		t.Fatalf("忽略时 attachMemoryRecall 应为空操作: %q", recalled)
	}
	if _, err := app.Chat(sess.ID, "/remember 仍可写入", false, false); err != nil {
		t.Fatal(err)
	}
	on, err := app.Chat(sess.ID, "/memory on", false, false)
	if err != nil || !strings.Contains(on.Reply, "恢复") {
		t.Fatalf("应能恢复: %q %v", on.Reply, err)
	}
}

func TestRecallSurfacedThenClearedOnCompact(t *testing.T) {
	app, sess := newMemoryTestApp(t)
	got, err := app.memory.AddMemory(memory.MemoryMeta{
		Title: "ginkgo 约定", Description: "本项目用 ginkgo", Type: memory.TypeProject,
		Scope: memory.ScopeProject, Project: memoryProjectSlug(sess.Project), Tags: []string{"ginkgo"},
	}, "测试框架用 ginkgo。")
	if err != nil {
		t.Fatal(err)
	}
	prompt := app.attachMemoryRecall(sess, "ginkgo 测试怎么跑", app.buildBasePrompt(sess, sess.Project))
	if !strings.Contains(prompt, "测试框架用 ginkgo") {
		t.Fatalf("首轮应召回正文: %q", prompt)
	}
	reloaded, _ := app.sessionStore.GetSession(sess.ID)
	if len(reloaded.SurfacedMemoryIDs) == 0 {
		t.Fatal("应记下已展示 id")
	}
	again := app.attachMemoryRecall(reloaded, "ginkgo 测试怎么跑", "BASE")
	if strings.Contains(again, got.ID) {
		t.Fatalf("已展示的不应再召回: %q", again)
	}

	if err := app.sessionStore.SetContextSummary(sess.ID, "摘要", 2, 1); err != nil {
		t.Fatal(err)
	}
	after, _ := app.sessionStore.GetSession(sess.ID)
	if len(after.SurfacedMemoryIDs) != 0 {
		t.Fatalf("压缩后应清空 surfaced, got %v", after.SurfacedMemoryIDs)
	}
}

func TestMemoryToolsDirectAndAutoAllow(t *testing.T) {
	app, sess := newMemoryTestApp(t)
	view := app.toolManager.BuildView(context.Background(), BuildOptions{
		Enforcer:      AllowAllEnforcer{},
		Memory:        app.memory,
		MemoryProject: memoryProjectSlug(sess.Project),
		SessionID:     sess.ID,
	})
	names := map[string]bool{}
	for _, d := range view.GetToolsForLLM() {
		names[d.Function.Name] = true
	}
	for _, n := range []string{toolMemorySearch, toolMemorySave, toolMemoryForget} {
		if !names[n] {
			t.Fatalf("%s 应直出，实际 %v", n, names)
		}
	}

	out, err := view.ExecuteTool(toolMemorySave, map[string]interface{}{
		"content": "用户希望中文注释", "title": "注释风格", "type": "user", "scope": "user",
	})
	if err != nil {
		t.Fatalf("memory_save: %v", err)
	}
	if !strings.Contains(out, "已保存") {
		t.Fatalf("save 回执: %q", out)
	}
	found, err := view.ExecuteTool(toolMemorySearch, map[string]interface{}{"query": "中文注释"})
	if err != nil || !strings.Contains(found, "中文注释") {
		t.Fatalf("search: %q %v", found, err)
	}

	// 多条 forget 只列出
	_, _ = view.ExecuteTool(toolMemorySave, map[string]interface{}{
		"content": "另一条也谈注释规范", "title": "注释规范补充", "type": "feedback", "scope": "user", "tags": "comment",
	})
	multi, err := view.ExecuteTool(toolMemoryForget, map[string]interface{}{"target": "注释"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(multi, "命中多条") {
		t.Fatalf("多条不应直接删: %q", multi)
	}

	prompt := app.buildBasePrompt(sess, sess.Project)
	for _, n := range []string{toolMemorySearch, toolMemorySave, toolMemoryForget} {
		if !strings.Contains(prompt, n) {
			t.Fatalf("系统提示应点名 %s", n)
		}
		if !names[n] {
			t.Fatalf("提示词点名的 %s 必须出现在 GetToolsForLLM", n)
		}
	}
}

func TestMemorySearchRespectsIgnore(t *testing.T) {
	app, _ := newMemoryTestApp(t)
	_, _ = app.memory.AddMemory(memory.MemoryMeta{Title: "x", Scope: memory.ScopeUser, Type: memory.TypeUser}, "secret-fact")
	view := app.toolManager.BuildView(context.Background(), BuildOptions{
		Enforcer:     AllowAllEnforcer{},
		Memory:       app.memory,
		IgnoreMemory: true,
	})
	out, err := view.ExecuteTool(toolMemorySearch, map[string]interface{}{"query": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "secret-fact") {
		t.Fatalf("忽略时 search 不应返回正文: %q", out)
	}
}

func TestSubagentExcludesMemoryWrites(t *testing.T) {
	app, sess := newMemoryTestApp(t)
	view := app.toolManager.BuildView(context.Background(), BuildOptions{
		Enforcer:      AllowAllEnforcer{},
		Memory:        app.memory,
		MemoryProject: memoryProjectSlug(sess.Project),
		ExcludeTools:  []string{toolAskUser, toolMemorySave, toolMemoryForget},
	})
	names := map[string]bool{}
	for _, d := range view.GetToolsForLLM() {
		names[d.Function.Name] = true
	}
	if names[toolMemorySave] || names[toolMemoryForget] {
		t.Fatalf("子代理不应直出写记忆工具: %v", names)
	}
	if !names[toolMemorySearch] {
		t.Fatal("子代理应能 search")
	}
}
