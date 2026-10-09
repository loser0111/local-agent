package llm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ===== LLM 协议类型（OpenAI 兼容形状）=====
//
// 本文件由 chat.go 拆出：这些类型是 LLM 层的公共词汇表，协议适配（anthropic.go）与
// 重试层（llmretry.go）都依赖它。放这里也解开了 usage.go（用量统计）对 LLMUsage 的依赖。

// ===== LLM 请求数据结构（OpenAI 兼容格式）=====

// LLMMessage LLM API 的消息格式。
//
// Images 是**协议中立**的载荷：这里只声明"这条消息带了这几张图"，
// 具体拼成什么形状由各自的适配器决定（chat.go 的 MarshalJSON 负责 OpenAI 的
// content 数组；anthropic.go 负责 image 块）。这一点很重要——两条协议对
// "图片能出现在哪"的规定并不相同（见 buildLLMMessages 里工具结果那段说明）。
type LLMMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []LLMToolCall `json:"tool_calls,omitempty"`
	Images     []LLMImage    `json:"images,omitempty"`
}

// LLMImage 一次请求里携带的一张图片
type LLMImage struct {
	MediaType string `json:"-"`
	Data      []byte `json:"-"`
	// Bytes/Width/Height 只是元数据：Data 被清空（快照脱敏）之后，
	// 界面上还要能显示"这里原本有一张多大的图"。
	Bytes  int `json:"-"`
	Width  int `json:"-"`
	Height int `json:"-"`
	// Redacted 为 true 时不做 base64 编码，只输出一行占位说明。
	// 只有请求快照会置它——把几 MB 的图片塞进排障视图毫无意义。
	Redacted bool `json:"-"`
}

// dataURL 拼成 data URL（OpenAI 兼容协议用这个形状传图）
func (img LLMImage) dataURL() string {
	mt := img.MediaType
	if mt == "" {
		mt = "image/png"
	}
	return "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

// describe 一行人类可读描述（脱敏占位与日志都用它）
func (img LLMImage) describe() string {
	dim := ""
	if img.Width > 0 && img.Height > 0 {
		dim = fmt.Sprintf("%d×%d，", img.Width, img.Height)
	}
	return fmt.Sprintf("图片已省略：%s%s，%.0f KB", dim, img.MediaType, float64(img.Bytes)/1024)
}

// ImageOnlyTextPart 纯图片消息补的那个文本块。
// 写成一句有信息量的话而不是空串：模型由此知道"用户只发了图、没别的话"，
// 不至于反过来猜"用户是不是发了链接"。
const ImageOnlyTextPart = "（用户发来一张图片，没有附加文字）"

// TextPartFor 返回这条消息应当写入的文本块内容。
//
// **带图的消息必须同时带一个文本块**，哪怕用户一个字都没写。
// 这是真机上踩出来的（见 docs/image-input-design.md §6.4）：同一次对话里，
// 用户纯图片消息（内容数组里只有 image 块）模型读不到，而 read_image 工具结果的图
// （带一句说明文本）能读到——两者的差别正是有没有文本块。
// 多一个文本块对 OpenAI 与 Anthropic 两条协议都无害，缺了却可能整张图失效。
func TextPartFor(content string, images []LLMImage) string {
	if len(images) == 0 || strings.TrimSpace(content) != "" {
		return content
	}
	return ImageOnlyTextPart
}

// MarshalJSON 序列化一条消息。
//
// 无图时走 plain 结构体——**输出必须与加 Images 字段之前逐字节一致**：
// 请求快照、测试断言、各家网关都依赖这个形状。
// 有图时 content 变成块数组（OpenAI 的视觉输入形状）：
//
//	{"role":"user","content":[{"type":"text","text":"..."},
//	                          {"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}]}
//
// 注意：只有 user 角色的消息会带图。工具产出的图由 buildLLMMessages 拆成一条
// 独立的 user 消息承载——OpenAI 的 tool 消息 content 只能是字符串，塞数组会被网关拒。
func (m LLMMessage) MarshalJSON() ([]byte, error) {
	if len(m.Images) == 0 {
		// 用一个没有方法的别名类型，避免 json.Marshal 再次进到本函数（无限递归）
		type plain LLMMessage
		return json.Marshal(plain(m))
	}
	parts := make([]map[string]interface{}, 0, len(m.Images)+1)
	// TextPartFor：纯图片消息也要补一个文本块（见该函数的说明）
	if text := TextPartFor(m.Content, m.Images); strings.TrimSpace(text) != "" {
		parts = append(parts, map[string]interface{}{"type": "text", "text": text})
	}
	for _, img := range m.Images {
		url := img.dataURL()
		if img.Redacted {
			url = img.describe()
		}
		parts = append(parts, map[string]interface{}{
			"type":      "image_url",
			"image_url": map[string]interface{}{"url": url},
		})
	}
	out := map[string]interface{}{"role": m.Role, "content": parts}
	if m.ToolCallID != "" {
		out["tool_call_id"] = m.ToolCallID
	}
	if len(m.ToolCalls) > 0 {
		out["tool_calls"] = m.ToolCalls
	}
	return json.Marshal(out)
}

// LLMToolCall LLM 返回的工具调用
type LLMToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function LLMToolFunction `json:"function"`
}

