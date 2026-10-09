package store

// 消息角色（OpenAI / Anthropic 共用的中立取值）。
//
// 放在 store 而不是 llm：会话消息（Message.Role）与 LLM 请求（LLMMessage.Role）用的是
// 同一套取值，而 llm 包依赖 store（见 anthropic.go 的 *store.Model），反向依赖会成环。
const (
	RoleUser      = "user"
	RoleSystem    = "system"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)
