package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 静默失败防线：模型返回空回复 =====
//
// 这是本项目反复出现的故障（会话标题里至少出现过 3 次："为什么失败了" /
// "为什么又断掉了" / "静默的失败，但是没有报错"）。根因是：上下文超出窗口时，
// 网关（实测 DeepSeek / Anthropic 兼容端点）**不回错误**，而是返回一个
// finish_reason=stop + 空 content 的响应。修复前 runToolLoop 会把它当最终回复
// 静默落库——用户看到一条空回复，再发"继续"也因为上下文没变而依旧为空，
// 全程没有任何报错。
//
// 这里守三件事：
//
//	① 空回复绝不落库（会话里不留空的 assistant 消息）；
//	② 空回复先尝试压缩上下文后重试一次（上下文不变的重试没有意义）；
//	③ 重试后仍为空，以明确错误返回，而不是"成功返回空回复"。
func TestEmptyReplyIsNotPersistedSilently(t *testing.T) {
	var chatCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req llm.LLMReq
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")

		var resp llm.LLMResp
		if len(req.Tools) == 0 {
			// 摘要器请求（不带 tools）：回一段摘要正文，让压缩能真的生效
			resp = llm.LLMResp{Choices: []llm.LLMChoice{{
				FinishReason: llm.FinishReasonStop,
				Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: "（历史对话摘要）"},
			}}}
		} else {
			// 主对话请求：无论发几次，一律回空回复（模拟上下文超限）
			atomic.AddInt32(&chatCalls, 1)
			resp = llm.LLMResp{Choices: []llm.LLMChoice{{
				FinishReason: llm.FinishReasonStop,
				Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: ""},
			}}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &agent.RunRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(50)

	// 造足够多的历史消息，让"空回复 → 压缩 → 重试"这条路径真的走得通：
	// CompactSession 待压缩内容少于 8 条时直接放弃压缩。
	for i := 0; i < 6; i++ {
		if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{Role: store.RoleUser, Content: fmt.Sprintf("问题 %d", i)}); err != nil {
			t.Fatal(err)
		}
		if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{Role: store.RoleAssistant, Content: fmt.Sprintf("回答 %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{Role: store.RoleUser, Content: "再问一个"}); err != nil {
		t.Fatal(err)
	}

	res := app.executeChat(sess.ID, "再问一个", false)

	// ① 必须以明确错误结束，而不是"成功返回空回复"
	if res.Error == "" {
		t.Fatalf("空回复应返回明确错误，实际 error 为空，reply=%q", res.Reply)
	}
	if !strings.Contains(res.Error, "空回复") {
		t.Fatalf("错误信息应点明是空回复，实际 %q", res.Error)
	}
	// ② 会话里绝不能留下空的 assistant 回复（修复前正是这一条被静默落库）
	saved, err := app.sessionStore.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range saved.Messages {
		if m.Role == store.RoleAssistant && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			t.Fatalf("空回复不应被持久化，却发现了空 assistant 消息: %+v", m)
		}
	}
	// ③ 应当先尝试过一次压缩（压缩请求无 tools），再放弃：主对话请求至少发了 2 次
	if n := atomic.LoadInt32(&chatCalls); n < 2 {
		t.Fatalf("应在压缩后重试一次主对话请求，实际只发了 %d 次", n)
	}
}
