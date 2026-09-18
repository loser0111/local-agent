Set-Location 'e:\learn\local-agent'
try {
    $path = Join-Path $env:USERPROFILE '.local-agent\tasks.json'
    Write-Output ('PATH=' + $path)
    $json = @'
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
  "tasks": [
    {
      "id": "task_7e913330",
      "title": "\u63d0\u4ea4\u4ee3\u7801",
      "kind": "reminder",
      "trigger": { "kind": "once", "once": { "at": "2026-09-18T20:38:53+08:00" } },
      "enabled": true,
      "reminder": { "snoozeMinutes": 5 },
      "notify": { "level": "L1" },
      "state": {},
      "createdAt": "2026-09-18T20:35:53+08:00",
      "updatedAt": "2026-09-18T20:35:53+08:00"
    }
  ]
}
'@
    Write-Output ('JSON_LEN=' + $json.Length)
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($path, $json, $utf8NoBom)
    Write-Output 'WRITE_OK'
    Start-Sleep -Seconds 3
    $b = [System.IO.File]::ReadAllBytes($path)
    Write-Output ('BOM=' + ($b[0] -eq 0xEF) + ' SIZE=' + $b.Length)
    $j = ([System.IO.File]::ReadAllText($path)) | ConvertFrom-Json
    $t = $j.tasks[0]
    Write-Output ('COUNT=' + @($j.tasks).Count + ' ID=' + $t.id)
    Write-Output ('AT=' + $t.trigger.once.at + ' ENABLED=' + $t.enabled)
    Write-Output ('TITLE_LEN=' + $t.title.Length)
    $cps = @(); foreach ($c in $t.title.ToCharArray()) { $cps += ([int]$c).ToString('X4') }
    Write-Output ('TITLE_CODEPOINTS=' + ($cps -join ' '))
    Write-Output ('FILE_AT=' + (Get-Item $path).LastWriteTime.ToString('HH:mm:ss'))
} catch {
    Write-Output ('CAUGHT=' + $_.Exception.GetType().FullName)
    Write-Output ('MSG=' + $_.Exception.Message)
    Write-Output ('AT_LINE=' + $_.InvocationInfo.ScriptLineNumber)
}
exit 0
