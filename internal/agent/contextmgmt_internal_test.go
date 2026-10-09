package agent

import (
	"strings"
	"testing"

	"wails-tmp/internal/store"
)

// ===== 上下文管理的包内用例 =====
//
// 这些用例断言**未导出**的机制：字符估算的系数、压缩切点的合法性、工具结果预算的裁剪。
// 根包不能跨包引用未导出符号，所以随包迁入（与 runcontrol_internal_test.go、ask_internal_test.go 同理）。
//
// 留在根包的同类用例：凡是只用导出 API 就能表达的（EstimateSeqTokens / NeedCompact /
// ContextStatOf / FormatContextStatReport），仍放在 contextmgmt_test.go —— 它们同时是
// "根包怎么用这套引擎"的说明。

// 字符估算的量级：中日韩字符明显比同长度的 ASCII 贵
func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("空串应为 0，实际 %d", got)
	}
	ascii := estimateTokens(strings.Repeat("a", 400))
	cjk := estimateTokens(strings.Repeat("字", 400))
	if ascii != 100 {
		t.Fatalf("400 个 ASCII 字符应约 100 token，实际 %d", ascii)
	}
	if cjk != 400 {
		t.Fatalf("400 个汉字应约 400 token，实际 %d", cjk)
	}
	if cjk <= ascii {
		t.Fatal("同等长度的中文应比英文贵——系数写反了")
	}
}

// 切点必须落在非 tool 结果的 user 消息之前，且不能切开 assistant(tool_calls) 与其 tool 结果
func TestFindCompactCut(t *testing.T) {
	build := func() []store.Message {
		return []store.Message{
			{ID: "m0", Role: store.RoleUser, Content: "任务"},
			{ID: "m1", Role: store.RoleAssistant, Content: "", ToolCalls: []store.ToolCall{{ID: "c1", Name: "exec_shell"}}},
			{ID: "m2", Role: store.RoleTool, Content: "结果1", ToolCallID: "c1"},
			{ID: "m3", Role: store.RoleAssistant, Content: "做完了第一步"},
			{ID: "m4", Role: store.RoleUser, Content: "继续"},
			{ID: "m5", Role: store.RoleAssistant, Content: "", ToolCalls: []store.ToolCall{{ID: "c2", Name: "read_file"}}},
			{ID: "m6", Role: store.RoleTool, Content: "结果2", ToolCallID: "c2"},
			{ID: "m7", Role: store.RoleAssistant, Content: "读完了"},
			{ID: "m8", Role: store.RoleUser, Content: "再看一下"},
			{ID: "m9", Role: store.RoleAssistant, Content: "好的"},
			{ID: "m10", Role: store.RoleUser, Content: "还有吗"},
			{ID: "m11", Role: store.RoleAssistant, Content: "有"},
			{ID: "m12", Role: store.RoleUser, Content: "最后一个问题"},
			{ID: "m13", Role: store.RoleAssistant, Content: "答"},
		}
	}

	msgs := build()
	cut := findCompactCut(msgs, 6)
	if cut <= 0 {
		t.Fatalf("应找到一个合法切点，实际 %d", cut)
	}
	if msgs[cut].Role != store.RoleUser {
		t.Fatalf("切点应落在 user 消息之前，实际在第 %d 条（role=%s）", cut, msgs[cut].Role)
	}
	if msgs[cut].ToolCallID != "" {
		t.Fatal("切点不能落在 tool 结果消息之前")
	}
	// 左侧必须自洽：不能以"带 tool_calls 的 assistant"收尾（那样它的 tool 结果会被切走）
	prev := msgs[cut-1]
	if len(prev.ToolCalls) > 0 {
		t.Fatalf("切点左侧不该是带 tool_calls 的 assistant（第 %d 条）", cut-1)
	}

	// 消息太少：不切
	if got := findCompactCut(msgs, len(msgs)); got != -1 {
		t.Fatalf("消息数不超过保留条数时应返回 -1，实际 %d", got)
	}
	if got := findCompactCut(msgs, len(msgs)+5); got != -1 {
		t.Fatalf("保留数超过总条数时应返回 -1，实际 %d", got)
	}
}

// 找不到合法切点时返回 -1（整段都是 tool 结果，硬切会产生非法序列）
func TestFindCompactCutNoLegalPoint(t *testing.T) {
	msgs := []store.Message{
		{ID: "m0", Role: store.RoleUser, Content: "任务"},
		{ID: "m1", Role: store.RoleAssistant, Content: "", ToolCalls: []store.ToolCall{{ID: "c1", Name: "t"}}},
	}
	// 后面全是 tool 结果，没有可切的 user 消息（除 index 0，但它不构成"非空切点"）
	for i := 0; i < 12; i++ {
		msgs = append(msgs, store.Message{ID: "t", Role: store.RoleTool, Content: "r", ToolCallID: "c1"})
	}
	if got := findCompactCut(msgs, 4); got != -1 {
		t.Fatalf("没有合法的 user 切点时应返回 -1，实际 %d", got)
	}
}

// 工具结果预算：超限保留头尾、短的原样；且**不改动传入的切片**（持久化存全量）
func TestApplyToolResultBudget(t *testing.T) {
	long := strings.Repeat("x", contextToolResultBudget+5000)
	msgs := []store.Message{
		{ID: "a", Role: store.RoleTool, Content: long},
		{ID: "b", Role: store.RoleTool, Content: "短的"},
		{ID: "c", Role: store.RoleUser, Content: long},
	}
	out := ApplyToolResultBudget(msgs)

	got := []rune(out[0].Content)
	if len(got) >= len([]rune(long)) {
		t.Fatal("超限的工具结果应被截断")
	}
	if !strings.Contains(out[0].Content, "中间省略") {
		t.Fatal("截断处应有明确说明，否则模型以为它看到的就是全部")
	}
	if !strings.HasPrefix(out[0].Content, "xxx") || !strings.HasSuffix(out[0].Content, "xxx") {
		t.Fatal("应保留头尾")
	}
	if out[1].Content != "短的" {
		t.Fatal("未超限的工具结果不应被改动")
	}
	if out[2].Content != long {
		t.Fatal("非工具消息不应被改动")
	}
	// 关键：原切片必须原样——持久化存全量，只有发给模型的那份被裁剪
	if msgs[0].Content != long {
		t.Fatal("预算只该作用于副本，不能改动传入的消息（持久化要存全量）")
	}
}
