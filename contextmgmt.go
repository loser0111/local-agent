package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// ===== 上下文管理（P1-B）=====
//
// 目标：普通聊天也能跑长任务。三层手段，从便宜到昂贵：
//
//	1. 用量估算：用响应里回传的**真实** input token 当锚点，锚点之间按字符估增量。
//	2. 摘要式压缩：接近窗口阈值时把早期对话交给模型压成结构化摘要，保留最近若干条原文。
//	3. 单条工具结果预算：一个 read_file 就能吃掉大半个窗口，这层不依赖压缩。
//
// 三条不变式（破了任何一条都会出问题）：
//
//   - **会话里永远保留完整原文**，摘要只在构建请求时生效。压缩因此是可逆的：
//     清掉 ContextSummary 三个字段就回到全量。原文是用户资产，不能因为省 token 就丢掉。
//   - **摘要切点只能落在非 tool 结果的 user 消息之前**。tool 结果必须与它对应的
//     assistant(tool_calls) 同侧，被切开的话下一次请求会被 API 直接拒绝。
//   - **压缩失败不能阻断这一轮**：一律降级为确定性截断（compactMessages），
//     宁可这次少省点 token，也不能让对话发不出去。

const (
	// contextCompactRatio 触发自动压缩的用量比例（窗口占用超过它就开始压）
	contextCompactRatioNum = 70 // 百分比，用整数避免浮点比较的边界抖动
	// contextDefaultWindow 模型未配置上下文窗口时的保守默认值
	contextDefaultWindow = 32000
	// contextKeepRecentMsgs 压缩时保留的最近消息条数（近似"最近若干轮"）
	contextKeepRecentMsgs = 12
	// contextMinCompressMsgs 待压缩消息少于这么多条就不压：不值得多一次调用，
	// 而且容易把本来就短的上下文压没
	contextMinCompressMsgs = 8
	// contextSummaryMaxChars 摘要正文的长度上限（超出截断）
	contextSummaryMaxChars = 8000
	// compactTranscriptLimit 摘要素材的总长度上限。
	// 摘要请求本身也不能超长，否则压缩会以另一种方式失败。
	compactTranscriptLimit = 60000
	// compactTranscriptMsgLimit 摘要素材里单条消息的截断长度
	compactTranscriptMsgLimit = 2000

	// contextToolResultBudget 单条工具结果**发给模型**的字符上限。
	// 这是一道独立防线：read_file 一次默认 2000 行，单条结果就能吃掉大半个窗口。
	contextToolResultBudget = 24000
	contextToolResultHead   = 18000
	contextToolResultTail   = 4000

	// contextCompactCmd 手动压缩命令（/compact）
	contextCompactCmd = "compact"
	// contextStatCmd 上下文统计命令（/context-stat）。纯读，不改状态。
	contextStatCmd = "context-stat"
)

// ===== 用量估算 =====

// estimateTokens 字符级估算：中日韩字符约 1 token/字，其余约 4 字符/token。
//
// 系数很粗，但这套估算只在**两次真实用量锚点之间**使用，误差不累积——
// 这是它敢用粗略系数的前提。没有锚点时的整体估算只是兜底。
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + other/4
}

// isCJK 常用中日韩字符区段（含中文标点与假名/谚文）
func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK 统一表意
		return true
	case r >= 0x3400 && r <= 0x4DBF: // 扩展 A
		return true
	case r >= 0xF900 && r <= 0xFAFF: // 兼容表意
		return true
	case r >= 0x3000 && r <= 0x303F: // 中文标点
		return true
	case r >= 0x3040 && r <= 0x30FF: // 平假名/片假名
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // 谚文
		return true
	}
	return false
}

// scaleTokens 把字符估算按校准系数缩放。**这是全项目唯一的缩放入口**：
//
// 分散成多处各乘各的，早晚会有人漏乘一处或者重复乘（同一条消息既进 used 又进 saved）。
// 系数为 0/负数时按 1.0 处理——绝不能让估算变成"乘 0"，那会把用量显示成 0，
// 用户会以为上下文是空的。
func scaleTokens(n int, calib float64) int {
	if calib <= 0 {
		calib = calibDefault
	}
	v := int(math.Round(float64(n) * calib))
	if v < 0 {
		return 0
	}
	return v
}

