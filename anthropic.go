package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ===== Anthropic Messages 协议适配 =====
//
// 部分网关（如携程 ada-cli-go coding-plan/ccdesktop）只提供 Anthropic Messages
// 协议，不提供 OpenAI 的 /chat/completions。本文件把内部中立的 OpenAI 形状
// 请求/响应与 Anthropic 协议互相转换，使 runToolLoop / ChatPlan 无需感知协议差异。

const (
	anthropicVersion          = "2023-06-01"
	anthropicDefaultMaxTokens = 8192
)

// isAnthropicEndpoint 判断某模型是否走 Anthropic 协议。
// 优先取显式 protocol 配置；未配置时按端点路径推断（/v1/messages）。
func isAnthropicEndpoint(m *Model) bool {
	if m == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(m.Protocol)) {
	case "anthropic", "claude":
		return true
	case "openai", "oai":
		return false
	}
	u := strings.ToLower(m.URL)
	return strings.Contains(u, "/v1/messages")
}

// resolveEndpointURL 根据协议将用户填写的 URL 规整为完整的 API 端点。
// 用户可以只填 baseUrl（如 https://api.anthropic.com），本函数自动拼接路径：
//   - anthropic 协议 → 追加 /v1/messages
//   - openai  协议  → 追加 /chat/completions
//   - 自动推断模式：保持原样（用户应填完整路径，或 URL 已含路径时也可识别）
//
// 如果 URL 已含对应路径后缀则不重复追加。
func resolveEndpointURL(model *Model) string {
	u := strings.TrimSpace(model.URL)
	if u == "" {
		return u
	}
	lower := strings.ToLower(u)
	switch strings.ToLower(strings.TrimSpace(model.Protocol)) {
	case "anthropic", "claude":
		if !strings.HasSuffix(lower, "/v1/messages") {
			u = strings.TrimRight(u, "/") + "/v1/messages"
		}
	case "openai", "oai":
		if !strings.HasSuffix(lower, "/chat/completions") {
			u = strings.TrimRight(u, "/") + "/chat/completions"
		}
	}
	return u
}

// callLLMForModel 非流式调用。
//
// ctx 是运行 ctx，必须一路传到 http.NewRequestWithContext —— 硬取消要能立刻断开
// 在途请求，用 context.Background() 会让"点了停止还要等它跑完"（最长 120s 超时）。
func callLLMForModel(ctx context.Context, model *Model, req *LLMReq) (*LLMResp, error) {
	if isAnthropicEndpoint(model) {
		return callAnthropic(ctx, resolveEndpointURL(model), model.APIKey, req)
	}
	return callLLM(ctx, resolveEndpointURL(model), model.APIKey, req)
}

func callLLMStreamForModel(ctx context.Context, model *Model, req *LLMReq, onContent func(string)) (*LLMResp, error) {
	if isAnthropicEndpoint(model) {
		return callAnthropicStream(ctx, resolveEndpointURL(model), model.APIKey, req, onContent)
	}
	return callLLMStream(ctx, resolveEndpointURL(model), model.APIKey, req, onContent)
}

// ===== 请求结构 =====

type anthropicReq struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Temperature float64            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// anthropicContentBlock 兼容请求/响应两端的块结构（请求端用 text/tool_use/tool_result，
// 响应端用 text/tool_use）。
type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

// anthropicStreamDelta 流式帧的 delta 字段。它承载两类内容：
// content_block_delta 里的文本/工具参数分片，以及 message_delta 里的 stop_reason 与 output_tokens。
type anthropicStreamDelta struct {
	Type        string         `json:"type"`
	Text        string         `json:"text"`
	PartialJSON string         `json:"partial_json"`
	StopReason  string         `json:"stop_reason"`
	Usage       anthropicUsage `json:"usage"`
}

// anthropicStreamError 流式帧里的错误对象
type anthropicStreamError struct {
	Message string `json:"message"`
}

// anthropicStreamMessage 流式 message_start 帧里的 message 字段。
// 单独提成具名类型而不是写成内联 struct：内联的多行字段类型会干扰所属结构体的
// gofmt 列对齐，抽出来两边都干净。
type anthropicStreamMessage struct {
	Usage anthropicUsage `json:"usage"`
}

