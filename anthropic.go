package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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

// anthropicContentBlock 兼容请求/响应两端的块结构（请求端用 text/tool_use/tool_result/image，
// 响应端用 text/tool_use）。
type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	// Source 图片块专用。Anthropic 的图片块是 {"type":"image","source":{...}}，
	// 与 OpenAI 的 data URL 形状完全不同——这正是需要适配器的那类差异。
	Source *anthropicImageSource `json:"source,omitempty"`
}

// anthropicImageSource Anthropic 的 base64 图片来源。
// Data 是**裸 base64**，不带 "data:image/png;base64," 前缀（那正是 OpenAI 的形状）。
type anthropicImageSource struct {
	Type      string `json:"type"`       // 固定 "base64"
	MediaType string `json:"media_type"` // image/png / image/jpeg / image/gif
	Data      string `json:"data"`
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
// anthropicUsage Anthropic 响应的 usage。
//
// ⚠️ 语义陷阱：**input_tokens 只是「未命中缓存的那部分」**，不含缓存读写。
// 输入总量 = InputTokens + CacheCreationInputTokens + CacheReadInputTokens。
// 直接拿 InputTokens 当输入总量，会在开启缓存后把锚点算成极小值——
// 进而让上下文占比被低估、压缩阈值永不触发。映射必须走 llmUsageFromAnthropic()，
// 不要在任何调用点自己拼。
type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	// 缓存相关。未开启缓存时三者恒为 0。
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`

	// CacheCreation 写入量按 TTL 的拆分，同一时刻只有一个非零。
	CacheCreation anthropicCacheCreation `json:"cache_creation"`

	// ServiceTier standard / priority / batch，影响计价。
	ServiceTier string `json:"service_tier"`

	// ServerToolUse 服务端工具（联网搜索等）的调用统计。
	ServerToolUse anthropicServerToolUse `json:"server_tool_use"`
}

// anthropicCacheCreation 缓存写入量按 TTL 拆分。
// 它回答的是「这轮写入按 1.25x 还是 2x 计价」——服务端会单方面调整默认 TTL，
// 这个字段是用户判断「我配的 1h 到底生效没有」的唯一手段。
type anthropicCacheCreation struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}

type anthropicServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests"`
}

// llmUsageFromAnthropic 把 Anthropic 的 usage 映射成内部统一形状。
//
// 这是**唯一**允许做 Anthropic → LLMUsage 的地方。非流式与流式两处都必须走它：
// 原先两处各写一份映射，正是「两处实现悄悄漂移」的高发形态
// （本项目在权限网关与工具曝光策略上已经各吃过一次）。
func llmUsageFromAnthropic(u anthropicUsage) LLMUsage {
	return LLMUsage{
		// 输入总量必须是三者相加，见该结构体上的说明。
		PromptTokens:     u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
		CompletionTokens: u.OutputTokens,

		CacheRead:    u.CacheReadInputTokens,
		CacheWrite:   u.CacheCreationInputTokens,
		CacheWrite5m: u.CacheCreation.Ephemeral5mInputTokens,
		CacheWrite1h: u.CacheCreation.Ephemeral1hInputTokens,

		ServiceTier:            u.ServiceTier,
		ServerToolUseWebSearch: u.ServerToolUse.WebSearchRequests,
	}
}

// mergeUsage 把 src 里的**非零**字段并入 dst。
//
// 为什么是"按非零合并"而不是"整体替换"，也不是"只挑几个字段复制"：
//
//   - 整体替换不行：流式 usage 是**分两帧给的**——message_start 带输入侧（含缓存读写），
//     message_delta 带输出侧。后一帧整体覆盖会把输入侧清零。
//   - 只挑字段复制也不行（这正是原实现的写法，也是"输出恒为 0、缓存数字全丢"的根因）：
//     网关心血来潮换一种放置形状，被挑的那几个字段就再也读不到，而且**不报错**。
//   - 按非零合并对两种形状都成立：谁给了值就用谁的，各帧互补，重复给的以后者为准。
//
// "非零才覆盖"是安全的：这些字段在有真实请求时不会是 0
// （input_tokens 至少几百、output_tokens 对任何非空回复都 > 0），
// 所以 0 只可能表示"这一帧没带这个字段"，而不表示"真的是 0"。
func mergeUsage(dst *anthropicUsage, src anthropicUsage) {
	if dst == nil {
		return
	}
	if src.InputTokens != 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens != 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CacheCreationInputTokens != 0 {
		dst.CacheCreationInputTokens = src.CacheCreationInputTokens
	}
	if src.CacheReadInputTokens != 0 {
		dst.CacheReadInputTokens = src.CacheReadInputTokens
	}
	if src.CacheCreation.Ephemeral5mInputTokens != 0 {
		dst.CacheCreation.Ephemeral5mInputTokens = src.CacheCreation.Ephemeral5mInputTokens
	}
	if src.CacheCreation.Ephemeral1hInputTokens != 0 {
		dst.CacheCreation.Ephemeral1hInputTokens = src.CacheCreation.Ephemeral1hInputTokens
	}
	if src.ServiceTier != "" {
		dst.ServiceTier = src.ServiceTier
	}
	if src.ServerToolUse.WebSearchRequests != 0 {
		dst.ServerToolUse.WebSearchRequests = src.ServerToolUse.WebSearchRequests
	}
}