// normalizeCalib 把调用方传进来的系数收进合法区间，并保证"没有系数"落在 1.0。
// 单独成函数是为了让 CalibRatio 与 SavedTokens 用上同一个值——两者分头回落，
// 报告里就会出现"系数显示 1.0、saved 却是 0"这种自相矛盾。
func normalizeCalib(calib float64) float64 {
	if calib <= 0 {
		return calibDefault
	}
	return clampCalib(calib)
}

// tokenAnchor 一次真实用量锚点：由 MsgCount 条消息构成的请求，
// 实测消耗了 InputTokens 个输入 token。
type tokenAnchor struct {
	InputTokens int
	MsgCount    int
}

// estimateSeqTokens 估算当前消息序列的输入 token。
// 有锚点时 = 锚点 + 其后新增消息的估算；否则整体估算。
func estimateSeqTokens(messages []LLMMessage, anchor *tokenAnchor) int {
	if anchor != nil && anchor.InputTokens > 0 && anchor.MsgCount >= 0 && anchor.MsgCount <= len(messages) {
		delta := 0
		for _, m := range messages[anchor.MsgCount:] {
			delta += messageTokens(m)
		}
		return anchor.InputTokens + delta
	}
	total := 0
	for _, m := range messages {
		total += messageTokens(m)
	}
	return total
}

// imageTokens 估算一张图片占用的输入 token。
//
// 用 Anthropic 公开的口径 tokens ≈ (宽 × 高) / 750，而**不是**按 base64 字符数估：
// 图片的计费只与像素数有关，与字节数基本无关。照字符估会得出完全相反的结论——
// 一张 200KB 的 1568×1568 截图（≈3270 token）比一张 2MB 的 4000×3000 照片贵得多
// （后者在入站时已被压到 1568px 上限）。
//
// 向上取整：宁可高估。压缩判定宁早勿晚——早压一次只是多花一次摘要调用，
// 压晚了会直接把请求打成超限错误。
func imageTokens(w, h int) int {
	if w <= 0 || h <= 0 {
		return 0
	}
	t := (w*h + 749) / 750
	if t < 1 {
		t = 1
	}
	return t
}

// messageTokens 单条消息的估算（含 role 等结构开销）
func messageTokens(m LLMMessage) int {
	n := estimateTokens(m.Content) + 4
	if m.ToolCallID != "" {
		n += estimateTokens(m.ToolCallID)
	}
	for _, tc := range m.ToolCalls {
		n += estimateTokens(tc.Function.Name) + estimateTokens(tc.Function.Arguments) + 8
	}
	for _, img := range m.Images {
		n += imageTokens(img.Width, img.Height)
	}
	return n
}

// storedMessageTokens 估算会话里存的 Message（注意不是发给模型的 LLMMessage）。
//
// 两者字段名相同但不能互相转换：Message.ToolCalls 是 []ToolCall（会话记录），
// LLMMessage.ToolCalls 是 []LLMToolCall（协议格式）。所以拆成两个函数而不是做类型转换。
func storedMessageTokens(m Message) int {
	n := estimateTokens(m.Content) + 4
	if m.ToolCallID != "" {
		n += estimateTokens(m.ToolCallID)
	}
	for _, tc := range m.ToolCalls {
		// 参数用 %v 粗估即可：这里只是为了算出"压缩前大概多大"，
		// 精确值由锚点负责，不需要为了几个 token 去序列化 JSON
		n += estimateTokens(tc.Name) + estimateTokens(fmt.Sprintf("%v", tc.Args)) + 8
	}
	// 附件同样要计入：不然一张图能被"压缩省下 0 token"，而它实际占了三千多。
	for _, att := range m.Attachments {
		n += imageTokens(att.Width, att.Height)
	}
	return n
}

// toolSchemaTokens 工具定义本身的占用。每次请求都要带上、且不随对话变化，
// 工具多的时候它是笔不小的固定开销，算用量时不能漏。
func toolSchemaTokens(tools []LLMTool) int {
	total := 0
	for _, t := range tools {
		total += estimateTokens(t.Function.Name) +
			estimateTokens(t.Function.Description) +
			estimateTokens(string(t.Function.Parameters)) + 12
	}
	return total
}

