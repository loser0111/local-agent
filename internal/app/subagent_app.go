package app

import (
	"fmt"
	"sort"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/diff"
	"wails-tmp/internal/store"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ===== 子代理的进度推送与查询（App 侧）=====
//
// 运行概况的内存注册表（agent.SubagentTracker / agent.CloneSubagentInfo）已下沉到
// internal/agent：它只依赖 store.SubagentInfo，与界面无关。
//
// 留在这一侧的只有三件必须碰 App 的事：
//   - subagentRecorder：把子代理的工具调用**翻译成独立事件通道**（要 a.ctx 发事件）；
//   - emitSubagentProgress / emitSubagentEvent：Wails 推送；
//   - ListSubagents / GetSubagentMessages：Wails 绑定查询入口。
//
// 为什么查询入口必须留在根包：ListSubagents 要把**内存里的运行中记录**与**磁盘上的
// 历史记录**（子代理自己的会话文件）合成一份列表，而 App 恰好同时握着这两者。

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
		r.app.subagents.ToolStart(r.runID, ev.ToolCall.Name)
	case ChatEventToolCallEnd:
		r.app.subagents.ToolEnd(r.runID)
	default:
		return
	}
	r.app.emitSubagentProgress(r.runID)
}

// EmitDiff 不推：子代理的改动属于它自己的会话，面板展开时按需加载，
// 推给主会话的 diff 面板等于把它算成了主会话的改动。
func (r *subagentRecorder) EmitDiff([]diff.DiffFile, int) {}

// FlushStream 子代理不流式，返回空实现即可
func (r *subagentRecorder) FlushStream() (func(string), func()) {
	return func(string) {}, func() {}
}

// emitSubagentProgress 把某子代理的最新进度推给前端（SubagentPane 订阅 subagent:event）
func (a *App) emitSubagentProgress(runID string) {
	if a == nil || a.ctx == nil {
		return
	}
	info := a.subagents.Get(runID)
	if info == nil {
		return
	}
	a.emitSubagentEvent(*info)
}

// emitSubagentEvent 推一条子代理概况事件。
// 与 chat:event 分开走一条通道，两边的消费方互不干扰（见 subagentRecorder 的说明）。
func (a *App) emitSubagentEvent(info store.SubagentInfo) {
	if a == nil || a.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(a.ctx, "subagent:event", info)
}

// ===== 查询接口（绑定给前端）=====

// ListSubagents 列出某主会话派生过的子代理：正在跑的 + 历史上跑完的。
func (a *App) ListSubagents(sessionID string) ([]store.SubagentInfo, error) {
	out := make([]store.SubagentInfo, 0, 4)
	if sessionID == "" || a.sessionStore == nil {
		return out, nil
	}

	// 1) 正在跑的：磁盘上还没有它们的最终状态
	seen := map[string]bool{}
	for _, info := range a.subagents.List(sessionID) {
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
			out = append(out, *agent.CloneSubagentInfo(s.Subagent))
			continue
		}
		// 有 ParentID 却没有概况：只可能是应用在它跑动中被关掉了（收尾那一次写入没发生）。
		// 报"已中断"而不是继续显示运行中——否则用户会一直等一个不会回来的东西。
		out = append(out, store.SubagentInfo{
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
func (a *App) GetSubagentMessages(runID string) ([]store.Message, error) {
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
	out := make([]store.Message, 0, len(s.Messages))
	for _, m := range s.Messages {
		if m.Role == store.RoleSystem {
			continue // 系统提示词不是"它做了什么"，面板不展示
		}
		out = append(out, m)
	}
	return out, nil
}
