// 本文件是子代理的**运行概况注册表**（内存态）与概况深拷贝。
//
// 为什么它属于引擎而不属于界面：它是一份"边跑边变"的运行状态，只依赖
// store.SubagentInfo 这一个数据模型，与 App、Wails 事件、UI 通道都无关——
// 根包那边剩下的只是"把它推给前端"和"把它与磁盘上的历史合起来查询"。
//
// 数据分两处，各有分工（这条分工是这块设计的骨架）：
//   - **运行中的**：这里的内存注册表。面板要显示"跑到第几步、当前在跑什么工具"，
//     这些每执行一次工具就变一次，没必要落盘。
//   - **跑完的**：写回子代理自己的会话文件（store.Session.Subagent）。重启后仍能查看，
//     而"回退它改过的文件"本来就要靠那个会话（checkpoint 与 diff 都按会话 ID 键控）。
package agent

import (
	"sync"

	"wails-tmp/internal/store"
)

// CloneSubagentInfo 复制一份概况（含切片的深拷贝）。
//
// 必须复制，不能把注册表里的指针交出去：这份数据是**边跑边变**的（每执行一次工具
// 就改一次），直接交指针会让"当时推给前端的那一份"事后跟着变，排查时看到的就不是
// 现场。这是"记录现场"类数据的通用坑。
func CloneSubagentInfo(info *store.SubagentInfo) *store.SubagentInfo {
	if info == nil {
		return nil
	}
	out := *info
	out.Files = append([]string(nil), info.Files...)
	out.Declined = append([]string(nil), info.Declined...)
	return &out
}

// SubagentTracker 运行中的子代理（只放还没跑完的）。所有方法对 nil 接收者安全——
// 零值 App（测试里常见）没有这个注册表，那时功能退化成"看不到运行中的"，不该 panic。
type SubagentTracker struct {
	mu   sync.Mutex
	live map[string]*store.SubagentInfo
}

// NewSubagentTracker 建一个空的运行注册表
func NewSubagentTracker() *SubagentTracker {
	return &SubagentTracker{live: map[string]*store.SubagentInfo{}}
}

// Begin 登记一个开始运行的子代理
func (t *SubagentTracker) Begin(info *store.SubagentInfo) {
	if t == nil || info == nil || info.RunID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.live == nil {
		t.live = map[string]*store.SubagentInfo{}
	}
	t.live[info.RunID] = info
}

// ToolStart 记下一次工具调用开始：步数 +1，当前工具更新
func (t *SubagentTracker) ToolStart(runID, tool string) *store.SubagentInfo {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	info := t.live[runID]
	if info == nil {
		return nil
	}
	info.Step++
	info.CurrentTool = tool
	return CloneSubagentInfo(info)
}

// ToolEnd 记下一次工具调用结束（当前工具清空，避免界面显示一个已经跑完的工具）
func (t *SubagentTracker) ToolEnd(runID string) *store.SubagentInfo {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	info := t.live[runID]
	if info == nil {
		return nil
	}
	info.CurrentTool = ""
	return CloneSubagentInfo(info)
}

// Finish 摘掉运行中的记录（跑完的改由会话文件承载）
func (t *SubagentTracker) Finish(runID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, runID)
}

// Get 取某个运行中的子代理概况（副本）
func (t *SubagentTracker) Get(runID string) *store.SubagentInfo {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return CloneSubagentInfo(t.live[runID])
}

// List 列出某主会话下正在跑的（副本，避免调用方遍历时被并发修改）
func (t *SubagentTracker) List(parentID string) []store.SubagentInfo {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]store.SubagentInfo, 0, len(t.live))
	for _, info := range t.live {
		if parentID != "" && info.ParentID != parentID {
			continue
		}
		out = append(out, *CloneSubagentInfo(info))
	}
	return out
}
