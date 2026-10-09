package permission

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ===== 授权请求 / 应答协议 =====

// Request 送到前端的授权请求
type Request struct {
	ID        string   `json:"id"`
	SessionID string   `json:"sessionId"`
	Tool      string   `json:"tool"`
	Subject   string   `json:"subject"` // 待确认内容的单行摘要
	Units     []string `json:"units"`   // 命令类主体分解后的各段（供界面展示与生成规则）
	Stage     string   `json:"stage"`   // 判定落在管线的哪一步
	Reason    string   `json:"reason"`  // 为什么要问
	CreatedAt int64    `json:"createdAt"`
}

// Answer 前端回传的授权答复。
// Scope 取值：once=仅此一次；session=本会话放行（逐段精确记忆）；rule=写入规则文件。
type Answer struct {
	ID       string `json:"id"`
	Decision string `json:"decision"` // allow / deny
	Scope    string `json:"scope"`    // once / session / rule
	Rule     string `json:"rule"`     // scope=rule 时的规则文本
	Layer    string `json:"layer"`    // scope=rule 时的写入层：user / project / local
}

// ===== 询问回路（broker）=====

// Broker 授权请求的等待-唤醒回路。
// 后端在工具循环里阻塞等待，前端弹窗把答复经 Resolve 灌回来。
//
// 这一层刻意不问"谁在等"：登记与等待分成 Register / Wait 两步，
// 是因为请求 ID 要在**送出事件之前**就确定（前端要按 ID 应答），
// 而等待方可能是另一个 goroutine（工具的 ctx 也可能在等待期间结束）。
type Broker struct {
	mu      sync.Mutex
	timeout time.Duration
	seq     int64
	pending map[string]*pendingRequest
}

type pendingRequest struct {
	sessionID string
	req       Request
	ch        chan Answer
}

// SetRequest 补全挂起请求的本体（ID 生成后才能写回），供兜底查询使用
func (b *Broker) SetRequest(id string, req Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.pending[id]; ok {
		p.req = req
	}
}

// Pending 返回某会话挂起的授权请求；没有则返回 nil。
// 前端在模型运行期间轮询它作为兜底：事件万一没送达，弹窗仍能补上。
func (b *Broker) Pending(sessionID string) *Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.pending {
		if p.sessionID == sessionID {
			req := p.req
			return &req
		}
	}
	return nil
}

// NewBroker 创建回路；timeout<=0 时默认 5 分钟
func NewBroker(timeout time.Duration) *Broker {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &Broker{timeout: timeout, pending: map[string]*pendingRequest{}}
}

// Register 登记一个挂起请求，返回请求 ID 与等待通道
func (b *Broker) Register(sessionID string) (string, chan Answer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := fmt.Sprintf("perm_%d_%d", time.Now().UnixMilli(), b.seq)
	ch := make(chan Answer, 1)
	if b.pending == nil {
		b.pending = map[string]*pendingRequest{}
	}
	b.pending[id] = &pendingRequest{sessionID: sessionID, ch: ch}
	return id, ch
}

// PendingCount 当前挂起的请求数（测试与诊断用）
func (b *Broker) PendingCount(sessionID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, p := range b.pending {
		if p.sessionID == sessionID {
			n++
		}
	}
	return n
}

// Forget 注销挂起请求
func (b *Broker) Forget(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

// Resolve 前端答复。未知 ID 说明已超时或已被取消（迟到答复），返回错误供界面提示。
func (b *Broker) Resolve(ans Answer) error {
	b.mu.Lock()
	p, ok := b.pending[ans.ID]
	if ok {
		delete(b.pending, ans.ID)
	}
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("授权请求不存在或已超时: %s", ans.ID)
	}
	p.ch <- ans
	return nil
}

// CancelSession 取消某会话全部挂起的请求：灌入一个拒绝答复，让等待方立即返回。
// 返回被取消的请求数。
func (b *Broker) CancelSession(sessionID string) int {
	b.mu.Lock()
	targets := make([]*pendingRequest, 0, len(b.pending))
	for id, p := range b.pending {
		if p.sessionID == sessionID {
			targets = append(targets, p)
			delete(b.pending, id)
		}
	}
	b.mu.Unlock()
	for _, p := range targets {
		// 通道缓冲为 1 且此处已从 pending 摘除，不会阻塞
		p.ch <- Answer{Decision: "deny", Scope: "once"}
	}
	return len(targets)
}

// Wait 等待答复。超时、会话取消、ctx 结束都返回错误，调用方一律按拒绝处理。
func (b *Broker) Wait(id string, ch chan Answer, ctx context.Context) (Answer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(b.timeout)
	defer timer.Stop()

	select {
	case ans := <-ch:
		d, ok := ParseDecision(ans.Decision)
		if !ok || d != DecisionAllow {
			return Answer{Decision: DecisionDeny.String(), Scope: ans.Scope}, nil
		}
		// 归一化决定字段的写法（避免 "Allow" 这类大小写差异被下游误判为拒绝）
		return Answer{Decision: DecisionAllow.String(), Scope: ans.Scope, Rule: ans.Rule, Layer: ans.Layer}, nil
	case <-timer.C:
		b.Forget(id)
		return Answer{}, fmt.Errorf("等待用户授权超时（%s）", b.timeout)
	case <-ctx.Done():
		b.Forget(id)
		return Answer{}, fmt.Errorf("授权等待已取消")
	}
}

// AnswerAllows 答复是否构成放行。无法识别的决定一律视为不放行（fail closed）。
func AnswerAllows(ans Answer) bool {
	d, ok := ParseDecision(ans.Decision)
	return ok && d == DecisionAllow
}