// rawUsageOfEvent 从 Anthropic 流式事件里取出 usage 子对象（原样文本）。
//
// 两处形状不同：message_start 的把 usage 嵌在 message 下，message_delta 的在顶层。
// 都试一遍，取到就返回，取不到返回空串（不是所有帧都带 usage）。
// 刻意返回**原样 JSON** 而不是重新序列化我们解析出的结构体——
// 排障时「网关到底发了什么」比「我们解析出了什么」更有价值。
func rawUsageOfEvent(payload []byte) string {
	var probe struct {
		Usage   json.RawMessage `json:"usage"`
		Message struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return ""
	}
	if len(probe.Usage) > 0 {
		return string(probe.Usage)
	}
	if len(probe.Message.Usage) > 0 {
		return string(probe.Message.Usage)
	}
	return ""
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
			// assistant 消息不带图（模型不会"发出"图片），呈现在这里会被静默丢掉；
			// 若日后要做"模型生成图片"，必须在这里补分支而不是让它悄悄消失。
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
			// 有图但没文字的消息也必须发出去，所以判空条件是"两者皆空"
			if strings.TrimSpace(m.Content) == "" && len(m.Images) == 0 {
				continue
			}
			blocks := make([]anthropicContentBlock, 0, len(m.Images)+1)
			// 文本在前、图片在后：与 Claude Code 的实际行为一致（用户先写一句话再附图）。
			// 顺序对正确性没有要求，只影响模型对"这句话是在说哪张图"的理解。
			// textPartFor：**纯图片消息必须补一个文本块**——Anthropic 本身允许
			// 只有 image 的 content，但网关侧未必，而两条协议共用同一条规则更不容易漂。
			if text := textPartFor(m.Content, m.Images); strings.TrimSpace(text) != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: text})
			}
			for _, img := range appendAnthropicImages(m.Images) {
				blocks = append(blocks, img)
			}
			appendAnthropicMessage(out, RoleUser, blocks...)
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

