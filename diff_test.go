package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// T1 基本解析：add/del/context 统计与行号
func TestParseUnifiedDiffBasic(t *testing.T) {
	out := `diff --git a/app.go b/app.go
index 1111111..2222222 100644
--- a/app.go
+++ b/app.go
@@ -1,4 +1,5 @@
 package main
 import "fmt"
-func old() {}
+func new() {}
+func extra() {}
 // end
`
	files := ParseUnifiedDiff(out)
	if len(files) != 1 {
		t.Fatalf("期望 1 个文件，实际 %d", len(files))
	}
	f := files[0]
	if f.Path != "app.go" {
		t.Fatalf("路径错误: %q", f.Path)
	}
	if f.Status != "modified" {
		t.Fatalf("状态错误: %q", f.Status)
	}
	if f.Additions != 2 || f.Deletions != 1 {
		t.Fatalf("增删统计错误: +%d -%d", f.Additions, f.Deletions)
	}
	if len(f.Hunks) != 1 || len(f.Hunks[0].Lines) != 6 {
		t.Fatalf("hunk/行数错误: %+v", f.Hunks)
	}
	// 校验行号（package main(1,1) / import(2,2) / del old=3 / add new=3 / add new=4 // end(4,5)）
	lines := f.Hunks[0].Lines
	if lines[0].Type != "context" || lines[0].OldLineNo != 1 || lines[0].NewLineNo != 1 {
		t.Fatalf("context 行错误: %+v", lines[0])
	}
	if lines[2].Type != "del" || lines[2].OldLineNo != 3 || lines[2].NewLineNo != 0 {
		t.Fatalf("del 行错误: %+v", lines[2])
	}
	if lines[3].Type != "add" || lines[3].NewLineNo != 3 || lines[3].OldLineNo != 0 {
		t.Fatalf("add 行错误: %+v", lines[3])
	}
	if lines[4].Type != "add" || lines[4].NewLineNo != 4 {
		t.Fatalf("第二行 add 行错误: %+v", lines[4])
	}
	if lines[5].Type != "context" || lines[5].OldLineNo != 4 || lines[5].NewLineNo != 5 {
		t.Fatalf("末尾 context 行错误: %+v", lines[5])
	}
}

// T2 头尾歧义：正文中以 --- 开头的行不能被误判为文件头
func TestParseUnifiedDiffAmbiguousHeader(t *testing.T) {
	out := `diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 context line
---- this is content, not a file header
+--- changed content
 last line
`
	files := ParseUnifiedDiff(out)
	if len(files) != 1 {
		t.Fatalf("期望 1 个文件，实际 %d", len(files))
	}
	f := files[0]
	if f.Path != "notes.txt" {
		t.Fatalf("路径被污染: %q", f.Path)
	}
	if f.Additions != 1 || f.Deletions != 1 {
		t.Fatalf("统计错误: +%d -%d", f.Additions, f.Deletions)
	}
}

// T3 新增文件
func TestParseUnifiedDiffAddedFile(t *testing.T) {
	out := `diff --git a/new.go b/new.go
new file mode 100644
index 0000000..9999999
--- /dev/null
+++ b/new.go
@@ -0,0 +1,2 @@
+package main
+
`
	files := ParseUnifiedDiff(out)
	if len(files) != 1 {
		t.Fatalf("期望 1 个文件，实际 %d", len(files))
	}
	if files[0].Status != "added" {
		t.Fatalf("状态错误: %q", files[0].Status)
	}
	if files[0].Path != "new.go" {
		t.Fatalf("路径错误: %q", files[0].Path)
	}
	if files[0].Additions != 2 {
		t.Fatalf("新增行数错误: %d", files[0].Additions)
	}
}

// T4 删除文件
func TestParseUnifiedDiffDeletedFile(t *testing.T) {
	out := `diff --git a/gone.go b/gone.go
deleted file mode 100644
index 9999999..0000000
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package main
-
`
	files := ParseUnifiedDiff(out)
	if len(files) != 1 || files[0].Status != "deleted" {
		t.Fatalf("删除文件解析错误: %+v", files)
	}
	if files[0].Deletions != 2 {
		t.Fatalf("删除行数错误: %d", files[0].Deletions)
	}
}

// T5 重命名
func TestParseUnifiedDiffRenamedFile(t *testing.T) {
	out := `diff --git a/old.txt b/new.txt
similarity index 95%
rename from old.txt
rename to new.txt
index 1111111..2222222 100644
--- a/old.txt
+++ b/new.txt
@@ -1,1 +1,1 @@
-a
+b
`
	files := ParseUnifiedDiff(out)
	if len(files) != 1 {
		t.Fatalf("期望 1 个文件，实际 %d", len(files))
	}
	f := files[0]
	if f.Status != "renamed" || f.OldPath != "old.txt" || f.Path != "new.txt" {
		t.Fatalf("重命名解析错误: %+v", f)
	}
}

