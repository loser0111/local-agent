package main

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"wails-tmp/plugin"
)

// ===== 桌面插件 ←→ 主程序的桥接层 =====
//
// 本文件是 plugin.Host 的唯一实现，也是插件与主程序之间唯一允许的耦合点。
// 这里只做「翻译」：把插件的能力请求翻译成主程序既有的调用
// （wails runtime、agentRun/runToolLoop、runRegistry、EventsEmit），
// 不承载任何业务逻辑 —— 业务逻辑都在 plugin/ 子包内。

// appVersion 是主程序版本号，供插件记录与兼容性判断。
// 后续可由 -ldflags "-X main.appVersion=..." 注入。
var appVersion = "0.1.0"

// appHost 把 App 的能力暴露给插件。
type appHost struct {
	app *App

	mu   sync.RWMutex
	subs []func(plugin.RunEvent)
}

// 通知子系统只初始化一次（进程级）。
//
// 为什么必须显式初始化：wails 的 windows 实现里 categories 是**包级 map**，
// 只有 InitializeNotifications() 才会 make 出来（internal/frontend/desktop/
// windows/notifications.go:59）。未初始化就调 RegisterNotificationCategory，
// 相当于往 nil map 里写，直接 panic：
//
//	panic: assignment to entry in nil map
//	  .../windows.(*Frontend).RegisterNotificationCategory
//	  main.(*appHost).RegisterCategories  ← 本文件
//
// 而这行代码跑在 wails 的 OnStartup goroutine 里，panic 无人接管，
// 会带走整个进程（退出码 2）——表现就是「wails build 通过、程序起不来」。
var (
	notificationsOnce sync.Once
	notificationsErr  error
)

// ensureNotifications 幂等地初始化通知子系统，返回首次初始化的结果。
// 必须在任何通知类调用之前执行。
func (h *appHost) ensureNotifications() error {
	if h.app == nil || h.app.ctx == nil {
		return fmt.Errorf("通知子系统初始化失败: 应用尚未就绪")
	}
	notificationsOnce.Do(func() {
		notificationsErr = wailsRuntime.InitializeNotifications(h.app.ctx)
		if notificationsErr != nil {
			fmt.Printf("[desktop-plugin] 通知子系统初始化失败（通知将降级）: %v\n", notificationsErr)
		}
	})
	return notificationsErr
}

// newAppHost 构造宿主适配器。
func newAppHost(app *App) *appHost { return &appHost{app: app} }

// NotifyAvailable 报告本平台是否具备通知通道。
//
// 注意：Windows 上底层实现恒为 true（见 wails windows/notifications.go），
// 因此它的返回值只能说明「通道存在」，**不能**说明通知能送达；
// 真正的可用性判定以 Notify 的返回值为准（附录 F3.4）。
func (h *appHost) NotifyAvailable() bool {
	if h.app == nil || h.app.ctx == nil {
		return false
	}
	return wailsRuntime.IsNotificationAvailable(h.app.ctx)
}

// Notify 发送一条系统通知。
//
// 说明（已核对 wails v2.15 的 Windows 实现）：
//   - 动作按钮来自「已注册的分类」，而不是本次请求的参数；
//     因此这里用 CategoryID 表达按钮集合，请求里的 Actions 仅作日志参考。
//   - 分类不存在时 wails 会自动退化为无按钮通知，不会失败。
//   - 返回错误即 Push 失败，调用方据此降级为应用内提醒（被动降级）。
func (h *appHost) Notify(req plugin.NotifyRequest) error {
	if h.app == nil || h.app.ctx == nil {
		return fmt.Errorf("通知失败: 应用尚未就绪")
	}
	// 通知的发送与分类注册依赖同一套初始化（appName / toast 激活器）。
	if err := h.ensureNotifications(); err != nil {
		return fmt.Errorf("通知失败: %w", err)
	}

	opts := wailsRuntime.NotificationOptions{
		ID:         req.ID,
		Title:      req.Title,
		Body:       req.Body,
		CategoryID: req.Category,
	}
	if len(req.Data) > 0 {
		data := make(map[string]interface{}, len(req.Data))
		for k, v := range req.Data {
			data[k] = v
		}
		opts.Data = data
	}

	var err error
	if opts.CategoryID != "" {
		err = wailsRuntime.SendNotificationWithActions(h.app.ctx, opts)
	} else {
		err = wailsRuntime.SendNotification(h.app.ctx, opts)
	}
	if err != nil {
		fmt.Printf("[desktop-plugin] 通知发送失败(id=%s level=%s): %v\n", req.ID, req.Level, err)
		return err
	}
	return nil
}

// RegisterCategories 注册通知分类（动作按钮）。
//
// 逐个注册并聚合错误：一个分类失败不应连带让其余分类也失去按钮
// （Windows 实现里注册会写 HKCU，失败是可能的）。
func (h *appHost) RegisterCategories(cats []plugin.NotifyCategory) error {
	if h.app == nil || h.app.ctx == nil {
		return fmt.Errorf("注册通知分类失败: 应用尚未就绪")
	}
	// 必须先初始化通知子系统：否则下面的 RegisterNotificationCategory
	// 会写 wails 的 nil categories map 并 panic（见 ensureNotifications 注释）。
	if err := h.ensureNotifications(); err != nil {
		return fmt.Errorf("注册通知分类失败: %w", err)
	}
	var failed []string
	for _, c := range cats {
		actions := make([]wailsRuntime.NotificationAction, 0, len(c.Actions))
		for _, a := range c.Actions {
			actions = append(actions, wailsRuntime.NotificationAction{ID: a.ID, Title: a.Label})
		}
		err := wailsRuntime.RegisterNotificationCategory(h.app.ctx, wailsRuntime.NotificationCategory{
			ID:               c.ID,
			Actions:          actions,
			HasReplyField:    c.HasReplyField,
			ReplyPlaceholder: c.ReplyPlaceholder,
			ReplyButtonTitle: c.ReplyButtonTitle,
		})
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", c.ID, err))
			continue
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("部分通知分类注册失败: %v", failed)
	}
	return nil
}