// contextWindowOf 取模型的上下文窗口；未配置时用保守默认值。
// 保守意味着可能压得偏早（浪费一点 token），但不会撑爆——反过来会直接报错。
func contextWindowOf(model *Model) int {
	if model != nil && model.ContextWindow > 0 {
		return model.ContextWindow
	}
	return contextDefaultWindow
}

// needCompact 用量是否已达到触发压缩的比例
func needCompact(used, window int) bool {
	if window <= 0 {
		return false
	}
	return used*100 >= window*contextCompactRatioNum
}

// ===== 切点与消息组装 =====

// findCompactCut 找压缩切点：返回 idx 表示 messages[0:idx] 可被摘要替换，-1 表示没有合法切点。
//
// 切点必须落在一条**非 tool 结果的 user 消息**之前。这样切点左侧要么以 assistant 收尾、
// 要么以一组完整的 tool 结果收尾，右侧从一条干净的 user 消息开始，序列始终合法。
//
// 注意它是从 len-keepRecent 往**前**找，所以找不到时只会保留更多而不是更少——
// 宁可少压一点，也不要把最近的一轮切掉。
func findCompactCut(msgs []Message, keepRecent int) int {
	if keepRecent < 1 {
		keepRecent = 1
	}
	if len(msgs) <= keepRecent {
		return -1
	}
	for i := len(msgs) - keepRecent; i > 0; i-- {
		if msgs[i].Role != RoleUser {
			continue
		}
		// tool 结果消息的 role 也是 user（OpenAI 协议里 tool 走独立 role，
		// 但历史数据里可能混入），所以额外排除带 ToolCallID 的与空内容的
		if msgs[i].ToolCallID != "" || strings.TrimSpace(msgs[i].Content) == "" {
			continue
		}
		return i
	}
	return -1
}

// withContextSummary 把被摘要覆盖的前缀换成一条说明消息。
//
// 用 user 角色而不是 system：部分协议（Anthropic）要求 system 只在最前，
// 在序列中间插 system 会被拒。user 角色各协议都接受。
func withContextSummary(history []Message, covered int, summary string) []Message {
	out := make([]Message, 0, len(history)-covered+1)
	out = append(out, Message{
		Role:    RoleUser,
		Content: "（以下是此前对话的摘要，用于节省上下文；完整原文仍在会话记录中）\n\n" + summary,
	})
	out = append(out, history[covered:]...)
	return out
}

// applyToolResultBudget 对单条工具结果施加预算：超限时保留头尾、中间省略。
//
// 只作用于**发给模型的内容**，持久化仍存全量——与摘要同一个原则：
// 省 token 不能以丢用户数据为代价。
func applyToolResultBudget(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role != RoleTool {
			continue
		}
		r := []rune(out[i].Content)
		if len(r) <= contextToolResultBudget {
			continue
		}
		omitted := len(r) - contextToolResultHead - contextToolResultTail
		out[i].Content = string(r[:contextToolResultHead]) +
			fmt.Sprintf("\n\n…（中间省略 %d 字符；需要完整内容请用 offset/limit 分段读取，或用 grep 精确定位）\n\n", omitted) +
			string(r[len(r)-contextToolResultTail:])
	}
	return out
}

// buildRunMessages 组装本次运行发给模型的消息序列。
//
// 顺序有讲究：先做摘要前缀替换（它改变消息条数），再施加工具结果预算（逐条改内容），
// 最后交给 buildLLMMessages 转成协议格式。
// forceTruncate 为 true 时走确定性截断——它是降级路径与调试开关，不是常规流程。
//
// loader 是附件读取口，一路上传给 buildLLMMessages（可为 nil，见 imageLoader 的说明）。
// 它必须是参数而不是去全局取：这一层要做的是"取哪几条消息"，取图的动作属于协议组装，
// 待到了最后一步才发生——中间那两步（摘要替换、结果预算）都不碰图片。
func buildRunMessages(session *Session, systemPrompt string, forceTruncate bool, loader imageLoader) []LLMMessage {
	if session == nil {
		return buildLLMMessages(nil, systemPrompt, loader)
	}
	history := session.Messages
	if session.ContextCoveredUpTo > 0 &&
		session.ContextCoveredUpTo <= len(history) &&
		strings.TrimSpace(session.ContextSummary) != "" {
		history = withContextSummary(history, session.ContextCoveredUpTo, session.ContextSummary)
	}
	if forceTruncate {
		history = compactMessages(history)
	}
	history = applyToolResultBudget(history)
	return buildLLMMessages(history, systemPrompt, loader)
}

