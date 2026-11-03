param(
    [Parameter(Mandatory=$true)][string]$ArtifactDirectory,
    [ValidateSet('Success','UnrelatedError','SelfTest','BudgetMaximum','BudgetTooSmall','BudgetTooLarge')][string]$Case='Success'
)
$ErrorActionPreference='Stop'
if(Test-Path -LiteralPath $ArtifactDirectory){throw 'Use a fresh private artifact directory'}
$root=[IO.Path]::GetFullPath($ArtifactDirectory)
[IO.Directory]::CreateDirectory($root)|Out-Null
$wrapper=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../refresh-qwen-quota.ps1'))
$recorder=Join-Path $root 'native helper & (literal).exe'
$capture=Join-Path $root 'helper-argv.json'
$cache=Join-Path $root 'cache literal & (one);$name.json'
$state=Join-Path $root 'login-state'
$bl=Join-Path $root 'bl literal & (two);$name.exe'
$utf8=New-Object Text.UTF8Encoding($false)
$expectedOutput='{"protocol":"qwen-coding-plan-v1","product":"coding-plan","queryStatus":"success","authorityStatus":"needs_configuration","reason":"account_authority_unverified","observedAt":"2026-11-02T12:00:00Z","instanceType":"pro","windows":[{"window":"5h","unit":"requests","used":"38","total":"100","remaining":"62","resetTime":"2026-08-06T07:06:40Z"},{"window":"week","unit":"requests","used":"500","total":"1000","remaining":"500","resetTime":"2026-08-07T10:53:20Z"},{"window":"billing_month","unit":"requests","used":"950","total":"1000","remaining":"50","resetTime":"2026-08-08T14:40:00Z"}]}'
$expectedExit=0
if($Case -eq 'UnrelatedError'){
    $expectedOutput='{"error":"invalid_response","message":"synthetic unrelated failure"}'
    $expectedExit=23
}
$queryBudget='15s'
if($Case -eq 'BudgetMaximum'){$queryBudget='60s'}
$expectedArgs=@('--json','--source','coding-plan','--protocol','qwen-coding-plan-v1','--bl-path',$bl,'--timeout',$queryBudget,'--cache-file',$cache)
$receipt=[ordered]@{case=$Case;passed=$false;scope='public PowerShell script to synthetic native recorder only; no official CLI or account';expectedArgs=$expectedArgs;expectedExit=$expectedExit}
try{
    Add-Type -Language CSharp -OutputType ConsoleApplication -OutputAssembly $recorder -ReferencedAssemblies 'System.Web.Extensions' -TypeDefinition @'
using System;
using System.IO;
using System.Text;
using System.Web.Script.Serialization;
public static class QuotaArgumentRecorder {
    public static int Main(string[] args) {
        File.WriteAllText(Environment.GetEnvironmentVariable("QWEN_REFRESH_FIXTURE_CAPTURE"),
            new JavaScriptSerializer().Serialize(args), new UTF8Encoding(false));
        Console.Out.Write(Environment.GetEnvironmentVariable("QWEN_REFRESH_FIXTURE_STDOUT"));
        Console.Error.Write("synthetic helper stderr");
        return Int32.Parse(Environment.GetEnvironmentVariable("QWEN_REFRESH_FIXTURE_EXIT"));
    }
}
'@
    # All possible executable targets are synthetic. PATH cannot resolve a real
    # login-notice PowerShell if an unrelated-error regression enters recovery.
    Copy-Item -LiteralPath $recorder -Destination $bl
    $bin=Join-Path $root 'private-bin'
    [IO.Directory]::CreateDirectory($bin)|Out-Null
    Copy-Item -LiteralPath $recorder -Destination (Join-Path $bin 'powershell.exe')
    [IO.File]::WriteAllText($cache,'last-good-synthetic-cache',$utf8)
    [IO.File]::WriteAllText($state,'previous-notification',$utf8)
    $beforeCache=[Convert]::ToBase64String([IO.File]::ReadAllBytes($cache))
    $info=New-Object Diagnostics.ProcessStartInfo
    $info.FileName=Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe'
    $arguments=@('-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File',$wrapper,'-QuotaExecutable',$recorder,'-BlPath',$bl,'-CacheFile',$cache,'-StateFile',$state)
    switch($Case){
        'SelfTest' {$arguments+='-SelfTest'}
        'BudgetMaximum' {$arguments+=@('-QueryTimeoutSeconds','60')}
        'BudgetTooSmall' {$arguments+=@('-QueryTimeoutSeconds','0');$receipt.expectedExit=1}
        'BudgetTooLarge' {$arguments+=@('-QueryTimeoutSeconds','61');$receipt.expectedExit=1}
    }
    $info.Arguments=($arguments|ForEach-Object {'"'+$_+'"'}) -join ' '
    $info.UseShellExecute=$false
    $info.CreateNoWindow=$true
    $info.RedirectStandardOutput=$true
    $info.RedirectStandardError=$true
    $info.EnvironmentVariables['QWEN_REFRESH_FIXTURE_CAPTURE']=$capture
    $info.EnvironmentVariables['QWEN_REFRESH_FIXTURE_STDOUT']=$expectedOutput
    $info.EnvironmentVariables['QWEN_REFRESH_FIXTURE_EXIT']=[string]$expectedExit
    $info.EnvironmentVariables['PATH']=$bin
    foreach($name in @('HOME','USERPROFILE','LOCALAPPDATA','APPDATA','TMP','TEMP')){
        $private=Join-Path $root $name
        [IO.Directory]::CreateDirectory($private)|Out-Null
        $info.EnvironmentVariables[$name]=$private
    }
    $process=New-Object Diagnostics.Process
    $process.StartInfo=$info
    if(-not $process.Start()){throw 'public wrapper did not start'}
    $stdout=$process.StandardOutput.ReadToEndAsync()
    $stderr=$process.StandardError.ReadToEndAsync()
    $process.WaitForExit()
    $wrapperOutput=$stdout.GetAwaiter().GetResult()
    $wrapperError=$stderr.GetAwaiter().GetResult()
    [IO.File]::WriteAllText((Join-Path $root 'wrapper.stdout.log'),$wrapperOutput,$utf8)
    [IO.File]::WriteAllText((Join-Path $root 'wrapper.stderr.log'),$wrapperError,$utf8)
    $receipt.wrapperExit=$process.ExitCode
    if($process.ExitCode -ne $receipt.expectedExit){throw 'exact public wrapper exit code was not preserved'}
    if($Case -eq 'SelfTest'){
        if($wrapperOutput -notmatch 'login notification self-test passed'){throw 'existing auth classification/one-time notification SelfTest did not pass'}
        if(Test-Path -LiteralPath $capture){throw 'SelfTest executed a quota query'}
        if([IO.File]::ReadAllText($state) -cne 'previous-notification'){throw 'SelfTest changed supplied notification state'}
    }elseif($Case -eq 'BudgetTooSmall' -or $Case -eq 'BudgetTooLarge'){
        if(Test-Path -LiteralPath $capture){throw 'out-of-range query budget reached the helper'}
        if($wrapperError -notmatch 'QueryTimeoutSeconds'){throw 'invalid budget failed for an unrelated reason'}
        if([IO.File]::ReadAllText($state) -cne 'previous-notification'){throw 'invalid budget changed supplied notification state'}
    }else{
        $decodedArgs=ConvertFrom-Json -InputObject ([IO.File]::ReadAllText($capture))
        $actualArgs=@(foreach($argument in $decodedArgs){[string]$argument})
        $receipt.actualArgs=$actualArgs
        if(($actualArgs|ConvertTo-Json -Compress) -cne ($expectedArgs|ConvertTo-Json -Compress)){throw 'native helper did not receive the canonical exact arguments'}
        if($expectedExit -eq 0){
            if(Test-Path -LiteralPath $state){throw 'success did not clear notification suppression'}
        }elseif([IO.File]::ReadAllText($state) -cne 'previous-notification'){throw 'unrelated error changed notification suppression'}
        if([IO.File]::ReadAllText((Join-Path $root 'refresh.out.log')) -cne $expectedOutput){throw 'helper stdout log changed'}
        if([IO.File]::ReadAllText((Join-Path $root 'refresh.err.log')) -cne 'synthetic helper stderr'){throw 'helper stderr log changed'}
    }
    if([Convert]::ToBase64String([IO.File]::ReadAllBytes($cache)) -cne $beforeCache){throw 'wrapper, not helper, overwrote the cache'}
    if(Test-Path -LiteralPath (Join-Path $root 'login-notice.ps1')){throw 'test unexpectedly entered login recovery'}
    $receipt.passed=$true
}catch{
    $receipt.error=$_.Exception.Message
    $receipt.scriptStackTrace=$_.ScriptStackTrace
}finally{
    [IO.File]::WriteAllText((Join-Path $root 'RESULT.json'),($receipt|ConvertTo-Json -Depth 8),$utf8)
}
if(-not $receipt.passed){throw $receipt.error}
Write-Output ('PASS '+$Case+': public wrapper exit, helper arguments/cache/logs or existing SelfTest')
