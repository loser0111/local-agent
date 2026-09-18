package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ===== 实际发出的请求快照（排障视图）=====

// 摘要消息的位置是固定约定：system 恒为第 0 条，摘要插在其后
func TestSummaryMessageIndex(t *testing.T) {
	msgs := []LLMMessage{
		{Role: RoleSystem, Content: "SYS"},
		{Role: RoleUser, Content: "摘要说明"},
		{Role: RoleUser, Content: "原文"},
	}
	withSummary := &Session{ContextSummary: "摘要", ContextCoveredUpTo: 5}
	if got := summaryMessageIndex(withSummary, msgs); got != 1 {
		t.Fatalf("有摘要时应指向第 1 条，实际 %d", got)
	}

	noSummary := &Session{}
	if got := summaryMessageIndex(noSummary, msgs); got != -1 {
		t.Fatalf("无摘要时应为 -1，实际 %d", got)
	}
	// 覆盖条数为 0 但摘要字段非空（不该出现的状态）：同样不标
	half := &Session{ContextSummary: "摘要", ContextCoveredUpTo: 0}
	if got := summaryMessageIndex(half, msgs); got != -1 {
		t.Fatalf("覆盖条数为 0 时不应标记，实际 %d", got)
	}
	// 序列太短（只有 system）
	if got := summaryMessageIndex(withSummary, msgs[:1]); got != -1 {
		t.Fatalf("序列过短时应为 -1，实际 %d", got)
	}
	if got := summaryMessageIndex(nil, msgs); got != -1 {
		t.Fatalf("session 为 nil 时应为 -1，实际 %d", got)
	}
}

// 快照必须与调用方的切片解耦。
//
// 这是这类"记录现场"功能的经典坑：循环随后还会 append / 重建 messages，
// 若快照共享底层数组，事后看到的就不是当时发出去的那一份了。
func TestSnapshotIsDecoupledFromCallerSlice(t *testing.T) {
	msgs := []LLMMessage{
		{Role: RoleSystem, Content: "SYS"},
		{Role: RoleUser, Content: "原始内容"},
	}
	snap := snapshotLLMRequest(nil, nil, 0, "m", "SYS", msgs, nil, nil, false)

	// 模拟循环后续的动作：改元素、append（可能复用底层数组）
	msgs[1].Content = "被后续改写了"
	msgs = append(msgs, LLMMessage{Role: RoleAssistant, Content: "新增的一轮"})

	if snap.Messages[1].Content != "原始内容" {
		t.Fatalf("快照应保留当时的内容，实际 %q", snap.Messages[1].Content)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("快照长度不应随后续 append 变化，实际 %d", len(snap.Messages))
	}
}

// 记录与读取对 nil 接收者安全（测试里会直接构造零值 App）
func TestRequestLogNilSafety(t *testing.T) {
	var log *llmRequestLog
	log.record(&LLMRequestSnapshot{SessionID: "s1"}) // 不该 panic
	if got := log.get("s1"); got != nil {
		t.Fatalf("nil 日志应返回 nil，实际 %+v", got)
	}
	log.forget("s1")

	// 零值 App（没有 reqLog）也要能查，不能 panic
	app := &App{}
	snap, err := app.GetLastLLMRequest("s1")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if snap != nil {
		t.Fatalf("没有记录时应返回 nil，实际 %+v", snap)
	}
	// 空会话 ID 明确报错，而不是返回 nil 让人以为"没记录"
	if _, err := app.GetLastLLMRequest(""); err == nil {
		t.Fatal("空会话 ID 应报错")
	}
}

// 会话之间互不串台；删除会话会清掉记录
func TestRequestLogSessionIsolation(t *testing.T) {
	log := &llmRequestLog{}
	log.record(&LLMRequestSnapshot{SessionID: "a", Model: "ma"})
	log.record(&LLMRequestSnapshot{SessionID: "b", Model: "mb"})
	if got := log.get("a"); got == nil || got.Model != "ma" {
		t.Fatalf("会话 a 的记录错误: %+v", got)
	}
	if got := log.get("b"); got == nil || got.Model != "mb" {
		t.Fatalf("会话 b 的记录错误: %+v", got)
	}
	log.forget("a")
	if log.get("a") != nil {
		t.Fatal("forget 后不该还能查到")
	}
	if log.get("b") == nil {
		t.Fatal("forget 不该影响其它会话")
	}
}

// 端到端：跑完一次真实对话后，快照里应是**压缩之后**的序列，
// 而不是会话里存的原文——这正是这个视图存在的意义。
func TestLastLLMRequestReflectsCompaction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req LLMReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = r.Body.Close()
		text := "回复内容"
		if len(req.Tools) == 0 {
			text = "这是摘要标记-GAMMA"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LLMResp{Choices: []LLMChoice{{
			FinishReason: FinishReasonStop,
			Message:      LLMMessage{Role: RoleAssistant, Content: text},
		}}})
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &runRegistry{}
	app.reqLog = &llmRequestLog{}
	app.baseDir = t.TempDir()
	app.fileChanges = NewFileChangeLog(50)
	seedMessages(t, app, sess.ID, 16)

	// 先手动压一次，制造"已有摘要"的状态
	if out, err := app.compactSessionContext(context.Background(), sess.ID); err != nil || !out.Compressed {
		t.Fatalf("准备阶段压缩应成功: %+v err=%v", out, err)
	}

	if res := app.executeChat(sess.ID, "继续", false); res.Error != "" {
		t.Fatalf("对话应正常完成: %s", res.Error)
	}

	snap, err := app.GetLastLLMRequest(sess.ID)
	if err != nil || snap == nil {
		t.Fatalf("跑过一次后应有请求快照: %v", err)
	}
	if snap.SummaryIndex != 1 {
		t.Fatalf("摘要说明应在第 1 条，实际 %d", snap.SummaryIndex)
	}
	if !strings.Contains(snap.Messages[1].Content, "GAMMA") {
		t.Fatalf("快照里的摘要应是压缩产物，实际 %q", snap.Messages[1].Content)
	}
	if snap.CoveredMsgs <= 0 {
		t.Fatalf("快照应带上压缩状态，实际 %+v", snap)
	}
	// 关键：快照是压缩后的序列，条数应少于会话原文（+1 是 system）
	reloaded, _ := app.sessionStore.GetSession(sess.ID)
	if len(snap.Messages) >= len(reloaded.Messages)+1 {
		t.Fatalf("快照应是压缩后的序列：快照 %d 条（含 system），会话原文 %d 条",
			len(snap.Messages), len(reloaded.Messages))
	}
	// 工具定义只留名字，不带 schema
	if len(snap.ToolNames) == 0 {
		t.Fatal("应记录工具名")
	}
	if snap.WindowTokens != contextDefaultWindow {
		t.Fatalf("窗口应取默认值 %d，实际 %d", contextDefaultWindow, snap.WindowTokens)
	}
}
