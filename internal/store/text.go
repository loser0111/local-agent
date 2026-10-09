package store

// TruncateRunes 按字符截断（多字节安全），超长追加省略号。
//
// 由 plan.go 下沉：它同时被会话侧（计划摘要、早期工具结果压缩）与 llm 层
// （错误响应体截断，见 llmretry.go）使用，而 llm 包依赖 store。
func TruncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
