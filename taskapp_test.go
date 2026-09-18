package main

import (
	"testing"

	"wails-tmp/plugin"
)

// ===== 主程序侧桥接与装配的契约测试 =====
//
// 这些用例刻意**不依赖 wails 运行时**（不起窗口、不发通知、不推事件）：
// 只验证「应用未就绪时不得 panic、不得误报可用」以及在无插件时行为可控。

func TestAppHostImplementsHostContract(t *testing.T) {
	var h plugin.Host = newAppHost(&App{baseDir: "C:/tmp/.local-agent"})

	if got := h.BaseDir(); got != "C:/tmp/.local-agent" {
		t.Errorf("BaseDir() = %q", got)
	}
	if h.AppVersion() != appVersion {
		t.Errorf("AppVersion() = %q, want %q", h.AppVersion(), appVersion)
	}

	// 应用尚未就绪（ctx 为空）：不得 panic，也不得误报通知可用。
	if h.NotifyAvailable() {
		t.Error("应用未就绪时 NotifyAvailable 应为 false")
	}
	if err := h.Notify(plugin.NotifyRequest{ID: "task:t1:tr1"}); err == nil {
		t.Error("应用未就绪时发送通知应返回错误（供调用方降级）")
	}
	if err := h.RegisterCategories(nil); err == nil {
		t.Error("应用未就绪时注册分类应返回错误")
	}
	h.RevealWindow(plugin.RevealPayload{TaskID: "t1"}) // 应静默返回
	h.Emit(plugin.EventChannel, plugin.Event{})        // 应静默返回
	h.Emit("chat:event", plugin.Event{})               // 契约守卫：非插件通道被拒绝
}

func TestAppHostSubscribeRuns(t *testing.T) {
	host := newAppHost(&App{})
	var got []plugin.RunEvent
	host.SubscribeRuns(func(ev plugin.RunEvent) { got = append(got, ev) })
	host.notifyRunEvent(plugin.RunEvent{RunID: "r1", TaskID: "t1"})

	if len(got) != 1 || got[0].RunID != "r1" {
		t.Fatalf("运行事件广播失败: %+v", got)
	}
}

func TestTaskPluginInfoUnavailableWithoutPlugin(t *testing.T) {
	a := &App{baseDir: "C:/tmp/.local-agent"}
	info := a.TaskPluginInfo()

	if info.State != "unavailable" {
		t.Errorf("State = %q, want unavailable", info.State)
	}
	if info.DataDir != a.baseDir {
		t.Errorf("DataDir = %q, want %q", info.DataDir, a.baseDir)
	}
	if info.AppVersion == "" {
		t.Error("AppVersion 不应为空")
	}
	if m := a.TaskPluginManifest(); m.ID != "" {
		t.Errorf("插件不可用时应返回空清单，实际 %+v", m)
	}
}

func TestStartDesktopPluginSkipsSafely(t *testing.T) {
	// nil App 与缺少数据目录两种情况都必须安全跳过：此时绝不能触碰 wails 运行时。
	startDesktopPlugin(nil)
	startDesktopPlugin(&App{})

	if p := currentDesktopPlugin(); p != nil {
		t.Errorf("跳过加载时不应留下插件实例: %+v", p.Info())
	}
	stopDesktopPlugin() // 无实例时停止不应 panic
}
