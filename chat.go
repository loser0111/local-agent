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

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ===== LLM 请求数据结构（OpenAI 兼容格式）=====

// LLMMessage LLM API 的消息格式
type LLMMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []LLMToolCall `json:"tool_calls,omitempty"`
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
// 字段名统一用 OpenAI 的 prompt_tokens / completion_tokens，
// Anthropic 的 input_tokens / output_tokens 在解析时映射进来，全项目只用这一套名字。
//
// 有它才能把「纯字符估算」升级成「真实锚点 + 增量估算」——精度是数量级差别。
type LLMUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens,omitempty"`
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

// ChatResult 对话结果（返回给前端）
type ChatResult struct {
	Reply     string     `json:"reply"`               // AI 最终回复内容
	ToolCalls []ToolCall `json:"toolCalls,omitempty"` // 工具调用记录
	Messages  []Message  `json:"messages,omitempty"`  // 后端持久化的所有消息（assistant+tool_calls, tool结果, 最终回复）
	Diff      []DiffFile `json:"diff,omitempty"`      // 本轮对话产生的工作区差异
	Plan      *Plan      `json:"plan,omitempty"`      // 规划/执行流程返回时携带的计划
	Error     string     `json:"error,omitempty"`     // 错误信息

	// Cancelled 本轮是否被用户停止（区别于"失败"：取消不应触发自动重试，
	// 前端文案也不同——超时可以说"重试一下"，取消不该）。
	Cancelled  bool   `json:"cancelled,omitempty"`
	CancelKind string `json:"cancelKind,omitempty"` // soft | hard

	// Context 本轮结束时的上下文用量（前端据此显示用量指示；/compact 的提示也用它）
	Context *ContextStat `json:"context,omitempty"`
}

// ===== 常量 =====

const (
	RoleUser      = "user"
	RoleSystem    = "system"
	RoleAssistant = "assistant"
	RoleTool      = "tool"

	FinishReasonStop      = "stop"
	FinishReasonLength    = "length"
	FinishReasonToolCalls = "tool_calls"

	ToolTypeFunction = "function"

	MaxChatTurns = 50

	// llmStreamHeaderTimeout 流式请求等待响应头的上限。
	// 只约束"网关多久开始回话"，不限制整体时长（长回复不能被砍断）。
	llmStreamHeaderTimeout = 120 * time.Second

	SystemPrompt = "你是一个智能助手，可以调用工具来帮助用户解决问题。"
)

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
		return nil, fmt.Errorf("LLM 返回错误 (status=%d): %s", resp.StatusCode, string(body))
	}

	var llmResp LLMResp
	if err := json.Unmarshal(body, &llmResp); err != nil {
		return nil, fmt.Errorf("解析 LLM 响应失败: %w (body=%s)", err, string(body))
	}
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
}

