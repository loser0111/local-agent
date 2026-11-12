package agent

import "wails-tmp/internal/store"

// LatestAttemptMessages 计划步骤的「尝试去重」：同一步骤只保留最后一次尝试的过程。
//
// 为什么需要：步骤失败后会被自动重试或从断点续跑，而每次尝试都会在会话历史里留下
// 自己的 assistant 回复与工具结果。全都发给模型有两个代价——上下文被失败过程挤满；
// 模型看到自己上一次已经做过的副作用（改了文件、跑过命令），会倾向于重复执行。
//
// **不删库**：会话里保留全部尝试（聊天流即审计日志，用户要能回看"上次为什么失败"），
// 这里只决定"发什么"。与 ApplyToolResultBudget、摘要前缀替换同一个原则。
//
// 归属判定用 `Attempt > 0`，不用 `PlanStepIndex`：步骤索引从 0 开始，而
// PlanStepIndex 带 omitempty——第 0 步的消息序列化后字段直接消失，与"不属于任何
// 计划步骤"的老消息无法区分。Attempt 是 1 起的，0 天然表示"未标注"，没有这个歧义。
func LatestAttemptMessages(msgs []store.Message) []store.Message {
	if len(msgs) == 0 {
		return msgs
	}

	latest := make(map[int]int, 4)
	for _, m := range msgs {
		if m.Attempt <= 0 {
			continue
		}
		if m.Attempt > latest[m.PlanStepIndex] {
			latest[m.PlanStepIndex] = m.Attempt
		}
	}
	if len(latest) == 0 {
		return msgs // 没有任何计划步骤消息（普通对话）：原样返回，不做拷贝
	}

	out := make([]store.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Attempt <= 0 {
			out = append(out, m) // 非计划步骤消息（含用户原始请求、普通对话）一律保留
			continue
		}
		if m.Attempt == latest[m.PlanStepIndex] {
			out = append(out, m)
		}
	}
	return out
}
