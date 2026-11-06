#requires -Version 5.1
param([Parameter(Mandatory = $true)][string]$OutputDir,
      [ValidateSet('All','ShadowIdentity')][string]$Cases = 'All')
$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$entry = Join-Path $repo 'ops\windows\deploy-local-stack.ps1'
if (Test-Path -LiteralPath $OutputDir) { throw 'A new disposable output directory is required.' }
[IO.Directory]::CreateDirectory($OutputDir) | Out-Null
function Assert-True($Value, [string]$Message) { if (-not $Value) { throw ('ASSERTION: ' + $Message) } }
$tokens = $null; $errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($entry, [ref]$tokens, [ref]$errors)
Assert-True ($errors.Count -eq 0) 'entrypoint parses'
foreach ($node in $ast.EndBlock.Statements) {
    if ($node -is [Management.Automation.Language.FunctionDefinitionAst]) { . ([scriptblock]::Create($node.Extent.Text)) }
}
$script:CPAStackEntrypoint = $entry
$names = @('LocalCPA', 'LocalCPAManagerPlus', 'CPAUsageKeeper')
$script:processes = @{}; $script:ports = @{}; $script:serviceCalls = [Collections.Generic.List[string]]::new()
$script:startupModes = @{}
$script:failure = ''; $script:copyCount = 0
$script:realCompatibility = ${function:Assert-CPAStackCompatibility}
$script:realRunningIdentity = ${function:Get-CPAStackRunningIdentity}
$script:useShadowIdentity = $false
$script:shadowOptions = $null
$script:shadowMode = 'Correct'
$script:receiptFailurePath = ''
$script:receiptWriteAttempts = 0
$script:receiptFailAt = 1
$script:receiptFailPermanently = $false
$script:useProductionCompatibility = $false

# Fixture deployment is intentionally behind a test-only seam. The production
# gate must reject this proof class; this adapter only exercises the isolated
# file/process/SQLite rollback harness without claiming real CPA evidence.
function Assert-CPAStackCompatibility {
    param($Context, $Manifest)
    if ($script:useProductionCompatibility) { return & $script:realCompatibility $Context $Manifest }
    $proof = Get-Content -LiteralPath $Context.compatibility -Raw | ConvertFrom-Json
    $semantic = $proof.semanticEvidence
    if ($semantic.evidenceClass -ne 'fixture' -or $semantic.schema -ne 1) { throw 'fixture_semantic_proof_required' }
    Assert-CPAStackSemanticEvidence $semantic $proof.pairIdentity $Manifest.schema
    $core = @($Manifest.files | Where-Object { $_.target -eq 'CPA-Core/app/cli-proxy-api.exe' })
    $oldCore = Join-Path $Context.runtime 'CPA-Core/app/cli-proxy-api.exe'
    if ($proof.candidateCoreSHA256 -ne $core[0].sha256 -or $proof.rollbackCoreSHA256 -ne (Get-CPAStackHash $oldCore)) { throw 'fixture_core_pair_mismatch' }
    if (@($proof.artifactPairs).Count -ne @($Manifest.files).Count) { throw 'fixture_artifact_pair_incomplete' }
    foreach ($file in $Manifest.files) {
        $pair = @($proof.artifactPairs | Where-Object { $_.target -eq $file.target })
        $path = Join-Path $Context.runtime $file.target
        $oldHash = if (Test-Path -LiteralPath $path -PathType Leaf) { Get-CPAStackHash $path } else { $null }
        if ($pair.Count -ne 1 -or $pair[0].candidateSHA256 -ne $file.sha256 -or $pair[0].rollbackSHA256 -ne $oldHash) { throw 'fixture_artifact_pair_mismatch' }
    }
    $expectedPairIdentity = Get-CPAStackPairIdentity $proof.artifactPairs
    if ($semantic.pairIdentity -ne $proof.pairIdentity -or $proof.pairIdentity -ne $expectedPairIdentity) { throw 'fixture_pair_identity_missing' }
    if (-not $proof.evidence -or @($proof.evidence).Count -eq 0 -or @($proof.evidence | Where-Object { $_.evidenceClass -eq 'fixture' -and $_.pairIdentity -eq $proof.pairIdentity }).Count -ne @($proof.evidence).Count) { throw 'fixture_evidence_binding_missing' }
    return Get-CPAStackHash $Context.compatibility
}

