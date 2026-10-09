package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"wails-tmp/internal/tool"
)

// ===== ask_user：模型主动向用户提问 =====
//
// 解决的问题：模型不确定怎么实现、不知道某个信息从哪拿、或者有几种做法需要用户拍板时，
// 原先只能把问题写在回复里然后结束这一轮——用户看到一段文字，得自己重新组织成一条消息，
// 而且下一轮模型还得重新推断上下文。
//
// 现在模型可以调用 ask_user：给出若干问题与候选项，后端**阻塞**等待用户在弹窗里作答，
// 把答复作为工具结果回填给模型，同一轮对话继续往下走。
//
// 与权限询问（permission.Broker）刻意分成两套回路：两者语义不同（一个是"允许做吗"、
// 一个是"你想怎么做"），fail 方向也不同（权限超时=拒绝，提问超时=用户未作答），
// 分开可以避免互相污染，也不去动已经验证过的权限链路。
//
// 与根包的边界：问答的数据结构、回路（Broker）与工具实现都与 UI 无关，放这里；
// App 侧的四个绑定方法（askUser / ResolveAskUser / CancelAskUser / GetPendingAsk）
// 要发事件、要看 a.ctx，留在根包 ask_app.go。

// 规模上限：一次提问最多几个问题、每个问题最多几个选项
const (
	maxAskQuestions = 4
	maxAskOptions   = 4
)

// AskOption 一个候选项
type AskOption struct {
	Label       string `json:"label"`                 // 选项文案（回传给模型的就是它）
	Description string `json:"description,omitempty"` // 选项说明（帮助用户判断）
}

// AskQuestion 一个问题
type AskQuestion struct {
	ID            string      `json:"id,omitempty"`
	Header        string      `json:"header,omitempty"`      // 短标签（弹窗上的分组标题，如"状态管理"）
	Question      string      `json:"question"`              // 问题正文
	Options       []AskOption `json:"options,omitempty"`     // 候选项；为空表示纯信息收集
	MultiSelect   bool        `json:"multiSelect,omitempty"` // 是否可多选
	AllowFreeText bool        `json:"allowFreeText"`         // 是否允许自由输入（默认允许）
}

// AskRequest 一次提问（可能含多个问题）
type AskRequest struct {
	ID        string        `json:"id"`
	SessionID string        `json:"sessionId"`
	Questions []AskQuestion `json:"questions"`
	CreatedAt int64         `json:"createdAt"`
}

// AskItem 单个问题的答复
type AskItem struct {
	QuestionID string   `json:"questionId,omitempty"`
	Selected   []string `json:"selected,omitempty"` // 选中的选项 label
	Text       string   `json:"text,omitempty"`     // 自由输入内容
}

// AskAnswer 用户的答复
type AskAnswer struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId,omitempty"`
	Answers   []AskItem `json:"answers"`
	Cancelled bool      `json:"cancelled,omitempty"` // 用户跳过 / 超时 / 会话取消
}

// ===== 提问回路 =====

// AskBroker 挂起提问的登记处：一次提问从 register 到 Resolve/Cancel/超时，
// 期间双方（模型侧等答复、前端侧作答）只通过它碰面。
type AskBroker struct {
	mu      sync.Mutex
	timeout time.Duration
	seq     int64
	pending map[string]*askPending
}

type askPending struct {
	sessionID string
	req       AskRequest
	ch        chan AskAnswer
}

// NewAskBroker 创建回路；timeout<=0 时默认 10 分钟
// （比权限询问长：权限是"要不要放行"一眼可判，提问往往需要用户想一会儿或去查东西）
func NewAskBroker(timeout time.Duration) *AskBroker {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	return &AskBroker{timeout: timeout, pending: map[string]*askPending{}}
}

// Register 登记一个挂起提问
func (b *AskBroker) Register(sessionID string, req AskRequest) (string, chan AskAnswer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := fmt.Sprintf("ask_%d_%d", time.Now().UnixMilli(), b.seq)
	ch := make(chan AskAnswer, 1)
	if b.pending == nil {
		b.pending = map[string]*askPending{}
	}
	b.pending[id] = &askPending{sessionID: sessionID, req: req, ch: ch}
	return id, ch
}

