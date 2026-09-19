# ASCII-only helper: print app process start time + now (no Chinese, safe under PS 5.1).
$p = Get-Process -Name 'wails-tmp' -ErrorAction SilentlyContinue
if ($p) {
    foreach ($x in $p) {
        Write-Output ("PID=" + $x.Id + " START=" + $x.StartTime.ToString('yyyy-MM-ddTHH:mm:ss'))
    }
} else {
    Write-Output "APP=NOT_RUNNING"
}
Write-Output ("NOW=" + (Get-Date).ToString('yyyy-MM-ddTHH:mm:ss'))
