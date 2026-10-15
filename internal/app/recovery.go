package app

import (
	"strings"

	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
)

// ===== 中断恢复 =====
//
// 会话被异常中断（应用被强杀、进程崩溃）后，"接着往下做"此前没有任何代码保证：
// 清单不自动进上下文、悬空的 tool_call 也没人补。本文件补上这两点。
//
// 它不改变正常路径的行为——只有"确实存在未完成清单 / 确实有悬空调用"时才动手。

// withTodoContext 把当前**未完成**的 todo 清单追加到系统提示词末尾。
//
// 为什么需要：todo 清单落盘在会话里，但系统提示词从不读它。中断后用户说"继续"时，
// 模型当轮只能靠自己想起来调 list —— 没有任何硬机制保证它会。把未完成项直接摆进
// 上下文，就把这条软引导变成了确定的事实。
//
// 只列未完成项（pending / in_progress）：已完成项对"接下来做什么"没有信息量，列出来
// 只是占 token。清单为空或全部完成时返回原提示词，不追加任何内容。
func withTodoContext(systemPrompt string, session *store.Session) string {
	if session == nil {
		return systemPrompt
	}
	var open []store.TodoItem
	for _, it := range session.Todos {
		if it.Status != store.TodoDone {
			open = append(open, it)
		}
	}
	if len(open) == 0 {
		return systemPrompt
	}
	var b strings.Builder
	b.WriteString("\n\n## 未完成的待办（todo）\n")
	b.WriteString("本会话还有未完成的待办清单。若用户让你继续、或当前任务尚未收尾，")
	b.WriteString("请从这些项接着做；每完成一项就用 todo 工具把它标成 done：\n")
	for _, it := range open {
		mark := "[ ]"
		if it.Status == store.TodoInProgress {
			mark = "[~]"
		}
		b.WriteString(mark + " " + it.Text + "\n")
	}
	return systemPrompt + b.String()
}

// danglingToolResultNote 悬空 tool_call 的占位结果正文。
const danglingToolResultNote = "[系统] 该工具调用在上一次运行中断时未产生结果（应用被关闭或强制中断），此处以空结果占位。"

// repairDanglingToolCalls 给**悬空的 tool_call** 补一条占位工具结果。
//
// 成因：一轮工具调用里，assistant(tool_calls) 先落盘，工具结果随后逐条落盘（见
// runToolLoop）。若在"assistant 已落盘、结果未落盘"之间进程被杀，会话里就留下
// "有 tool_calls、无配对 tool 消息"的序列。这种序列**不会当次报错**，而是在下一次
// 构建请求时被 API 拒绝（协议要求每个 tool_call 都有配对的 tool 消息），
// 表现为"重启后这条会话一开口就 400"。
//
// 正常结束时本函数是空操作（所有 tool_call 都有配对结果）；它只在真正有悬空时才改消息序列。
// 占位结果排在**既有结果之后**、且按 tool_calls 原序补——Anthropic 要求 tool_result
// 与 tool_use 同序。返回新切片，不修改入参。
func repairDanglingToolCalls(messages []llm.LLMMessage) []llm.LLMMessage {
	if len(messages) == 0 {
		return messages
	}
	answered := make(map[string]bool, len(messages))
	for _, m := range messages {
		if m.Role == store.RoleTool && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}
	need := false
	for _, m := range messages {
		if m.Role != store.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" && !answered[tc.ID] {
				need = true
			}
		}
	}
	if !need {
		return messages
	}

	out := make([]llm.LLMMessage, 0, len(messages)+2)
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		out = append(out, m)
		if m.Role != store.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		// 原样带上紧随其后的既有 tool 结果（正常是 tool_calls 的一个前缀）
		j := i + 1
		for j < len(messages) && messages[j].Role == store.RoleTool {
			out = append(out, messages[j])
			j++
		}
		// 再按 tool_calls 原序补齐缺结果的那些
		for _, tc := range m.ToolCalls {
			if tc.ID == "" || answered[tc.ID] {
				continue
			}
			out = append(out, llm.LLMMessage{
				Role:       store.RoleTool,
				Content:    danglingToolResultNote,
				ToolCallID: tc.ID,
			})
			answered[tc.ID] = true
		}
		i = j - 1 // 已并入的 tool 结果不再重复处理
	}
	return out
}
