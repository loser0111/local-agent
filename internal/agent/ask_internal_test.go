package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ===== ask_user 引擎侧用例 =====
//
// 这些用例直接断言包内未导出的解析/格式化函数（parseAskQuestions / formatAskResult），
// 或只测回路本身（Busy/超时/取消）。根包不能跨包引用未导出符号，所以随包迁入这里；
// 根包 ask_test.go 只留「App 侧接线」的用例（事件、快进失败、取消）。
// 与 runcontrol_internal_test.go 同理。

func TestParseAskQuestions(t *testing.T) {
	args := map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{
				"header":   "状态管理",
				"question": "用哪种状态管理？",
				"options": []interface{}{
					map[string]interface{}{"label": "Zustand", "description": "轻量"},
					map[string]interface{}{"label": "Pinia"},
					map[string]interface{}{"label": "   "}, // 空选项应被忽略
				},
			},
			map[string]interface{}{
				"question":    "部署到哪个环境？",
				"multiSelect": true,
			},
		},
	}
	qs, err := parseAskQuestions(args)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(qs) != 2 {
		t.Fatalf("应解析出 2 个问题，实际 %d", len(qs))
	}
	if qs[0].Header != "状态管理" || qs[0].Question != "用哪种状态管理？" {
		t.Fatalf("第一个问题解析异常: %+v", qs[0])
	}
	if len(qs[0].Options) != 2 {
		t.Fatalf("空选项应被忽略，实际 %d 个: %+v", len(qs[0].Options), qs[0].Options)
	}
	if qs[0].Options[0].Description != "轻量" {
		t.Fatalf("选项说明未解析: %+v", qs[0].Options[0])
	}
	if !qs[0].AllowFreeText {
		t.Fatal("allowFreeText 默认应为 true")
	}
	if !qs[1].MultiSelect {
		t.Fatal("multiSelect 未解析")
	}

	bad := []map[string]interface{}{
		{}, // 缺 questions
		{"questions": []interface{}{}},
		{"questions": "not-a-list"},
		{"questions": []interface{}{map[string]interface{}{"header": "只有标题"}}}, // 缺 question
		{"questions": []interface{}{"字符串不是对象"}},
		{"questions": []interface{}{
			map[string]interface{}{"question": "q", "options": []interface{}{
				map[string]interface{}{"label": "1"}, map[string]interface{}{"label": "2"},
				map[string]interface{}{"label": "3"}, map[string]interface{}{"label": "4"},
				map[string]interface{}{"label": "5"},
			}},
		}}, // 选项过多
		{"questions": []interface{}{
			map[string]interface{}{"question": "1"}, map[string]interface{}{"question": "2"},
			map[string]interface{}{"question": "3"}, map[string]interface{}{"question": "4"},
			map[string]interface{}{"question": "5"},
		}}, // 问题过多
	}
	for i, b := range bad {
		if _, err := parseAskQuestions(b); err == nil {
			t.Errorf("第 %d 组非法参数应报错", i+1)
		}
	}
}

