$ErrorActionPreference = "Continue"
$deployPs1 = "D:\WILL\AGENT\CPA\ops\needs-forever-all-specs-20261103\worktrees\manager-canonical82-closing\ops\windows\deploy-cpamp.ps1"
$app = "D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\app"
$panel = "D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\panel\management.html"
$art = "D:\WILL\AGENT\CPA\ops\needs-forever-all-specs-20261103\worktrees\manager-canonical82-closing\docs\verification\issue95-e03-prep-20261104\artifacts"
$backup = "D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\deploy-backups"
$ev = "D:\WILL\AGENT\CPA\ops\needs-forever-all-specs-20261103\worktrees\manager-canonical82-closing\docs\verification\issue95-e03-prep-20261104\deploy-run"
Write-Host "=== ROLLBACK ==="
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $deployPs1 -RollbackOnly -Target LocalCPAManagerPlus -ServiceName LocalCPAManagerPlus -AppDir $app -PanelPath $panel -BackupDir $backup -HealthUrl "http://127.0.0.1:18317/health" -ExpectMarker "ok" -LogPath "$ev\rollback-20261104.log" -ResultPath "$ev\rollback-20261104.json" -TimeoutSeconds 180
Write-Host "ROLLBACK_EXIT=$LASTEXITCODE"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "=== REDEPLOY ==="
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $deployPs1 -Target LocalCPAManagerPlus -ServiceName LocalCPAManagerPlus -AppDir $app -PanelPath $panel -ArtifactDir $art -BackupDir $backup -HealthUrl "http://127.0.0.1:18317/health" -ExpectMarker "ok" -LogPath "$ev\redeploy-20261104.log" -ResultPath "$ev\redeploy-20261104.json" -TimeoutSeconds 180 -RehearsalPort 28327
Write-Host "REDEPLOY_EXIT=$LASTEXITCODE"
exit $LASTEXITCODE
