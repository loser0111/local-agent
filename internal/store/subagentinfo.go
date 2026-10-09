package store

// SubagentInfo 子代理会话的运行概况：派给它的任务、跑完的结论、进度与它改过的文件。
//
// 它是会话的持久化字段（Session.Subagent），所以与会话数据模型同处 store。
// 「运行中的」那份由 main 侧的内存 tracker 维护（每执行一次工具就变一次，不必落盘）；
// 「跑完的」写回会话文件（重启后仍能查看，而"回退它改过的文件"本来就要靠那个会话）。
type SubagentInfo struct {
	RunID       string   `json:"runId"`    // = 子代理会话 ID（消息、diff、checkpoint 都用它）
	ParentID    string   `json:"parentId"` // 父会话 ID
	Title       string   `json:"title"`
	Task        string   `json:"task"`   // 派给它做的事（title 是它的截断版）
	Status      string   `json:"status"` // running | completed | failed | cancelled | interrupted
	Model       string   `json:"model,omitempty"`
	Step        int      `json:"step"` // 已执行的工具调用数
	CurrentTool string   `json:"currentTool,omitempty"`
	StartedAt   int64    `json:"startedAt"`
	EndedAt     int64    `json:"endedAt,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Files       []string `json:"files,omitempty"` // 它改过的文件
	Declined    []string `json:"declined,omitempty"`
	Error       string   `json:"error,omitempty"`
}