// OnNotifyResponse 注册通知响应回调。
func (h *appHost) OnNotifyResponse(fn func(plugin.NotifyResponse)) {
	if h.app == nil || h.app.ctx == nil || fn == nil {
		return
	}
	// 先初始化（幂等），再挂回调：保证回调挂在已就绪的通知通道上。
	if err := h.ensureNotifications(); err != nil {
		fmt.Printf("[desktop-plugin] 通知回调挂载前初始化失败: %v\n", err)
	}
	wailsRuntime.OnNotificationResponse(h.app.ctx, func(result wailsRuntime.NotificationResult) {
		// 这个闭包跑在 wails 的通知 goroutine 上，panic 无人接管 ——
		// 会直接带走整个进程（退出码 2），用户看到的现象是「点了一下通知按钮，
		// 窗口就没了」，而且没有日志、没有弹窗、事件日志里也没有线索。
		//
		// 通知按钮最多只能影响任务状态，绝不能掀翻主程序，所以这里自己兜住：
		// 与 startDesktopPlugin（装配期）、Scheduler.fire（调度期）、
		// Plugin.fireSafely（触发期）一起，构成四条「panic 不得穿透」的边界。
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("[desktop-plugin] 通知响应处理发生 panic（已隔离，主程序继续）: %v\n%s\n", r, debug.Stack())
			}
		}()
		if result.Error != nil {
			fmt.Printf("[desktop-plugin] 通知响应解析失败: %v\n", result.Error)
			return
		}
		resp := result.Response
		out := plugin.NotifyResponse{
			ID:       resp.ID,
			ActionID: resp.ActionIdentifier,
			Category: resp.CategoryID,
			Text:     resp.UserText,
		}
		if len(resp.UserInfo) > 0 {
			out.Data = make(map[string]string, len(resp.UserInfo))
			for k, v := range resp.UserInfo {
				out.Data[k] = fmt.Sprintf("%v", v)
			}
		}
		fn(out)
	})
}

// RevealWindow 显示并聚焦主窗口，并通知前端定位到目标任务（F3.3）。
func (h *appHost) RevealWindow(payload plugin.RevealPayload) {
	if h.app == nil || h.app.ctx == nil {
		return
	}
	wailsRuntime.WindowShow(h.app.ctx)
	wailsRuntime.WindowUnminimise(h.app.ctx)
	h.Emit(plugin.EventChannel, plugin.Event{
		Type:    plugin.EventReveal,
		Payload: payload,
	})
}

// RunAgent 以独立会话跑一次 agent。
//
// 骨架阶段尚未接入：第 6 步会复用既有执行链（agentRun + runToolLoop +
// runRecorder，参照 runSubagent 的装配方式：独立 sessionID、不写用户会话、
// 无人值守权限策略、串行并发 1、超时走既有取消链路）。
func (h *appHost) RunAgent(ctx context.Context, req plugin.RunRequest) (plugin.RunResult, error) {
	return plugin.RunResult{}, plugin.ErrNotImplemented
}

// CancelRun 按 runID 取消一次运行（第 6 步接 runRegistry / runControl）。
func (h *appHost) CancelRun(runID string) error {
	return plugin.ErrNotImplemented
}

// SubscribeRuns 订阅运行生命周期事件。
func (h *appHost) SubscribeRuns(fn func(plugin.RunEvent)) {
	if fn == nil {
		return
	}
	h.mu.Lock()
	h.subs = append(h.subs, fn)
	h.mu.Unlock()
}

// notifyRunEvent 把运行事件广播给已订阅的插件。
//
// 第 6 步在运行生命周期（开始/结束/失败）处调用本方法。
func (h *appHost) notifyRunEvent(ev plugin.RunEvent) {
	h.mu.RLock()
	subs := make([]func(plugin.RunEvent), len(h.subs))
	copy(subs, h.subs)
	h.mu.RUnlock()
	for _, fn := range subs {
		fn(ev)
	}
}

// Emit 经桌面插件的事件通道推送事件。
//
// 契约守卫：插件只允许往 task:event 上推，防止误用 chat:event / diff:update
// 造成前端监听串台。
func (h *appHost) Emit(name string, payload any) {
	if h.app == nil || h.app.ctx == nil {
		return
	}
	if name != plugin.EventChannel {
		fmt.Printf("[desktop-plugin] 拒绝非插件通道的事件: %s\n", name)
		return
	}
	wailsRuntime.EventsEmit(h.app.ctx, name, payload)
}

// BaseDir 返回本地数据目录（~/.local-agent）。
func (h *appHost) BaseDir() string {
	if h.app == nil || h.app.baseDir == "" {
		return "."
	}
	return h.app.baseDir
}

// AppVersion 返回主程序版本。
func (h *appHost) AppVersion() string { return appVersion }

// 编译期断言：appHost 必须完整实现 plugin.Host。
var _ plugin.Host = (*appHost)(nil)