function Move-Item {
    [CmdletBinding()]
    param([string]$LiteralPath, [string]$Destination, [switch]$Force)
    $full = [IO.Path]::GetFullPath($Destination)
    if ($script:receiptFailurePath -and $full -eq [IO.Path]::GetFullPath($script:receiptFailurePath)) {
        $script:receiptWriteAttempts++
        if ($script:receiptWriteAttempts -eq $script:receiptFailAt -or ($script:receiptFailPermanently -and $script:receiptWriteAttempts -ge $script:receiptFailAt)) { throw 'fixture private-key=must-not-leak atomic rename failure' }
    }
    Microsoft.PowerShell.Management\Move-Item -LiteralPath $LiteralPath -Destination $Destination -Force:$Force -ErrorAction Stop
}
foreach ($name in $names) {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start(); $script:ports[$name] = $listener.LocalEndpoint.Port; $listener.Stop()
    Assert-True ($script:ports[$name] -notin @(8317,18317,8318)) 'fixtures do not use live ports'
}
$source = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'fixtures\deploy-fixture.cs'))
$compiled = @{}
foreach ($mode in @('old','new')) {
    $compiled[$mode] = Join-Path $OutputDir ($mode + '.exe')
    Add-Type -TypeDefinition $source.Replace('FIXTURE_MODE', $mode).Replace('DeployFixture', ('StackFixture_' + $mode)) -OutputAssembly $compiled[$mode] -OutputType ConsoleApplication
}

function Test-CPAStackAdministrator { return $true }
function Get-CPAStackServices {
    param($Context)
    return @($names | ForEach-Object { [pscustomobject]@{ name=$_; status=$(if($script:processes.ContainsKey($_)){'Running'}else{'Stopped'}); startMode=$(if($script:startupModes.ContainsKey($_)){$script:startupModes[$_]}else{'Automatic'}); definitionHash='fixture'; dependents=@() } })
}
function Set-CPAStackService {
    param($Context, [string]$Name, [string]$State)
    Assert-True ($Name -in $names) 'only isolated process services addressed'
    $script:serviceCalls.Add($State + ':' + $Name)
    if($State -eq 'Disabled'){$script:startupModes[$Name]='Disabled';return}
    if ($State -eq 'Stopped') {
        if ($script:processes.ContainsKey($Name)) {
            $p=$script:processes[$Name]; if(-not $p.HasExited){$p.Kill()}; $p.WaitForExit(); $p.Dispose(); $script:processes.Remove($Name)
        }
        return
    }
    $image=Join-Path $Context.runtime $Context.executables[$Name]
    if ($script:failure -eq 'Rollback' -or ($script:failure -eq 'Start' -and (Get-CPAStackHash $image) -eq (Get-CPAStackHash $compiled.new))) { throw 'fixture_start' }
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = Join-Path $Context.runtime $Context.executables[$Name]
    $info.WorkingDirectory = Split-Path -Parent $info.FileName
    $info.UseShellExecute=$false; $info.EnvironmentVariables.Clear(); $info.EnvironmentVariables['SystemRoot']=$env:SystemRoot
    $info.EnvironmentVariables['HTTP_ADDR']='127.0.0.1:'+$script:ports[$Name]
    $info.RedirectStandardOutput=$true; $info.RedirectStandardError=$true
    $p=[Diagnostics.Process]::Start($info); $p.BeginOutputReadLine(); $p.BeginErrorReadLine()
    $script:processes[$Name]=$p
}
$script:realCopy = ${function:Copy-CPAStackFile}
function Copy-CPAStackFile {
    param([string]$Source, [string]$Target)
    & $script:realCopy $Source $Target
    $script:copyCount++
    if ($script:failure -eq 'Copy' -and $script:copyCount -eq 1) { throw 'fixture_copy' }
}
$script:realSmoke = ${function:Test-CPAStackHealth}
function Test-CPAStackHealth {
    param($Context, [string]$Phase)
    if ($Phase -eq 'candidate' -and $script:failure -eq 'Migration') {
        Invoke-FixtureSQLite (Join-Path $Context.runtime 'CPA-Manager-Plus/data/usage.sqlite') 'migrate' | Out-Null
        throw 'fixture_migration'
    }
    if ($Phase -eq 'candidate' -and $script:failure -eq 'Health') { throw 'fixture_health' }
    if ($Phase -eq 'candidate' -and $script:failure -eq 'PolicyThenHealth') {
        [IO.File]::WriteAllText((Join-Path $Context.runtime 'CPA-Core\.resource-catalog\manual-owner.txt'), 'foreign-owner-must-survive')
        Invoke-FixtureSQLite (Join-Path $Context.runtime 'CPA-Manager-Plus/data/usage.sqlite') 'add-owner' | Out-Null
        throw 'fixture_health'
    }
    try { & $script:realSmoke $Context $Phase }
    catch {
        Write-Host ('FIXTURE health failure phase='+$Phase+' exited='+(@($script:processes.GetEnumerator()|ForEach-Object {$_.Key+':'+$_.Value.HasExited}) -join ','))
        throw
    }
}
function Get-CPAStackRunningIdentity {
    param($Context)
    if ($script:useShadowIdentity) { return & $script:realRunningIdentity $Context }
    # Native DLL loading and SCM identity are separate production gates, not fixture claims.
    return @($names | ForEach-Object { $p=$script:processes[$_]; Assert-True (-not $p.HasExited) 'owned process remains live'; @{name=$_;pid=$p.Id;sha256=(Get-CPAStackHash (Join-Path $Context.runtime $Context.executables[$_]))} })
}

