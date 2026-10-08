package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	"wails-tmp/internal/git"
)

// newTestRepo 建一个临时 git 仓库（环境没有 git 时跳过）。
//
// 拆包前它定义在 diff_test.go 里；diff 域摘到 internal/diff 之后，根包中
// checkpoint / undo / subagent 的测试仍需要一个真实仓库（回退、基线、归因都靠它），
// 所以在这里保留一份。
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
