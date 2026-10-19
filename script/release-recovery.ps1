# 版本发布恢复验证(R24):停止实例 → 复制数据库(含 WAL/SHM)与快照目录 → 新实例验证;
# 资源文件缺失时必须 503 且不回退编辑区,恢复文件后无需重启即可再次通过。
[CmdletBinding()]
param(
    [string]$RepoRoot = 'D:/kylin/rainbow_bridge',
    [string]$OutputRoot = '',
    [switch]$SkipBuild
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$runId = (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + (-join ((97..122) | Get-Random -Count 4 | ForEach-Object { [char]$_ }))
$runDir = Join-Path $env:TEMP "rainbow-release-recovery-$runId"
$binDir = Join-Path $runDir 'bin'
$instDir = Join-Path $runDir 'instance'
$restoreDir = Join-Path $runDir 'instance-restored'
$artifacts = Join-Path $runDir 'artifacts'
foreach ($dir in @($binDir, $instDir, $restoreDir, $artifacts)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
}
$token = 'qa-release-recovery-' + ([guid]::NewGuid().ToString('N') + [guid]::NewGuid().ToString('N'))
$portA = 11920
$portB = 11921
$records = @()
$exitCode = 0
$basePath = '/rainbow-bridge'

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

function New-ConfigFile {
    param([string]$Dir, [int]$Port)
    $yaml = @"
server:
  address: "127.0.0.1:$Port"
  base_path: "$basePath"
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
    Set-Content -Path (Join-Path $Dir 'config.yaml') -Value $yaml -Encoding UTF8
}

function Start-Instance {
    param([string]$Name, [string]$Dir, [int]$Port)
    $exe = Join-Path $binDir 'rainbow-release.exe'
    $record = [ordered]@{
        name = $Name; port = $Port; dir = $Dir
        outLog = Join-Path $artifacts "$Name.out.log"
        errLog = Join-Path $artifacts "$Name.err.log"
        process = $null
    }
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $env:RAINBOW_FREEZE_ADMIN_TOKEN = $token
        Remove-Item Env:RAINBOW_RELEASE_CRASH_POINT -ErrorAction SilentlyContinue
        Remove-Item Env:RAINBOW_RELEASE_DIR -ErrorAction SilentlyContinue
        Remove-Item Env:BASE_PATH -ErrorAction SilentlyContinue
        $record.process = Start-Process -FilePath $exe -ArgumentList '--config', (Join-Path $Dir 'config.yaml'), '--no-frontend' `
            -WorkingDirectory $Dir -WindowStyle Hidden -PassThru `
            -RedirectStandardOutput $record.outLog -RedirectStandardError $record.errLog
    } finally {
        $ErrorActionPreference = $previous
    }
    $script:records += $record
    return $record
}

function Wait-Ready {
    param($Record, [int]$TimeoutSec = 30)
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if ($Record.process.HasExited) { throw "$($Record.name) exited early" }
        try {
            $ping = Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 -Uri "http://127.0.0.1:$($Record.port)$basePath/ping"
            if ($ping.Content -match 'pong') { return }
        } catch { }
        Start-Sleep -Milliseconds 400
    }
    throw "$($Record.name) not ready"
}

function Stop-Instance {
    param($Record)
    if (-not $Record -or -not $Record.process) { return }
    try {
        if (-not $Record.process.HasExited) {
            Stop-Process -Id $Record.process.Id -Force
            $Record.process.WaitForExit(10000) | Out-Null
        }
    } catch { }
}

