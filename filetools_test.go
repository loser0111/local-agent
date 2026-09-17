package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFileToolCtx 构造临时工作区上的文件工具上下文
func newFileToolCtx(t *testing.T) (fileToolContext, string) {
	t.Helper()
	dir := t.TempDir()
	return fileToolContext{root: dir, sessionID: "s1", changes: NewFileChangeLog(50)}, dir
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ===== 路径解析 =====

func TestFileToolContextResolve(t *testing.T) {
	fc, root := newFileToolCtx(t)

	abs, err := fc.resolve("src/main.go")
	if err != nil {
		t.Fatalf("相对路径解析失败: %v", err)
	}
	if abs != filepath.Join(root, "src", "main.go") {
		t.Fatalf("相对路径应基于工作区目录，实际 %s", abs)
	}

	abs2, err := fc.resolve("/tmp/abs.go")
	if err != nil || abs2 != "/tmp/abs.go" {
		t.Fatalf("绝对路径应原样返回，实际 %s err=%v", abs2, err)
	}

	if _, err := fc.resolve("  "); err == nil {
		t.Fatal("空路径应报错")
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		got, err := fc.resolve("~/x.txt")
		if err != nil || got != filepath.Join(home, "x.txt") {
			t.Fatalf("~ 应展开为家目录，实际 %s err=%v", got, err)
		}
	}

	// 展示路径：工作区内用相对路径，外部用绝对路径
	if rel := fc.rel(filepath.Join(root, "a", "b.txt")); rel != "a/b.txt" {
		t.Fatalf("rel 应为 a/b.txt，实际 %s", rel)
	}
	if rel := fc.rel("/etc/hosts"); rel != "/etc/hosts" {
		t.Fatalf("工作区外应返回绝对路径，实际 %s", rel)
	}
}

// ===== read_file =====

func TestReadFileTool(t *testing.T) {
	fc, root := newFileToolCtx(t)
	tool := newReadFileTool(buildContext{fileCtx: fc})
	writeTestFile(t, filepath.Join(root, "a.txt"), "第一行\n第二行\n第三行\n")

	out, err := tool.Execute(context.Background(), map[string]interface{}{"path": "a.txt"})
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	for _, want := range []string{"a.txt", "共 3 行", "1\t第一行", "3\t第三行"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q，实际:\n%s", want, out)
		}
	}

	// offset/limit
	out, err = tool.Execute(context.Background(), map[string]interface{}{
		"path": "a.txt", "offset": float64(2), "limit": float64(1),
	})
	if err != nil {
		t.Fatalf("分片读取失败: %v", err)
	}
	if !strings.Contains(out, "显示 2-2") || !strings.Contains(out, "第二行") {
		t.Fatalf("分片读取结果异常:\n%s", out)
	}
	if strings.Contains(out, "第一行") {
		t.Fatalf("分片读取不应包含范围外内容:\n%s", out)
	}

	// 缺文件 / 目录 / 空路径
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"path": "nope.txt"}); err == nil {
		t.Error("不存在的文件应报错")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"path": "."}); err == nil {
		t.Error("目录应报错并提示用 list_dir")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"path": ""}); err == nil {
		t.Error("空路径应报错")
	}

	// 二进制文件
	binPath := filepath.Join(root, "b.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"path": "b.bin"}); err == nil {
		t.Error("二进制文件应被拒绝")
	}
}

// ===== write_file =====