// anthropicUsage Anthropic 的用量字段名（input_tokens / output_tokens）。
// 转换时映射进统一的 LLMUsage（prompt_tokens / completion_tokens），全项目只用一套名字。
type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResp struct {
	ID         string                  `json:"id"`
	Model      string                  `json:"model"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

// ===== 请求转换（OpenAI 形状 → Anthropic）=====

func toAnthropicRequest(req *LLMReq) *anthropicReq {
	out := &anthropicReq{
		Model:       req.Model,
		MaxTokens:   anthropicDefaultMaxTokens,
		Temperature: req.Temperature,
		Stream:      req.Stream,
		Tools:       toAnthropicTools(req.Tools),
	}

	var systemParts []string
	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			if strings.TrimSpace(m.Content) != "" {
				systemParts = append(systemParts, m.Content)
			}
		case RoleTool:
			// 工具结果 → user 消息中的 tool_result 块
			appendAnthropicMessage(out, RoleUser, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				blocks := make([]anthropicContentBlock, 0, len(m.ToolCalls)+1)
				if strings.TrimSpace(m.Content) != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					blocks = append(blocks, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: rawJSONObject(tc.Function.Arguments),
					})
				}
				appendAnthropicMessage(out, RoleAssistant, blocks...)
			} else if strings.TrimSpace(m.Content) != "" {
				appendAnthropicMessage(out, RoleAssistant, anthropicContentBlock{Type: "text", Text: m.Content})
			}
		default: // user
			if strings.TrimSpace(m.Content) == "" {
				continue
			}
			appendAnthropicMessage(out, RoleUser, anthropicContentBlock{Type: "text", Text: m.Content})
		}
	}
	out.System = strings.Join(systemParts, "\n\n")
	return out
}

// appendAnthropicMessage 追加消息，并把连续同角色消息合并进一条（Anthropic 要求角色交替，
// 且多个 tool_result 必须位于同一个 user 回合内）。
func appendAnthropicMessage(out *anthropicReq, role string, blocks ...anthropicContentBlock) {
	if len(blocks) == 0 {
		return
	}
	if n := len(out.Messages); n > 0 && out.Messages[n-1].Role == role {
		if prev, ok := out.Messages[n-1].Content.([]anthropicContentBlock); ok {
			out.Messages[n-1].Content = append(prev, blocks...)
			return
		}
	}
	out.Messages = append(out.Messages, anthropicMessage{Role: role, Content: blocks})
}

// rawJSONObject 把工具参数（JSON 字符串）规整为合法对象字面量，异常时回退为 {}。
// Anthropic 的 tool_use.input / input_json_delta 要求对象。
func rawJSONObject(s string) json.RawMessage {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &m); err != nil || m == nil {
		return json.RawMessage("{}")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

func toAnthropicTools(tools []LLMTool) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Function.Name)
		if name == "" {
			continue // Anthropic 要求 name 必填，跳过无名工具避免整请求 400
		}
		out = append(out, anthropicTool{
			Name:        name,
			Description: t.Function.Description,
			InputSchema: inputSchemaFor(t.Function.Parameters),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// inputSchemaFor 把工具的 JSON Schema 直通给 Anthropic（它本身就吃标准 JSON Schema，
// 与 OpenAI 的形状一致），因此不再做任何裁剪——enum/items/嵌套/required 都保留。
// 只兜底两件事：必须是对象、必须有 type（部分 server 会省略）。
func inputSchemaFor(raw json.RawMessage) map[string]interface{} {
	schema := map[string]interface{}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &schema)
	}
	if len(schema) == 0 {
		return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
	}
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}
	return schema
}

// ===== 响应转换（Anthropic → OpenAI 形状）=====

func mapAnthropicStop(reason string) string {
	switch reason {
	case "tool_use":
		return FinishReasonToolCalls
	case "max_tokens":
		return FinishReasonLength
	default: // end_turn / stop_sequence / 空
		return FinishReasonStop
	}
}

func fromAnthropicResponse(r *anthropicResp) *LLMResp {
	msg := LLMMessage{Role: RoleAssistant}
	var text strings.Builder
	var calls []LLMToolCall
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			args := "{}"
			if len(b.Input) > 0 {
				args = string(b.Input)
			}
			calls = append(calls, LLMToolCall{
				ID:   b.ID,
				Type: ToolTypeFunction,
				Function: LLMToolFunction{
					Name:      b.Name,
					Arguments: args,
				},
			})
		}
	}
	msg.Content = text.String()
	msg.ToolCalls = calls

	finish := mapAnthropicStop(r.StopReason)
	if len(calls) > 0 && finish == FinishReasonStop {
		finish = FinishReasonToolCalls
	}
	return &LLMResp{
		ID:     r.ID,
		Model:  r.Model,
		Object: "chat.completion",
		Choices: []LLMChoice{{
			FinishReason: finish,
			Message:      msg,
		}},
		Usage: LLMUsage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
		},
	}
}

// ===== HTTP 调用 =====

func newAnthropicHTTPRequest(ctx context.Context, url, token string, body []byte, stream bool) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	httpReq.Header.Set("x-api-key", token)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("Cache-Control", "no-cache")
	}
	return httpReq, nil
}

// callAnthropic 非流式调用 Anthropic Messages 协议。
func callAnthropic(ctx context.Context, url, token string, req *LLMReq) (*LLMResp, error) {
	areq := toAnthropicRequest(req)
	areq.Stream = false
	data, err := json.Marshal(areq)
	if err != nil {
		return nil, fmt.Errorf("序列化 Anthropic 请求失败: %w", err)
	}
	httpReq, err := newAnthropicHTTPRequest(ctx, url, token, data, false)
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 LLM 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM 返回错误 (status=%d): %s", resp.StatusCode, string(body))
	}

	var ar anthropicResp
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("解析 Anthropic 响应失败: %w (body=%s)", err, string(body))
	}
	return fromAnthropicResponse(&ar), nil
}

// callAnthropicStream 以 SSE 方式调用 Anthropic Messages 协议。
// 文本分片实时回调 onContent；tool_use 的 input_json_delta 分片按 index 聚合，
// 流结束后一次性返回完整 LLMResp，使主循环无需感知协议差异。
func callAnthropicStream(ctx context.Context, url, token string, req *LLMReq, onContent func(string)) (*LLMResp, error) {
	areq := toAnthropicRequest(req)
	areq.Stream = true
	data, err := json.Marshal(areq)
	if err != nil {
		return nil, fmt.Errorf("序列化 Anthropic 请求失败: %w", err)
	}
	httpReq, err := newAnthropicHTTPRequest(ctx, url, token, data, true)
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	// 流式响应不设整体超时，只约束建连与响应头
	client := &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
			Proxy:                 http.ProxyFromEnvironment,
		},
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("调用 LLM 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("LLM 返回错误 (status=%d): %s", resp.StatusCode, string(body))
	}

	var (
		content    strings.Builder
		stopReason string
		sawEvent   bool
		tcOrder    []int
		tcByIdx    = map[int]*LLMToolCall{}
		tcArgs     = map[int]*strings.Builder{}
	)

	type anthropicStreamEvent struct {
		Type         string                 `json:"type"`
		Index        int                    `json:"index"`
		ContentBlock *anthropicContentBlock `json:"content_block"`
		Delta        *anthropicStreamDelta  `json:"delta"`
		Error        *anthropicStreamError  `json:"error"`
		// Message 只在 message_start 出现，其中带 input_tokens
		Message *anthropicStreamMessage `json:"message"`
	}

	// usage 流式用量：input_tokens 来自 message_start，output_tokens 来自 message_delta，
	// 两者拼起来才是整次请求的用量（这正是"锚点"的来源）
	var usage anthropicUsage

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		sawEvent = true

		var ev anthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			fmt.Printf("[chat] 跳过无法解析的 Anthropic SSE 帧: %v\n", err)
			continue
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				usage.InputTokens = ev.Message.Usage.InputTokens
			}
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				tcByIdx[ev.Index] = &LLMToolCall{
					ID:   ev.ContentBlock.ID,
					Type: ToolTypeFunction,
					Function: LLMToolFunction{
						Name: ev.ContentBlock.Name,
					},
				}
				tcArgs[ev.Index] = &strings.Builder{}
				tcOrder = append(tcOrder, ev.Index)
			}
		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					content.WriteString(ev.Delta.Text)
					if onContent != nil {
						onContent(ev.Delta.Text)
					}
				}
			case "input_json_delta":
				if b, ok := tcArgs[ev.Index]; ok {
					b.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if ev.Delta != nil {
				if ev.Delta.StopReason != "" {
					stopReason = ev.Delta.StopReason
				}
				if ev.Delta.Usage.OutputTokens > 0 {
					usage.OutputTokens = ev.Delta.Usage.OutputTokens
				}
			}
		case "error":
			msg := "未知错误"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			return nil, fmt.Errorf("LLM 返回错误: %s", msg)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取流式响应失败: %w", err)
	}
	if !sawEvent {
		return nil, fmt.Errorf("流式响应为空")
	}

	msg := LLMMessage{Role: RoleAssistant, Content: content.String()}
	if len(tcOrder) > 0 {
		calls := make([]LLMToolCall, 0, len(tcOrder))
		for _, idx := range tcOrder {
			tc := tcByIdx[idx]
			args := tcArgs[idx].String()
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			tc.Function.Arguments = args
			calls = append(calls, *tc)
		}
		msg.ToolCalls = calls
	}

	finish := mapAnthropicStop(stopReason)
	if len(msg.ToolCalls) > 0 && finish == FinishReasonStop {
		finish = FinishReasonToolCalls
	}
	return &LLMResp{
		Object:  "chat.completion",
		Choices: []LLMChoice{{FinishReason: finish, Message: msg}},
		Usage: LLMUsage{
			PromptTokens:     usage.InputTokens,
			CompletionTokens: usage.OutputTokens,
		},
	}, nil
}
