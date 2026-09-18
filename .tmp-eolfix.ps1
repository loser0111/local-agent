$root = 'e:\learn\local-agent'
$mode = if ($args.Count -gt 0) { $args[0] } else { 'lf' }
foreach ($f in @('plugin\store.go', 'plugin\plugin.go')) {
    $p = Join-Path $root $f
    $bytes = [System.IO.File]::ReadAllBytes($p)
    $bom = ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)
    $t = [System.IO.File]::ReadAllText($p)
    $t = $t.Replace("`r`n", "`n")
    $t = $t.Replace("`r", "`n")
    if ($mode -ne 'lf') {
        $t = $t.Replace("`n", "`r`n")
    }
    $enc = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($p, $t, $enc)
    Write-Output ("$f mode=$mode BOM_WAS=$bom")
}