// callLLMStream 以 SSE 方式调用 LLM。
// 文本分片实时回调 onContent（由调用方节流后推送前端）；
// 工具调用的 arguments 分片在流内按 index 聚合，流结束后一次性返回完整 LLMResp，
// 使 executeChat 主循环无需感知流式/非流式差异。
func callLLMStream(ctx context.Context, url, token string, req *LLMReq, onContent func(string)) (*LLMResp, error) {
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
		return nil, fmt.Errorf("LLM 返回错误 (status=%d): %s", resp.StatusCode, string(body))
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
		// 用量只在最后一帧出现，取到就覆盖（它是整次请求的累计值）
		if chunk.Usage != nil && chunk.Usage.PromptTokens > 0 {
			streamUsage = *chunk.Usage
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

	msg := LLMMessage{Role: RoleAssistant, Content: content.String()}
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

	return &LLMResp{Choices: []LLMChoice{{FinishReason: finish, Message: msg}}, Usage: streamUsage}, nil
}

// startDeltaFlusher 启动文本分片节流推送器：
// 首个分片立即推送，之后 50ms 或累计 ≥20 字符合并推送一次，避免高频 IPC。
// 返回的 enqueue 非阻塞入队；调用方在流结束后调 shutdown 等待残余分片发完。
func (a *App) startDeltaFlusher() (enqueue func(string), shutdown func()) {
	ch := make(chan string, 256)
	done := make(chan struct{})

	go func() {
		var sb strings.Builder
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		flush := func() {
			if sb.Len() == 0 || a.ctx == nil {
				sb.Reset()
				return
			}
			wailsRuntime.EventsEmit(a.ctx, "chat:event", ChatEvent{
				Type:  "reply_delta",
				Reply: sb.String(),
			})
			sb.Reset()
		}

		for {
			select {
			case c, ok := <-ch:
				if !ok {
					flush()
					close(done)
					return
				}
				if sb.Len() == 0 {
					sb.WriteString(c)
					flush() // 首分片立即推送
				} else {
					sb.WriteString(c)
					if sb.Len() >= 20 {
						flush()
					}
				}
			case <-ticker.C:
				flush()
			}
		}
	}()

	enqueue = func(c string) {
		select {
		case ch <- c:
		default: // 背压极端情况下丢弃单分片也不阻塞 LLM 读取（文本已在后端聚合）
		}
	}
	shutdown = func() {
		close(ch)
		<-done
	}
	return enqueue, shutdown
}

// ===== 对话流程（借鉴 01agent 的状态机，简化为循环）=====

// ChatEvent 推送给前端的事件数据
// ChatEvent 聊天过程事件（经 "chat:event" 通道推送给前端）
type ChatEvent struct {
	Type      string     `json:"type"` // tool_call_start / tool_call_end / reply_delta / done / error / diff_update / plan_update / permission_request / ask_user
	ToolCall  *ToolCall  `json:"toolCall,omitempty"`
	Reply     string     `json:"reply,omitempty"`
	Error     string     `json:"error,omitempty"`
	Diff      []DiffFile `json:"diff,omitempty"`      // diff_update 事件携带的差异文件
	Turn      int        `json:"turn,omitempty"`      // diff 所属轮次
	Plan      *Plan      `json:"plan,omitempty"`      // plan_update 事件全量携带最新计划
	StepIndex int        `json:"stepIndex,omitempty"` // plan_update 触发步骤索引（计划级变更为 -1，omitempty 时不下发）
	// Permission 权限授权请求（type=permission_request）：前端应弹出授权弹窗，
	// 并把结果经 ResolvePermission 回传。请求期间后端阻塞等待，超时/取消一律按拒绝处理。
	Permission *PermissionAskRequest `json:"permission,omitempty"`
	// Ask 模型主动提问（type=ask_user）：前端应弹出提问弹窗，用户作答后经 ResolveAskUser 回传。
	// 等待期间后端阻塞；超时/取消会以"用户未作答"回给模型，让它自行决策而不是让整轮失败。
	Ask *AskRequest `json:"ask,omitempty"`
	// Compact 自动摘要压缩的产物（type=context_compacted）。
	// 压缩会让用量**突然下降**，这是设计内行为；但用户只看到数字掉了一半，
	// 很容易以为对话被截断了。所以压缩一发生就主动推一次，把"原文没丢"讲清楚。
	Compact *CompactOutcome `json:"compact,omitempty"`
	// Context 事件发生后的上下文用量。前端据此就地刷新指示，
	// 不必等这一轮结束的 ChatResult 回来。
	Context *ContextStat `json:"context,omitempty"`
}

// 事件类型常量（前端按同名分支分发）
const (
	ChatEventToolCallStart = "tool_call_start"
	ChatEventToolCallEnd   = "tool_call_end"
	ChatEventReplyDelta    = "reply_delta"
	ChatEventPermission    = "permission_request"
	// ChatEventCancelled 本轮被用户停止（软取消或硬取消）。前端据此把流式气泡
	// 收尾成"已停止"，而不是留着它永远转圈。
	ChatEventCancelled = "cancelled"
	// ChatEventContextCompacted 自动摘要压缩已生效（事件携带压缩结果与新的用量）。
	// 前端用它弹一次提示：用量下降是压缩造成的，原文仍在会话记录里。
	ChatEventContextCompacted = "context_compacted"
	// ChatEventPermissionExpired / ChatEventAskExpired 后端已终结的挂起请求（超时、
	// 取消或跳过）。前端据此关掉对应弹窗：pending 状态只认"已答复/已终结"两个方向，
	// 若只发请求不发终结通知，弹窗会在超时后永久悬挂（悬空全屏遮罩挡住整个应用）。
	ChatEventPermissionExpired = "permission_expired"
	ChatEventAskExpired        = "ask_expired"
)

// emitChatEvent 向前端推送一个聊天事件（无 Wails 上下文时静默跳过）
// EventUserInteraction 需要用户交互的阻塞式请求（授权 / 提问）专用事件名。
//
// 为什么不复用 "chat:event"：前端每次调用结束都会 EventsOff("chat:event")，而 Wails 的
// EventsOff 会移除该事件的**全部**监听——于是这类"无论从哪条链路发起都要能弹出来"的请求，
// 一旦挂在按调用注册的回调上就会漏（曾经就漏了计划执行路径：授权弹窗没弹，后端白等 5 分钟
// 才超时失败）。独立通道让前端只需在应用根部订阅一次，新增任何调用入口都不会再漏。
const EventUserInteraction = "user:interaction"

// emitInteraction 推送需要用户应答的交互事件。
//
// **同时发到两条通道**，是为了对「前端产物与 Go 二进制版本不一致」免疫：
// main.go 用 go:embed 把 frontend/dist 打进二进制，只要构建顺序不当（先 build Go 后 build 前端，
// 或只重建了一侧），就会出现"后端在新通道喊、前端在旧通道听"（或反之），
// 表现为弹窗永不出现、工具卡片一直卡在"运行中"直到超时。
// 两条通道都发，新前端听 user:interaction、旧前端听 chat:event，各自只收到一次。
func (a *App) emitInteraction(ev ChatEvent) {
	if a.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(a.ctx, EventUserInteraction, ev)
	wailsRuntime.EventsEmit(a.ctx, "chat:event", ev) // 兼容旧前端产物
	// 打一行日志：下次界面没弹出时，看控制台就能判断是"后端没发"还是"前端没收到"
	fmt.Printf("[交互] 已推送 %s，等待界面应答（若弹窗没出现，请确认前端产物与二进制来自同一次构建）\n", ev.Type)
}

func (a *App) emitChatEvent(ev ChatEvent) {
	if a.ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(a.ctx, "chat:event", ev)
}

// buildLLMMessages 从历史消息构造 LLM messages 数组
// 正确重建消息序列：assistant(tool_calls) → tool(tool_call_id) → assistant(最终回复)
// systemPrompt 由调用方拼接（基础人设 + L1 技能清单 + 强制注入正文）
// 约定：当前这条用户输入（普通聊天由前端、计划步骤由执行器）已持久化在 history 末尾，
// 此处不再重复追加，避免同一句话在模型上下文中出现两次。
func buildLLMMessages(history []Message, systemPrompt string) []LLMMessage {
	messages := []LLMMessage{
		{Role: RoleSystem, Content: systemPrompt},
	}

	// 添加历史消息
	for _, msg := range history {
		m := LLMMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
		// tool 角色消息：携带 tool_call_id
		if msg.ToolCallID != "" {
			m.ToolCallID = msg.ToolCallID
		}
		// assistant 角色消息：转换工具调用记录为 LLM tool_calls 格式
		if len(msg.ToolCalls) > 0 {
			calls := make([]LLMToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				argsBytes, _ := json.Marshal(tc.Args)
				calls = append(calls, LLMToolCall{
					ID:   tc.ID,
					Type: ToolTypeFunction,
					Function: LLMToolFunction{
						Name:      tc.Name,
						Arguments: string(argsBytes),
					},
				})
			}
			m.ToolCalls = calls
		}
		messages = append(messages, m)
	}

	return messages
}

// executeChat 执行完整的多轮对话流程（普通聊天入口）
// 借鉴 01agent 的状态机设计：Preprocessing → LLM Call → Judge → Tool Execute → Rebuild → 循环
// 持久化所有中间消息（assistant+tool_calls, tool结果, 最终回复），确保下次对话时消息序列完整
func (a *App) executeChat(sessionID, query string, useStream bool) *ChatResult {
	session, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("加载会话失败: %v", err)}
	}
	model, err := a.modelStore.GetModelForCall(session.Model)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("获取模型配置失败: %v", err)}
	}
	dir, _ := a.resolveProjectDir(sessionID)
	prompt, err := a.buildBasePromptWithSkill(session, dir, query)
	if err != nil {
		return &ChatResult{Error: err.Error()}
	}
	// 登记为可取消的运行：前端「停止」经 StopChat 命中它（软取消 / 硬取消）
	run := a.runs.begin(sessionID, "")
	defer a.runs.end(run)
	return runToolLoop(a.newMainAgentRun(run, session, dir, prompt, &model, useStream, false))
}

