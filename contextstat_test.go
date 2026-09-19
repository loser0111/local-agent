package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== 上下文压缩偏好（可配置项）=====

// 没配过 = 用内置默认值，而不是 0
func TestContextPrefsDefaultWhenFileMissing(t *testing.T) {
	s := NewContextPrefsStore(filepath.Join(t.TempDir(), "context.json"))
	if got := s.Get().KeepRecentMsgs; got != contextKeepRecentMsgs {
		t.Fatalf("未配置时应回落默认 %d，实际 %d", contextKeepRecentMsgs, got)
	}
}

// 落盘后新开一个 store 也要读得回来（模拟重启）
func TestContextPrefsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context.json")
	if err := NewContextPrefsStore(path).SetKeepRecentMsgs(30); err != nil {
		t.Fatal(err)
	}
	if got := NewContextPrefsStore(path).Get().KeepRecentMsgs; got != 30 {
		t.Fatalf("重启后应读到 30，实际 %d", got)
	}
}

// 越界值：写入当场被拒；文件被人手改坏也要回落默认（不能把压缩策略变成不可控值）
func TestContextPrefsBoundsAndCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context.json")
	s := NewContextPrefsStore(path)
	if err := s.SetKeepRecentMsgs(0); err == nil {
		t.Fatal("0 应被拒绝，而不是悄悄存下去")
	}
	if err := s.SetKeepRecentMsgs(contextKeepRecentMax + 1); err == nil {
		t.Fatal("超上限应被拒绝")
	}
	if err := os.WriteFile(path, []byte(`{"keepRecentMsgs": 99999}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := NewContextPrefsStore(path).Get().KeepRecentMsgs; got != contextKeepRecentMsgs {
		t.Fatalf("文件里的越界值应回落默认 %d，实际 %d", contextKeepRecentMsgs, got)
	}
}

// 存储未初始化（测试里直接构造 App）不能让策略变成"留 0 条"，那会把对话压没
func TestKeepRecentMsgsNilStore(t *testing.T) {
	app := &App{}
	if got := app.keepRecentMsgs(); got != contextKeepRecentMsgs {
		t.Fatalf("无存储时应回落 %d，实际 %d", contextKeepRecentMsgs, got)
	}
}

// 配置项真的传导到了压缩切点：16 条消息、默认留 12 条 → 只能覆盖 4 条；
// 配成留 4 条之后应覆盖 12 条。
func TestCompactUsesConfiguredKeepRecent(t *testing.T) {
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
	app.contextPrefs = NewContextPrefsStore(filepath.Join(t.TempDir(), "context.json"))
	if err := app.contextPrefs.SetKeepRecentMsgs(4); err != nil {
		t.Fatal(err)
	}
	seedMessages(t, app, sess.ID, 16)

	out, err := app.compactSessionContext(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Compressed {
		t.Fatalf("应完成压缩，实际未压缩：%s", out.Reason)
	}
	if out.CoveredMsgs != 12 {
		t.Fatalf("留 4 条时应覆盖 16-4=12 条，实际 %d", out.CoveredMsgs)
	}
}

// ===== /context-stat 命令 =====

// 命令路径是纯读操作：不发起任何模型调用，但仍要落一条 assistant 说明消息
func TestContextStatCommand(t *testing.T) {
	app, sess := newPlanTestApp(t, "http://127.0.0.1:9/never-called")
	app.runs = &runRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = NewFileChangeLog(50)
	seedMessages(t, app, sess.ID, 6)

	res, err := app.Chat(sess.ID, "/context-stat", false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"上下文统计", "用量：", "消息：", "摘要：未压缩", "锚点：无", "压缩触发线", "保留最近原文"} {
		if !strings.Contains(res.Reply, want) {
			t.Fatalf("报告缺少 %q：\n%s", want, res.Reply)
		}
	}
	if len(res.Messages) != 1 || res.Messages[0].Role != RoleAssistant {
		t.Fatalf("应落一条 assistant 消息，实际 %+v", res.Messages)
	}
	if res.Context == nil {
		t.Fatal("应带回上下文用量")
	}
	if res.Context.HasAnchor {
		t.Fatal("命令路径没有真实模型调用，不该声称有锚点")
	}
}

// 报告要如实反映"已压缩"状态：覆盖条数、省下的 token、生成时间
func TestContextStatReportReportsCompaction(t *testing.T) {
	sess := &Session{
		ID:                 "s1",
		Messages:           []Message{{Role: RoleUser, Content: strings.Repeat("内容", 100)}, {Role: RoleAssistant, Content: "好"}},
		ContextSummary:     "摘要正文",
		ContextCoveredUpTo: 1,
		ContextSummaryAt:   1700000000000,
	}
	st := contextStatOf(sess, buildRunMessages(sess, "", false), 10000, nil, 0, 1.0)
	if st.SavedTokens <= 0 {
		t.Fatalf("覆盖了 100 字原文却算不出省下的 token：%+v", st)
	}
	rep := FormatContextStatReport(st, 30)
	for _, want := range []string{"已覆盖前 1 条消息", "省下：约", "生成于 20", "保留最近原文：30 条"} {
		if !strings.Contains(rep, want) {
			t.Fatalf("报告缺少 %q：\n%s", want, rep)
		}
	}
}

// 锚点状态来自真实 usage 回传：有锚点时报告要说明基准是真实 token 数
func TestContextStatHasAnchor(t *testing.T) {
	sess := &Session{}
	msgs := []LLMMessage{{Role: RoleUser, Content: "hi"}}
	if st := contextStatOf(sess, msgs, 1000, nil, 0, 1.0); st.HasAnchor {
		t.Fatal("无锚点时应为 false")
	}
	st := contextStatOf(sess, msgs, 1000, &tokenAnchor{InputTokens: 50, MsgCount: 1}, 0, 1.0)
	if !st.HasAnchor {
		t.Fatal("有锚点时应为 true")
	}
	if rep := FormatContextStatReport(st, 12); !strings.Contains(rep, "锚点：有") {
		t.Fatalf("有锚点时报告应说明，实际：\n%s", rep)
	}
}

// 清空摘要 = 回到全量：省下的 token 必须跟着归零，否则报告会说谎
func TestSavedTokensClearedWithSummary(t *testing.T) {
	sess := &Session{
		Messages:           []Message{{Role: RoleUser, Content: strings.Repeat("内容", 100)}},
		ContextSummary:     "摘要正文",
		ContextCoveredUpTo: 1,
	}
	if got := summarySavedTokens(sess, 1.0); got <= 0 {
		t.Fatalf("压缩态应算出正数，实际 %d", got)
	}
	sess.ContextSummary = ""
	sess.ContextCoveredUpTo = 0
	if got := summarySavedTokens(sess, 1.0); got != 0 {
		t.Fatalf("清空摘要后应归零，实际 %d", got)
	}
}
