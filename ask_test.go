package main

import (
	"context"
	"testing"
	"time"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/permission"
	"wails-tmp/internal/tool"
)

// ===== ask_user：App 侧接线 =====
//
// 引擎侧的用例（参数解析、回路 BUSY/超时/取消、工具的三种失败路径、结果格式化）
// 已随包迁到 internal/agent/ask_internal_test.go——那里可以直接断言未导出函数。
// 这里只留"必须经过 App 才能验证"的部分：装配后两条调用路径都通、界面未就绪快进失败、取消挂起提问。

// ask_user 必须直出给模型、能从两条路径执行，并且不触发权限二次弹窗
func TestAskUserAssembledAndAllowed(t *testing.T) {
	tm, dir := newTestToolManager(t)
	askCalls := 0
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		SessionID:  "s1",
		Enforcer:   tool.AllowAllEnforcer{},
		Ask: func(ctx context.Context, req agent.AskRequest) (agent.AskAnswer, error) {
			askCalls++
			return agent.AskAnswer{Answers: []agent.AskItem{{Selected: []string{"选项A"}}}}, nil
		},
	})

	names := map[string]bool{}
	var askDef *llm.LLMTool
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
	subject := permission.NewToolSubject(toolAskUser, args)
	v := permission.Authorize(permission.AuthorizeInput{Subject: subject, Mode: permission.ModeManual, ProjectDir: dir})
	if v.Decision != permission.DecisionAllow {
		t.Fatalf("ask_user 不应被权限拦下，实际 %v（stage=%s）", v.Decision, v.Stage)
	}
}

// 界面未就绪时必须立刻返回错误，而不是让工具调用白等 10 分钟超时
func TestAskUserWithoutUIFailsFast(t *testing.T) {
	app := &App{}
	app.ensurePermissionState() // 只初始化回路，不设 a.ctx（模拟界面未就绪）

	start := time.Now()
	_, err := app.askUser(context.Background(), "s1", agent.AskRequest{Questions: []agent.AskQuestion{{Question: "q"}}})
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

	id, ch := app.askBroker.Register("s1", agent.AskRequest{Questions: []agent.AskQuestion{{Question: "q"}}})
	_ = id
	done := make(chan agent.AskAnswer, 1)
	go func() {
		ans, _ := app.askBroker.Wait(context.Background(), id, ch)
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
