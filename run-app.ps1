# run-app.ps1 —— 「带日志」启动修复版 app，用于排查「窗口自己关了」
#
# 为什么需要它：
#   本 app 是 GUI 子系统程序，双击启动时 stdout / stderr 无处可去。
#   一旦有后台 goroutine panic（典型：wails 通知子系统没初始化就往 nil map 写，
#   见 taskhost.go 的 ensureNotifications 注释），进程会直接消失（退出码 2），
#   界面上只看到「窗口自己关了」，日志、弹窗、事件日志里都没有线索 ——
#   这正是之前「wails build 成功但程序起不来」难以定位的原因。
#
#   用本脚本启动：stdout 落 build\bin\app.out.log，stderr（panic 原文在这里）
#   落 build\bin\app.err.log，退出码与存活时长也记进这两个文件。
#
# 用法（仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File .\run-app.ps1
#
# 注意：务必跑 build\bin\wails-tmp.exe 这一份。
#   根目录那份 E:\learn\local-agent\wails-tmp.exe 是 2026-09-16 的旧产物，
#   不含桌面插件与通知初始化的修复；跑错文件会得到完全无关的现象。

$ErrorActionPreference = "Stop"

$root = $PSScriptRoot
if (-not $root) { $root = (Get-Location).Path }

$exe = Join-Path $root "build\bin\wails-tmp.exe"
$outLog = Join-Path $root "build\bin\app.out.log"
$errLog = Join-Path $root "build\bin\app.err.log"

if (-not (Test-Path $exe)) {
    Write-Host "找不到 $exe —— 先执行 wails build"
    exit 1
}

$exeInfo = Get-Item $exe
Write-Host "启动   : $exe"
Write-Host "构建   : $($exeInfo.LastWriteTime)"
Write-Host "stdout : $outLog"
Write-Host "stderr : $errLog  (panic 栈在这里)"
Write-Host "提示   : 关掉窗口 = 进程结束（还没有托盘常驻，不会后台续跑）"
Write-Host ""

# 每次启动重写日志：只看本次会话的现场，避免多次运行互相污染。
$header = "===== {0}  启动 {1}（构建于 {2}）=====" -f (Get-Date -Format o), $exe, $exeInfo.LastWriteTime
Set-Content -Path $outLog -Value $header -Encoding UTF8
Set-Content -Path $errLog -Value $header -Encoding UTF8

$sw = [System.Diagnostics.Stopwatch]::StartNew()
$proc = Start-Process -FilePath $exe -PassThru -NoNewWindow -RedirectStandardOutput $outLog -RedirectStandardError $errLog
$proc.WaitForExit()
$sw.Stop()

$tail = "===== {0}  退出，exit code = {1}，存活 {2:N1} 秒 =====" -f (Get-Date -Format o), $proc.ExitCode, $sw.Elapsed.TotalSeconds
Add-Content -Path $outLog -Value $tail -Encoding UTF8
Add-Content -Path $errLog -Value $tail -Encoding UTF8

Write-Host ""
Write-Host $tail
if ($proc.ExitCode -eq 0) {
    Write-Host "exit code 0 = 正常退出（通常是点了窗口的 X 或 Alt+F4）"
} else {
    Write-Host "非 0 退出 = 被异常终止（Go panic 的退出码是 2）；看下面两个日志的尾部"
}
Write-Host ""
Write-Host "----- app.err.log 尾部（stderr / panic）-----"
Get-Content -Path $errLog -Tail 40 -ErrorAction SilentlyContinue
Write-Host ""
Write-Host "----- app.out.log 尾部（插件日志）-----"
Get-Content -Path $outLog -Tail 40 -ErrorAction SilentlyContinue
