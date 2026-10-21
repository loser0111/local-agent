package app

import (
	"encoding/json"
	"fmt"
	"time"

	"wails-tmp/internal/llm"
	"wails-tmp/internal/observability"
	"wails-tmp/internal/store"
)

// ===== Langfuse 埋点（本地上下文 -> 遥测字段）=====
//
// 为什么单独一个文件：埋点散在三处（根 / generation / tool），但「把本项目的
// 内部结构翻译成 Langfuse 认的字段」这件事只有一套规则——用量键名、内容截断、
// 图片脱敏、metadata 前缀。写在各自调用点必然漂移，而漂移的后果是**静默**的：
// UI 里某一类数据突然空掉，不会报错。
//
// 本文件的所有函数都对 nil 接收者/入参安全：遥测未启用时整个埋点只是一次判空。

// traceRun 开始本次运行的 trace。
//
// trace 名刻意带会话与运行标识：Langfuse UI 的 trace 列表里只有名字能一眼分辨，
// 光看时间戳在一堆并发子代理运行时基本无从下手。
func (ar *agentRun) traceRun(name string) *observability.Run {
	if ar == nil || ar.Trace == nil {
		return nil
	}
	return ar.Trace.StartRun(ar.SessionID, name)
}

// traceGeneration 上报一次模型调用。
//
// 两个值得注意的点：
//   - usage 的键必须与 Langfuse 模型定义里的价格键**逐字一致**才会被用于成本计算。
//     本项目走内网网关 + 自定义模型名，内置价格表里没有，cost 会是空——这是预期行为，
//     token 量本身已经足够用（见 docs/langfuse-integration-survey.md §2.4）。
//   - 内容只在 CaptureContent 打开时才组装。关着的时候连序列化都不做，
//     既省掉一次 JSON 编码，也避免"不小心把正文传下去"这类事故。
func (ar *agentRun) traceGeneration(tr *observability.Run, turn int, model *store.Model,
	req *llm.LLMReq, resp *llm.LLMResp, err error, start time.Time) {

	if tr == nil {
		return
	}
	data := observability.GenerationData{
		Name:   fmt.Sprintf("turn-%d", turn),
		Model:  store.ModelCallID(model),
		Start:  start,
		End:    time.Now(),
		Params: map[string]any{"temperature": req.Temperature},
		Metadata: map[string]any{
			"turn":   turn,
			"runId":  ar.RunID,
			"stream": req.Stream,
		},
	}
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
		data.Params["thinking_budget_tokens"] = req.Thinking.BudgetTokens
	}
	if err != nil {
		data.ErrMsg = err.Error()
	}
	if resp != nil {
		data.Usage = usageDetails(resp.Usage)
		if len(resp.Choices) > 0 {
			data.Output = resp.Choices[0].Message.Content
		}
		if resp.Thinking != "" {
			// 思考过程单独记一个 metadata 长度而不是塞进 output：
			// 它不该混进「回答正文」里，但"这一轮到底思考了多少"对排障有价值。
			data.Metadata["thinkingChars"] = len(resp.Thinking)
		}
	}
	if ar.traceCaptureContent() {
		data.Input = traceInputText(req.Messages)
	}
	tr.Generation(data)
}

// traceTool 上报一次工具调用。rec 已经是执行后的完整记录（状态/耗时/改动文件都在里头）。
func (ar *agentRun) traceTool(tr *observability.Run, rec *store.ToolCall,
	model *store.Model, start time.Time, err error) {

	if tr == nil || rec == nil {
		return
	}
	data := observability.ToolData{
		Name:   rec.Name,
		Start:  start,
		End:    start.Add(time.Duration(rec.Duration * float64(time.Second))),
		Output: rec.Result,
		Metadata: map[string]any{
			"toolCallId": rec.ID,
			"status":     rec.Status,
			"durationMs": int64(rec.Duration * 1000),
			"model":      store.ModelCallID(model),
		},
	}
	if len(rec.Files) > 0 {
		data.Metadata["files"] = len(rec.Files)
		// 文件名单独放一份（截断到前 10 个）：Langfuse 的 metadata 是字符串标量，
		// 把整张列表塞进去会变成一个不可筛选的长串。
		data.Metadata["fileNames"] = joinLimited(rec.Files, 10)
	}
	if len(rec.Images) > 0 {
		data.Metadata["images"] = len(rec.Images)
	}
	if err != nil {
		data.ErrMsg = err.Error()
	}
	if ar.traceCaptureContent() {
		data.Input = traceArgsText(rec.Args)
	}
	tr.Tool(data)
}

// traceCaptureContent 是否导出正文。未启用的 Tracer 走不到这里（调用点已判 nil），
// 因此这里只关心配置本身。
func (ar *agentRun) traceCaptureContent() bool {
	if ar == nil || ar.Trace == nil {
		return false
	}
	return ar.Trace.CaptureContent()
}

// usageDetails 把已归一化的 LLMUsage 摊成 usage_details。
//
// **键名照抄 Langfuse 的约定**（input / output / cache_read_input_tokens /
// cache_creation_input_tokens / reasoning），不要在这里自创名字——它们要和
// 模型定义里的价格键逐字对齐，且 cache_read 这类键在 UI 的成本视图里是特判的。
//
// 只填非零项：Langfuse 会拒绝 usage_details 里的负值，而一堆 0 会让
// 「这个模型到底有没有缓存」这类判断变得难读。
func usageDetails(u llm.LLMUsage) map[string]int {
	if u.PromptTokens <= 0 && u.CompletionTokens <= 0 {
		return nil
	}
	d := map[string]int{
		"input":  u.PromptTokens,
		"output": u.CompletionTokens,
	}
	if u.CacheRead > 0 {
		d["cache_read_input_tokens"] = u.CacheRead
	}
	if u.CacheWrite > 0 {
		d["cache_creation_input_tokens"] = u.CacheWrite
	}
	if u.ReasoningTokens > 0 {
		d["reasoning"] = u.ReasoningTokens
	}
	return d
}

// traceInputText 把要发出去的 messages 序列化成一段文本。
//
// 复用 llm.RedactMessages：它会把图片换成「图片已省略：1024×768，image/png，120 KB」
// 的占位说明。这一步是**必须**的——base64 后的图是 MB 级，既撑爆请求也没有排障价值。
// 请求快照（internal/llm/requestlog.go）用的是同一份脱敏逻辑，两处口径因此天然一致。
//
// 用 JSON 而不是各家协议的原始线格式：trace 是给人看的，统一形状更好读。
func traceInputText(messages []llm.LLMMessage) string {
	if len(messages) == 0 {
		return ""
	}
	b, err := json.Marshal(llm.RedactMessages(messages))
	if err != nil {
		return ""
	}
	return string(b)
}

// traceArgsText 工具参数序列化（参数是 map，直接 marshal 即可）
func traceArgsText(args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(b)
}

// joinLimited 取前 n 项拼成逗号分隔串，超出部分只标数量。
func joinLimited(items []string, n int) string {
	if len(items) == 0 {
		return ""
	}
	out := ""
	for i, s := range items {
		if i >= n {
			out += fmt.Sprintf("…（共 %d 个）", len(items))
			break
		}
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// traceRunName 生成 trace 名。计划步骤执行与普通聊天的区别值得在列表里一眼看出。
func traceRunName(planID string) string {
	if planID != "" {
		return "plan-step"
	}
	return "chat"
}
