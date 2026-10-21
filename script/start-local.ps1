# Windows PowerShell 5.1 / PowerShell 7. Local demo only.
# Stable data directory; builds and generated config stay outside the repository.
[CmdletBinding()]
param(
    [ValidateRange(1024, 65535)]
    [int]$Port = 18080,
    [string]$DataDir = (Join-Path $env:LOCALAPPDATA 'RainbowBridge\local-demo'),
    [switch]$NoBrowser,
    [switch]$CheckOnly,
    [switch]$NoReplace,
    [string]$RepoDir
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1's web-request progress renderer can corrupt the
# console buffer after native build output. Readiness checks need no progress UI.
$ProgressPreference = 'SilentlyContinue'
$repoRoot = Split-Path -Parent $PSScriptRoot
$serverProcess = $null
$instanceMutex = $null
$mutexOwned = $false
$exitCode = 0
$savedEnv = @{}
$launcherErrorLog = $null

function Write-LauncherMessage {
    param([string]$Message, [switch]$ErrorOutput)
    # Avoid Write-Host's console/progress renderer. Display failures must not
    # tear down a healthy server or obscure an original startup exception.
    try {
        if ($ErrorOutput) { [Console]::Error.WriteLine($Message) }
        else { [Console]::Out.WriteLine($Message) }
    } catch { }
}

# The consolidated checkout is the source of truth. An explicit override
# remains available for an independently checked out version.
function Resolve-LauncherSource {
    param([string]$RequestedDir, [string]$EntryRoot)
    if ($RequestedDir) {
        $candidate = [IO.Path]::GetFullPath($RequestedDir)
    } else {
        $candidate = $EntryRoot
    }
    foreach ($required in @('go.mod', 'biz/service/auth_login.go', 'vue/src/lib/api/auth.ts')) {
        if (-not (Test-Path -LiteralPath (Join-Path $candidate $required) -PathType Leaf)) {
            throw "Selected source is missing $required. Choose the completed login integration with -RepoDir."
        }
    }
    return [IO.Path]::GetFullPath($candidate)
}

function Test-LocalLogin {
    param([string]$Url, [string]$ResetExe, [string]$DataDir)
    $origin = ([uri]$Url).GetLeftPart([UriPartial]::Authority)
    $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    try {
        $body = @{ username = 'admin'; password = 'admin' } | ConvertTo-Json -Compress
        try {
            $login = Invoke-RestMethod -Method Post -Uri "$Url/api/v1/auth/login" -WebSession $session `
                -Headers @{ Origin = $origin } -ContentType 'application/json' -Body $body -TimeoutSec 5
        } catch {
            if (-not $_.Exception.Response -or [int]$_.Exception.Response.StatusCode -ne 401) { throw }
            # Migrate an older random demo password only when the fixed login fails.
            # Existing business data is retained; reset revokes old sessions.
            Write-LauncherMessage 'Updating the existing demo account to admin / admin.'
            Push-Location $DataDir
            try { Invoke-BuildStep -Command $ResetExe -Arguments @('--config', 'config.local-launcher.yaml') }
            finally { Pop-Location }
            $login = Invoke-RestMethod -Method Post -Uri "$Url/api/v1/auth/login" -WebSession $session `
                -Headers @{ Origin = $origin } -ContentType 'application/json' -Body $body -TimeoutSec 5
        }
        $me = Invoke-RestMethod -Uri "$Url/api/v1/auth/me" -WebSession $session -TimeoutSec 5
        if ($me.data.user.username -ne 'admin') { throw 'Unexpected session identity.' }
        $freeze = Invoke-RestMethod -Uri "$Url/api/v1/freeze/status?environment_key=default&pipeline_key=default" -WebSession $session -TimeoutSec 5
        if (-not $freeze.data.admin_configured) { throw 'Administrator is not configured.' }
        $null = Invoke-RestMethod -Method Post -Uri "$Url/api/v1/auth/logout" -WebSession $session `
            -Headers @{ Origin = $origin; 'X-CSRF-Token' = $login.data.csrf_token } `
            -ContentType 'application/json' -Body '{}' -TimeoutSec 5
    } catch {
        throw 'Login readiness check failed. Check server logs; demo credentials must be admin / admin.'
    }
}

function Invoke-BuildStep {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Command failed (exit $LASTEXITCODE)." }
}

function Wait-LocalServer {
    param($Process, [string]$Url, [string]$BuildId)
    $deadline = [DateTime]::UtcNow.AddSeconds(45)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($Process.HasExited) { throw "Server exited during startup (exit $($Process.ExitCode))." }
        try {
            $version = Invoke-RestMethod -Uri "$Url/api/v1/version" -TimeoutSec 2
            if ($version.data.version_info.git_commit -eq $BuildId) {
                $page = Invoke-WebRequest -UseBasicParsing -Uri "$Url/" -TimeoutSec 2
                if ($page.StatusCode -eq 200 -and $page.Content -match '<html') { return }
            }
        } catch { }
        Start-Sleep -Milliseconds 400
    }
    throw 'Server or frontend did not become ready within 45 seconds.'
}

