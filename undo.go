package main

import (
	"fmt"
	"strings"
)

// ===== 撤销：按归因把某一轮改过的文件回退到该轮开始时的状态 =====
//
// 三条边界，都是刻意的：
//
//   - **只回退这一轮实际碰过的路径**（按 diff 归因，见 diff.go 的会话级隔离），
//     不做整树 restore。用户在这一轮之外的手工改动因此不受影响。
//   - **回退前做冲突检查**：某个文件在本轮之后又被改过（很可能是用户手工改的），
//     默认停下来让用户确认，而不是默默覆盖。
//   - **逐文件汇报结果**，部分失败不当成整体失败——文件被占用、权限不足都是常见的，
//     不能因为一个失败就丢掉其余已成功的回退。
//
// 不推 diff:update 事件：前端的 applyUpdate 会把**空 diff** 直接丢掉
// （frontend/src/stores/diff.js 的 `if (!diff || !diff.length) return`），
// 而回退后恰好经常变成空——靠事件刷新会显示过期数据。改为由前端在回退后重新拉取。

// CheckpointInfo 一轮的回退可用性（供前端决定按钮是否可点）
type CheckpointInfo struct {
	Turn      int      `json:"turn"`
	Label     string   `json:"label"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"` // 不可回退的原因
	Files     []string `json:"files"`            // 将被回退的路径
	Conflicts []string `json:"conflicts"`        // 本轮之后又被改过的路径
	Undone    bool     `json:"undone,omitempty"` // 是否已回退过
}