// Pending 返回某会话挂起的提问（切换会话后回到该会话时，弹窗据此重现）。
// 一个会话同时只可能有一个挂起提问——工具调用是串行的。
func (b *AskBroker) Pending(sessionID string) *AskRequest {
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

// SetRequest 补全挂起请求的本体（ID 生成后才能写回）
func (b *AskBroker) SetRequest(id string, req AskRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.pending[id]; ok {
		p.req = req
	}
}

// Forget 注销挂起提问
func (b *AskBroker) Forget(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

// Resolve 前端答复；未知 ID 说明已超时或已被取消（迟到答复）
func (b *AskBroker) Resolve(ans AskAnswer) error {
	b.mu.Lock()
	p, ok := b.pending[ans.ID]
	if ok {
		delete(b.pending, ans.ID)
	}
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("提问不存在或已超时: %s", ans.ID)
	}
	ans.SessionID = p.sessionID
	p.ch <- ans
	return nil
}

// CancelSession 取消某会话全部挂起的提问（用户点了停止）。返回被取消的数量。
func (b *AskBroker) CancelSession(sessionID string) int {
	b.mu.Lock()
	targets := make([]*askPending, 0, len(b.pending))
	for id, p := range b.pending {
		if p.sessionID == sessionID {
			targets = append(targets, p)
			delete(b.pending, id)
		}
	}
	b.mu.Unlock()
	for _, p := range targets {
		// 通道缓冲为 1 且已从 pending 摘除，此处不会阻塞
		p.ch <- AskAnswer{Cancelled: true}
	}
	return len(targets)
}

// Wait 等待用户作答。
//
// 超时、会话取消与 ctx 取消（硬取消）都一样：把"用户未作答"回给模型，让它自行决策，
// 而不是让整轮对话失败——也避免用户点了停止之后还要再等 10 分钟。
func (b *AskBroker) Wait(ctx context.Context, id string, ch chan AskAnswer) (AskAnswer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(b.timeout)
	defer timer.Stop()

	select {
	case ans := <-ch:
		return ans, nil
	case <-timer.C:
		b.Forget(id)
		return AskAnswer{ID: id, Cancelled: true}, nil
	case <-ctx.Done():
		b.Forget(id)
		return AskAnswer{ID: id, Cancelled: true}, nil
	}
}

// ===== 工具实现 =====

// AskUserTool 让模型向用户提问的工具。asker 由根包注入（发事件 + 阻塞等待）。
type AskUserTool struct {
	*tool.BaseTool
	asker    func(ctx context.Context, req AskRequest) (AskAnswer, error)
	required []string
}

// RequiredParams 必填参数（供直出给模型时生成 JSON Schema）
func (t *AskUserTool) RequiredParams() []string { return t.required }

// NewAskUserTool 构造提问工具。asker 为 nil 时工具**仍会装配**，但调用会返回
// "界面未就绪"——便于测试与降级，也比"注册了却每次失败"更可控。
func NewAskUserTool(asker func(ctx context.Context, req AskRequest) (AskAnswer, error)) *AskUserTool {
	params, required := tool.FileToolParams([]tool.FileArg{
		{Name: "questions", Type: "array", Description: "要询问用户的问题列表（1-4 个）", Required: true},
	})
	return &AskUserTool{
		BaseTool: &tool.BaseTool{
			Name: tool.ToolAskUser,
			Description: "当你缺少做出决定所必需的信息、或者有多种可行做法需要用户拍板时，调用本工具向用户提问。" +
				"可以为每个问题给出 2-4 个候选项（用户也可以自由输入）。调用后会**暂停等待用户作答**，" +
				"答复会作为工具结果返回给你，随后继续执行。" +
				"适合的场景：需求有歧义、要在几种实现方案中选择、需要用户提供只有他知道的信息（密钥位置、部署环境、业务口径）。" +
				"不适合：自己能查证的事实（先用 read_file / grep）、能自己判断的琐碎细节。",
			Parameters: params,
		},
		asker:    asker,
		required: required,
	}
}

func (t *AskUserTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	questions, err := parseAskQuestions(args)
	if err != nil {
		return "", err
	}
	if t.asker == nil {
		return "", fmt.Errorf("当前环境不支持向用户提问（界面未就绪）")
	}
	ans, err := t.asker(ctx, AskRequest{Questions: questions})
	if err != nil {
		return "", err
	}
	if ans.Cancelled {
		return "", fmt.Errorf("用户没有作答（跳过或超时）。请基于现有信息自行决策并说明你的假设，然后继续")
	}
	return formatAskResult(questions, ans), nil
}

// parseAskQuestions 解析并校验工具参数
func parseAskQuestions(args map[string]interface{}) ([]AskQuestion, error) {
	raw, ok := args["questions"].([]interface{})
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("questions 参数是必需的：至少提供一个问题")
	}
	if len(raw) > maxAskQuestions {
		return nil, fmt.Errorf("一次最多提 %d 个问题，收到 %d 个；请合并或分批提问", maxAskQuestions, len(raw))
	}

	out := make([]AskQuestion, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("第 %d 个问题的格式不正确（应为对象）", i+1)
		}
		q := AskQuestion{
			Question:      strings.TrimSpace(askStringField(m, "question")),
			Header:        strings.TrimSpace(askStringField(m, "header")),
			MultiSelect:   tool.BoolArg(m, "multiSelect"),
			AllowFreeText: true,
		}
		if v, ok := m["allowFreeText"].(bool); ok {
			q.AllowFreeText = v
		}
		if q.Question == "" {
			return nil, fmt.Errorf("第 %d 个问题缺少 question 字段", i+1)
		}
		if opts, ok := m["options"].([]interface{}); ok {
			if len(opts) > maxAskOptions {
				return nil, fmt.Errorf("第 %d 个问题最多 %d 个选项，收到 %d 个", i+1, maxAskOptions, len(opts))
			}
			for _, o := range opts {
				om, ok := o.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("第 %d 个问题的选项格式不正确（应为对象）", i+1)
				}
				label := strings.TrimSpace(askStringField(om, "label"))
				if label == "" {
					continue // 空选项直接忽略，不值得让整次提问失败
				}
				q.Options = append(q.Options, AskOption{
					Label:       label,
					Description: strings.TrimSpace(askStringField(om, "description")),
				})
			}
		}
		out = append(out, q)
	}
	return out, nil
}

