# README
# local-agent
golang local agent

## About

This is the official Wails Vue template.

You can configure the project by editing `wails.json`. More information about the project settings can be found
here: https://wails.io/docs/reference/project-config

## Live Development

To run in live development mode, run `wails dev` in the project directory. This will run a Vite development
server that will provide very fast hot reload of your frontend changes. If you want to develop in a browser
and have access to your Go methods, there is also a dev server that runs on http://localhost:34115. Connect
to this in your browser, and you can call your Go code from devtools.

## Building

To build a redistributable, production mode package, use `wails build`.

## Windows 打包注意：子进程不要弹控制台窗口

`wails build` 在 Windows 上默认按 **GUI 子系统**构建（等价于 `-H windowsgui`），程序自身没有控制台。
这时它启动的任何控制台程序（`powershell`、`git`、`node`、脚本…）都会被 Windows **新建一个控制台窗口**，
表现为每执行一次工具闪一个黑框——`exec_shell`（每次都起一个 powershell）与 checkpoint（每轮都起 git）
最明显。macOS / Linux 上没有这个现象，所以这种缺陷在开发机上完全看不见。

**约定：所有拉起子进程的地方，构造完 `cmd` 后必须调用 `hideConsoleWindow(cmd)`**
（实现在 `procexec_windows.go`，非 Windows 是空操作；新增调用点照抄一行即可）。
`procexec_test.go` 里有一条结构性测试扫全包源码守着这条约定，在任意平台上都会跑。
