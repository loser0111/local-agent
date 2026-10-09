package llm

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

	"wails-tmp/internal/store"
)

// 本文件由 chat.go 拆出：OpenAI 兼容协议的 HTTP 调用（非流式 + SSE 流式）。

// llmStreamHeaderTimeout 流式请求等待响应头的上限。
// 只约束"网关多久开始回话"，不限制整体时长（长回复不能被砍断）。
const llmStreamHeaderTimeout = 120 * time.Second

// ===== LLM HTTP 调用 =====

// callLLM 调用 LLM API（OpenAI 兼容格式，非流式）。
// ctx 取运行 ctx：硬取消要能立刻断开在途请求。
func callLLM(ctx context.Context, url, token string, req *LLMReq) (*LLMResp, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化 LLM 请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	httpReq.Header.Set("Authorization", "Bearer "+token)

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
		return nil, NewLLMHTTPError(resp.StatusCode, body, resp.Header)
	}

	var llmResp LLMResp
	if err := json.Unmarshal(body, &llmResp); err != nil {
		return nil, fmt.Errorf("解析 LLM 响应失败: %w (body=%s)", err, string(body))
	}
	// 归一化 usage：把 cached_tokens / reasoning_tokens 这类嵌套形状摊平到统一字段。
	// 顺序在 tokenAnchor 之前——锚点必须建在**归一后**的 PromptTokens 上（见 LLMUsage 的不变式）。
	llmResp.Usage.Normalize()
	llmResp.Usage.RawUsage = ExtractRawUsage(body)
	return &llmResp, nil
}

// ===== LLM 流式调用（SSE，OpenAI 兼容）=====

// llmStreamChunk SSE 单个 data 帧
type llmStreamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	// Usage 部分网关只在最后一帧里带用量（OpenAI 需 stream_options.include_usage）。
	// 不带也不影响正确性：估算会退回「上一锚点 + 增量」。
	Usage *LLMUsage `json:"usage"`
	// Error 错误帧。OpenAI 系把错误也放在 data: 里发（HTTP 状态码仍是 200），
	// 所以只能靠显式识别这一帧来判断失败。
	Error *openAIStreamError `json:"error"`
}

// openAIStreamError OpenAI 系流内错误帧的内容。
// 三个字段都给出来是因为各家填哪个不一定：有的给 type、有的只给 code、有的只有 message。
type openAIStreamError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// callLLMStream 以 SSE 方式调用 LLM。
// 文本分片实时回调 onContent（由调用方节流后推送前端）；
// 工具调用的 arguments 分片在流内按 index 聚合，流结束后一次性返回完整 LLMResp，
// 使 executeChat 主循环无需感知流式/非流式差异。
func CallLLMStream(ctx context.Context, url, token string, req *LLMReq, onContent func(string)) (*LLMResp, error) {
	req.Stream = true
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化 LLM 请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")

	// 流式响应不能设置整体超时（长回复会被砍断），只约束建连与响应头。
	// 120s 而不是 30s：网关（尤其 coding-plan 这类代理）在 prompt 很长时要考虑一阵子
	// 才吐首字节，30s 会变成"timeout awaiting response headers"这类假失败。
	client := &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: llmStreamHeaderTimeout,
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
		return nil, NewLLMHTTPError(resp.StatusCode, body, resp.Header)
	}

	var (
		content     strings.Builder
		finish      string
		tcOrder     []int
		tcCalls     = map[int]*LLMToolCall{}
		tcArgs      = map[int]*strings.Builder{}
		sawDataLine = false
		badPayload  strings.Builder
		// streamUsage 流式用量：只有网关在帧里带了才有值
		streamUsage LLMUsage
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // 单行上限 4MB

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			// 200 但非 SSE 帧：收集起来作为格式错误上报
			badPayload.WriteString(line)
			continue
		}
		sawDataLine = true
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var chunk llmStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// 单个坏帧跳过，不中断整个流
			fmt.Printf("[chat] 跳过无法解析的 SSE 帧: %v\n", err)
			continue
		}
		// 错误帧必须显式拦下：它没有 choices，会被下面的处理当成"一帧什么都没说"
		// 静默跳过，最后表现为「LLM 返回空响应」——把一次限流误报成空回复，
		// 而且永远不会触发重试（这是重试层名副其实的漏网之鱼）。
		if chunk.Error != nil {
			typ := chunk.Error.Type
			if typ == "" {
				typ = chunk.Error.Code
			}
			return nil, LLMStreamError(typ, chunk.Error.Message)
		}
		// 用量只在最后一帧出现，取到就覆盖（它是整次请求的累计值）
		if chunk.Usage != nil && chunk.Usage.PromptTokens > 0 {
			streamUsage = *chunk.Usage
			// 流式这一帧就是唯一的原始来源：直接留原样文本，保证排障区看到的是网关真发的东西
			// （而非我们解析出的子集）。
			streamUsage.RawUsage = payload
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
				if onContent != nil {
					onContent(choice.Delta.Content)
				}
			}
			for _, dtc := range choice.Delta.ToolCalls {
				acc, ok := tcCalls[dtc.Index]
				if !ok {
					acc = &LLMToolCall{}
					tcCalls[dtc.Index] = acc
					args := &strings.Builder{}
					tcArgs[dtc.Index] = args
					tcOrder = append(tcOrder, dtc.Index)
				}
				if dtc.ID != "" {
					acc.ID = dtc.ID
					acc.Type = dtc.Type
					if acc.Type == "" {
						acc.Type = ToolTypeFunction
					}
					acc.Function.Name = dtc.Function.Name
				}
				if dtc.Function.Arguments != "" {
					tcArgs[dtc.Index].WriteString(dtc.Function.Arguments)
				}
			}
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取流式响应失败: %w", err)
	}
	if !sawDataLine {
		if badPayload.Len() > 0 {
			snippet := badPayload.String()
			if len(snippet) > 500 {
				snippet = snippet[:500] + "..."
			}
			return nil, fmt.Errorf("流式响应格式错误（服务端可能未开启 SSE）: %s", snippet)
		}
		return nil, fmt.Errorf("流式响应为空")
	}

	msg := LLMMessage{Role: store.RoleAssistant, Content: content.String()}
	if len(tcOrder) > 0 {
		calls := make([]LLMToolCall, 0, len(tcOrder))
		for _, idx := range tcOrder {
			acc := tcCalls[idx]
			if acc.Type == "" {
				acc.Type = ToolTypeFunction
			}
			acc.Function.Arguments = tcArgs[idx].String()
			calls = append(calls, *acc)
		}
		msg.ToolCalls = calls
		if finish == "" {
			finish = FinishReasonToolCalls
		}
	}

	// 归一化流式用量：与 callLLM 同一条规则，必须在返回前完成——
	// tokenAnchor 建的是 Usage.PromptTokens，锚在归一前的值上等于把缓存量算丢。
	streamUsage.Normalize()
	return &LLMResp{Choices: []LLMChoice{{FinishReason: finish, Message: msg}}, Usage: streamUsage}, nil
}