func TestWriteFileTool(t *testing.T) {
	fc, root := newFileToolCtx(t)
	tool := newWriteFileTool(buildContext{fileCtx: fc})

	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": "src/new/file.txt", "content": "hello\nworld\n",
	})
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if !strings.Contains(out, "已新建") {
		t.Errorf("首次写入应提示已新建，实际: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(root, "src", "new", "file.txt"))
	if err != nil || string(data) != "hello\nworld\n" {
		t.Fatalf("落盘内容不对: %q err=%v", string(data), err)
	}

	// 覆盖：应提示已覆盖，并记录改动（含新增/删除行数）
	out, err = tool.Execute(context.Background(), map[string]interface{}{
		"path": "src/new/file.txt", "content": "hello\nchanged\nmore\n",
	})
	if err != nil {
		t.Fatalf("覆盖失败: %v", err)
	}
	if !strings.Contains(out, "已覆盖") {
		t.Errorf("覆盖时应提示已覆盖，实际: %s", out)
	}
	changes := fc.changes.List("s1")
	if len(changes) != 2 {
		t.Fatalf("应记录 2 次改动，实际 %d", len(changes))
	}
	last := changes[1]
	if last.Tool != toolWriteFile || last.Rel != "src/new/file.txt" || last.Action != "update" {
		t.Fatalf("归因记录异常: %+v", last)
	}
	if last.Added == 0 || last.Removed == 0 {
		t.Fatalf("增删行数应被统计，实际 +%d -%d", last.Added, last.Removed)
	}
	if !strings.Contains(last.Diff, "@@") {
		t.Fatalf("应保存统一 diff 文本，实际: %q", last.Diff)
	}

	// content 缺失
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"path": "x.txt"}); err == nil {
		t.Error("缺少 content 应报错")
	}
}

// ===== edit_file =====

func TestEditFileTool(t *testing.T) {
	fc, root := newFileToolCtx(t)
	tool := newEditFileTool(buildContext{fileCtx: fc})
	path := filepath.Join(root, "code.go")
	writeTestFile(t, path, "package main\n\nfunc main() {\n\tprintln(1)\n}\n")

	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": "code.go", "old_string": "println(1)", "new_string": "println(2)",
	})
	if err != nil {
		t.Fatalf("替换失败: %v", err)
	}
	if !strings.Contains(out, "替换 1 处") {
		t.Errorf("应提示替换处数，实际: %s", out)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "println(2)") || strings.Contains(string(data), "println(1)") {
		t.Fatalf("文件内容未正确替换: %s", string(data))
	}
	if len(fc.changes.List("s1")) != 1 {
		t.Fatal("编辑应记录一次改动")
	}

	// 找不到
	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": "code.go", "old_string": "不存在的内容", "new_string": "x",
	}); err == nil {
		t.Error("未找到 old_string 应报错")
	}

	// 多次匹配且未开 replace_all
	writeTestFile(t, filepath.Join(root, "dup.txt"), "aa\naa\n")
	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": "dup.txt", "old_string": "aa", "new_string": "bb",
	}); err == nil {
		t.Error("多次匹配应报错提示不唯一")
	}
	// replace_all 生效
	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": "dup.txt", "old_string": "aa", "new_string": "bb", "replace_all": true,
	}); err != nil {
		t.Fatalf("replace_all 应成功: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(root, "dup.txt"))
	if string(data) != "bb\nbb\n" {
		t.Fatalf("replace_all 结果异常: %q", string(data))
	}

	// old == new
	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"path": "dup.txt", "old_string": "bb", "new_string": "bb",
	}); err == nil {
		t.Error("old_string 与 new_string 相同应报错")
	}
}

// ===== glob =====

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "src/main.go", false}, // 单星不跨段
		{"**/*.go", "main.go", true},   // ** 可匹配零个目录
		{"**/*.go", "a/b/main.go", true},
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/a/b.go", false},
		{"src/**/*.ts", "src/a/b/c.ts", true},
		{"**/*_test.go", "x/y_test.go", true},
		{"a?.txt", "ab.txt", true},
		{"a?.txt", "abc.txt", false},
		{"docs/**", "docs/a/b.md", true},
	}
	for _, c := range cases {
		re, err := globToRegexp(c.pattern)
		if err != nil {
			t.Fatalf("globToRegexp(%q) 报错: %v", c.pattern, err)
		}
		if got := re.MatchString(c.path); got != c.want {
			t.Errorf("glob %q 匹配 %q = %v，期望 %v（regex=%s）", c.pattern, c.path, got, c.want, re.String())
		}
	}
	if _, err := globToRegexp(""); err == nil {
		t.Error("空模式应报错")
	}
}

