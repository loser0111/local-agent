package llm

import "wails-tmp/internal/store"

// 本文件由根包 usage.go 拆出：把已归一化的 LLMUsage 折算成 store.TokenUsage。
//
// 它是唯一的「协议用量 -> 中立用量」换算点，放在 llm 包是因为它必须看见 LLMUsage 的
// 归一化不变式（PromptTokens 恒为输入总量，见 LLMUsage 的说明）。

// tokenUsageOf 把统一形状的 LLMUsage 折算成 store.TokenUsage。
//
// 前提：LLMUsage.PromptTokens 已被各协议的解析层归一成「输入总量」
// （见 anthropic.go 的 LLMUsageFromAnthropic）。这里只做减法，不做任何协议判断。
func TokenUsageOf(u LLMUsage) store.TokenUsage {
	uncached := u.PromptTokens - u.CacheRead - u.CacheWrite
	if uncached < 0 {
		// 网关给的数不自洽（缓存量大于总量）时宁可归零：
		// 负数会让「未命中」这一格无法解释，也会把成本倍数算成负数。
		uncached = 0
	}
	return store.TokenUsage{
		InputUncached:          uncached,
		CacheRead:              u.CacheRead,
		CacheWrite:             u.CacheWrite,
		CacheWrite5m:           u.CacheWrite5m,
		CacheWrite1h:           u.CacheWrite1h,
		Output:                 u.CompletionTokens,
		OutputReasoning:        u.ReasoningTokens,
		ServerToolUseWebSearch: u.ServerToolUseWebSearch,
		ServiceTier:            u.ServiceTier,
	}
}
