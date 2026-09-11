package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
type LLMToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  *LLMToolParams `json:"parameters"`
}

// LLMToolParams 工具参数定义
type LLMToolParams struct {
	Type       string                 `json:"type"`
	Required   []string               `json:"required"`
	Properties map[string]LLMToolProp `json:"properties"`
}

// LLMToolProp 工具参数属性
type LLMToolProp struct {
	Type        string `json:"type"`
	Description string `json:"description"`
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
type LLMResp struct {
	Choices []LLMChoice `json:"choices"`
	Created int64       `json:"created"`
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Object  string      `json:"object"`
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
	Error     string     `json:"error,omitempty"`     // 错误信息
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

	SystemPrompt = "你是一个智能助手，可以调用工具来帮助用户解决问题。"
)

// ===== LLM HTTP 调用 =====

// callLLM 调用 LLM API（OpenAI 兼容格式，非流式）
func callLLM(url, token string, req *LLMReq) (*LLMResp, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化 LLM 请求失败: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(data))
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

// ===== 对话流程（借鉴 01agent 的状态机，简化为循环）=====

// ChatEvent 推送给前端的事件数据
type ChatEvent struct {
	Type     string     `json:"type"` // tool_call_start / tool_call_end / done / error / diff_update
	ToolCall *ToolCall  `json:"toolCall,omitempty"`
	Reply    string     `json:"reply,omitempty"`
	Error    string     `json:"error,omitempty"`
	Diff     []DiffFile `json:"diff,omitempty"` // diff_update 事件携带的差异文件
	Turn     int        `json:"turn,omitempty"` // diff 所属轮次
}

// buildLLMMessages 从会话历史消息构造 LLM messages 数组
// 正确重建消息序列：assistant(tool_calls) → tool(tool_call_id) → assistant(最终回复)
func buildLLMMessages(session *Session, query string) []LLMMessage {
	messages := []LLMMessage{
		{Role: RoleSystem, Content: SystemPrompt},
	}

	// 添加历史消息
	for _, msg := range session.Messages {
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

	// 添加当前用户消息
	messages = append(messages, LLMMessage{Role: RoleUser, Content: query})

	return messages
}

// executeChat 执行完整的多轮对话流程
// 借鉴 01agent 的状态机设计：Preprocessing → LLM Call → Judge → Tool Execute → Rebuild → 循环
// 持久化所有中间消息（assistant+tool_calls, tool结果, 最终回复），确保下次对话时消息序列完整
func (a *App) executeChat(sessionID, query string) *ChatResult {
	// 1. 加载会话
	session, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("加载会话失败: %v", err)}
	}

	// 2. 获取模型配置
	model, err := a.modelStore.GetModel(session.Model)
	if err != nil {
		return &ChatResult{Error: fmt.Sprintf("获取模型配置失败: %v", err)}
	}

	// 3. 确定模型 ID（优先用 ModelID，为空则用 Name）
	modelID := model.ModelID
	if modelID == "" {
		modelID = model.Name
	}

	// ★ 记录本轮工作区基线（对话开始前的快照，用于轮末计算本轮 diff）
	dir, _ := a.resolveProjectDir(sessionID)
	isRepo := dir != "" && a.diffService.IsRepo(dir)
	turnBase := ""
	if isRepo {
		a.diffService.EnsureBaseline(sessionID, dir)
		turnBase = a.diffService.TurnSnapshot(dir)
	}

	// 4. 构造 LLM messages
	messages := buildLLMMessages(session, query)

	// 5. 获取工具定义
	tools := a.toolManager.GetToolsForLLM()

	// 6. 工具调用循环
	var toolCallRecords []ToolCall
	var persistedMsgs []Message // 本次对话持久化的所有消息

	for turn := 0; turn < MaxChatTurns; turn++ {
		req := &LLMReq{
			Model:       modelID,
			Messages:    messages,
			Temperature: 1.0,
			Stream:      false,
			Tools:       tools,
		}

		resp, err := callLLM(model.URL, model.APIKey, req)
		if err != nil {
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
				if a.ctx != nil {
					wailsRuntime.EventsEmit(a.ctx, "chat:event", ChatEvent{
						Type:     "tool_call_start",
						ToolCall: &toolCallRecord,
					})
				}

				// 执行工具
				result, execErr := a.toolManager.ExecuteTool(tc.Function.Name, args)
				duration := time.Since(startTime).Seconds()

				if execErr != nil {
					toolCallRecord.Status = "error"
					toolCallRecord.Duration = duration
					toolCallRecord.Result = fmt.Sprintf("执行失败: %v", execErr)
				} else {
					toolCallRecord.Status = "success"
					toolCallRecord.Duration = duration
					toolCallRecord.Result = result
				}
				toolCallRecords = append(toolCallRecords, toolCallRecord)
				executedToolCalls = append(executedToolCalls, toolCallRecord)

				// 推送工具调用完成事件
				if a.ctx != nil {
					wailsRuntime.EventsEmit(a.ctx, "chat:event", ChatEvent{
						Type:     "tool_call_end",
						ToolCall: &toolCallRecord,
					})
				}
			}

			// 持久化 assistant 消息（含 tool_calls 执行记录）
			assistantMsg := Message{
				Role:      RoleAssistant,
				Content:   choice.Message.Content, // LLM 可能返回空内容
				ToolCalls: executedToolCalls,
			}
			savedAssistant, err := a.sessionStore.AppendMessage(sessionID, assistantMsg)
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
				savedTool, err := a.sessionStore.AppendMessage(sessionID, toolMsg)
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
		savedFinal, err := a.sessionStore.AppendMessage(sessionID, finalMsg)
		if err != nil {
			return &ChatResult{
				Error:     fmt.Sprintf("持久化最终回复失败: %v", err),
				ToolCalls: toolCallRecords,
				Messages:  persistedMsgs,
			}
		}
		persistedMsgs = append(persistedMsgs, *savedFinal)

		// ★ 计算本轮工作区 diff：相对轮初快照，有改动则持久化并推送事件
		var diffFiles []DiffFile
		if isRepo {
			if files, err := a.diffService.Diff(dir, turnBase); err == nil && len(files) > 0 {
				diffFiles = files
				turnNo, appendErr := a.sessionStore.AppendDiff(sessionID, DiffTurn{
					Files:     files,
					Additions: sumAdd(files),
					Deletions: sumDel(files),
					CreatedAt: time.Now().UnixMilli(),
				})
				if appendErr == nil && a.ctx != nil {
					wailsRuntime.EventsEmit(a.ctx, "diff:update", ChatEvent{
						Type: "diff_update",
						Diff: files,
						Turn: turnNo,
					})
				}
			}
		}

		result := &ChatResult{
			Reply:     choice.Message.Content,
			ToolCalls: toolCallRecords,
			Messages:  persistedMsgs,
			Diff:      diffFiles,
		}

		// 推送完成事件
		if a.ctx != nil {
			wailsRuntime.EventsEmit(a.ctx, "chat:event", ChatEvent{
				Type:  "done",
				Reply: result.Reply,
			})
		}

		return result
	}

	return &ChatResult{
		Error:     "超过最大对话轮次限制",
		ToolCalls: toolCallRecords,
		Messages:  persistedMsgs,
	}
}
