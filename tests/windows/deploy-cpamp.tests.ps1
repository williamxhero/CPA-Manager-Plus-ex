#requires -Version 5.1
[CmdletBinding()]
param([string]$OutputDir)
$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$entry = Join-Path $repo 'ops\windows\deploy-cpamp.ps1'
if (-not $OutputDir) { $OutputDir = Join-Path $PSScriptRoot ('.runs\' + [guid]::NewGuid().ToString('N')) }
$OutputDir = [IO.Path]::GetFullPath($OutputDir)
if (Test-Path -LiteralPath $OutputDir) { throw 'Tests require a new, disposable output directory.' }
[IO.Directory]::CreateDirectory($OutputDir) | Out-Null

function Assert-True($Value, [string]$Message) {
    if (-not $Value) { throw ('ASSERTION FAILED: ' + $Message) }
}
function Get-TreeHashes([string]$Directory) {
    return @((Get-ChildItem -LiteralPath $Directory -Recurse -File | Sort-Object FullName | ForEach-Object { $_.FullName + ':' + (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash })) -join "`n"
}

$tokens = $null
$parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($entry, [ref]$tokens, [ref]$parseErrors)
Assert-True ($parseErrors.Count -eq 0) 'PS5.1 syntax parses'
# Import definitions, not the CLI exit. Test doubles never ship in the entrypoint.
foreach ($node in $ast.EndBlock.Statements) {
    if ($node -is [Management.Automation.Language.FunctionDefinitionAst]) { . ([scriptblock]::Create($node.Extent.Text)) }
}

$site = Join-Path $OutputDir 'site'
$app = Join-Path $site 'app'
$artifact = Join-Path $OutputDir 'artifact'
$backup = Join-Path $OutputDir 'backups'
$logs = Join-Path $OutputDir 'logs'
foreach ($directory in @($app, $artifact, $backup, $logs)) { [IO.Directory]::CreateDirectory($directory) | Out-Null }
$fixture = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'fixtures\deploy-fixture.cs'))
$compiled = @{}
foreach ($mode in @('old', 'new', 'broken')) {
    $path = Join-Path $OutputDir ($mode + '.exe')
    $source = $fixture.Replace('FIXTURE_MODE', $mode).Replace('DeployFixture', ('DeployFixture_' + $mode))
    Add-Type -TypeDefinition $source -OutputAssembly $path -OutputType ConsoleApplication
    $compiled[$mode] = $path
}
$exe = Join-Path $app 'cpa-manager-plus.exe'
$panel = Join-Path $app 'management.html'
Copy-Item -LiteralPath $compiled.old -Destination $exe
[IO.File]::WriteAllText($panel, 'old-panel')
[IO.File]::WriteAllText((Join-Path $app 'config.json'), '{"test":"do-not-change"}')
Copy-Item -LiteralPath $compiled.new -Destination (Join-Path $artifact 'cpa-manager-plus.exe')
[IO.File]::WriteAllText((Join-Path $artifact 'management.html'), 'new-panel')
$listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
$listener.Start()
$script:fakePort = $listener.LocalEndpoint.Port
$listener.Stop()
Assert-True ($script:fakePort -ne 8317) 'fixture never uses 8317'
$script:fakeName = 'FakeCPAMP-SpecA'
$script:fakeStatus = 'Running'
$script:fakeDependents = @()
$script:fakeProcess = $null
$script:serviceCalls = [Collections.Generic.List[string]]::new()
$script:failRestoreStart = $false
$script:lockLogOnStop = $null
$script:logLock = $null
$script:failResultOnce = $false
$script:jsonWriter = ${function:Write-CPAMPJson}
function Write-CPAMPJson([string]$Path, $Value) {
    if ($script:failResultOnce -and $Value.status -eq 'deployed' -and $Value.after.exeSha256) {
        $script:failResultOnce = $false
        throw 'injected_result_write_failure'
    }
    & $script:jsonWriter $Path $Value
}

function Test-CPAMPAdministrator { return $true }
function Get-Service {
    param([string]$Name, $ErrorAction)
    Assert-True ($Name -eq $script:fakeName) 'only fake service addressed'
    $service = [pscustomobject]@{ Name = $Name; Status = $script:fakeStatus; DependentServices = $script:fakeDependents }
    $service | Add-Member -MemberType ScriptMethod -Name Stop -Value { Stop-Service -Name $this.Name }
    $service | Add-Member -MemberType ScriptMethod -Name Start -Value { Start-Service -Name $this.Name }
    return $service
}
function Stop-Service {
    param([string]$Name, $ErrorAction)
    Assert-True ($Name -eq $script:fakeName) 'only fake service stopped'
    $script:serviceCalls.Add('stop:' + $Name)
    if ($script:lockLogOnStop) {
        $script:logLock = [IO.File]::Open($script:lockLogOnStop, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        $script:lockLogOnStop = $null
    }
    if ($script:fakeProcess) {
        if (-not $script:fakeProcess.HasExited) { $script:fakeProcess.Kill() }
        $script:fakeProcess.WaitForExit()
        $script:fakeProcess.Dispose()
        $script:fakeProcess = $null
    }
    $script:fakeStatus = 'Stopped'
}
function Start-Service {
    param([string]$Name, $ErrorAction)
    Assert-True ($Name -eq $script:fakeName) 'only fake service started'
    $script:serviceCalls.Add('start:' + $Name)
    if ($script:failRestoreStart -and (Get-FileHash -LiteralPath $exe).Hash -eq (Get-FileHash -LiteralPath $compiled.old).Hash) { throw 'injected_restore_start_failure' }
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = $exe
    $info.WorkingDirectory = $app
    $info.UseShellExecute = $false
    $info.EnvironmentVariables.Clear()
    $info.EnvironmentVariables['SystemRoot'] = $env:SystemRoot
    $info.EnvironmentVariables['HTTP_ADDR'] = '127.0.0.1:' + $script:fakePort
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $script:fakeProcess = [Diagnostics.Process]::Start($info)
    $script:fakeProcess.BeginOutputReadLine()
    $script:fakeProcess.BeginErrorReadLine()
    $script:fakeStatus = 'Running'
}

$common = @{ Target = 'LocalCPAManagerPlus'; ServiceName = $script:fakeName; AppDir = $app; PanelPath = $panel; ArtifactDir = $artifact; BackupDir = $backup; HealthUrl = ('http://127.0.0.1:' + $script:fakePort + '/health'); ExpectMarker = 'fixture-healthy'; TimeoutSeconds = 3 }
function Invoke-Case([string]$Case, [hashtable]$Extra = @{}) {
    $options = $common.Clone()
    $options.LogPath = Join-Path $logs ($Case + '.log')
    $options.ResultPath = Join-Path $logs ($Case + '.json')
    $options.RehearsalDataDir = Join-Path $OutputDir ('rehearsal-' + $Case)
    foreach ($key in $Extra.Keys) { $options[$key] = $Extra[$key] }
    $code = Invoke-CPAMPDeployment @options
    $json = [IO.File]::ReadAllText($options.ResultPath) | ConvertFrom-Json
    Assert-True (-not (Test-Path -LiteralPath $options.RehearsalDataDir)) ($Case + ' cleaned rehearsal')
    Write-Host ('CASE {0}: exit={1}; status={2}' -f $Case, $code, $json.status)
    return [pscustomobject]@{ Code = $code; Result = $json; Path = $options.ResultPath }
}

try {
    # Test a dummy sentinel, not real credentials, in the caller's environment.
    $env:CPAMP_DEPLOY_TEST_SENTINEL = 'not-a-real-credential'
    # Actual standalone -File invocation: DryRun explicitly permits offline fixtures without SCM.
    $beforeTree = Get-TreeHashes $site
    $dryJson = Join-Path $logs 'dryrun-cli.json'
    $dryOutput = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $entry -DryRun -ServiceName $script:fakeName -AppDir $app -PanelPath $panel -ArtifactDir $artifact -BackupDir $backup -HealthUrl $common.HealthUrl -ExpectMarker 'fixture-healthy' -TimeoutSeconds 3 -RehearsalDataDir (Join-Path $OutputDir 'rehearsal-cli') -LogPath (Join-Path $logs 'dryrun-cli.log') -ResultPath $dryJson
    $dryExit = $LASTEXITCODE
    $dryOutput | ForEach-Object { Write-Host $_ }
    [IO.File]::WriteAllText((Join-Path $logs 'dryrun-cli.stdout.txt'), ($dryOutput -join "`r`n") + "`r`nexit=$dryExit")
    Assert-True ($dryExit -eq 0) 'actual -File DryRun exit 0'
    $afterTree = Get-TreeHashes $site
    Assert-True ($beforeTree -eq $afterTree) 'ALL site files unchanged after actual DryRun'
    $hashEvidence = [ordered]@{ before = @($beforeTree -split "`n"); after = @($afterTree -split "`n"); equal = ($beforeTree -eq $afterTree) }
    [IO.File]::WriteAllText((Join-Path $logs 'dryrun-hashes.json'), ($hashEvidence | ConvertTo-Json -Depth 4))
    Assert-True ($script:serviceCalls.Count -eq 0) 'DryRun made no service calls'
    $dryRecord = [IO.File]::ReadAllText($dryJson) | ConvertFrom-Json
    Assert-True ($dryRecord.dryRun -and -not $dryRecord.serviceChanged) 'DryRun result is explicitly non-deploying'

    Start-Service $script:fakeName
    $success = Invoke-Case 'deployed'
    Assert-True ($success.Code -eq 0 -and $success.Result.status -eq 'deployed') 'deployment succeeds'
    Assert-True ($script:fakeStatus -eq 'Running') 'fake service running after deploy'
    Assert-True ((Get-FileHash -LiteralPath $exe).Hash -eq (Get-FileHash -LiteralPath $compiled.new).Hash) 'new executable installed'
    Assert-True ([IO.File]::ReadAllText($panel) -eq 'new-panel') 'new panel installed'
    Assert-True (@($success.Result.health | Where-Object { $_.phase -eq 'deploy' -and $_.httpStatus -eq 200 -and $_.expectedMarkerFound }).Count -gt 0) 'live health verified'

    $specificManifest = Join-Path $backup ([IO.Path]::GetFileName($success.Result.backup.exeBackup).Replace('-cpa-manager-plus.exe', '.manifest.json'))
    $restore = Invoke-Case 'rollback-only' @{ RollbackOnly = $true; BackupManifest = $specificManifest }
    Assert-True ($restore.Code -eq 0 -and $restore.Result.status -eq 'rolled_back') 'independent latest-backup restore'
    Assert-True ((Get-FileHash -LiteralPath $exe).Hash -eq (Get-FileHash -LiteralPath $compiled.old).Hash) 'old executable restored'
    Assert-True ([IO.File]::ReadAllText($panel) -eq 'old-panel') 'old panel restored'

    # A fault-injected executable passes isolation but exits immediately in the app directory.
    Copy-Item -LiteralPath $compiled.broken -Destination (Join-Path $artifact 'cpa-manager-plus.exe') -Force
    $failure = Invoke-Case 'rolled-back'
    Assert-True ($failure.Code -eq 1 -and $failure.Result.status -eq 'rolled_back') 'post-rehearsal deploy failure auto-rolls back'
    $backupHash = (Get-FileHash -LiteralPath $failure.Result.backup.exeBackup).Hash
    $restoredHash = (Get-FileHash -LiteralPath $exe).Hash
    Assert-True ($backupHash -eq $restoredHash) 'restored executable SHA256 equals actual backup SHA256'
    Assert-True ([IO.File]::ReadAllText($panel) -eq 'old-panel') 'automatic panel restore'
    Assert-True (@($failure.Result.health | Where-Object { $_.phase -eq 'rollback' -and $_.httpStatus -eq 200 -and $_.expectedMarkerFound }).Count -gt 0) 'rollback health verified'
    [IO.File]::WriteAllText((Join-Path $logs 'hash-proof.txt'), "backup=$backupHash`r`nrestored=$restoredHash`r`nsiteDryRunAllHashesUnchanged=true")

    $script:failRestoreStart = $true
    $failedRestore = Invoke-Case 'rollback-failed'
    Assert-True ($failedRestore.Code -eq 3 -and $failedRestore.Result.status -eq 'rollback_failed') 'rollback failure exit 3'
    $failedLog = [IO.File]::ReadAllText((Join-Path $logs 'rollback-failed.log'))
    Assert-True ($failedLog.Contains('MANUAL RECOVERY:') -and $failedLog.Contains($failedRestore.Result.backup.exeBackup)) 'manual recovery contains absolute backup path'
    $script:failRestoreStart = $false
    Start-Service $script:fakeName

    # A genuinely non-executable artifact must abort before service control.
    [IO.File]::WriteAllText((Join-Path $artifact 'cpa-manager-plus.exe'), 'not a PE executable')
    $tree = Get-TreeHashes $site
    $calls = $script:serviceCalls.Count
    $bad = Invoke-Case 'invalid-artifact'
    Assert-True ($bad.Code -eq 2 -and $bad.Result.status -eq 'rehearsal_failed') 'invalid executable rehearsal failure'
    Assert-True ($tree -eq (Get-TreeHashes $site) -and $calls -eq $script:serviceCalls.Count) 'rehearsal failure site/service unchanged'
    Copy-Item -LiteralPath $compiled.new -Destination (Join-Path $artifact 'cpa-manager-plus.exe') -Force

    $wrongMarker = Invoke-Case 'wrong-marker' @{ DryRun = $true; ExpectMarker = 'never-present' }
    Assert-True ($wrongMarker.Code -eq 2 -and $wrongMarker.Result.status -eq 'rehearsal_failed') 'HTTP 200 without marker fails'
    $wrongStatus = Invoke-Case 'wrong-status' @{ DryRun = $true; HealthUrl = ('http://127.0.0.1:' + $script:fakePort + '/status503') }
    Assert-True ($wrongStatus.Code -eq 2 -and @($wrongStatus.Result.health | Where-Object { $_.httpStatus -eq 503 }).Count -gt 0) 'right marker with HTTP 503 cannot pass'
    $core = Invoke-Case 'blocked-core' @{ HealthUrl = 'http://127.0.0.1:8317/' }
    Assert-True ($core.Code -eq 2 -and $core.Result.status -eq 'preflight_failed' -and $core.Result.health.Count -eq 0) '8317 guard before any probe'
    $coreApp = Join-Path $OutputDir 'core-site\app'
    $coreArtifact = Join-Path $OutputDir 'core-artifact'
    [IO.Directory]::CreateDirectory($coreApp) | Out-Null
    [IO.Directory]::CreateDirectory($coreArtifact) | Out-Null
    Copy-Item -LiteralPath $compiled.old -Destination (Join-Path $coreApp 'cli-proxy-api.exe')
    Copy-Item -LiteralPath $compiled.new -Destination (Join-Path $coreArtifact 'cli-proxy-api.exe')
    $coreOptIn = Invoke-Case 'core-opt-in' @{ Target = 'LocalCPA'; DryRun = $true; AppDir = $coreApp; PanelPath = (Join-Path $coreApp 'management.html'); ArtifactDir = $coreArtifact }
    Assert-True ($coreOptIn.Code -eq 0 -and $coreOptIn.Result.target -eq 'LocalCPA' -and -not $coreOptIn.Result.serviceChanged) 'explicit core rehearsal uses isolated config/port'
    $conflict = Invoke-Case 'conflicting-modes' @{ DryRun = $true; RollbackOnly = $true }
    Assert-True ($conflict.Code -eq 2 -and $conflict.Result.status -eq 'preflight_failed') 'contradictory modes rejected'

    $script:lockLogOnStop = Join-Path $logs 'locked-log.log'
    try {
        $lockedLog = Invoke-Case 'locked-log'
        Assert-True ($lockedLog.Code -eq 1 -and $lockedLog.Result.status -eq 'rolled_back') 'log failure cannot bypass rollback'
        Assert-True ((Get-FileHash -LiteralPath $exe).Hash -eq (Get-FileHash -LiteralPath $compiled.old).Hash) 'log failure restores old executable'
    } finally {
        if ($script:logLock) { $script:logLock.Dispose(); $script:logLock = $null }
    }
    $script:failResultOnce = $true
    $resultFailure = Invoke-Case 'result-write-failure'
    Assert-True ($resultFailure.Code -eq 1 -and $resultFailure.Result.status -eq 'rolled_back') 'result persistence failure rolls back before completing'

    $tree = Get-TreeHashes $site
    $calls = $script:serviceCalls.Count
    $script:failResultOnce = $true
    $dryWriteFailure = Invoke-Case 'dryrun-result-write-failure' @{ DryRun = $true }
    Assert-True ($dryWriteFailure.Code -eq 2 -and $dryWriteFailure.Result.status -eq 'preflight_failed') 'DryRun result-write failure cannot falsely exit zero'
    Assert-True ($tree -eq (Get-TreeHashes $site) -and $calls -eq $script:serviceCalls.Count) 'failed DryRun still changes no site/service state'

    # Restore the absence of a panel introduced by a failed deployment.
    Assert-True ([IO.File]::ReadAllText($panel) -eq 'old-panel') 'owned old test panel before removal'
    Remove-Item -LiteralPath $panel
    Copy-Item -LiteralPath $compiled.broken -Destination (Join-Path $artifact 'cpa-manager-plus.exe') -Force
    $absentPanel = Invoke-Case 'absent-panel'
    Assert-True ($absentPanel.Code -eq 1 -and -not (Test-Path -LiteralPath $panel)) 'rollback restores originally absent panel'
    Assert-True ($null -eq $absentPanel.Result.before.panelSha256 -and $null -eq $absentPanel.Result.after.panelSha256) 'absent panel snapshots preserved'
    Copy-Item -LiteralPath $compiled.new -Destination (Join-Path $artifact 'cpa-manager-plus.exe') -Force
    Stop-Service $script:fakeName
    Assert-True ((Get-FileHash -LiteralPath $exe).Hash -eq (Get-FileHash -LiteralPath $compiled.old).Hash) 'owned test executable before removal'
    Remove-Item -LiteralPath $exe
    $missingExe = Invoke-Case 'missing-installed-exe' @{ RollbackOnly = $true }
    Assert-True ($missingExe.Code -eq 0 -and (Get-FileHash -LiteralPath $exe).Hash -eq (Get-FileHash -LiteralPath $compiled.old).Hash) 'RollbackOnly repairs missing installed executable'

    # Malformed manifest cannot bypass hash validation; output cannot overwrite recovery input.
    $latestGood = @(Get-ChildItem -LiteralPath $backup -Filter '*.manifest.json' | Sort-Object Name -Descending)[0]
    $badRecord = [IO.File]::ReadAllText($latestGood.FullName) | ConvertFrom-Json
    $badRecord.before.exeSha256 = $null
    $malformed = Join-Path $backup 'malformed.manifest.json'
    [IO.File]::WriteAllText($malformed, ($badRecord | ConvertTo-Json -Depth 12))
    $calls = $script:serviceCalls.Count
    $schema = Invoke-Case 'missing-backup-hash' @{ RollbackOnly = $true; BackupManifest = $malformed }
    Assert-True ($schema.Code -eq 2 -and $calls -eq $script:serviceCalls.Count) 'missing executable hash rejected before stop'
    $manifestHash = (Get-FileHash -LiteralPath $malformed).Hash
    $options = $common.Clone()
    $options.RollbackOnly = $true
    $options.BackupManifest = $malformed
    $options.ResultPath = $malformed
    $options.LogPath = Join-Path $logs 'manifest-overlap.log'
    $code = Invoke-CPAMPDeployment @options
    Assert-True ($code -eq 2 -and (Get-FileHash -LiteralPath $malformed).Hash -eq $manifestHash) 'unsafe output does not overwrite manifest'

    $configPath = Join-Path $app 'config.json'
    $configHash = (Get-FileHash -LiteralPath $configPath).Hash
    $options = $common.Clone()
    $options.PanelPath = $configPath
    $options.LogPath = Join-Path $logs 'wrong-panel-path.log'
    $options.ResultPath = Join-Path $logs 'wrong-panel-path.json'
    $code = Invoke-CPAMPDeployment @options
    Assert-True ($code -eq 2 -and (Get-FileHash -LiteralPath $configPath).Hash -eq $configHash) 'PanelPath cannot overwrite runtime config'

    # Backup validation must fail before service control when backup bytes are damaged.
    $latest = $latestGood
    $manifestRecord = [IO.File]::ReadAllText($latest.FullName) | ConvertFrom-Json
    [IO.File]::AppendAllText($manifestRecord.exeBackup, 'damaged')
    $calls = $script:serviceCalls.Count
    $damaged = Invoke-Case 'damaged-backup' @{ RollbackOnly = $true; BackupManifest = $latest.FullName }
    Assert-True ($damaged.Code -eq 2 -and $calls -eq $script:serviceCalls.Count) 'damaged backup rejected before stop'

    $script:fakeDependents = @([pscustomobject]@{ Name = 'AnotherFakeService'; Status = 'Running' })
    $calls = $script:serviceCalls.Count
    $dependent = Invoke-Case 'running-dependent'
    Assert-True ($dependent.Code -eq 2 -and $calls -eq $script:serviceCalls.Count) 'running dependent service rejected without cascading stop'
    $script:fakeDependents = @()
    function Test-CPAMPAdministrator { return $false }
    $nonAdmin = Invoke-Case 'non-admin'
    Assert-True ($nonAdmin.Code -eq 2 -and $nonAdmin.Result.status -eq 'preflight_failed') 'non-admin deployment rejected'
    Assert-True ([IO.File]::ReadAllText((Join-Path $logs 'non-admin.log')).Contains('Run as Administrator')) 'clear elevation guidance'
    foreach ($file in @(Get-ChildItem -LiteralPath $logs -File | Where-Object { $_.Extension -in @('.log', '.json') })) {
        $content = [IO.File]::ReadAllText($file.FullName)
        Assert-True (-not $content.Contains('not-a-real-credential') -and -not $content.Contains('fixture-private-stdout') -and -not $content.Contains('fixture-private-stderr')) 'no inherited sentinel/process output leaked'
    }
    Write-Host ('PASS: all isolated assertions. Evidence: ' + $logs)
} finally {
    $env:CPAMP_DEPLOY_TEST_SENTINEL = $null
    if ($script:logLock) { $script:logLock.Dispose() }
    Stop-Service $script:fakeName
}
