package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ===== ask_user：模型主动向用户提问 =====

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
	b := newAskBroker(time.Minute)

	// 正常答复
	id, ch := b.register("s1", AskRequest{Questions: []AskQuestion{{Question: "q"}}})
	if p := b.Pending("s1"); p == nil || p.Questions[0].Question != "q" {
		t.Fatalf("Pending 应能取回挂起提问，实际 %+v", p)
	}
	done := make(chan AskAnswer, 1)
	go func() {
		ans, _ := b.Wait(id, ch)
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
	id2, ch2 := b.register("s2", AskRequest{Questions: []AskQuestion{{Question: "q2"}}})
	done2 := make(chan AskAnswer, 1)
	go func() {
		ans, _ := b.Wait(id2, ch2)
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
	tiny := newAskBroker(30 * time.Millisecond)
	id3, ch3 := tiny.register("s3", AskRequest{})
	ans3, err := tiny.Wait(id3, ch3)
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
	tool := newAskUserTool(buildContext{
		asker: func(ctx context.Context, req AskRequest) (AskAnswer, error) {
			captured = req
			return AskAnswer{Answers: []AskItem{
				{Selected: []string{"Zustand"}, Text: "顺便用 immer"},
				{Text: "预发环境"},
			}}, nil
		},
	})

	out, err := tool.Execute(context.Background(), map[string]interface{}{
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
	skipped := newAskUserTool(buildContext{
		asker: func(ctx context.Context, req AskRequest) (AskAnswer, error) {
			return AskAnswer{Cancelled: true}, nil
		},
	})
	if _, err := skipped.Execute(context.Background(), map[string]interface{}{
		"questions": []interface{}{map[string]interface{}{"question": "q"}},
	}); err == nil || !strings.Contains(err.Error(), "没有作答") {
		t.Fatalf("跳过时应返回可读错误，实际: %v", err)
	}

	// 界面未就绪（asker 为 nil）：明确报错，不静默成功
	noUI := newAskUserTool(buildContext{})
	if _, err := noUI.Execute(context.Background(), map[string]interface{}{
		"questions": []interface{}{map[string]interface{}{"question": "q"}},
	}); err == nil {
		t.Fatal("没有界面支持时应报错")
	}

	// 参数非法时不打扰用户（不应调用 asker）
	called := false
	strict := newAskUserTool(buildContext{
		asker: func(ctx context.Context, req AskRequest) (AskAnswer, error) {
			called = true
			return AskAnswer{}, nil
		},
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

// ask_user 必须直出给模型、能从两条路径执行，并且不触发权限二次弹窗
func TestAskUserAssembledAndAllowed(t *testing.T) {
	tm, dir := newTestToolManager(t)
	askCalls := 0
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		SessionID:  "s1",
		Enforcer:   AllowAllEnforcer{},
		Ask: func(ctx context.Context, req AskRequest) (AskAnswer, error) {
			askCalls++
			return AskAnswer{Answers: []AskItem{{Selected: []string{"选项A"}}}}, nil
		},
	})

	names := map[string]bool{}
	var askDef *LLMTool
	for _, d := range view.GetToolsForLLM() {
		names[d.Function.Name] = true
		if d.Function.Name == toolAskUser {
			def := d
			askDef = &def
		}
	}
	if !names[toolAskUser] {
		t.Fatalf("ask_user 应直出给模型，实际: %v", names)
	}
	if askDef == nil || len(schemaRequired(t, askDef.Function.Parameters)) == 0 {
		t.Fatalf("ask_user 的必填参数缺失: %+v", askDef)
	}

	args := map[string]interface{}{
		"questions": []interface{}{map[string]interface{}{"question": "选哪个？"}},
	}
	if _, err := view.ExecuteTool(toolAskUser, args); err != nil {
		t.Fatalf("直调 ask_user 失败: %v", err)
	}
	if _, err := view.ExecuteTool("tool_router", map[string]interface{}{
		"action": "execute", "tool_name": toolAskUser, "arguments": args,
	}); err != nil {
		t.Fatalf("经 tool_router 调 ask_user 失败: %v", err)
	}
	if askCalls != 2 {
		t.Fatalf("两条路径都应触达回路，实际 %d 次", askCalls)
	}

	// 权限判定：ask_user 无系统副作用，应直接放行（否则会出现"提问前先弹授权"的双弹窗）
	subject := newToolSubject(toolAskUser, args)
	v := Authorize(AuthorizeInput{Subject: subject, Mode: ModeManual, ProjectDir: dir})
	if v.Decision != DecisionAllow {
		t.Fatalf("ask_user 不应被权限拦下，实际 %v（stage=%s）", v.Decision, v.Stage)
	}
}

// 界面未就绪时必须立刻返回错误，而不是让工具调用白等 10 分钟超时
func TestAskUserWithoutUIFailsFast(t *testing.T) {
	app := &App{}
	app.ensurePermissionState() // 只初始化回路，不设 a.ctx（模拟界面未就绪）

	start := time.Now()
	_, err := app.askUser(context.Background(), "s1", AskRequest{Questions: []AskQuestion{{Question: "q"}}})
	if err == nil {
		t.Fatal("界面未就绪时应立刻报错")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("应快速失败，实际耗时 %s", elapsed)
	}
	if app.askBroker.Pending("s1") != nil {
		t.Fatal("失败路径不应留下挂起记录")
	}
}

// 无挂起提问时取消应报错；有挂起时应能取消（前端"跳过"按钮走这条）
func TestCancelAskUser(t *testing.T) {
	app := &App{}
	app.ensurePermissionState()
	app.ctx = context.Background() // 仅用于让 askUser 不因缺少界面而提前返回

	if err := app.CancelAskUser("s1"); err == nil {
		t.Fatal("没有挂起提问时应报错")
	}

	id, ch := app.askBroker.register("s1", AskRequest{Questions: []AskQuestion{{Question: "q"}}})
	_ = id
	done := make(chan AskAnswer, 1)
	go func() {
		ans, _ := app.askBroker.Wait(id, ch)
		done <- ans
	}()
	time.Sleep(20 * time.Millisecond)
	if err := app.CancelAskUser("s1"); err != nil {
		t.Fatalf("取消挂起提问应成功: %v", err)
	}
	select {
	case ans := <-done:
		if !ans.Cancelled {
			t.Fatalf("被取消的提问应返回 Cancelled，实际 %+v", ans)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后等待方应立即返回")
	}
}
