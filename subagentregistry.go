package main

import (
	"fmt"
	"sort"
	"sync"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ===== 子代理的运行概况与进度（P3 第 5 步）=====
//
// 这一层解决的是"用户看不见子代理"。子代理跑在自己独立的会话里，事件也刻意**不走**
// chat:event（否则会混进主会话的聊天流，看起来像主会话自己在跑那些工具），
// 于是它既不在聊天里露面、也不在任何面板里露面。这里给它一条独立通道和一份可查询的概况。
//
// 数据分两处，各有分工：
//   - **运行中的**：内存里的 tracker。面板要显示"跑到第几步、当前在跑什么工具"，
//     这些每执行一次工具就变一次，没必要落盘。
//   - **跑完的**：写回子代理自己的会话文件（Session.Subagent）。重启后仍能查看，
//     而"回退它改过的文件"本来就要靠那个会话（checkpoint 与 diff 都按会话 ID 键控）。
//
// ListSubagents 把两者合起来，调用方不必关心哪条记录存在哪里。
type SubagentInfo struct {
	RunID       string   `json:"runId"`    // = 子代理会话 ID（消息、diff、checkpoint 都用它）
	ParentID    string   `json:"parentId"` // 父会话 ID
	Title       string   `json:"title"`
	Task        string   `json:"task"`   // 派给它做的事（title 是它的截断版）
	Status      string   `json:"status"` // running | completed | failed | cancelled | interrupted
	Model       string   `json:"model,omitempty"`
	Step        int      `json:"step"`        // 已执行的工具调用数
	CurrentTool string   `json:"currentTool,omitempty"`
	StartedAt   int64    `json:"startedAt"`
	EndedAt     int64    `json:"endedAt,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Files       []string `json:"files,omitempty"` // 它改过的文件
	Declined    []string `json:"declined,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// cloneSubagentInfo 复制一份概况（含切片的深拷贝）。
//
// 必须复制，不能把注册表里的指针交出去：这份数据是**边跑边变**的（每执行一次工具
// 就改一次），直接交指针会让"当时推给前端的那一份"事后跟着变，排查时看到的就不是
// 现场。这是"记录现场"类数据的通用坑（同 requestlog.go 的快照要拷贝）。
func cloneSubagentInfo(info *SubagentInfo) *SubagentInfo {
	if info == nil {
		return nil
	}
	out := *info
	out.Files = append([]string(nil), info.Files...)
	out.Declined = append([]string(nil), info.Declined...)
	return &out
}

// subagentTracker 运行中的子代理（只放还没跑完的）。所有方法对 nil 接收者安全——
// 零值 App（测试里常见）没有这个注册表，那时功能退化成"看不到运行中的"，不该 panic。
type subagentTracker struct {
	mu   sync.Mutex
	live map[string]*SubagentInfo
}

func newSubagentTracker() *subagentTracker {
	return &subagentTracker{live: map[string]*SubagentInfo{}}
}

// begin 登记一个开始运行的子代理
func (t *subagentTracker) begin(info *SubagentInfo) {
	if t == nil || info == nil || info.RunID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.live == nil {
		t.live = map[string]*SubagentInfo{}
	}
	t.live[info.RunID] = info
}

// toolStart 记下一次工具调用开始：步数 +1，当前工具更新
func (t *subagentTracker) toolStart(runID, tool string) *SubagentInfo {
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
	return cloneSubagentInfo(info)
}

// toolEnd 记下一次工具调用结束（当前工具清空，避免界面显示一个已经跑完的工具）
func (t *subagentTracker) toolEnd(runID string) *SubagentInfo {
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
	return cloneSubagentInfo(info)
}

// finish 摘掉运行中的记录（跑完的改由会话文件承载）
func (t *subagentTracker) finish(runID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, runID)
}

// get 取某个运行中的子代理概况（副本）
func (t *subagentTracker) get(runID string) *SubagentInfo {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneSubagentInfo(t.live[runID])
}

// list 列出某主会话下正在跑的（副本，避免调用方遍历时被并发修改）
func (t *subagentTracker) list(parentID string) []SubagentInfo {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]SubagentInfo, 0, len(t.live))
	for _, info := range t.live {
		if parentID != "" && info.ParentID != parentID {
			continue
		}
		out = append(out, *cloneSubagentInfo(info))
	}
	return out
}

// ===== 事件通道 =====

