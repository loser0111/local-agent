package main

import (
	"fmt"

	"wails-tmp/internal/snapshot"
)

// ===== 撤销（Wails 绑定入口）=====
//
// 回退算法与三个 JSON 结果类型（CheckpointInfo / UndoFileResult / UndoResult）已下沉到
// internal/snapshot/undo.go——包括"只回退本轮碰过的路径""回退前做冲突检查""逐文件汇报、
// 部分失败不当整体失败"这三条边界的理由，以及"为什么不推 diff:update 事件"。
//
// 这里只剩 Wails 绑定所需的 App 方法：取会话、解析工作区目录，
// 回退后要落的两件事（标记轮次已回退、清掉本轮归因）由 *store.SessionStore 与
// *diff.DiffService 通过 snapshot.UndoRecorder / snapshot.TouchedDropper 交回去做。

// ListCheckpoints 列出会话各轮的回退可用性与冲突情况
func (a *App) ListCheckpoints(sessionID string) ([]snapshot.CheckpointInfo, error) {
	s, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	// 目录解析失败不在这里报错：ListCheckpoints 是只读的列表接口，
	// 解析不到时各轮会带上"无法确定工作区目录"这个 reason 返回（见 engine）。
	dir, _ := a.resolveProjectDir(sessionID)
	return snapshot.ListCheckpoints(dir, s), nil
}

// UndoDiffTurn 把某一轮改过的文件回退到该轮开始时的状态。
// force=false 时若检测到冲突则不执行任何改动，把冲突清单通过错误返回给调用方。
func (a *App) UndoDiffTurn(sessionID string, turn int, force bool) (*snapshot.UndoResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("会话 ID 不能为空")
	}
	s, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	// 解析失败一律以空目录进引擎，由引擎给出统一的"无法确定工作区目录"
	dir, _ := a.resolveProjectDir(sessionID)
	return snapshot.UndoDiffTurn(dir, s, turn, force, a.sessionStore, a.diffService)
}
