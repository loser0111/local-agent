package llm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"wails-tmp/internal/store"
)

// ===== 实际发出的请求快照（排障视图）：存储、脱敏与两个纯函数 =====
//
// 「把根包侧的运行上下文组装成快照」的用例仍在根包的 requestlog_test.go
// （那边才拿得到 runControl / ContextStat / 装配好的工具视图）。

// 摘要消息的位置是固定约定：system 恒为第 0 条，摘要插在其后
func TestSummaryMessageIndex(t *testing.T) {
	msgs := []LLMMessage{
		{Role: store.RoleSystem, Content: "SYS"},
		{Role: store.RoleUser, Content: "摘要说明"},
		{Role: store.RoleUser, Content: "原文"},
	}
	withSummary := &store.Session{ContextSummary: "摘要", ContextCoveredUpTo: 5}
	if got := SummaryMessageIndex(withSummary, msgs); got != 1 {
		t.Fatalf("有摘要时应指向第 1 条，实际 %d", got)
	}

	noSummary := &store.Session{}
	if got := SummaryMessageIndex(noSummary, msgs); got != -1 {
		t.Fatalf("无摘要时应为 -1，实际 %d", got)
	}
	// 覆盖条数为 0 但摘要字段非空（不该出现的状态）：同样不标
	half := &store.Session{ContextSummary: "摘要", ContextCoveredUpTo: 0}
	if got := SummaryMessageIndex(half, msgs); got != -1 {
		t.Fatalf("覆盖条数为 0 时不应标记，实际 %d", got)
	}
	// 序列太短（只有 system）
	if got := SummaryMessageIndex(withSummary, msgs[:1]); got != -1 {
		t.Fatalf("序列过短时应为 -1，实际 %d", got)
	}
	if got := SummaryMessageIndex(nil, msgs); got != -1 {
		t.Fatalf("session 为 nil 时应为 -1，实际 %d", got)
	}
}

// 记录与读取对 nil 接收者安全（根包会直接构造零值 App 拿到 nil 的 *RequestLog）
func TestRequestLogNilSafety(t *testing.T) {
	var log *RequestLog
	log.Record(&RequestSnapshot{SessionID: "s1"}) // 不该 panic
	if got := log.Get("s1"); got != nil {
		t.Fatalf("nil 日志应返回 nil，实际 %+v", got)
	}
	log.Forget("s1")

	// 空会话 ID 不应被写进表里：那样 Get("") 会返回一份"无主"快照
	real := &RequestLog{}
	real.Record(&RequestSnapshot{SessionID: ""})
	real.Record(nil)
	if got := real.Get(""); got != nil {
		t.Fatalf("空会话 ID 不该有条目，实际 %+v", got)
	}
}

// 会话之间互不串台；删除会话会清掉记录
func TestRequestLogSessionIsolation(t *testing.T) {
	log := &RequestLog{}
	log.Record(&RequestSnapshot{SessionID: "a", Model: "ma"})
	log.Record(&RequestSnapshot{SessionID: "b", Model: "mb"})
	if got := log.Get("a"); got == nil || got.Model != "ma" {
		t.Fatalf("会话 a 的记录错误: %+v", got)
	}
	if got := log.Get("b"); got == nil || got.Model != "mb" {
		t.Fatalf("会话 b 的记录错误: %+v", got)
	}
	log.Forget("a")
	if log.Get("a") != nil {
		t.Fatal("forget 后不该还能查到")
	}
	if log.Get("b") == nil {
		t.Fatal("forget 不该影响其它会话")
	}
}

func TestRedactMessagesDropsImageBytes(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("SUPER-SECRET-IMAGE-BYTES"))
	msgs := []LLMMessage{{
		Role: store.RoleUser, Content: "看图",
		Images: []LLMImage{{MediaType: "image/png", Data: []byte("SUPER-SECRET-IMAGE-BYTES"), Bytes: 24, Width: 10, Height: 10}},
	}}
	out := RedactMessages(msgs)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("快照里不应出现图片的 base64 内容")
	}
	if !strings.Contains(string(raw), "图片已省略") || !strings.Contains(string(raw), "10×10") {
		t.Fatalf("应保留「这里有一张多大的图」这一信息，实际 %s", raw)
	}
	// 脱敏是拷贝，不能改到调用方手上的那份（它还要真的发出去）
	if len(msgs[0].Images[0].Data) == 0 || msgs[0].Images[0].Redacted {
		t.Fatal("RedactMessages 不该改动原消息")
	}
}
