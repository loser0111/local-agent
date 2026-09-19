package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== P2 第 3 步：回退算法 =====
//
// 这些测试全部在真实 git 仓库上跑。重点守两件事：
//   1. "轮次之前就存在但未跟踪的文件"必须被**恢复**而不是删除（数据丢失场景）；
//   2. 冲突检查真的能拦住对用户手工改动的覆盖。

// newUndoTestApp 最小 App：UndoDiffTurn 只需要 sessionStore + diffService
func newUndoTestApp(t *testing.T, projectDir string) (*App, *Session) {
	t.Helper()
	app := &App{
		sessionStore: NewSessionStore(filepath.Join(t.TempDir(), "sessions")),
		diffService:  NewDiffService(),
	}
	sess, err := app.sessionStore.CreateSession(SessionConfig{
		Title:   "t",
		Model:   "m",
		Project: projectDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, sess
}

// beginTurn 取一轮的快照 —— **必须在改文件之前调用**，返回轮次号与快照 sha
func beginTurn(t *testing.T, app *App, sess *Session, repo string) (int, string) {
	t.Helper()
	session, err := app.sessionStore.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	turnNo := nextDiffTurn(session)
	cp, err := Checkpoint(repo, sess.ID, turnNo)
	if err != nil {
		t.Fatalf("取快照失败: %v", err)
	}
	return turnNo, cp
}

// endTurn 记录这一轮 —— **在改完文件之后调用**
func endTurn(t *testing.T, app *App, sess *Session, repo string, wantTurn int, cp string, files []DiffFile) {
	t.Helper()
	got, err := app.sessionStore.AppendDiff(sess.ID, DiffTurn{
		Files:    files,
		Base:     cp,
		EndState: endStateOf(repo, files),
	}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 快照 ref 的编号与 DiffTurn.Turn 必须一致，否则回退会找不到快照
	if got != wantTurn {
		t.Fatalf("轮次编号不一致：快照用 %d，记录用 %d（两处必须走同一个 nextDiffTurn）", wantTurn, got)
	}
}

// ★ 本文件最重要的一条：轮次之前就存在、但一直未跟踪的文件，回退必须**恢复**而不是删除。
//
// 它同时验证了"Status 说的是 added"这个前提——若哪天真去掉了 checkpoint、
// 改成只信 Status，这条测试会立刻红。
func TestUndoRestoresPreExistingUntrackedFile(t *testing.T) {
	repo := newTestRepo(t)
	app, sess := newUndoTestApp(t, repo)

	// 用户手写的文件，从未被 git 跟踪
	writeTestFile(t, filepath.Join(repo, "user-note.txt"), "用户手写的内容\n")

	turnNo, cp := beginTurn(t, app, sess, repo)
	// 本轮 agent 改了这个未跟踪文件
	writeTestFile(t, filepath.Join(repo, "user-note.txt"), "被 agent 改过\n")
	// DiffScoped 会把它报成 added —— 这里手工构造同样的形状
	files := []DiffFile{{Path: "user-note.txt", Status: "added"}}
	endTurn(t, app, sess, repo, turnNo, cp, files)

	res, err := app.UndoDiffTurn(sess.ID, turnNo, false)
	if err != nil {
		t.Fatalf("回退应成功: %v", err)
	}
	if res.Failed != 0 || len(res.Files) != 1 {
		t.Fatalf("不该有失败: %+v", res)
	}
	if res.Files[0].Action != "restore" {
		t.Fatalf("动作应为 restore（不能删！），实际 %q", res.Files[0].Action)
	}
	if got := cpReadFile(t, repo, "user-note.txt"); got != "用户手写的内容\n" {
		t.Fatalf("用户手写的未跟踪文件应被恢复，实际 %q", got)
	}
}

// 本轮真正新建的文件，回退时删除
func TestUndoDeletesTrulyNewFile(t *testing.T) {
	repo := newTestRepo(t)
	app, sess := newUndoTestApp(t, repo)

	turnNo, cp := beginTurn(t, app, sess, repo)
	writeTestFile(t, filepath.Join(repo, "fresh.txt"), "agent 新建\n")
	files := []DiffFile{{Path: "fresh.txt", Status: "added"}}
	endTurn(t, app, sess, repo, turnNo, cp, files)

	res, err := app.UndoDiffTurn(sess.ID, turnNo, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files[0].Action != "delete" {
		t.Fatalf("快照里没有的文件应被删除，实际动作 %q", res.Files[0].Action)
	}
	if _, err := os.Stat(filepath.Join(repo, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatal("本轮新建的文件应已被删除")
	}
}

// 被修改的已跟踪文件 → 恢复到快照内容（不是 HEAD）
func TestUndoRestoresModifiedFile(t *testing.T) {
	repo := newTestRepo(t)
	app, sess := newUndoTestApp(t, repo)
	before := cpReadFile(t, repo, "a.txt") // newTestRepo 提交的内容

	turnNo, cp := beginTurn(t, app, sess, repo)
	writeTestFile(t, filepath.Join(repo, "a.txt"), "agent 改过\n")
	files := []DiffFile{{Path: "a.txt", Status: "modified"}}
	endTurn(t, app, sess, repo, turnNo, cp, files)

	if _, err := app.UndoDiffTurn(sess.ID, turnNo, false); err != nil {
		t.Fatal(err)
	}
	if got := cpReadFile(t, repo, "a.txt"); got != before {
		t.Fatalf("应恢复到快照内容 %q，实际 %q", before, got)
	}
}

// 被删除的文件 → 恢复回来
func TestUndoRestoresDeletedFile(t *testing.T) {
	repo := newTestRepo(t)
	app, sess := newUndoTestApp(t, repo)
	before := cpReadFile(t, repo, "a.txt")

	turnNo, cp := beginTurn(t, app, sess, repo)
	if err := os.Remove(filepath.Join(repo, "a.txt")); err != nil {
		t.Fatal(err)
	}
	files := []DiffFile{{Path: "a.txt", Status: "deleted"}}
	endTurn(t, app, sess, repo, turnNo, cp, files)

	if _, err := app.UndoDiffTurn(sess.ID, turnNo, false); err != nil {
		t.Fatal(err)
	}
	if got := cpReadFile(t, repo, "a.txt"); got != before {
		t.Fatalf("被删除的文件应恢复，实际 %q", got)
	}
}

// 冲突检查：本轮之后又被改过的文件，默认拦下来且一个字节都不动
func TestUndoDetectsConflict(t *testing.T) {
	repo := newTestRepo(t)
	app, sess := newUndoTestApp(t, repo)
	snapshot := cpReadFile(t, repo, "a.txt")

	turnNo, cp := beginTurn(t, app, sess, repo)
	writeTestFile(t, filepath.Join(repo, "a.txt"), "agent 在本轮改的\n")
	files := []DiffFile{{Path: "a.txt", Status: "modified"}}
	endTurn(t, app, sess, repo, turnNo, cp, files)

	// 用户在本轮之后又手工改了一次
	writeTestFile(t, filepath.Join(repo, "a.txt"), "用户后来手改的\n")

	// ListCheckpoints 应能报出冲突
	list, err := app.ListCheckpoints(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Available {
		t.Fatalf("该轮应可回退: %+v", list)
	}
	if len(list[0].Conflicts) != 1 || list[0].Conflicts[0] != "a.txt" {
		t.Fatalf("应报出 a.txt 的冲突，实际 %v", list[0].Conflicts)
	}

	// force=false：拒绝，且不改动文件
	if _, err := app.UndoDiffTurn(sess.ID, turnNo, false); err == nil {
		t.Fatal("有冲突时应拒绝回退")
	}
	if got := cpReadFile(t, repo, "a.txt"); got != "用户后来手改的\n" {
		t.Fatalf("拒绝时不该改动文件，实际 %q", got)
	}

	// force=true：覆盖，回到快照内容
	if _, err := app.UndoDiffTurn(sess.ID, turnNo, true); err != nil {
		t.Fatalf("force 应能强制回退: %v", err)
	}
	if got := cpReadFile(t, repo, "a.txt"); got != snapshot {
		t.Fatalf("强制回退应回到快照内容 %q，实际 %q", snapshot, got)
	}
}

// 幂等：连续回退同一轮不报错、结果一致
func TestUndoIsIdempotent(t *testing.T) {
	repo := newTestRepo(t)
	app, sess := newUndoTestApp(t, repo)
	before := cpReadFile(t, repo, "a.txt")

	turnNo, cp := beginTurn(t, app, sess, repo)
	writeTestFile(t, filepath.Join(repo, "a.txt"), "agent 改过\n")
	writeTestFile(t, filepath.Join(repo, "new.txt"), "agent 新建\n")
	files := []DiffFile{
		{Path: "a.txt", Status: "modified"},
		{Path: "new.txt", Status: "added"},
	}
	endTurn(t, app, sess, repo, turnNo, cp, files)

	first, err := app.UndoDiffTurn(sess.ID, turnNo, false)
	if err != nil {
		t.Fatal(err)
	}
	// 第二次：文件已回到快照状态，冲突检查会认为内容变了？不会——
	// EndState 记的是"本轮结束时的内容"，恢复后内容与它不同，因此会被判为冲突。
	// 这正是我们想要的保护，所以第二次带 force 走。
	second, err := app.UndoDiffTurn(sess.ID, turnNo, true)
	if err != nil {
		t.Fatalf("再次回退不该报错: %v", err)
	}
	if first.Failed != 0 || second.Failed != 0 {
		t.Fatalf("两次都不该有失败: %+v / %+v", first, second)
	}
	if got := cpReadFile(t, repo, "a.txt"); got != before {
		t.Fatalf("内容应稳定在快照状态，实际 %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("新建文件应保持已删除")
	}
}

// 不可回退的几种情形都要明确报错，而不是静默什么都不做
func TestUndoRejectsMissingSnapshot(t *testing.T) {
	repo := newTestRepo(t)
	app, sess := newUndoTestApp(t, repo)

	// Base 为空（老数据、或快照失败的轮次）
	if _, err := app.sessionStore.AppendDiff(sess.ID, DiffTurn{
		Files: []DiffFile{{Path: "a.txt", Status: "modified"}},
	}, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UndoDiffTurn(sess.ID, 1, false); err == nil {
		t.Fatal("没有快照的轮次应拒绝回退")
	}
	list, _ := app.ListCheckpoints(sess.ID)
	if len(list) != 1 || list[0].Available || list[0].Reason == "" {
		t.Fatalf("应标记为不可回退并给出原因: %+v", list)
	}

	// 不存在的轮次
	if _, err := app.UndoDiffTurn(sess.ID, 99, false); err == nil {
		t.Fatal("不存在的轮次应报错")
	}
	// 空会话 ID
	if _, err := app.UndoDiffTurn("", 1, false); err == nil {
		t.Fatal("空会话 ID 应报错")
	}
	// 快照 ref 被清理后
	turnNo, cp := beginTurn(t, app, sess, repo)
	writeTestFile(t, filepath.Join(repo, "a.txt"), "agent 改过\n")
	endTurn(t, app, sess, repo, turnNo, cp, []DiffFile{{Path: "a.txt", Status: "modified"}})
	if err := DropCheckpoint(repo, sess.ID, turnNo); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UndoDiffTurn(sess.ID, turnNo, false); err == nil {
		t.Fatal("快照被清理后应拒绝回退")
	}
}

// 端到端前提验证：走**真实流程**（DiffScoped + turnBase + checkpoint）跑一轮，
// 断言"轮次之前就存在的未跟踪文件被改"确实被报成 added，且回退能恢复它。
//
// 这条把整个设计的前提钉死了：Status 不可信，必须用含未跟踪文件的快照复核。
func TestRecordedStatusForUntrackedFileIsAddedAndUndoRestores(t *testing.T) {
	repo := newTestRepo(t)
	app, sess := newUndoTestApp(t, repo)
	svc := app.diffService

	// 用户在轮次之前就写好的未跟踪文件
	writeTestFile(t, filepath.Join(repo, "user-note.txt"), "用户手写的内容\n")
	svc.EnsureBaseline(sess.ID, repo)
	svc.NoteActivity(sess.ID, repo, false) // 起始扫描：只建基线

	// 轮次开始：两份快照各取一次（与 chat.go 的真实流程一致）
	turnBase := svc.TurnSnapshot(repo)
	turnNo, cp := beginTurn(t, app, sess, repo)
	svc.BeginTurn(sess.ID)

	// 本轮的"工具调用"：改那个未跟踪文件 + 新建一个
	svc.NoteActivity(sess.ID, repo, false)
	writeTestFile(t, filepath.Join(repo, "user-note.txt"), "被 agent 改过\n")
	writeTestFile(t, filepath.Join(repo, "fresh.txt"), "agent 新建\n")
	svc.NoteActivity(sess.ID, repo, true)

	// 真实流程里就是这样算本轮 diff 的：基线用 turnBase（不含未跟踪文件）
	paths := svc.TurnTouched(sess.ID)
	files, err := svc.DiffScoped(repo, turnBase, paths)
	if err != nil {
		t.Fatal(err)
	}

	// 前提验证：那个"本来就存在、只是未跟踪"的文件被报成 added
	statusOf := map[string]string{}
	for _, f := range files {
		statusOf[f.Path] = f.Status
	}
	if statusOf["user-note.txt"] != "added" {
		t.Fatalf("前提不成立：预存在的未跟踪文件应被报成 added，实际 %q（files=%+v）",
			statusOf["user-note.txt"], files)
	}
	if statusOf["fresh.txt"] != "added" {
		t.Fatalf("新文件应报成 added，实际 %q", statusOf["fresh.txt"])
	}

	endTurn(t, app, sess, repo, turnNo, cp, files)

	// 回退：预存在的未跟踪文件必须恢复、真正新建的必须删除
	res, err := app.UndoDiffTurn(sess.ID, turnNo, false)
	if err != nil {
		t.Fatalf("回退应成功: %v", err)
	}
	if res.Failed != 0 {
		t.Fatalf("不该有失败: %+v", res)
	}
	if got := cpReadFile(t, repo, "user-note.txt"); got != "用户手写的内容\n" {
		t.Fatalf("用户手写的文件应被恢复，实际 %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "fresh.txt")); !os.IsNotExist(err) {
		t.Fatal("agent 新建的文件应被删除")
	}
	// 两个文件都报 added，但动作必须不同 —— 这正是复核快照带来的差别
	actions := map[string]string{}
	for _, f := range res.Files {
		actions[f.Path] = f.Action
	}
	if actions["user-note.txt"] != "restore" || actions["fresh.txt"] != "delete" {
		t.Fatalf("同样的 added 应给出不同动作: %+v", actions)
	}

	// 回退后归因也应清掉：累计 diff 里不该再有这两个文件
	remaining, _ := app.GetDiff(sess.ID)
	for _, f := range remaining {
		if strings.Contains(f.Path, "user-note") || strings.Contains(f.Path, "fresh") {
			t.Fatalf("回退后不该再归因这些文件: %+v", remaining)
		}
	}
}