# 占用指定端口的进程;查不到返回 $null(不改动任何进程)。
function Get-PortOwnerProcess {
    param([int]$Port)
    try {
        $connection = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction Stop |
            Select-Object -First 1
        if ($connection) {
            return Get-Process -Id $connection.OwningProcess -ErrorAction SilentlyContinue
        }
        return $null
    } catch { }
    # 回退:没有 Get-NetTCPConnection 时用 netstat 解析 PID
    foreach ($line in (netstat -ano -p TCP)) {
        if ($line -match "^\s*TCP\s+\S+:$Port\s+\S+\s+LISTENING\s+(\d+)\s*$") {
            return Get-Process -Id ([int]$Matches[1]) -ErrorAction SilentlyContinue
        }
    }
    return $null
}

# 是否为本数据目录上一次启动、但启动窗口已退出的演示服务(只认自己的程序与配置)。
function Test-StaleDemoServer {
    param($Process, [string]$ExePath, [string]$ConfigName)
    if (-not $Process) { return $false }
    try {
        $owner = Get-CimInstance Win32_Process -Filter "ProcessId=$($Process.Id)" -ErrorAction SilentlyContinue
        if (-not $owner -or -not $owner.ExecutablePath) { return $false }
        if ([IO.Path]::GetFullPath($owner.ExecutablePath) -ne [IO.Path]::GetFullPath($ExePath)) { return $false }
        if ($ConfigName -and $owner.CommandLine -and ($owner.CommandLine -notlike "*$ConfigName*")) { return $false }
        return $true
    } catch {
        return $false
    }
}

# 停止遗留实例并确认端口已释放;失败返回 $false,由调用方给出明确错误。
function Remove-StaleDemoServer {
    param($Process, [int]$Port)
    try {
        Stop-Process -Id $Process.Id -Force
        $null = $Process.WaitForExit(10000)
    } catch { }
    try {
        $probe = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, $Port)
        try { $probe.Start(); return $true } finally { $probe.Stop() }
    } catch {
        return $false
    }
}

try {
    foreach ($command in @('go', 'node', 'npm.cmd')) {
        if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
            throw "Missing $command. Install Go (see go.mod) and Node.js, then reopen this window."
        }
    }
    $repoRoot = Resolve-LauncherSource -RequestedDir $RepoDir -EntryRoot $repoRoot
    Write-LauncherMessage "Source: $repoRoot"
    $DataDir = [IO.Path]::GetFullPath($DataDir)
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $key = [BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($DataDir.ToLowerInvariant()))).Replace('-', '') }
    finally { $sha.Dispose() }
    $instanceMutex = New-Object Threading.Mutex($false, "Local\RainbowBridgeDemo-$key")
    try { $mutexOwned = $instanceMutex.WaitOne(0) }
    catch [Threading.AbandonedMutexException] { $mutexOwned = $true }
    if (-not $mutexOwned) { throw 'This demo data directory is already in use by another launcher.' }

    $webDir = Join-Path $DataDir 'web'
    $exe = Join-Path $DataDir 'rainbow-local.exe'
    $resetExe = Join-Path $DataDir 'rainbow-password-reset.exe'
    $configFile = Join-Path $DataDir 'config.local-launcher.yaml'
    $outLog = Join-Path $DataDir 'server.stdout.log'
    $errLog = Join-Path $DataDir 'server.stderr.log'
    New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
    $launcherErrorLog = Join-Path $DataDir 'launcher-error.log'

    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, $Port)
    try {
        $listener.Start()
    } catch {
        $owner = Get-PortOwnerProcess -Port $Port
        $isStale = (-not $NoReplace) -and (Test-StaleDemoServer -Process $owner -ExePath $exe -ConfigName 'config.local-launcher.yaml')
        if ($isStale) {
            Write-LauncherMessage "Previous demo server is still running (PID $($owner.Id)); stopping it before restart."
            if (-not (Remove-StaleDemoServer -Process $owner -Port $Port)) {
                throw "Port $Port is still occupied by the previous demo server (PID $($owner.Id)). Stop it manually or use -Port 18083."
            }
            Write-LauncherMessage 'Previous demo server stopped; continuing startup.'
        } else {
            $hint = if ($owner) { " (PID $($owner.Id) $($owner.ProcessName))" } else { '' }
            throw "Port $Port is occupied$hint. Stop that program or use -Port 18083."
        }
    } finally { $listener.Stop() }
    foreach ($name in @('BASE_PATH', 'VITE_API_BASE_URL', 'FRONTEND_DIR', 'RAINBOW_ADMIN_USERNAME', 'RAINBOW_ADMIN_INITIAL_PASSWORD', 'RAINBOW_ADMIN_RESET_PASSWORD')) {
        $savedEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
    }
    $env:BASE_PATH = '/rainbow-bridge/'
    Remove-Item Env:VITE_API_BASE_URL -ErrorAction SilentlyContinue

    Write-LauncherMessage '[1/3] Building frontend...'
    Push-Location (Join-Path $repoRoot 'vue')
    try {
        if (-not (Test-Path -LiteralPath 'node_modules/.bin/vite.cmd')) {
            Invoke-BuildStep -Command 'npm.cmd' -Arguments @('ci')
        }
        Invoke-BuildStep -Command 'npm.cmd' -Arguments @('exec', '--', 'vue-tsc', '-b')
        Invoke-BuildStep -Command 'npm.cmd' -Arguments @('exec', '--', 'vite', 'build', '--outDir', $webDir)
    } finally { Pop-Location }

    Write-LauncherMessage '[2/3] Building backend...'
    $buildId = 'local-' + [guid]::NewGuid().ToString('N')
    Push-Location $repoRoot
    try {
        Invoke-BuildStep -Command 'go' -Arguments @('build', '-tags=dev', '-ldflags', "-X main.GitCommit=$buildId", '-o', $exe, '.')
        Invoke-BuildStep -Command 'go' -Arguments @('build', '-o', $resetExe, './cmd/admin-password-reset')
    } finally { Pop-Location }

    @"
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
auth:
  cookie_name: "rb_demo_$($key.Substring(0, 12).ToLowerInvariant())"
  cookie_secure: false
  allow_insecure_localhost: true
  trusted_origins:
    - "http://127.0.0.1:$Port"