function Invoke-Json {
    param([int]$Port, [string]$Method, [string]$Path, [string]$Body = '', [string]$Bearer = '', [string]$ScopeEnvironment = '', [string]$ScopePipeline = '')
    $headers = @{ 'Content-Type' = 'application/json' }
    if ($Bearer) { $headers.Authorization = "Bearer $Bearer" }
    if ($ScopeEnvironment) { $headers['x-environment'] = $ScopeEnvironment }
    if ($ScopePipeline) { $headers['x-pipeline'] = $ScopePipeline }
    try {
        $params = @{ UseBasicParsing = $true; TimeoutSec = 30; Method = $Method; Uri = "http://127.0.0.1:$Port$basePath$Path"; Headers = $headers }
        if ($Body) { $params.Body = $Body }
        $response = Invoke-WebRequest @params
        return [ordered]@{ status = $response.StatusCode; json = ($response.Content | ConvertFrom-Json); text = $response.Content; ok = $true }
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
        return [ordered]@{ status = $status; json = $parsed; text = $content; ok = $false }
    }
}

function Invoke-Bytes {
    param([int]$Port, [string]$Path)
    try {
        $response = Invoke-WebRequest -UseBasicParsing -TimeoutSec 30 -Method GET -Uri "http://127.0.0.1:$Port$basePath$Path"
        $bytes = $response.Content
        if ($bytes -is [string]) { $bytes = [System.Text.Encoding]::UTF8.GetBytes($bytes) }
        return [ordered]@{ status = $response.StatusCode; bytes = $bytes; sha = (Get-Sha256Bytes $bytes); ok = $true }
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
        return [ordered]@{ status = $status; bytes = $null; sha = ''; json = $parsed; ok = $false }
    }
}

function Get-Sha256Bytes {
    param($Bytes)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    $hash = $sha.ComputeHash([byte[]]$Bytes)
    return ($hash | ForEach-Object { $_.ToString('x2') }) -join ''
}

function Field {
    param($Object, [string]$Name)
    if ($null -eq $Object) { return '' }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { return '' }
    return $property.Value
}

function Get-ItemList {
    param($Value)
    if ($null -eq $Value) { return ,@() }
    return ,@($Value)
}

function Capture-State {
    param([int]$Port, [string]$Label, [string]$Dir)
    $list = Invoke-Json -Port $Port -Method GET -Path '/api/v1/release/list?environment_key=qa_prod&pipeline_key=main'
    $active = Invoke-Json -Port $Port -Method GET -Path '/api/v1/release/active?environment_key=qa_prod&pipeline_key=main'
    $detail = Invoke-Json -Port $Port -Method GET -Path '/api/v1/release/detail?environment_key=qa_prod&pipeline_key=main&version=v1.0'
    $runtime = Invoke-Json -Port $Port -Method GET -ScopeEnvironment 'qa_prod' -ScopePipeline 'main' -Path '/api/v1/runtime/release?version=v1.0'
    $history = Invoke-Json -Port $Port -Method GET -Path '/api/v1/release/history?environment_key=qa_prod&pipeline_key=main'
    $items = Get-ItemList (Field (Field $list.json 'data') 'items')
    $first = if ($items.Count -gt 0) { $items[0] } else { $null }
    $snapshotID = [string](Field (Field $first 'meta') 'snapshot_id')
    $assets = Get-ItemList (Field (Field $detail.json 'data') 'assets')
    $firstAsset = if ($assets.Count -gt 0) { $assets[0] } else { $null }
    $assetID = [string](Field $firstAsset 'asset_id')
    $download = if ($assetID) { Invoke-Bytes -Port $Port -Path "/api/v1/release/asset/$snapshotID/$assetID" } else { $null }
    $result = [ordered]@{
        label = $Label
        dir = $Dir
        versionCount = $items.Count
        metaSha = [string](Field (Field $first 'meta') 'payload_sha256')
        metaVersion = [string](Field (Field $first 'meta') 'version')
        metaFreezeRevision = [string](Field (Field $first 'meta') 'source_freeze_revision')
        assetCount = $assets.Count
        assetSha = [string](Field $firstAsset 'sha256')
        assetID = $assetID
        snapshotID = $snapshotID
        activeVersion = [string](Field (Field (Field $active.json 'data') 'active') 'version')
        activeRevision = [string](Field (Field (Field $active.json 'data') 'active') 'revision')
        historyCount = (Get-ItemList (Field (Field $history.json 'data') 'items')).Count
        runtimeStatus = $runtime.status
        runtimeText = [string]$runtime.text
        runtimeSha = [string](Field (Field $runtime.json 'data') 'payload_sha256')
        downloadStatus = if ($download) { $download.status } else { -1 }
        downloadSha = if ($download) { $download.sha } else { '' }
        configCount = (Get-ItemList (Field (Field $runtime.json 'data') 'configs')).Count
    }
    return $result
}