func TestGlobTool(t *testing.T) {
	fc, root := newFileToolCtx(t)
	tool := newGlobTool(buildContext{fileCtx: fc})
	writeTestFile(t, filepath.Join(root, "main.go"), "package main")
	writeTestFile(t, filepath.Join(root, "src", "a.go"), "package a")
	writeTestFile(t, filepath.Join(root, "node_modules", "lib", "x.go"), "package x")
	writeTestFile(t, filepath.Join(root, "notes.md"), "# notes")

	out, err := tool.Execute(context.Background(), map[string]interface{}{"pattern": "**/*.go"})
	if err != nil {
		t.Fatalf("glob 失败: %v", err)
	}
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "src/a.go") {
		t.Fatalf("应匹配到 go 文件，实际:\n%s", out)
	}
	if strings.Contains(out, "node_modules") {
		t.Fatalf("应跳过 node_modules，实际:\n%s", out)
	}
	if strings.Contains(out, "notes.md") {
		t.Fatalf("不应匹配 md 文件，实际:\n%s", out)
	}

	// 无匹配
	out, err = tool.Execute(context.Background(), map[string]interface{}{"pattern": "*.rs"})
	if err != nil || !strings.Contains(out, "没有匹配") {
		t.Fatalf("无匹配时应给出提示，实际: %s err=%v", out, err)
	}
}

// ===== grep =====

func TestGrepTool(t *testing.T) {
	fc, root := newFileToolCtx(t)
	tool := newGrepTool(buildContext{fileCtx: fc})
	writeTestFile(t, filepath.Join(root, "a.go"), "package main\n// TODO: fix\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(root, "b.txt"), "todo later\n")
	writeTestFile(t, filepath.Join(root, ".git", "config"), "TODO in git dir\n")

	// 默认 files_with_matches
	out, err := tool.Execute(context.Background(), map[string]interface{}{"pattern": "TODO"})
	if err != nil {
		t.Fatalf("grep 失败: %v", err)
	}
	if !strings.Contains(out, "a.go") {
		t.Fatalf("应列出匹配文件，实际:\n%s", out)
	}
	if strings.Contains(out, ".git") {
		t.Fatalf("应跳过 .git，实际:\n%s", out)
	}
	if strings.Contains(out, "b.txt") {
		t.Fatalf("大小写敏感时不应匹配 b.txt，实际:\n%s", out)
	}

	// 忽略大小写
	out, _ = tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "todo", "case_insensitive": true,
	})
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.txt") {
		t.Fatalf("忽略大小写应命中两个文件，实际:\n%s", out)
	}

	// content 模式带行号
	out, _ = tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "TODO", "output_mode": "content",
	})
	if !strings.Contains(out, "a.go:2:") {
		t.Fatalf("content 模式应带路径与行号，实际:\n%s", out)
	}

	// count 模式
	out, _ = tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "main", "output_mode": "count",
	})
	if !strings.Contains(out, "a.go: 2") {
		t.Fatalf("count 模式应给出计数，实际:\n%s", out)
	}

	// glob 过滤
	writeTestFile(t, filepath.Join(root, "c.go"), "// TODO in go\n")
	out, _ = tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "TODO", "glob": "*.go",
	})
	if !strings.Contains(out, "a.go") || strings.Contains(out, "b.txt") || strings.Contains(out, "c.go") == false {
		t.Fatalf("glob 过滤结果异常:\n%s", out)
	}

	// 非法正则 / 非法模式 / 空 pattern
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"pattern": "("}); err == nil {
		t.Error("非法正则应报错")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"pattern": "x", "output_mode": "whatever"}); err == nil {
		t.Error("非法 output_mode 应报错")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"pattern": ""}); err == nil {
		t.Error("空 pattern 应报错")
	}
}