intranet:
  enabled: true
log:
  level: "info"
  output: "stdout"
"@ | Set-Content -LiteralPath $configFile -Encoding UTF8

    $env:FRONTEND_DIR = $webDir
    $env:RAINBOW_ADMIN_USERNAME = 'admin'
    $env:RAINBOW_ADMIN_INITIAL_PASSWORD = 'admin'
    $env:RAINBOW_ADMIN_RESET_PASSWORD = 'admin'
    $url = "http://127.0.0.1:$Port/rainbow-bridge"
    Write-LauncherMessage '[3/3] Starting isolated local server...'
    $serverProcess = Start-Process -FilePath $exe -ArgumentList @('--config', 'config.local-launcher.yaml') `
        -WorkingDirectory $DataDir -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput $outLog -RedirectStandardError $errLog
    Wait-LocalServer -Process $serverProcess -Url $url -BuildId $buildId

    Test-LocalLogin -Url $url -ResetExe $resetExe -DataDir $DataDir
    Write-LauncherMessage "Ready: $url/"
    Write-LauncherMessage "Persistent demo data: $DataDir"
    Write-LauncherMessage 'Login account: admin / password: admin'
    Write-LauncherMessage "Logs: $outLog / $errLog"
    if ($CheckOnly) {
        Write-LauncherMessage 'CHECK PASSED: backend, frontend, login session and authenticated management read are ready. Stopping test instance.'
    } else {
        Write-LauncherMessage 'Sign in once on the login page. Freeze and release operations use the same session.'
        Write-LauncherMessage 'Keep this window open. Press Ctrl+C to stop. Restarting keeps your demo data.'
        if (-not $NoBrowser) {
            try { Start-Process "$url/" }
            catch { Write-LauncherMessage "Could not open browser. Open $url/ manually." -ErrorOutput }
        }
        while (-not $serverProcess.HasExited) { Start-Sleep -Milliseconds 500 }
        if ($serverProcess.ExitCode -ne 0) { throw "Server exited (exit $($serverProcess.ExitCode))." }
    }
} catch {
    $exitCode = 1
    $startupError = $_
    Write-LauncherMessage "ERROR: $($startupError.Exception.Message)" -ErrorOutput
    Write-LauncherMessage $startupError.InvocationInfo.PositionMessage -ErrorOutput
    if ($launcherErrorLog) {
        try {
            @(
                [DateTime]::UtcNow.ToString('o')
                $startupError.Exception.ToString()
                $startupError.InvocationInfo.PositionMessage
                $startupError.ScriptStackTrace
            ) | Add-Content -LiteralPath $launcherErrorLog -Encoding UTF8
            Write-LauncherMessage "Launcher error details: $launcherErrorLog" -ErrorOutput
        } catch {
            Write-LauncherMessage 'Could not write launcher-error.log.' -ErrorOutput
        }
    }
    Write-LauncherMessage 'If startup reached the server step, inspect server.stdout.log and server.stderr.log in the demo data directory.' -ErrorOutput
} finally {
    if ($serverProcess -and -not $serverProcess.HasExited) {
        Stop-Process -Id $serverProcess.Id -ErrorAction SilentlyContinue
        $serverProcess.WaitForExit()
    }
    if ($serverProcess) { $serverProcess.Dispose() }
    foreach ($name in $savedEnv.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnv[$name], 'Process')
    }
    if ($mutexOwned) { $instanceMutex.ReleaseMutex() }
    if ($instanceMutex) { $instanceMutex.Dispose() }
}
exit $exitCode
