package main

import (
	"fmt"
	"time"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
)

// ===== 实际发出的 LLM 请求快照：根包侧的装配 + Wails 绑定入口 =====
//
// 快照的**存储与脱敏**已下沉到 internal/llm/requestlog.go（RequestLog / RequestSnapshot /
// RedactMessages / SummaryMessageIndex）——"只留内存不落盘""工具定义只留名字与体积、
// 不带 JSON Schema"这几条理由都随它走了。
//
// 这里只剩两件事，而且都是**根包侧才有的上下文**撑起来的：
//
//   - snapshotLLMRequest：把 agent.RunControl（运行注册表）、agent.ContextStat（用量统计）、
//     装配好的工具视图拼成一份 llm.RequestSnapshot。这些类型属于根包 / context 域，
//     不该反过来要求 internal/llm 认识它们。
//   - GetLastLLMRequest：Wails 绑定方法。
//
// 有一条坑必须留着说明：快照里的 Messages **必须拷贝**，因为调用方（工具循环）
// 随后还会 append / 重建这条序列，共享底层数组会让"事后看到的"不是"当时发出去的"。

// snapshotLLMRequest 组装一次请求快照（内部做浅拷贝，避免与循环里的切片共享底层数组）
func snapshotLLMRequest(run *agent.RunControl, session *store.Session, turn int, modelID, systemPrompt string,
	messages []llm.LLMMessage, tools []llm.LLMTool, ctxStat *agent.ContextStat, compactedThisTurn bool) *llm.RequestSnapshot {
	snap := &llm.RequestSnapshot{
		Turn:              turn,
		Model:             modelID,
		At:                time.Now().UnixMilli(),
		SystemPrompt:      systemPrompt,
		SummaryIndex:      llm.SummaryMessageIndex(session, messages),
		CompactedThisTurn: compactedThisTurn,
	}
	if run != nil {
		snap.SessionID = run.SessionID
		snap.RunID = run.RunID
	}
	if session != nil {
		snap.CoveredMsgs = session.ContextCoveredUpTo
	}
	// 图片另做一次"去掉字节、留下描述"的处理，见 llm.RedactMessages。
	snap.Messages = llm.RedactMessages(messages)

	snap.ToolNames = make([]string, 0, len(tools))
	for _, t := range tools {
		snap.ToolNames = append(snap.ToolNames, t.Function.Name)
	}
	snap.ToolSchemaTokens = agent.ToolSchemaTokens(tools)
	if ctxStat != nil {
		snap.EstimatedTokens = ctxStat.UsedTokens
		snap.WindowTokens = ctxStat.WindowTokens
	}
	return snap
}

// GetLastLLMRequest 返回本会话最近一次真实发出的请求快照（压缩与预算之后的那一份）。
//
// 内存态：应用重启后、或本会话还没发过消息时返回 nil，界面据此提示"暂无记录"。
func (a *App) GetLastLLMRequest(sessionID string) (*llm.RequestSnapshot, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("会话 ID 不能为空")
	}
	return a.reqLog.Get(sessionID), nil
}
