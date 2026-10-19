# 冻结双进程写协调验证(F20,含 F19 重启持久化抽查)
# 两个独立服务进程共用同一个 SQLite 文件:并发锁版必须只有一个成功(数据库级协调),
# 另一个进程必须立即看到 locked 并在写入时被 409 拒绝。
[CmdletBinding()]
param(
    [string]$RepoRoot = 'D:/kylin/rainbow_bridge',
    [switch]$Keep
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$runId = (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + (-join ((97..122) | Get-Random -Count 4 | ForEach-Object { [char]$_ }))
$runDir = Join-Path $env:TEMP "rainbow-freeze-2p-$runId"
$binDir = Join-Path $runDir 'bin'
$p1Dir = Join-Path $runDir 'p1'
$p2Dir = Join-Path $runDir 'p2'
$sharedDir = Join-Path $runDir 'shared'
$artifacts = Join-Path $runDir 'artifacts'
foreach ($dir in @($binDir, $p1Dir, $p2Dir, $sharedDir, $artifacts)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}
$dbPath = (Join-Path $sharedDir 'resource.db') -replace '\\', '/'
$token = 'qa-freeze-2p-' + ([guid]::NewGuid().ToString('N') + [guid]::NewGuid().ToString('N'))
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
    $exe = Join-Path $binDir 'rainbow-freeze.exe'
    $outLog = Join-Path $artifacts "$Name.out.log"
    $errLog = Join-Path $artifacts "$Name.err.log"
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $env:RAINBOW_FREEZE_ADMIN_TOKEN = $token
        Remove-Item Env:BASE_PATH -ErrorAction SilentlyContinue
        $process = Start-Process -FilePath $exe -ArgumentList '--config', (Join-Path $Dir 'config.yaml'), '--no-frontend' `
            -WorkingDirectory $Dir -WindowStyle Hidden -PassThru `
            -RedirectStandardOutput $outLog -RedirectStandardError $errLog
    } finally {
        $ErrorActionPreference = $previous
    }
    $record = [ordered]@{ name = $Name; pid = $process.Id; port = $Port; dir = $Dir; process = $process; outLog = $outLog }
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

function Post-Json {
    param([int]$Port, [string]$Path, [hashtable]$Body, [string]$Bearer = '')
    $headers = @{ 'Content-Type' = 'application/json' }
    if ($Bearer) { $headers.Authorization = "Bearer $Bearer" }
    try {
        $response = Invoke-WebRequest -UseBasicParsing -TimeoutSec 20 -Method Post `
            -Uri "http://127.0.0.1:$Port/rainbow-bridge$Path" -Headers $headers -Body ($Body | ConvertTo-Json -Compress)
        return [ordered]@{ status = $response.StatusCode; body = $response.Content }
    } catch {
        $status = -1
        $content = ''
        if ($_.Exception.Response) {
            $status = [int]$_.Exception.Response.StatusCode
            if ($_.ErrorDetails -and $_.ErrorDetails.Message) {
                $content = $_.ErrorDetails.Message
            } elseif ($_.Exception.Response.PSObject.Methods['GetResponseStream']) {
                # Windows PowerShell 5.1 exposes HttpWebResponse.
                $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
                try { $content = $reader.ReadToEnd() } finally { $reader.Dispose() }
            } else {
                # PowerShell 7 exposes HttpResponseMessage.
                $content = $_.Exception.Response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
            }
        }
        return [ordered]@{ status = $status; body = $content }
    }
}

function Field {
    param($Object, [string]$Name)
    if ($null -eq $Object) { return '' }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return '' }
    return $property.Value
}

$repoUnix = $RepoRoot
try {
    Write-Host "== 构建被测二进制"
    $build = Invoke-NativeCapture -LogPath (Join-Path $artifacts 'build.log') -Command {
        $env:GOPROXY = 'https://goproxy.cn,direct'
        $env:GOTOOLCHAIN = 'local'
        $goExe = Join-Path $env:USERPROFILE 'sdk\go1.27.1\go\bin\go.exe'
        if (-not (Test-Path $goExe)) { $goExe = 'go' }
        Push-Location $RepoRoot
        try { & $goExe build -tags=dev -o (Join-Path $binDir 'rainbow-freeze.exe') . } finally { Pop-Location }
    }
    if ($build.ExitCode -ne 0) { throw "build failed: see artifacts/build.log" }

    Write-Host "== 启动 P1(11896)与 P2(11897),共用数据库 $dbPath"
    $p1 = Start-Instance -Name 'p1' -Dir $p1Dir -Port 11896
    $p2 = Start-Instance -Name 'p2' -Dir $p2Dir -Port 11897
    Wait-Ready $p1
    Wait-Ready $p2

    # 种子(经 P1)
    $env1 = Post-Json -Port 11896 -Path '/api/v1/environment/create' -Body @{ environment = @{ environment_key = 'qa_prod'; environment_name = 'QA Prod'; is_active = $true } }
    $pipe1 = Post-Json -Port 11896 -Path '/api/v1/pipeline/create' -Body @{ environment_key = 'qa_prod'; pipeline = @{ pipeline_key = 'main'; pipeline_name = 'main'; is_active = $true } }
    $seed = [ordered]@{ envCode = ($env1.body | ConvertFrom-Json).code; pipeCode = ($pipe1.body | ConvertFrom-Json).code }

    Write-Host "== F20:两个进程并发锁定同一范围(revision=0)"
    $jobs = @(
        Start-Job -ScriptBlock {
            param($port, $token)
            try {
                $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 20 -Method Post `
                    -Uri "http://127.0.0.1:$port/rainbow-bridge/api/v1/freeze/lock" `
                    -Headers @{ 'Content-Type' = 'application/json'; Authorization = "Bearer $token" } `
                    -Body '{"environment_key":"qa_prod","pipeline_key":"main","reason":"双进程并发锁版","expected_revision":"0"}'
                @{ status = $r.StatusCode; body = $r.Content }
            } catch {
                $status = -1; $content = ''
                if ($_.Exception.Response) { $status = [int]$_.Exception.Response.StatusCode }
                @{ status = $status; body = $content }
            }
        } -ArgumentList 11896, $token
        Start-Job -ScriptBlock {
            param($port, $token)
            try {
                $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 20 -Method Post `
                    -Uri "http://127.0.0.1:$port/rainbow-bridge/api/v1/freeze/lock" `
                    -Headers @{ 'Content-Type' = 'application/json'; Authorization = "Bearer $token" } `
                    -Body '{"environment_key":"qa_prod","pipeline_key":"main","reason":"双进程并发锁版","expected_revision":"0"}'
                @{ status = $r.StatusCode; body = $r.Content }
            } catch {
                $status = -1; $content = ''
                if ($_.Exception.Response) { $status = [int]$_.Exception.Response.StatusCode }
                @{ status = $status; body = $content }
            }
        } -ArgumentList 11897, $token
    )
    $results = $jobs | Wait-Job | Receive-Job
    $jobs | Remove-Job -Force
    $statuses = @($results | ForEach-Object { $_.status })
    $winners = @($statuses | Where-Object { $_ -eq 200 }).Count
    $conflicts = @($statuses | Where-Object { $_ -eq 409 }).Count
    Write-Host "== 并发锁结果: $($statuses -join ', ')(200=$winners, 409=$conflicts)"

    Write-Host "== P2 立即写入:必须 409(证明不是进程内 mutex)"
    $blockedWrite = Post-Json -Port 11897 -Path '/api/v1/config/create' -Body @{ config = @{ environment_key = 'qa_prod'; pipeline_key = 'main'; alias = 'TWO_PROC_BLOCKED'; name = 'x'; type = 'text'; content = 'x' } }

    Write-Host "== P2 读取历史:必须恰好 1 条 lock 事件"
    $historyRaw = (Invoke-WebRequest -UseBasicParsing -TimeoutSec 10 -Uri "http://127.0.0.1:11897/rainbow-bridge/api/v1/freeze/history?environment_key=qa_prod&pipeline_key=main").Content | ConvertFrom-Json
    $eventCount = @($historyRaw.data.items).Count

    Write-Host "== 重启持久化:停止 P1,以同库重启 P3"
    Stop-Instance $p1
    Start-Sleep -Seconds 1
    $p3 = Start-Instance -Name 'p3' -Dir $p1Dir -Port 11896
    Wait-Ready $p3
    $statusAfterRestart = (Invoke-WebRequest -UseBasicParsing -TimeoutSec 10 -Uri "http://127.0.0.1:11896/rainbow-bridge/api/v1/freeze/status?environment_key=qa_prod&pipeline_key=main").Content | ConvertFrom-Json
    $unlockNoToken = Post-Json -Port 11896 -Path '/api/v1/freeze/unlock' -Body @{ environment_key = 'qa_prod'; pipeline_key = 'main'; reason = '无凭据'; expected_revision = '1' }
    $unlockWithToken = Post-Json -Port 11896 -Path '/api/v1/freeze/unlock' -Body @{ environment_key = 'qa_prod'; pipeline_key = 'main'; reason = '双进程验证完成'; expected_revision = '1' } -Bearer $token
    $writeAfterUnlock = Post-Json -Port 11897 -Path '/api/v1/config/create' -Body @{ config = @{ environment_key = 'qa_prod'; pipeline_key = 'main'; alias = 'TWO_PROC_OK'; name = 'x'; type = 'text'; content = 'x' } }

    $summary = [ordered]@{
        runId          = $runId
        dbPath         = $dbPath
        seed           = $seed
        concurrentLock = [ordered]@{ statuses = $statuses; winners = $winners; conflicts = $conflicts }
        blockedWrite   = [ordered]@{ status = $blockedWrite.status; errorType = (Field ($blockedWrite.body | ConvertFrom-Json) 'error_type') }
        historyEvents  = $eventCount
        afterRestart   = [ordered]@{ state = $statusAfterRestart.data.state; revision = $statusAfterRestart.data.revision }
        unlockNoToken  = [ordered]@{ status = $unlockNoToken.status; errorType = (Field ($unlockNoToken.body | ConvertFrom-Json) 'error_type') }
        unlockByToken  = [ordered]@{ status = $unlockWithToken.status }
        writeAfterUnlock = [ordered]@{ status = $writeAfterUnlock.status; code = (Field ($writeAfterUnlock.body | ConvertFrom-Json) 'code') }
    }
    $summary | ConvertTo-Json -Depth 8 | Set-Content -Path (Join-Path $artifacts 'summary.json') -Encoding UTF8

    if ($winners -ne 1 -or $conflicts -ne 1) { Write-Host 'FAIL: 并发锁必须恰好一胜一冲突'; $exitCode = 1 }
    if ($blockedWrite.status -ne 409) { Write-Host "FAIL: 跨进程写入必须 409,得到 $($blockedWrite.status)"; $exitCode = 1 }
    if ($eventCount -ne 1) { Write-Host "FAIL: 事件数必须为 1,得到 $eventCount"; $exitCode = 1 }
    if ($statusAfterRestart.data.state -ne 'locked') { Write-Host 'FAIL: 重启后必须仍为 locked'; $exitCode = 1 }
    if ($unlockNoToken.status -ne 401) { Write-Host "FAIL: 无凭据解锁必须 401,得到 $($unlockNoToken.status)"; $exitCode = 1 }
    if ($unlockWithToken.status -ne 200) { Write-Host "FAIL: 持凭据解锁必须 200"; $exitCode = 1 }
    if ($writeAfterUnlock.status -ne 200) { Write-Host 'FAIL: 解锁后写入必须成功'; $exitCode = 1 }
    if ($exitCode -eq 0) { Write-Host '== F20/F19 双进程验证通过' }
} catch {
    Write-Host "FATAL: $($_.Exception.Message)" -ForegroundColor Red
    $exitCode = 1
} finally {
    foreach ($record in $records) { Stop-Instance $record }
    Write-Host "== 运行目录:$runDir"
    if (-not $Keep) { }
}
exit $exitCode
