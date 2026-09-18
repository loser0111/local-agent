package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== P2 第 1 步：checkpoint 层 =====
//
// 这些测试用**真实 git 仓库**跑（复用 diff_test.go 的 newTestRepo）。
// 它们验证的是 git 机制本身对不对——不是替身、不是模拟。
//
// 回退算法（UndoDiffTurn）的测试在 app 层写完之后单独加，见 checkpoint 的第二批测试。

// cpReadFile 读工作区文件内容
func cpReadFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", rel, err)
	}
	return string(data)
}

// newEmptyRepo 建一个**没有任何提交**的 git 仓库（覆盖 commit-tree 的无父分支）
func newEmptyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := runGit(dir, "init", "-q"); err != nil {
		t.Skipf("git 不可用: %v", err)
	}
	_, _ = runGit(dir, "config", "user.email", "test@example.com")
	_, _ = runGit(dir, "config", "user.name", "test")
	return dir
}

// 快照必须包含未跟踪文件 —— 这是整个撤销功能的地基。
// 若这条不成立，"轮次前就存在但未跟踪的文件"在回退时会被误删。
func TestCheckpointIncludesUntracked(t *testing.T) {
	dir := newTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "modified in turn\n")   // 已跟踪，被改
	writeTestFile(t, filepath.Join(dir, "untracked.txt"), "pre-existing\n") // 未跟踪，本轮改过
	writeTestFile(t, filepath.Join(dir, "added.txt"), "brand new\n")       // 未跟踪，本轮新建

	cp, err := Checkpoint(dir, "sess1", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a.txt", "untracked.txt", "added.txt"} {
		if !pathInCheckpoint(dir, cp, rel) {
			t.Fatalf("快照应包含 %s（未跟踪文件正是 stash create 会漏掉的那类）", rel)
		}
	}
	if got := cpReadFile(t, dir, "a.txt"); got != "modified in turn\n" {
		t.Fatalf("快照应记录轮次开始时的内容，实际 %q", got)
	}

	// 对照：stash create 不含未跟踪文件（这正是本函数存在的理由）
	if stash, err := runGit(dir, "stash", "create"); err == nil {
		stash = strings.TrimSpace(stash)
		if stash != "" && pathInCheckpoint(dir, stash, "untracked.txt") {
			t.Fatal("stash create 竟然包含未跟踪文件？前提变了，checkpoint 的必要性需重新评估")
		}
	}
}

// 取快照不能动真实索引与工作区
func TestCheckpointDoesNotTouchRealIndex(t *testing.T) {
	dir := newTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "modified\n")
	writeTestFile(t, filepath.Join(dir, "untracked.txt"), "u\n")
	writeTestFile(t, filepath.Join(dir, "staged.txt"), "s\n")
	if _, err := runGit(dir, "add", "staged.txt"); err != nil {
		t.Fatal(err)
	}

	before, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Checkpoint(dir, "sess1", 1); err != nil {
		t.Fatal(err)
	}
	after, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("快照不该改动真实索引/工作区：\n前 %q\n后 %q", before, after)
	}
}

// 打了 ref 的快照必须扛得住 gc；ref 本身也要真实存在
func TestCheckpointSurvivesGC(t *testing.T) {
	dir := newTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "modified\n")
	writeTestFile(t, filepath.Join(dir, "untracked.txt"), "u\n")

	cp, err := Checkpoint(dir, "sess1", 1)
	if err != nil {
		t.Fatal(err)
	}

	// ref 必须指向它——"不会被回收"的机制保证在这里
	out, err := runGit(dir, "rev-parse", checkpointRef("sess1", 1))
	if err != nil {
		t.Fatalf("ref 应存在: %v", err)
	}
	if strings.TrimSpace(out) != cp {
		t.Fatalf("ref 应指向快照: %q != %q", strings.TrimSpace(out), cp)
	}

	if _, err := runGit(dir, "gc", "--prune=now"); err != nil {
		t.Skipf("gc 不可用: %v", err)
	}
	if _, err := runGit(dir, "cat-file", "-e", cp); err != nil {
		t.Fatalf("打了 ref 的快照必须扛住 gc，实际被回收: %v", err)
	}

	// 反向对照不做硬断言：悬空 commit 是否被回收与 git 版本/配置有关，
	// 硬断言会让测试在别人的机器上 flaky。而"打 ref"是无条件正确的做法，
	// 上面那条正向断言已经守住了它。
	if dangling, err := runGit(dir, "stash", "create"); err == nil {
		dangling = strings.TrimSpace(dangling)
		if dangling != "" {
			_, _ = runGit(dir, "gc", "--prune=now")
			if _, err := runGit(dir, "cat-file", "-e", dangling); err == nil {
				t.Log("提示：本次环境未回收未打 ref 的悬空 commit（版本相关），不影响上面的结论")
			}
		}
	}
}

