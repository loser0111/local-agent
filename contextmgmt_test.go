package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ===== 上下文管理（P1-B）=====
//
// 这些测试守三件事：估算的准确性来源（锚点）、压缩切点的合法性（不能切开
// tool_calls 与 tool 结果）、以及"压缩失败不能阻断这一轮"这条底线。

// 字符估算的量级：中日韩字符明显比同长度的 ASCII 贵
func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("空串应为 0，实际 %d", got)
	}
	ascii := estimateTokens(strings.Repeat("a", 400))
	cjk := estimateTokens(strings.Repeat("字", 400))
	if ascii != 100 {
		t.Fatalf("400 个 ASCII 字符应约 100 token，实际 %d", ascii)
	}
	if cjk != 400 {
		t.Fatalf("400 个汉字应约 400 token，实际 %d", cjk)
	}
	if cjk <= ascii {
		t.Fatal("同等长度的中文应比英文贵——系数写反了")
	}
}

// 锚点生效时，估算 = 锚点 + 其后增量（误差不累积）
func TestEstimateSeqTokensWithAnchor(t *testing.T) {
	msgs := []LLMMessage{
		{Role: RoleSystem, Content: "system"},
		{Role: RoleUser, Content: "你好"},
	}
	// 没有锚点：整体估算
	whole := estimateSeqTokens(msgs, nil)
	if whole <= 0 {
		t.Fatalf("整体估算应大于 0，实际 %d", whole)
	}

	// 有锚点：锚点值 + 其后新增消息的增量
	anchor := &tokenAnchor{InputTokens: 1000, MsgCount: 1}
	after := estimateSeqTokens(msgs, anchor)
	if after <= 1000 {
		t.Fatalf("应等于 1000 加上第二条消息的估算，实际 %d", after)
	}
	if after == whole {
		t.Fatal("有锚点与无锚点不该给出同一个结果")
	}

	// 锚点计数超出当前序列（压缩后序列变短）：退回整体估算，不能算出负数
	stale := &tokenAnchor{InputTokens: 9999, MsgCount: 99}
	if got := estimateSeqTokens(msgs, stale); got != whole {
		t.Fatalf("失效锚点应退回整体估算，实际 %d（期望 %d）", got, whole)
	}
}

// 切点必须落在非 tool 结果的 user 消息之前，且不能切开 assistant(tool_calls) 与其 tool 结果
func TestFindCompactCut(t *testing.T) {
	build := func() []Message {
		return []Message{
			{ID: "m0", Role: RoleUser, Content: "任务"},
			{ID: "m1", Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "exec_shell"}}},
			{ID: "m2", Role: RoleTool, Content: "结果1", ToolCallID: "c1"},
			{ID: "m3", Role: RoleAssistant, Content: "做完了第一步"},
			{ID: "m4", Role: RoleUser, Content: "继续"},
			{ID: "m5", Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{ID: "c2", Name: "read_file"}}},
			{ID: "m6", Role: RoleTool, Content: "结果2", ToolCallID: "c2"},
			{ID: "m7", Role: RoleAssistant, Content: "读完了"},
			{ID: "m8", Role: RoleUser, Content: "再看一下"},
			{ID: "m9", Role: RoleAssistant, Content: "好的"},
			{ID: "m10", Role: RoleUser, Content: "还有吗"},
			{ID: "m11", Role: RoleAssistant, Content: "有"},
			{ID: "m12", Role: RoleUser, Content: "最后一个问题"},
			{ID: "m13", Role: RoleAssistant, Content: "答"},
		}
	}

	msgs := build()
	cut := findCompactCut(msgs, 6)
	if cut <= 0 {
		t.Fatalf("应找到一个合法切点，实际 %d", cut)
	}
	if msgs[cut].Role != RoleUser {
		t.Fatalf("切点应落在 user 消息之前，实际在第 %d 条（role=%s）", cut, msgs[cut].Role)
	}
	if msgs[cut].ToolCallID != "" {
		t.Fatal("切点不能落在 tool 结果消息之前")
	}
	// 左侧必须自洽：不能以"带 tool_calls 的 assistant"收尾（那样它的 tool 结果会被切走）
	prev := msgs[cut-1]
	if len(prev.ToolCalls) > 0 {
		t.Fatalf("切点左侧不该是带 tool_calls 的 assistant（第 %d 条）", cut-1)
	}

	// 消息太少：不切
	if got := findCompactCut(msgs, len(msgs)); got != -1 {
		t.Fatalf("消息数不超过保留条数时应返回 -1，实际 %d", got)
	}
	if got := findCompactCut(msgs, len(msgs)+5); got != -1 {
		t.Fatalf("保留数超过总条数时应返回 -1，实际 %d", got)
	}
}

