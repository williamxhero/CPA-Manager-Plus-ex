#requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet('LocalCPAManagerPlus', 'LocalCPA')][string]$Target = 'LocalCPAManagerPlus',
    [string]$ServiceName,
    [string]$AppDir,
    [string]$PanelPath,
    [string]$ArtifactDir,
    [string]$BackupDir,
    [string]$HealthUrl,
    [string]$ExpectMarker,
    [string]$LogPath,
    [string]$ResultPath,
    [ValidateRange(1, 3600)][int]$TimeoutSeconds = 60,
    [ValidateRange(0, 65535)][int]$RehearsalPort = 0,
    [string]$RehearsalDataDir,
    [string]$BackupManifest,
    [switch]$DryRun,
    [switch]$RollbackOnly
)

function Test-CPAMPAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    return ([Security.Principal.WindowsPrincipal]$identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-CPAMPFullPath([string]$Path) {
    $provider = $null
    $drive = $null
    $resolved = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path, [ref]$provider, [ref]$drive)
    if ($provider.Name -ne 'FileSystem') { throw 'filesystem_path' }
    return [IO.Path]::GetFullPath($resolved)
}

function Test-CPAMPWithin([string]$Path, [string]$Directory) {
    $prefix = $Directory.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    return $Path.Equals($Directory, [StringComparison]::OrdinalIgnoreCase) -or $Path.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)
}

function Assert-CPAMPPlainPath([string]$Path) {
    # Check existing ancestors too: a missing child can still traverse a junction.
    $current = $Path
    while ($current) {
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -LiteralPath $current -Force -ErrorAction Stop
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'reparse_path' }
        }
        $parent = Split-Path -Parent $current
        if ($parent -eq $current) { break }
        $current = $parent
    }
}

function Get-CPAMPHash([string]$Path) {
    if (Test-Path -LiteralPath $Path -PathType Leaf) { return (Get-FileHash -LiteralPath $Path -Algorithm SHA256 -ErrorAction Stop).Hash }
    return $null
}

function Get-CPAMPSnapshot([string]$Exe, [string]$Panel) {
    $version = $null
    if (Test-Path -LiteralPath $Exe -PathType Leaf) {
        # Never execute an untrusted binary merely to obtain its version.
        $version = [Diagnostics.FileVersionInfo]::GetVersionInfo($Exe).FileVersion
    }
    return [ordered]@{ exePath = $Exe; exeSha256 = (Get-CPAMPHash $Exe); panelPath = $Panel; panelSha256 = (Get-CPAMPHash $Panel); version = $version }
}

function Write-CPAMPJson([string]$Path, $Value) {
    $temporary = $Path + '.' + [guid]::NewGuid().ToString('N') + '.tmp'
    try {
        [IO.File]::WriteAllText($temporary, ($Value | ConvertTo-Json -Depth 12), [Text.UTF8Encoding]::new($false))
        Move-Item -LiteralPath $temporary -Destination $Path -Force -ErrorAction Stop
    } finally {
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force }
    }
}

function Write-CPAMPStep($Context, [string]$Name, [bool]$Ok, [string]$Detail) {
    $Context.result.steps.Add([ordered]@{ name = $Name; ok = $Ok; detail = $Detail })
    $line = '{0} [{1}] {2}: {3}' -f [DateTime]::UtcNow.ToString('o'), $(if ($Ok) { 'OK' } else { 'FAIL' }), $Name, $Detail
    Write-Host $line
    if ($Context.logReady) {
        try { [IO.File]::AppendAllText($Context.logPath, $line + [Environment]::NewLine, [Text.UTF8Encoding]::new($false)) }
        catch {
            # Logging must never prevent the recovery path from executing.
            $Context.logFailed = $true
            Write-Host 'TEXT LOG UNAVAILABLE: recovery continues; diagnostic content suppressed.'
        }
    }
}