// askStringField 从 map 里取字符串字段（容忍非字符串类型）
func askStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// formatAskResult 把答复整理成给模型看的文本：编号对应问题，明确区分"选项"与"自由输入"
func formatAskResult(questions []AskQuestion, ans AskAnswer) string {
	var sb strings.Builder
	sb.WriteString("用户答复：\n")
	for i, q := range questions {
		var item AskItem
		if i < len(ans.Answers) {
			item = ans.Answers[i]
		}
		fmt.Fprintf(&sb, "%d. 问题：%s\n", i+1, q.Question)

		selected := make([]string, 0, len(item.Selected))
		for _, s := range item.Selected {
			if t := strings.TrimSpace(s); t != "" {
				selected = append(selected, t)
			}
		}
		text := strings.TrimSpace(item.Text)
		switch {
		case len(selected) > 0 && text != "":
			fmt.Fprintf(&sb, "   选择：%s\n   补充说明：%s\n", strings.Join(selected, "、"), text)
		case len(selected) > 0:
			fmt.Fprintf(&sb, "   选择：%s\n", strings.Join(selected, "、"))
		case text != "":
			fmt.Fprintf(&sb, "   回答：%s\n", text)
		default:
			sb.WriteString("   用户未作答\n")
		}
	}
	sb.WriteString("请按上述答复继续。")
	return sb.String()
}
