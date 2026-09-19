//go:build !windows

package main

import "os/exec"

// hideConsoleWindow 在非 Windows 上是空操作：类 Unix 没有"子进程控制台窗口"这回事
// （终端的显示由父进程的 tty 决定，GUI 程序启动的子进程本来就不显示）。
//
// 有意的空实现，而不是在调用点写 `if runtime.GOOS == "windows"`：调用点保持一行、
// 与平台无关，将来新增子进程调用点时照抄即可——不会因为漏写那次判断而在 Windows 上闪黑框，
// 也让"每个 spawn 点都必须调用它"这条结构性测试在 macOS/Linux 上照样能跑（见 procexec_test.go）。
func hideConsoleWindow(cmd *exec.Cmd) {}