// T6 多文件
func TestParseUnifiedDiffMultipleFiles(t *testing.T) {
	out := `diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1,1 +1,1 @@
-x
+y
diff --git a/b.txt b/b.txt
--- a/b.txt
+++ b/b.txt
@@ -1,1 +1,1 @@
-p
+q
`
	files := ParseUnifiedDiff(out)
	if len(files) != 2 {
		t.Fatalf("期望 2 个文件，实际 %d", len(files))
	}
	if files[0].Path != "a.txt" || files[1].Path != "b.txt" {
		t.Fatalf("文件顺序/路径错误: %q %q", files[0].Path, files[1].Path)
	}
}

// 空输入返回空切片而非 nil
func TestParseUnifiedDiffEmpty(t *testing.T) {
	files := ParseUnifiedDiff("")
	if files == nil || len(files) != 0 {
		t.Fatalf("空输入应返回空切片，实际 %#v", files)
	}
}

// 多 hunk 行号连续
func TestParseHunkHeader(t *testing.T) {
	oldStart, newStart := parseHunkHeader("@@ -12,7 +14,9 @@ func main() {")
	if oldStart != 12 || newStart != 14 {
		t.Fatalf("hunk 头解析错误: old=%d new=%d", oldStart, newStart)
	}
	oldStart, newStart = parseHunkHeader("@@ -5 +6 @@")
	if oldStart != 5 || newStart != 6 {
		t.Fatalf("无计数 hunk 头解析错误: old=%d new=%d", oldStart, newStart)
	}
}

// ===== 会话级差异（归因与隔离）=====

// newTestRepo 建一个临时 git 仓库（环境没有 git 时跳过）
func newTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境没有 git，跳过会话级 diff 测试")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := runGit(dir, args...); err != nil {
			t.Skipf("git %v 不可用: %v", args, err)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	writeTestFile(t, filepath.Join(dir, "a.txt"), "line1\nline2\n")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "bee\n")
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

