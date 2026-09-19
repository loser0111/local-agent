package main

import (
	"fmt"
	"sync"
	"time"
)

// ===== 实际发出的 LLM 请求快照（排障用）=====
//
// 会话里存的 Messages 是**原文**，而真正发给模型的是经过三层处理后的序列：
// 摘要前缀替换 → （必要时）确定性截断 → 单条工具结果预算。两者不同，
// 且此前没有任何地方能看到后者——压缩到底做了什么、模型实际看到了什么，全靠猜。
//
// 这里记录的是**真实发出的那一份**（在发起请求前做的浅拷贝），不是事后重算：
// 重算需要重新装配工具视图（会去连 MCP），而且未必与当时一致。
//
// 三条刻意约束：
//   - 只保留每会话最近一次，且只在内存里。它是排障视图，不是审计日志；
//     落盘会让每次对话都多一次写 I/O，而需要看的时候通常就是刚发完那次。
//   - 只记录内部统一格式（OpenAI 形状）。Anthropic 发送前还会由适配器再转一次
//     （system 抽出、tool 结果变 content 块），这里不做二次转换——界面上会标注这点。
//   - 工具定义只留名字与体积估算，不带 JSON Schema：那是静态的，
//     而且几十个工具的 schema 会让这份快照变得很大，对排障没有增量价值。

// LLMRequestSnapshot 一次真实发出的请求快照
type LLMRequestSnapshot struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId,omitempty"`
	Turn      int    `json:"turn"`
	Model     string `json:"model"`
	At        int64  `json:"at"`

	SystemPrompt string       `json:"systemPrompt"`
	Messages     []LLMMessage `json:"messages"`

	// SummaryIndex 摘要说明消息在 Messages 里的下标；-1 表示本次没有摘要
	SummaryIndex int `json:"summaryIndex"`

	ToolNames []string `json:"toolNames"`
	// ToolSchemaTokens 工具定义占用的估算 token（不含在 Messages 里，但是固定开销）
	ToolSchemaTokens int `json:"toolSchemaTokens"`

	EstimatedTokens   int  `json:"estimatedTokens"`
	WindowTokens      int  `json:"windowTokens"`
	CompactedThisTurn bool `json:"compactedThisTurn"` // 本轮是否刚做过摘要压缩
	CoveredMsgs       int  `json:"coveredMsgs"`       // 摘要累计覆盖的消息条数
}

// llmRequestLog 每会话最近一次请求快照
type llmRequestLog struct {
	mu    sync.Mutex
	items map[string]*LLMRequestSnapshot
}

// record 记下一次真实发出的请求。对 nil 接收者安全：
// 测试里会直接构造零值 App，那一路径不该 panic。
func (l *llmRequestLog) record(snap *LLMRequestSnapshot) {
	if l == nil || snap == nil || snap.SessionID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.items == nil {
		l.items = map[string]*LLMRequestSnapshot{}
	}
	l.items[snap.SessionID] = snap
}

// get 取某会话最近一次请求快照；没有则返回 nil
func (l *llmRequestLog) get(sessionID string) *LLMRequestSnapshot {
	if l == nil || sessionID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.items[sessionID]
}

// forget 会话删除时清理
func (l *llmRequestLog) forget(sessionID string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.items, sessionID)
}

// summaryMessageIndex 摘要说明消息在序列里的下标；没有摘要时为 -1。
//
// 位置是固定的：buildLLMMessages 恒把 system 放在第 0 条，
// withContextSummary 把摘要插在原文之前，所以它落在第 1 条。
// 写成函数而不是在调用处硬编码 1，是为了让这个约定只有一处、且可被测试。
func summaryMessageIndex(session *Session, messages []LLMMessage) int {
	if session == nil || len(messages) < 2 {
		return -1
	}
	if session.ContextCoveredUpTo <= 0 || session.ContextSummary == "" {
		return -1
	}
	return 1
}

// snapshotLLMRequest 组装一次请求快照（内部做浅拷贝，避免与循环里的切片共享底层数组）
func snapshotLLMRequest(run *runControl, session *Session, turn int, modelID, systemPrompt string,
	messages []LLMMessage, tools []LLMTool, ctxStat *ContextStat, compactedThisTurn bool) *LLMRequestSnapshot {

	snap := &LLMRequestSnapshot{
		Turn:              turn,
		Model:             modelID,
		At:                time.Now().UnixMilli(),
		SystemPrompt:      systemPrompt,
		SummaryIndex:      summaryMessageIndex(session, messages),
		CompactedThisTurn: compactedThisTurn,
	}
	if run != nil {
		snap.SessionID = run.sessionID
		snap.RunID = run.runID
	}
	if session != nil {
		snap.CoveredMsgs = session.ContextCoveredUpTo
	}
	// 必须拷贝：循环随后还会 append/重建 messages，共享底层数组会让快照跟着变
	snap.Messages = make([]LLMMessage, len(messages))
	copy(snap.Messages, messages)

	snap.ToolNames = make([]string, 0, len(tools))
	for _, t := range tools {
		snap.ToolNames = append(snap.ToolNames, t.Function.Name)
	}
	snap.ToolSchemaTokens = toolSchemaTokens(tools)
	if ctxStat != nil {
		snap.EstimatedTokens = ctxStat.UsedTokens
		snap.WindowTokens = ctxStat.WindowTokens
	}
	return snap
}

// GetLastLLMRequest 返回本会话最近一次真实发出的请求快照（压缩与预算之后的那一份）。
//
// 内存态：应用重启后、或本会话还没发过消息时返回 nil，界面据此提示"暂无记录"。
func (a *App) GetLastLLMRequest(sessionID string) (*LLMRequestSnapshot, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("会话 ID 不能为空")
	}
	return a.reqLog.get(sessionID), nil
}