// ===== list_dir =====

func TestListDirTool(t *testing.T) {
	fc, root := newFileToolCtx(t)
	tool := newListDirTool(buildContext{fileCtx: fc})
	writeTestFile(t, filepath.Join(root, "a.txt"), "12345")
	writeTestFile(t, filepath.Join(root, "node_modules", "x.js"), "x")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("list_dir 失败: %v", err)
	}
	if !strings.Contains(out, "sub/") {
		t.Fatalf("应列出子目录（带斜杠），实际:\n%s", out)
	}
	if !strings.Contains(out, "a.txt\t5") {
		t.Fatalf("应列出文件与大小，实际:\n%s", out)
	}
	if strings.Contains(out, "node_modules") {
		t.Fatalf("应跳过 node_modules，实际:\n%s", out)
	}

	// 不存在的目录
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"path": "nope"}); err == nil {
		t.Error("不存在的目录应报错")
	}
}

// ===== 权限主体 =====

func TestFileToolPermissionSubjects(t *testing.T) {
	fc, root := newFileToolCtx(t)
	cases := []struct {
		tool   ToolInterface
		args   map[string]interface{}
		kind   SubjectKind
		action string
	}{
		{newReadFileTool(buildContext{fileCtx: fc}), map[string]interface{}{"path": "a.txt"}, SubjectPath, SubjectActionRead},
		{newWriteFileTool(buildContext{fileCtx: fc}), map[string]interface{}{"path": "a.txt", "content": "x"}, SubjectPath, SubjectActionWrite},
		{newEditFileTool(buildContext{fileCtx: fc}), map[string]interface{}{"path": "a.txt", "old_string": "a", "new_string": "b"}, SubjectPath, SubjectActionWrite},
		{newGlobTool(buildContext{fileCtx: fc}), map[string]interface{}{"pattern": "*.go"}, SubjectPath, SubjectActionRead},
		{newGrepTool(buildContext{fileCtx: fc}), map[string]interface{}{"pattern": "x"}, SubjectPath, SubjectActionRead},
		{newListDirTool(buildContext{fileCtx: fc}), map[string]interface{}{}, SubjectPath, SubjectActionRead},
	}
	for _, c := range cases {
		sp, ok := c.tool.(SubjectProvider)
		if !ok {
			t.Fatalf("%s 应实现 SubjectProvider", c.tool.GetName())
		}
		s := sp.PermissionSubject(c.args)
		if s.Kind != c.kind || s.Action != c.action {
			t.Errorf("%s 主体应为 kind=%s action=%s，实际 kind=%s action=%s",
				c.tool.GetName(), c.kind, c.action, s.Kind, s.Action)
		}
		if !s.Trusted {
			t.Errorf("%s 的正常参数应产出可信主体", c.tool.GetName())
		}
		if len(s.Units) != 1 || !strings.HasPrefix(s.Units[0], root) {
			t.Errorf("%s 主体应含解析后的绝对路径，实际 %v", c.tool.GetName(), s.Units)
		}
	}

	// 路径解析失败 → 不可信主体（判定会落到询问）
	sp := newWriteFileTool(buildContext{fileCtx: fc})
	if s := sp.PermissionSubject(map[string]interface{}{"path": ""}); s.Trusted {
		t.Fatal("空路径应产出不可信主体")
	}
}

// ===== 归因日志 =====

