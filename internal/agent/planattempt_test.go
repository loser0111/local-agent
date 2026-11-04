package agent

import (
	"strings"
	"testing"

	"wails-tmp/internal/store"
)

func planMsg(role, content string, step, attempt int) store.Message {
	return store.Message{Role: role, Content: content, PlanStepIndex: step, Attempt: attempt}
}

// TestLatestAttemptMessagesKeepsOnlyLastAttempt 两次尝试的步骤只保留最后一次。
func TestLatestAttemptMessagesKeepsOnlyLastAttempt(t *testing.T) {
	msgs := []store.Message{
		{Role: store.RoleUser, Content: "帮我升级依赖"},       // 普通消息
		planMsg(store.RoleUser, "【计划步骤 1/2】检查依赖", 0, 1), // 第 1 步第 1 次
		planMsg(store.RoleAssistant, "第一次失败前的话", 0, 1),
		planMsg(store.RoleTool, "第一次的工具结果", 0, 1),
		planMsg(store.RoleUser, "【计划步骤 1/2】检查依赖", 0, 2), // 第 1 步第 2 次
		planMsg(store.RoleAssistant, "第二次成功", 0, 2),
		planMsg(store.RoleUser, "【计划步骤 2/2】升级", 1, 1), // 第 2 步只跑过一次
		planMsg(store.RoleAssistant, "升级完成", 1, 1),
	}

	got := LatestAttemptMessages(msgs)

	// 期望：普通消息 + 第 1 步的第 2 次 + 第 2 步的第 1 次
	if len(got) != 5 {
		t.Fatalf("消息条数应为 5，实际 %d：%+v", len(got), contents(got))
	}
	if want := []string{"帮我升级依赖", "第二次成功", "升级完成"}; !containsAll(contents(got), want) {
		t.Fatalf("最终尝试的内容应保留，实际：%v", contents(got))
	}
	for _, c := range contents(got) {
		if strings.Contains(c, "第一次") {
			t.Fatalf("失败尝试的内容不应出现：%q", c)
		}
	}
}

// TestLatestAttemptMessagesStepZeroIsAddressable 第 0 步必须可寻址。
//
// 这条用例守的是一个真实陷阱：PlanStepIndex 带 omitempty，若用"字段是否有值"判断
// 归属，第 0 步的消息会被当成普通消息、永远不会去重（静默失效）。
// 归属改用 Attempt>0 判定，这里逐字段核对。
func TestLatestAttemptMessagesStepZeroIsAddressable(t *testing.T) {
	msgs := []store.Message{
		planMsg(store.RoleAssistant, "step0 attempt1", 0, 1),
		planMsg(store.RoleAssistant, "step0 attempt2", 0, 2),
		planMsg(store.RoleAssistant, "step0 attempt3", 0, 3),
	}
	got := LatestAttemptMessages(msgs)
	if len(got) != 1 || got[0].Content != "step0 attempt3" {
		t.Fatalf("第 0 步应只保留最后一次尝试，实际：%v", contents(got))
	}
}

// TestLatestAttemptMessagesNoPlanMessages 没有计划步骤消息时原样返回（不拷贝、不改变顺序）。
//
// 普通对话占绝大多数，这条路径不能有任何行为变化。
func TestLatestAttemptMessagesNoPlanMessages(t *testing.T) {
	msgs := []store.Message{
		{Role: store.RoleUser, Content: "问"},
		{Role: store.RoleAssistant, Content: "答"},
		{Role: store.RoleTool, Content: "工具结果", ToolCallID: "c1"},
	}
	got := LatestAttemptMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("条数不应变化，实际 %d", len(got))
	}
	for i := range msgs {
		if got[i].Content != msgs[i].Content {
			t.Fatalf("内容不应变化：idx=%d", i)
		}
	}
	if LatestAttemptMessages(nil) != nil {
		t.Fatal("nil 输入应原样返回 nil")
	}
}

// TestLatestAttemptMessagesAcrossSessionRerun 跨步骤交错时按步骤各取最后一次。
func TestLatestAttemptMessagesAcrossSessionRerun(t *testing.T) {
	msgs := []store.Message{
		planMsg(store.RoleAssistant, "s0a1", 0, 1),
		planMsg(store.RoleAssistant, "s0a2", 0, 2),
		planMsg(store.RoleAssistant, "s1a1", 1, 1),
		planMsg(store.RoleAssistant, "s2a1", 2, 1),
		// 续跑：第 1 步再来一次（顺序上晚于第 2 步，但归属仍是第 1 步）
		planMsg(store.RoleAssistant, "s1a2", 1, 2),
	}
	got := LatestAttemptMessages(msgs)
	want := []string{"s0a2", "s2a1", "s1a2"}
	if len(got) != len(want) {
		t.Fatalf("期望 %d 条，实际 %d：%v", len(want), len(got), contents(got))
	}
	for i, w := range want {
		if got[i].Content != w {
			t.Fatalf("第 %d 条应为 %q，实际 %q（顺序也必须保留）", i, w, got[i].Content)
		}
	}
}

func contents(msgs []store.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func containsAll(hay []string, needles []string) bool {
	set := make(map[string]bool, len(hay))
	for _, h := range hay {
		set[h] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}