// buildBasePrompt 组装基础系统提示词：基础人设 + 工作区说明 + 技能上下文（L1 清单 / 强制注入正文）
// dir 为会话工作区目录（为空串时不追加工作区段）
func (a *App) buildBasePrompt(session *Session, dir string) string {
	prompt := SystemPrompt
	// 工作区说明：告知模型当前会话绑定的工作目录（未设置时为进程工作目录）
	if dir != "" {
		prompt += fmt.Sprintf("\n\n## 工作区\n"+
			"当前会话绑定的工作区目录为：%s\n"+
			"涉及文件读写、目录操作或运行命令时，若未指定绝对路径，默认应基于此目录（相对路径均相对于该目录）。", dir)
	}
	prompt += "\n\n## 工具使用偏好\n" +
		"读写文件请用 read_file / write_file / edit_file（相对路径相对于工作区目录）；" +
		"查找文件用 glob、搜索内容用 grep、查看目录用 list_dir。这些工具要么只读、要么按路径精确操作，" +
		"比 shell 命令更安全，也不会被 shell 语法（引号、重定向、命令替换）影响。\n" +
		"exec_shell 留给构建、测试、git 等真正的命令；不要用它做 cat / sed -i / echo > 这类文件读写。\n" +
		"遇到会产生大量中间过程、且与当前主线关系不大的任务（把一个模块摸清楚、独立调研一个问题），" +
		"可以用 spawn_agent 派一个子代理去做：它有自己独立的上下文，你只收到一份结论，" +
		"那些中间过程不会占用你的上下文。一两步就能做完的事不必派。"

	// 效率约定：引导模型一轮批量调用、避免重复读数与无谓的工具发现往返。
	// 执行层本就支持一轮返回多个 tool_calls（runToolLoop 串行执行、整体只算一轮），
	// 但此前的提示词从未提及，模型只会一个一个来，探索类任务因此多耗一倍以上轮次。
	prompt += "\n\n## 效率约定\n" +
		"轮次很宝贵，请用下面这些方式减少往返：\n" +
		"1. 一轮可以发起多个互不依赖的工具调用：在同一次响应里返回多个 tool_call，它们只算一轮、按先后顺序执行。" +
		"调研阶段（读多个文件、搜多处代码、列目录）请尽量合并到同一轮，不要一个个来。\n" +
		"2. 有副作用的操作（write_file / edit_file / exec_shell）一次只发一个，确认结果无误再发下一个：" +
		"批量发出会让权限确认连续弹窗，出错了也更难定位是哪一个。\n" +
		"3. 同一个文件不要反复读：读完记住内容，要改就直接 edit_file；" +
		"已经确认过的命令也不要再跑一遍去'再验证'。\n" +
		"4. 先用 glob / grep 定位到具体文件与行，再 read_file 精读；不要盲目通读整个目录。\n" +
		"5. 上面「工具使用偏好」里已点名的工具都可直接调用，不必先经 tool_router 去 list / describe；" +
		"只有不确定有哪些工具、或不确定某工具的完整参数时，才用 tool_router 发现。"

	if a.skillStore != nil {
		enabled := a.enabledSkillsForSession(session)
		if idx := BuildSkillIndex(enabled); idx != "" {
			prompt += "\n\n## 可用技能（Skills）\n" +
				"当请求与下列技能的描述匹配时，先调用 read_skill 取回该技能的完整说明，再按说明执行；" +
				"技能自带的 references/ 文档用 read_skill_file 读取，scripts/ 下的脚本用 exec_shell 执行：\n" + idx
		}
	}
	return prompt
}

// buildBasePromptWithSkill 在基础提示词上叠加「显式调用技能」段。
// 用户在消息开头写 /技能名 时，该技能正文直接进入本轮 system prompt：
// 是否使用该技能由用户决定，不再经模型路由（这正是 disable-model-invocation 的语义）。
// 未命中技能时按普通消息处理（例如消息其实是一条以 / 开头的路径）。
func (a *App) buildBasePromptWithSkill(session *Session, dir, query string) (string, error) {
	prompt := a.buildBasePrompt(session, dir)
	if a.skillStore == nil {
		return prompt, nil
	}
	res := a.skillStore.ResolveSkillCommand(query)
	if res.Reason != "" {
		return "", fmt.Errorf("%s", res.Reason)
	}
	if !res.Ok || res.Skill == nil {
		return prompt, nil
	}
	// 会话白名单同样约束显式调用：本会话没开放的技能不该被 /名称 绕过
	if !a.skillAllowedInSession(session, res.Skill.ID) {
		return "", fmt.Errorf("技能 %s 未在本会话的可用技能中", res.Skill.ID)
	}
	body, err := a.skillStore.LoadBody(res.Skill.ID)
	if err != nil {
		return "", err
	}
	return prompt + BuildExplicitSkillBlock(res.Skill, body), nil
}