// LLMToolFunction 工具调用的函数信息
type LLMToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 字符串格式
}

// LLMTool LLM API 的工具定义
type LLMTool struct {
	Type     string     `json:"type"`
	Function LLMToolDef `json:"function"`
}

// LLMToolDef 工具定义
//
// Parameters 直接用 **原始 JSON Schema**（json.RawMessage）而不是结构体：
// MCP 工具的 inputSchema 里可能有 enum、items、嵌套 properties 与 required，
// 任何中间结构体都会把它们压平（曾经就是这样，模型只能猜参数形状）。
// 两条协议都吃标准 JSON Schema，所以这里原样直通即可。
type LLMToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// LLMReq LLM 请求
type LLMReq struct {
	Model       string       `json:"model"`
	Messages    []LLMMessage `json:"messages"`
	Temperature float64      `json:"temperature"`
	Stream      bool         `json:"stream"`
	Tools       []LLMTool    `json:"tools,omitempty"`
}

// LLMResp LLM 响应
// LLMUsage 一次请求的真实用量。
//
// 字段名以 OpenAI 的 prompt_tokens / completion_tokens 为基准，
// 其余协议在各自的适配层归一进来（Anthropic 见 llmUsageFromAnthropic）。
//
// ⚠️ 一条必须成立的不变式：**PromptTokens 恒为「输入总量」**。
//   - Anthropic 的 input_tokens 只是「未命中缓存的那部分」，适配层要把
//     input + cache_creation + cache_read 相加后再填进来，**不能原样搬**；
//   - OpenAI / DeepSeek 的 prompt_tokens 本身就是总量（含缓存），原样即可。
//
// 有了这条不变式，「未命中 = PromptTokens − CacheRead − CacheWrite」对三家都成立，
// tokenUsageOf 里因此不需要任何协议判断。这条不变式曾经被破坏过：
// 两处 Anthropic 映射各写一份、都直接填 InputTokens，一旦开启缓存，
// 上下文锚点会骤降到极小值，压缩阈值永不触发——而症状要等到聊很久之后才显现。
//
// 有它才能把「纯字符估算」升级成「真实锚点 + 增量估算」——精度是数量级差别。
type LLMUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens,omitempty"`

	// ===== 以下为归一化后的统一字段（各协议的原始形状在 normalize 里摊平）=====

	// CacheRead 命中缓存的输入量 / CacheWrite 写入缓存的量。
	CacheRead  int `json:"cache_read_input_tokens,omitempty"`
	CacheWrite int `json:"cache_creation_input_tokens,omitempty"`
	// CacheWrite5m / CacheWrite1h 仅 Anthropic 提供（usage.cache_creation 的 TTL 拆分）。
	CacheWrite5m int `json:"-"`
	CacheWrite1h int `json:"-"`
	// ReasoningTokens 推理 token：计费算输出，但不进正文，对用户是完全看不见的成本。
	ReasoningTokens int `json:"-"`
	// ServiceTier 与 ServerToolUseWebSearch 仅 Anthropic 提供，用于解释计价与额外调用。
	ServiceTier            string `json:"-"`
	ServerToolUseWebSearch int    `json:"-"`

	// ===== 各协议的原始形状（只用于解析，统一字段以上面那组为准）=====

	// PromptTokensDetails / CompletionTokensDetails OpenAI 系的嵌套形状。
	PromptTokensDetails     *llmUsageDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *llmUsageDetails `json:"completion_tokens_details,omitempty"`

	// PromptCacheHitTokens / PromptCacheMissTokens DeepSeek 系的扁平别名。
	// hit 与 OpenAI 的 cached_tokens 同义；miss 就是「未命中」，**不是写入**（见 normalize）。
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens,omitempty"`

	// RawUsage 原始 usage JSON 文本，只用于「用量明细」弹窗底部的排障折叠区。
	// 非流式由 ExtractRawUsage 从响应体**原样**取出（保留我们没建模的字段）；
	// 流式从带 usage 的那一帧取。它不出现在任何请求体里。
	RawUsage string `json:"-"`
}