// appendAnthropicImages 把内部中立的图片载荷转成 Anthropic 的 image 块。
//
// 两处形状差异都在这里吸收掉：
//   - OpenAI 用 data URL（前缀 + base64 拼在一起），Anthropic 要求 media_type 与
//     data 分成两个字段；
//   - 脱敏后的图片（请求快照路径）不能编一段假 base64 发出去，降级成一行文本说明，
//     这样"这里本该有张图"这件事在报文里仍然是可见的。
func appendAnthropicImages(images []LLMImage) []anthropicContentBlock {
	if len(images) == 0 {
		return nil
	}
	out := make([]anthropicContentBlock, 0, len(images))
	for _, img := range images {
		if img.Redacted {
			out = append(out, anthropicContentBlock{Type: "text", Text: "（" + img.describe() + "）"})
			continue
		}
		mediaType := img.MediaType
		if mediaType == "" {
			mediaType = "image/png"
		}
		out = append(out, anthropicContentBlock{
			Type: "image",
			Source: &anthropicImageSource{
				Type:      "base64",
				MediaType: mediaType,
				Data:      base64.StdEncoding.EncodeToString(img.Data),
			},
		})
	}
	return out
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
		Usage: llmUsageFromAnthropic(r.Usage),
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

// logAnthropicImageShape 出站前记录带图请求的**实际形状**。
//
// 为什么必须记：`RequestPreviewDialog` 展示的是**内部中立格式**（OpenAI 形状）的序列，
// Anthropic 侧还会再转一次（system 抽出、图片变成 source 块）。两次转换之间出问题的话，
// 在对话框里一点异常都看不出来——真机上已经吃过这个亏（模型说"图片只以 URL 形式出现"）。
// 只记形状与长度，不记内容。
func logAnthropicImageShape(areq *anthropicReq) {
	if areq == nil {
		return
	}
	blocks, images := 0, 0
	firstType, firstMedia, firstLen := "", "", 0
	for _, m := range areq.Messages {
		bs, ok := m.Content.([]anthropicContentBlock)
		if !ok {
			continue
		}
		for _, b := range bs {
			blocks++
			if b.Type != "image" {
				continue
			}
			images++
			if images == 1 && b.Source != nil {
				firstType = b.Source.Type
				firstMedia = b.Source.MediaType
				firstLen = len(b.Source.Data)
			}
		}
	}
	if images == 0 {
		return // 不带图的请求不记，免得刷屏
	}
	fmt.Printf("[协议] Anthropic 出站：%d 条消息 / %d 个内容块 / %d 个图像块；首个图像 type=%s media=%s data=%d 字符\n",
		len(areq.Messages), blocks, images, firstType, firstMedia, firstLen)
}

// callAnthropic 非流式调用 Anthropic Messages 协议。
func callAnthropic(ctx context.Context, url, token string, req *LLMReq) (*LLMResp, error) {
	areq := toAnthropicRequest(req)
	areq.Stream = false
	logAnthropicImageShape(areq)
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
	// 注意变量名不能叫 resp：上面那个 resp 是 *http.Response，同名用 := 会
	// 编译不过（"no new variables on left side of :="）。
	out := fromAnthropicResponse(&ar)
	out.Usage.RawUsage = extractRawUsage(body)
	return out, nil
}

// callAnthropicStream 以 SSE 方式调用 Anthropic Messages 协议。
// 文本分片实时回调 onContent；tool_use 的 input_json_delta 分片按 index 聚合，
// 流结束后一次性返回完整 LLMResp，使主循环无需感知协议差异。
func callAnthropicStream(ctx context.Context, url, token string, req *LLMReq, onContent func(string)) (*LLMResp, error) {
	areq := toAnthropicRequest(req)
	areq.Stream = true
	logAnthropicImageShape(areq)
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
		// Message 只在 message_start 出现，其中带输入侧 usage（input_tokens + 缓存读写）
		Message *anthropicStreamMessage `json:"message"`
		// Usage 事件**顶层**的 usage。
		//
		// 规范里 message_delta 的 usage 嵌在 delta 下，但**实测本项目所用的网关
		// 把它放在事件顶层**，且是完整的一份（input/output/cache_creation/cache_read 全有）。
		// 少读这一处，表现就是"输出 token 恒为 0、缓存数字全丢"——
		// 而请求本身完全成功，没有任何报错。两处都要读，见 mergeUsage。
		Usage *anthropicUsage `json:"usage"`
	}

	// usage 流式用量：input_tokens 来自 message_start，output_tokens 来自 message_delta，
	// 两者拼起来才是整次请求的用量（这正是"锚点"的来源）
	var usage anthropicUsage

	// rawUsageParts 收集带 usage 的那几帧的 usage 子对象（原样文本）。
	// 流式没有单一响应体可留，只能逐帧收集；最后拼成一个合法 JSON 数组交给排障视图。
	// 两个来源都保留（而不是后一个覆盖前一个）：缓存字段只在 message_start 里，
	// 输出 token 只在 message_delta 里，丢掉任一个都会让排障区看不出全貌。
	var rawUsageParts []string

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
		if raw := rawUsageOfEvent([]byte(payload)); raw != "" {
			rawUsageParts = append(rawUsageParts, raw)
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				// 整份合并，而不是只挑 InputTokens：
				// 输入侧的缓存读写（cache_read_input_tokens 等）**只在这一帧里**。
				mergeUsage(&usage, ev.Message.Usage)
			}
			if ev.Usage != nil {
				mergeUsage(&usage, *ev.Usage)
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
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			// 两处都读：规范形状在 delta.usage，实测网关在事件顶层 usage。
			// 顶层那份通常是完整的一份（含缓存字段），所以它放在后面 merg——非零覆盖。
			if ev.Delta != nil {
				mergeUsage(&usage, ev.Delta.Usage)
			}
			if ev.Usage != nil {
				mergeUsage(&usage, *ev.Usage)
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
	// 先归一化，再补原始文本：原始文本只是排障用，不能影响归一结果。
	out := llmUsageFromAnthropic(usage)
	if len(rawUsageParts) > 0 {
		out.RawUsage = "[" + strings.Join(rawUsageParts, ",") + "]"
	}
	return &LLMResp{
		Object:  "chat.completion",
		Choices: []LLMChoice{{FinishReason: finish, Message: msg}},
		Usage:   out,
	}, nil
}
