#requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$CandidateRoot,
    [Parameter(Mandatory = $true)][string]$ManifestPath,
    [Parameter(Mandatory = $true)][string]$CompatibilityPath,
    [Parameter(Mandatory = $true)][string]$BackupRoot,
    [Parameter(Mandatory = $true)][string]$ReceiptPath,
    [string]$RuntimeRoot = 'D:\WILL\AGENT\CPA\runtime',
    [string]$ServiceRoot = 'D:\WILL\AGENT\CPA\ops\cpa-windows-services\runtime',
    [string]$HealthPlanPath,
    [ValidateSet('Deploy','PreflightOnly')][string]$Mode = 'Deploy',
    [ValidateRange(1,300)][int]$TimeoutSeconds = 60
)

function Get-CPAStackHash([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
}
function Write-CPAStackJSON([string]$Path, $Value) {
    [IO.Directory]::CreateDirectory((Split-Path -Parent $Path)) | Out-Null
    $temp=$Path+'.'+[guid]::NewGuid().ToString('N')+'.tmp'
    try {
        [IO.File]::WriteAllText($temp,($Value|ConvertTo-Json -Depth 16),[Text.UTF8Encoding]::new($false))
        Move-Item -LiteralPath $temp -Destination $Path -Force -ErrorAction Stop
    } finally { if(Test-Path -LiteralPath $temp){Remove-Item -LiteralPath $temp -Force} }
}
function Assert-CPAStackPath([string]$Path, [string]$Root) {
    $full=[IO.Path]::GetFullPath($Path);$base=[IO.Path]::GetFullPath($Root).TrimEnd('\','/')
    if($full -ne $base -and -not $full.StartsWith($base+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)){throw 'path_outside_root'}
    $current=$full
    while($current){
        if(Test-Path -LiteralPath $current){if(((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'reparse_path'}}
        $parent=Split-Path -Parent $current;if($parent -eq $current){break};$current=$parent
    }
    return $full
}
function Test-CPAStackPathOverlap([string]$Left, [string]$Right) {
    $leftFull=[IO.Path]::GetFullPath($Left).TrimEnd('\','/')
    $rightFull=[IO.Path]::GetFullPath($Right).TrimEnd('\','/')
    if($leftFull -eq $rightFull){return $true}
    $leftPrefix=$leftFull+[IO.Path]::DirectorySeparatorChar
    $rightPrefix=$rightFull+[IO.Path]::DirectorySeparatorChar
    return $leftFull.StartsWith($rightPrefix,[StringComparison]::OrdinalIgnoreCase) -or $rightFull.StartsWith($leftPrefix,[StringComparison]::OrdinalIgnoreCase)
}
function Get-CPAStackTree([string]$Root) {
    return @(Get-ChildItem -LiteralPath $Root -Force -File -Recurse -ErrorAction Stop | Sort-Object FullName | ForEach-Object {
        Assert-CPAStackPath $_.FullName $Root | Out-Null
        [pscustomobject]@{relative=$_.FullName.Substring($Root.TrimEnd('\').Length).TrimStart('\');sha256=(Get-CPAStackHash $_.FullName)}
    })
}
function Copy-CPAStackFile([string]$Source, [string]$Target) {
    [IO.Directory]::CreateDirectory((Split-Path -Parent $Target)) | Out-Null
    Copy-Item -LiteralPath $Source -Destination $Target -Force -ErrorAction Stop
}
function Test-CPAStackAdministrator {
    return ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Get-CPAStackServices($Context) {
    return @($Context.names | ForEach-Object {
        $service=Get-Service -Name $_ -ErrorAction Stop
        $cim=Get-CimInstance Win32_Service -Filter ("Name='"+$_+"'") -ErrorAction Stop
        $definition=$(if($_ -eq 'CPAUsageKeeper'){Join-Path $Context.runtime 'CPA-Usage-Keeper/CPAUsageKeeperService.xml'}else{Join-Path $Context.service ($_+'.xml')})
        [pscustomobject]@{name=$_;status=[string]$service.Status;startMode=[string]$cim.StartMode;startName=[string]$cim.StartName;pathName=[string]$cim.PathName;definitionHash=(Get-CPAStackHash $definition);dependents=@($service.DependentServices|Where-Object {$_.Status -eq 'Running'}|ForEach-Object {$_.Name})}
    })
}
function Set-CPAStackService($Context, [string]$Name, [string]$State) {
    if($Name -notin $Context.names){throw 'service_outside_stack'}
    if($State -eq 'Disabled'){Set-Service -Name $Name -StartupType Disabled -ErrorAction Stop;return}
    $service=Get-Service -Name $Name -ErrorAction Stop
    if([string]$service.Status -ne $State){
        if($State -eq 'Stopped'){Stop-Service -Name $Name -ErrorAction Stop}else{Start-Service -Name $Name -ErrorAction Stop}
        $service.WaitForStatus($State,[TimeSpan]::FromSeconds($Context.timeout))
    }
}
function Stop-CPAStack($Context) {
    $names=@($Context.names);[array]::Reverse($names)
    foreach($name in $names){Set-CPAStackService $Context $name 'Stopped'}
}
function Start-CPAStack($Context) {
    foreach($name in $Context.names){Set-CPAStackService $Context $name 'Running'}
}
function Stop-CPAStackSafely($Context) {
    $ok=$true
    # WinSW/SCM delayed recovery must not restart a partially rolled-back stack.
    foreach($name in $Context.names){try{Set-CPAStackService $Context $name 'Disabled'}catch{$ok=$false}}
    $names=@($Context.names);[array]::Reverse($names)
    foreach($name in $names){try{Set-CPAStackService $Context $name 'Stopped'}catch{$ok=$false}}
    try {
        $services=@(Get-CPAStackServices $Context)
        if($services.Count -ne 3 -or @($services|Where-Object {$_.status -ne 'Stopped' -or $_.startMode -notin @('Disabled')}).Count){$ok=$false}
    } catch {$ok=$false}
    return $ok
}
function Get-CPAStackRunningIdentity($Context) {
    $identities=@()
    foreach($name in $Context.names){
        $path=Assert-CPAStackPath (Join-Path $Context.runtime $Context.executables[$name]) $Context.runtime
        $processes=@(Get-CimInstance Win32_Process -ErrorAction Stop | Where-Object {$_.ExecutablePath -and $_.ExecutablePath.Equals($path,[StringComparison]::OrdinalIgnoreCase)})
        if($processes.Count -ne 1 -or -not $processes[0].CreationDate){throw 'running_image_unverified'}
        $identities+=@{name=$name;pid=$processes[0].ProcessId;startedAt=$processes[0].CreationDate.ToUniversalTime().ToString('o');path=$path;sha256=(Get-CPAStackHash $path)}
        if($name -eq 'LocalCPA'){
            $modules=@((Get-Process -Id $processes[0].ProcessId -ErrorAction Stop).Modules|ForEach-Object {$_.FileName})
            foreach($relative in @('CPA-Core/plugins/windows/amd64/opencode-go-cliproxyapi.dll','CPA-Core/plugins/windows/amd64/qwen-cliproxyapi.dll')){
                $dll=[IO.Path]::GetFullPath((Join-Path $Context.runtime $relative))
                if($dll -notin $modules){throw 'running_plugin_unverified'}
                $identities+=@{name=$relative;pid=$processes[0].ProcessId;path=$dll;sha256=(Get-CPAStackHash $dll)}
            }
        }
    }
    return $identities
}
function Test-CPAStackHealth($Context, [string]$Phase) {
    $results=@();$key=$null
    if(-not $Context.customHealth){
        $key=[IO.File]::ReadAllText((Join-Path $Context.runtime 'CPA-Manager-Plus/data/cpa-management-key.txt')).Trim()
        if(-not $key){throw 'management_key_missing'}
    }
    foreach($probe in $Context.probes){
        $uri=[uri]$probe.url
        if($uri.Scheme -ne 'http' -or $uri.Host -ne '127.0.0.1' -or $uri.UserInfo -or $uri.Query -or $uri.Fragment){throw 'health_url_not_loopback'}
        $clock=[Diagnostics.Stopwatch]::StartNew();$ready=$false
        do {
            $response=$null;$reader=$null
            try {
                $request=[Net.HttpWebRequest]::Create($uri);$request.Proxy=$null;$request.AllowAutoRedirect=$false
                $remaining=[Math]::Max(1,[int](1000*($Context.timeout-$clock.Elapsed.TotalSeconds)))
                $request.Timeout=[Math]::Min(2000,$remaining);$request.ReadWriteTimeout=$request.Timeout
                if($key -and $probe.name -ne 'CPAUsageKeeper'){$request.Headers['Authorization']='Bearer '+$key}
                $response=$request.GetResponse();$stream=$response.GetResponseStream();$reader=[IO.StreamReader]::new($stream)
                $buffer=New-Object char[] 4096;$body=[Text.StringBuilder]::new()
                while($body.Length -lt 1048576){
                    $remaining=[int](1000*($Context.timeout-$clock.Elapsed.TotalSeconds));if($remaining -le 0){throw 'health_deadline'}
                    $stream.ReadTimeout=[Math]::Min(2000,$remaining);$count=$reader.Read($buffer,0,[Math]::Min(4096,1048576-$body.Length));if($count -eq 0){break};[void]$body.Append($buffer,0,$count)
                }
                if($body.Length -ge 1048576){throw 'health_response_limit'}
                $ready=([int]$response.StatusCode -eq 200)
                if($probe.marker){$ready=$ready -and $body.ToString().Contains([string]$probe.marker)}
                if($probe.kind -eq 'gate'){$value=$body.ToString()|ConvertFrom-Json;$ready=$ready -and $value.ready -eq $true}
                if($probe.kind -eq 'inventory'){$value=$body.ToString()|ConvertFrom-Json;$ready=$ready -and -not $value.error}
            } catch { $ready=$false }
            finally {if($reader){$reader.Dispose()};if($response){$response.Close()}}
            if(-not $ready){Start-Sleep -Milliseconds 100}
        }while(-not $ready -and $clock.Elapsed.TotalSeconds -lt $Context.timeout)
        if(-not $ready){throw 'stack_health_failed'}
        $results+=@{name=$probe.name;phase=$Phase;status=200;elapsedMs=[int]$clock.Elapsed.TotalMilliseconds}
    }
    return $results
}
function Get-CPAStackPairIdentity($Pairs) {
    $canonical=@($Pairs|Sort-Object { [string]$_.target }|ForEach-Object {
        $target=([string]$_.target).ToLowerInvariant()
        $candidate=([string]$_.candidateSHA256).ToLowerInvariant()
        $rollback=([string]$_.rollbackSHA256).ToLowerInvariant()
        $target+'|'+$candidate+'|'+$rollback
    }) -join "`n"
    $sha=[Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha.ComputeHash([Text.UTF8Encoding]::new($false).GetBytes($canonical))) -replace '-','').ToLowerInvariant()
    } finally {$sha.Dispose()}
}
function Assert-CPAStackSemanticEvidence($Semantic, [string]$PairIdentity, [int]$ManifestSchema) {
    if($Semantic.schema -ne 1 -or $Semantic.manifestSchema -ne $ManifestSchema -or $Semantic.pairIdentity -ne $PairIdentity){throw 'compatibility_semantic_schema_mismatch'}
    # These are actual schema identities recorded by the artifact roundtrip,
    # not an assertion that the evidence JSON itself has schema=1.
    foreach($name in @('coreGate','managerRuntime','sqliteBefore','sqliteAfter')){
        if($Semantic.stateSchema.$name -isnot [string] -or [string]::IsNullOrWhiteSpace($Semantic.stateSchema.$name)){throw 'compatibility_state_schema_missing'}
    }
    foreach($check in @($Semantic.floor.candidateBelowCurrentRejected,$Semantic.floor.rejectionVerifiedBeforeStop)){
        if($check -isnot [bool] -or -not $check){throw 'compatibility_floor_rejection_unproven'}
    }
    foreach($check in @($Semantic.oldManager.canReadCandidateRuntimeSchema,$Semantic.oldManager.canReadMigratedCurrentSQLite)){
        if($check -isnot [bool] -or -not $check){throw 'compatibility_manager_schema_unproven'}
    }
    foreach($check in @($Semantic.ownerManual.manualIntentPreserved,$Semantic.ownerManual.currentStatePreserved)){
        if($check -isnot [bool] -or -not $check){throw 'compatibility_owner_intent_unproven'}
    }
}
function Assert-CPAStackCompatibility($Context, $Manifest) {
    $proof=Get-Content -LiteralPath $Context.compatibility -Raw -ErrorAction Stop|ConvertFrom-Json
    if($proof.schema -ne 1 -or $proof.accepted -ne $true -or $proof.gateVersion -ne 1 -or $proof.preserveCurrentState -ne $true){throw 'rollback_compatibility_unproven'}
    $core=@($Manifest.files|Where-Object {$_.target -eq 'CPA-Core/app/cli-proxy-api.exe'})
    $oldCore=Join-Path $Context.runtime 'CPA-Core/app/cli-proxy-api.exe'
    if($proof.candidateCoreSHA256 -ne $core[0].sha256 -or $proof.rollbackCoreSHA256 -ne (Get-CPAStackHash $oldCore)){throw 'rollback_core_incompatible'}
    if(@($proof.artifactPairs).Count -ne @($Manifest.files).Count){throw 'rollback_artifact_pair_incomplete'}
    foreach($file in $Manifest.files){
        $pair=@($proof.artifactPairs|Where-Object {$_.target -eq $file.target})
        $path=Join-Path $Context.runtime $file.target
        $oldHash=$(if(Test-Path -LiteralPath $path -PathType Leaf){Get-CPAStackHash $path}else{$null})
        if($pair.Count -ne 1 -or $pair[0].candidateSHA256 -ne $file.sha256 -or $pair[0].rollbackSHA256 -ne $oldHash){throw 'rollback_artifact_pair_mismatch'}
    }
    $pairIdentity=Get-CPAStackPairIdentity $proof.artifactPairs
    if($proof.pairIdentity -ne $pairIdentity){throw 'rollback_pair_identity_mismatch'}
    $semantic=$proof.semanticEvidence
    if(-not $semantic -or $semantic.evidenceClass -ne 'production'){throw 'compatibility_evidence_not_production'}
    Assert-CPAStackSemanticEvidence $semantic $pairIdentity $Manifest.schema
    if(-not $proof.evidence -or @($proof.evidence).Count -eq 0){throw 'compatibility_evidence_missing'}
    foreach($item in $proof.evidence){
        if($item.evidenceClass -ne 'production'){throw 'compatibility_evidence_not_production'}
        if($item.kind -ne 'compatibility-roundtrip' -or $item.pairIdentity -ne $pairIdentity){throw 'compatibility_evidence_binding_mismatch'}
        if(-not (Test-Path -LiteralPath $item.path -PathType Leaf) -or (Get-CPAStackHash $item.path) -ne $item.sha256){throw 'compatibility_evidence_changed'}
        try {$evidenceBody=Get-Content -LiteralPath $item.path -Raw -ErrorAction Stop|ConvertFrom-Json} catch {throw 'compatibility_evidence_not_production'}
        if($evidenceBody.evidenceClass -ne 'production' -or $evidenceBody.semanticEvidence.evidenceClass -ne 'production'){throw 'compatibility_evidence_not_production'}
        if($evidenceBody.schema -ne 1 -or $evidenceBody.kind -ne $item.kind -or $evidenceBody.pairIdentity -ne $pairIdentity){throw 'compatibility_evidence_binding_mismatch'}
        Assert-CPAStackSemanticEvidence $evidenceBody.semanticEvidence $pairIdentity $Manifest.schema
        foreach($name in @('coreGate','managerRuntime','sqliteBefore','sqliteAfter')){
            if($evidenceBody.semanticEvidence.stateSchema.$name -cne $semantic.stateSchema.$name){throw 'compatibility_evidence_schema_mismatch'}
        }
    }
    # This validates evidence supplied by the real artifact verifier. Isolated
    # fixtures are never production acceptance, and there is no fixture bypass.
    return Get-CPAStackHash $Context.compatibility
}
function New-CPAStackBackup($Context, $Manifest, $Services) {
    [IO.Directory]::CreateDirectory($Context.backup)|Out-Null
    $security=[Security.AccessControl.DirectorySecurity]::new();$security.SetAccessRuleProtection($true,$false)
    foreach($sid in @([Security.Principal.WindowsIdentity]::GetCurrent().User,[Security.Principal.SecurityIdentifier]::new('S-1-5-18'),[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))){$security.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid,'FullControl','ContainerInherit,ObjectInherit','None','Allow'))}
    [IO.Directory]::SetAccessControl($Context.backup,$security)
    $trees=@()
    foreach($item in @(@{name='runtime';path=$Context.runtime},@{name='services';path=$Context.service})){
        $before=Get-CPAStackTree $item.path;$dest=Join-Path $Context.backup $item.name
        Copy-Item -LiteralPath $item.path -Destination $dest -Recurse -ErrorAction Stop
        $after=Get-CPAStackTree $dest
        if((ConvertTo-Json -InputObject @($before) -Compress) -ne (ConvertTo-Json -InputObject @($after) -Compress)){throw 'backup_hash_mismatch'}
        $trees+=@{name=$item.name;files=$after}
    }
    $files=@($Manifest.files|ForEach-Object{
        $relative=[string]$_.target;$path=Join-Path $Context.runtime $relative
        @{target=$relative;existed=(Test-Path -LiteralPath $path -PathType Leaf);sha256=$(if(Test-Path -LiteralPath $path -PathType Leaf){Get-CPAStackHash $path}else{$null})}
    })
    $backup=@{schema=1;createdAt=[DateTime]::UtcNow.ToString('o');trees=$trees;files=$files;services=$Services;stateRollback='preserve-current-compatible-state';scriptSHA256=(Get-CPAStackHash $Context.script)}
    Write-CPAStackJSON (Join-Path $Context.backup 'backup-manifest.json') $backup
    return $backup
}
function Assert-CPAStackBackup($Context, $Backup) {
    foreach($tree in $Backup.trees){
        $path=Join-Path $Context.backup $tree.name
        $actual=Get-CPAStackTree $path
        if((ConvertTo-Json -InputObject @($actual) -Compress) -ne (ConvertTo-Json -InputObject @($tree.files) -Compress)){throw 'backup_changed'}
    }
}
function Restore-CPAStackArtifacts($Context, $Backup) {
    Stop-CPAStack $Context
    Assert-CPAStackBackup $Context $Backup
    foreach($file in $Backup.files){
        $target=Assert-CPAStackPath (Join-Path $Context.runtime $file.target) $Context.runtime
        if($file.existed){
            Copy-CPAStackFile (Join-Path $Context.backup ('runtime/'+$file.target)) $target
            if((Get-CPAStackHash $target) -ne $file.sha256){throw 'rollback_hash_mismatch'}
        } elseif(Test-Path -LiteralPath $target){Remove-Item -LiteralPath $target -Force -ErrorAction Stop}
    }
    # Never replace identity/seals, durable owners/floor, credentials or SQLite
    # with an older snapshot. The exact binary pair must pass current-state
    # compatibility first; an unproven/failed restart stays safely stopped.
    foreach($before in $Backup.services){
        $current=@(Get-CPAStackServices $Context|Where-Object {$_.name -eq $before.name})
        if($current.Count -ne 1 -or $current[0].definitionHash -ne $before.definitionHash -or $current[0].pathName -ne $before.pathName -or $current[0].startName -ne $before.startName -or $current[0].startMode -ne $before.startMode){throw 'service_definition_changed'}
    }
    Start-CPAStack $Context
}
function Invoke-CPALocalStackDeployment {
    [CmdletBinding()]
    param([string]$CandidateRoot,[string]$ManifestPath,[string]$CompatibilityPath,[string]$BackupRoot,[string]$ReceiptPath,[string]$RuntimeRoot='D:\WILL\AGENT\CPA\runtime',[string]$ServiceRoot='D:\WILL\AGENT\CPA\ops\cpa-windows-services\runtime',[string]$HealthPlanPath,[ValidateSet('Deploy','PreflightOnly')][string]$Mode='Deploy',[int]$TimeoutSeconds=60)
    $ErrorActionPreference='Stop';$changed=$false;$stopAttempted=$false;$backup=$null;$lock=$null;$code=2;$saved=$false;$receiptOwned=$false;$receiptWriteFailed=$false
    $ctx=@{runtime=[IO.Path]::GetFullPath($RuntimeRoot);service=[IO.Path]::GetFullPath($ServiceRoot);backup=[IO.Path]::GetFullPath($BackupRoot);candidate=[IO.Path]::GetFullPath($CandidateRoot);compatibility=[IO.Path]::GetFullPath($CompatibilityPath);script=$script:CPAStackEntrypoint;timeout=$TimeoutSeconds;names=@('LocalCPA','LocalCPAManagerPlus','CPAUsageKeeper');executables=@{LocalCPA='CPA-Core/app/cli-proxy-api.exe';LocalCPAManagerPlus='CPA-Manager-Plus/app/cpa-manager-plus.exe';CPAUsageKeeper='CPA-Usage-Keeper/cpa-usage-keeper.exe'};customHealth=[bool]$HealthPlanPath}
    if(-not $ctx.script){$ctx.script=Join-Path $PSScriptRoot 'deploy-local-stack.ps1'}
    $result=[ordered]@{schema=1;status='preflight_failed';success=$false;startedAt=[DateTime]::UtcNow.ToString('o');rollback='not-needed';steps=@();customHealth=$ctx.customHealth}
    try {
        if(Test-Path -LiteralPath $ReceiptPath){throw 'receipt_exists'}
        $ReceiptPath=[IO.Path]::GetFullPath($ReceiptPath)
        foreach($path in @($ctx.runtime,$ctx.service,$ctx.candidate)){Assert-CPAStackPath $path $path|Out-Null;if(-not(Test-Path -LiteralPath $path -PathType Container)){throw 'input_directory_missing'}}
        foreach($out in @($ctx.backup,$ReceiptPath)){
            foreach($input in @($ctx.runtime,$ctx.service,$ctx.candidate)){
                if(Test-CPAStackPathOverlap $out $input){throw 'output_overlap'}
            }
            Assert-CPAStackPath $out $out | Out-Null
        }
        if(Test-CPAStackPathOverlap $ctx.backup $ReceiptPath){throw 'output_overlap'}
        $receiptOwned=$true
        if(Test-Path -LiteralPath $ctx.backup){throw 'backup_exists'}
        $manifest=Get-Content -LiteralPath $ManifestPath -Raw|ConvertFrom-Json
        $required=@('CPA-Core/app/cli-proxy-api.exe','CPA-Manager-Plus/app/cpa-manager-plus.exe','CPA-Usage-Keeper/cpa-usage-keeper.exe','CPA-Core/static/management.html','CPA-Manager-Plus/panel/management.html','CPA-Core/plugins/windows/amd64/opencode-go-cliproxyapi.dll','CPA-Core/plugins/windows/amd64/qwen-cliproxyapi.dll','CPA-Core/tools/bailian-quota.exe')
        if($manifest.schema -ne 1 -or @($manifest.files).Count -ne $required.Count){throw 'manifest_schema'}
        $seen=@{};$before=@{}
        foreach($file in $manifest.files){
            if($file.target -notin $required -or $seen.ContainsKey($file.target) -or $file.sha256 -notmatch '^[a-f0-9]{64}$'){throw 'manifest_target'}
            $seen[$file.target]=$true;$source=Assert-CPAStackPath (Join-Path $ctx.candidate $file.relative) $ctx.candidate
            $target=Assert-CPAStackPath (Join-Path $ctx.runtime $file.target) $ctx.runtime
            if((Get-CPAStackHash $source) -ne $file.sha256){throw 'candidate_hash_mismatch'}
            $before[$file.target]=$(if(Test-Path -LiteralPath $target -PathType Leaf){Get-CPAStackHash $target}else{$null})
        }
        $result.compatibilitySHA256=Assert-CPAStackCompatibility $ctx $manifest
        $result.scriptSHA256=Get-CPAStackHash $ctx.script
        if($ctx.customHealth){$plan=Get-Content -LiteralPath $HealthPlanPath -Raw|ConvertFrom-Json;if($plan.schema -ne 1){throw 'health_plan_schema'};$ctx.probes=@($plan.probes)}
        else{$ctx.probes=@(@{name='LocalCPA';url='http://127.0.0.1:8317/v8/management/inference-gates';kind='gate'},@{name='LocalCPAManagerPlus';url='http://127.0.0.1:18317/usage-service/full-refresh';kind='inventory'},@{name='CPAUsageKeeper';url='http://127.0.0.1:8318/readyz'})}
        if(@($ctx.probes).Count -ne 3 -or @($ctx.probes|Select-Object -ExpandProperty name -Unique).Count -ne 3){throw 'health_plan_incomplete'}
        $services=@(Get-CPAStackServices $ctx)
        if($services.Count -ne 3 -or @($services|Where-Object {$_.status -ne 'Running'}).Count){throw 'service_not_running'}
        foreach($service in $services){if(@($service.dependents|Where-Object {$_ -notin $ctx.names}).Count){throw 'external_dependent'}}
        if($Mode -eq 'PreflightOnly'){$result.status='preflight_passed';$result.success=$true;$code=0}
        else {
            if(-not(Test-CPAStackAdministrator)){throw 'administrator_required'}
            $lock=[IO.File]::Open((Join-Path (Split-Path -Parent $ctx.runtime) '.cpa-stack-deploy.lock'),[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
            $result.beforeHealth=@(Test-CPAStackHealth $ctx 'before')
            $stopAttempted=$true;Stop-CPAStack $ctx
            $backup=New-CPAStackBackup $ctx $manifest $services
            $result.backup=$ctx.backup;$result.backupManifestSHA256=Get-CPAStackHash (Join-Path $ctx.backup 'backup-manifest.json')
            $result.status='backed_up';try{Write-CPAStackJSON $ReceiptPath $result}catch{$receiptWriteFailed=$true;throw}
            foreach($file in $manifest.files){
                $source=Assert-CPAStackPath (Join-Path $ctx.candidate $file.relative) $ctx.candidate
                $target=Assert-CPAStackPath (Join-Path $ctx.runtime $file.target) $ctx.runtime
                $current=$(if(Test-Path -LiteralPath $target -PathType Leaf){Get-CPAStackHash $target}else{$null})
                if($current -ne $before[$file.target] -or (Get-CPAStackHash $source) -ne $file.sha256){throw 'installation_or_candidate_changed'}
                $changed=$true;Copy-CPAStackFile $source $target
                if((Get-CPAStackHash $target) -ne $file.sha256){throw 'replacement_hash_mismatch'}
                $result.steps+=@{stage='copy';target=$file.target;sha256=$file.sha256}
            }
            Start-CPAStack $ctx
            $result.afterHealth=@(Test-CPAStackHealth $ctx 'candidate')
            $result.running=@(Get-CPAStackRunningIdentity $ctx)
            foreach($file in $manifest.files){if((Get-CPAStackHash (Join-Path $ctx.runtime $file.target)) -ne $file.sha256){throw 'post_start_hash_mismatch'}}
            $result.installed=@($manifest.files|ForEach-Object{@{target=$_.target;sha256=$_.sha256;source_head=$_.source_head;sdk_revision=$_.sdk_revision}})
            $result.status='deployed';$result.success=$true;$result.rollback='not-required';$code=0
        }
        $result.finishedAt=[DateTime]::UtcNow.ToString('o');try{Write-CPAStackJSON $ReceiptPath $result}catch{$receiptWriteFailed=$true;throw};$saved=$true
    } catch {
        # OS/process error messages can contain private paths or credentials.
        $result.success=$false
        if($code -eq 0){$code=2}
        $result.errorCode=$(if($_.Exception.Message -match '^[a-z_]+$'){$_.Exception.Message}else{'operation_failed'})
        if($changed -and $backup){
            try {
                Restore-CPAStackArtifacts $ctx $backup
                $result.rollbackHealth=@(Test-CPAStackHealth $ctx 'rollback')
                $result.running=@(Get-CPAStackRunningIdentity $ctx)
                $result.status='rolled_back';$result.rollback='verified-current-state-preserved';$code=1
            } catch {
                $result.safeStopVerified=Stop-CPAStackSafely $ctx
                $result.status='rollback_failed';$result.rollback=$(if($result.safeStopVerified){'failed-safe-stop'}else{'failed-stop-unverified'});$result.recovery='Keep the stack stopped and disabled. Preserve current identity/gates/credentials/SQLite. Inspect the private stopped-state backup and compatibility evidence; restore original startup modes only after recovery verification.';$code=3
            }
        } elseif($stopAttempted){
            try{Start-CPAStack $ctx;$result.rollbackHealth=@(Test-CPAStackHealth $ctx 'rollback');$result.status='rolled_back';$result.rollback='pre-copy-recovered';$code=1}
            catch{$result.safeStopVerified=Stop-CPAStackSafely $ctx;$result.status='rollback_failed';$result.rollback=$(if($result.safeStopVerified){'failed-safe-stop'}else{'failed-stop-unverified'});$code=3}
        }
        if($receiptWriteFailed){
            $result.receiptWriteFailed=$true;$result.success=$false;$result.errorCode='receipt_write_failed'
            if($result.status -in @('deployed','preflight_passed','preflight_failed')){$result.status='receipt_failed'}
            if($code -eq 0){$code=4}
        }
    } finally {
        if($lock){$lock.Dispose()}
        if(-not $saved -and $receiptOwned){
            try{$result.finishedAt=[DateTime]::UtcNow.ToString('o');Write-CPAStackJSON $ReceiptPath $result;$saved=$true}
            catch{
                $receiptWriteFailed=$true;$result.receiptWriteFailed=$true;$result.success=$false;$result.errorCode='receipt_write_failed'
                if($result.status -notin @('rolled_back','rollback_failed')){$result.status='receipt_failed'}
                Write-Host 'Receipt unavailable; inspect the private backup before recovery.';$code=3
            }
        }
        Write-Host ('RESULT: '+$result.status+'; exit='+$code)
    }
    return $code
}

$script:CPAStackEntrypoint=$MyInvocation.MyCommand.Path
exit (Invoke-CPALocalStackDeployment @PSBoundParameters)
