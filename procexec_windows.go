//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// createNoWindow 是 CreateProcess 的 CREATE_NO_WINDOW 标志（winbase.h）。
// 没有用 syscall 里的同名常量——那里没有导出它，只能写字面量。
const createNoWindow = 0x08000000

// hideConsoleWindow 让子进程不要在 Windows 上弹出控制台窗口。
//
// 为什么需要：Wails 在 Windows 上默认用 **GUI 子系统**（-H windowsgui）构建，
// 本项目自身**没有控制台**。这时它启动的任何控制台程序（powershell / git / node /
// python / 批处理脚本）都拿不到可继承的控制台，Windows 会为它**新建一个控制台窗口**
// 并显示出来——表现为每执行一次工具就闪一次黑框，exec_shell 尤其明显（它每次都要
// 起一个 powershell）。同理，checkpoint 每次取快照都会起 git。
//
// 两个开关一起给，各管一半：
//   - CreationFlags=CREATE_NO_WINDOW：**根本不创建控制台窗口**。这是主力。
//     另一个附带好处是子进程仍然拥有一个"无窗口的控制台"，于是它再启动的控制台程序
//     （powershell 里跑 python、跑 build 脚本）会**继承**这个控制台，不会各自再弹一个。
//   - HideWindow：设 STARTF_USESHOWWINDOW + SW_HIDE，兜住那些自己调 ShowWindow 的程序。
//
// 两者都不影响管道读写（stdout/stderr 仍是管道），也不影响 Ctrl+C/取消——
// 取消走的是 CommandContext 的 kill，与此无关。
//
// ⚠️ 新增任何 exec.Command / exec.CommandContext 调用点都必须调用它：漏一处就在
// Windows 上多闪一个黑框，而这种缺陷在 macOS/Linux 上开发时**完全看不见**。
// procexec_test.go 里有一条结构性测试扫全包源码守着这件事。
func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	attr := cmd.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	attr.HideWindow = true
	attr.CreationFlags |= createNoWindow
	cmd.SysProcAttr = attr
}