// 空仓库（没有 HEAD）也要能取快照
func TestCheckpointOnEmptyRepo(t *testing.T) {
	dir := newEmptyRepo(t)
	writeTestFile(t, filepath.Join(dir, "first.txt"), "first\n")

	cp, err := Checkpoint(dir, "sess1", 1)
	if err != nil {
		t.Fatalf("空仓库应能取快照（commit-tree 去掉 -p 的分支）: %v", err)
	}
	if !pathInCheckpoint(dir, cp, "first.txt") {
		t.Fatal("快照应包含首个文件")
	}
}

// 参数非法时明确报错，不静默产出一个不可用的快照
func TestCheckpointRejectsBadArgs(t *testing.T) {
	dir := newTestRepo(t)
	if _, err := Checkpoint("", "sess1", 1); err == nil {
		t.Fatal("目录为空应报错")
	}
	if _, err := Checkpoint(dir, "", 1); err == nil {
		t.Fatal("会话 ID 为空应报错")
	}
	if _, err := Checkpoint(dir, "bad/id", 1); err == nil {
		t.Fatal("含非法字符的会话 ID 应报错")
	}
	if _, err := Checkpoint(dir, "sess1", 0); err == nil {
		t.Fatal("轮次号 0 应报错")
	}
	// 非仓库目录：git 命令会失败，应返回错误而不是 panic
	plain := t.TempDir()
	if _, err := Checkpoint(plain, "sess1", 1); err == nil {
		t.Fatal("非 git 仓库应报错")
	}
}

func TestValidRefSegment(t *testing.T) {
	valid := []string{"sess1", "a-b_c", "ABC123"}
	invalid := []string{"", ".", "..", "a/b", "a b", "中文", strings.Repeat("x", 129)}
	for _, s := range valid {
		if !validRefSegment(s) {
			t.Fatalf("应判定合法: %q", s)
		}
	}
	for _, s := range invalid {
		if validRefSegment(s) {
			t.Fatalf("应判定非法: %q", s)
		}
	}
}

// ref 的增删与保留策略
func TestDropAndPruneCheckpoints(t *testing.T) {
	dir := newTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "one\n")

	for turn := 1; turn <= 5; turn++ {
		if _, err := Checkpoint(dir, "sess1", turn); err != nil {
			t.Fatalf("第 %d 轮取快照失败: %v", turn, err)
		}
	}
	turns := listSessionCheckpointTurns(dir, "sess1")
	if len(turns) != 5 {
		t.Fatalf("应有 5 个轮次快照，实际 %v", turns)
	}

	// 只保留最近 2 轮
	PruneCheckpoints(dir, "sess1", 2)
	turns = listSessionCheckpointTurns(dir, "sess1")
	if len(turns) != 2 || turns[0] != 4 || turns[1] != 5 {
		t.Fatalf("应只留最近两轮，实际 %v", turns)
	}
	if _, ok := ResolveCheckpoint(dir, "sess1", 3); ok {
		t.Fatal("被清理的轮次不应还能解析")
	}
	if _, ok := ResolveCheckpoint(dir, "sess1", 5); !ok {
		t.Fatal("保留的轮次应能解析")
	}

	// 会话级清理
	if err := DropSessionCheckpoints(dir, "sess1"); err != nil {
		t.Fatal(err)
	}
	if turns = listSessionCheckpointTurns(dir, "sess1"); len(turns) != 0 {
		t.Fatalf("会话清理后不该有残留 ref: %v", turns)
	}
	// 其它会话不受影响
	if _, err := Checkpoint(dir, "sess2", 1); err != nil {
		t.Fatal(err)
	}
	if err := DropSessionCheckpoints(dir, "sess1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ResolveCheckpoint(dir, "sess2", 1); !ok {
		t.Fatal("清理一个会话不该影响另一个")
	}
}

