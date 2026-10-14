package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"wails-tmp/internal/store"
)

// ===== Extended Thinking 链路回归 =====
//
// 这一组守的是"开了 thinking 却什么都没记下来"这一类静默失效。链路共 5 段，
// 任意一段断了症状都一样（thinking 恒为空），所以每段各钉一条：
//  ① 请求侧：thinking 配置真的进了请求体；
//  ② 非流式响应：thinking 块（字段名 thinking，不是 text）+ signature 被解析出来；
//  ③ 流式响应：thinking_delta / signature_delta 被解析出来（主路径正是流式）；
//  ④ 回传：带工具调用的 assistant 消息，thinking + signature 被放回请求（否则 400）；
//  ⑤ OpenAI 系：reasoning_content / reasoning 被解析出来。

// ① thinking 配置进请求体
func TestAnthropicRequestCarriesThinking(t *testing.T) {
	req := &LLMReq{
		Model:    "claude-x",
		Messages: []LLMMessage{{Role: store.RoleUser, Content: "hi"}},
		Thinking: &LLMThinking{Type: "enabled", BudgetTokens: 5000},
	}
	out := ToAnthropicRequest(req)
	if out.Thinking == nil {
		t.Fatal("thinking 配置应被转换进 Anthropic 请求")
	}
	if out.Thinking.Type != "enabled" || out.Thinking.BudgetTokens != 5000 {
		t.Fatalf("thinking 参数不正确: %+v", *out.Thinking)
	}
	// 关掉时不应出现 thinking 字段
	off := ToAnthropicRequest(&LLMReq{Messages: []LLMMessage{{Role: store.RoleUser, Content: "hi"}}})
	if off.Thinking != nil {
		t.Fatalf("未启用 thinking 时不应带上 thinking 参数: %+v", *off.Thinking)
	}
}

// ② 非流式响应解析 thinking 块 + 签名
func TestFromAnthropicResponseParsesThinkingBlock(t *testing.T) {
	resp := &anthropicResp{
		ID: "msg_1",
		Content: []AnthropicContentBlock{
			{Type: "thinking", Thinking: "先想一下", Signature: "sig-abc"},
			{Type: "text", Text: "答案是 42"},
		},
		StopReason: "end_turn",
	}
	got := fromAnthropicResponse(resp)
	if got.Thinking != "先想一下" {
		t.Fatalf("thinking 正文应为 %q，实际 %q（字段名必须是 thinking 而不是 text）", "先想一下", got.Thinking)
	}
	if got.ThinkingSignature != "sig-abc" {
		t.Fatalf("thinking 签名应为 sig-abc，实际 %q", got.ThinkingSignature)
	}
	if got.Choices[0].Message.Content != "答案是 42" {
		t.Fatalf("正文应为 %q，实际 %q", "答案是 42", got.Choices[0].Message.Content)
	}
}