// ===== 摘要生成 =====

// contextSummaryPrompt 摘要器提示词。要求结构化输出——这段摘要要独自承担
// "模型继续干活所需的全部背景"，写成一段散文会丢关键状态。
const contextSummaryPrompt = `你在为一次长对话做上下文压缩。把下面的对话压成一段结构化摘要，供后续继续工作使用。

必须保留：
1. 用户的目标，以及已确认的约束（特别是"不要做 X"这类否定约束）；
2. 已完成的工作与结论（只留结果，不要过程细节）；
3. 出现过的具体标识：文件路径、函数/变量名、命令、配置项、报错原文——这些不能概括改写；
4. 未完成的待办与下一步；
5. 已尝试并排除的方案及其原因。

不要保留：寒暄、重复的试探、已被推翻的中间推导。
只输出摘要正文，不要开场白。用简洁的中文，可用小标题与短列表。`

// buildCompactTranscript 把待压缩的消息拼成摘要器的输入。
// 有总长与单条长度上限：摘要请求本身也不能超长。
//
// 图片**不进**摘要素材（摘要请求只发文本），但必须留下痕迹：被摘要覆盖之后，
// 那张图不会再出现在后续请求里，摘要若不记一笔，模型就完全不知道"用户曾给过一张图"——
// 而用户后面完全可能问"刚才那张图里第三行是什么"。
func buildCompactTranscript(msgs []Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && len(m.Attachments) == 0 {
			continue
		}
		content := m.Content
		if r := []rune(content); len(r) > compactTranscriptMsgLimit {
			content = string(r[:compactTranscriptMsgLimit]) + "…（截断）"
		}
		sb.WriteString("[" + m.Role + "] " + content)
		if len(m.ToolCalls) > 0 {
			names := make([]string, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				names = append(names, tc.Name)
			}
			sb.WriteString("（调用了工具：" + strings.Join(names, ", ") + "）")
		}
		if len(m.Attachments) > 0 {
			sb.WriteString("（附带 " + describeAttachments(m.Attachments) + "）")
		}
		sb.WriteString("\n")
		if sb.Len() >= compactTranscriptLimit {
			sb.WriteString("…（更早的内容已省略）\n")
			break
		}
	}
	return sb.String()
}

// describeAttachments 附件清单的一行描述（摘要素材与工具结果文本共用）
func describeAttachments(atts []Attachment) string {
	names := make([]string, 0, len(atts))
	for _, a := range atts {
		name := a.Name
		if strings.TrimSpace(name) == "" {
			name = a.ID
		}
		if a.Width > 0 && a.Height > 0 {
			name = fmt.Sprintf("%s（%d×%d）", name, a.Width, a.Height)
		}
		names = append(names, name)
	}
	return fmt.Sprintf("%d 张图片：%s", len(atts), strings.Join(names, ", "))
}