function Get-CPAMPFailureDetail($ErrorRecord) {
    # Only our exact, fixed error codes may be echoed; OS/process errors are untrusted.
    $messages = @{
        administrator_required = 'Administrator privileges required; no service was changed.'
        artifact_missing = 'Artifact executable is missing from ArtifactDir.'
        installed_exe_missing = 'Installed executable is missing from AppDir.'
        app_directory_missing = 'AppDir does not exist.'
        panel_parent_missing = 'Panel destination parent directory does not exist.'
        panel_path = 'PanelPath must name a management.html file, not a directory or another runtime file.'
        core_not_explicit = 'Core service/port/path requires explicit -Target LocalCPA.'
        health_url = 'HealthUrl must be loopback HTTP(S), without credentials, query or fragment.'
        rehearsal_port = 'Rehearsal port must be available, different from the live port, and never 8317.'
        rehearsal_directory_exists = 'RehearsalDataDir must be a new, nonexistent directory.'
        rehearsal_health = 'Rehearsal process exited or HTTP 200 + expected marker timed out; installation unchanged.'
        rehearsal_cleanup = 'Unable to terminate rehearsal process; installation unchanged.'
        deploy_health = 'Deployment HTTP 200 + expected marker timed out; attempting rollback.'
        rollback_health = 'Rollback HTTP 200 + expected marker timed out.'
        conflicting_modes = 'DryRun and RollbackOnly cannot be combined.'
        service_not_running = 'Deployment requires a Running service; use RollbackOnly for recovery.'
        service_name = 'Invalid service name.'
        service_timeout = 'Service did not reach the requested state before the timeout.'
        service_dependents = 'Refusing to stop running dependent services; stop them separately before retrying.'
        backup_missing = 'No matching, complete backup exists.'
        backup_schema = 'Backup manifest must contain valid SHA256 hashes.'
        backup_hash = 'Backup file is missing or does not match its recorded SHA256.'
        backup_path = 'Backup files must be inside the manifest directory.'
        backup_target_mismatch = 'Backup target/service/destination does not match this invocation.'
        artifact_changed = 'Artifact changed during rehearsal; installation unchanged.'
        installation_changed = 'Installation changed after backup; aborting before service control.'
        replacement_hash = 'Replacement hash mismatch; attempting rollback.'
        restore_hash = 'Restored executable/panel hashes do not match the backup.'
        restore_hash_after_start = 'Restored hashes changed after starting the service.'
        text_log_failed = 'Text log became unavailable; attempting rollback if deployment began.'
        output_paths = 'LogPath must end in .log and ResultPath in .json; they must differ.'
        output_overlap = 'Output paths overlap protected runtime/input/backup paths.'
        directory_overlap = 'Backup/rehearsal directories overlap protected paths.'
        input_destination_overlap = 'Artifact and installation destinations overlap.'
        reparse_path = 'Symbolic links/junctions are not allowed in deployment paths.'
        filesystem_path = 'Deployment paths must use the FileSystem provider.'
    }
    $message = $messages[$ErrorRecord.Exception.Message]
    if ($message) { return $message }
    return 'Service, process or file operation failed; raw diagnostics suppressed to protect credentials.'
}

function Wait-CPAMPHealth($Context, [uri]$Url, [string]$Marker, [string]$Phase, [int]$Timeout, $Process = $null) {
    $clock = [Diagnostics.Stopwatch]::StartNew()
    do {
        $status = $null
        $found = $false
        $response = $null
        $reader = $null
        try {
            $request = [Net.HttpWebRequest]::Create($Url)
            $request.Proxy = $null
            $request.AllowAutoRedirect = $false
            $request.UseDefaultCredentials = $false
            $remaining = [Math]::Max(1, [int](1000 * ($Timeout - $clock.Elapsed.TotalSeconds)))
            $request.Timeout = [Math]::Min(2000, $remaining)
            $request.ReadWriteTimeout = $request.Timeout
            $response = $request.GetResponse()
            $status = [int]$response.StatusCode
            $stream = $response.GetResponseStream()
            $reader = [IO.StreamReader]::new($stream)
            # Bound both size and the total deadline, including slow/chunked bodies.
            $buffer = New-Object char[] 4096
            $body = [Text.StringBuilder]::new()
            while ($body.Length -lt 1048576) {
                $remaining = [int](1000 * ($Timeout - $clock.Elapsed.TotalSeconds))
                if ($remaining -le 0) { throw 'health_read_timeout' }
                $stream.ReadTimeout = [Math]::Min(2000, $remaining)
                $count = $reader.Read($buffer, 0, [Math]::Min($buffer.Length, 1048576 - $body.Length))
                if ($count -eq 0) { break }
                $body.Append($buffer, 0, $count) | Out-Null
            }
            # Never record bodies, markers, headers or exception messages.
            $found = $body.ToString().IndexOf($Marker, [StringComparison]::Ordinal) -ge 0
        } catch {
            $probeException = $_.Exception
            # PS5.1 wraps .NET method exceptions; retain the actual non-2xx status.
            while ($probeException.InnerException) { $probeException = $probeException.InnerException }
            if ($probeException -is [Net.WebException] -and $probeException.Response) {
                $status = [int]$probeException.Response.StatusCode
                $probeException.Response.Close()
            }
        } finally {
            if ($reader) { $reader.Dispose() }
            if ($response) { $response.Dispose() }
        }
        $Context.result.health.Add([ordered]@{ at = [DateTime]::UtcNow.ToString('o'); phase = $Phase; httpStatus = $status; expectedMarkerFound = $found })
        if ($status -eq 200 -and $found -and (-not $Process -or -not $Process.HasExited)) { return $true }
        if ($Process -and $Process.HasExited) { return $false }
        if ($clock.Elapsed.TotalSeconds -lt $Timeout) { Start-Sleep -Milliseconds 250 }
    } while ($clock.Elapsed.TotalSeconds -lt $Timeout)
    return $false
}

