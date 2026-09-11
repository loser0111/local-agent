package main

import "testing"

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