func TestFileChangeLog(t *testing.T) {
	log := NewFileChangeLog(3)
	if mark := log.Mark("s1"); mark != 0 {
		t.Fatalf("初始游标应为 0，实际 %d", mark)
	}
	log.Record("s1", FileChange{Tool: "write_file", Rel: "a.go"})
	mark := log.Mark("s1")
	log.Record("s1", FileChange{Tool: "edit_file", Rel: "b.go"})
	log.Record("s1", FileChange{Tool: "edit_file", Rel: "c.go"})
	log.Record("s1", FileChange{Tool: "edit_file", Rel: "d.go"}) // 超出 limit=3

	if got := len(log.List("s1")); got != 3 {
		t.Fatalf("应保留最近 3 条，实际 %d", got)
	}
	since := log.Since("s1", mark)
	if len(since) != 2 {
		t.Fatalf("游标之后应有 2 条，实际 %d", len(since))
	}
	// 其它会话互不影响
	log.Record("s2", FileChange{Tool: "write_file", Rel: "x.go"})
	if len(log.List("s1")) != 3 || len(log.List("s2")) != 1 {
		t.Fatal("会话之间的记录应互相隔离")
	}
	log.Clear("s1")
	if len(log.List("s1")) != 0 {
		t.Fatal("清空后应为空")
	}
}

// ===== 装配与直出 =====

func TestFileToolsAssembledAndDirectExposed(t *testing.T) {
	tm, dir := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		SessionID:  "s1",
		Enforcer:   AllowAllEnforcer{},
	})

	// 六个文件工具 + tool_router 都直出给模型
	defs := view.GetToolsForLLM()
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Function.Name] = true
	}
	for _, want := range append([]string{"tool_router"}, directToolOrder...) {
		if !names[want] {
			t.Errorf("工具定义应包含 %s，实际 %v", want, names)
		}
	}
	// 直出定义要有必填参数信息（否则模型不知道哪些参数必填）
	for _, d := range defs {
		if d.Function.Name == toolWriteFile {
			req := map[string]bool{}
			for _, r := range d.Function.Parameters.Required {
				req[r] = true
			}
			if !req["path"] || !req["content"] {
				t.Errorf("write_file 的必填参数应包含 path 与 content，实际 %v", d.Function.Parameters.Required)
			}
		}
	}

	// 直调与经路由器两条路径都能真正执行
	out, err := view.ExecuteTool(toolListDir, map[string]interface{}{})
	if err != nil {
		t.Fatalf("直调 list_dir 失败: %v", err)
	}
	if !strings.Contains(out, dir) && !strings.Contains(out, "个文件") {
		t.Fatalf("list_dir 输出异常: %s", out)
	}
	if _, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "execute", "tool_name": toolGlob, "arguments": map[string]interface{}{"pattern": "*.txt"},
	}); err != nil {
		t.Fatalf("经 tool_router 调 glob 失败: %v", err)
	}
}

// 文件工具也必须过权限网关（两条路径都不能绕过）
func TestFileToolsAreGuarded(t *testing.T) {
	tm, dir := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{ProjectDir: dir, Enforcer: DenyAllEnforcer{}})

	if _, err := view.ExecuteTool(toolReadFile, map[string]interface{}{"path": "a.txt"}); err == nil {
		t.Fatal("直调 read_file 必须经过权限网关")
	}
	if _, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "execute", "tool_name": toolWriteFile,
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

	store := NewToolStore(filePath)
	names := map[string]bool{}
	for _, c := range store.GetAll() {
		names[c.Name] = true
	}
	for _, want := range append([]string{"exec_shell"}, directToolOrder...) {
		if !names[want] {
			t.Errorf("补齐后应包含 %s，实际 %v", want, names)
		}
	}
	// 已存在的配置不应被重复追加
	if len(store.GetAll()) != 1+len(directToolOrder) {
		t.Fatalf("工具数应为 %d，实际 %d", 1+len(directToolOrder), len(store.GetAll()))
	}
	// 补齐结果应落盘（再次加载不再变化）
	again := NewToolStore(filePath)
	if len(again.GetAll()) != len(store.GetAll()) {
		t.Fatal("补齐结果应持久化")
	}
}