// ③ 流式响应解析 thinking_delta / signature_delta
func TestAnthropicStreamParsesThinkingDeltas(t *testing.T) {
	var sse strings.Builder
	write := func(v interface{}) {
		b, _ := json.Marshal(v)
		sse.WriteString("data: " + string(b) + "\n\n")
	}
	write(map[string]interface{}{"type": "message_start", "message": map[string]interface{}{
		"usage": map[string]interface{}{"input_tokens": 10},
	}})
	write(map[string]interface{}{"type": "content_block_delta", "index": 0,
		"delta": map[string]interface{}{"type": "thinking_delta", "thinking": "想一"}})
	write(map[string]interface{}{"type": "content_block_delta", "index": 0,
		"delta": map[string]interface{}{"type": "thinking_delta", "thinking": "想二"}})
	write(map[string]interface{}{"type": "content_block_delta", "index": 0,
		"delta": map[string]interface{}{"type": "signature_delta", "signature": "sig-stream"}})
	write(map[string]interface{}{"type": "content_block_delta", "index": 0,
		"delta": map[string]interface{}{"type": "text_delta", "text": "最终答案"}})
	write(map[string]interface{}{"type": "message_delta", "delta": map[string]interface{}{
		"stop_reason": "end_turn", "usage": map[string]interface{}{"output_tokens": 5},
	}})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse.String()))
	}))
	defer srv.Close()

	var streamed, streamedThinking strings.Builder
	resp, err := CallAnthropicStream(context.Background(), srv.URL, "k",
		&LLMReq{Model: "claude-x", Messages: []LLMMessage{{Role: store.RoleUser, Content: "hi"}}},
		func(s string) { streamed.WriteString(s) },
		func(s string) { streamedThinking.WriteString(s) })
	if err != nil {
		t.Fatalf("流式调用失败: %v", err)
	}
	if resp.Thinking != "想一"+"想二" {
		t.Fatalf("流式 thinking 应累加为 %q，实际 %q", "想一"+"想二", resp.Thinking)
	}
	if resp.ThinkingSignature != "sig-stream" {
		t.Fatalf("流式签名应为 sig-stream，实际 %q", resp.ThinkingSignature)
	}
	// 思考过程不能混进正文流（否则会出现在回答气泡里）
	if strings.Contains(streamed.String(), "想一") {
		t.Fatalf("thinking 分片不应推给正文通道，实际收到 %q", streamed.String())
	}
	if streamed.String() != "最终答案" {
		t.Fatalf("正文流应为 %q，实际 %q", "最终答案", streamed.String())
	}
	// 但要经 onThinking 实时外推：只累加不回调的话，用户在模型思考期间看不到任何东西
	if streamedThinking.String() != "想一"+"想二" {
		t.Fatalf("thinking 分片应实时外推为 %q，实际 %q", "想一"+"想二", streamedThinking.String())
	}
}

// ④ 带工具调用的 assistant 消息回传 thinking + 签名（Anthropic 的硬性要求）
func TestAnthropicRequestResendsThinkingForToolCalls(t *testing.T) {
	req := &LLMReq{
		Model: "claude-x",
		Messages: []LLMMessage{
			{Role: store.RoleUser, Content: "查一下"},
			{
				Role:              store.RoleAssistant,
				ToolCalls:         []LLMToolCall{{ID: "call_1", Type: ToolTypeFunction, Function: LLMToolFunction{Name: "read_file", Arguments: `{"path":"a"}`}}},
				Thinking:          "我得先读文件",
				ThinkingSignature: "sig-xyz",
			},
			{Role: store.RoleTool, Content: "文件内容", ToolCallID: "call_1"},
		},
	}
	out := ToAnthropicRequest(req)

	var assistantBlocks []AnthropicContentBlock
	for _, m := range out.Messages {
		if m.Role != store.RoleAssistant {
			continue
		}
		if b, ok := m.Content.([]AnthropicContentBlock); ok {
			assistantBlocks = b
		}
	}
	if len(assistantBlocks) == 0 {
		t.Fatal("未找到 assistant 消息块")
	}
	if assistantBlocks[0].Type != "thinking" {
		t.Fatalf("thinking 块必须排在 assistant 消息最前（规范要求），实际首块是 %q", assistantBlocks[0].Type)
	}
	if assistantBlocks[0].Thinking != "我得先读文件" || assistantBlocks[0].Signature != "sig-xyz" {
		t.Fatalf("thinking 块内容/签名不正确: %+v", assistantBlocks[0])
	}
	hasToolUse := false
	for _, b := range assistantBlocks {
		if b.Type == "tool_use" {
			hasToolUse = true
		}
	}
	if !hasToolUse {
		t.Fatal("tool_use 块丢失")
	}
}

