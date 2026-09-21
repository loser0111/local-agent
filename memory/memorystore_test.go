package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const dayMs = 24 * 60 * 60 * 1000

func newStore(t *testing.T, cfg MemoryConfig) (*MemoryStore, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := NewMemoryStore(dir, cfg)
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	return st, dir
}

func mustAdd(t *testing.T, st *MemoryStore, m MemoryMeta, content string) MemoryMeta {
	t.Helper()
	got, err := st.AddMemory(m, content)
	if err != nil {
		t.Fatalf("AddMemory(%q): %v", m.Title, err)
	}
	return got
}

// ---- CRUD + 中文主题文件 ----

func TestAddListUpdateDelete(t *testing.T) {
	st, dir := newStore(t, MemoryConfig{})

	got, err := st.AddMemory(MemoryMeta{
		Title:       "用户注释风格",
		Description: "偏好中文注释、camelCase 命名",
		Type:        TypeUser,
		Scope:       ScopeUser,
		Tags:        []string{"coding-style", "comment"},
		Importance:  ImportanceHigh,
		Source:      "sess_1",
	}, "用户习惯用中文写注释。")
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if !strings.HasPrefix(got.ID, "mem_") {
		t.Fatalf("id 前缀应为 mem_, got %q", got.ID)
	}
	if got.Path == "" {
		t.Fatal("path 不应为空")
	}
	full := filepath.Join(dir, got.Path)
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("主题文件未生成: %v", err)
	}

	list, err := st.ListMemories(ScopeUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1, got %d", len(list))
	}
	if list[0].Content != "用户习惯用中文写注释。" {
		t.Fatalf("content=%q", list[0].Content)
	}
	if list[0].Meta.Title != "用户注释风格" || list[0].Meta.Tags == nil {
		t.Fatalf("meta 丢失: %+v", list[0].Meta)
	}

	// 更新正文
	if err := st.UpdateMemory(got.ID, "更新：注释用中文，简洁。"); err != nil {
		t.Fatal(err)
	}
	list, _ = st.ListMemories("", "")
	if len(list) != 1 || list[0].Content != "更新：注释用中文，简洁。" {
		t.Fatalf("更新后 content=%q", list[0].Content)
	}

	// 删除：索引与主题文件都要清掉
	if err := st.DeleteMemory(got.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatalf("删除后主题文件应消失, err=%v", err)
	}
	if list, _ := st.ListMemories("", ""); len(list) != 0 {
		t.Fatalf("删除后应为空, got %d", len(list))
	}

	// 不存在的 id
	if err := st.DeleteMemory("mem_nope"); err == nil {
		t.Fatal("删除不存在应报错")
	}
	if err := st.UpdateMemory("mem_nope", "x"); err == nil {
		t.Fatal("更新不存在应报错")
	}
}

func TestAddMemoryDerivesTitle(t *testing.T) {
	st, _ := newStore(t, MemoryConfig{})
	got, err := st.AddMemory(MemoryMeta{Scope: ScopeUser, Type: TypeUser}, "这是一条没有标题的记忆内容")
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if got.Title == "" {
		t.Fatal("应自动推导标题")
	}
	if got.Description == "" {
		t.Fatal("应自动推导描述")
	}
}

// ---- 去重合并 ----

func TestAddMemoryDedupMergesContent(t *testing.T) {
	st, _ := newStore(t, MemoryConfig{})
	m := MemoryMeta{
		Title:       "项目测试框架",
		Description: "本项目用 ginkgo 做测试",
		Type:        TypeProject,
		Scope:       ScopeProject,
		Project:     "local-agent",
		Tags:        []string{"test", "ginkgo"},
	}
	a := mustAdd(t, st, m, "测试框架用 ginkgo。")
	b := mustAdd(t, st, m, "用例放 *_spec.go。")
	if a.ID != b.ID {
		t.Fatalf("相似记忆应合并，id 不同: %s vs %s", a.ID, b.ID)
	}
	list, _ := st.ListMemories(ScopeProject, "local-agent")
	if len(list) != 1 {
		t.Fatalf("合并后应 1 条, got %d", len(list))
	}
	if !strings.Contains(list[0].Content, "ginkgo") || !strings.Contains(list[0].Content, "*_spec.go") {
		t.Fatalf("合并正文应保留两段, got %q", list[0].Content)
	}
}

// ---- 检索 ----