// summarizeContext 把 msgs 压成一段摘要。失败由调用方降级处理。
func summarizeContext(ctx context.Context, model *Model, msgs []Message) (string, error) {
	if len(msgs) == 0 {
		return "", fmt.Errorf("没有可压缩的内容")
	}
	modelID := model.ModelID
	if modelID == "" {
		modelID = model.Name
	}
	req := &LLMReq{
		Model:       modelID,
		Temperature: 0.2,
		Messages: []LLMMessage{
			{Role: RoleSystem, Content: contextSummaryPrompt},
			{Role: RoleUser, Content: buildCompactTranscript(msgs)},
		},
	}
	resp, err := callLLMForModel(ctx, model, req)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("摘要器返回空响应")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// ===== 压缩编排 =====

// CompactOutcome 一次压缩的结果
type CompactOutcome struct {
	Compressed   bool   `json:"compressed"`
	CoveredMsgs  int    `json:"coveredMsgs"` // 摘要累计覆盖的消息条数
	SummaryChars int    `json:"summaryChars"`
	SavedTokens  int    `json:"savedTokens"` // 估算省下的输入 token
	Degraded     bool   `json:"degraded,omitempty"`
	Reason       string `json:"reason,omitempty"` // 未压缩的原因
}

// ContextStat 上下文用量（给界面展示与阈值判断）
type ContextStat struct {
	SessionID    string  `json:"sessionId"`
	UsedTokens   int     `json:"usedTokens"`
	WindowTokens int     `json:"windowTokens"`
	Ratio        float64 `json:"ratio"` // 0..1
	MessageCount int     `json:"messageCount"`
	CoveredMsgs  int     `json:"coveredMsgs"` // 摘要已覆盖的条数（0=未压缩）
	SummaryChars int     `json:"summaryChars"`
	SummaryAt    int64   `json:"summaryAt,omitempty"`
	// HasAnchor 本次统计是否建立在**真实用量锚点**上。
	// 锚点只在一次真实模型调用回传 usage 之后建立，所以界面查询与命令路径恒为 false，
	// 此时 UsedTokens 是纯字符估算，误差可以到两位数百分比——这点必须让用户看见。
	HasAnchor bool `json:"hasAnchor"`
	// SavedTokens 上一次摘要压缩省下的输入 token（估算）。
	// 由"被覆盖原文 - 摘要正文"现算，不额外持久化：原文一直在会话里，随时能重算，
	// 多存一个字段等于制造第二个真源（手工清掉摘要就会对不上）。
	SavedTokens int `json:"savedTokens"`
	// CalibRatio 本次统计套用的**估算校准系数**（真实 token / 字符估算，按模型观测而来）。
	// 无锚点时 UsedTokens 与 SavedTokens 都乘过它；有锚点时它只说明"若退回估算会乘多少"，
	// 因为锚点本身就是真值，再乘一次等于对真值做二次修正（见 contextStatOf）。
	CalibRatio float64 `json:"calibRatio"`
}

// compactSessionContext 按会话 ID 做一次摘要式压缩。
// ctx 用运行 ctx：压缩要额外发一次 LLM 请求，硬取消同样要能断掉它。
func (a *App) compactSessionContext(ctx context.Context, sessionID string) (*CompactOutcome, error) {
	session, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("加载会话失败: %w", err)
	}
	model, err := a.modelStore.GetModelForCall(session.Model)
	if err != nil {
		return nil, fmt.Errorf("获取模型配置失败: %w", err)
	}
	return a.compactSession(ctx, session, &model)
}

