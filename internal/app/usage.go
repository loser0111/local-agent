package app

import (
	"sync"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
)

// UsageDetail 「用量明细」弹窗的数据。
//
// 刻意**不并进 agent.ContextStat**：后者是「切会话即拉」的高频轻量接口，
// 而明细要额外取请求快照、算派生指标，塞进去会拖慢每一次切换。
type UsageDetail struct {
	SessionID string `json:"sessionId"`
	// HasData 会话累计轮数为 0。老会话（统计上线前创建）以及从未真正发过请求的会话为 false，
	// 前端据此显示空态而不是一排 0。
	HasData bool `json:"hasData"`

	Last   store.TokenUsage  `json:"last"`   // 本轮（最近一次请求）
	Totals store.UsageTotals `json:"totals"` // 会话累计
	Cache  store.CacheView   `json:"cache"`

	// Context 上下文构成与校准系数。与指示器用的是同一份计算，
	// 保证弹窗里的百分比和指示器上的百分比**必然一致**（否则用户会以为哪里出错了）。
	Context *agent.ContextStat `json:"context,omitempty"`
	// Request 最近一次真实发出的请求快照（工具定义占用、摘要覆盖范围等）。
	// 为 nil 表示本进程还没发过请求（重启后即丢失，该快照只在内存里）。
	Request *llm.RequestSnapshot `json:"request,omitempty"`

	// RawUsage 最近一次响应的原始 usage JSON，排障用。
	// 成本极低，但能省掉未来所有「到底是哪一层错了」的扯皮。
	RawUsage string `json:"rawUsage,omitempty"`
}

// ===== 每会话最近一次的用量（内存态）=====

// usageRecord 一次请求的用量快照
type usageRecord struct {
	Usage    store.TokenUsage
	RawUsage string
	At       int64
}

// usageLog 每会话最近一次的用量，与 llm.RequestLog 同一个定位与生命周期。
//
// 为什么不落进会话文件：它是"看最近这次"的排障视图，不是审计日志。
// 落盘会让每一轮都多一次整份会话文件的写 I/O，而这个信息只在刚跑完那几分钟里有价值。
// 会话累计（需要跨重启保留）走的是 store.Session.UsageTotals，两者刻意分开。
type usageLog struct {
	mu    sync.Mutex
	items map[string]*usageRecord
}

// record 记下一次用量。对 nil 接收者安全：测试里会直接构造零值 App，那一路径不该 panic。
func (l *usageLog) record(sessionID string, u store.TokenUsage, raw string, at int64) {
	if l == nil || sessionID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.items == nil {
		l.items = map[string]*usageRecord{}
	}
	l.items[sessionID] = &usageRecord{Usage: u, RawUsage: raw, At: at}
}

// get 取某会话最近一次用量；没有则返回 nil（调用方据此显示空态）
func (l *usageLog) get(sessionID string) *usageRecord {
	if l == nil || sessionID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.items[sessionID]
}

// forget 丢弃某会话的用量记录（删除会话时调用）。
// 不清理的话内存里会留下永远够不着的条目——量小，但"删了还在"是会被发现的那类残留。
func (l *usageLog) forget(sessionID string) {
	if l == nil || sessionID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.items, sessionID)
}

// ===== 用量事件 =====

// UsageEvent 一轮对话的用量推送（ChatEvent.Type = ChatEventUsage）。
//
// 带上会话累计值，是为了让开着的弹窗实时更新——只发本轮的话前端得自己累加，
// 而"哪些轮算进累计"这个口径只应该有一处定义（在后端）。
type UsageEvent struct {
	// Turn 这是本会话的第几次模型请求（即累计轮数），便于前端判断"更新的是不是新的一轮"。
	Turn   int               `json:"turn"`
	Last   store.TokenUsage  `json:"last"`
	Totals store.UsageTotals `json:"totals"`
	Cache  store.CacheView   `json:"cache"`
}
