$root = 'e:\learn\local-agent'
foreach ($f in @('plugin\store.go', 'plugin\plugin.go', 'plugin\watch.go', 'plugin\service.go')) {
    $p = Join-Path $root $f
    $bytes = [System.IO.File]::ReadAllBytes($p)
    $crlf = 0; $lf = 0
    for ($i = 0; $i -lt $bytes.Length; $i++) {
        if ($bytes[$i] -eq 10) {
            if ($i -gt 0 -and $bytes[$i - 1] -eq 13) { $crlf++ } else { $lf++ }
        }
    }
    Write-Output ("$f CRLF=$crlf LF_ONLY=$lf SIZE=" + $bytes.Length)
}