// diffPaths 取出 diff 里的路径（排序，便于断言）
func diffPaths(files []DiffFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

// 归因算法：只有「工具调用执行窗口内」出现的改动才归给该会话，
// 空闲会话不能认领别人期间的改动
func TestDiffServiceSessionIsolation(t *testing.T) {
	dir := newTestRepo(t)
	svc := NewDiffService()

	// 会话 A 开始（仓库里先放一个「早于任何会话」的改动）
	writeTestFile(t, filepath.Join(dir, "user-note.txt"), "先于会话就存在的改动\n")
	svc.EnsureBaseline("A", dir)
	svc.NoteActivity("A", dir, false) // 起始扫描：只建基线

	files, err := svc.DiffForSession("A", dir)
	if err != nil {
		t.Fatalf("DiffForSession 失败: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("首次扫描不应归因既有改动，实际: %v", diffPaths(files))
	}

	// A 的工具调用：改 a.txt + 新建 c.txt（模拟 exec_shell 干的活）
	svc.NoteActivity("A", dir, false)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "line1\nCHANGED-BY-A\n")
	writeTestFile(t, filepath.Join(dir, "c.txt"), "new by A\n")
	svc.NoteActivity("A", dir, true)

	files, _ = svc.DiffForSession("A", dir)
	if got := diffPaths(files); len(got) != 2 || got[0] != "a.txt" || got[1] != "c.txt" {
		t.Fatalf("A 应只看到自己改的两个文件，实际: %v", got)
	}

	// 会话 B 在同一项目里开始
	svc.EnsureBaseline("B", dir)
	svc.NoteActivity("B", dir, false)
	files, _ = svc.DiffForSession("B", dir)
	if len(files) != 0 {
		t.Fatalf("B 刚开场不应看到 A 的改动，实际: %v", diffPaths(files))
	}

	// B 改 b.txt
	svc.NoteActivity("B", dir, false)
	writeTestFile(t, filepath.Join(dir, "b.txt"), "bee\nCHANGED-BY-B\n")
	svc.NoteActivity("B", dir, true)

	bPaths := diffPaths(mustDiff(t, svc, "B", dir))
	if len(bPaths) != 1 || bPaths[0] != "b.txt" {
		t.Fatalf("B 应只看到 b.txt（A 的 a.txt / 未跟踪的 c.txt 都不能串进来），实际: %v", bPaths)
	}
	if aPaths := diffPaths(mustDiff(t, svc, "A", dir)); len(aPaths) != 2 {
		t.Fatalf("A 的视图不应被 B 影响，实际: %v", aPaths)
	}

	// 关键：A 空闲一轮后再次开工，起始扫描不能把 B 的改动算到 A 头上
	svc.NoteActivity("A", dir, false)
	if aPaths := diffPaths(mustDiff(t, svc, "A", dir)); len(aPaths) != 2 {
		t.Fatalf("A 空闲期间的他人改动不应归给 A，实际: %v", aPaths)
	}

	// A 删除 b.txt（B 用过的文件）→ 应归因给 A，且 B 也能看到内容变了
	svc.NoteActivity("A", dir, false)
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	svc.NoteActivity("A", dir, true)
	aPaths := diffPaths(mustDiff(t, svc, "A", dir))
	if len(aPaths) != 3 {
		t.Fatalf("A 应看到被删除的 b.txt，实际: %v", aPaths)
	}
}

func mustDiff(t *testing.T, svc *DiffService, sessionID, dir string) []DiffFile {
	t.Helper()
	files, err := svc.DiffForSession(sessionID, dir)
	if err != nil {
		t.Fatalf("DiffForSession(%s) 失败: %v", sessionID, err)
	}
	return files
}

// 重启后从会话文件恢复状态，仍能算出同一份会话级 diff（而不是整个工作区）
func TestDiffServiceSnapshotRestore(t *testing.T) {
	dir := newTestRepo(t)
	svc := NewDiffService()
	svc.EnsureBaseline("s1", dir)
	svc.NoteActivity("s1", dir, false)
	writeTestFile(t, filepath.Join(dir, "only-mine.txt"), "mine\n")
	svc.NoteActivity("s1", dir, true)

	base, touched := svc.SnapshotState("s1")
	if base == "" || len(touched) != 1 || touched[0] != "only-mine.txt" {
		t.Fatalf("快照异常: base=%q touched=%v", base, touched)
	}

	// 另一个工作区此时也有一堆改动，但恢复后的会话视图不应把它们算进来
	writeTestFile(t, filepath.Join(dir, "someone-else.txt"), "other\n")

	fresh := NewDiffService()
	fresh.RestoreSession("s1", base, touched)
	got := diffPaths(mustDiff(t, fresh, "s1", dir))
	if len(got) != 1 || got[0] != "only-mine.txt" {
		t.Fatalf("恢复后应仍只含本会话改动，实际: %v", got)
	}

	// 没有恢复任何状态时，不应把整个工作区当成"本会话改动"
	empty := NewDiffService()
	if files := mustDiff(t, empty, "s1", dir); len(files) != 0 {
		t.Fatalf("无状态时应返回空，实际: %v", diffPaths(files))
	}
}

// 本轮归因集合：BeginTurn 清空、TurnTouched 取出并清空
func TestDiffServiceTurnTouched(t *testing.T) {
	dir := newTestRepo(t)
	svc := NewDiffService()
	svc.EnsureBaseline("s1", dir)
	svc.BeginTurn("s1")
	svc.NoteActivity("s1", dir, false) // 建基线
	writeTestFile(t, filepath.Join(dir, "x.txt"), "x\n")
	svc.NoteActivity("s1", dir, true)

	if got := svc.TurnTouched("s1"); len(got) != 1 || got[0] != "x.txt" {
		t.Fatalf("本轮应含 x.txt，实际: %v", got)
	}
	if got := svc.TurnTouched("s1"); len(got) != 0 {
		t.Fatalf("取出后应清空，实际: %v", got)
	}
	// 全局的会话归因不受影响
	if got := svc.Touched("s1"); len(got) != 1 {
		t.Fatalf("会话累计归因应保留 x.txt，实际: %v", got)
	}
}

// git status --porcelain -z 的路径解析（重命名条目会多带一个源路径字段）
func TestParsePorcelainPaths(t *testing.T) {
	// 普通条目 + 已暂存改名（R 带源路径）+ 未跟踪 + 删除
	out := " M a.txt\x00" + "R  renamed.txt\x00original.txt\x00" + "?? newdir/\x00" + " D gone.txt\x00"
	got := parsePorcelainPaths(out)
	want := []string{"a.txt", "renamed.txt", "newdir/", "gone.txt"}
	if len(got) != len(want) {
		t.Fatalf("解析出 %d 个路径，期望 %d：%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个路径应为 %q，实际 %q（全部: %v）", i, want[i], got[i], got)
		}
	}
	if got := parsePorcelainPaths(""); len(got) != 0 {
		t.Fatalf("空输入应返回空，实际: %v", got)
	}
}

// pathspec 数量上限与排序稳定性
func TestCapPathsAndSortedKeys(t *testing.T) {
	many := make([]string, maxDiffFiles+50)
	for i := range many {
		many[i] = fmt.Sprintf("f%03d.txt", i)
	}
	if got := capPaths(many); len(got) != maxDiffFiles {
		t.Fatalf("应截断到 %d，实际 %d", maxDiffFiles, len(got))
	}
	if got := capPaths(many[:5]); len(got) != 5 {
		t.Fatalf("未超限时不应改动，实际 %d", len(got))
	}
	set := map[string]bool{"b": true, "a": true, "c": true}
	got := sortedKeys(set)
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("sortedKeys 结果应稳定排序，实际: %v", got)
	}
}