// ⑤ OpenAI 系：流式 reasoning_content / reasoning 被解析
func TestOpenAIStreamParsesReasoning(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"推理A"}}]}`,
		`data: {"choices":[{"delta":{"reasoning":"推理B"}}]}`,
		`data: {"choices":[{"delta":{"content":"回答"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var streamed, streamedThinking strings.Builder
	resp, err := CallLLMStream(context.Background(), srv.URL, "k",
		&LLMReq{Model: "m", Messages: []LLMMessage{{Role: store.RoleUser, Content: "hi"}}},
		func(s string) { streamed.WriteString(s) },
		func(s string) { streamedThinking.WriteString(s) })
	if err != nil {
		t.Fatalf("流式调用失败: %v", err)
	}
	if resp.Thinking != "推理A推理B" {
		t.Fatalf("reasoning 应累加为 %q，实际 %q", "推理A推理B", resp.Thinking)
	}
	if streamed.String() != "回答" {
		t.Fatalf("正文流应为 %q，实际 %q", "回答", streamed.String())
	}
	// reasoning 同样要实时外推（与 Anthropic 的 thinking_delta 对齐）
	if streamedThinking.String() != "推理A推理B" {
		t.Fatalf("reasoning 应实时外推为 %q，实际 %q", "推理A推理B", streamedThinking.String())
	}
}

// ⑥ OpenAI 系：非流式 message.reasoning_content 被解析
func TestOpenAIParsesNonStreamReasoning(t *testing.T) {
	body := `{"id":"t","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop",` +
		`"message":{"role":"assistant","content":"答案","reasoning_content":"非流式推理"}}]}`
	srv, _ := captureLLMServer(t, body)

	resp, err := callLLM(context.Background(), srv.URL, "k",
		&LLMReq{Model: "m", Messages: []LLMMessage{{Role: store.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if resp.Thinking != "非流式推理" {
		t.Fatalf("非流式 reasoning 应为 %q，实际 %q", "非流式推理", resp.Thinking)
	}
}

// ⑧ 思考分片也算「已吐出」：重试会把同一段思考再渲染一遍。
//
// 前端对思考是**追加**渲染的，重试会让用户看到思考"倒带重来"，
// 甚至接上第二轮完全不同的思考——比直接报错更让人困惑。
func TestStreamNoRetryAfterThinkingEmitted(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		// 只吐思考、正文一个字都还没吐，然后报一个本可重试的限流错
		_, _ = w.Write([]byte(sse(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"先想想"}}`)))
		_, _ = w.Write([]byte(sse(`{"type":"error","error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`)))
	}))
	defer srv.Close()

	model := &store.Model{Name: "claude-x", ModelID: "claude-x", Protocol: "anthropic", URL: srv.URL, APIKey: "k"}
	var gotThinking strings.Builder
	_, err := CallLLMStreamForModelWith(context.Background(), model, &LLMReq{
		Model:    "claude-x",
		Messages: []LLMMessage{{Role: store.RoleUser, Content: "hi"}},
		Thinking: &LLMThinking{Type: "enabled", BudgetTokens: 4096},
	}, nil, func(s string) { gotThinking.WriteString(s) }, fastPolicy(3))
	if err == nil {
		t.Fatal("流内错误帧应当让本次调用失败")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("已经吐出过思考就不该重试；实际请求 %d 次", n)
	}
	if gotThinking.String() != "先想想" {
		t.Fatalf("已吐出的思考不该被丢弃: %q", gotThinking.String())
	}
}

func TestAnthropicThinkingEndToEnd(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":8}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"思考中"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"S1"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"答案"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","usage":{"output_tokens":3}}}`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	defer srv.Close()

	model := &store.Model{Name: "claude-x", ModelID: "claude-x", Protocol: "anthropic", URL: srv.URL, APIKey: "k"}
	resp, err := CallLLMStreamForModel(context.Background(), model, &LLMReq{
		Model:    "claude-x",
		Messages: []LLMMessage{{Role: store.RoleUser, Content: "hi"}},
		Thinking: &LLMThinking{Type: "enabled", BudgetTokens: 4096},
	}, nil, nil)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if resp.Thinking != "思考中" || resp.ThinkingSignature != "S1" {
		t.Fatalf("thinking 解析不正确: thinking=%q sig=%q", resp.Thinking, resp.ThinkingSignature)
	}
	if resp.Choices[0].Message.Content != "答案" {
		t.Fatalf("正文应为 答案，实际 %q", resp.Choices[0].Message.Content)
	}
	// 请求体里确实带了 thinking
	th, _ := gotBody["thinking"].(map[string]interface{})
	if th == nil || th["type"] != "enabled" {
		t.Fatalf("请求体未携带 thinking 配置: %v", gotBody["thinking"])
	}
	if b, _ := th["budget_tokens"].(float64); int(b) != 4096 {
		t.Fatalf("budget_tokens 应为 4096，实际 %v", th["budget_tokens"])
	}
}
