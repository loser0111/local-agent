package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// newErrorKindTestApp 造一个"模型调用必然失败"的 App。
//
// 用 executeChat（而非 Chat）拿回 ChatResult：Chat 会把"有 error 无 reply"转成 Go error，
// 而这里要看的是结果对象上的 ErrorKind。
func newErrorKindTestApp(t *testing.T, handler http.HandlerFunc) (*App, *store.Session) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	app, _ := newPlanTestApp(t, srv.URL)
	app.runs = &agent.RunRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(10)
	sess, err := app.sessionStore.CreateSession(store.SessionConfig{
		Title: "错误分类", Model: "mock", Project: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{Role: store.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	return app, sess
}

// TestChatErrorKindAuthIsNotRetryable 认证失败：ErrorKind=auth，且不重试。
//
// 这条同时守住一个成本问题：401 若被判成"可重试"，计划执行层会对着一个必然失败的
// 请求反复退避重试，用户等半分钟才看到"密钥不对"。
func TestChatErrorKindAuthIsNotRetryable(t *testing.T) {
	var calls int
	app, sess := newErrorKindTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	})

	res := app.executeChat(sess.ID, "hi", false)
	if res.Error == "" {
		t.Fatal("认证失败应返回错误")
	}
	if res.ErrorKind != string(llm.ErrorKindAuth) {
		t.Fatalf("ErrorKind 应为 %q，实际 %q（错误：%s）", llm.ErrorKindAuth, res.ErrorKind, res.Error)
	}
	if calls != 1 {
		t.Fatalf("认证失败不应重试，实际请求了 %d 次", calls)
	}
}

// TestChatErrorKindBadRequestIsNotRetryable 请求本身有问题（非超限的 400）：不重试。
func TestChatErrorKindBadRequestIsNotRetryable(t *testing.T) {
	var calls int
	app, sess := newErrorKindTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"unknown model name"}}`))
	})

	res := app.executeChat(sess.ID, "hi", false)
	if res.ErrorKind != string(llm.ErrorKindBadRequest) {
		t.Fatalf("ErrorKind 应为 %q，实际 %q（错误：%s）", llm.ErrorKindBadRequest, res.ErrorKind, res.Error)
	}
	if calls != 1 {
		t.Fatalf("400 不应重试，实际请求了 %d 次", calls)
	}
}

// TestChatErrorKindEmptyForNonLLMFailures 非模型调用类失败不填 ErrorKind。
//
// "空回复"是模型回了、但内容为空——它不是一次调用失败，按 ErrorKind 去自动重试
// 只会重复拿到空回复（真正的处置是压缩上下文，那条路径已在 runToolLoop 里）。
func TestChatErrorKindEmptyForNonLLMFailures(t *testing.T) {
	app, sess := newErrorKindTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`))
	})

	res := app.executeChat(sess.ID, "hi", false)
	if res.Error == "" {
		t.Fatal("空回复应返回明确错误")
	}
	if res.ErrorKind != "" {
		t.Fatalf("空回复不应带 ErrorKind（它不是模型调用失败），实际 %q", res.ErrorKind)
	}
}
