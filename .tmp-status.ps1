Set-Location 'e:\learn\local-agent'
Write-Output ('NOW=' + (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'))
$procs = Get-Process -Name 'wails-tmp' -ErrorAction SilentlyContinue
if ($procs) {
    foreach ($p in $procs) {
        Write-Output ('RUNNING PID=' + $p.Id + ' START=' + $p.StartTime.ToString('yyyy-MM-dd HH:mm:ss') + ' PATH=' + $p.Path)
    }
} else {
    Write-Output 'RUNNING=none'
}
foreach ($f in @('build\bin\wails-tmp.exe', 'build\bin\wails-tmp-new.exe')) {
    $p = Join-Path (Get-Location) $f
    if (Test-Path $p) {
        $i = Get-Item $p
        Write-Output ($f + ' SIZE=' + $i.Length + ' AT=' + $i.LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss'))
    } else {
        Write-Output ($f + ' MISSING')
    }
}
$t = Join-Path $env:USERPROFILE '.local-agent\tasks.json'
if (Test-Path $t) {
    $ti = Get-Item $t
    Write-Output ('tasks.json SIZE=' + $ti.Length + ' AT=' + $ti.LastWriteTime.ToString('yyyy-MM-dd HH:mm:ss'))
    Write-Output ('---LOGTAIL---')
    Get-Content 'build\bin\app.out.log' -Tail 8 -ErrorAction SilentlyContinue
}
