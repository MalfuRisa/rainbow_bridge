# 版本发布双进程验证(R06/R07):两进程共享同一 SQLite 与同一快照根。
# R06: 并发创建同版本恰好一次成功;同 request_id 跨进程重试返回原版本。
# R07: 并发切换仅一次成功,另一进程立即看到提交后的生效指针。
[CmdletBinding()]
param(
    [string]$RepoRoot = 'D:/kylin/rainbow_bridge',
    [switch]$Keep
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$runId = (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + (-join ((97..122) | Get-Random -Count 4 | ForEach-Object { [char]$_ }))
$runDir = Join-Path $env:TEMP "rainbow-release-2p-$runId"
$binDir = Join-Path $runDir 'bin'
$p1Dir = Join-Path $runDir 'p1'
$p2Dir = Join-Path $runDir 'p2'
$sharedDir = Join-Path $runDir 'shared'
$snapshotRoot = Join-Path $sharedDir 'release-snapshots'
$artifacts = Join-Path $runDir 'artifacts'
foreach ($dir in @($binDir, $p1Dir, $p2Dir, $sharedDir, $snapshotRoot, $artifacts)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}
$dbPath = (Join-Path $sharedDir 'resource.db') -replace '\\', '/'
$releaseDir = $snapshotRoot -replace '\\', '/'
$token = 'qa-release-2p-' + ([guid]::NewGuid().ToString('N') + [guid]::NewGuid().ToString('N'))
$records = @()
$exitCode = 0

function Invoke-NativeCapture {
    param([scriptblock]$Command, [string]$LogPath = '')
    $previous = $ErrorActionPreference
    $previousEncoding = [Console]::OutputEncoding
    $ErrorActionPreference = 'Continue'
    $output = ''
    $code = 0
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

function New-InstanceConfig {
    param([string]$Dir, [int]$Port)
    $yaml = @"
server:
  address: "127.0.0.1:$Port"
  base_path: "/rainbow-bridge"
database:
  driver: "sqlite"
  sqlite:
    path: "$dbPath"
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
    Set-Content -Path (Join-Path $Dir 'config.yaml') -Value $yaml -Encoding UTF8
}

function Start-Instance {
    param([string]$Name, [string]$Dir, [int]$Port)
    New-InstanceConfig -Dir $Dir -Port $Port
    $exe = Join-Path $binDir 'rainbow-release.exe'
    $outLog = Join-Path $artifacts "$Name.out.log"
    $errLog = Join-Path $artifacts "$Name.err.log"
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $env:RAINBOW_FREEZE_ADMIN_TOKEN = $token
        $env:RAINBOW_RELEASE_DIR = $releaseDir
        Remove-Item Env:BASE_PATH -ErrorAction SilentlyContinue
        $process = Start-Process -FilePath $exe -ArgumentList '--config', (Join-Path $Dir 'config.yaml'), '--no-frontend' `
            -WorkingDirectory $Dir -WindowStyle Hidden -PassThru `
            -RedirectStandardOutput $outLog -RedirectStandardError $errLog
    } finally {
        $ErrorActionPreference = $previous
    }
    $record = [ordered]@{ name = $Name; pid = $process.Id; port = $Port; dir = $Dir; process = $process }
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
        Start-Sleep -Milliseconds 500
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
    param([int]$Port, [string]$Method, [string]$Path, [string]$Body = '', [string]$Bearer = '')
    $headers = @{ 'Content-Type' = 'application/json' }
    if ($Bearer) { $headers.Authorization = "Bearer $Bearer" }
    try {
        $params = @{ UseBasicParsing = $true; TimeoutSec = 30; Method = $Method; Uri = "http://127.0.0.1:$Port/rainbow-bridge$Path"; Headers = $headers }
        if ($Body) { $params.Body = $Body }
        $response = Invoke-WebRequest @params
        return [ordered]@{ status = $response.StatusCode; json = ($response.Content | ConvertFrom-Json) }
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
        return [ordered]@{ status = $status; json = $parsed; raw = $content }
    }
}

function Field {
    param($Object, [string]$Name)
    if ($null -eq $Object) { return '' }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return '' }
    return $property.Value
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
    if ($build.ExitCode -ne 0) { throw 'build failed: see artifacts/build.log' }

    Write-Host "== 启动 P1(11900)与 P2(11901),共享 SQLite:$dbPath,快照根:$releaseDir"
    $p1 = Start-Instance -Name 'p1' -Dir $p1Dir -Port 11900
    $p2 = Start-Instance -Name 'p2' -Dir $p2Dir -Port 11901
    Wait-Ready $p1
    Wait-Ready $p2

    # 种子与锁定
    Invoke-Json -Port 11900 -Method POST -Path '/api/v1/environment/create' -Body '{"environment":{"environment_key":"qa_prod","environment_name":"QA Prod","is_active":true}}' | Out-Null
    Invoke-Json -Port 11900 -Method POST -Path '/api/v1/pipeline/create' -Body '{"environment_key":"qa_prod","pipeline":{"pipeline_key":"main","pipeline_name":"main","is_active":true}}' | Out-Null
    Invoke-Json -Port 11900 -Method POST -Path '/api/v1/config/create' -Body '{"config":{"environment_key":"qa_prod","pipeline_key":"main","alias":"TWO_PROC_TEXT","name":"x","type":"text","content":"rel"}}' | Out-Null
    $lock = Invoke-Json -Port 11900 -Method POST -Path '/api/v1/freeze/lock' -Body '{"environment_key":"qa_prod","pipeline_key":"main","reason":"双进程发布验证","expected_revision":"0"}' -Bearer $token
    $freezeRevision = [string](Field (Field $lock.json 'data') 'revision')

    $createJob = {
        param($port, $token, $body)
        try {
            $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 60 -Method POST -Uri "http://127.0.0.1:$port/rainbow-bridge/api/v1/release/create" -Headers @{ 'Content-Type' = 'application/json'; Authorization = "Bearer $token" } -Body $body
            @{ status = $r.StatusCode; body = $r.Content }
        } catch {
            $s = -1; $c = ''
            if ($_.Exception.Response) { $s = [int]$_.Exception.Response.StatusCode }
            if ($_.ErrorDetails -and $_.ErrorDetails.Message) {
                $c = $_.ErrorDetails.Message
            } elseif ($_.Exception.Response) {
                try {
                    $rd = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
                    $c = $rd.ReadToEnd()
                } catch { $c = '' }
            }
            @{ status = $s; body = $c }
        }
    }
    Write-Host '== R06:并发创建同版本 v1.0(不同 request_id,必须恰好一胜一冲突)'
    $createBodyA = "{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"version`":`"v1.0`",`"expected_freeze_revision`":`"$freezeRevision`",`"request_id`":`"$(New-Guid)`",`"reason`":`"并发创建 A`"}"
    $createBodyB = "{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"version`":`"v1.0`",`"expected_freeze_revision`":`"$freezeRevision`",`"request_id`":`"$(New-Guid)`",`"reason`":`"并发创建 B`"}"
    $jobs = @(
        Start-Job -ScriptBlock $createJob -ArgumentList 11900, $token, $createBodyA
        Start-Job -ScriptBlock $createJob -ArgumentList 11901, $token, $createBodyB
    )
    $createResults = @($jobs | Wait-Job | Receive-Job)
    $jobs | Remove-Job -Force
    $createStatuses = @($createResults | ForEach-Object { $_.status })
    $createWins = @($createStatuses | Where-Object { $_ -eq 200 }).Count
    $createConflicts = @($createStatuses | Where-Object { $_ -eq 409 }).Count
    $conflictType = ''
    foreach ($result in $createResults) {
        if ($result.status -eq 409) {
            $parsed = $null
            try { $parsed = $result.body | ConvertFrom-Json } catch { }
            $conflictType = [string](Field $parsed 'error_type')
        }
    }
    Write-Host "== 并发创建结果: $($createStatuses -join ', ')(200=$createWins, 409=$createConflicts, 冲突类型=$conflictType)"

    $list1 = Invoke-Json -Port 11901 -Method GET -Path '/api/v1/release/list?environment_key=qa_prod&pipeline_key=main'
    $itemCount = @((Field (Field $list1.json 'data') 'items')).Count
    $snapshotId = [string](Field (Field ((Field (Field $list1.json 'data') 'items')[0]) 'meta') 'snapshot_id')
    $dirs = @(Get-ChildItem -Path $snapshotRoot -Directory -ErrorAction SilentlyContinue | Where-Object { $_.Name -ne '.staging' })
    Write-Host "== 版本数量=$itemCount,快照目录数=$($dirs.Count)"

    Write-Host '== R06b:胜者请求经另一进程重放 → 返回原版本;败者请求重试 → 仍 409'
    $winnerIndex = 0
    if ($createResults[1].status -eq 200) { $winnerIndex = 1 }
    $winnerPort = if ($winnerIndex -eq 0) { 11900 } else { 11901 }
    $winnerBody = if ($winnerIndex -eq 0) { $createBodyA } else { $createBodyB }
    $loserPort = if ($winnerIndex -eq 0) { 11901 } else { 11900 }
    $loserBody = if ($winnerIndex -eq 0) { $createBodyB } else { $createBodyA }
    $replay = Invoke-Json -Port $loserPort -Method POST -Path '/api/v1/release/create' -Body $winnerBody -Bearer $token
    $replayFlag = Field (Field $replay.json 'data') 'replayed'
    $replaySnapshot = [string](Field (Field (Field $replay.json 'data') 'meta') 'snapshot_id')
    $loserRetry = Invoke-Json -Port $winnerPort -Method POST -Path '/api/v1/release/create' -Body $loserBody -Bearer $token
    $loserRetryType = [string](Field $loserRetry.json 'error_type')
    Write-Host "== 重放 replayed=$replayFlag 快照一致=$($replaySnapshot -eq $snapshotId);败者重试=$($loserRetry.status)/$loserRetryType"

    Write-Host '== R06c:相同 request_id 并发(两进程)→ 都成功且只产生一份快照'
    $createBodyC = "{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"version`":`"v1.2`",`"expected_freeze_revision`":`"$freezeRevision`",`"request_id`":`"$(New-Guid)`",`"reason`":`"相同请求并发`"}"
    $jobsC = @(
        Start-Job -ScriptBlock $createJob -ArgumentList 11900, $token, $createBodyC
        Start-Job -ScriptBlock $createJob -ArgumentList 11901, $token, $createBodyC
    )
    $sameResults = @($jobsC | Wait-Job | Receive-Job)
    $jobsC | Remove-Job -Force
    $sameStatuses = @($sameResults | ForEach-Object { $_.status })
    $sameSnapshots = @()
    foreach ($result in $sameResults) {
        $parsed = $null
        try { $parsed = $result.body | ConvertFrom-Json } catch { }
        $sameSnapshots += [string](Field (Field (Field $parsed 'data') 'meta') 'snapshot_id')
    }
    $dirsAfterC = @(Get-ChildItem -Path $snapshotRoot -Directory -ErrorAction SilentlyContinue | Where-Object { $_.Name -ne '.staging' })
    $identicalSnapshot = ($sameSnapshots.Count -eq 2 -and $sameSnapshots[0] -ne '' -and $sameSnapshots[0] -eq $sameSnapshots[1])
    Write-Host "== 相同请求并发结果: $($sameStatuses -join ', '),快照一致=$identicalSnapshot,快照目录数=$($dirsAfterC.Count)"

    Write-Host '== 顺序创建 v1.1(P1)'
    $createV11 = Invoke-Json -Port 11900 -Method POST -Path '/api/v1/release/create' -Body "{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"version`":`"v1.1`",`"expected_freeze_revision`":`"$freezeRevision`",`"request_id`":`"$(New-Guid)`",`"reason`":`"并发切换准备`"}" -Bearer $token
    Write-Host "== v1.1 create status=$($createV11.status)"

    Write-Host '== R07:并发切换 v1.1 / v1.0(相同 expected head=0)'
    $activateV11 = "{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"version`":`"v1.1`",`"expected_active_revision`":`"0`",`"request_id`":`"$(New-Guid)`",`"reason`":`"并发切换`"}"
    $activateV10 = "{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"version`":`"v1.0`",`"expected_active_revision`":`"0`",`"request_id`":`"$(New-Guid)`",`"reason`":`"并发切换`"}"
    $activateJobs = @(
        Start-Job -ScriptBlock { param($port, $token, $body)
            try { $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 30 -Method POST -Uri "http://127.0.0.1:$port/rainbow-bridge/api/v1/release/activate" -Headers @{ 'Content-Type' = 'application/json'; Authorization = "Bearer $token" } -Body $body
                @{ status = $r.StatusCode; body = $r.Content } }
            catch { $s = -1; $c = ''; if ($_.Exception.Response) { $s = [int]$_.Exception.Response.StatusCode }; if ($_.ErrorDetails -and $_.ErrorDetails.Message) { $c = $_.ErrorDetails.Message } elseif ($_.Exception.Response) { try { $rd = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream()); $c = $rd.ReadToEnd() } catch { $c = '' } }; @{ status = $s; body = $c } }
        } -ArgumentList 11900, $token, $activateV11
        Start-Job -ScriptBlock { param($port, $token, $body)
            try { $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 30 -Method POST -Uri "http://127.0.0.1:$port/rainbow-bridge/api/v1/release/activate" -Headers @{ 'Content-Type' = 'application/json'; Authorization = "Bearer $token" } -Body $body
                @{ status = $r.StatusCode; body = $r.Content } }
            catch { $s = -1; $c = ''; if ($_.Exception.Response) { $s = [int]$_.Exception.Response.StatusCode }; if ($_.ErrorDetails -and $_.ErrorDetails.Message) { $c = $_.ErrorDetails.Message } elseif ($_.Exception.Response) { try { $rd = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream()); $c = $rd.ReadToEnd() } catch { $c = '' } }; @{ status = $s; body = $c } }
        } -ArgumentList 11901, $token, $activateV10
    )
    $activateResults = $activateJobs | Wait-Job | Receive-Job
    $activateJobs | Remove-Job -Force
    $activateStatuses = @($activateResults | ForEach-Object { $_.status })
    $activateWins = @($activateStatuses | Where-Object { $_ -eq 200 }).Count
    $activateConflicts = @($activateStatuses | Where-Object { $_ -eq 409 }).Count
    Write-Host "== 并发切换结果: $($activateStatuses -join ', ')(200=$activateWins, 409=$activateConflicts)"

    $activeP2 = Invoke-Json -Port 11901 -Method GET -Path '/api/v1/release/active?environment_key=qa_prod&pipeline_key=main'
    $activeVersion = [string](Field (Field (Field $activeP2.json 'data') 'active') 'version')
    $activeRevision = [string](Field (Field (Field $activeP2.json 'data') 'active') 'revision')
    $history = Invoke-Json -Port 11900 -Method GET -Path '/api/v1/release/history?environment_key=qa_prod&pipeline_key=main'
    $activateEvents = @((Field (Field $history.json 'data') 'items') | Where-Object { $_.event_type -eq 'activate' }).Count
    Write-Host "== P2 立即看到: active=$activeVersion revision=$activeRevision,切换事件数=$activateEvents"

    $summary = [ordered]@{
        runId            = $runId
        sharedDB         = $dbPath
        snapshotRoot     = $releaseDir
        concurrentCreate = [ordered]@{ statuses = $createStatuses; winners = $createWins; conflicts = $createConflicts; conflictType = $conflictType; itemCount = $itemCount; snapshotDirs = $dirs.Count }
        replay           = [ordered]@{ replayed = $replayFlag; snapshotMatches = ($replaySnapshot -eq $snapshotId); loserRetryStatus = $loserRetry.status; loserRetryType = $loserRetryType }
        identicalRequestConcurrency = [ordered]@{ statuses = $sameStatuses; identicalSnapshot = $identicalSnapshot; snapshotDirs = $dirsAfterC.Count }
        concurrentActivate = [ordered]@{ statuses = $activateStatuses; winners = $activateWins; conflicts = $activateConflicts; activeVersion = $activeVersion; activeRevision = $activeRevision; activateEvents = $activateEvents }
    }
    $summary | ConvertTo-Json -Depth 8 | Set-Content -Path (Join-Path $artifacts 'summary.json') -Encoding UTF8

    if ($createWins -ne 1 -or $createConflicts -ne 1) { Write-Host 'FAIL: 并发创建必须恰好一胜一冲突'; $exitCode = 1 }
    if ($conflictType -ne 'RELEASE_VERSION_EXISTS') { Write-Host "FAIL: 并发创建冲突类型应为 RELEASE_VERSION_EXISTS,实际 $conflictType"; $exitCode = 1 }
    if ($itemCount -ne 1 -or $dirs.Count -ne 1) { Write-Host 'FAIL: 恰好一个完整版本与一个快照目录'; $exitCode = 1 }
    if ($replayFlag -ne $true -or $replaySnapshot -ne $snapshotId) { Write-Host 'FAIL: 跨进程重放必须返回原版本'; $exitCode = 1 }
    if ($loserRetry.status -ne 409 -or $loserRetryType -ne 'RELEASE_VERSION_EXISTS') { Write-Host "FAIL: 败者请求重试仍应 409 RELEASE_VERSION_EXISTS,实际 $($loserRetry.status)/$loserRetryType"; $exitCode = 1 }
    if (@($sameStatuses | Where-Object { $_ -eq 200 }).Count -ne 2) { Write-Host 'FAIL: 相同 request_id 并发两处都应为 200'; $exitCode = 1 }
    if (-not $identicalSnapshot) { Write-Host 'FAIL: 相同 request_id 并发必须返回同一快照'; $exitCode = 1 }
    if ($dirsAfterC.Count -ne 2) { Write-Host "FAIL: 相同 request_id 并发只应新增一份快照目录,实际 $($dirsAfterC.Count)"; $exitCode = 1 }
    if ($activateWins -ne 1 -or $activateConflicts -ne 1) { Write-Host 'FAIL: 并发切换必须恰好一胜一冲突'; $exitCode = 1 }
    if ($activeRevision -ne '1' -or $activateEvents -ne 1) { Write-Host 'FAIL: head revision 必须为 1 且仅一条切换事件'; $exitCode = 1 }
    if ($activeVersion -ne 'v1.0' -and $activeVersion -ne 'v1.1') { Write-Host 'FAIL: 生效版本必须为参与的版本之一'; $exitCode = 1 }
    if ($exitCode -eq 0) { Write-Host '== R06/R07 双进程验证通过' }
} catch {
    Write-Host "FATAL: $($_.Exception.Message)" -ForegroundColor Red
    $exitCode = 1
} finally {
    foreach ($record in $records) { Stop-Instance $record }
    Write-Host "== 运行目录:$runDir"
}
exit $exitCode