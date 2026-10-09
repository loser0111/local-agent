package llm

import "strings"

// IsContextOverflowError 判断错误是否属于"上下文超长"。
//
// 由 contextmgmt.go 下沉：重试层（llmretry.go 的 shouldRetry）与上下文压缩都要判它，
// 而 llm 包是两者的共同下游。
//
// 各家措辞不一，只能做关键字匹配；刻意不用裸 "exceed"——那会把
// "rate limit exceeded" 之类也卷进来，触发一次毫无意义的压缩。
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, k := range []string{
		"context length", "context_length", "context window", "maximum context",
		"too many tokens", "token limit", "input is too long", "reduce the length",
		"prompt is too long", "上下文超", "请求过长", "内容过长",
	} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}
