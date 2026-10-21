# Lightweight launcher regressions; no service or user database is started.
param([switch]$FailureChild, [string]$TestDataDir)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$launcher = Join-Path $PSScriptRoot 'start-local.ps1'

if ($FailureChild) {
    function global:go { throw 'Injected backend build failure' }
    function global:npm.cmd { $global:LASTEXITCODE = 0 }
    & $launcher -DataDir $TestDataDir -NoBrowser -CheckOnly -Port 18090
    exit $LASTEXITCODE
}

$tokens = $null
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($launcher, [ref]$tokens, [ref]$errors)
if ($errors.Count) { throw 'Launcher parse failed.' }
foreach ($helperName in @('Write-LauncherMessage', 'Get-PortOwnerProcess', 'Test-StaleDemoServer', 'Remove-StaleDemoServer')) {
    $helperFunction = $ast.Find({ param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $helperName
    }, $true)
    if (-not $helperFunction) { throw "Launcher helper missing: $helperName" }
    . ([scriptblock]::Create($helperFunction.Extent.Text))
}

Add-Type -TypeDefinition @'
public sealed class FailingLauncherWriter : System.IO.StringWriter {
    public override void WriteLine(string value) {
        throw new System.IndexOutOfRangeException("Injected console failure");
    }
}
'@
$originalOut = [Console]::Out
$originalError = [Console]::Error
$capturedOut = New-Object IO.StringWriter
$capturedError = New-Object IO.StringWriter
$brokenWriter = New-Object FailingLauncherWriter
try {
    [Console]::SetOut($capturedOut)
    [Console]::SetError($capturedError)
    Write-LauncherMessage 'ready-message'
    Write-LauncherMessage 'error-message' -ErrorOutput
    if ($capturedOut.ToString().Trim() -ne 'ready-message') { throw 'stdout routing failed.' }
    if ($capturedError.ToString().Trim() -ne 'error-message') { throw 'stderr routing failed.' }
    [Console]::SetOut($brokenWriter)
    [Console]::SetError($brokenWriter)
    Write-LauncherMessage 'must-not-throw'
    Write-LauncherMessage 'must-not-throw' -ErrorOutput
} finally {
    [Console]::SetOut($originalOut)
    [Console]::SetError($originalError)
    $capturedOut.Dispose()
    $capturedError.Dispose()
    $brokenWriter.Dispose()
}
[Console]::Out.WriteLine('PASS: stdout/stderr routing and console failure isolation')

$testRoot = Join-Path $env:TEMP ('rainbow-launcher-output-tests-' + [guid]::NewGuid().ToString('N'))
[void](New-Item -ItemType Directory -Path $testRoot)
foreach ($blockedLog in @($false, $true)) {
    $caseDir = Join-Path $testRoot ([string]$blockedLog)
    [void](New-Item -ItemType Directory -Path $caseDir)
    if ($blockedLog) { [void](New-Item -ItemType Directory -Path (Join-Path $caseDir 'launcher-error.log')) }
    $stdout = Join-Path $caseDir 'test.stdout.log'
    $stderr = Join-Path $caseDir 'test.stderr.log'
    $arguments = '-NoLogo -NoProfile -ExecutionPolicy Bypass -File "{0}" -FailureChild -TestDataDir "{1}"' -f $PSCommandPath, $caseDir
    $child = Start-Process powershell.exe -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    try {
        $null = $child.Handle
        if (-not $child.WaitForExit(30000)) {
            Stop-Process -Id $child.Id
            throw 'Failure-path test timed out.'
        }
        if ($child.ExitCode -ne 1) { throw "Wrong failure exit code: $($child.ExitCode)" }
        if ((Get-Content -LiteralPath $stderr -Raw) -notmatch 'Injected backend build failure') { throw 'Original error missing.' }
        if ($blockedLog) {
            if ((Get-Content -LiteralPath $stderr -Raw) -notmatch 'Could not write launcher-error.log') { throw 'Log failure fallback missing.' }
        } else {
            $diagnostic = Get-Content -LiteralPath (Join-Path $caseDir 'launcher-error.log') -Raw
            if ($diagnostic -notmatch 'Injected backend build failure' -or $diagnostic -notmatch 'start-local.ps1') { throw 'Error detail log incomplete.' }
        }
    } finally { $child.Dispose() }
}
[Console]::Out.WriteLine('PASS: build failure exit code, original error details and unwritable error log')

# 端口冲突:只认本数据目录遗留的演示服务,外来进程不误判,停止后端口必须释放
$staleRoot = Join-Path $testRoot 'stale-server'
[void](New-Item -ItemType Directory -Path $staleRoot)
$staleExe = Join-Path $staleRoot 'rainbow-local.exe'
Copy-Item -LiteralPath (Join-Path $PSHOME 'powershell.exe') -Destination $staleExe
$holdScript = Join-Path $staleRoot 'hold-port.ps1'
@'
param([int]$Port)
$listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, $Port)
$listener.Start()
Start-Sleep -Seconds 120
'@ | Set-Content -LiteralPath $holdScript -Encoding UTF8
$holdPort = 18091
$holder = Start-Process -FilePath $staleExe `
    -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $holdScript, '-Port', "$holdPort", '--config', 'config.local-launcher.yaml') `
    -WindowStyle Hidden -PassThru
try {
    $deadline = (Get-Date).AddSeconds(20)
    $owner = $null
    while ((Get-Date) -lt $deadline -and -not $owner) {
        Start-Sleep -Milliseconds 300
        $owner = Get-PortOwnerProcess -Port $holdPort
    }
    if (-not $owner) { throw 'Stale holder did not bind the port.' }
    if (-not (Test-StaleDemoServer -Process $owner -ExePath $staleExe -ConfigName 'config.local-launcher.yaml')) {
        throw 'Previous demo server was not detected as replaceable.'
    }
    if (Test-StaleDemoServer -Process (Get-Process -Id $PID) -ExePath $staleExe -ConfigName 'config.local-launcher.yaml') {
        throw 'Foreign process must not be treated as the demo server.'
    }
    if (Test-StaleDemoServer -Process $owner -ExePath (Join-Path $staleRoot 'other.exe') -ConfigName 'config.local-launcher.yaml') {
        throw 'Different executable path must not be treated as the demo server.'
    }
    if (-not (Remove-StaleDemoServer -Process $owner -Port $holdPort)) {
        throw 'Stopping the previous demo server did not release the port.'
    }
    if (Get-PortOwnerProcess -Port $holdPort) { throw 'Port is still held after stopping the stale server.' }
    if (-not $holder.HasExited) { throw 'Stale holder process should have exited.' }
} finally {
    if (-not $holder.HasExited) { Stop-Process -Id $holder.Id -Force }
    $holder.Dispose()
}
[Console]::Out.WriteLine('PASS: stale demo server detection, foreign process rejection and port release')
[Console]::Out.WriteLine("Test evidence: $testRoot")