func TestRetrieveScopeAndRanking(t *testing.T) {
	st, _ := newStore(t, MemoryConfig{})
	mustAdd(t, st, MemoryMeta{Title: "用户注释风格", Description: "偏好中文注释", Type: TypeUser, Scope: ScopeUser, Tags: []string{"style"}}, "x")
	mustAdd(t, st, MemoryMeta{Title: "甲项目测试框架", Description: "甲项目用ginkgo", Type: TypeProject, Scope: ScopeProject, Project: "projA", Tags: []string{"ginkgo", "alpha"}}, "x")
	mustAdd(t, st, MemoryMeta{Title: "乙项目构建工具", Description: "乙项目用ginkgo", Type: TypeProject, Scope: ScopeProject, Project: "projB", Tags: []string{"ginkgo", "beta"}}, "x")

	res, err := st.Retrieve("ginkgo", "projA", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Meta.Project != "projA" {
		t.Fatalf("应只命中 projA, got %+v", res)
	}

	if all, _ := st.Retrieve("ginkgo", "", 10); len(all) != 2 {
		t.Fatalf("项目 slug 为空应命中两个项目级, got %d", len(all))
	}

	if r, _ := st.Retrieve("", "projA", 5); len(r) != 0 {
		t.Fatalf("空查询应返回空, got %d", len(r))
	}

	if one, _ := st.Retrieve("ginkgo", "", 1); len(one) != 1 {
		t.Fatalf("topK=1 应返回 1, got %d", len(one))
	}
}

func TestRetrieveSkipsExpired(t *testing.T) {
	st, _ := newStore(t, MemoryConfig{})
	past := time.Now().Add(-time.Hour).UnixMilli()
	mustAdd(t, st, MemoryMeta{Title: "过期记忆", Description: "expired ginkgo", Type: TypeProject, Scope: ScopeProject, Project: "p", Tags: []string{"ginkgo"}, ExpiresAt: past}, "x")
	if res, _ := st.Retrieve("ginkgo", "p", 10); len(res) != 0 {
		t.Fatalf("过期记忆不应被检索, got %d", len(res))
	}
}

// ---- PII ----

func TestPIIFilter(t *testing.T) {
	st, _ := newStore(t, MemoryConfig{PIIFilter: true})
	if _, err := st.AddMemory(MemoryMeta{Title: "contact", Scope: ScopeUser, Type: TypeUser}, "邮箱是 alice@example.com"); err == nil {
		t.Fatal("含邮箱应被 PII 拦截")
	}
	if _, err := st.AddMemory(MemoryMeta{Title: "phone", Scope: ScopeUser, Type: TypeUser}, "手机号 13800138000"); err == nil {
		t.Fatal("含手机号应被 PII 拦截")
	}

	st2, _ := newStore(t, MemoryConfig{PIIFilter: false})
	if _, err := st2.AddMemory(MemoryMeta{Title: "contact", Scope: ScopeUser, Type: TypeUser}, "邮箱是 alice@example.com"); err != nil {
		t.Fatalf("关闭 PII 过滤后不应拦截: %v", err)
	}
}

// ---- 路径安全 ----

func TestPathSafety(t *testing.T) {
	for _, p := range []string{"../etc/passwd", "..\\..\\windows", "a/../../b", ""} {
		if _, err := sanitizeRelPath(p); err == nil {
			t.Errorf("sanitizeRelPath(%q) 应报错", p)
		}
	}
	for in, want := range map[string]string{
		"docs/报告.md": "docs/报告.md",
		"a/b/c.md":   "a/b/c.md",
	} {
		got, err := sanitizeRelPath(in)
		if err != nil || got != want {
			t.Errorf("sanitizeRelPath(%q)=%q,%v want %q", in, got, err, want)
		}
	}
	if isSafeRelSegment("..") || isSafeRelSegment("") {
		t.Error(".. 与空段应被视为不安全")
	}
	if !isSafeRelSegment("abc_1-2") {
		t.Error("abc_1-2 应安全")
	}

	if _, err := themePathFor(ScopeProject, "", "x"); err == nil {
		t.Error("project 作用域缺 project slug 应报错")
	}
	if _, err := themePathFor("bogus", "", "x"); err == nil {
		t.Error("未知作用域应报错")
	}
	if p, err := themePathFor(ScopeUser, "", "用户注释风格"); err != nil || p != "user/用户注释风格.md" {
		t.Errorf("CJK 主题路径 = %q, %v", p, err)
	}
}

// ---- 持久化 ----

func TestPersistenceReload(t *testing.T) {
	dir := t.TempDir()
	st, err := NewMemoryStore(dir, MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.AddMemory(MemoryMeta{Title: "记住我", Scope: ScopeUser, Type: TypeUser}, "跨会话内容"); err != nil {
		t.Fatal(err)
	}

	st2, err := NewMemoryStore(dir, MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := st2.ListMemories("", "")
	if len(list) != 1 {
		t.Fatalf("重载后应 1 条, got %d", len(list))
	}
	if list[0].Content != "跨会话内容" || list[0].Meta.Title != "记住我" {
		t.Fatalf("重载丢失数据: %+v", list[0])
	}
}

// ---- 注入 ----

func TestFormatInjection(t *testing.T) {
	st, _ := newStore(t, MemoryConfig{StalenessCaveat: true})
	if got := st.FormatInjection(nil); got != "" {
		t.Fatalf("空输入应返回空串, got %q", got)
	}
	entries := []*MemoryEntry{{
		Meta: MemoryMeta{Title: "用户偏好", Description: "中文注释", Scope: ScopeUser, Importance: ImportanceHigh, UpdatedAt: time.Now().UnixMilli()},
		Age:  "today",
	}}
	out := st.FormatInjection(entries)
	if !strings.Contains(out, "## 长期记忆") || !strings.Contains(out, "用户偏好") {
		t.Fatalf("注入块内容异常: %q", out)
	}
	if !strings.HasSuffix(out, "核实。") {
		t.Fatalf("应带 drift caveat 结尾: %q", out)
	}
}

func TestFormatInjectionBudgetAndStale(t *testing.T) {
	st, _ := newStore(t, MemoryConfig{InjectionMaxChars: 300, StalenessCaveat: true})
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	var entries []*MemoryEntry
	for i := 0; i < 20; i++ {
		entries = append(entries, &MemoryEntry{
			Meta: MemoryMeta{Title: "项目知识条目", Description: "用于预算截断测试的描述文本", Scope: ScopeProject, Project: "local-agent", UpdatedAt: old},
			Age:  "2 days ago", Stale: true,
		})
	}
	out := st.FormatInjection(entries)
	if !strings.Contains(out, "已显示 top") {
		t.Fatalf("应触发预算截断提示: %q", out)
	}
	if !strings.Contains(out, "注意") {
		t.Fatalf("stale 应触发 drift caveat: %q", out)
	}
}

// ---- 统计 ----

func TestStats(t *testing.T) {
	st, _ := newStore(t, MemoryConfig{})
	mustAdd(t, st, MemoryMeta{Title: "u1", Scope: ScopeUser, Type: TypeUser}, "a")
	mustAdd(t, st, MemoryMeta{Title: "pa1", Scope: ScopeProject, Project: "p1", Type: TypeProject, Tags: []string{"x"}}, "b")
	mustAdd(t, st, MemoryMeta{Title: "pb1", Scope: ScopeProject, Project: "p2", Type: TypeProject, Tags: []string{"y"}}, "c")
	s := st.Stats()
	if s["total"] != 3 {
		t.Fatalf("total=%d", s["total"])
	}
	if s["user"] != 1 {
		t.Fatalf("user=%d", s["user"])
	}
	if s["project/p1"] != 1 {
		t.Fatalf("project/p1=%d", s["project/p1"])
	}
}

// ---- 辅助函数 ----

func TestRelAgeAndHelpers(t *testing.T) {
	now := int64(1_000_000_000_000)
	if relAge(now, now) != "today" {
		t.Fatal("today")
	}
	if relAge(now-dayMs, now) != "yesterday" {
		t.Fatal("yesterday")
	}
	if relAge(now-3*dayMs, now) != "3 days ago" {
		t.Fatal("3 days ago")
	}
	if jaccard(nil, nil) != 0 {
		t.Fatal("空集合 jaccard 应为 0")
	}
	if jaccard([]string{"a", "b"}, []string{"a", "b"}) != 1 {
		t.Fatal("相同集合 jaccard 应为 1")
	}
	toks := strings.Join(tokenize("Hello 世界 123"), ",")
	if !strings.Contains(toks, "hello") || !strings.Contains(toks, "世") || !strings.Contains(toks, "123") {
		t.Fatalf("tokenize=%q", toks)
	}
}

// ---- 并发安全 ----

func TestConcurrentAdd(t *testing.T) {
	st, _ := newStore(t, MemoryConfig{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = st.AddMemory(MemoryMeta{
				Title: fmt.Sprintf("并发记忆%d", n),
				Scope: ScopeUser, Type: TypeUser,
				Tags: []string{fmt.Sprintf("tag%d", n)},
			}, "c")
		}(i)
	}
	wg.Wait()
	if list, _ := st.ListMemories("", ""); len(list) != 20 {
		t.Fatalf("并发写入后应 20 条, got %d", len(list))
	}
}