try {
    if (-not $SkipBuild) {
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
    } else {
        Copy-Item (Join-Path $RepoRoot 'output/bin/rainbow-release.exe') (Join-Path $binDir 'rainbow-release.exe') -Force
    }

    New-ConfigFile -Dir $instDir -Port $portA
    New-ConfigFile -Dir $restoreDir -Port $portB

    Write-Host '== 播种:环境/渠道/资源/配置/锁版/创建 v1.0/生效'
    $a = Start-Instance -Name 'origin' -Dir $instDir -Port $portA
    Wait-Ready $a
    Invoke-Json -Port $portA -Method POST -Path '/api/v1/environment/create' -Body '{"environment":{"environment_key":"qa_prod","environment_name":"QA Prod","is_active":true}}' | Out-Null
    Invoke-Json -Port $portA -Method POST -Path '/api/v1/pipeline/create' -Body '{"environment_key":"qa_prod","pipeline":{"pipeline_key":"main","pipeline_name":"main","is_active":true}}' | Out-Null
    Set-Content -Path (Join-Path $instDir 'asset.txt') -Value 'recovery-asset-bytes-0123456789'
    $uploadLog = Join-Path $artifacts 'upload.log'
    $upload = Invoke-NativeCapture -LogPath $uploadLog -Command {
        & curl.exe -s -X POST "http://127.0.0.1:$portA$basePath/api/v1/asset/upload" -F ("file=@" + (Join-Path $instDir 'asset.txt')) -F "environment_key=qa_prod" -F "pipeline_key=main"
    }
    $uploadJSON = $upload.Output | ConvertFrom-Json
    $fileID = [string](Field (Field (Field $uploadJSON 'data') 'asset') 'file_id')
    if (-not $fileID) { throw "asset upload failed: $($upload.Output.Substring(0, [Math]::Min(300, $upload.Output.Length)))" }
    Invoke-Json -Port $portA -Method POST -Path '/api/v1/config/create' -Body "{`"config`":{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"alias`":`"REC_FILE`",`"name`":`"恢复样本文件`",`"type`":`"file`",`"content`":`"asset://$fileID`"}}" | Out-Null
    Invoke-Json -Port $portA -Method POST -Path '/api/v1/config/create' -Body "{`"config`":{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"alias`":`"REC_TEXT`",`"name`":`"恢复样本文本`",`"type`":`"object`",`"content`":`"{\`"big\`":9007199254740993}`"}}" | Out-Null
    $lock = Invoke-Json -Port $portA -Method POST -Path '/api/v1/freeze/lock' -Body '{"environment_key":"qa_prod","pipeline_key":"main","reason":"恢复验证","expected_revision":"0"}' -Bearer $token
    $freezeRevision = [string](Field (Field $lock.json 'data') 'revision')
    $createBody = "{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"version`":`"v1.0`",`"expected_freeze_revision`":`"$freezeRevision`",`"request_id`":`"$(New-Guid)`",`"reason`":`"恢复验证快照`"}"
    $create = Invoke-Json -Port $portA -Method POST -Path '/api/v1/release/create' -Body $createBody -Bearer $token
    if ($create.status -ne 200) { throw "create failed: $($create.text.Substring(0, [Math]::Min(400, $create.text.Length)))" }
    $activate = Invoke-Json -Port $portA -Method POST -Path '/api/v1/release/activate' -Body "{`"environment_key`":`"qa_prod`",`"pipeline_key`":`"main`",`"version`":`"v1.0`",`"expected_active_revision`":`"0`",`"request_id`":`"$(New-Guid)`",`"reason`":`"恢复验证生效`"}" -Bearer $token
    if ($activate.status -ne 200) { throw "activate failed: $($activate.text.Substring(0, [Math]::Min(400, $activate.text.Length)))" }

    $before = Capture-State -Port $portA -Label 'before-backup' -Dir $instDir
    Write-Host "== 备份前状态: 版本=$($before.versionCount) 资源=$($before.assetCount) active=$($before.activeVersion)/rev$($before.activeRevision) 下载=$($before.downloadStatus)"

    Write-Host '== 停止实例并复制数据库(含 WAL/SHM)与快照目录'
    Stop-Instance $a
    Start-Sleep -Milliseconds 500
    New-Item -ItemType Directory -Force -Path (Join-Path $restoreDir 'data') | Out-Null
    foreach ($suffix in @('', '-wal', '-shm')) {
        $source = Join-Path $instDir "data/resource.db$suffix"
        if (Test-Path $source) { Copy-Item $source (Join-Path $restoreDir "data/resource.db$suffix") -Force }
    }
    $snapshotSource = Join-Path $instDir 'data/release-snapshots'
    $snapshotTarget = Join-Path $restoreDir 'data/release-snapshots'
    Copy-Item $snapshotSource $snapshotTarget -Recurse -Force

    Write-Host '== 启动恢复实例并逐项比对'
    $b = Start-Instance -Name 'restored' -Dir $restoreDir -Port $portB
    Wait-Ready $b
    $after = Capture-State -Port $portB -Label 'after-restore' -Dir $restoreDir
    Write-Host "== 恢复后状态: 版本=$($after.versionCount) 资源=$($after.assetCount) active=$($after.activeVersion)/rev$($after.activeRevision) 下载=$($after.downloadStatus)"

    $compare = [ordered]@{
        versionCount = ($before.versionCount -eq $after.versionCount)
        metaSha = ($before.metaSha -eq $after.metaSha -and $before.metaSha -ne '')
        metaVersion = ($before.metaVersion -eq $after.metaVersion)
        metaFreezeRevision = ($before.metaFreezeRevision -eq $after.metaFreezeRevision)
        assetCount = ($before.assetCount -eq $after.assetCount)
        assetSha = ($before.assetSha -eq $after.assetSha -and $before.assetSha -ne '')
        activeVersion = ($before.activeVersion -eq $after.activeVersion -and $after.activeVersion -eq 'v1.0')
        activeRevision = ($before.activeRevision -eq $after.activeRevision -and $after.activeRevision -eq '1')
        historyCount = ($before.historyCount -eq $after.historyCount)
        runtimeStatus = ($after.runtimeStatus -eq 200)
        runtimeSha = ($before.runtimeSha -eq $after.runtimeSha -and $before.runtimeSha -ne '')
        downloadStatus = ($after.downloadStatus -eq 200)
        downloadSha = ($before.downloadSha -eq $after.downloadSha -and $before.downloadSha -eq $after.assetSha)
        bigIntegerPreserved = ($after.runtimeText -match '9007199254740993' -and $after.configCount -eq 2)
    }

    Write-Host '== 资源文件缺失:必须 503 且不回退编辑区'
    Stop-Instance $b
    $restoredSnapshotRoot = Join-Path $restoreDir 'data/release-snapshots'
    $assetPath = Join-Path $restoredSnapshotRoot "$($after.snapshotID)/assets/$($after.assetID).bin"
    if (-not (Test-Path $assetPath)) {
        $found = Get-ChildItem -Path (Join-Path $restoredSnapshotRoot "$($after.snapshotID)/assets") -File -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($found) { $assetPath = $found.FullName }
    }
    if (-not (Test-Path $assetPath)) { throw "restored asset file not found under $restoredSnapshotRoot" }
    Copy-Item $assetPath (Join-Path $artifacts 'asset-backup.bin') -Force
    Remove-Item $assetPath -Force
    $c = Start-Instance -Name 'restored-missing' -Dir $restoreDir -Port $portB
    Wait-Ready $c
    $missingRuntime = Invoke-Json -Port $portB -Method GET -ScopeEnvironment 'qa_prod' -ScopePipeline 'main' -Path '/api/v1/runtime/release'
    $missingFixed = Invoke-Json -Port $portB -Method GET -ScopeEnvironment 'qa_prod' -ScopePipeline 'main' -Path '/api/v1/runtime/release?version=v1.0'
    $missingAsset = Invoke-Bytes -Port $portB -Path "/api/v1/release/asset/$($after.snapshotID)/$($after.assetID)"
    $missingOldRuntime = Invoke-Json -Port $portB -Method GET -ScopeEnvironment 'qa_prod' -ScopePipeline 'main' -Path '/api/v1/runtime/config'
    Write-Host "== 缺失态: 跟随=$($missingRuntime.status) 固定=$($missingFixed.status) 资源=$($missingAsset.status) error_type=$([string](Field $missingRuntime.json 'error_type'))"

    Copy-Item (Join-Path $artifacts 'asset-backup.bin') $assetPath -Force
    $restoredRuntime = Invoke-Json -Port $portB -Method GET -ScopeEnvironment 'qa_prod' -ScopePipeline 'main' -Path '/api/v1/runtime/release?version=v1.0'
    Write-Host "== 恢复文件后(不重启): 固定读取=$($restoredRuntime.status)"

    $missingChecks = [ordered]@{
        activeRuntime503 = ($missingRuntime.status -eq 503 -and [string](Field $missingRuntime.json 'error_type') -eq 'RELEASE_INCOMPLETE')
        fixedRuntime503 = ($missingFixed.status -eq 503)
        assetDownload503 = ($missingAsset.status -eq 503)
        didNotFallbackToEditor = ($missingRuntime.status -eq 503)
        oldRuntimeStill200 = ($missingOldRuntime.status -eq 200)
        restoredWithoutRestart = ($restoredRuntime.status -eq 200)
    }

    $summary = [ordered]@{
        runId = $runId
        before = $before
        after = $after
        compare = $compare
        missingChecks = $missingChecks
        missingDetail = [ordered]@{ status = $missingRuntime.status; errorType = [string](Field $missingRuntime.json 'error_type'); message = [string](Field $missingRuntime.json 'error') }
    }
    $summary | ConvertTo-Json -Depth 10 | Set-Content -Path (Join-Path $artifacts 'summary.json') -Encoding UTF8

    foreach ($key in $compare.Keys) {
        if (-not $compare[$key]) { Write-Host "FAIL: 恢复比对失败 $key"; $exitCode = 1 }
    }
    foreach ($key in $missingChecks.Keys) {
        if (-not $missingChecks[$key]) { Write-Host "FAIL: 缺失态断言失败 $key"; $exitCode = 1 }
    }
    if ($exitCode -eq 0) { Write-Host '== R24 恢复验证通过' }

    if ($OutputRoot) {
        New-Item -ItemType Directory -Force -Path $OutputRoot | Out-Null
        Copy-Item (Join-Path $artifacts 'summary.json') (Join-Path $OutputRoot "release-recovery-$runId.json") -Force
    }
} catch {
    Write-Host "FATAL: $($_.Exception.Message)" -ForegroundColor Red
    $exitCode = 1
} finally {
    foreach ($record in $records) { Stop-Instance $record }
    Write-Host "== 运行目录:$runDir"
}
exit $exitCode