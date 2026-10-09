// 本文件是桌面插件（任务子系统）的**进程级生命周期**：装配、登记、停止。
//
// 与根包的边界：插件实例是进程级单例，但"怎么把宿主能力接上"是主程序的活——
// 宿主适配器（plugin.Host 的实现，见根目录 taskhost.go 的 appHost）要摸 wails runtime
// 与 App 的 ctx / baseDir。所以这里只认 plugin.Host 接口与几个字符串参数：
// **不认识 App，也不 import wails runtime**。
//
// 那不是巧合，是 internal/* 一直守着的一条线（本仓全部 internal 包对
// wails runtime 的 import 数为 0），也是 plugin/host.go 那句"由 main 侧实现"能成立的前提。
package task

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"

	"wails-tmp/plugin"
)

// 运行中的插件实例。
//
// 用包级变量而不是某个结构体字段：插件是「进程级单例」——托盘、调度循环、
// App 上的导出方法都必须拿到同一个实例，多一个实例等于多一份调度循环。
var state struct {
	mu sync.RWMutex
	p  *plugin.Plugin
}

// Current 返回已启动的插件实例，未启用时为 nil。
func Current() *plugin.Plugin {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.p
}

// Start 装载并启动桌面插件，成功后登记为进程级单例。
//
// 必须在宿主侧 startup 之后调用，因为宿主适配器依赖已初始化的 ctx 与数据目录。
//
// 整个装配过程包在 recover 里：wails 的 OnStartup 跑在独立 goroutine 上，
// 这里一旦 panic 就是进程级崩溃（窗口直接消失，用户看到的现象是「起不来」，
// 而 wails build 是成功的、不会报错）。插件出问题只能降级，不能带走主程序。
//
// 装配顺序：plugin.New（纯装配，不做 IO）→ plugin.Start（注册通知分类与回调、
// 订阅运行事件、广播 task:plugin:ready）。任一步失败都只记录并留 nil。
func Start(ctx context.Context, host plugin.Host, dataDir, appVersion string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[desktop-plugin] 装配发生 panic（插件功能不可用，主程序继续）: %v\n%s\n", r, debug.Stack())
		}
	}()
	if dataDir == "" {
		fmt.Println("[desktop-plugin] 跳过加载：数据目录不可用")
		return
	}

	p, err := plugin.New(plugin.Options{
		Host:       host,
		DataDir:    dataDir,
		AppVersion: appVersion,
	})
	if err != nil {
		fmt.Printf("[desktop-plugin] 初始化失败（插件功能将不可用）: %v\n", err)
		return
	}

	if err := p.Start(ctx); err != nil {
		fmt.Printf("[desktop-plugin] 启动失败（插件功能将不可用）: %v\n", err)
		return
	}

	state.mu.Lock()
	state.p = p
	state.mu.Unlock()

	info := p.Info()
	fmt.Printf("[desktop-plugin] 已注册: %s\n", info.String())
}

// Stop 停止插件。供退出流程调用（骨架阶段仅 App 退出时兜底）。
//
// 只停止、不从单例摘除：正在退出时没人会再去问 Current()。运行中需要换一份实例
// （重装配、测试复位）用 Reset。
func Stop() {
	state.mu.RLock()
	p := state.p
	state.mu.RUnlock()
	if p == nil {
		return
	}
	if err := p.Stop(); err != nil {
		fmt.Printf("[desktop-plugin] 停止失败: %v\n", err)
	}
}

// Reset 清空进程级单例（不停止插件，调用方自己决定先不先 Stop）。
//
// 两处需要它：
//   - 测试要保证每个用例都从「没有插件」的干净状态开始；
//   - 重装配场景要在旧实例已停止后把它摘掉——已被停掉的插件若还留在单例里，
//     界面会把它报成「已启用」，而每次调用都只会拿到 ErrNotRunning。
func Reset() {
	state.mu.Lock()
	state.p = nil
	state.mu.Unlock()
}
