# new-reminder.ps1 -- 往 tasks.json 里追加一条「N 分钟后提醒」的一次性任务。
#
# 为什么需要它：Tasks 面板目前没有界面入口（前端任何地方都没有调用
#   openPane(sid,'tasks')），所以没法从运行中的 app 里创建任务。
#
# 两条硬约束（决定了用法）：
#   1) 插件只在启动时读 tasks.json（无热加载）—— 写完必须重启 app 才会生效；
#   2) 插件在每次任务变更时会整文件回写 —— app 运行时改写文件可能被覆盖。
#   因此正确用法是：**先跑本脚本，再启动 app**，提醒会在启动后 N 分钟响。
#
# 触发时刻是绝对时间（插件不支持「相对启动时刻」）：
#   at = 运行本脚本的时刻 + N 分钟（+ S 秒）。
#   若在 at 之前启动，则准点响；若启动晚了，这条属于「错过」，
#   按 missedPolicy=catchup 会在启动瞬间补响一次（不会静默丢掉）。
#
# 用法：
#   powershell -ExecutionPolicy Bypass -File .\new-reminder.ps1
#   powershell -ExecutionPolicy Bypass -File .\new-reminder.ps1 -Minutes 10 -Title "喝水"
#   powershell -ExecutionPolicy Bypass -File .\new-reminder.ps1 -Seconds 50 -Title "上大号"
#   powershell -ExecutionPolicy Bypass -File .\new-reminder.ps1 -ReplaceAll      # 清掉旧任务只留这条
#   powershell -ExecutionPolicy Bypass -File .\new-reminder.ps1 -DryRun          # 只打印，不落盘

param(
    [int]$Minutes = 3,
    [int]$Seconds = 0,
    [string]$Title = '上厕所',
    [string]$Note = '',
    [string]$Level = 'L1',
    [int]$SnoozeMinutes = 5,
    [switch]$ReplaceAll,
    [switch]$DryRun,
    [switch]$Start
)

$ErrorActionPreference = 'Stop'

# 只给 -Seconds 时不要再叠加 -Minutes 的默认 3 分钟（否则 -Seconds 50 会变成 230 秒）。
if ($PSBoundParameters.ContainsKey('Seconds') -and -not $PSBoundParameters.ContainsKey('Minutes')) {
    $Minutes = 0
}

$lead = ($Minutes * 60) + $Seconds
if ($lead -lt 1) { throw '提醒提前量至少 1 秒（-Minutes 3 或 -Seconds 30）' }
if ($Title.Length -gt 50) { throw '-Title 超过 50 个字符（插件上限 maxTitleRunes）' }
if ($lead -lt 15) {
    Write-Output "提醒：只剩 $lead 秒，重启 app 大概来不及；晚了会走「错过补发」（启动瞬间响，不是启动后 $lead 秒）。"
}

$dir = Join-Path $env:USERPROFILE '.local-agent'
$path = Join-Path $dir 'tasks.json'
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)

# 文件不存在时的骨架：字段与插件 NewGlobalConfig 的取值一致。
$defaultFile = @'
{
  "version": 1,
  "settings": {
    "enabled": true,
    "paused": false,
    "defaultSnoozeMinutes": 10,
    "missedPolicy": "catchup",
    "dnd": { "enabled": false, "start": "", "end": "" },
    "maxConcurrency": 1,
    "notifyEnabled": true,
    "rateLimitPerTaskPerMinute": 1,
    "rateLimitGlobalPer5Minutes": 3,
    "foregroundOnlyInApp": true,
    "taskSoftLimit": 200,
    "unhandledWindowMinutes": 120
  },
  "tasks": []
}
'@

if (Test-Path $path) {
    $raw = [System.IO.File]::ReadAllText($path)
    if (-not $raw.Trim()) { $raw = $defaultFile }
} else {
    $raw = $defaultFile
    Write-Output "提示：$path 不存在，将按默认配置新建"
}
$file = $raw | ConvertFrom-Json

$running = Get-Process -Name 'wails-tmp' -ErrorAction SilentlyContinue
if ($running) {
    Write-Output "警告：app 正在运行（PID $($running.Id -join ',')）。"
    Write-Output "      这一步很关键：退出 app 时插件会把内存里的任务表回写覆盖磁盘（plugin.Stop -> store.Save），"
    Write-Output "      所以「运行中改写文件」的改动会在退出那一刻被冲掉。正确顺序是："
    Write-Output "        1) 先正常退出 app -> 2) 再跑本脚本 -> 3) 再启动 app（或用 -Start 让本脚本代启）。"
    Write-Output "      本次写入仍然执行，但在退出前不生效、退出时可能被覆盖。"
}

