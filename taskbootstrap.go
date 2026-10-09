package main

import (
	"fmt"

	"wails-tmp/internal/task"
	"wails-tmp/plugin"
)

// ===== 桌面插件的装配入口（宿主侧）=====
//
// 进程级生命周期（单例 + plugin.New/Start/Stop + 装配期 recover）已下沉
// `internal/task`；这里只剩**必须摸 App** 的那一层：把 App 的 ctx 与 baseDir 交出去，
// 并校验它们已就绪。宿主适配器 appHost（plugin.Host 的实现，要调 wails runtime）
// 按设计留在根包 taskhost.go，见 plugin/host.go 顶部关于依赖方向的说明。
//
// 为什么装配放在这里，而不是 App.startup 里？
//   本仓库既有 .go 文件多为 CRLF 行尾，改动集中在新文件里可以把对既有文件的
//   侵入降到最低：主程序侧只需在 main.go 的 OnStartup 里多调一次 startDesktopPlugin。

// startDesktopPlugin 装载并启动桌面插件。必须在 app.startup 之后调用，
// 因为宿主适配器依赖 App 已初始化的 ctx 与 baseDir。
//
// 装配期一旦 panic 会带走整个进程（wails 的 OnStartup 跑在独立 goroutine 上），
// 所以 recover 在 task.Start 里兜着；这里只做前置校验，把 App 的两种"未就绪"
// 说清楚——跳过加载是正常降级，不是错误。
func startDesktopPlugin(app *App) {
	if app == nil {
		fmt.Println("[desktop-plugin] 跳过加载：App 为空")
		return
	}
	task.Start(app.ctx, newAppHost(app), app.baseDir, appVersion)
}

// currentDesktopPlugin 返回已启动的插件实例，未启用时为 nil。
// taskapp.go 的 18 个导出方法都靠它判断"插件是否可用"。
func currentDesktopPlugin() *plugin.Plugin { return task.Current() }

// stopDesktopPlugin 停止插件。供退出流程调用（骨架阶段仅 App 退出时兜底）。
func stopDesktopPlugin() { task.Stop() }