// compactSession 对已加载的会话做一次压缩。
//
// 只改存储层的三个字段，会话原文一字不动——压缩因此是可逆的。
func (a *App) compactSession(ctx context.Context, session *Session, model *Model) (*CompactOutcome, error) {
	base := session.ContextCoveredUpTo
	if base < 0 || base > len(session.Messages) {
		base = 0
	}
	pending := session.Messages[base:]
	if len(pending) < contextMinCompressMsgs {
		return &CompactOutcome{
			Compressed: false,
			Reason:     fmt.Sprintf("待压缩内容不足 %d 条，无需压缩", contextMinCompressMsgs),
		}, nil
	}

	cut := findCompactCut(pending, a.keepRecentMsgs())
	if cut <= 0 {
		return &CompactOutcome{
			Compressed: false,
			Reason:     "找不到合法切点（切点只能落在 user 消息之前）",
		}, nil
	}

	prefix := pending[:cut]
	beforeTokens := 0
	for _, m := range prefix {
		beforeTokens += storedMessageTokens(m)
	}

	// 已有摘要必须先并入这次的输入。
	//
	// 新的摘要会**整体替换**旧摘要，而它声明的覆盖范围是 Messages[0 : base+cut]；
	// 如果只把 pending 喂给摘要器，上一段摘要覆盖过的内容既不在新摘要里、
	// 又因为 buildRunMessages 的前缀替换而不再发给模型——静默丢信息。
	// 加进来之后，不变式「ContextSummary 覆盖 Messages[0:ContextCoveredUpTo]」才成立。
	summarizeInput := prefix
	if base > 0 && strings.TrimSpace(session.ContextSummary) != "" {
		prior := Message{
			Role:    RoleUser,
			Content: "（以下是更早一轮已经压缩好的摘要，请把它一并纳入这次的新摘要）\n\n" + session.ContextSummary,
		}
		summarizeInput = append([]Message{prior}, prefix...)
		beforeTokens += storedMessageTokens(prior)
	}

	summary, err := summarizeContext(ctx, model, summarizeInput)
	if err != nil || strings.TrimSpace(summary) == "" {
		// 降级：不落摘要，让调用方退回确定性截断。这一轮必须还能发出去。
		reason := "摘要生成失败，退回确定性截断"
		if err != nil {
			reason = "摘要生成失败（" + err.Error() + "），退回确定性截断"
		}
		return &CompactOutcome{Compressed: false, Degraded: true, Reason: reason}, nil
	}
	if r := []rune(summary); len(r) > contextSummaryMaxChars {
		summary = string(r[:contextSummaryMaxChars]) + "…"
	}

	covered := base + cut
	if err := a.sessionStore.SetContextSummary(session.ID, summary, covered, time.Now().UnixMilli()); err != nil {
		return nil, fmt.Errorf("保存摘要失败: %w", err)
	}

	// 口径必须与 summarySavedTokens 一致（两侧各自缩放后相减），否则 /compact 当场报出的
	// 数字与之后 /context-stat 读到的会对不上。
	calib := a.calibFor(session.Model)
	saved := scaleTokens(beforeTokens, calib) - scaleTokens(estimateTokens(summary), calib)
	if saved < 0 {
		saved = 0
	}
	return &CompactOutcome{
		Compressed:   true,
		CoveredMsgs:  covered,
		SummaryChars: len([]rune(summary)),
		SavedTokens:  saved,
	}, nil
}

// keepRecentMsgs 压缩时要保留的最近消息条数（读可配置项）。
//
// 存储未初始化时（测试里直接构造 App，或 startup 尚未跑到）回落到内置默认值——
// 压缩策略不能因为一个 nil 指针就变成"留 0 条"，那会把整段对话压没。
func (a *App) keepRecentMsgs() int {
	if a == nil || a.contextPrefs == nil {
		return contextKeepRecentMsgs
	}
	return a.contextPrefs.Get().KeepRecentMsgs
}

// summarySavedTokens 估算"上次压缩一共省下多少输入 token"。
//
// 用"被摘要覆盖的原文 - 摘要正文"现算，不额外落盘：原文一直在会话里，随时能重算，
// 多存一个字段反而会制造第二个真源（手工清空摘要之后两边就对不上了）。
// 口径与 compactSession 里那一次 saved 的计算保持一致。
func summarySavedTokens(session *Session, calib float64) int {
	if session == nil || session.ContextCoveredUpTo <= 0 || strings.TrimSpace(session.ContextSummary) == "" {
		return 0
	}
	covered := session.ContextCoveredUpTo
	if covered > len(session.Messages) {
		covered = len(session.Messages)
	}
	before := 0
	for _, m := range session.Messages[:covered] {
		before += storedMessageTokens(m)
	}
	// 两侧按同一个系数缩放。同一个系数不会互相抵消——差值被整体放大/缩小，而这正是
	// 想要的：中文会话真比字符估算贵，省下的量也该按真实比例算。
	// （摘要与原文的字种构成不同，套同一个系数是一处已知近似。）
	saved := scaleTokens(before, calib) - scaleTokens(estimateTokens(session.ContextSummary), calib)
	if saved < 0 {
		saved = 0
	}
	return saved
}