function Get-Process {
    [CmdletBinding()]
    param([int]$Id)
    if (-not $script:useShadowIdentity -or $Id -ne $script:processes['LocalCPA'].Id) {
        return Microsoft.PowerShell.Management\Get-Process -Id $Id -ErrorAction Stop
    }
    # External OS-module adapter only: these are declared synthetic module
    # records and real files, NOT a claim that the fixture mapped native DLLs.
    $core=Join-Path $script:shadowOptions.RuntimeRoot 'CPA-Core/app/cli-proxy-api.exe'
    $candidate=(Get-CPAStackHash $core) -eq (Get-CPAStackHash $compiled.new)
    if($candidate -and $script:shadowMode -eq 'Missing'){return [pscustomobject]@{Modules=@()}}
    $owner=$(if($candidate -and $script:shadowMode -eq 'WrongPid'){$Id+1}else{$Id})
    $directory=Join-Path $script:shadowOptions.RuntimeRoot ('shadow-fixture/cliproxy-pluginhost/pid-'+$owner)
    [IO.Directory]::CreateDirectory($directory)|Out-Null
    $modules=@()
    foreach($name in @('cpa-credential-opencode','cpa-credential-qwen')){
        $artifact=Join-Path $script:shadowOptions.RuntimeRoot ('CPA-Core/plugins/windows/amd64/'+$name+'.dll')
        $digest=Get-CPAStackHash $artifact
        $mapped=Join-Path $directory ('cliproxy-plugin-'+$name+'-'+$digest.Substring(0,32)+'.dll')
        Copy-Item -LiteralPath $artifact -Destination $mapped -Force
        if($candidate -and $script:shadowMode -eq 'WrongHash'){[IO.File]::AppendAllText($mapped,'different-full-content')}
        $modules+=@([pscustomobject]@{FileName=$(if($script:shadowMode -eq 'Direct'){$artifact}else{$mapped})})
        if($candidate -and $script:shadowMode -eq 'Ambiguous'){
            $other=Join-Path $script:shadowOptions.RuntimeRoot ('second-shadow-fixture/cliproxy-pluginhost/pid-'+$Id)
            [IO.Directory]::CreateDirectory($other)|Out-Null
            $duplicate=Join-Path $other ([IO.Path]::GetFileName($mapped))
            Copy-Item -LiteralPath $artifact -Destination $duplicate -Force
            $modules+=@([pscustomobject]@{FileName=$duplicate})
        }
    }
    return [pscustomobject]@{Modules=$modules}
}

function Invoke-FixtureSQLite([string]$Path, [string]$Mode) {
    $code=@'
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
mode = sys.argv[2]
if mode == 'init':
    c.execute('CREATE TABLE policy(owner TEXT PRIMARY KEY, disabled INTEGER NOT NULL)')
    c.execute('INSERT INTO policy VALUES (?, ?)', ('manual-original', 1))
elif mode == 'migrate':
    c.execute('CREATE TABLE additive_candidate_migration(id INTEGER PRIMARY KEY)')
elif mode == 'add-owner':
    c.execute('INSERT INTO policy VALUES (?, ?)', ('foreign-new', 1))
elif mode == 'read':
    print(c.execute('PRAGMA integrity_check').fetchone()[0])
    for owner, disabled in c.execute('SELECT owner, disabled FROM policy ORDER BY owner'):
        print(owner + ':' + str(disabled))
c.commit()
c.close()
'@
    $value=& python -c $code $Path $Mode
    if($LASTEXITCODE -ne 0){throw 'fixture_sqlite_failed'}
    return $value
}

