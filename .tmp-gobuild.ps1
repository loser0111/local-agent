Set-Location 'e:\learn\local-agent'
$r = & go test ./plugin/ -count=1 -run 'Watch|PluginPicksUp' -v 2>&1 | Out-String
Write-Output ('TEST_EXIT=' + $LASTEXITCODE)
Write-Output ('TEST_OUT=' + $r)
$v = & go vet ./plugin/ 2>&1 | Out-String
Write-Output ('VET_EXIT=' + $LASTEXITCODE)
Write-Output ('VET_OUT=' + $v)
$all = & go test ./plugin/ -count=1 2>&1 | Out-String
Write-Output ('ALL_EXIT=' + $LASTEXITCODE)
Write-Output ('ALL_OUT=' + $all)