// FormatContextStatReport 把用量统计渲染成 /context-stat 的文本报告。
//
// 单独成函数（而不是写在 App 方法里）是为了让测试能直接盯住它：
// 这份报告的每一行都在解释"数字为什么变了"，说错就是误导。
func FormatContextStatReport(st *ContextStat, keepRecent int) string {
	if st == nil {
		return "上下文统计不可用（会话不存在）。"
	}
	pct := 0
	if st.WindowTokens > 0 {
		pct = st.UsedTokens * 100 / st.WindowTokens
	}
	threshold := st.WindowTokens * contextCompactRatioNum / 100
	reachLine := "未达"
	if st.WindowTokens > 0 && st.UsedTokens*100 >= st.WindowTokens*contextCompactRatioNum {
		reachLine = "已达（下一轮满足切点条件时会自动压缩）"
	}

	var b strings.Builder
	b.WriteString("上下文统计\n")
	fmt.Fprintf(&b, "用量：%d / %d token（%d%%）\n", st.UsedTokens, st.WindowTokens, pct)
	fmt.Fprintf(&b, "消息：%d 条\n", st.MessageCount)
	if st.CoveredMsgs > 0 {
		fmt.Fprintf(&b, "摘要：已覆盖前 %d 条消息（%d 字）", st.CoveredMsgs, st.SummaryChars)
		if st.SummaryAt > 0 {
			fmt.Fprintf(&b, "，生成于 %s", time.UnixMilli(st.SummaryAt).Format("2006-01-02 15:04:05"))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "省下：约 %d 个输入 token（原文仍在会话记录中，清空摘要即回到全量）\n", st.SavedTokens)
	} else {
		b.WriteString("摘要：未压缩，发给模型的是全量原文\n")
	}
	if st.HasAnchor {
		b.WriteString("锚点：有（以模型回传的真实 usage 为基准，误差只累积在两次锚点之间）\n")
	} else {
		b.WriteString("锚点：无（纯字符估算；锚点只在真实模型调用回传 usage 后建立）\n")
	}
	if st.CalibRatio > 0 && math.Abs(st.CalibRatio-1) > 0.005 {
		fmt.Fprintf(&b, "估算系数：×%.2f（按本模型的历史真实用量自校准，仅无锚点时的估算套用）\n", st.CalibRatio)
	}
	fmt.Fprintf(&b, "压缩触发线：%d%%（≈%d token）→ 当前%s\n", contextCompactRatioNum, threshold, reachLine)
	fmt.Fprintf(&b, "保留最近原文：%d 条（可在设置→通用 调整）\n", keepRecent)
	return b.String()
}

// contextStatOf 计算某会话当前的上下文用量。
// messages 与 anchor 由调用方给出（循环里是真实序列，界面查询时是近似序列）。
func contextStatOf(session *Session, messages []LLMMessage, window int, anchor *tokenAnchor, extra int, calib float64) *ContextStat {
	used := estimateSeqTokens(messages, anchor) + extra
	// 只在**无锚点**时套校准系数：有锚点时上式里的锚点部分已经是模型回传的真值，
	// 再乘一次等于对真值做二次修正，只会把它推歪。
	if anchor == nil {
		used = scaleTokens(used, calib)
	}
	calib = normalizeCalib(calib)
	st := &ContextStat{
		UsedTokens:   used,
		WindowTokens: window,
		HasAnchor:    anchor != nil && anchor.InputTokens > 0,
		CalibRatio:   calib,
		MessageCount: len(messages),
	}
	if session != nil {
		st.SessionID = session.ID
		st.CoveredMsgs = session.ContextCoveredUpTo
		st.SummaryChars = len([]rune(session.ContextSummary))
		st.SummaryAt = session.ContextSummaryAt
		st.SavedTokens = summarySavedTokens(session, calib)
	}
	if window > 0 {
		st.Ratio = float64(used) / float64(window)
	}
	return st
}

// isContextOverflowError 判断错误是否属于"上下文超长"。
//
// 各家措辞不一，只能做关键字匹配；刻意不用裸 "exceed"——那会把
// "rate limit exceeded" 之类也卷进来，触发一次毫无意义的压缩。
func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, k := range []string{
		"context length", "context_length", "context window", "maximum context",
		"too many tokens", "token limit", "input is too long", "reduce the length",
		"prompt is too long", "上下文超", "请求过长", "内容过长",
	} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}