// 找不到合法切点时返回 -1（整段都是 tool 结果，硬切会产生非法序列）
func TestFindCompactCutNoLegalPoint(t *testing.T) {
	msgs := []Message{
		{ID: "m0", Role: RoleUser, Content: "任务"},
		{ID: "m1", Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "t"}}},
	}
	// 后面全是 tool 结果，没有可切的 user 消息（除 index 0，但它不构成"非空切点"）
	for i := 0; i < 12; i++ {
		msgs = append(msgs, Message{ID: "t", Role: RoleTool, Content: "r", ToolCallID: "c1"})
	}
	if got := findCompactCut(msgs, 4); got != -1 {
		t.Fatalf("没有合法的 user 切点时应返回 -1，实际 %d", got)
	}
}

// 工具结果预算：超限保留头尾、短的原样；且**不改动传入的切片**（持久化存全量）
func TestApplyToolResultBudget(t *testing.T) {
	long := strings.Repeat("x", contextToolResultBudget+5000)
	msgs := []Message{
		{ID: "a", Role: RoleTool, Content: long},
		{ID: "b", Role: RoleTool, Content: "短的"},
		{ID: "c", Role: RoleUser, Content: long},
	}
	out := applyToolResultBudget(msgs)

	got := []rune(out[0].Content)
	if len(got) >= len([]rune(long)) {
		t.Fatal("超限的工具结果应被截断")
	}
	if !strings.Contains(out[0].Content, "中间省略") {
		t.Fatal("截断处应有明确说明，否则模型以为它看到的就是全部")
	}
	if !strings.HasPrefix(out[0].Content, "xxx") || !strings.HasSuffix(out[0].Content, "xxx") {
		t.Fatal("应保留头尾")
	}
	if out[1].Content != "短的" {
		t.Fatal("未超限的工具结果不应被改动")
	}
	if out[2].Content != long {
		t.Fatal("非工具消息不应被改动")
	}
	// 关键：原切片必须原样——持久化存全量，只有发给模型的那份被裁剪
	if msgs[0].Content != long {
		t.Fatal("预算只该作用于副本，不能改动传入的消息（持久化要存全量）")
	}
}

// 有摘要时：前缀被换成一条摘要说明，其余原样
func TestBuildRunMessagesWithSummary(t *testing.T) {
	session := &Session{
		Messages: []Message{
			{ID: "m0", Role: RoleUser, Content: "第一轮问题"},
			{ID: "m1", Role: RoleAssistant, Content: "第一轮回答"},
			{ID: "m2", Role: RoleUser, Content: "第二轮问题"},
			{ID: "m3", Role: RoleAssistant, Content: "第二轮回答"},
		},
		ContextSummary:     "之前聊了两轮，结论是 X。",
		ContextCoveredUpTo: 2,
	}
	out := buildRunMessages(session, "SYS", false)

	// system + 摘要说明 + 后 2 条
	if len(out) != 4 {
		t.Fatalf("应为 system + 摘要 + 2 条，实际 %d 条", len(out))
	}
	if out[0].Role != RoleSystem || out[0].Content != "SYS" {
		t.Fatalf("第一条应为 system，实际 %+v", out[0])
	}
	if !strings.Contains(out[1].Content, "之前聊了两轮") {
		t.Fatalf("第二条应为摘要说明，实际 %q", out[1].Content)
	}
	if strings.Contains(out[1].Content, "第一轮问题") {
		t.Fatal("被摘要覆盖的原文不该再发给模型")
	}
	if out[2].Content != "第二轮问题" || out[3].Content != "第二轮回答" {
		t.Fatalf("摘要之后的消息应原样保留，实际 %q / %q", out[2].Content, out[3].Content)
	}
	// 会话原文不受影响：压缩是可逆的
	if len(session.Messages) != 4 {
		t.Fatal("buildRunMessages 不该改动会话原文")
	}
}