$now = Get-Date
$at = $now.AddSeconds($lead)

$bytes = New-Object byte[] 4
(New-Object System.Security.Cryptography.RNGCryptoServiceProvider).GetBytes($bytes)
$id = 'task_' + [System.BitConverter]::ToString($bytes).Replace('-', '').ToLower()

# 模板是 JSON 文本，先做最小转义，避免标题/备注里的引号把模板弄坏。
function Esc([string]$s) { return $s.Replace('\', '\\').Replace('"', '\"') }

$noteLine = ''
if ($Note) { $noteLine = '"note": "' + (Esc $note) + '",' + "`n    " }

$template = @'
  {
    "id": "__ID__",
    "title": "__TITLE__",
    __NOTE__"kind": "reminder",
    "trigger": {
      "kind": "once",
      "once": { "at": "__AT__" }
    },
    "enabled": true,
    "reminder": { "snoozeMinutes": __SNOOZE__ },
    "notify": { "level": "__LEVEL__" },
    "state": {},
    "createdAt": "__NOW__",
    "updatedAt": "__NOW__"
  }
'@

$taskJson = $template.Replace('__ID__', $id).
    Replace('__TITLE__', (Esc $Title)).
    Replace('__NOTE__', $noteLine).
    Replace('__AT__', $at.ToString('yyyy-MM-ddTHH:mm:sszzz')).
    Replace('__SNOOZE__', [string]$SnoozeMinutes).
    Replace('__LEVEL__', $Level).
    Replace('__NOW__', $now.ToString('yyyy-MM-ddTHH:mm:sszzz'))

$task = $taskJson | ConvertFrom-Json

if ($ReplaceAll) {
    $file.tasks = @($task)
} else {
    $file.tasks = @($file.tasks) + $task
}

$json = $file | ConvertTo-Json -Depth 10
# ConvertTo-Json 会把中文转成 \uXXXX：合法但没法读，这里还原成字符（只动 \uXXXX，其余转义原样保留）。
$json = [regex]::Replace($json, '\\u([0-9a-fA-F]{4})', { param($m) [string][char][int]('0x' + $m.Groups[1].Value) })

if ($DryRun) {
    Write-Output '----- DryRun：不写文件，内容如下 -----'
    Write-Output $json
    exit 0
}

if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }

# 原子写：先写临时文件再改名；UTF-8 不带 BOM（Go 的 json 解析器不接受 BOM）。
$tmp = $path + '.tmp'
[System.IO.File]::WriteAllText($tmp, $json, $utf8NoBom)
Move-Item -Force $tmp $path

# 回读校验：解析得通、条数对得上，才算写成功。
$back = ([System.IO.File]::ReadAllText($path)) | ConvertFrom-Json
$n = @($back.tasks).Count
if ($n -ne @($file.tasks).Count) { throw "回读校验失败：期望 $(@($file.tasks).Count) 条，读到 $n 条" }

Write-Output "已写入 $path"
Write-Output "  任务: $Title（id=$id，kind=reminder，once）"
Write-Output "  触发: $($at.ToString('HH:mm:ss'))（现在 $($now.ToString('HH:mm:ss'))，提前 $lead 秒）"
Write-Output "  任务数: $n"
Write-Output ''
Write-Output '接下来：退出当前 app（若在跑）→ 重新启动它。'
Write-Output '  启动后可用 .\run-app.ps1 看日志，会打印 [desktop-plugin] 已启动: ...'
Write-Output '  到点若系统通知被拦，插件会降级为应用内提醒（日志里会有 notify-send-failed）。'

if ($Start) {
    if ($running) {
        Write-Output ''
        Write-Output "未能自动启动：app 仍在运行（PID $($running.Id -join ',')）。先退出它，再重跑本脚本。"
        Write-Output '（若强行再启一个实例，会有两个调度器各自读同一份文件、重复弹通知。）'
        exit 0
    }
    $exe = Join-Path (Split-Path -Parent $MyInvocation.MyCommand.Path) 'build\bin\wails-tmp.exe'
    if (-not (Test-Path $exe)) { throw "找不到 exe：$exe（先 wails build）" }
    Start-Process -FilePath $exe -WorkingDirectory (Split-Path -Parent $exe) | Out-Null
    Write-Output ''
    Write-Output "已启动 $exe —— 提醒会在 $($at.ToString('HH:mm:ss')) 弹（约 $lead 秒后）。"
}
