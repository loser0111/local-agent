package snapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"wails-tmp/internal/git"
)

// 下面两个助手是**从根包测试里复制过来的副本**，不是共享。
//
// 拆包之后测试助手无法跨包引用：它们定义在根包的 *_test.go 里，而本包既不能
// import 根包（`package main` 不可被 import），也不该为了测试反过来依赖上层。
// helper 本身只是"写个临时文件""建个临时仓库"这种无业务语义的动作，复制一份
// 比引入一层共享测试包更省事，也不会在两边漂移——它们够简单，几乎不会变。
//
// internal/diff/helpers_test.go 里有同名的同样副本，是同样的理由。

// writeTestFile 写一个测试文件，自动建父目录。
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newTestRepo 建一个临时 git 仓库（环境没有 git 时跳过）。
func newTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境没有 git，跳过依赖真实仓库的测试")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := git.Run(dir, args...); err != nil {
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
