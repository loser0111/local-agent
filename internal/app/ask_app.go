package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/tool"
)

// ===== ask_user 的 App 侧：发事件、阻塞等待、接收答复 =====
//
// 引擎（数据结构、回路、工具）已下沉到 internal/agent：见 internal/agent/ask.go。
// 这里只留必须碰 App 的四个方法——它们要发 Wails 事件、要看 a.ctx 是否就绪，
// 而 Wails 绑定命名空间是 main.App，绑定方法不能搬走。

// toolAskUser 工具名（直出给模型，见 internal/tool 的 DirectToolOrder）
const toolAskUser = tool.ToolAskUser

// askUser 向前端推送提问并阻塞等待用户作答
func (a *App) askUser(ctx context.Context, sessionID string, req agent.AskRequest) (agent.AskAnswer, error) {
	a.ensurePermissionState()
	if a.ctx == nil {
		return agent.AskAnswer{}, fmt.Errorf("界面尚未就绪，无法向用户提问")
	}
	id, ch := a.askBroker.Register(sessionID, req)
	req.ID = id
	req.SessionID = sessionID
	req.CreatedAt = time.Now().UnixMilli()
	// 请求本体在 register 后补全：Pending() 需要能返回带 ID 的完整请求
	a.askBroker.SetRequest(id, req)

	a.emitInteraction(sessionID, ChatEvent{Type: "ask_user", Ask: &req})
	return a.askBroker.Wait(ctx, id, ch)
}

// ResolveAskUser 前端提交答复（bound 方法）
func (a *App) ResolveAskUser(ans agent.AskAnswer) error {
	a.ensurePermissionState()
	if strings.TrimSpace(ans.ID) == "" {
		return fmt.Errorf("提问 ID 不能为空")
	}
	return a.askBroker.Resolve(ans)
}

// CancelAskUser 取消该会话挂起的提问（用户点停止 / 关闭弹窗）
func (a *App) CancelAskUser(sessionID string) error {
	a.ensurePermissionState()
	if n := a.askBroker.CancelSession(sessionID); n == 0 {
		return fmt.Errorf("当前没有等待作答的提问")
	}
	return nil
}

// GetPendingAsk 返回该会话挂起的提问（切回该会话时重新弹窗用）；没有则返回 nil
func (a *App) GetPendingAsk(sessionID string) *agent.AskRequest {
	a.ensurePermissionState()
	return a.askBroker.Pending(sessionID)
}