// skillAllowedInSession 会话技能白名单是否放行该技能（白名单为空=全部放行）
func (a *App) skillAllowedInSession(session *Session, id string) bool {
	if len(session.EnabledSkills) == 0 {
		return true
	}
	for _, s := range session.EnabledSkills {
		if s == id {
			return true
		}
	}
	return false
}

// runToolLoop 执行一次「构建请求→LLM→工具循环」的完整运行。
//
// 普通聊天、计划步骤执行、以及将来的子代理共用的**唯一**执行引擎。
// 它只依赖 agentRun 携带的依赖、不直接引用 *App，所以是自由函数而不是方法——
// 编译通过本身就是这层解耦的证明（见 agentrun.go）。
//
// 调用方负责：组装 systemPrompt、装配工具视图与权限网关、把当前用户消息持久化进会话历史。
// ar.Compact=true 时对历史消息做确定性截断（降级路径与调试开关）。
// 软取消在每轮工具循环开始前检查；硬取消由 ar.Control 的 ctx 贯穿到在途请求与子进程。
func runToolLoop(ar *agentRun) *ChatResult {
	if ar == nil || ar.Store == nil {
		return &ChatResult{Error: "运行上下文缺失"}
	}
	// 把运行参数取成局部变量：循环主体因此几乎不用改，
	// diff 里只剩「依赖从哪里来」这一件事变了，便于逐行复核。
	run := ar.Control
	if run == nil {
		run = (&runRegistry{}).begin(ar.SessionID, ar.PlanID)
	}
	sessionID := ar.SessionID
	systemPrompt := ar.SystemPrompt
	model := ar.Model
	useStream, compact := ar.Stream, ar.Compact
	dir := ar.Dir
	isRepo := ar.IsRepo
	maxTurns := ar.MaxTurns
	if maxTurns <= 0 {
		maxTurns = MaxChatTurns
	}

	// 1. 重新加载会话：确保包含调用方刚持久化的用户消息以及此前全部消息（跨步骤上下文连续）
	session, err := ar.Store.GetSession(sessionID)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("加载会话失败: %v", err)}
	}

	// 2. 确定模型 ID（优先用 ModelID，为空则用 Name）
	modelID := model.ModelID
	if modelID == "" {
		modelID = model.Name
	}

	// ★ 记录本轮工作区基线（对话开始前的快照，用于轮末计算本轮 diff）
	// dir 与 isRepo 由调用方给出（见 agentRun），这里不再自行解析
	turnBase := ""
	// turnCheckpoint 撤销用的完整快照。与 turnBase 刻意分开、各取一份：
	// turnBase（stash create）不含未跟踪文件、用于算 diff；这份含未跟踪文件、用于回退。
	// 合并会带来两个问题：diff 会把未跟踪文件重复计算；回退会误删用户手写的未跟踪文件。
	turnCheckpoint := ""
	if isRepo {
		// 先恢复会话里持久化的 diff 状态（基线与已归因的路径），重启后仍能算会话级 diff
		ar.Diff.RestoreSession(sessionID, session.DiffBaseline, session.DiffTouched)
		ar.Diff.EnsureBaseline(sessionID, dir)
		ar.Diff.BeginTurn(sessionID)
		turnBase = ar.Diff.TurnSnapshot(dir)

		// 取不到不算错误：本轮只是不可回退，对话照常进行
		if cp, cpErr := Checkpoint(dir, sessionID, nextDiffTurn(session)); cpErr == nil {
			turnCheckpoint = cp
			PruneCheckpoints(dir, sessionID, checkpointKeepTurns)
		} else {
			fmt.Printf("[checkpoint] 本轮快照失败，该轮不可回退: %v\n", cpErr)
		}
	}

	// 3. 组装发给模型的消息序列：摘要前缀替换（若会话有生效摘要）+ 单条工具结果预算。
	//    compact=true 是降级/调试开关，强制走确定性截断，不是常规路径。
	messages := buildRunMessages(session, systemPrompt, compact)

	// 4. 工具视图由调用方装配（见 newMainAgentRun）。
	// 这里是子代理唯一需要与主会话不同的地方——它要用自己的权限网关与工具子集，
	// 所以装配点必须留在调用方，循环只管拿来用。
	toolView := ar.ToolView
	tools := toolView.GetToolsForLLM()

	// 5. 上下文用量的基准量：窗口大小、工具定义的固定开销、用量锚点。
	//    锚点由响应里回传的真实 input token 建立，把估算误差限制在两条锚点之间。
	window := contextWindowOf(model)
	toolTokens := toolSchemaTokens(tools)
	// 估算校准系数：本次运行全程不变（按会话模型查一次）。ar.TokenCalib 为 nil
	// （子代理，以及只手工构造了 agentRun 的测试）时按 1.0，即退化成纯字符估算。
	calib := 1.0
	if ar.TokenCalib != nil {
		calib = ar.TokenCalib.Ratio(session.Model)
	}
	var anchor *tokenAnchor
	var ctxStat *ContextStat
	overflowRetried := false
	// summaryBroken 摘要器坏掉（调用失败/结果异常）后置位：本run之内不再尝试摘要式压缩。
	// 否则每一轮都要白白付一次失败的 LLM 调用，而确定性截断本来就能兜住。
	summaryBroken := false

	// 6. 工具调用循环
	var toolCallRecords []ToolCall
	var persistedMsgs []Message // 本次运行持久化的所有消息

	for turn := 0; turn < maxTurns; turn++ {
		// 软取消：当前 LLM 调用与工具执行跑完，下一轮循环不再开始。
		//
		// 刻意**不在循环中途掐断**：本轮所有工具都会走完（硬取消时它们会因 ctx 已取消
		// 而立即失败），随后 assistant 消息与全部 tool 结果一起落库，保证
		// tool_calls 与 tool 消息一一配对。若在工具执行到一半直接返回，
		// 会留下断裂的消息序列——它不会当次报错，而是在下一次构建请求时被 API 拒绝。
		if run.SoftRequested() {
			ar.Recorder.Emit(ChatEvent{Type: ChatEventCancelled, Error: "执行已取消"})
			return &ChatResult{
				Error:      "执行已取消",
				ToolCalls:  toolCallRecords,
				Messages:   persistedMsgs,
				Cancelled:  true,
				CancelKind: run.Kind(),
			}
		}

		// 上下文接近窗口就先压缩再发请求——这是"敢让 agent 跑长任务"的关键一步。
		// 压缩改的是存储（会话三个摘要字段），所以序列必须跟着重建、用量锚点必须作废。
		// 压缩失败不阻断本轮：compactSession 内部已降级，这里只记一笔日志。
		ctxStat = contextStatOf(session, messages, window, anchor, toolTokens, calib)
		compactedThisTurn := false
		if ar.Compactor != nil && needCompact(ctxStat.UsedTokens, window) && !summaryBroken && !compact {
			out, cerr := ar.Compactor(run.Ctx(), session, model)
			switch {
			case cerr != nil:
				fmt.Printf("[context] 压缩出错（本轮继续，不阻断对话）: %v\n", cerr)
				summaryBroken = true
			case out.Degraded:
				fmt.Printf("[context] 摘要不可用，本run改用确定性截断: %s\n", out.Reason)
				summaryBroken = true
				messages = buildRunMessages(session, systemPrompt, true)
				ctxStat = contextStatOf(session, messages, window, nil, toolTokens, calib)
			case !out.Compressed:
				fmt.Printf("[context] 未压缩: %s\n", out.Reason)
			default:
				if s2, e2 := ar.Store.GetSession(sessionID); e2 == nil {
					session = s2
					messages = buildRunMessages(session, systemPrompt, compact)
					anchor = nil
					ctxStat = contextStatOf(session, messages, window, nil, toolTokens, calib)
					compactedThisTurn = true
					// 主动告知：前缀被换成摘要之后用量会立刻下降。走事件而不落库——
					// 它是解释而不是对话内容，不该在会话记录里留一串噪声。
					ar.Recorder.Emit(ChatEvent{
						Type:    ChatEventContextCompacted,
						Compact: out,
						Context: ctxStat,
					})
				}
			}
		}

		// 记录**真实发出的那一份**（摘要替换 + 工具结果预算之后），供界面排障查看。
		// 快照内部做浅拷贝；这里不阻断任何逻辑，失败了也不影响对话。
		ar.ReqLog.record(snapshotLLMRequest(run, session, turn, modelID, systemPrompt, messages, tools, ctxStat, compactedThisTurn))

		req := &LLMReq{
			Model:       modelID,
			Messages:    messages,
			Temperature: 1.0,
			Stream:      useStream,
			Tools:       tools,
		}

		// 流式与非流式在聚合后返回结构相同，主循环无需分叉
		var (
			resp          *LLMResp
			flusher       func(string)
			flushShutdown func()
		)
		if useStream {
			flusher, flushShutdown = ar.Recorder.FlushStream()
			resp, err = callLLMStreamForModel(run.Ctx(), model, req, flusher)
			flushShutdown() // 确保残余分片在进入工具执行/done 前全部发出
		} else {
			resp, err = callLLMForModel(run.Ctx(), model, req)
		}
		if err != nil {
			// 硬取消会让在途请求以 "context canceled" 失败：按"已取消"返回，
			// 而不是当成一次真实失败——前端对这两者的处理不同（取消不该提示重试）。
			if run.Kind() == cancelKindHard {
				ar.Recorder.Emit(ChatEvent{Type: ChatEventCancelled, Error: "执行已取消"})
				return &ChatResult{
					Error:      "执行已取消",
					ToolCalls:  toolCallRecords,
					Messages:   persistedMsgs,
					Cancelled:  true,
					CancelKind: cancelKindHard,
				}
			}
			// 上下文超限：这是压缩之外的最后一道防线（被动触发，说明前面的
			// 主动阈值判断没兜住——比如窗口配得比真实值大）。只试一次，
			// 免得和网络类错误混在一起反复重试。
			if !overflowRetried && isContextOverflowError(err) {
				overflowRetried = true
				if !summaryBroken && !compact {
					out, cerr := ar.Compactor(run.Ctx(), session, model)
					if cerr == nil && out.Compressed {
						if s2, e2 := ar.Store.GetSession(sessionID); e2 == nil {
							session = s2
							messages = buildRunMessages(session, systemPrompt, compact)
							anchor = nil
							fmt.Printf("[context] 上下文超限，已摘要压缩（覆盖 %d 条消息）后重试\n", out.CoveredMsgs)
							// 超限才压缩说明前面的阈值判断没兜住，用户更该知道
							// 这次"用量突然下降"是怎么来的——同样只发事件不落库。
							ctxStat = contextStatOf(session, messages, window, nil, toolTokens, calib)
							ar.Recorder.Emit(ChatEvent{
								Type:    ChatEventContextCompacted,
								Compact: out,
								Context: ctxStat,
							})
							continue
						}
					}
					if out.Degraded {
						summaryBroken = true
					}
				}
				// 摘要压不出来（消息太少 / 摘要器不可用）：退回确定性截断再试一次
				messages = buildRunMessages(session, systemPrompt, true)
				anchor = nil
				fmt.Printf("[context] 上下文超限，改用确定性截断后重试\n")
				continue
			}

			return &ChatResult{
				Error:     err.Error(),
				ToolCalls: toolCallRecords,
				Messages:  persistedMsgs,
			}
		}

		if len(resp.Choices) == 0 {
			return &ChatResult{
				Error:     "LLM 返回空响应",
				ToolCalls: toolCallRecords,
				Messages:  persistedMsgs,
			}
		}

		// 响应里带回真实用量就更新锚点：后续估算以它为基准，两条锚点之间的误差不累积。
		// 这是整套估算敢用粗略字符系数的前提。
		if resp.Usage.PromptTokens > 0 {
			anchor = &tokenAnchor{InputTokens: resp.Usage.PromptTokens, MsgCount: len(messages)}

			// 顺手做一次校准观测：同一段序列，真实用量与字符估算此刻都在手上。
			// 估算那侧刻意用**未缩放**的口径（estimateSeqTokens 不乘 calib），否则观测值
			// 会被现有系数拉向 1.0，校准自己把自己抹平。
			if ar.TokenCalib != nil {
				ar.TokenCalib.Observe(session.Model, resp.Usage.PromptTokens,
					estimateSeqTokens(messages, nil)+toolTokens)
			}
		}

		choice := resp.Choices[0]

		// 判断是否需要工具调用
		if choice.FinishReason == FinishReasonToolCalls && len(choice.Message.ToolCalls) > 0 {
			// === 持久化 assistant 消息（含 tool_calls 记录）===
			// 先执行所有工具，收集执行记录，再持久化 assistant 消息
			var executedToolCalls []ToolCall

			for _, tc := range choice.Message.ToolCalls {
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

				startTime := time.Now()

				// 推送工具调用开始事件
				toolCallRecord := ToolCall{
					ID:     tc.ID,
					Name:   tc.Function.Name,
					Args:   args,
					Status: "running",
				}
				ar.Recorder.Emit(ChatEvent{
					Type:     ChatEventToolCallStart,
					ToolCall: &toolCallRecord,
				})

				// 执行工具（记录改动游标，执行后取本次调用改了哪些文件）
				mark := ar.Changes.Mark(sessionID)
				// 前后各扫一次工作区：执行窗口内的改动归因给本会话（含 exec_shell 改的文件）
				ar.Diff.NoteActivity(sessionID, dir, false)
				// 执行工具：必须传运行 ctx —— 硬取消靠它切断权限等待、子进程与在途请求
				result, execErr := toolView.ExecuteToolCtx(run.Ctx(), tc.Function.Name, args)
				ar.Diff.NoteActivity(sessionID, dir, true)
				// 子代理在本次调用期间改的文件不该算作本会话的改动（见 runSubagent 与
				// runControl.noteExcludedPaths）。剔除必须放在这里——归因是上面这一句
				// 按时间窗口扫全工作区写进去的，早一步剔除就会被重新记上。
				if excluded := run.takeExcludedPaths(sessionID); len(excluded) > 0 {
					ar.Diff.DropTouched(sessionID, excluded)
				}
				duration := time.Since(startTime).Seconds()
				if changed := ar.Changes.Since(sessionID, mark); len(changed) > 0 {
					files := make([]string, 0, len(changed))
					for _, c := range changed {
						files = append(files, c.Rel)
					}
					toolCallRecord.Files = files
				}

				if execErr != nil {
					toolCallRecord.Status = "error"
					toolCallRecord.Duration = duration
					if run.Kind() == cancelKindHard {
						// 硬取消会让未跑完的工具统一报 "context canceled"，
						// 换成可读说明——工具卡片与审计记录都要看得懂
						toolCallRecord.Result = "已取消：运行被用户停止"
					} else {
						toolCallRecord.Result = fmt.Sprintf("执行失败: %v", execErr)
					}
				} else {
					toolCallRecord.Status = "success"
					toolCallRecord.Duration = duration
					toolCallRecord.Result = result
				}
				toolCallRecords = append(toolCallRecords, toolCallRecord)
				executedToolCalls = append(executedToolCalls, toolCallRecord)

				// 推送工具调用完成事件
				ar.Recorder.Emit(ChatEvent{
					Type:     ChatEventToolCallEnd,
					ToolCall: &toolCallRecord,
				})
			}

			// 持久化 assistant 消息（含 tool_calls 执行记录）
			assistantMsg := Message{
				Role:      RoleAssistant,
				Content:   choice.Message.Content, // LLM 可能返回空内容
				ToolCalls: executedToolCalls,
			}
			savedAssistant, err := ar.Store.AppendMessage(sessionID, assistantMsg)
			if err != nil {
				return &ChatResult{
					Error:     fmt.Sprintf("持久化 assistant 消息失败: %v", err),
					ToolCalls: toolCallRecords,
					Messages:  persistedMsgs,
				}
			}
			persistedMsgs = append(persistedMsgs, *savedAssistant)
			// 同步到内存消息列表（用于后续 LLM 调用）
			messages = append(messages, LLMMessage{
				Role:      RoleAssistant,
				Content:   choice.Message.Content,
				ToolCalls: choice.Message.ToolCalls,
			})

			// 持久化每个工具结果消息（role=tool, tool_call_id）
			for _, tc := range choice.Message.ToolCalls {
				// 从执行记录中找到对应结果
				var toolContent string
				for _, rec := range executedToolCalls {
					if rec.ID == tc.ID {
						toolContent = rec.Result
						break
					}
				}

				toolMsg := Message{
					Role:       RoleTool,
					Content:    toolContent,
					ToolCallID: tc.ID,
				}
				savedTool, err := ar.Store.AppendMessage(sessionID, toolMsg)
				if err != nil {
					return &ChatResult{
						Error:     fmt.Sprintf("持久化 tool 消息失败: %v", err),
						ToolCalls: toolCallRecords,
						Messages:  persistedMsgs,
					}
				}
				persistedMsgs = append(persistedMsgs, *savedTool)
				// 同步到内存消息列表
				messages = append(messages, LLMMessage{
					Role:       RoleTool,
					Content:    toolContent,
					ToolCallID: tc.ID,
				})
			}

			// 继续循环，重新调用 LLM（带工具结果）
			continue
		}

		// === finish_reason == "stop"，持久化最终回复 ===
		finalMsg := Message{
			Role:    RoleAssistant,
			Content: choice.Message.Content,
		}
		savedFinal, err := ar.Store.AppendMessage(sessionID, finalMsg)
		if err != nil {
			return &ChatResult{
				Error:     fmt.Sprintf("持久化最终回复失败: %v", err),
				ToolCalls: toolCallRecords,
				Messages:  persistedMsgs,
			}
		}
		persistedMsgs = append(persistedMsgs, *savedFinal)

		// ★ 计算本轮 diff：只统计本会话在本轮触碰过的路径（会话级隔离）
		var diffFiles []DiffFile
		if isRepo {
			turnPaths := ar.Diff.TurnTouched(sessionID)
			if files, err := ar.Diff.DiffScoped(dir, turnBase, turnPaths); err == nil && len(files) > 0 {
				diffFiles = files
				base, touched := ar.Diff.SnapshotState(sessionID)
				turnNo, appendErr := ar.Store.AppendDiff(sessionID, DiffTurn{
					Files:     files,
					Additions: sumAdd(files),
					Deletions: sumDel(files),
					CreatedAt: time.Now().UnixMilli(),
					// 撤销所需的两项：本轮开始时的完整快照 + 本轮结束时各文件的内容状态
					Base:     turnCheckpoint,
					EndState: endStateOf(dir, files),
				}, base, touched)
				if appendErr == nil {
					ar.Recorder.EmitDiff(files, turnNo)
				}
			}
		}

		result := &ChatResult{
			Reply:     choice.Message.Content,
			ToolCalls: toolCallRecords,
			Messages:  persistedMsgs,
			Diff:      diffFiles,
			Context:   ctxStat,
		}

		// 推送完成事件
		ar.Recorder.Emit(ChatEvent{
			Type:  "done",
			Reply: result.Reply,
		})

		return result
	}

	return &ChatResult{
		Error:     "超过最大对话轮次限制",
		ToolCalls: toolCallRecords,
		Messages:  persistedMsgs,
	}
}