// 没有摘要时：全部消息都发出去
func TestBuildRunMessagesWithoutSummary(t *testing.T) {
	session := &Session{Messages: []Message{
		{ID: "m0", Role: RoleUser, Content: "问题"},
		{ID: "m1", Role: RoleAssistant, Content: "回答"},
	}}
	out := buildRunMessages(session, "SYS", false)
	if len(out) != 3 {
		t.Fatalf("应为 system + 2 条，实际 %d", len(out))
	}
	if out[1].Content != "问题" {
		t.Fatalf("无摘要时应原样发送，实际 %q", out[1].Content)
	}
}

// 清空摘要字段即回到全量上下文（压缩可逆的落点）
func TestContextSummaryIsReversible(t *testing.T) {
	store := NewSessionStore(t.TempDir())
	sess, err := store.CreateSession(SessionConfig{Title: "t", Model: "m", Project: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []Message{
		{Role: RoleUser, Content: "问题一"},
		{Role: RoleAssistant, Content: "回答一"},
		{Role: RoleUser, Content: "问题二"},
		{Role: RoleAssistant, Content: "回答二"},
	} {
		if _, err := store.AppendMessage(sess.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetContextSummary(sess.ID, "摘要正文", 2, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	got, _ := store.GetSession(sess.ID)
	compressed := buildRunMessages(got, "SYS", false)
	if !strings.Contains(compressed[1].Content, "摘要正文") {
		t.Fatalf("应发送摘要，实际 %q", compressed[1].Content)
	}

	// 清空 → 回到全量
	if err := store.ClearContextSummary(sess.ID); err != nil {
		t.Fatal(err)
	}
	got2, _ := store.GetSession(sess.ID)
	restored := buildRunMessages(got2, "SYS", false)
	if !strings.Contains(restored[1].Content, "问题一") {
		t.Fatalf("清空摘要后应回到全量上下文，实际 %q", restored[1].Content)
	}
	if got2.ContextSummary != "" || got2.ContextCoveredUpTo != 0 {
		t.Fatal("清空后三个字段都应归零")
	}
}

// 用量统计
func TestContextStatOf(t *testing.T) {
	session := &Session{
		ID:                 "s1",
		Messages:           []Message{{Role: RoleUser, Content: strings.Repeat("字", 200)}},
		ContextCoveredUpTo: 3,
		ContextSummary:     "摘要",
	}
	msgs := buildRunMessages(session, "", false)
	st := contextStatOf(session, msgs, 1000, nil, 0, 1.0)
	if st.SessionID != "s1" || st.WindowTokens != 1000 {
		t.Fatalf("基础字段错误: %+v", st)
	}
	if st.UsedTokens <= 0 || st.Ratio <= 0 {
		t.Fatalf("用量应大于 0: %+v", st)
	}
	if st.CoveredMsgs != 3 || st.SummaryChars == 0 {
		t.Fatalf("应带上压缩状态: %+v", st)
	}
	// 窗口未知时不除零
	zero := contextStatOf(session, msgs, 0, nil, 0, 1.0)
	if zero.Ratio != 0 {
		t.Fatalf("窗口为 0 时比例应为 0，实际 %v", zero.Ratio)
	}
}

// 阈值判定
func TestNeedCompact(t *testing.T) {
	if needCompact(100, 0) {
		t.Fatal("窗口未知时不应触发压缩")
	}
	if needCompact(690, 1000) {
		t.Fatal("69% 不该触发（阈值 70%）")
	}
	if !needCompact(700, 1000) {
		t.Fatal("70% 应触发")
	}
	if !needCompact(1500, 1000) {
		t.Fatal("超过窗口更应触发")
	}
}

// 上下文超长的识别：要认得出，也不能把限流误判成超长
func TestIsContextOverflowError(t *testing.T) {
	yes := []string{
		"LLM 返回错误 (status=400): {\"error\":{\"message\":\"This model's maximum context length is 8192 tokens\"}}",
		"context_length_exceeded",
		"input is too long for requested model",
		"请求过长，请缩短",
	}
	for _, s := range yes {
		if !isContextOverflowError(errString(s)) {
			t.Fatalf("应识别为上下文超长: %q", s)
		}
	}
	no := []string{
		"rate limit exceeded",
		"401 unauthorized",
		"connection refused",
		"",
	}
	for _, s := range no {
		if isContextOverflowError(errString(s)) {
			t.Fatalf("不该识别为上下文超长: %q", s)
		}
	}
	if isContextOverflowError(nil) {
		t.Fatal("nil 不应算超长")
	}
}

// errString 把字符串包成 error
type errString string

func (e errString) Error() string { return string(e) }

// Anthropic 流式的用量要拼起来：input_tokens 在 message_start，output_tokens 在 message_delta。
// 整个"锚点"机制都依赖这两个值被正确取到——之前这里漏了字段，编译期才发现。
func TestAnthropicStreamCapturesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("anthropic-version") == "" {
			t.Error("缺少 anthropic-version 头")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			fmt.Fprint(w, s)
			fl.Flush()
		}
		write(`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":1234,"output_tokens":1}}}` + "\n\n")
		write(`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}` + "\n\n")
		write(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好"}}` + "\n\n")
		write(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":56}}` + "\n\n")
		write(`data: {"type":"message_stop"}` + "\n\n")
	}))
	defer srv.Close()

	resp, err := callAnthropicStream(context.Background(), srv.URL, "tok", &LLMReq{Model: "m"}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "你好" {
		t.Fatalf("文本聚合错误: %+v", resp.Choices)
	}
	if resp.Usage.PromptTokens != 1234 {
		t.Fatalf("input_tokens 应从 message_start 取到，实际 %d", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 56 {
		t.Fatalf("output_tokens 应从 message_delta 取到，实际 %d", resp.Usage.CompletionTokens)
	}
}

// ===== 压缩编排（用 mock LLM）=====

// 摘要器不可用时必须降级：Compressed=false、Degraded=true，且**不写**摘要字段
func TestCompactSessionDegradesWhenSummarizerFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 一律返回错误，模拟摘要器不可用
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.baseDir = t.TempDir()
	seedMessages(t, app, sess.ID, 16)

	out, err := app.compactSessionContext(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("摘要失败不该让压缩报错（要能降级）: %v", err)
	}
	if out.Compressed {
		t.Fatal("摘要不可用时不该声称压缩成功")
	}
	if !out.Degraded {
		t.Fatalf("应标记为已降级，实际 %+v", out)
	}
	got, _ := app.sessionStore.GetSession(sess.ID)
	if got.ContextSummary != "" || got.ContextCoveredUpTo != 0 {
		t.Fatal("降级时不该写入摘要字段")
	}
}

// 正常路径：摘要落库、覆盖条数正确、省下的 token 为正
func TestCompactSessionWritesSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LLMResp{Choices: []LLMChoice{{
			FinishReason: FinishReasonStop,
			Message:      LLMMessage{Role: RoleAssistant, Content: "## 已完成\n- 做了 A\n\n## 待办\n- 做 B"},
		}}})
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.baseDir = t.TempDir()
	seedMessages(t, app, sess.ID, 16)

	out, err := app.compactSessionContext(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Compressed {
		t.Fatalf("应压缩成功，实际 %+v", out)
	}
	if out.CoveredMsgs <= 0 || out.SummaryChars <= 0 {
		t.Fatalf("覆盖条数与摘要长度应有效: %+v", out)
	}
	if out.SavedTokens <= 0 {
		t.Fatalf("省下的 token 应为正: %+v", out)
	}

	got, _ := app.sessionStore.GetSession(sess.ID)
	if !strings.Contains(got.ContextSummary, "待办") {
		t.Fatalf("摘要应落库，实际 %q", got.ContextSummary)
	}
	if got.ContextCoveredUpTo != out.CoveredMsgs {
		t.Fatalf("覆盖条数应一致: %d != %d", got.ContextCoveredUpTo, out.CoveredMsgs)
	}
	// 原文必须完整保留
	if len(got.Messages) != 16 {
		t.Fatalf("压缩不该改动会话原文，实际 %d 条", len(got.Messages))
	}
}

// 连续两次压缩：第二次的摘要器输入必须带上第一次的摘要。
//
// 否则会静默丢信息——新摘要整体替换旧摘要，但它声明的覆盖范围（Messages[0:covered]）
// 比它实际读到的内容更大，于是上一段摘要覆盖过的部分既不在新摘要里、又因为前缀替换
// 而不再发给模型。这是"摘要器只收到 pending"那个写法的直接后果。
func TestSecondCompactCarriesPreviousSummary(t *testing.T) {
	var (
		mu           sync.Mutex
		summaryCalls int
		secondInput  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req LLMReq
		_ = json.Unmarshal(body, &req)
		if len(req.Tools) > 0 {
			t.Error("本测试不该发起带工具的请求")
			return
		}
		mu.Lock()
		summaryCalls++
		n := summaryCalls
		if n == 2 {
			secondInput = string(body)
		}
		mu.Unlock()

		text := "第一段摘要标记-ALPHA"
		if n >= 2 {
			text = "第二段摘要标记-BETA"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LLMResp{Choices: []LLMChoice{{
			FinishReason: FinishReasonStop,
			Message:      LLMMessage{Role: RoleAssistant, Content: text},
		}}})
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.baseDir = t.TempDir()
	seedMessages(t, app, sess.ID, 16)

	first, err := app.compactSessionContext(context.Background(), sess.ID)
	if err != nil || !first.Compressed {
		t.Fatalf("第一次压缩应成功: %+v err=%v", first, err)
	}
	if first.CoveredMsgs <= 0 {
		t.Fatalf("第一次应覆盖若干条消息: %+v", first)
	}

	// 再造出足够多的待压缩内容（角色与 seedMessages 保持同一奇偶约定，切点才找得到）
	for i := 0; i < 20; i++ {
		role := RoleUser
		if i%2 == 1 {
			role = RoleAssistant
		}
		if _, err := app.sessionStore.AppendMessage(sess.ID,
			Message{Role: role, Content: strings.Repeat("后续内容", 20)}); err != nil {
			t.Fatal(err)
		}
	}

	second, err := app.compactSessionContext(context.Background(), sess.ID)
	if err != nil || !second.Compressed {
		t.Fatalf("第二次压缩应成功: %+v err=%v", second, err)
	}
	if second.CoveredMsgs <= first.CoveredMsgs {
		t.Fatalf("第二次的覆盖范围应扩大: %d -> %d", first.CoveredMsgs, second.CoveredMsgs)
	}

	mu.Lock()
	got := secondInput
	mu.Unlock()
	if !strings.Contains(got, "ALPHA") {
		t.Fatalf("第二次摘要的输入必须包含上一段摘要，否则那段内容会静默丢失；实际输入=%s", got)
	}

	// 模型看到的应是新摘要，且原文依然完整保留
	reloaded, _ := app.sessionStore.GetSession(sess.ID)
	visible := buildRunMessages(reloaded, "SYS", false)
	if !strings.Contains(visible[1].Content, "BETA") {
		t.Fatalf("应发送新摘要，实际 %q", visible[1].Content)
	}
	if len(reloaded.Messages) != 36 {
		t.Fatalf("压缩不该改动会话原文，实际 %d 条", len(reloaded.Messages))
	}
}

// 消息太少时不压（不值得多一次调用，也容易把上下文压没）
func TestCompactSessionSkipsWhenTooFewMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("消息太少时不该发起摘要请求")
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.baseDir = t.TempDir()
	seedMessages(t, app, sess.ID, 4)

	out, err := app.compactSessionContext(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Compressed {
		t.Fatal("消息太少不该压缩")
	}
	if out.Reason == "" {
		t.Fatal("应说明未压缩的原因")
	}
}

// 端到端：首轮请求被 API 判为上下文超长 → 压缩后重试 → 本轮正常完成
func TestContextOverflowRetryRecovers(t *testing.T) {
	var toolCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req LLMReq
		_ = json.Unmarshal(body, &req)

		if len(req.Tools) == 0 {
			// 摘要请求
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(LLMResp{Choices: []LLMChoice{{
				FinishReason: FinishReasonStop,
				Message:      LLMMessage{Role: RoleAssistant, Content: "压缩后的摘要"},
			}}})
			return
		}
		if atomic.AddInt32(&toolCalls, 1) == 1 {
			// 第一次带工具的请求：假装上下文超长
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"This model's maximum context length is 8192 tokens"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LLMResp{Choices: []LLMChoice{{
			FinishReason: FinishReasonStop,
			Message:      LLMMessage{Role: RoleAssistant, Content: "重试后正常回复"},
		}}})
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &runRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = NewFileChangeLog(50)
	seedMessages(t, app, sess.ID, 16)
	if _, err := app.sessionStore.AppendMessage(sess.ID, Message{Role: RoleUser, Content: "继续"}); err != nil {
		t.Fatal(err)
	}

	res := app.executeChat(sess.ID, "继续", false)
	if res.Error != "" {
		t.Fatalf("超限后应能压缩重试并成功，实际报错: %s", res.Error)
	}
	if res.Reply != "重试后正常回复" {
		t.Fatalf("应拿到重试后的回复，实际 %q", res.Reply)
	}
	if atomic.LoadInt32(&toolCalls) != 2 {
		t.Fatalf("工具请求应为 2 次（首次超限 + 重试），实际 %d", toolCalls)
	}
	got, _ := app.sessionStore.GetSession(sess.ID)
	if got.ContextCoveredUpTo <= 0 {
		t.Fatal("压缩结果应已落库")
	}
}

// 端到端：摘要器坏掉时靠确定性截断兜住，本轮仍然能完成
func TestContextOverflowFallsBackToTruncation(t *testing.T) {
	var toolCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req LLMReq
		_ = json.Unmarshal(body, &req)

		if len(req.Tools) == 0 {
			// 摘要器坏掉
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"summarizer down"}`))
			return
		}
		if atomic.AddInt32(&toolCalls, 1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"context length exceeded"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LLMResp{Choices: []LLMChoice{{
			FinishReason: FinishReasonStop,
			Message:      LLMMessage{Role: RoleAssistant, Content: "截断后也能回复"},
		}}})
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &runRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = NewFileChangeLog(50)
	seedMessages(t, app, sess.ID, 16)
	if _, err := app.sessionStore.AppendMessage(sess.ID, Message{Role: RoleUser, Content: "继续"}); err != nil {
		t.Fatal(err)
	}

	res := app.executeChat(sess.ID, "继续", false)
	if res.Error != "" {
		t.Fatalf("摘要不可用时也该靠截断重试成功，实际报错: %s", res.Error)
	}
	if res.Reply != "截断后也能回复" {
		t.Fatalf("应拿到重试后的回复，实际 %q", res.Reply)
	}
}

// ===== /compact 命令 =====

// /compact 走命令路径：压缩 + 落一条说明性回复（不是一次普通对话）
func TestCompactCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LLMResp{Choices: []LLMChoice{{
			FinishReason: FinishReasonStop,
			Message:      LLMMessage{Role: RoleAssistant, Content: "摘要内容"},
		}}})
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &runRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = NewFileChangeLog(50)
	seedMessages(t, app, sess.ID, 16)

	res, err := app.Chat(sess.ID, "/compact", false, false)
	if err != nil {
		t.Fatalf("/compact 不该报错: %v", err)
	}
	if !strings.Contains(res.Reply, "已压缩上下文") {
		t.Fatalf("应给出压缩结果说明，实际 %q", res.Reply)
	}
	if res.Context == nil {
		t.Fatal("应带回上下文用量")
	}
	if len(res.Messages) != 1 || res.Messages[0].Role != RoleAssistant {
		t.Fatalf("应落一条 assistant 说明消息，实际 %+v", res.Messages)
	}
}

// 消息太少时 /compact 给出原因而不是报错
func TestCompactCommandWithoutEnoughMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("不该发起摘要请求")
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &runRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = NewFileChangeLog(50)
	seedMessages(t, app, sess.ID, 2)

	res, err := app.Chat(sess.ID, "/compact", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Reply, "未压缩") {
		t.Fatalf("应说明未压缩的原因，实际 %q", res.Reply)
	}
}

// seedMessages 往会话里塞 n 条交替的 user/assistant 消息（偶数下标是 user，
// 这样 findCompactCut 能找到合法切点）
func seedMessages(t *testing.T, app *App, sessionID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		role := RoleUser
		if i%2 == 1 {
			role = RoleAssistant
		}
		content := strings.Repeat("内容", 20)
		if _, err := app.sessionStore.AppendMessage(sessionID, Message{Role: role, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
}
