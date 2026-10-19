# 版本发布崩溃点验证(R15):真实子进程在确定性位置退出。
# A during_copy  : 复制中途退出 → 无可读版本
# B before_commit: 最终目录移动后、数据库提交前退出 → 无半成品可读,目录为可定位残留
# C after_commit : 提交后、响应前退出 → 同 request_id 确认已提交,文件不被误删
[CmdletBinding()]
param(
    [string]$RepoRoot = 'D:/kylin/rainbow_bridge',
    [switch]$Keep
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$runId = (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + (-join ((97..122) | Get-Random -Count 4 | ForEach-Object { [char]$_ }))
$runDir = Join-Path $env:TEMP "rainbow-release-crash-$runId"
$binDir = Join-Path $runDir 'bin'
$instDir = Join-Path $runDir 'instance'
$snapshotRoot = Join-Path $runDir 'release-snapshots'
$artifacts = Join-Path $runDir 'artifacts'
foreach ($dir in @($binDir, $instDir, $snapshotRoot, $artifacts)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}
$token = 'qa-release-crash-' + ([guid]::NewGuid().ToString('N') + [guid]::NewGuid().ToString('N'))
$releaseDir = $snapshotRoot -replace '\\', '/'
$basePort = 11910
$records = @()
$exitCode = 0

function Invoke-NativeCapture {
    param([scriptblock]$Command, [string]$LogPath = '')
    $previous = $ErrorActionPreference
    $previousEncoding = [Console]::OutputEncoding
    $ErrorActionPreference = 'Continue'
    $output = ''; $code = 0
    try {
        [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
        $output = & $Command *>&1 | Out-String
        $code = $LASTEXITCODE
    } finally {
        [Console]::OutputEncoding = $previousEncoding
        $ErrorActionPreference = $previous
    }
    if ($LogPath) { Set-Content -Path $LogPath -Value $output -Encoding UTF8 }
    return [ordered]@{ Output = $output; ExitCode = $code }
}

function New-InstanceConfigFile {
    param([int]$Port)
    $yaml = @"
server:
  address: "127.0.0.1:$Port"
  base_path: "/rainbow-bridge"
database:
  driver: "sqlite"
  sqlite:
    path: "data/resource.db"
redis:
  enabled: false
storage:
  type: "local"
intranet:
  enabled: true
log:
  level: "info"
  output: "stdout"
"@
    Set-Content -Path (Join-Path $instDir 'config.yaml') -Value $yaml -Encoding UTF8
}

function Start-Instance {
    param([string]$Name, [string]$CrashPoint = '')
    $exe = Join-Path $binDir 'rainbow-release.exe'
    $outLog = Join-Path $artifacts "$Name.out.log"
    $errLog = Join-Path $artifacts "$Name.err.log"
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $env:RAINBOW_FREEZE_ADMIN_TOKEN = $token
        $env:RAINBOW_RELEASE_DIR = $releaseDir
        if ($CrashPoint) { $env:RAINBOW_RELEASE_CRASH_POINT = $CrashPoint } else { Remove-Item Env:RAINBOW_RELEASE_CRASH_POINT -ErrorAction SilentlyContinue }
        Remove-Item Env:BASE_PATH -ErrorAction SilentlyContinue
        $process = Start-Process -FilePath $exe -ArgumentList '--config', (Join-Path $instDir 'config.yaml'), '--no-frontend' `
            -WorkingDirectory $instDir -WindowStyle Hidden -PassThru `
            -RedirectStandardOutput $outLog -RedirectStandardError $errLog
    } finally {
        $ErrorActionPreference = $previous
    }
    $record = [ordered]@{ name = $Name; pid = $process.Id; port = $basePort; process = $process; outLog = $outLog }
    $script:records += $record
    return $record
}

function Wait-Ready {
    param($Record, [int]$TimeoutSec = 30)
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if ($Record.process.HasExited) { throw "$($Record.name) exited early" }
        try {
            $ping = Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 -Uri "http://127.0.0.1:$($Record.port)/rainbow-bridge/ping"
            if ($ping.Content -match 'pong') { return }
        } catch { }
        Start-Sleep -Milliseconds 400
    }
    throw "$($Record.name) not ready"
}

function Stop-Instance {
    param($Record)
    if (-not $Record) { return }
    try {
        if (-not $Record.process.HasExited) {
            Stop-Process -Id $Record.pid -Force
            $Record.process.WaitForExit(10000) | Out-Null
        }
    } catch { }
}

function Invoke-Json {
    param([string]$Method, [string]$Path, [string]$Body = '', [string]$Bearer = '', [string]$ScopeEnvironment = '', [string]$ScopePipeline = '')
    $headers = @{ 'Content-Type' = 'application/json' }
    if ($Bearer) { $headers.Authorization = "Bearer $Bearer" }
    if ($ScopeEnvironment) { $headers['x-environment'] = $ScopeEnvironment }
    if ($ScopePipeline) { $headers['x-pipeline'] = $ScopePipeline }
    try {
        $params = @{ UseBasicParsing = $true; TimeoutSec = 30; Method = $Method; Uri = "http://127.0.0.1:$basePort/rainbow-bridge$Path"; Headers = $headers }
        if ($Body) { $params.Body = $Body }
        $response = Invoke-WebRequest @params
        return [ordered]@{ status = $response.StatusCode; json = ($response.Content | ConvertFrom-Json); ok = $true }
    } catch {
        $status = -1; $content = ''
        if ($_.Exception.Response) { $status = [int]$_.Exception.Response.StatusCode }
        if ($_.ErrorDetails -and $_.ErrorDetails.Message) {
            $content = $_.ErrorDetails.Message
        } elseif ($_.Exception.Response) {
            try {
                $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
                $content = $reader.ReadToEnd()
            } catch { $content = '' }
        }
        $parsed = $null
        try { $parsed = $content | ConvertFrom-Json } catch { }
        return [ordered]@{ status = $status; json = $parsed; raw = $content; ok = $false }
    }
}

function Field {
    param($Object, [string]$Name)
    if ($null -eq $Object) { return '' }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return '' }
    return $property.Value
}