function Set-CPAMPServiceState([string]$Name, [string]$State, [int]$Timeout) {
    $service = Get-Service -Name $Name -ErrorAction Stop
    # ServiceController requests return promptly; enforce our own bounded status wait.
    if ($State -eq 'Stopped') {
        if (@($service.DependentServices | Where-Object { $_ -and $_.Status -ne 'Stopped' }).Count) { throw 'service_dependents' }
        if ([string]$service.Status -notin @('Stopped', 'StopPending')) { $service.Stop() }
    } elseif ([string]$service.Status -notin @('Running', 'StartPending')) { $service.Start() }
    $clock = [Diagnostics.Stopwatch]::StartNew()
    do {
        if ([string](Get-Service -Name $Name -ErrorAction Stop).Status -eq $State) { return }
        if ($clock.Elapsed.TotalSeconds -ge $Timeout) { throw 'service_timeout' }
        Start-Sleep -Milliseconds 250
    } while ($true)
}

function Invoke-CPAMPRehearsal($Context, [string]$Exe, [string]$Panel, [string]$Directory, [int]$Port, [uri]$Health, [string]$Marker, [string]$Target, [int]$Timeout) {
    $process = $null
    $launched = $false
    $owned = $false
    try {
        if (Test-Path -LiteralPath $Directory) { throw 'rehearsal_directory_exists' }
        [IO.Directory]::CreateDirectory($Directory) | Out-Null
        $owned = $true
        $security = [Security.AccessControl.DirectorySecurity]::new()
        $security.SetAccessRuleProtection($true, $false)
        $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
        $rule = [Security.AccessControl.FileSystemAccessRule]::new($sid, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $security.AddAccessRule($rule)
        [IO.Directory]::SetAccessControl($Directory, $security)
        $copy = Join-Path $Directory ([IO.Path]::GetFileName($Exe))
        Copy-Item -LiteralPath $Exe -Destination $copy -ErrorAction Stop
        $panelCopy = Join-Path $Directory 'management.html'
        if ($Panel) { Copy-Item -LiteralPath $Panel -Destination $panelCopy -ErrorAction Stop }
        $info = [Diagnostics.ProcessStartInfo]::new()
        $info.FileName = $copy
        $info.WorkingDirectory = $Directory
        $info.UseShellExecute = $false
        $info.CreateNoWindow = $true
        $info.RedirectStandardOutput = $true
        $info.RedirectStandardError = $true
        # Use an allowlist, not the operator's inherited environment or credentials.
        $info.EnvironmentVariables.Clear()
        $info.EnvironmentVariables['SystemRoot'] = $env:SystemRoot
        $info.EnvironmentVariables['WINDIR'] = $env:SystemRoot
        foreach ($name in @('TEMP', 'TMP', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'HOME')) { $info.EnvironmentVariables[$name] = $Directory }
        if ($Target -eq 'LocalCPAManagerPlus') {
            $info.EnvironmentVariables['HTTP_ADDR'] = '127.0.0.1:' + $Port
            $info.EnvironmentVariables['USAGE_DATA_DIR'] = Join-Path $Directory 'data'
            $info.EnvironmentVariables['USAGE_DB_PATH'] = Join-Path $Directory 'data\usage.sqlite'
            $info.EnvironmentVariables['CPA_MANAGER_CONFIG'] = Join-Path $Directory 'config.json'
            $info.EnvironmentVariables['CPA_MANAGER_DATA_KEY_PATH'] = Join-Path $Directory 'data\data.key'
            foreach ($name in @('CPA_MANAGEMENT_KEY_FILE', 'CPA_MANAGER_ADMIN_KEY_FILE', 'CPA_MANAGER_DATA_KEY_FILE')) {
                $info.EnvironmentVariables[$name] = Join-Path $Directory ('absent-' + $name)
            }
            foreach ($name in @('CPAMP_UPDATE_CHECK_ENABLED', 'USAGE_QUOTA_COOLDOWN_ENABLED', 'USAGE_ACCOUNT_ACTIONS_ENABLED', 'USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE', 'USAGE_ARCHIVE_RETENTION_ENABLED', 'USAGE_DASHBOARD_HOURLY_ROLLUP_ENABLED')) {
                $info.EnvironmentVariables[$name] = 'false'
            }
            $info.EnvironmentVariables['PANEL_PATH'] = $panelCopy
        } else {
            # CLIProxyAPI's -config contract; no copied .env, auth files or live configuration.
            $config = Join-Path $Directory 'config.yaml'
            $auth = (Join-Path $Directory 'auth').Replace('\', '/')
            $yaml = "host: '127.0.0.1'`nport: $Port`nauth-dir: '$auth'`napi-keys: []`nremote-management:`n  allow-remote: false`n  secret-key: ''`n  disable-control-panel: true`n  disable-auto-update-panel: true`nlogging-to-file: false`nusage-statistics-enabled: false`n"
            [IO.File]::WriteAllText($config, $yaml, [Text.UTF8Encoding]::new($false))
            $info.Arguments = '-config "' + $config + '"'
        }
        $builder = [UriBuilder]::new($Health)
        $builder.Host = '127.0.0.1'
        $builder.Port = $Port
        $builder.Scheme = 'http'
        $process = [Diagnostics.Process]::new()
        $process.StartInfo = $info
        if (-not $process.Start()) { throw 'rehearsal_start' }
        $launched = $true
        # Drain and discard: the Manager can print a newly generated admin key at startup.
        $process.BeginOutputReadLine()
        $process.BeginErrorReadLine()
        if (-not (Wait-CPAMPHealth $Context $builder.Uri $Marker 'rehearsal' $Timeout $process)) { throw 'rehearsal_health' }
    } finally {
        $canDelete = -not $launched
        try {
            if ($launched) {
                if (-not $process.HasExited) { $process.Kill() }
                if (-not $process.WaitForExit(5000)) { throw 'rehearsal_cleanup' }
                $canDelete = $true
            }
        } finally {
            if ($process) { $process.Dispose() }
            if ($owned -and $canDelete) { Remove-Item -LiteralPath $Directory -Recurse -Force -ErrorAction Stop }
        }
    }
}

function Get-CPAMPBackup([string]$Manifest, [string]$Directory, [string]$Target, [string]$Service, [string]$Exe, [string]$Panel) {
    if (-not $Manifest) {
        $candidates = @(Get-ChildItem -LiteralPath $Directory -Filter '*.manifest.json' -File -ErrorAction Stop | Sort-Object Name -Descending)
        foreach ($candidate in $candidates) {
            $record = [IO.File]::ReadAllText($candidate.FullName) | ConvertFrom-Json -ErrorAction Stop
            if ($record.target -eq $Target -and $record.serviceName -eq $Service -and $record.before.exePath -eq $Exe -and $record.before.panelPath -eq $Panel) {
                $Manifest = $candidate.FullName
                break
            }
        }
    }
    if (-not $Manifest) { throw 'backup_missing' }
    Assert-CPAMPPlainPath $Manifest
    $record = [IO.File]::ReadAllText($Manifest) | ConvertFrom-Json -ErrorAction Stop
    if ($record.target -ne $Target -or $record.serviceName -ne $Service -or $record.before.exePath -ne $Exe -or $record.before.panelPath -ne $Panel) { throw 'backup_target_mismatch' }
    if ($record.before.exeSha256 -notmatch '^[A-Fa-f0-9]{64}$' -or -not $record.exeBackup) { throw 'backup_schema' }
    if ($record.before.panelSha256 -and $record.before.panelSha256 -notmatch '^[A-Fa-f0-9]{64}$') { throw 'backup_schema' }
    foreach ($pair in @(@($record.exeBackup, $record.before.exeSha256), @($record.panelBackup, $record.before.panelSha256))) {
        if ($pair[1]) {
            if (-not $pair[0]) { throw 'backup_missing' }
            Assert-CPAMPPlainPath $pair[0]
            if (-not (Test-CPAMPWithin (Get-CPAMPFullPath $pair[0]) (Split-Path -Parent $Manifest))) { throw 'backup_path' }
            if ((Get-CPAMPHash $pair[0]) -ne $pair[1]) { throw 'backup_hash' }
        }
    }
    return $record
}

function Assert-CPAMPBackupFiles($Backup) {
    if ($Backup.before.exeSha256 -notmatch '^[A-Fa-f0-9]{64}$' -or (Get-CPAMPHash $Backup.exeBackup) -ne $Backup.before.exeSha256) { throw 'backup_hash' }
    if ($Backup.before.panelSha256 -and (Get-CPAMPHash $Backup.panelBackup) -ne $Backup.before.panelSha256) { throw 'backup_hash' }
}

function Invoke-CPAMPRestore($Context, $Backup, [string]$Exe, [string]$Panel, [string]$Service, [uri]$Health, [string]$Marker, [int]$Timeout) {
    Assert-CPAMPBackupFiles $Backup
    Set-CPAMPServiceState $Service 'Stopped' $Timeout
    Write-CPAMPStep $Context 'rollback_stop' $true 'Service stopped.'
    Copy-Item -LiteralPath $Backup.exeBackup -Destination $Exe -Force -ErrorAction Stop
    if ($Backup.before.panelSha256) {
        Copy-Item -LiteralPath $Backup.panelBackup -Destination $Panel -Force -ErrorAction Stop
    } elseif (Test-Path -LiteralPath $Panel) {
        # Restore absence too, if this deployment introduced a panel.
        Remove-Item -LiteralPath $Panel -Force -ErrorAction Stop
    }
    if ((Get-CPAMPHash $Exe) -ne $Backup.before.exeSha256 -or (Get-CPAMPHash $Panel) -ne $Backup.before.panelSha256) { throw 'restore_hash' }
    Write-CPAMPStep $Context 'rollback_restore' $true 'Executable and panel hashes match the backup (including absent panel).'
    Set-CPAMPServiceState $Service 'Running' $Timeout
    Write-CPAMPStep $Context 'rollback_start' $true 'Service running.'
    if (-not (Wait-CPAMPHealth $Context $Health $Marker 'rollback' $Timeout)) { throw 'rollback_health' }
    if ((Get-CPAMPHash $Exe) -ne $Backup.before.exeSha256 -or (Get-CPAMPHash $Panel) -ne $Backup.before.panelSha256) { throw 'restore_hash_after_start' }
    Write-CPAMPStep $Context 'rollback_health' $true 'HTTP 200, expected marker and restored hashes verified.'
}

function Invoke-CPAMPDeployment {
    [CmdletBinding()]
    param(
        [ValidateSet('LocalCPAManagerPlus', 'LocalCPA')][string]$Target = 'LocalCPAManagerPlus',
        [string]$ServiceName, [string]$AppDir, [string]$PanelPath, [string]$ArtifactDir,
        [string]$BackupDir, [string]$HealthUrl, [string]$ExpectMarker, [string]$LogPath, [string]$ResultPath,
        [ValidateRange(1, 3600)][int]$TimeoutSeconds = 60,
        [ValidateRange(0, 65535)][int]$RehearsalPort = 0,
        [string]$RehearsalDataDir, [string]$BackupManifest, [switch]$DryRun, [switch]$RollbackOnly
    )
    $ErrorActionPreference = 'Stop'
    $started = [DateTime]::UtcNow
    $stamp = $started.ToString('yyyyMMddTHHmmssfffffffZ') + '-' + [guid]::NewGuid().ToString('N')
    if (-not $ServiceName) { $ServiceName = $Target }
    $base = if ($Target -eq 'LocalCPA') { 'D:\WILL\AGENT\CPA\CLIProxyAPI' } else { 'D:\WILL\AGENT\CPA\CPA-Manager-Plus' }
    if (-not $AppDir) { $AppDir = Join-Path $base 'app' }
    if (-not $PanelPath) { $PanelPath = Join-Path $AppDir 'management.html' }
    if (-not $ArtifactDir) { $ArtifactDir = Join-Path $PSScriptRoot 'artifacts' }
    if (-not $BackupDir) { $BackupDir = Join-Path (Split-Path -Parent $AppDir) 'deploy-backups' }
    if (-not $LogPath) { $LogPath = Join-Path (Split-Path -Parent $AppDir) ('deploy-logs\deploy-' + $stamp + '.log') }
    if (-not $ResultPath) { $ResultPath = [IO.Path]::ChangeExtension($LogPath, '.json') }
    if (-not $HealthUrl) { $HealthUrl = if ($Target -eq 'LocalCPA') { 'http://127.0.0.1:8317/' } else { 'http://127.0.0.1:18317/health' } }
    if (-not $ExpectMarker) { $ExpectMarker = if ($Target -eq 'LocalCPA') { 'CLI Proxy API' } else { '"ok":true' } }
    if (-not $RehearsalDataDir) { $RehearsalDataDir = Join-Path ([IO.Path]::GetTempPath()) ('cpamp-rehearsal-' + $stamp) }
    $result = [ordered]@{
        startedAt = $started.ToString('o'); finishedAt = $null; durationSeconds = 0
        target = $Target; serviceName = $ServiceName; status = 'preflight_failed'
        dryRun = [bool]$DryRun; rollbackOnly = [bool]$RollbackOnly; serviceChanged = $false
        before = [ordered]@{ exePath = $null; exeSha256 = $null; panelPath = $null; panelSha256 = $null; version = $null }
        after = [ordered]@{ exePath = $null; exeSha256 = $null; panelPath = $null; panelSha256 = $null; version = $null }
        backup = $null
        steps = [Collections.Generic.List[object]]::new(); health = [Collections.Generic.List[object]]::new()
    }
    $context = @{ result = $result; logPath = $LogPath; logReady = $false; logFailed = $false }
    $lock = $null
    $backup = $null
    $changed = $false
    $code = 2
    $phase = 'preflight'
    $outputsReady = $false
    $resultSaved = $false
    $exe = $null
    try {
        $AppDir = Get-CPAMPFullPath $AppDir
        $PanelPath = Get-CPAMPFullPath $PanelPath
        $ArtifactDir = Get-CPAMPFullPath $ArtifactDir
        $BackupDir = Get-CPAMPFullPath $BackupDir
        $LogPath = Get-CPAMPFullPath $LogPath
        $ResultPath = Get-CPAMPFullPath $ResultPath
        $RehearsalDataDir = Get-CPAMPFullPath $RehearsalDataDir
        if ($BackupManifest) { $BackupManifest = Get-CPAMPFullPath $BackupManifest }
        $context.logPath = $LogPath
        $name = if ($Target -eq 'LocalCPA') { 'cli-proxy-api.exe' } else { 'cpa-manager-plus.exe' }
        $exe = Join-Path $AppDir $name
        $artifact = Join-Path $ArtifactDir $name
        $panelArtifact = Join-Path $ArtifactDir 'management.html'
        foreach ($path in @($AppDir, $PanelPath, $ArtifactDir, $BackupDir, $LogPath, $ResultPath, $RehearsalDataDir)) { Assert-CPAMPPlainPath $path }
        if ($Target -ne 'LocalCPA') {
            # Reject the known core installation for ALL writes, not just its health port.
            $coreRoot = 'D:\WILL\AGENT\CPA\CLIProxyAPI'
            foreach ($path in @($AppDir, $PanelPath, $BackupDir, $LogPath, $ResultPath, $RehearsalDataDir)) {
                if (Test-CPAMPWithin $path $coreRoot) { throw 'core_not_explicit' }
            }
        }
        if ($exe -eq $PanelPath -or (Test-CPAMPWithin $artifact $AppDir) -or $PanelPath -eq $artifact -or $PanelPath -eq $panelArtifact) { throw 'input_destination_overlap' }
        if ([IO.Path]::GetFileName($PanelPath) -ne 'management.html' -or (Test-Path -LiteralPath $PanelPath -PathType Container)) { throw 'panel_path' }
        # Output paths must never overwrite runtime, inputs, backups, or each other.
        if ((Test-Path -LiteralPath $LogPath -PathType Container) -or (Test-Path -LiteralPath $ResultPath -PathType Container)) { throw 'output_paths' }
        if ($LogPath -eq $ResultPath -or [IO.Path]::GetExtension($LogPath) -ne '.log' -or [IO.Path]::GetExtension($ResultPath) -ne '.json') { throw 'output_paths' }
        foreach ($output in @($LogPath, $ResultPath)) {
            if ($BackupManifest -and (Test-CPAMPWithin $output (Split-Path -Parent $BackupManifest))) { throw 'output_overlap' }
            foreach ($directory in @($AppDir, $ArtifactDir, $BackupDir, $RehearsalDataDir, (Split-Path -Parent $PanelPath))) {
                if (Test-CPAMPWithin $output $directory) { throw 'output_overlap' }
            }
        }
        foreach ($directory in @($BackupDir, $RehearsalDataDir)) {
            foreach ($protected in @($AppDir, $ArtifactDir, (Split-Path -Parent $PanelPath))) {
                if ((Test-CPAMPWithin $directory $protected) -or (Test-CPAMPWithin $protected $directory)) { throw 'directory_overlap' }
            }
        }
        if ((Test-CPAMPWithin $RehearsalDataDir $BackupDir) -or (Test-CPAMPWithin $BackupDir $RehearsalDataDir)) { throw 'directory_overlap' }
        foreach ($output in @($LogPath, $ResultPath)) { [IO.Directory]::CreateDirectory((Split-Path -Parent $output)) | Out-Null }
        # Test write access before any service operation; raw exceptions never reach logs.
        [IO.File]::AppendAllText($LogPath, '', [Text.UTF8Encoding]::new($false))
        $context.logReady = $true
        Write-CPAMPJson $ResultPath $result
        $outputsReady = $true
        if ($DryRun -and $RollbackOnly) { throw 'conflicting_modes' }
        if ($ServiceName -notmatch '^[A-Za-z0-9_.-]+$') { throw 'service_name' }
        if ($Target -ne 'LocalCPA' -and $ServiceName -eq 'LocalCPA') { throw 'core_not_explicit' }
        $health = [uri]$HealthUrl
        if (-not $health.IsAbsoluteUri -or $health.Scheme -notin @('http', 'https') -or -not $health.IsLoopback -or $health.UserInfo -or $health.Query -or $health.Fragment) { throw 'health_url' }
        if ($Target -ne 'LocalCPA' -and $health.Port -eq 8317) { throw 'core_not_explicit' }
        if ($RehearsalPort -eq 8317 -or $RehearsalPort -eq $health.Port) { throw 'rehearsal_port' }
        if (-not $DryRun -and -not (Test-CPAMPAdministrator)) {
            Write-CPAMPStep $context 'administrator' $false 'Run as Administrator: stopping/starting Windows services requires elevation. No service was changed.'
            throw 'administrator_required'
        }
        if (-not (Test-Path -LiteralPath $AppDir -PathType Container)) { throw 'app_directory_missing' }
        if (-not $RollbackOnly -and -not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw 'installed_exe_missing' }
        if (-not (Test-Path -LiteralPath (Split-Path -Parent $PanelPath) -PathType Container)) { throw 'panel_parent_missing' }
        if (-not $DryRun) {
            $service = Get-Service -Name $ServiceName -ErrorAction Stop
            if (@($service.DependentServices | Where-Object { $_ -and $_.Status -ne 'Stopped' }).Count) { throw 'service_dependents' }
            if (-not $RollbackOnly -and [string]$service.Status -ne 'Running') { throw 'service_not_running' }
        }
        [IO.Directory]::CreateDirectory($BackupDir) | Out-Null
        # Serialize even when two callers choose different BackupDir values.
        $lockId = [BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes($exe.ToLowerInvariant()))).Replace('-', '')
        $lockPath = Join-Path ([IO.Path]::GetTempPath()) ('cpamp-deploy-' + $lockId + '.lock')
        $lock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        $result.before = Get-CPAMPSnapshot $exe $PanelPath
        if ($RollbackOnly) {
            $backup = Get-CPAMPBackup $BackupManifest $BackupDir $Target $ServiceName $exe $PanelPath
            $result.backup = $backup
        } else {
            if (-not (Test-Path -LiteralPath $artifact -PathType Leaf)) { throw 'artifact_missing' }
            $artifactHash = Get-CPAMPHash $artifact
            $panelHash = Get-CPAMPHash $panelArtifact
            if (Test-Path -LiteralPath $RehearsalDataDir) { throw 'rehearsal_directory_exists' }
            $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $RehearsalPort)
            try {
                $listener.Start()
                $RehearsalPort = $listener.LocalEndpoint.Port
            } finally { $listener.Stop() }
            if ($RehearsalPort -eq 8317 -or $RehearsalPort -eq $health.Port) { throw 'rehearsal_port' }
        }
        Write-CPAMPStep $context 'preflight' $true 'Paths, target, permissions, inputs and isolated port validated; no credentials read.'
        if (-not $DryRun -and -not $RollbackOnly) {
            $phase = 'backup'
            $exeBackup = Join-Path $BackupDir ($stamp + '-' + $name)
            $panelBackup = $null
            Copy-Item -LiteralPath $exe -Destination $exeBackup -ErrorAction Stop
            if ($result.before.panelSha256) {
                $panelBackup = Join-Path $BackupDir ($stamp + '-management.html')
                Copy-Item -LiteralPath $PanelPath -Destination $panelBackup -ErrorAction Stop
            }
            $backup = [ordered]@{ target = $Target; serviceName = $ServiceName; createdAt = $started.ToString('o'); before = $result.before; exeBackup = $exeBackup; panelBackup = $panelBackup }
            $manifest = Join-Path $BackupDir ($stamp + '.manifest.json')
            Write-CPAMPJson $manifest $backup
            $backup = Get-CPAMPBackup $manifest $BackupDir $Target $ServiceName $exe $PanelPath
            $result.backup = $backup
            Write-CPAMPStep $context 'backup' $true 'Timestamped executable/panel backups and manifest verified.'
        }
        if (-not $RollbackOnly) {
            $phase = 'rehearsal'
            $rehearsalPanel = if ($panelHash) { $panelArtifact } elseif ($result.before.panelSha256) { $PanelPath } else { $null }
            Invoke-CPAMPRehearsal $context $artifact $rehearsalPanel $RehearsalDataDir $RehearsalPort $health $ExpectMarker $Target $TimeoutSeconds
            if ((Get-CPAMPHash $artifact) -ne $artifactHash -or (Get-CPAMPHash $panelArtifact) -ne $panelHash) { throw 'artifact_changed' }
            Write-CPAMPStep $context 'rehearsal' $true 'New executable healthy on isolated loopback port/data; process terminated and temporary data removed.'
        }
        if ($DryRun) {
            $result.status = 'deployed'
            Write-CPAMPStep $context 'dry_run' $true 'DRY RUN PASSED: rehearsal only; no deployment or service changes.'
            $code = 0
        } elseif ($RollbackOnly) {
            $phase = 'rollback'
            $changed = $true
            $result.serviceChanged = $true
            Invoke-CPAMPRestore $context $backup $exe $PanelPath $ServiceName $health $ExpectMarker $TimeoutSeconds
            $result.status = 'rolled_back'
            $code = 0
        } else {
            $phase = 'deploy'
            # Fail closed on a changed installation, before attempting to stop anything.
            if ((Get-CPAMPHash $exe) -ne $result.before.exeSha256 -or (Get-CPAMPHash $PanelPath) -ne $result.before.panelSha256) { throw 'installation_changed' }
            Assert-CPAMPBackupFiles $backup
            $changed = $true
            $result.serviceChanged = $true
            Set-CPAMPServiceState $ServiceName 'Stopped' $TimeoutSeconds
            Write-CPAMPStep $context 'stop' $true 'Service stopped.'
            Copy-Item -LiteralPath $artifact -Destination $exe -Force -ErrorAction Stop
            if ($panelHash) { Copy-Item -LiteralPath $panelArtifact -Destination $PanelPath -Force -ErrorAction Stop }
            if ((Get-CPAMPHash $exe) -ne $artifactHash -or ($panelHash -and (Get-CPAMPHash $PanelPath) -ne $panelHash)) { throw 'replacement_hash' }
            Write-CPAMPStep $context 'replace' $true 'Replacement hashes verified.'
            Set-CPAMPServiceState $ServiceName 'Running' $TimeoutSeconds
            Write-CPAMPStep $context 'start' $true 'Service running.'
            if (-not (Wait-CPAMPHealth $context $health $ExpectMarker 'deploy' $TimeoutSeconds)) { throw 'deploy_health' }
            Write-CPAMPStep $context 'health' $true 'HTTP 200 and expected marker verified.'
            $result.status = 'deployed'
            $code = 0
        }
        $phase = 'result_write'
        if ($context.logFailed) { throw 'text_log_failed' }
        $result.after = Get-CPAMPSnapshot $exe $PanelPath
        $result.finishedAt = [DateTime]::UtcNow.ToString('o')
        $result.durationSeconds = [Math]::Round(([DateTime]::UtcNow - $started).TotalSeconds, 3)
        Write-CPAMPJson $ResultPath $result
        $resultSaved = $true
    } catch {
        # Only fixed messages are logged. Exception text can contain paths, URLs or secrets.
        Write-CPAMPStep $context $phase $false (Get-CPAMPFailureDetail $_)
        if ($changed) {
            try {
                if ($RollbackOnly) { throw 'rollback_failed' }
                Invoke-CPAMPRestore $context $backup $exe $PanelPath $ServiceName $health $ExpectMarker $TimeoutSeconds
                $result.status = 'rolled_back'
                $code = 1
            } catch {
                $result.status = 'rollback_failed'
                $code = 3
                Write-CPAMPStep $context 'rollback' $false 'Automatic rollback failed. Manual intervention is required.'
            }
        } elseif ($phase -eq 'rehearsal') {
            $result.status = 'rehearsal_failed'
            $code = 2
        } else {
            $result.status = 'preflight_failed'
            $code = 2
        }
    } finally {
        try {
            if (-not $resultSaved) {
                try { if ($exe) { $result.after = Get-CPAMPSnapshot $exe $PanelPath } }
                catch { Write-CPAMPStep $context 'after_snapshot' $false 'Unable to read final hashes; inspect backups manually.' }
                $result.finishedAt = [DateTime]::UtcNow.ToString('o')
                $result.durationSeconds = [Math]::Round(([DateTime]::UtcNow - $started).TotalSeconds, 3)
                if ($result.status -eq 'rollback_failed') {
                    # Print even if snapshots, disk writes or log writes have failed.
                    $instructions = 'MANUAL RECOVERY: Open an elevated PowerShell. Stop service ' + $ServiceName + ', restore these backup files to ' + $exe + ' / ' + $PanelPath + ', start the service, then verify HTTP 200 + your expected marker and SHA256 hashes. EXE backup: ' + $backup.exeBackup + '; PANEL backup: ' + $(if ($backup.panelBackup) { $backup.panelBackup } else { '(panel originally absent; remove the introduced panel)' })
                    Write-CPAMPStep $context 'manual_recovery' $false $instructions
                }
                if ($outputsReady) { Write-CPAMPJson $ResultPath $result }
            }
        } catch {
            Write-Host 'RESULT WRITE FAILED: inspect the text log and timestamped backup manifests; no exception content disclosed.'
            if ($code -eq 0) { $code = 2 }
        } finally {
            if ($lock) { $lock.Dispose() }
            Write-Host ('RESULT: {0}; exit={1}; dryRun={2}' -f $result.status, $code, [bool]$DryRun)
        }
    }
    return $code
}

exit (Invoke-CPAMPDeployment @PSBoundParameters)
