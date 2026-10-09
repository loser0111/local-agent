package llm

import (
	"sync"

	"wails-tmp/internal/store"
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
//
// 本文件只负责**存储、脱敏与两个纯函数**。"把根包侧的运行上下文（runControl、
// ContextStat、工具视图）组装成一份 RequestSnapshot" 仍在根包的 requestlog.go——
// 那些类型属于根包 / context 域，不该反过来要求这个包认识它们。

// RequestSnapshot 一次真实发出的请求快照
type RequestSnapshot struct {
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

// RequestLog 每会话最近一次请求快照
type RequestLog struct {
	mu    sync.Mutex
	items map[string]*RequestSnapshot
}

// Record 记下一次真实发出的请求。对 nil 接收者安全：
// 测试里会直接构造零值 App，那一路径不该 panic。
func (l *RequestLog) Record(snap *RequestSnapshot) {
	if l == nil || snap == nil || snap.SessionID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.items == nil {
		l.items = map[string]*RequestSnapshot{}
	}
	l.items[snap.SessionID] = snap
}

// Get 取某会话最近一次请求快照；没有则返回 nil
func (l *RequestLog) Get(sessionID string) *RequestSnapshot {
	if l == nil || sessionID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.items[sessionID]
}

// Forget 会话删除时清理
func (l *RequestLog) Forget(sessionID string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.items, sessionID)
}

// RedactMessages 拷贝一份消息序列，并把图片换成"去掉字节、留下描述"的占位。
//
// 请求快照是给人看的排障视图。几 MB 的 base64 进去之后这份快照既没法读
// （界面与日志都会被撑爆）又占内存，而排障真正需要的只是"这条消息带了几张多大的图"。
// 底层数组是浅拷贝，但每条带图的消息都换成新的 Images 切片，因此不会改到调用方的数据。
func RedactMessages(messages []LLMMessage) []LLMMessage {
	out := make([]LLMMessage, len(messages))
	copy(out, messages)
	for i := range out {
		if len(out[i].Images) == 0 {
			continue
		}
		imgs := make([]LLMImage, len(out[i].Images))
		for j, img := range out[i].Images {
			imgs[j] = LLMImage{
				MediaType: img.MediaType,
				Bytes:     img.Bytes,
				Width:     img.Width,
				Height:    img.Height,
				Redacted:  true,
			}
		}
		out[i].Images = imgs
	}
	return out
}

// SummaryMessageIndex 摘要说明消息在序列里的下标；没有摘要时为 -1。
//
// 位置是固定的：buildLLMMessages 恒把 system 放在第 0 条，
// withContextSummary 把摘要插在原文之前，所以它落在第 1 条。
// 写成函数而不是在调用处硬编码 1，是为了让这个约定只有一处、且可被测试。
//
// 只看会话上的两个摘要字段，不看消息内容：这是"装配约定"而非"内容分析"，
// 所以即便序列被后续处理改过，位置约定仍然成立。
func SummaryMessageIndex(session *store.Session, messages []LLMMessage) int {
	if session == nil || len(messages) < 2 {
		return -1
	}
	if session.ContextCoveredUpTo <= 0 || session.ContextSummary == "" {
		return -1
	}
	return 1
}