// ===== Plan & Execute（规划-执行模式）=====

// emitPlanUpdate 推送计划状态变更事件（plan_update，全量携带最新计划）
func (a *App) emitPlanUpdate(plan *Plan, stepIndex int) {
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "chat:event", ChatEvent{
			Type:      "plan_update",
			Plan:      plan,
			StepIndex: stepIndex,
		})
	}
}

// ChatPlan 规划流程：调用规划器产出结构化计划（或平凡请求直答）。
// 用户消息已由前端持久化，此处不再落库；规划器过程消息不持久化（对用户无信息量）。
func (a *App) ChatPlan(sessionID, query string, useStream bool) *ChatResult {
	// 1. 会话与模型解析（与 executeChat 一致）
	session, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("加载会话失败: %v", err)}
	}
	model, err := a.modelStore.GetModelForCall(session.Model)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("获取模型配置失败: %v", err)}
	}
	modelID := model.ModelID
	if modelID == "" {
		modelID = model.Name
	}
	dir, _ := a.resolveProjectDir(sessionID)

	// 登记为可取消的运行：规划期的 LLM 调用同样要能被停止
	// （硬取消会断开在途的规划请求，而不是让用户白等一次 120s 超时）
	run := a.runs.begin(sessionID, "")
	defer a.runs.end(run)

	// 2. 规划器调用：无工具、非流式、低温；失败重试一次
	var resp *LLMResp
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if run.SoftRequested() {
			return &ChatResult{Error: "执行已取消", Cancelled: true, CancelKind: run.Kind()}
		}
		req := &LLMReq{
			Model:       modelID,
			Temperature: 0.2,
			Messages: []LLMMessage{
				{Role: RoleSystem, Content: plannerSystemPrompt},
				{Role: RoleUser, Content: buildPlannerUserPrompt(query, dir)},
			},
		}
		resp, lastErr = callLLMForModel(run.Ctx(), &model, req)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return &ChatResult{Error: fmt.Sprintf("规划失败: %v", lastErr)}
	}
	if len(resp.Choices) == 0 {
		return &ChatResult{Error: "规划器返回空响应"}
	}

	// 3. 解析规划输出
	d, err := parsePlanDraft(resp.Choices[0].Message.Content)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("规划失败: %v", err)}
	}

	// 4. 平凡请求免计划（G8）：走普通工具循环直答
	if !d.NeedPlan {
		prompt, err := a.buildBasePromptWithSkill(session, dir, query)
		if err != nil {
			return &ChatResult{Error: err.Error()}
		}
		return runToolLoop(a.newMainAgentRun(run, session, dir, prompt, &model, useStream, false))
	}

	// 5. 组装计划并落盘（待审核状态）
	now := time.Now().UnixMilli()
	plan := &Plan{
		ID:        generatePlanID(),
		SessionID: sessionID,
		Title:     d.Title,
		Status:    PlanAwaitingApproval,
		CreatedAt: now,
		UpdatedAt: now,
	}
	for i, s := range d.Steps {
		plan.Steps = append(plan.Steps, &PlanStep{
			Index:  i,
			Title:  strings.TrimSpace(s.Title),
			Detail: s.Detail,
			Status: StepPending,
		})
	}
	if err := a.planStore.Save(plan); err != nil {
		return &ChatResult{Error: fmt.Sprintf("保存计划失败: %v", err)}
	}

	// 6. 推送计划事件并返回
	a.emitPlanUpdate(plan, -1)
	return &ChatResult{Plan: plan}
}