// llmUsageDetails OpenAI 系 usage 里的明细对象。
// prompt_tokens_details 与 completion_tokens_details 形状不同但字段不冲突，共用一个类型。
type llmUsageDetails struct {
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// normalize 把各家的嵌套 / 别名形状摊平成上面那组统一字段。
//
// 幂等：只做「先判断再赋值」，不做累加，重复调用结果不变。
// 只对 OpenAI 兼容路径有意义——Anthropic 走 llmUsageFromAnthropic，那边直接产出统一字段。
func (u *LLMUsage) Normalize() {
	if u == nil {
		return
	}
	// OpenAI：cached_tokens 藏在 prompt_tokens_details 里
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		u.CacheRead = u.PromptTokensDetails.CachedTokens
	}
	// DeepSeek：prompt_cache_hit_tokens 与 cached_tokens 同义。它更明确，优先级更高，
	// 有些网关两个都发且不一致时以它为准。
	if u.PromptCacheHitTokens > 0 {
		u.CacheRead = u.PromptCacheHitTokens
	}
	// ⚠️ PromptCacheMissTokens 刻意**不**映射到 CacheWrite：
	// 「未命中」是没走缓存的那部分输入，它不是写入，没有写入溢价。
	// 混为一谈会让成本倍数把未命中量也按 1.25x/2x 计，凭空变贵。
	// 未命中的量本来就是 PromptTokens − CacheRead，不需要单独存。
	if u.CompletionTokensDetails != nil && u.CompletionTokensDetails.ReasoningTokens > 0 {
		u.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
}

// ExtractRawUsage 从原始响应体里取出 usage 子对象（原样，不经过我们的结构体）。
//
// 为什么要「原样」：排障时「网关到底发了什么」比「我们解析出了什么」更有价值——
// 我们没建模的字段（各家自定义的缓存明细）恰恰是回答「这家到底有没有缓存」的关键。
// 解析失败或没有 usage 时返回空串，不报错：它只是锦上添花，不能影响主链路。
func ExtractRawUsage(body []byte) string {
	var probe struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || len(probe.Usage) == 0 {
		return ""
	}
	return string(probe.Usage)
}

type LLMResp struct {
	Choices []LLMChoice `json:"choices"`
	Created int64       `json:"created"`
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Object  string      `json:"object"`
	// Usage 用量。OpenAI 兼容响应直接映射进来；Anthropic 在适配器里映射。
	// 部分网关的流式响应不带 usage，此时保持零值——估算会退回上一锚点+增量。
	Usage LLMUsage `json:"usage"`
}

// LLMChoice LLM 选择项
type LLMChoice struct {
	FinishReason string     `json:"finish_reason"`
	Index        int        `json:"index"`
	Message      LLMMessage `json:"message"`
}

// ===== 协议常量 =====

const (
	FinishReasonStop      = "stop"
	FinishReasonLength    = "length"
	FinishReasonToolCalls = "tool_calls"

	ToolTypeFunction = "function"
)
