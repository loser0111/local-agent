Set-Location 'e:\learn\local-agent'
$b = & go build -ldflags "-w -s" -o build/bin/wails-tmp-new.exe . 2>&1 | Out-String
Write-Output ('EXE_EXIT=' + $LASTEXITCODE)
Write-Output ('EXE_OUT=' + $b)
if (Test-Path 'build\bin\wails-tmp-new.exe') {
    $f = Get-Item 'build\bin\wails-tmp-new.exe'
    Write-Output ('NEW_SIZE=' + $f.Length + ' AT=' + $f.LastWriteTime.ToString('HH:mm:ss'))
} else {
    Write-Output 'EXE_MISSING'
}
if (Test-Path 'build\bin\wails-tmp.exe') {
    $o = Get-Item 'build\bin\wails-tmp.exe'
    Write-Output ('OLD_SIZE=' + $o.Length + ' AT=' + $o.LastWriteTime.ToString('HH:mm:ss'))
} else {
    Write-Output 'OLD_MISSING'
}
