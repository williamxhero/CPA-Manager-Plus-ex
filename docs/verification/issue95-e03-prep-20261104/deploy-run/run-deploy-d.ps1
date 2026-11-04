$ErrorActionPreference = "Continue"
$attestSrc = "D:\WILL\AGENT\CPA\ops\needs-forever-all-specs-20261103\worktrees\manager-canonical82-closing\docs\verification\issue97-live-20261104\host-attestation.5cfdda27.json"
$attestDst = "D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\host-attestation.json"
Copy-Item -Force $attestSrc $attestDst
$deployPs1 = "D:\WILL\AGENT\CPA\ops\needs-forever-all-specs-20261103\worktrees\manager-canonical82-closing\ops\windows\deploy-cpamp.ps1"
$app = "D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\app"
$panel = "D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\panel\management.html"
$art = "D:\WILL\AGENT\CPA\ops\needs-forever-all-specs-20261103\worktrees\manager-canonical82-closing\docs\verification\issue95-e03-prep-20261104\artifacts"
$backup = "D:\WILL\AGENT\CPA\runtime\CPA-Manager-Plus\deploy-backups"
$log = "D:\WILL\AGENT\CPA\ops\needs-forever-all-specs-20261103\worktrees\manager-canonical82-closing\docs\verification\issue95-e03-prep-20261104\deploy-run\deploy-20261104d.log"
$result = "D:\WILL\AGENT\CPA\ops\needs-forever-all-specs-20261103\worktrees\manager-canonical82-closing\docs\verification\issue95-e03-prep-20261104\deploy-run\deploy-20261104d.json"
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $deployPs1 -Target LocalCPAManagerPlus -ServiceName LocalCPAManagerPlus -AppDir $app -PanelPath $panel -ArtifactDir $art -BackupDir $backup -HealthUrl "http://127.0.0.1:18317/health" -ExpectMarker "ok" -LogPath $log -ResultPath $result -TimeoutSeconds 180 -RehearsalPort 28326
$code = $LASTEXITCODE
Write-Host "DEPLOY_EXIT=$code"
if ($code -eq 0) {
  $xmlPath = "D:\WILL\AGENT\CPA\ops\cpa-windows-services\runtime\LocalCPAManagerPlus.xml"
  [xml]$x = Get-Content -Raw $xmlPath
  $ns = $x.service
  $exists = $false
  foreach ($e in $ns.env) { if ($e.name -eq "CPAMP_WEB_BROWSER_ATTESTATION") { $exists = $true; $e.value = $attestDst } }
  if (-not $exists) {
    $envNode = $x.CreateElement("env")
    $envNode.SetAttribute("name", "CPAMP_WEB_BROWSER_ATTESTATION")
    $envNode.SetAttribute("value", $attestDst)
    $ns.AppendChild($envNode) | Out-Null
  }
  $x.Save($xmlPath)
  Write-Host "ATTEST_ENV_SAVED"
  & "D:\WILL\AGENT\CPA\ops\cpa-windows-services\runtime\LocalCPAManagerPlus.exe" refresh
  sc.exe stop LocalCPAManagerPlus
  Start-Sleep -Seconds 4
  sc.exe start LocalCPAManagerPlus
  Start-Sleep -Seconds 6
  sc.exe query LocalCPAManagerPlus
}
exit $code