function New-Case([string]$Name) {
    $case=Join-Path $OutputDir $Name; [IO.Directory]::CreateDirectory($case)|Out-Null
    $runtime=Join-Path $case 'runtime'; $candidate=Join-Path $case 'candidate'; $service=Join-Path $case 'service'
    [IO.Directory]::CreateDirectory($runtime)|Out-Null; [IO.Directory]::CreateDirectory($candidate)|Out-Null; [IO.Directory]::CreateDirectory($service)|Out-Null
    $targets=@('CPA-Core/app/cli-proxy-api.exe','CPA-Manager-Plus/app/cpa-manager-plus.exe','CPA-Usage-Keeper/cpa-usage-keeper.exe','CPA-Core/static/management.html','CPA-Manager-Plus/panel/management.html','CPA-Core/plugins/windows/amd64/cpa-credential-opencode.dll','CPA-Core/plugins/windows/amd64/cpa-credential-qwen.dll','CPA-Core/tools/bailian-quota.exe')
    $files=@(); $index=0
    foreach($target in $targets){
        $installed=Join-Path $runtime $target; [IO.Directory]::CreateDirectory((Split-Path -Parent $installed))|Out-Null
        $artifact=Join-Path $candidate ($index.ToString()+'.bin')
        if($target.EndsWith('.exe')){Copy-Item -LiteralPath $compiled.old -Destination $installed; Copy-Item -LiteralPath $compiled.new -Destination $artifact}
        else{[IO.File]::WriteAllText($installed,'old-file');[IO.File]::WriteAllText($artifact,'new-file')}
        $files+=@{relative=($index.ToString()+'.bin');target=$target;sha256=(Get-CPAStackHash $artifact)}; $index++
    }
    foreach($relative in @('CPA-Core/config.yaml','CPA-Core/config.yaml.gate-floor-v1','CPA-Core/.resource-catalog/gate-floor-v1','CPA-Core/.resource-catalog/catalog.json','CPA-Core/auths/credential.json','CPA-Manager-Plus/config.json')){
        $path=Join-Path $runtime $relative;[IO.Directory]::CreateDirectory((Split-Path -Parent $path))|Out-Null;[IO.File]::WriteAllText($path,'private-fixture-state')
    }
    [IO.Directory]::CreateDirectory((Join-Path $runtime 'CPA-Manager-Plus/data')) | Out-Null
    Invoke-FixtureSQLite (Join-Path $runtime 'CPA-Manager-Plus/data/usage.sqlite') 'init' | Out-Null
    $manifest=Join-Path $case 'manifest.json'; Write-CPAStackJSON $manifest @{schema=1;files=$files}
    $trace=Join-Path $case 'compatibility-evidence.txt';[IO.File]::WriteAllText($trace,'isolated process-fixture contract, not real Core gate acceptance')
    $pairs=@($files|ForEach-Object{@{target=$_.target;candidateSHA256=$_.sha256;rollbackSHA256=(Get-CPAStackHash (Join-Path $runtime $_.target))}})
    $pairIdentity=Get-CPAStackPairIdentity $pairs
    $semantic=@{schema=1;manifestSchema=1;evidenceClass='fixture';pairIdentity=$pairIdentity;stateSchema=@{coreGate='fixture-only-gate-v1';managerRuntime='fixture-only-runtime-v1';sqliteBefore='fixture-only-policy-v1';sqliteAfter='fixture-only-additive-v2'};floor=@{candidateBelowCurrentRejected=$true;rejectionVerifiedBeforeStop=$true};oldManager=@{canReadCandidateRuntimeSchema=$true;canReadMigratedCurrentSQLite=$true};ownerManual=@{manualIntentPreserved=$true;currentStatePreserved=$true}}
    $proof=Join-Path $case 'compatibility.json'; Write-CPAStackJSON $proof @{schema=1;accepted=$true;gateVersion=1;candidateCoreSHA256=(Get-CPAStackHash (Join-Path $candidate '0.bin'));rollbackCoreSHA256=(Get-CPAStackHash (Join-Path $runtime $targets[0]));artifactPairs=$pairs;pairIdentity=$pairIdentity;preserveCurrentState=$true;semanticEvidence=$semantic;evidence=@(@{kind='fixture-compatibility';evidenceClass='fixture';pairIdentity=$pairIdentity;path=$trace;sha256=(Get-CPAStackHash $trace)})}
    $health=@($names|ForEach-Object{@{name=$_;url=('http://127.0.0.1:'+$script:ports[$_]+'/health');marker='fixture-healthy'}})
    $healthPath=Join-Path $case 'health.json';Write-CPAStackJSON $healthPath @{schema=1;probes=$health}
    return @{CandidateRoot=$candidate;ManifestPath=$manifest;RuntimeRoot=$runtime;ServiceRoot=$service;BackupRoot=(Join-Path $case 'backup');ReceiptPath=(Join-Path $case 'receipt.json');CompatibilityPath=$proof;HealthPlanPath=$healthPath;TimeoutSeconds=3}
}
function Invoke-Case([string]$Name, [string]$Failure) {
    $options=New-Case $Name; $script:failure='';$script:copyCount=0;$script:startupModes.Clear()
    $ctx=@{runtime=$options.RuntimeRoot;executables=@{LocalCPA='CPA-Core/app/cli-proxy-api.exe';LocalCPAManagerPlus='CPA-Manager-Plus/app/cpa-manager-plus.exe';CPAUsageKeeper='CPA-Usage-Keeper/cpa-usage-keeper.exe'}}
    foreach($serviceName in $names){Set-CPAStackService $ctx $serviceName 'Running'}
    $script:serviceCalls.Clear(); $script:failure=$Failure
    if($Failure -eq 'Preflight'){
        $p=Get-Content -LiteralPath $options.CompatibilityPath -Raw|ConvertFrom-Json;$p.rollbackCoreSHA256=('0'*64);Write-CPAStackJSON $options.CompatibilityPath $p
    }
    if($Failure -eq 'PairPreflight'){
        $p=Get-Content -LiteralPath $options.CompatibilityPath -Raw|ConvertFrom-Json;$p.artifactPairs[1].rollbackSHA256=('0'*64);Write-CPAStackJSON $options.CompatibilityPath $p
    }
    if($Failure -eq 'SemanticFloor'){
        $p=Get-Content -LiteralPath $options.CompatibilityPath -Raw|ConvertFrom-Json;$p.semanticEvidence.floor.candidateBelowCurrentRejected=$false;Write-CPAStackJSON $options.CompatibilityPath $p
    }
    $code=Invoke-CPALocalStackDeployment @options
    $receipt=Get-Content -LiteralPath $options.ReceiptPath -Raw|ConvertFrom-Json
    if($Failure -eq ''){Assert-True ($code -eq 0 -and $receipt.status -eq 'deployed') 'candidate deployed'}
    elseif($Failure -in @('Preflight','PairPreflight','SemanticFloor')){Assert-True ($code -ne 0 -and $receipt.status -eq 'preflight_failed' -and $script:serviceCalls.Count -eq 0) 'compatibility proof refused before stop'}
    elseif($Failure -eq 'Rollback'){
        Assert-True ($code -eq 3 -and $receipt.status -eq 'rollback_failed' -and $script:processes.Count -eq 0) 'rollback start failure leaves every process stopped'
        Assert-True ($receipt.safeStopVerified -eq $true -and $script:startupModes.Count -eq 3 -and @($script:startupModes.Values|Where-Object {$_ -ne 'Disabled'}).Count -eq 0) 'delayed automatic restarts blocked for every fixture service'
    }
    else{
        Assert-True ($code -eq 1 -and $receipt.status -eq 'rolled_back') ($Name+' real rollback')
        Assert-True ((Get-CPAStackHash (Join-Path $options.RuntimeRoot 'CPA-Core/app/cli-proxy-api.exe')) -eq (Get-CPAStackHash $compiled.old)) 'old image restored'
        Assert-True ($receipt.rollbackHealth.Count -eq 3) 'all old processes HTTP healthy'
        Assert-True ($receipt.success -eq $false) 'failed deployment never reports success'
    }
    $sqlite=@(Invoke-FixtureSQLite (Join-Path $options.RuntimeRoot 'CPA-Manager-Plus/data/usage.sqlite') 'read')
    Assert-True ('ok' -in $sqlite -and 'manual-original:1' -in $sqlite) 'real SQLite remains valid and manual restriction survives'
    if($Failure -eq 'PolicyThenHealth'){
        Assert-True ([IO.File]::ReadAllText((Join-Path $options.RuntimeRoot 'CPA-Core/.resource-catalog/manual-owner.txt')) -eq 'foreign-owner-must-survive') 'new foreign restriction preserved'
        Assert-True ('foreign-new:1' -in $sqlite) 'new SQLite policy restriction preserved'
    }
    Write-Host ('CASE '+$Name+': '+$receipt.status+' exit='+$code)
    $script:failure=''; foreach($serviceName in $names){Set-CPAStackService $ctx $serviceName 'Stopped'}
}
function Invoke-ShadowIdentityCase([string]$Mode='Correct') {
    $options=New-Case ('shadow-identity-'+$Mode);$script:shadowOptions=$options;$script:shadowMode=$Mode
    $script:failure='';$script:copyCount=0;$script:startupModes.Clear()
    $ctx=@{runtime=$options.RuntimeRoot;executables=@{LocalCPA='CPA-Core/app/cli-proxy-api.exe';LocalCPAManagerPlus='CPA-Manager-Plus/app/cpa-manager-plus.exe';CPAUsageKeeper='CPA-Usage-Keeper/cpa-usage-keeper.exe'}}
    foreach($name in $names){Set-CPAStackService $ctx $name 'Running'}
    $script:serviceCalls.Clear();$script:useShadowIdentity=$true
    try {
        $code=Invoke-CPALocalStackDeployment @options
        $receipt=Get-Content -LiteralPath $options.ReceiptPath -Raw|ConvertFrom-Json
        if($Mode -in @('Correct','Direct')){
            Assert-True ($code -eq 0 -and $receipt.status -eq 'deployed') 'correct mapped modules are accepted by the deployment entrypoint'
        } else {
            Assert-True ($code -eq 1 -and $receipt.status -eq 'rolled_back' -and -not $receipt.success) 'invalid candidate module identity triggers verified rollback'
            $expected=$(if($Mode -eq 'WrongHash'){'running_plugin_hash_mismatch'}else{'running_plugin_unverified'})
            Assert-True ($receipt.errorCode -eq $expected -and $receipt.rollback -eq 'verified-current-state-preserved') 'wrong identity is never accepted or silently downgraded'
            $sqlite=@(Invoke-FixtureSQLite (Join-Path $options.RuntimeRoot 'CPA-Manager-Plus/data/usage.sqlite') 'read')
            Assert-True ('ok' -in $sqlite -and 'manual-original:1' -in $sqlite) 'identity failure preserves the same current SQLite/manual restriction'
        }
        $dlls=@($receipt.running|Where-Object {$_.name.EndsWith('.dll')})
        Assert-True ($dlls.Count -eq 2) 'both actual mapped DLL identities are recorded'
        foreach($dll in $dlls){
            if($Mode -eq 'Direct'){Assert-True ($dll.path -eq $dll.artifactPath) 'classic direct mapping remains supported'}
            else{Assert-True ($dll.path -ne $dll.artifactPath -and $dll.path.Contains('cliproxy-pluginhost')) 'receipt distinguishes mapped shadow from installed slot'}
            Assert-True ($dll.sha256 -eq (Get-CPAStackHash $dll.artifactPath)) 'full module digest matches the installed artifact'
        }
        Write-Host ('CASE shadow-identity-'+$Mode+': '+$receipt.status+'; production identity function, modeled module adapter; fixture only')
    } finally {
        $script:useShadowIdentity=$false;$script:shadowOptions=$null;$script:shadowMode='Correct'
        foreach($name in $names){Set-CPAStackService $ctx $name 'Stopped'}
    }
}
function Invoke-OverlapCase([string]$Name, [string]$BackupPath, [string]$ReceiptPath) {
    $options=New-Case $Name; $options.BackupRoot=$BackupPath; $options.ReceiptPath=$ReceiptPath; $script:failure='';$script:copyCount=0;$script:startupModes.Clear()
    $ctx=@{runtime=$options.RuntimeRoot;executables=@{LocalCPA='CPA-Core/app/cli-proxy-api.exe';LocalCPAManagerPlus='CPA-Manager-Plus/app/cpa-manager-plus.exe';CPAUsageKeeper='CPA-Usage-Keeper/cpa-usage-keeper.exe'}}
    foreach($serviceName in $names){Set-CPAStackService $ctx $serviceName 'Running'}
    $script:serviceCalls.Clear()
    $before=ConvertTo-Json -InputObject @(Get-CPAStackTree $options.RuntimeRoot) -Compress
    $code=Invoke-CPALocalStackDeployment @options
    Assert-True ($code -ne 0 -and $script:serviceCalls.Count -eq 0) ($Name+' rejected before service mutation')
    Assert-True (-not (Test-Path -LiteralPath $BackupPath) -and -not (Test-Path -LiteralPath $ReceiptPath)) ($Name+' did not create output')
    Assert-True ($before -ceq (ConvertTo-Json -InputObject @(Get-CPAStackTree $options.RuntimeRoot) -Compress)) ($Name+' installed files and SQLite untouched')
    Write-Host ('CASE '+$Name+': overlap rejected exit='+$code)
    foreach($serviceName in $names){Set-CPAStackService $ctx $serviceName 'Stopped'}
}
function Invoke-ReceiptFailureCase([string]$Name='receipt-write-failure', [string]$Mode='PreflightOnly', [int]$FailAt=1, [bool]$Permanently=$false, [string]$Failure='') {
    $options=New-Case $Name; $options.Mode=$Mode; $script:failure='';$script:copyCount=0;$script:startupModes.Clear();$script:receiptFailurePath=$options.ReceiptPath;$script:receiptWriteAttempts=0;$script:receiptFailAt=$FailAt;$script:receiptFailPermanently=$Permanently
    $ctx=@{runtime=$options.RuntimeRoot;executables=@{LocalCPA='CPA-Core/app/cli-proxy-api.exe';LocalCPAManagerPlus='CPA-Manager-Plus/app/cpa-manager-plus.exe';CPAUsageKeeper='CPA-Usage-Keeper/cpa-usage-keeper.exe'}}
    foreach($serviceName in $names){Set-CPAStackService $ctx $serviceName 'Running'}
    $script:serviceCalls.Clear()
    $script:failure=$Failure
    $output=@(Invoke-CPALocalStackDeployment @options 6>&1);$code=[int]$output[-1];$text=$output -join "`n"
    Assert-True ($code -ne 0 -and $text -notmatch 'exit=0|must-not-leak|private-key') 'any receipt failure returns nonzero without leaking raw exceptions'
    if(Test-Path -LiteralPath $options.ReceiptPath){
        $receipt=Get-Content -LiteralPath $options.ReceiptPath -Raw|ConvertFrom-Json
        Assert-True ($receipt.success -eq $false) 'persisted receipt never claims success after write failure'
        if(-not $Permanently){
            $expectedStatus=if($Mode -eq 'PreflightOnly'){'receipt_failed'}else{'rolled_back'}
            Assert-True ($receipt.status -eq $expectedStatus -and $receipt.errorCode -eq 'receipt_write_failed' -and $receipt.receiptWriteFailed -eq $true) 'retry preserves the failed outcome and verified recovery status'
        }
        Assert-True ((Get-Content -LiteralPath $options.ReceiptPath -Raw) -notmatch 'must-not-leak|private-key') 'receipt sanitizes OS errors'
    } else {Assert-True $Permanently 'only permanent write failure can leave receipt unavailable'}
    $expectedAttempts=if($Permanently -and $Failure){$FailAt}else{$FailAt+1}
    Assert-True ($script:receiptWriteAttempts -eq $expectedAttempts) 'receipt retry count is bounded'
    Assert-True (@(Get-ChildItem (Split-Path -Parent $options.ReceiptPath) -Filter 'receipt.json.*.tmp').Count -eq 0) 'failed atomic write cleans temporary file'
    if($Mode -eq 'PreflightOnly'){Assert-True ($script:serviceCalls.Count -eq 0) 'receipt failure preflight did not touch services'}
    elseif($Failure -eq 'Rollback'){
        Assert-True ($script:processes.Count -eq 0 -and $script:startupModes.Count -eq 3) 'receipt failure does not undo safe stop/disable'
        Assert-True ($text -match 'RESULT: rollback_failed; exit=3') 'receipt failure must retain rollback-failed safety outcome'
    } else {
        Assert-True ($script:processes.Count -eq 3) 'old processes recovered after receipt failure'
        $manifest=Get-Content -LiteralPath $options.ManifestPath -Raw|ConvertFrom-Json
        foreach($file in $manifest.files){$before=Join-Path $options.BackupRoot ('runtime/'+$file.target);Assert-True ((Get-CPAStackHash (Join-Path $options.RuntimeRoot $file.target)) -eq (Get-CPAStackHash $before)) 'all original artifacts recovered'}
        if($FailAt -eq 1){Assert-True ($script:copyCount -eq 0) 'backup receipt failure precedes candidate copy'}
    }
    Write-Host ('CASE '+$Name+': exit='+$code+' attempts='+$script:receiptWriteAttempts)
    $script:failure=''
    $script:receiptFailurePath=''; foreach($serviceName in $names){Set-CPAStackService $ctx $serviceName 'Stopped'}
}
function Assert-ProductionRejectsFixtureProof {
    $options=New-Case 'fixture-proof-is-not-production'; $manifest=Get-Content -LiteralPath $options.ManifestPath -Raw|ConvertFrom-Json
    $ctx=@{compatibility=$options.CompatibilityPath;runtime=$options.RuntimeRoot}
    $rejected=$false
    try { & $script:realCompatibility $ctx $manifest } catch { $rejected=($_.Exception.Message -eq 'compatibility_evidence_not_production') }
    Assert-True $rejected 'production gate rejects fixture evidence instead of unlocking deployment'
    Write-Host 'CASE fixture-proof-is-not-production: rejected by production gate'
}
function Invoke-RelabelledEvidenceCase([string]$Failure='FixtureBody', [string]$ExpectedError='compatibility_evidence_not_production') {
    # Deliberately invalid/adversarial labels, NEVER production acceptance.
    # The evidence body stays fixture-marked even if its index claims production.
    $name='evidence-body-'+$Failure;$options=New-Case $name
    $proof=Get-Content -LiteralPath $options.CompatibilityPath -Raw|ConvertFrom-Json
    $body=@{schema=1;evidenceClass='fixture';pairIdentity=$proof.pairIdentity;kind='compatibility-roundtrip';semanticEvidence=$proof.semanticEvidence}
    Write-CPAStackJSON $proof.evidence[0].path $body
    $proof.semanticEvidence.evidenceClass='production';$proof.evidence[0].evidenceClass='production'
    if($Failure -ne 'FixtureBody'){
        $body=Get-Content -LiteralPath $proof.evidence[0].path -Raw|ConvertFrom-Json
        $body.evidenceClass='production';$body.semanticEvidence.evidenceClass='production'
        if($Failure -eq 'FloorRejected'){$body.semanticEvidence.floor.candidateBelowCurrentRejected=$false}
        if($Failure -eq 'ManagerUnreadable'){$body.semanticEvidence.oldManager.canReadMigratedCurrentSQLite=$false}
        if($Failure -eq 'OwnerLost'){$body.semanticEvidence.ownerManual.manualIntentPreserved=$false}
        if($Failure -eq 'StringBoolean'){$body.semanticEvidence.floor.rejectionVerifiedBeforeStop='true'}
        if($Failure -eq 'StateSchemaMismatch'){$body.semanticEvidence.stateSchema.sqliteAfter='different-fixture-schema'}
        if($Failure -eq 'MissingSchema'){$body.semanticEvidence.stateSchema.sqliteBefore=''}
        if($Failure -eq 'ManifestSchema'){$body.semanticEvidence.manifestSchema=2}
        if($Failure -eq 'PairMismatch'){$body.pairIdentity=('0'*64)}
        Write-CPAStackJSON $proof.evidence[0].path $body
    }
    $proof.evidence[0].kind='compatibility-roundtrip';$proof.evidence[0].sha256=Get-CPAStackHash $proof.evidence[0].path
    Write-CPAStackJSON $options.CompatibilityPath $proof
    $script:failure='';$script:serviceCalls.Clear();$script:useProductionCompatibility=$true
    try { $code=Invoke-CPALocalStackDeployment @options }
    finally { $script:useProductionCompatibility=$false }
    $receipt=Get-Content -LiteralPath $options.ReceiptPath -Raw|ConvertFrom-Json
    Assert-True ($code -ne 0 -and $receipt.errorCode -eq $ExpectedError -and $script:serviceCalls.Count -eq 0) ($name+' invalid body cannot unlock the production gate')
    Assert-True (-not (Test-Path -LiteralPath $options.BackupRoot)) 'invalid evidence refused before backup'
    Assert-True ($receipt.success -eq $false -and $receipt.status -eq 'preflight_failed') 'invalid evidence is never successful preflight'
    Write-Host ('CASE '+$name+': refused before stop exit='+$code)
}
try {
    foreach($mode in @('Correct','Direct','WrongHash','WrongPid','Missing','Ambiguous')){Invoke-ShadowIdentityCase $mode}
    if($Cases -eq 'ShadowIdentity'){return}
    Invoke-RelabelledEvidenceCase 'FloorRejected' 'compatibility_floor_rejection_unproven'
    Invoke-RelabelledEvidenceCase 'ManagerUnreadable' 'compatibility_manager_schema_unproven'
    Invoke-RelabelledEvidenceCase 'OwnerLost' 'compatibility_owner_intent_unproven'
    Invoke-RelabelledEvidenceCase 'StringBoolean' 'compatibility_floor_rejection_unproven'
    Invoke-RelabelledEvidenceCase 'StateSchemaMismatch' 'compatibility_evidence_schema_mismatch'
    Invoke-RelabelledEvidenceCase 'MissingSchema' 'compatibility_state_schema_missing'
    Invoke-RelabelledEvidenceCase 'ManifestSchema' 'compatibility_semantic_schema_mismatch'
    Invoke-RelabelledEvidenceCase 'PairMismatch' 'compatibility_evidence_binding_mismatch'
    Invoke-ReceiptFailureCase 'receipt-finally-failure-preserves-safe-stop' 'Deploy' 2 $true 'Rollback'
    Invoke-RelabelledEvidenceCase
    $overlapCase=Join-Path $OutputDir 'overlap-equal'; Invoke-OverlapCase 'overlap-equal' (Join-Path $overlapCase 'same') (Join-Path $overlapCase 'same')
    $nestedCase=Join-Path $OutputDir 'overlap-receipt-inside-backup'; Invoke-OverlapCase 'overlap-receipt-inside-backup' (Join-Path $nestedCase 'backup') (Join-Path $nestedCase 'backup\receipt.json')
    $reverseCase=Join-Path $OutputDir 'overlap-backup-inside-receipt'; Invoke-OverlapCase 'overlap-backup-inside-receipt' (Join-Path $reverseCase 'receipt\backup') (Join-Path $reverseCase 'receipt')
    $normalizedCase=Join-Path $OutputDir 'overlap-normalized'; Invoke-OverlapCase 'overlap-normalized' (Join-Path $normalizedCase 'backup\') (Join-Path $normalizedCase 'other\..\BACKUP\runtime\CPA-Core\app\cli-proxy-api.exe')
    Invoke-Case 'incompatible-before-stop' 'Preflight'
    Invoke-Case 'manager-pair-before-stop' 'PairPreflight'
    Invoke-Case 'semantic-floor-before-stop' 'SemanticFloor'
    Invoke-Case 'success' ''
    Invoke-Case 'mid-copy' 'Copy'
    Invoke-Case 'migration-failure' 'Migration'
    Invoke-Case 'start-failure' 'Start'
    Invoke-Case 'health-failure' 'Health'
    Invoke-Case 'new-owner-preserved' 'PolicyThenHealth'
    Invoke-Case 'rollback-failure' 'Rollback'
    Invoke-ReceiptFailureCase
    Invoke-ReceiptFailureCase 'receipt-backup-one-time-failure' 'Deploy'
    Invoke-ReceiptFailureCase 'receipt-final-one-time-failure' 'Deploy' 2
    Invoke-ReceiptFailureCase 'receipt-permanent-preflight-failure' 'PreflightOnly' 1 $true
    Invoke-ReceiptFailureCase 'receipt-permanent-final-failure' 'Deploy' 2 $true
    Assert-ProductionRejectsFixtureProof
    Write-Host 'PASS: isolated file/process/SQLite rollback assertions; NOT real CPA schema/Core/SCM acceptance.'
} finally {
    foreach($p in @($script:processes.Values)){if(-not $p.HasExited){$p.Kill()};$p.WaitForExit();$p.Dispose()}
    $script:processes.Clear()
}
