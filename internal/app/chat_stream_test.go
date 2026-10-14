package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wails-tmp/internal/llm"
)

// 标准 SSE：文本分片 + 工具调用分片 + [DONE]
func TestCallLLMStreamBasic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("缺少 SSE Accept 头")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			fmt.Fprint(w, s)
			fl.Flush()
		}
		write(`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"你好"},"finish_reason":null}]}` + "\n\n")
		write(`data: {"choices":[{"index":0,"delta":{"content":"，世界"},"finish_reason":null}]}` + "\n\n")
		write(`: ping` + "\n\n")
		write(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
		write("data: [DONE]\n\n")
	}))
	defer srv.Close()

	var got strings.Builder
	resp, err := llm.CallLLMStream(context.Background(), srv.URL, "tok", &llm.LLMReq{Model: "m"},
		func(c string) { got.WriteString(c) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "你好，世界" {
		t.Fatalf("delta 回调错误: %q", got.String())
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "你好，世界" {
		t.Fatalf("聚合响应错误: %+v", resp)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason 错误: %s", resp.Choices[0].FinishReason)
	}
}

// 工具调用 arguments 分片聚合
func TestCallLLMStreamToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		write := func(s string) { fmt.Fprint(w, s); fl.Flush() }
		write(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"exec_shell","arguments":""}}]}}]}` + "\n\n")
		write(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]}}]}` + "\n\n")
		write(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":" \"ls\"}"}}]}}]}` + "\n\n")
		write(`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n")
		write("data: [DONE]\n\n")
	}))
	defer srv.Close()

	resp, err := llm.CallLLMStream(context.Background(), srv.URL, "", &llm.LLMReq{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg := resp.Choices[0].Message
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("应聚合出 1 个工具调用，得到 %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "exec_shell" {
		t.Fatalf("工具调用头错误: %+v", tc)
	}
	if tc.Function.Arguments != `{"cmd": "ls"}` {
		t.Fatalf("arguments 分片聚合错误: %q", tc.Function.Arguments)
	}
	if tc.Type != llm.ToolTypeFunction {
		t.Fatalf("type 默认值错误: %q", tc.Type)
	}
}

// 非 SSE 错误体（200 返回普通 JSON）应被嗅探报错
func TestCallLLMStreamBadPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"error":{"message":"stream not supported"}}`)
	}))
	defer srv.Close()

	_, err := llm.CallLLMStream(context.Background(), srv.URL, "", &llm.LLMReq{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "流式响应格式错误") {
		t.Fatalf("应报格式错误，得到: %v", err)
	}
}

// 401 错误状态码
func TestCallLLMStreamErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid token"}}`)
	}))
	defer srv.Close()

	_, err := llm.CallLLMStream(context.Background(), srv.URL, "bad", &llm.LLMReq{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("应报 401，得到: %v", err)
	}
}

// 节流推送器：首片立即、后续合并
func TestStartDeltaFlusher(t *testing.T) {
	app := &App{} // ctx=nil 时不实际 emit，但节流/关闭逻辑应正常退出
	enqueue, shutdown := app.startDeltaFlusher("s1", "run_1_1", func(chunk string) ChatEvent {
		return ChatEvent{Type: ChatEventReplyDelta, Reply: chunk}
	})
	enqueue("a")
	enqueue("b")
	shutdown() // 不死锁即通过
	time.Sleep(5 * time.Millisecond)
}

// 思考分片走独立通道：正文与思考各一条 flusher，互不干扰，一次 shutdown 收尾两条。
func TestFlushStreamSplitsContentAndThinking(t *testing.T) {
	rec := &appRecorder{app: &App{}, sessionID: "s1", runID: "run_1_1"}
	content, thinking, shutdown := rec.FlushStream()
	if content == nil || thinking == nil || shutdown == nil {
		t.Fatal("FlushStream 必须返回正文/思考两条通道与一个 shutdown")
	}
	content("正文")
	thinking("思考")
	shutdown() // 两条都要收尾，否则残片卡在缓冲里（不死锁即通过）
	time.Sleep(5 * time.Millisecond)
}

// 子代理 recorder 的 FlushStream 也必须给出三条非 nil 返回值：
// 调用方是同一段主循环代码，返回 nil 会在流式路径上直接 panic。
func TestSubagentFlushStreamIsNoop(t *testing.T) {
	rec := &subagentRecorder{app: &App{}, runID: "run_1_1"}
	content, thinking, shutdown := rec.FlushStream()
	content("a")
	thinking("b")
	shutdown()
}
