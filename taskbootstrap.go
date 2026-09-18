package main

import (
	"fmt"
	"sync"

	"wails-tmp/plugin"
)

// ===== 桌面插件的装配入口 =====
//
// 为什么装配放在这里，而不是 App.startup 里？
//   本仓库既有 .go 文件多为 CRLF 行尾，改动集中在新文件里可以把对既有文件的
//   侵入降到最低：主程序侧只需在 main.go 的 OnStartup 里多调一次 startDesktopPlugin。
//
// 装配顺序：manifest（内嵌清单，纯解析）→ New（纯装配，不做 IO）
//        → Start（注册通知分类与回调、订阅运行事件、广播 task:plugin:ready）。
//
// 任一步失败都只记录并留 nil，插件异常绝不影响主程序其余功能（附录 §6.5）。

// desktopPluginState 持有插件实例。
//
// 用包级变量而非 App 字段：插件是「进程级单例」，且 App 结构体在 CRLF 文件中，
// 集中在这里便于后续步骤（托盘、调度循环）共用同一实例。
var desktopPluginState struct {
	mu sync.RWMutex
	p  *plugin.Plugin
}

// currentDesktopPlugin 返回已启动的插件实例，未启用时为 nil。
func currentDesktopPlugin() *plugin.Plugin {
	desktopPluginState.mu.RLock()
	defer desktopPluginState.mu.RUnlock()
	return desktopPluginState.p
}

// startDesktopPlugin 装载并启动桌面插件。必须在 app.startup 之后调用，
// 因为宿主适配器依赖 App 已初始化的 ctx 与 baseDir。
func startDesktopPlugin(app *App) {
	if app == nil {
		fmt.Println("[desktop-plugin] 跳过加载：App 为空")
		return
	}

	// 数据目录：App.startup 已解析并创建（~/.local-agent）。
	baseDir := app.baseDir
	if baseDir == "" {
		fmt.Println("[desktop-plugin] 跳过加载：数据目录不可用")
		return
	}

	p, err := plugin.New(plugin.Options{
		Host:       newAppHost(app),
		DataDir:    baseDir,
		AppVersion: appVersion,
	})
	if err != nil {
		fmt.Printf("[desktop-plugin] 初始化失败（插件功能将不可用）: %v\n", err)
		return
	}

	if err := p.Start(app.ctx); err != nil {
		fmt.Printf("[desktop-plugin] 启动失败（插件功能将不可用）: %v\n", err)
		return
	}

	desktopPluginState.mu.Lock()
	desktopPluginState.p = p
	desktopPluginState.mu.Unlock()

	info := p.Info()
	fmt.Printf("[desktop-plugin] 已注册: %s\n", info.String())
}

// stopDesktopPlugin 停止插件。供退出流程调用（骨架阶段仅 App 退出时兜底）。
func stopDesktopPlugin() {
	p := currentDesktopPlugin()
	if p == nil {
		return
	}
	if err := p.Stop(); err != nil {
		fmt.Printf("[desktop-plugin] 停止失败: %v\n", err)
	}
}