func TestAskBrokerResolveAndCancel(t *testing.T) {
	b := NewAskBroker(time.Minute)

	// 正常答复
	id, ch := b.Register("s1", AskRequest{Questions: []AskQuestion{{Question: "q"}}})
	if p := b.Pending("s1"); p == nil || p.Questions[0].Question != "q" {
		t.Fatalf("Pending 应能取回挂起提问，实际 %+v", p)
	}
	done := make(chan AskAnswer, 1)
	go func() {
		ans, _ := b.Wait(context.Background(), id, ch)
		done <- ans
	}()
	if err := b.Resolve(AskAnswer{ID: id, Answers: []AskItem{{Selected: []string{"A"}}}}); err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	select {
	case ans := <-done:
		if ans.Cancelled || len(ans.Answers) != 1 || ans.Answers[0].Selected[0] != "A" {
			t.Fatalf("答复内容异常: %+v", ans)
		}
		if ans.SessionID != "s1" {
			t.Fatalf("答复应带上会话 ID，实际 %q", ans.SessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("等待答复超时")
	}

	// 迟到答复（已在 pending 之外）应报错
	if err := b.Resolve(AskAnswer{ID: id}); err == nil {
		t.Fatal("重复答复应报错")
	}
	if p := b.Pending("s1"); p != nil {
		t.Fatal("答复后不应再有挂起提问")
	}

	// 会话取消 → 等待方拿到 Cancelled（而不是永久阻塞）
	id2, ch2 := b.Register("s2", AskRequest{Questions: []AskQuestion{{Question: "q2"}}})
	done2 := make(chan AskAnswer, 1)
	go func() {
		ans, _ := b.Wait(context.Background(), id2, ch2)
		done2 <- ans
	}()
	time.Sleep(20 * time.Millisecond)
	if n := b.CancelSession("s2"); n != 1 {
		t.Fatalf("应取消 1 个挂起提问，实际 %d", n)
	}
	select {
	case ans := <-done2:
		if !ans.Cancelled {
			t.Fatalf("取消后应返回 Cancelled 答复，实际 %+v", ans)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后等待方应立刻返回")
	}

	// 超时 → 同样返回 Cancelled，而不是让整轮失败
	tiny := NewAskBroker(30 * time.Millisecond)
	id3, ch3 := tiny.Register("s3", AskRequest{})
	ans3, err := tiny.Wait(context.Background(), id3, ch3)
	if err != nil {
		t.Fatalf("超时不应返回 error（否则整轮会失败）: %v", err)
	}
	if !ans3.Cancelled {
		t.Fatalf("超时应标记为用户未作答，实际 %+v", ans3)
	}
	if tiny.Pending("s3") != nil {
		t.Fatal("超时后应清理挂起记录")
	}
}

func TestAskUserToolExecute(t *testing.T) {
	captured := AskRequest{}
	tl := NewAskUserTool(func(ctx context.Context, req AskRequest) (AskAnswer, error) {
		captured = req
		return AskAnswer{Answers: []AskItem{
			{Selected: []string{"Zustand"}, Text: "顺便用 immer"},
			{Text: "预发环境"},
		}}, nil
	})

	out, err := tl.Execute(context.Background(), map[string]interface{}{
		"questions": []interface{}{
			map[string]interface{}{"question": "用哪种状态管理？", "options": []interface{}{
				map[string]interface{}{"label": "Zustand"}, map[string]interface{}{"label": "Pinia"},
			}},
			map[string]interface{}{"question": "部署到哪？"},
		},
	})
	if err != nil {
		t.Fatalf("提问失败: %v", err)
	}
	if len(captured.Questions) != 2 || captured.Questions[0].Question != "用哪种状态管理？" {
		t.Fatalf("回路收到的请求不正确: %+v", captured)
	}
	for _, want := range []string{"用户答复", "用哪种状态管理？", "选择：Zustand", "补充说明：顺便用 immer", "回答：预发环境", "请按上述答复继续"} {
		if !strings.Contains(out, want) {
			t.Errorf("工具结果应包含 %q，实际:\n%s", want, out)
		}
	}

	// 用户未作答：工具返回错误，提示模型自行决策（而不是静默继续）
	skipped := NewAskUserTool(func(ctx context.Context, req AskRequest) (AskAnswer, error) {
		return AskAnswer{Cancelled: true}, nil
	})
	if _, err := skipped.Execute(context.Background(), map[string]interface{}{
		"questions": []interface{}{map[string]interface{}{"question": "q"}},
	}); err == nil || !strings.Contains(err.Error(), "没有作答") {
		t.Fatalf("跳过时应返回可读错误，实际: %v", err)
	}

	// 界面未就绪（asker 为 nil）：明确报错，不静默成功
	noUI := NewAskUserTool(nil)
	if _, err := noUI.Execute(context.Background(), map[string]interface{}{
		"questions": []interface{}{map[string]interface{}{"question": "q"}},
	}); err == nil {
		t.Fatal("没有界面支持时应报错")
	}

	// 参数非法时不打扰用户（不应调用 asker）
	called := false
	strict := NewAskUserTool(func(ctx context.Context, req AskRequest) (AskAnswer, error) {
		called = true
		return AskAnswer{}, nil
	})
	if _, err := strict.Execute(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("缺少 questions 应报错")
	}
	if called {
		t.Fatal("参数非法时不应发起提问")
	}
}

func TestFormatAskResultPartial(t *testing.T) {
	qs := []AskQuestion{{Question: "q1"}, {Question: "q2"}, {Question: "q3"}}
	out := formatAskResult(qs, AskAnswer{Answers: []AskItem{
		{Selected: []string{"A", "B"}},
		{}, // 未作答
	}})
	if !strings.Contains(out, "选择：A、B") {
		t.Errorf("多选应顿号连接，实际:\n%s", out)
	}
	if !strings.Contains(out, "用户未作答") {
		t.Errorf("未作答的问题应显式标注，实际:\n%s", out)
	}
	if strings.Count(out, "用户未作答") != 2 {
		t.Errorf("缺口题与多余题都应标为未作答，实际:\n%s", out)
	}
}