function Get-VersionCount {
    $list = Invoke-Json -Method GET -Path '/api/v1/release/list?environment_key=qa_prod&pipeline_key=main'
    return @((Field (Field $list.json 'data') 'items')).Count
}

function Get-FinalDirs {
    return @(Get-ChildItem -Path $snapshotRoot -Directory -ErrorAction SilentlyContinue | Where-Object { $_.Name -ne '.staging' })
}

try {
    Write-Host '== 构建被测二进制'
    $build = Invoke-NativeCapture -LogPath (Join-Path $artifacts 'build.log') -Command {
        $env:GOPROXY = 'https://goproxy.cn,direct'
        $env:GOTOOLCHAIN = 'local'
        $goExe = Join-Path $env:USERPROFILE 'sdk\go1.27.1\go\bin\go.exe'
        if (-not (Test-Path $goExe)) { $goExe = 'go' }
        Push-Location $RepoRoot
        try { & $goExe build -tags=dev -o (Join-Path $binDir 'rainbow-release.exe') . } finally { Pop-Location }
    }
    if ($build.ExitCode -ne 0) { throw 'build failed' }
    New-InstanceConfigFile -Port $basePort

    # 种子(通过正常实例完成)
    $p = Start-Instance -Name 'seed'
    Wait-Ready $p
    Invoke-Json -Method POST -Path '/api/v1/environment/create' -Body '{"environment":{"environment_key":"qa_prod","environment_name":"QA Prod","is_active":true}}' | Out-Null
    Invoke-Json -Method POST -Path '/api/v1/pipeline/create' -Body '{"environment_key":"qa_prod","pipeline":{"pipeline_key":"main","pipeline_name":"main","is_active":true}}' | Out-Null
    Set-Content -Path (Join-Path $instDir 'asset.txt') -Value 'crash-asset-bytes'
    $upload = Invoke-NativeCapture -LogPath (Join-Path $artifacts 'upload.log') -Command {
        & curl.exe -s -X POST "http://127.0.0.1:$basePort/rainbow-bridge/api/v1/asset/upload" -F ("file=@" + (Join-Path $instDir 'asset.txt')) -F "environment_key=qa_prod" -F "pipeline_key=main"
    }
    $uploadJSON = $upload.Output | ConvertFrom-Json
    $fileID = [string](Field (Field (Field $uploadJSON 'data') 'asset') 'file_id')
    if (-not $fileID) { throw "asset upload failed: $($upload.Output.Substring(0, [Math]::Min(300, $upload.Output.Length)))" }
    Invoke-Json -Method POST -Path '/api/v1/config/create' -Body "{`"config`":{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"alias`":`"CRASH_FILE`",`"name`":`"x`",`"type`":`"file`",`"content`":`"asset://$fileID`"}}" | Out-Null
    $lock = Invoke-Json -Method POST -Path '/api/v1/freeze/lock' -Body '{"environment_key":"qa_prod","pipeline_key":"main","reason":"崩溃验证","expected_revision":"0"}' -Bearer $token
    $freezeRevision = [string](Field (Field $lock.json 'data') 'revision')
    Stop-Instance $p

    $requestId = New-Guid
    $createBody = "{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"version`":`"v1.0`",`"expected_freeze_revision`":`"$freezeRevision`",`"request_id`":`"$requestId`",`"reason`":`"崩溃点验证`"}"

    # A: during_copy
    Write-Host '== A: during_copy 崩溃'
    $pa = Start-Instance -Name 'crash-during-copy' -CrashPoint 'during_copy'
    Wait-Ready $pa
    $resultA = Invoke-Json -Method POST -Path '/api/v1/release/create' -Body $createBody -Bearer $token
    Start-Sleep -Milliseconds 800
    $aExited = $pa.process.HasExited
    $aExitCode = if ($aExited) { $pa.process.ExitCode } else { -1 }
    Stop-Instance $pa
    $pc = Start-Instance -Name 'check-a'
    Wait-Ready $pc
    $countA = Get-VersionCount
    $activeA = Invoke-Json -Method GET -Path '/api/v1/release/active?environment_key=qa_prod&pipeline_key=main'
    Stop-Instance $pc
    Write-Host "== A 结果: 进程退出=$aExited(exit=$aExitCode),版本数=$countA"

    # B: before_commit
    Write-Host '== B: before_commit 崩溃'
    $pb = Start-Instance -Name 'crash-before-commit' -CrashPoint 'before_commit'
    Wait-Ready $pb
    $resultB = Invoke-Json -Method POST -Path '/api/v1/release/create' -Body $createBody -Bearer $token
    Start-Sleep -Milliseconds 800
    $bExited = $pb.process.HasExited
    Stop-Instance $pb
    $dirsAfterB = @(Get-FinalDirs)
    $pc2 = Start-Instance -Name 'check-b'
    Wait-Ready $pc2
    $countB = Get-VersionCount
    $fixedB = Invoke-Json -Method GET -ScopeEnvironment 'qa_prod' -ScopePipeline 'main' -Path '/api/v1/runtime/release?version=v1.0'
    Stop-Instance $pc2
    Write-Host "== B 结果: 进程退出=$bExited,版本数=$countB,最终目录数=$($dirsAfterB.Count),固定读取HTTP=$($fixedB.status)"

    # C: after_commit
    Write-Host '== C: after_commit 崩溃'
    $pcc = Start-Instance -Name 'crash-after-commit' -CrashPoint 'after_commit'
    Wait-Ready $pcc
    try {
        $resultC = Invoke-Json -Method POST -Path '/api/v1/release/create' -Body $createBody -Bearer $token
    } catch { }
    Start-Sleep -Milliseconds 1200
    $cExited = $pcc.process.HasExited
    Stop-Instance $pcc
    $pc3 = Start-Instance -Name 'check-c'
    Wait-Ready $pc3
    $countC = Get-VersionCount
    $replayC = Invoke-Json -Method POST -Path '/api/v1/release/create' -Body $createBody -Bearer $token
    $replayFlag = Field (Field $replayC.json 'data') 'replayed'
    $snapshotID = [string](Field (Field (Field $replayC.json 'data') 'meta') 'snapshot_id')
    $assetFile = Get-ChildItem -Path (Join-Path (Join-Path $snapshotRoot $snapshotID) 'assets') -File -ErrorAction SilentlyContinue | Select-Object -First 1
    $assetSha = if ($assetFile) { (Get-FileHash -Algorithm SHA256 -Path $assetFile.FullName).Hash.ToLower() } else { '' }
    Stop-Instance $pc3
    Write-Host "== C 结果: 进程退出=$cExited,版本数=$countC,重放replayed=$replayFlag,快照文件存在=$([bool]$assetFile)"

    $stagingResidue = Get-ChildItem -Path (Join-Path $snapshotRoot '.staging') -Directory -ErrorAction SilentlyContinue
    $summary = [ordered]@{
        runId = $runId
        duringCopy = [ordered]@{ exitCode = $aExitCode; versionCount = $countA; activeNull = ($null -eq (Field (Field $activeA.json 'data') 'active')) }
        beforeCommit = [ordered]@{ exitCode = $bExited; versionCount = $countB; orphanFinalDirs = $dirsAfterB.Count; fixedReadStatus = $fixedB.status; fixedReadErrorType = (Field $fixedB.json 'error_type') }
        afterCommit = [ordered]@{ exited = $cExited; versionCount = $countC; replayed = $replayFlag; snapshotId = $snapshotID; assetSha = $assetSha }
        stagingResidue = @($stagingResidue | ForEach-Object { $_.Name })
    }
    $summary | ConvertTo-Json -Depth 8 | Set-Content -Path (Join-Path $artifacts 'summary.json') -Encoding UTF8

    if (-not $aExited) { Write-Host 'FAIL: during_copy 未触发进程退出'; $exitCode = 1 }
    if ($countA -ne 0) { Write-Host 'FAIL: during_copy 后不得存在可读版本'; $exitCode = 1 }
    if (-not $bExited) { Write-Host 'FAIL: before_commit 未触发进程退出'; $exitCode = 1 }
    if ($countB -ne 0) { Write-Host 'FAIL: before_commit 后不得存在可读版本'; $exitCode = 1 }
    if ($fixedB.status -ne 404) { Write-Host 'FAIL: 残留目录不得被当作可读版本(应 404)'; $exitCode = 1 }
    if (-not $cExited) { Write-Host 'FAIL: after_commit 未触发进程退出'; $exitCode = 1 }
    if ($countC -ne 1) { Write-Host 'FAIL: after_commit 后应存在 1 个已提交版本'; $exitCode = 1 }
    if ($replayFlag -ne $true) { Write-Host 'FAIL: 同 request_id 必须确认已提交结果'; $exitCode = 1 }
    if (-not $assetFile) { Write-Host 'FAIL: 已提交版本的资源文件不得被补偿删除'; $exitCode = 1 }
    if ($exitCode -eq 0) { Write-Host '== R15 崩溃点验证通过' }
} catch {
    Write-Host "FATAL: $($_.Exception.Message)" -ForegroundColor Red
    $exitCode = 1
} finally {
    foreach ($record in $records) { Stop-Instance $record }
    Write-Host "== 运行目录:$runDir"
}
exit $exitCode