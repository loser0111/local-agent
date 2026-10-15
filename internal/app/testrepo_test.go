package app

import (
	"os"
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

// cpReadFile 读工作区文件内容。
//
// 原先定义在 checkpoint_test.go 里，但它并不是快照包私有的东西——它被根包的
// undo_test.go 大量使用（回退前后的内容比对）。快照测试随包迁走后，这里补一份副本。
func cpReadFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", rel, err)
	}
	return string(data)
}
