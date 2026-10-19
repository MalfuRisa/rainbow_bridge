# 本机完整应用测试入口(RB-QA-01)
# 在任意位置执行；脚本自行定位仓库并在系统临时目录生成本轮隔离实例。
#   & '<repo>/script/test-local.ps1' -Suite Smoke
#   & '<repo>/script/test-local.ps1' -Suite Full
# Smoke: T01/T02/T03/T04/T07/T11 正常路径(前缀实例)。
# Full : T01–T26 全量，并执行重启、备份恢复与根路径前缀重跑。
[CmdletBinding()]
param(
    [ValidateSet('Smoke', 'Full')]
    [string]$Suite = 'Smoke',
    [string]$RunId = '',
    [switch]$SkipBuild,
    [string]$BrowserChannel = '',
    [string]$AttachmentRoot = 'C:\Users\MalfuRisa\Desktop\产出\05-虹桥\附件\本机测试'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent $PSScriptRoot
$script:RunContext = $null
$script:Instances = @()
$script:PhaseResults = [ordered]@{}
$script:GoExe = ''

# ---------------------------------------------------------------- utilities

function Invoke-WithEnv {
    # 临时设置/清除进程环境变量，执行动作后恢复原值。
    param(
        [hashtable]$Set = @{},
        [string[]]$Clear = @(),
        [scriptblock]$Action
    )
    $saved = @{}
    foreach ($key in @($Set.Keys) + @($Clear)) {
        $saved[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
    }
    try {
        foreach ($key in $Clear) { Remove-Item "Env:$key" -ErrorAction SilentlyContinue }
        foreach ($key in $Set.Keys) { Set-Item "Env:$key" $Set[$key] }
        & $Action
    } finally {
        foreach ($key in $saved.Keys) {
            if ($null -eq $saved[$key]) { Remove-Item "Env:$key" -ErrorAction SilentlyContinue }
            else { Set-Item "Env:$key" $saved[$key] }
        }
    }
}

function Get-FirstFreePort {
    param([int[]]$Candidates)
    foreach ($port in $Candidates) {
        $listener = $null
        try {
            $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $port)
            $listener.Start()
            $listener.Stop()
            return $port
        } catch {
            if ($listener) { $listener.Stop() }
        }
    }
    throw "没有可用的回环端口: $($Candidates -join ', ')"
}

function Write-JsonFile {
    param([string]$Path, $Payload)
    $Payload | ConvertTo-Json -Depth 8 | Set-Content -Path $Path -Encoding UTF8
}

function Invoke-NativeCapture {
    # 运行原生命令(git/go/npx)，合并 stdout+stderr 并返回退出码。
    # PS 5.1 在 EAP=Stop 下会把合并进来的 stderr 当成终止错误，这里显式放宽。
    param(
        [scriptblock]$Command,
        [string]$LogPath = ''
    )
    $previous = $ErrorActionPreference
    $previousEncoding = [Console]::OutputEncoding
    $ErrorActionPreference = 'Continue'
    $output = ''
    $exitCode = 0
    try {
        # 原生命令(go/qa-dbtool)输出 UTF-8；PS 5.1 默认按系统代码页(GBK)解码会吃掉引号
        [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
        $output = & $Command *>&1 | Out-String
        $exitCode = $LASTEXITCODE
    } finally {
        [Console]::OutputEncoding = $previousEncoding
        $ErrorActionPreference = $previous
    }
    if ($LogPath) { Set-Content -Path $LogPath -Value $output -Encoding UTF8 }
    return [ordered]@{ Output = $output; ExitCode = $exitCode }
}

function Add-PhaseResult {
    param([string]$Name, [string]$Status, [string]$Detail = '')
    $script:PhaseResults[$Name] = [ordered]@{ status = $Status; detail = $Detail; at = (Get-Date -Format s) }
}

# ---------------------------------------------------------------- run layout

function New-RunContext {
    param([string]$Suite, [string]$RunId)
    if (-not $RunId) {
        $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
        $suffix = -join ((97..122) | Get-Random -Count 4 | ForEach-Object { [char]$_ })
        $RunId = "$stamp-$suffix"
    }
    $runDir = Join-Path $env:TEMP "rainbow-qa-$RunId"
    $subdirs = @('bin', 'web-prefix', 'web-root', 'instance-a', 'instance-b',
        'instance-a-root', 'instance-b-root', 'recovery', 'artifacts')
    foreach ($sub in $subdirs) {
        New-Item -ItemType Directory -Force -Path (Join-Path $runDir $sub) | Out-Null
    }
    $script:RunContext = [ordered]@{
        runId      = $RunId
        suite      = $Suite
        runDir     = $runDir
        artifacts  = Join-Path $runDir 'artifacts'
        bin        = Join-Path $runDir 'bin'
        startedAt  = (Get-Date -Format s)
        repoRoot   = $RepoRoot
        ports      = @{}
        playwright = [ordered]@{}
    }
    Write-Host "== 本轮隔离目录: $runDir"
}

function Save-GitSnapshot {
    $ctx = $script:RunContext
    $status = (Invoke-NativeCapture { & git -C $RepoRoot status --short }).Output
    $diff = (Invoke-NativeCapture { & git -C $RepoRoot diff }).Output
    $head = (Invoke-NativeCapture { & git -C $RepoRoot rev-parse HEAD }).Output
    Set-Content -Path (Join-Path $ctx.artifacts 'git-status.txt') -Value $status -Encoding UTF8
    Set-Content -Path (Join-Path $ctx.artifacts 'git-diff-tracked.patch') -Value $diff -Encoding UTF8
    Set-Content -Path (Join-Path $ctx.artifacts 'git-head.txt') -Value $head -Encoding UTF8
}

# ---------------------------------------------------------------- builds

function Resolve-GoExe {
    $candidates = @()
    if (Get-Command go -ErrorAction SilentlyContinue) { $candidates += 'go' }
    $candidates += (Join-Path $env:USERPROFILE 'sdk\go1.27.1\go\bin\go.exe')
    $candidates += (Join-Path $env:USERPROFILE 'sdk\go1.21.13\go\bin\go.exe')
    foreach ($candidate in $candidates) {
        if ($candidate -eq 'go' -or (Test-Path $candidate)) {
            $result = Invoke-NativeCapture { & $candidate version }
            if ($result.ExitCode -eq 0 -and $result.Output.Trim()) {
                $script:GoExe = $candidate
                return
            }
        }
    }
    throw '未找到可用的 Go 工具链'
}

function Invoke-BackendBuild {
    $ctx = $script:RunContext
    Resolve-GoExe
    $exe = Join-Path $ctx.bin 'rainbow-qa.exe'
    $log = Join-Path $ctx.artifacts 'build-backend.log'
    $buildResult = Invoke-WithEnv -Set @{ GOPROXY = 'https://goproxy.cn,direct'; GOTOOLCHAIN = 'local' } -Action {
        Invoke-NativeCapture -LogPath $log -Command {
            Push-Location $RepoRoot
            try { & $script:GoExe build -tags=dev -o $exe . } finally { Pop-Location }
        }
    }
    if ($buildResult.ExitCode -ne 0) { throw "后端构建失败，见 $log" }
    $dbtoolResult = Invoke-NativeCapture -LogPath (Join-Path $ctx.artifacts 'build-dbtool.log') -Command {
        Push-Location $RepoRoot
        try { & $script:GoExe build -o (Join-Path $ctx.bin 'qa-dbtool.exe') ./script/qa-dbtool } finally { Pop-Location }
    }
    if ($dbtoolResult.ExitCode -ne 0) { throw 'qa-dbtool 构建失败' }
    Write-Host '== 后端与取证工具构建完成'
}

function Invoke-FrontendTypecheck {
    $ctx = $script:RunContext
    $log = Join-Path $ctx.artifacts 'frontend-typecheck.log'
    $result = Invoke-NativeCapture -LogPath $log -Command {
        Push-Location (Join-Path $RepoRoot 'vue')
        try { & npx --no-install vue-tsc -b --force } finally { Pop-Location }
    }
    Add-PhaseResult 'frontend-typecheck' $(if ($result.ExitCode -eq 0) { 'PASS' } else { 'FAIL' }) "exit=$($result.ExitCode) log=$log"
    if ($result.ExitCode -ne 0) { Write-Warning "vue 类型检查未通过(exit=$($result.ExitCode))，继续构建以运行页面测试" }
}

function Invoke-FrontendBuild {
    # 构建两套前端产物:带前缀与根路径。不覆盖 vue/dist。
    param([string]$OutDir, [string]$BasePath)
    $ctx = $script:RunContext
    $log = Join-Path $ctx.artifacts "build-frontend-$(Split-Path -Leaf $OutDir).log"
    $result = Invoke-WithEnv -Set @{ BASE_PATH = $BasePath } -Clear @('VITE_API_BASE_URL') -Action {
        Invoke-NativeCapture -LogPath $log -Command {
            Push-Location (Join-Path $RepoRoot 'vue')
            try { & npx --no-install vite build --outDir $OutDir --emptyOutDir } finally { Pop-Location }
        }
    }
    if ($result.ExitCode -ne 0) { throw "前端构建失败($BasePath)，见 $log" }
    Write-Host "== 前端构建完成: $BasePath -> $OutDir"
}

function Invoke-FrontendBuilds {
    $ctx = $script:RunContext
    Invoke-FrontendTypecheck
    Invoke-FrontendBuild -OutDir (Join-Path $ctx.runDir 'web-prefix') -BasePath '/rainbow-bridge/'
    Invoke-FrontendBuild -OutDir (Join-Path $ctx.runDir 'web-root') -BasePath '/'
}

# ---------------------------------------------------------------- instances

function New-InstanceConfigFile {
    param([string]$InstanceDir, [int]$Port, [string]$BasePath)
    $yaml = @"
server:
  address: "127.0.0.1:$Port"
  base_path: "$BasePath"
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
    $configPath = Join-Path $InstanceDir 'config.yaml'
    Set-Content -Path $configPath -Value $yaml -Encoding UTF8
    return $configPath
}

function Start-QaInstance {
    param(
        [string]$Name,
        [string]$InstanceDir,
        [int]$Port,
        [string]$FrontendDir,
        [string]$BasePath,
        [switch]$ReuseConfig
    )
    $ctx = $script:RunContext
    $exe = Join-Path $ctx.bin 'rainbow-qa.exe'
    if ($ReuseConfig) {
        $configPath = Join-Path $InstanceDir 'config.yaml'
        if (-not (Test-Path $configPath)) { throw "ReuseConfig 但缺少配置文件: $configPath" }
    } else {
        $configPath = New-InstanceConfigFile -InstanceDir $InstanceDir -Port $Port -BasePath $BasePath
    }
    $outLog = Join-Path $ctx.artifacts "instance-$Name.out.log"
    $errLog = Join-Path $ctx.artifacts "instance-$Name.err.log"
    $process = Invoke-WithEnv -Set @{ FRONTEND_DIR = $FrontendDir } -Clear @('BASE_PATH') -Action {
        Start-Process -FilePath $exe -ArgumentList '--config', $configPath `
            -WorkingDirectory $InstanceDir -WindowStyle Hidden -PassThru `
            -RedirectStandardOutput $outLog -RedirectStandardError $errLog
    }
    $record = [ordered]@{
        name        = $Name
        pid         = $process.Id
        port        = $Port
        basePath    = $BasePath
        origin      = "http://127.0.0.1:$Port"
        instanceDir = $InstanceDir
        configPath  = $configPath
        frontendDir = $FrontendDir
        exe         = $exe
        outLog      = $outLog
        errLog      = $errLog
        startedAt   = (Get-Date -Format s)
        process     = $process
    }
    $script:Instances += $record
    Write-Host "== 实例 $Name 启动 pid=$($process.Id) port=$Port cwd=$InstanceDir"
    return $record
}

function Get-PrefixedUrl {
    param($Record, [string]$Path)
    $prefix = $Record.basePath
    if ($prefix -and $prefix.EndsWith('/')) { $prefix = $prefix.TrimEnd('/') }
    if (-not $Path.StartsWith('/')) { $Path = "/$Path" }
    return "$($Record.origin)$prefix$Path"
}

function Wait-QaInstanceReady {
    param($Record, [int]$TimeoutSec = 30)
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    $evidence = [ordered]@{ instance = $Record.name; port = $Record.port; attempts = @() }
    $lastError = ''
    while ((Get-Date) -lt $deadline) {
        if ($Record.process.HasExited) {
            $tail = Get-Content -Path $Record.outLog -Tail 30 -ErrorAction SilentlyContinue | Out-String
            throw "实例 $($Record.name) 进程提前退出(exit=$($Record.process.ExitCode))，日志:$tail"
        }
        try {
            $pingUrl = Get-PrefixedUrl $Record '/ping'
            $ping = Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 -Uri $pingUrl
            $homeUrl = Get-PrefixedUrl $Record '/'
            $homeResponse = Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 -Uri $homeUrl
            $versionUrl = Get-PrefixedUrl $Record '/api/v1/version'
            $version = Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 -Uri $versionUrl
            if ($ping.Content -match 'pong' -and $homeResponse.StatusCode -eq 200 -and $version.StatusCode -eq 200) {
                $evidence.ping = $ping.Content
                $evidence.homeStatus = $homeResponse.StatusCode
                $evidence.version = $version.Content
                # 首页引用的 JS/CSS 必须能取回
                $assetUrls = [regex]::Matches($homeResponse.Content, '(?:src|href)="([^"]+\.(?:js|css))"') |
                    ForEach-Object { $_.Groups[1].Value } | Select-Object -Unique -First 3
                $assetChecks = @()
                foreach ($assetUrl in $assetUrls) {
                    $absolute = $assetUrl
                    if ($absolute -notmatch '^https?://') {
                        $absolute = "$($Record.origin)$absolute"
                    }
                    $assetResp = Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 -Uri $absolute
                    $assetChecks += [ordered]@{ url = $absolute; status = $assetResp.StatusCode; length = $assetResp.RawContentLength }
                }
                $evidence.assets = $assetChecks
                $evidence.readyAt = (Get-Date -Format s)
                if ($assetChecks.Count -eq 0) { $evidence.warning = '首页未发现 JS/CSS 引用' }
                Write-JsonFile -Path (Join-Path $script:RunContext.artifacts "ready-$($Record.name).json") -Payload $evidence
                Write-Host "== 实例 $($Record.name) 就绪"
                return $evidence
            }
            $lastError = "ping=$($ping.StatusCode) home=$($homeResponse.StatusCode) version=$($version.StatusCode)"
        } catch {
            $lastError = $_.Exception.Message
        }
        Start-Sleep -Milliseconds 700
    }
    Write-JsonFile -Path (Join-Path $script:RunContext.artifacts "ready-$($Record.name).FAILED.json") -Payload $evidence
    throw "实例 $($Record.name) $TimeoutSec 秒内未就绪: $lastError"
}

function Stop-QaInstance {
    param($Record)
    if (-not $Record) { return }
    try {
        if (-not $Record.process.HasExited) {
            Stop-Process -Id $Record.pid -Force
            $Record.process.WaitForExit(10000) | Out-Null
        }
    } catch { Write-Warning "停止 $($Record.name) 失败: $($_.Exception.Message)" }
    $Record.stoppedAt = (Get-Date -Format s)
    Write-Host "== 实例 $($Record.name) 已停止 pid=$($Record.pid)"
}

function Stop-AllInstances {
    foreach ($record in $script:Instances) { Stop-QaInstance $record }
}

function Get-InstanceDataSnapshot {
    param($Record, [string]$Label)
    $dataDir = Join-Path $Record.instanceDir 'data'
    $files = @()
    if (Test-Path $dataDir) {
        $files = Get-ChildItem -Path $dataDir -Recurse -File | ForEach-Object {
            $isLiveDatabase = $_.Name -like '*.db*'
            [ordered]@{
                relative = $_.FullName.Substring($dataDir.Length + 1)
                size     = $_.Length
                sha256   = $(if ($isLiveDatabase) { $null } else { (Get-FileHash -Algorithm SHA256 -Path $_.FullName).Hash.ToLower() })
                note     = $(if ($isLiveDatabase) { 'live-sqlite:字节哈希跳过，改用下方 tables 逻辑摘要' } else { '' })
            }
        }
    }
    # 运行中的 SQLite 以只读方式导出逻辑摘要(计数 + 内容哈希)，可与文件哈希互补
    $tables = [ordered]@{}
    $dbFile = Join-Path $dataDir 'resource.db'
    $dbtool = Join-Path $script:RunContext.bin 'qa-dbtool.exe'
    if ((Test-Path $dbFile) -and (Test-Path $dbtool)) {
        foreach ($table in @('environment', 'pipeline', 'resource_config', 'asset')) {
            $result = Invoke-NativeCapture { & $dbtool dump $dbFile $table }
            if ($result.ExitCode -eq 0 -and $result.Output.Trim()) {
                $hash = [System.BitConverter]::ToString(
                    [System.Security.Cryptography.SHA256]::Create().ComputeHash(
                        [System.Text.Encoding]::UTF8.GetBytes($result.Output))).Replace('-', '').ToLower()
                $parsed = $result.Output | ConvertFrom-Json
                $tables[$table] = [ordered]@{ count = $parsed.count; sha256 = $hash }
            } else {
                $tables[$table] = [ordered]@{ count = -1; error = $result.Output.Substring(0, [Math]::Min(160, $result.Output.Length)) }
            }
        }
    }
    Write-JsonFile -Path (Join-Path $script:RunContext.artifacts "snapshot-$($Record.name)-$Label.json") -Payload @{
        instance = $Record.name; label = $Label; at = (Get-Date -Format s); files = $files; tables = $tables
    }
}

# ---------------------------------------------------------------- playwright

function Get-LiveInstances {
    $result = @{}
    foreach ($record in $script:Instances) {
        if (-not $record.process.HasExited) { $result[$record.name] = $record }
    }
    return $result
}

function Invoke-PlaywrightPhase {
    param(
        [string]$Phase,
        [string]$Grep = '',
        [string]$GrepInvert = ''
    )
    $ctx = $script:RunContext
    $live = Get-LiveInstances
    $a = $live['a']; $b = $live['b']; $ra = $live['root-a']; $rb = $live['root-b']
    # 恢复阶段:B 实例已停止，由 recovery 实例承担 B 角色
    if (-not $b -and $live['recovery']) { $b = $live['recovery'] }
    $envSet = @{
        RB_QA_RUN_ID           = $ctx.runId
        RB_QA_SUITE            = $Suite
        RB_QA_PHASE            = $Phase
        RB_QA_RUN_DIR          = $ctx.runDir
        RB_QA_ARTIFACTS_DIR    = $ctx.artifacts
        RB_QA_DBTOOL           = Join-Path $ctx.bin 'qa-dbtool.exe'
        RB_QA_PW_JSON          = Join-Path $ctx.artifacts "pw-$Phase.json"
        RB_QA_TEST_RESULTS_DIR = Join-Path $ctx.artifacts "test-results-$Phase"
        RB_QA_EXPECTED_STATE   = Join-Path $ctx.artifacts 'expected-state.json'
        RB_QA_CHANNEL          = $BrowserChannel
        RB_QA_A_ORIGIN         = $(if ($a) { $a.origin } else { '' })
        RB_QA_A_PREFIX         = $(if ($a) { $a.basePath } else { '' })
        RB_QA_A_PID            = $(if ($a) { "$($a.pid)" } else { '' })
        RB_QA_A_INSTANCE_DIR   = $(if ($a) { $a.instanceDir } else { '' })
        RB_QA_B_ORIGIN         = $(if ($b) { $b.origin } else { '' })
        RB_QA_B_PREFIX         = $(if ($b) { $b.basePath } else { '' })
        RB_QA_B_PID            = $(if ($b) { "$($b.pid)" } else { '' })
        RB_QA_B_INSTANCE_DIR   = $(if ($b) { $b.instanceDir } else { '' })
        RB_QA_ROOT_A_ORIGIN    = $(if ($ra) { $ra.origin } else { '' })
        RB_QA_ROOT_A_PREFIX    = $(if ($ra) { $ra.basePath } else { '' })
        RB_QA_ROOT_A_INSTANCE_DIR = $(if ($ra) { $ra.instanceDir } else { '' })
        RB_QA_ROOT_B_ORIGIN    = $(if ($rb) { $rb.origin } else { '' })
        RB_QA_ROOT_B_PREFIX    = $(if ($rb) { $rb.basePath } else { '' })
        RB_QA_ROOT_B_INSTANCE_DIR = $(if ($rb) { $rb.instanceDir } else { '' })
    }
    $log = Join-Path $ctx.artifacts "pw-$Phase.console.log"
    $result = Invoke-WithEnv -Set $envSet -Clear @('VITE_API_BASE_URL') -Action {
        Invoke-NativeCapture -LogPath $log -Command {
            Push-Location (Join-Path $RepoRoot 'tests\e2e')
            try {
                $pwArgs = @('playwright', 'test', '--config=playwright.local.config.ts')
                if ($Grep) { $pwArgs += @('--grep', $Grep) }
                if ($GrepInvert) { $pwArgs += @('--grep-invert', $GrepInvert) }
                & npx --no-install @pwArgs
            } finally { Pop-Location }
        }
    }
    $exit = $result.ExitCode
    $ctx.playwright[$Phase] = $exit
    Add-PhaseResult "playwright-$Phase" $(if ($exit -eq 0) { 'PASS' } else { 'FAIL' }) "exit=$exit log=$log"
    Write-Host "== Playwright 阶段 $Phase 结束 exit=$exit"
    return $exit
}

# ---------------------------------------------------------------- manifest

function Write-Manifest {
    $ctx = $script:RunContext
    $ctx.finishedAt = (Get-Date -Format s)
    $ctx.phases = $script:PhaseResults
    $ctx.instances = $script:Instances | ForEach-Object {
        [ordered]@{
            name = $_.name; pid = $_.pid; port = $_.port; basePath = $_.basePath
            instanceDir = $_.instanceDir; configPath = $_.configPath
            frontendDir = $_.frontendDir; exe = $_.exe
            outLog = $_.outLog; errLog = $_.errLog; startedAt = $_.startedAt
            stoppedAt = $(if ($_.Contains('stoppedAt')) { $_.stoppedAt } else { '' })
        }
    }
    $ctx.environment = [ordered]@{
        go     = $script:GoExe
        node   = (Invoke-NativeCapture { & node --version }).Output.Trim()
        npm    = (Invoke-NativeCapture { & npm --version }).Output.Trim()
    }
    Write-JsonFile -Path (Join-Path $ctx.artifacts 'manifest.json') -Payload $ctx
}

function Copy-ArtifactsToDesktop {
    $ctx = $script:RunContext
    $target = Join-Path $AttachmentRoot $ctx.runId
    New-Item -ItemType Directory -Force -Path $target | Out-Null
    Copy-Item -Path (Join-Path $ctx.artifacts '*') -Destination $target -Recurse -Force
    Write-Host "== 证据已复制到 $target"
}

# ---------------------------------------------------------------- main flow

function Invoke-QaRun {
    New-RunContext -Suite $Suite -RunId $RunId
    Save-GitSnapshot
    $ctx = $script:RunContext

    if (-not $SkipBuild) {
        Invoke-BackendBuild
        Invoke-FrontendBuilds
    } else {
        # 复用同 run 目录已有产物时必须显式确认
        if (-not (Test-Path (Join-Path $ctx.bin 'rainbow-qa.exe'))) {
            throw 'SkipBuild 但本轮目录缺少产物，请去掉 -SkipBuild'
        }
        Add-PhaseResult 'build' 'SKIPPED' 'SkipBuild'
    }

    $prefixPort = Get-FirstFreePort -Candidates @(11880, 11882, 11884, 11886)
    $bPort = Get-FirstFreePort -Candidates @(11881, 11883, 11885, 11887)
    $rootPortA = Get-FirstFreePort -Candidates @(11890, 11892, 11894)
    $rootPortB = Get-FirstFreePort -Candidates @(11891, 11893, 11895)
    $ctx.ports = @{ prefixA = $prefixPort; prefixB = $bPort; rootA = $rootPortA; rootB = $rootPortB }

    $webPrefix = Join-Path $ctx.runDir 'web-prefix'
    $webRoot = Join-Path $ctx.runDir 'web-root'

    # ---- T01: 空实例启动 → 停止 → 同实例重启
    $instanceA = Join-Path $ctx.runDir 'instance-a'
    $first = Start-QaInstance -Name 'a' -InstanceDir $instanceA -Port $prefixPort -FrontendDir $webPrefix -BasePath '/rainbow-bridge'
    $ready1 = Wait-QaInstanceReady $first
    Stop-QaInstance $first
    Start-Sleep -Seconds 1
    $second = Start-QaInstance -Name 'a' -InstanceDir $instanceA -Port $prefixPort -FrontendDir $webPrefix -BasePath '/rainbow-bridge'
    $ready2 = Wait-QaInstanceReady $second
    Write-JsonFile -Path (Join-Path $ctx.artifacts 't01-restart.json') -Payload @{
        firstPid = $first.pid; secondPid = $second.pid
        firstLog = $first.outLog; secondLog = $second.outLog
        firstReady = $ready1.version; secondReady = $ready2.version
    }
    Add-PhaseResult 't01-restart' 'EXECUTED' "pid1=$($first.pid) pid2=$($second.pid)"

    # ---- B 实例(导入/恢复目标)
    $instanceB = Join-Path $ctx.runDir 'instance-b'
    $instanceBRecord = Start-QaInstance -Name 'b' -InstanceDir $instanceB -Port $bPort -FrontendDir $webPrefix -BasePath '/rainbow-bridge'
    Wait-QaInstanceReady $instanceBRecord | Out-Null

    Get-InstanceDataSnapshot $second 'pre-suite'
    Get-InstanceDataSnapshot $instanceBRecord 'pre-suite'

    # ---- 前缀阶段:主套件
    $grep = $(if ($Suite -eq 'Smoke') { '@smoke' } else { '' })
    $invert = $(if ($Suite -eq 'Smoke') { '' } else { '@postrestart|@recovery|@root' })
    $mainExit = Invoke-PlaywrightPhase -Phase 'prefix-main' -Grep $grep -GrepInvert $invert

    Get-InstanceDataSnapshot $second 'post-suite'
    Get-InstanceDataSnapshot $instanceBRecord 'post-suite'

    if ($Suite -eq 'Full') {
        # ---- T25: 停进程 → 同配置重启 → 校验持久化
        Stop-QaInstance $second
        Start-Sleep -Seconds 1
        $restarted = Start-QaInstance -Name 'a' -InstanceDir $instanceA -Port $prefixPort -FrontendDir $webPrefix -BasePath '/rainbow-bridge'
        Wait-QaInstanceReady $restarted | Out-Null
        Invoke-PlaywrightPhase -Phase 'postrestart' -Grep '@postrestart' | Out-Null
        foreach ($restartRound in @(2,3)) {
            Stop-QaInstance $restarted
            $restarted = Start-QaInstance -Name 'a' -InstanceDir $instanceA -Port $prefixPort -FrontendDir $webPrefix -BasePath '/rainbow-bridge'
            Wait-QaInstanceReady $restarted | Out-Null
            Invoke-PlaywrightPhase -Phase "postrestart-$restartRound" -Grep '@postrestart' | Out-Null
        }


        # ---- T26: 停机一致性副本作为新实例启动
        Stop-QaInstance $restarted
        Stop-QaInstance $instanceBRecord
        $recoveryDir = Join-Path $ctx.runDir 'recovery'
        Copy-Item -Path (Join-Path $instanceA 'config.yaml') -Destination $recoveryDir -Force
        Copy-Item -Path (Join-Path $instanceA 'data') -Destination $recoveryDir -Recurse -Force
        $recovery = Start-QaInstance -Name 'recovery' -InstanceDir $recoveryDir -Port $prefixPort -FrontendDir $webRoot -BasePath ''
        Wait-QaInstanceReady $recovery | Out-Null
        Invoke-PlaywrightPhase -Phase 'recovery' -Grep '@recovery' | Out-Null
        Stop-QaInstance $recovery

        # ---- 根路径前缀重跑(独立实例目录 + web-root)
        $rootA = Join-Path $ctx.runDir 'instance-a-root'
        $rootB = Join-Path $ctx.runDir 'instance-b-root'
        $rootARecord = Start-QaInstance -Name 'root-a' -InstanceDir $rootA -Port $rootPortA -FrontendDir $webRoot -BasePath ''
        Wait-QaInstanceReady $rootARecord | Out-Null
        $rootBRecord = Start-QaInstance -Name 'root-b' -InstanceDir $rootB -Port $rootPortB -FrontendDir $webRoot -BasePath ''
        Wait-QaInstanceReady $rootBRecord | Out-Null
        Invoke-PlaywrightPhase -Phase 'root' -Grep '@root' | Out-Null
    } else {
        Stop-QaInstance $second
        Stop-QaInstance $instanceBRecord
    }
}

$exitCode = 0
try {
    Invoke-QaRun
} catch {
    Add-PhaseResult 'fatal' 'FAIL' $_.Exception.Message
    Write-Host "FATAL: $($_.Exception.Message)" -ForegroundColor Red
    $exitCode = 1
} finally {
    Stop-AllInstances
    try { Write-Manifest } catch { Write-Warning "写 manifest 失败: $($_.Exception.Message)" }
    try { Copy-ArtifactsToDesktop } catch { Write-Warning "复制附件失败: $($_.Exception.Message)" }
    if ($script:PhaseResults.Values | Where-Object { $_.status -eq 'FAIL' }) { $exitCode = 1 }
    Write-Host "== 运行目录保留: $($script:RunContext.runDir)"
}
exit $exitCode