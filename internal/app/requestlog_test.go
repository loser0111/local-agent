package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 实际发出的请求快照（排障视图）：根包侧的装配与绑定 =====
//
// 存储/脱敏/两个纯函数（RequestLog、RedactMessages、SummaryMessageIndex）的用例
// 已随实现搬到 internal/llm。这里只测根包才拿得到的东西：
// snapshotLLMRequest 的装配（agent.RunControl / agent.ContextStat / 工具视图）与 Wails 绑定方法。

// 快照必须与调用方的切片解耦。
//
// 这是这类"记录现场"功能的经典坑：循环随后还会 append / 重建 messages，
// 若快照共享底层数组，事后看到的就不是当时发出去的那一份了。
func TestSnapshotIsDecoupledFromCallerSlice(t *testing.T) {
	msgs := []llm.LLMMessage{
		{Role: store.RoleSystem, Content: "SYS"},
		{Role: store.RoleUser, Content: "原始内容"},
	}
	snap := snapshotLLMRequest(nil, nil, 0, "m", "SYS", msgs, nil, nil, false)

	// 模拟循环后续的动作：改元素、append（可能复用底层数组）
	msgs[1].Content = "被后续改写了"
	msgs = append(msgs, llm.LLMMessage{Role: store.RoleAssistant, Content: "新增的一轮"})

	if snap.Messages[1].Content != "原始内容" {
		t.Fatalf("快照应保留当时的内容，实际 %q", snap.Messages[1].Content)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("快照长度不应随后续 append 变化，实际 %d", len(snap.Messages))
	}
}

// 装配阶段对 nil 输入安全：agent.RunControl / session / ctxStat 都可能是 nil
func TestSnapshotToleratesNilInputs(t *testing.T) {
	snap := snapshotLLMRequest(nil, nil, 3, "m", "SYS", nil, nil, nil, true)
	if snap.SessionID != "" || snap.RunID != "" || snap.CoveredMsgs != 0 {
		t.Fatalf("nil 输入时不该凭空填出会话信息: %+v", snap)
	}
	if snap.Turn != 3 || snap.Model != "m" || !snap.CompactedThisTurn {
		t.Fatalf("与运行上下文无关的字段仍应如实记录: %+v", snap)
	}
	if snap.SummaryIndex != -1 {
		t.Fatalf("session 为 nil 时摘要下标应为 -1，实际 %d", snap.SummaryIndex)
	}
	if len(snap.ToolNames) != 0 {
		t.Fatalf("没有工具时不该有工具名: %+v", snap.ToolNames)
	}
}

// 零值 App（没有 reqLog）也要能查，不能 panic
func TestGetLastLLMRequestNilSafety(t *testing.T) {
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

// 端到端：跑完一次真实对话后，快照里应是**压缩之后**的序列，
// 而不是会话里存的原文——这正是这个视图存在的意义。
func TestLastLLMRequestReflectsCompaction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req llm.LLMReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = r.Body.Close()
		text := "回复内容"
		if len(req.Tools) == 0 {
			text = "这是摘要标记-GAMMA"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(llm.LLMResp{Choices: []llm.LLMChoice{{
			FinishReason: llm.FinishReasonStop,
			Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: text},
		}}})
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &agent.RunRegistry{}
	app.reqLog = &llm.RequestLog{}
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(50)
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
	if want := agent.ContextWindowOf(nil); snap.WindowTokens != want {
		t.Fatalf("窗口应取默认值 %d，实际 %d", want, snap.WindowTokens)
	}
}