// executePlan 逐步执行计划：每步 = 一次完整工具循环（runToolLoop）。
// 长耗时，结束时返回；失败即停（后续步骤置 skipped）；done 步骤在重试时自动跳过。
func (a *App) executePlan(planID string, useStream bool) (result *ChatResult) {
	// 1. 加载计划并校验状态（running 状态拒绝并发执行，R10）
	plan, err := a.planStore.Get(planID)
	if err != nil {
		return &ChatResult{Error: err.Error()}
	}
	if plan.Status != PlanAwaitingApproval && plan.Status != PlanCancelled && plan.Status != PlanFailed {
		return &ChatResult{Error: "计划状态不允许执行: " + plan.Status}
	}

	// 2. 会话与模型解析
	session, err := a.sessionStore.GetSession(plan.SessionID)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("加载会话失败: %v", err)}
	}
	model, err := a.modelStore.GetModelForCall(session.Model)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("获取模型配置失败: %v", err)}
	}
	dir, _ := a.resolveProjectDir(plan.SessionID)
	basePrompt := a.buildBasePrompt(session, dir)

	// 3. 登记为可取消的运行。计划执行与普通聊天共用同一套取消机制
	// （原先自己一套 planCancels），因此 CancelPlan 也能升级为硬取消。
	run := a.runs.begin(plan.SessionID, planID)
	defer a.runs.end(run)
	checkCancel := func() bool { return run.SoftRequested() }

	// 4. 计划置为执行中
	plan.Status = PlanRunning
	_ = a.planStore.Save(plan)
	a.emitPlanUpdate(plan, -1)

	// 兜底：无论从哪条路径离开（含 panic 与任何提前 return），都不能把计划留在 running。
	// 留在 running 的后果是前端永久显示"执行中"、取消又找不到活跃通道而失败，
	// 用户只能重启应用——这正是"计划失败后无法修改进度"的成因之一。
	defer func() {
		if r := recover(); r != nil {
			for _, st := range plan.Steps {
				if st.Status == StepRunning {
					st.Status = StepFailed
					st.Error = fmt.Sprintf("执行异常中断: %v", r)
					st.FinishedAt = time.Now().UnixMilli()
				}
			}
			markRemainingSkipped(plan, 0)
			plan.Status = PlanFailed
			_ = a.planStore.Save(plan)
			a.emitPlanUpdate(plan, -1)
			result = &ChatResult{Plan: plan, Error: fmt.Sprintf("计划执行异常中断: %v", r)}
			return
		}
		if plan.Status != PlanRunning {
			return // 已正常收尾（completed / failed / cancelled）
		}
		for _, st := range plan.Steps {
			if st.Status == StepRunning {
				st.Status = StepFailed
				st.Error = "执行中断（未正常结束）"
				st.FinishedAt = time.Now().UnixMilli()
			}
		}
		plan.Status = PlanFailed
		_ = a.planStore.Save(plan)
		a.emitPlanUpdate(plan, -1)
	}()

	// 5. 逐步执行
	for _, step := range plan.Steps {
		// 步骤边界取消检查
		if checkCancel() {
			plan.Status = PlanCancelled
			markRemainingSkipped(plan, step.Index)
			_ = a.planStore.Save(plan)
			a.emitPlanUpdate(plan, step.Index)
			return &ChatResult{Plan: plan}
		}
		// 失败重试时跳过已完成步骤
		if step.Status == StepDone {
			continue
		}

		step.Status = StepRunning
		step.StartedAt = time.Now().UnixMilli()
		step.Error = ""
		_ = a.planStore.Save(plan)
		a.emitPlanUpdate(plan, step.Index)

		// 步骤用户消息落库（聊天流即审计日志），随后走统一执行引擎
		if _, err := a.sessionStore.AppendMessage(plan.SessionID, Message{
			Role:    RoleUser,
			Content: buildPlanStepQuery(plan, step),
		}); err != nil {
			step.Status = StepFailed
			step.Error = fmt.Sprintf("持久化步骤消息失败: %v", err)
			step.FinishedAt = time.Now().UnixMilli()
			plan.Status = PlanFailed
			markRemainingSkipped(plan, step.Index+1)
			_ = a.planStore.Save(plan)
			a.emitPlanUpdate(plan, step.Index)
			return &ChatResult{Plan: plan, Error: step.Error}
		}

		sys := buildPlanSystemPrompt(basePrompt, plan, step)
		res := runToolLoop(a.newMainAgentRun(run, session, dir, sys, &model, useStream, true /*compact*/))

		// 步骤中途取消：按本步实际结果落状态，计划置 cancelled
		if checkCancel() {
			if res.Error == "" {
				step.Status = StepDone
				step.Summary = extractStepSummary(res.Reply)
			} else {
				step.Status = StepFailed
				step.Error = "执行取消: " + res.Error
			}
			step.FinishedAt = time.Now().UnixMilli()
			plan.Status = PlanCancelled
			markRemainingSkipped(plan, step.Index+1)
			_ = a.planStore.Save(plan)
			a.emitPlanUpdate(plan, step.Index)
			return &ChatResult{Plan: plan}
		}

		// 失败即停
		if res.Error != "" {
			step.Status = StepFailed
			step.Error = res.Error
			step.FinishedAt = time.Now().UnixMilli()
			plan.Status = PlanFailed
			markRemainingSkipped(plan, step.Index+1)
			_ = a.planStore.Save(plan)
			a.emitPlanUpdate(plan, step.Index)
			return &ChatResult{Plan: plan, Error: "步骤执行失败: " + res.Error}
		}

		// 步骤完成：回写摘要
		step.Status = StepDone
		step.Summary = extractStepSummary(res.Reply)
		step.FinishedAt = time.Now().UnixMilli()
		_ = a.planStore.Save(plan)
		a.emitPlanUpdate(plan, step.Index)
	}

	// 6. 全部完成
	plan.Status = PlanCompleted
	_ = a.planStore.Save(plan)
	a.emitPlanUpdate(plan, -1)
	return &ChatResult{Plan: plan, Reply: "计划执行完成"}
}