// 快照内容的查询：存在性、blob 哈希、与工作区哈希的对应关系
func TestBlobHashAndPathInCheckpoint(t *testing.T) {
	dir := newTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "content-a\n")
	cp, err := Checkpoint(dir, "sess1", 1)
	if err != nil {
		t.Fatal(err)
	}

	if !pathInCheckpoint(dir, cp, "a.txt") {
		t.Fatal("a.txt 应在快照里")
	}
	fromSnap := blobHashAt(dir, cp, "a.txt")
	if fromSnap == "" {
		t.Fatal("应能取到快照里的 blob 哈希")
	}
	// 工作区内容与快照一致时，两处哈希必须相等（回退的冲突检查依赖这个）
	if live := hashWorktreeFile(dir, "a.txt"); live != fromSnap {
		t.Fatalf("内容一致时哈希应相同: 快照 %q 工作区 %q", fromSnap, live)
	}

	// 快照之后新建的文件：不在快照里
	writeTestFile(t, filepath.Join(dir, "later.txt"), "later\n")
	if pathInCheckpoint(dir, cp, "later.txt") {
		t.Fatal("快照之后新建的文件不该出现在快照里")
	}
	if blobHashAt(dir, cp, "later.txt") != "" {
		t.Fatal("不存在的路径应返回空哈希")
	}
	// 空哈希可靠地表示"不存在"——空文件的哈希不是空串
	writeTestFile(t, filepath.Join(dir, "empty.txt"), "")
	if h := hashWorktreeFile(dir, "empty.txt"); h == "" {
		t.Fatal("空文件的哈希不是空串，否则'不存在'的判定会失效")
	}
	// 不存在的文件
	if hashWorktreeFile(dir, "no-such-file.txt") != "" {
		t.Fatal("不存在的文件应返回空哈希")
	}
}

// 回退原语：恢复到**快照**的状态，而不是 HEAD
func TestRestoreFromCheckpoint(t *testing.T) {
	dir := newTestRepo(t)
	writeTestFile(t, filepath.Join(dir, "a.txt"), "snapshot state\n")

	cp, err := Checkpoint(dir, "sess1", 1)
	if err != nil {
		t.Fatal(err)
	}
	// 快照之后再改，模拟 agent 在同一轮里继续写
	writeTestFile(t, filepath.Join(dir, "a.txt"), "changed after snapshot\n")

	if err := restoreFromCheckpoint(dir, cp, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if got := cpReadFile(t, dir, "a.txt"); got != "snapshot state\n" {
		t.Fatalf("应恢复到快照内容（不是 HEAD 的 line1/line2），实际 %q", got)
	}

	// 被删除的文件也能恢复
	writeTestFile(t, filepath.Join(dir, "gone.txt"), "to be deleted\n")
	cp2, err := Checkpoint(dir, "sess1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := restoreFromCheckpoint(dir, cp2, "gone.txt"); err != nil {
		t.Fatal(err)
	}
	if got := cpReadFile(t, dir, "gone.txt"); got != "to be deleted\n" {
		t.Fatalf("删除的文件应能恢复，实际 %q", got)
	}
}

// 删除文件时清理空目录；但**不能**删掉还有内容的目录
func TestRemoveWorktreeFileCleansEmptyDirs(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, filepath.Join(dir, "sub", "deep", "only.txt"), "x\n")
	if err := removeWorktreeFile(dir, "sub/deep/only.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Fatal("因此变空的目录应被清理")
	}

	// 目录里还有别的文件时，目录必须保留
	writeTestFile(t, filepath.Join(dir, "keep", "keep.txt"), "k\n")
	writeTestFile(t, filepath.Join(dir, "keep", "remove.txt"), "r\n")
	if err := removeWorktreeFile(dir, "keep/remove.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep")); err != nil {
		t.Fatalf("非空目录不该被删掉: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep", "keep.txt")); err != nil {
		t.Fatalf("无关文件不该被动: %v", err)
	}

	// 已经不存在时视为成功（幂等）
	if err := removeWorktreeFile(dir, "keep/remove.txt"); err != nil {
		t.Fatalf("重复删除应视为成功: %v", err)
	}
	// 参数不完整要报错
	if err := removeWorktreeFile("", "a.txt"); err == nil {
		t.Fatal("目录为空应报错")
	}
}

// endStateOf：记录每轮结束时的内容状态，供回退前的冲突检查
func TestEndStateOf(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "exists.txt"), "here\n")

	got := endStateOf(dir, []DiffFile{
		{Path: "exists.txt"},
		{Path: "absent.txt"}, // 该轮把它删了，或从未存在
		{Path: ""},           // 脏数据不该 panic
	})
	if got["exists.txt"] == "" {
		t.Fatal("存在的文件应有哈希")
	}
	if v, ok := got["absent.txt"]; !ok || v != "" {
		t.Fatalf("不存在的文件应记为空串，实际 %q（存在=%v）", v, ok)
	}
	if _, ok := got[""]; ok {
		t.Fatal("空路径不该进结果")
	}
	if endStateOf(dir, nil) != nil {
		t.Fatal("空输入应返回 nil")
	}
	if endStateOf("", []DiffFile{{Path: "a.txt"}}) != nil {
		t.Fatal("目录为空应返回 nil")
	}
}