// UndoFileResult 逐文件结果
type UndoFileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`        // restore | delete | skip
	Err    string `json:"err,omitempty"`
}

// UndoResult 一次回退的结果
type UndoResult struct {
	Turn    int              `json:"turn"`
	Files   []UndoFileResult `json:"files"`
	Failed  int              `json:"failed"`
	Skipped int              `json:"skipped"`
}

// diffFilePaths 取出 diff 里的路径（跳过空路径）
func diffFilePaths(files []DiffFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f.Path != "" {
			out = append(out, f.Path)
		}
	}
	return out
}

// conflictsOfTurn 找出"本轮之后又被改过"的文件。
//
// 判据是记录在本轮结束时的内容哈希（DiffTurn.EndState）与当前工作区是否一致。
// 老数据没有 EndState 时不做判断（那种数据 Base 也是空的，本来就不可回退）。
func conflictsOfTurn(dir string, t *DiffTurn) []string {
	if dir == "" || t == nil || t.EndState == nil {
		return nil
	}
	var out []string
	for _, f := range t.Files {
		if f.Path == "" {
			continue
		}
		want, recorded := t.EndState[f.Path]
		if !recorded {
			continue
		}
		if hashWorktreeFile(dir, f.Path) != want {
			out = append(out, f.Path)
		}
	}
	return out
}

// ListCheckpoints 列出会话各轮的回退可用性与冲突情况
func (a *App) ListCheckpoints(sessionID string) ([]CheckpointInfo, error) {
	s, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	dir, _ := a.resolveProjectDir(sessionID)

	out := make([]CheckpointInfo, 0, len(s.Diffs))
	for i := range s.Diffs {
		t := &s.Diffs[i]
		info := CheckpointInfo{
			Turn:      t.Turn,
			Label:     t.Label,
			Files:     diffFilePaths(t.Files),
			Conflicts: conflictsOfTurn(dir, t),
			Undone:    t.Undone,
		}
		switch {
		case t.Base == "":
			info.Reason = "本轮未取到快照（非 git 仓库，或取快照时失败）"
		case dir == "":
			info.Reason = "无法确定工作区目录"
		default:
			if _, ok := ResolveCheckpoint(dir, sessionID, t.Turn); !ok {
				info.Reason = "快照已过期或被清理"
			} else {
				info.Available = true
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// UndoDiffTurn 把某一轮改过的文件回退到该轮开始时的状态。
// force=false 时若检测到冲突则不执行任何改动，把冲突清单通过错误返回给调用方。
func (a *App) UndoDiffTurn(sessionID string, turn int, force bool) (*UndoResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("会话 ID 不能为空")
	}
	s, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	var target *DiffTurn
	for i := range s.Diffs {
		if s.Diffs[i].Turn == turn {
			target = &s.Diffs[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("轮次不存在: %d", turn)
	}
	if target.Base == "" {
		return nil, fmt.Errorf("第 %d 轮没有快照，无法回退", turn)
	}

	dir, err := a.resolveProjectDir(sessionID)
	if err != nil || dir == "" {
		return nil, fmt.Errorf("无法确定工作区目录")
	}
	cp, ok := ResolveCheckpoint(dir, sessionID, turn)
	if !ok {
		return nil, fmt.Errorf("第 %d 轮的快照已过期或被清理，无法回退", turn)
	}

	// 冲突检查：先拦下来，不执行任何写操作
	if conflicts := conflictsOfTurn(dir, target); len(conflicts) > 0 && !force {
		return nil, fmt.Errorf(
			"以下文件在第 %d 轮之后又被改动过，回退会覆盖那些改动：%s。确认后可带 force 重试",
			turn, strings.Join(conflicts, "、"))
	}

	res := &UndoResult{Turn: turn, Files: make([]UndoFileResult, 0, len(target.Files))}
	for i := range target.Files {
		rec := undoOneFile(dir, cp, &target.Files[i])
		if rec.Err != "" {
			if rec.Action == "skip" {
				res.Skipped++
			} else {
				res.Failed++
			}
		}
		res.Files = append(res.Files, rec)
	}

	// 回退后这些路径不再算作本会话的改动，从归因集合里移除
	a.diffService.DropTouched(sessionID, diffFilePaths(target.Files))
	if err := a.sessionStore.MarkDiffUndone(sessionID, turn, true); err != nil {
		// 标记失败不影响回退本身，只记一笔日志
		fmt.Printf("[undo] 标记轮次已回退失败: %v\n", err)
	}
	return res, nil
}

// undoOneFile 回退单个文件。
//
// 这里最关键的一处分叉：`added` 不能一律删。
// 一个"轮次之前就存在、但一直未跟踪、本轮被改"的文件，DiffScoped 也会把它报成 added
// （因为它的基线快照不含未跟踪文件）。所以必须用**含未跟踪文件**的 checkpoint 复核：
// 快照里存在 → 恢复内容；不存在 → 才是本轮真正新建的，可以删。
func undoOneFile(dir, cp string, f *DiffFile) UndoFileResult {
	rec := UndoFileResult{}
	if f == nil || f.Path == "" {
		rec.Action = "skip"
		rec.Err = "路径为空"
		return rec
	}
	rec.Path = f.Path

	switch f.Status {
	case "added":
		if pathInCheckpoint(dir, cp, f.Path) {
			rec.Action = "restore"
			if err := restoreFromCheckpoint(dir, cp, f.Path); err != nil {
				rec.Err = err.Error()
			}
			return rec
		}
		rec.Action = "delete"
		if err := removeWorktreeFile(dir, f.Path); err != nil {
			rec.Err = err.Error()
		}
		return rec

	case "modified", "deleted":
		rec.Action = "restore"
		if err := restoreFromCheckpoint(dir, cp, f.Path); err != nil {
			rec.Err = err.Error()
		}
		return rec

	case "renamed":
		rec.Action = "restore"
		// 先把旧路径恢复回来
		if f.OldPath != "" {
			if err := restoreFromCheckpoint(dir, cp, f.OldPath); err != nil {
				rec.Err = err.Error()
				return rec
			}
		}
		// 新路径若不在快照里，说明是本轮重命名造出来的，删掉；
		// 在快照里则说明它本来就存在，恢复内容
		if !pathInCheckpoint(dir, cp, f.Path) {
			if err := removeWorktreeFile(dir, f.Path); err != nil {
				rec.Err = err.Error()
			}
			return rec
		}
		if err := restoreFromCheckpoint(dir, cp, f.Path); err != nil {
			rec.Err = err.Error()
		}
		return rec

	default:
		rec.Action = "skip"
		rec.Err = fmt.Sprintf("未知的变更类型: %q", f.Status)
		return rec
	}
}