// subagentRecorder 子代理的事件出口：翻译进度，**不**混进主会话的聊天流。
//
// 子代理的工具调用若走 chat:event 推给前端，会让人以为主会话自己在跑那些工具；
// 而完全静默（早期实现就是这样）又会让面板里的"跑到哪一步"永远是空白。
// 所以这里只把工具调用的**开始/结束**翻译成 subagent:event（一条概况，不带工具输出），
// 其余事件一概不转发。
type subagentRecorder struct {
	app   *App
	runID string
}

// Emit 只处理工具调用的开始/结束，其余事件不转发
func (r *subagentRecorder) Emit(ev ChatEvent) {
	if r == nil || r.app == nil || ev.ToolCall == nil {
		return
	}
	switch ev.Type {
	case ChatEventToolCallStart:
		r.app.subagents.toolStart(r.runID, ev.ToolCall.Name)
	case ChatEventToolCallEnd:
		r.app.subagents.toolEnd(r.runID)
	default:
		return
	}
	r.app.emitSubagentProgress(r.runID)
}

// EmitDiff 不推：子代理的改动属于它自己的会话，面板展开时按需加载，
// 推给主会话的 diff 面板等于把它算成了主会话的改动。
func (r *subagentRecorder) EmitDiff([]DiffFile, int) {}

// FlushStream 子代理不流式，返回空实现即可
func (r *subagentRecorder) FlushStream() (func(string), func()) {
	return func(string) {}, func() {}
}

// emitSubagentProgress 把某子代理的最新进度推给前端（SubagentPane 订阅 subagent:event）
func (a *App) emitSubagentProgress(runID string) {
	if a == nil || a.ctx == nil {
		return
	}
	info := a.subagents.get(runID)
	if info == nil {
		return
	}
	a.emitSubagentEvent(*info)
}

// emitSubagentEvent 推一条子代理概况事件。
// 与 chat:event 分开走一条通道，两边的消费方互不干扰（见 subagentRecorder 的说明）。
func (a *App) emitSubagentEvent(info SubagentInfo) {
	if a == nil || a.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(a.ctx, "subagent:event", info)
}

// ===== 查询接口（绑定给前端）=====

// ListSubagents 列出某主会话派生过的子代理：正在跑的 + 历史上跑完的。
func (a *App) ListSubagents(sessionID string) ([]SubagentInfo, error) {
	out := make([]SubagentInfo, 0, 4)
	if sessionID == "" || a.sessionStore == nil {
		return out, nil
	}

	// 1) 正在跑的：磁盘上还没有它们的最终状态
	seen := map[string]bool{}
	for _, info := range a.subagents.list(sessionID) {
		out = append(out, info)
		seen[info.RunID] = true
	}

	// 2) 跑完的：记在子代理自己的会话文件里（重启后依然在）
	sessions, err := a.sessionStore.ListSubagentSessions(sessionID)
	if err != nil {
		// 磁盘读失败不影响"正在跑的"那部分：面板少列几条历史，比整个面板报错好
		return out, err
	}
	for _, s := range sessions {
		if seen[s.ID] {
			continue
		}
		if s.Subagent != nil {
			out = append(out, *cloneSubagentInfo(s.Subagent))
			continue
		}
		// 有 ParentID 却没有概况：只可能是应用在它跑动中被关掉了（收尾那一次写入没发生）。
		// 报"已中断"而不是继续显示运行中——否则用户会一直等一个不会回来的东西。
		out = append(out, SubagentInfo{
			RunID:     s.ID,
			ParentID:  sessionID,
			Title:     s.Title,
			Status:    subagentStatusInterrupted,
			StartedAt: s.StartAt,
			EndedAt:   s.EndAt,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out, nil
}

// GetSubagentMessages 取某个子代理的完整消息流（面板里展开看它到底做了什么）。
//
// 这是"点开才看"的：子代理的中间过程可能有几百条消息，它们存在的意义就是**不**进
// 主会话的上下文——所以只在用户主动展开某一条时才从磁盘读。
func (a *App) GetSubagentMessages(runID string) ([]Message, error) {
	if runID == "" {
		return nil, fmt.Errorf("子代理 ID 不能为空")
	}
	s, err := a.sessionStore.GetSession(runID)
	if err != nil {
		return nil, err
	}
	if s.ParentID == "" {
		return nil, fmt.Errorf("%s 不是子代理会话", runID)
	}
	out := make([]Message, 0, len(s.Messages))
	for _, m := range s.Messages {
		if m.Role == RoleSystem {
			continue // 系统提示词不是"它做了什么"，面板不展示
		}
		out = append(out, m)
	}
	return out, nil
}
