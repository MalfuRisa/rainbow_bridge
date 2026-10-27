param([int]$Port = 18103, [switch]$SkipBuild)
$ErrorActionPreference = 'Stop'
if ($Port -lt 18100 -or $Port -gt 18109) { throw 'Only isolated UI test ports 18100-18109 are allowed' }
if (Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue) { throw 'Test port is occupied; refusing to replace it' }
$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$runRoot = Join-Path $repository 'output/scope-transfer-ui'
$instance = Join-Path $runRoot ('instance-' + [Guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Path (Join-Path $instance 'browser-downloads') -Force | Out-Null
$exe = Join-Path $runRoot 'rainbow-scope-ui.exe'
$config = @"
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
backup:
  enabled: true
  control_dir: "$($instance.Replace('\','/'))/control"
auth:
  cookie_name: rb_scope_ui_qa
  cookie_secure: false
  allow_insecure_localhost: true
  trusted_origins:
    - "http://127.0.0.1:$Port"
intranet:
  enabled: true
log:
  level: "warn"
  output: "stdout"
"@
[IO.File]::WriteAllText((Join-Path $instance 'config.yaml'), $config)
$variables = @('RB_SCOPE_UI_INSTANCE','RB_QA_ADMIN_USERNAME','RB_QA_ADMIN_PASSWORD','RAINBOW_AUTH_UI_URL','RAINBOW_ADMIN_USERNAME','RAINBOW_ADMIN_INITIAL_PASSWORD','FRONTEND_DIR')
$previous = @{}
foreach ($variable in $variables) { $previous[$variable] = [Environment]::GetEnvironmentVariable($variable, 'Process') }
$ownedProcess = $null
function Start-OwnedUIServer {
    $script:ownedProcess = Start-Process -FilePath $exe -ArgumentList '--config', (Join-Path $instance 'config.yaml') -WorkingDirectory $instance -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $instance 'stdout.log') -RedirectStandardError (Join-Path $instance 'stderr.log')
    $script:ownedTicks = $ownedProcess.StartTime.ToUniversalTime().Ticks
    for ($attempt = 0; $attempt -lt 100; $attempt++) {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri ($env:RAINBOW_AUTH_UI_URL + 'api/v1/version') -TimeoutSec 2
            if ($response.StatusCode -eq 200) { return }
        } catch {}
        if ($ownedProcess.HasExited) { throw 'Isolated instance exited; inspect its test logs' }
        Start-Sleep -Milliseconds 200
    }
    throw 'Isolated instance did not become ready'
}
function Stop-OwnedUIServer {
    if (-not $ownedProcess) { return }
    $running = Get-Process -Id $ownedProcess.Id -ErrorAction SilentlyContinue
    if ($running) {
        if ($running.Path -ne $exe -or $running.StartTime.ToUniversalTime().Ticks -ne $ownedTicks) { throw 'Test process identity changed; refusing stop or fixture write' }
        Stop-Process -Id $running.Id
        $running.WaitForExit(15000) | Out-Null
        if (-not $running.HasExited) { throw 'Test process did not stop; refusing fixture write' }
    }
    $script:ownedProcess = $null
}
Push-Location $repository
try {
    if (-not $SkipBuild) {
        Push-Location vue
        try { npm run build; if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' } } finally { Pop-Location }
        go build -tags=dev -o $exe .
        if ($LASTEXITCODE -ne 0) { throw 'Isolated test binary build failed' }
    }
    $env:RB_SCOPE_UI_INSTANCE = $instance
    $env:RB_QA_ADMIN_USERNAME = 'scope_ui_qa'
    $env:RB_QA_ADMIN_PASSWORD = 'Qa!' + [Guid]::NewGuid().ToString('N')
    $env:RAINBOW_ADMIN_USERNAME = $env:RB_QA_ADMIN_USERNAME
    $env:RAINBOW_ADMIN_INITIAL_PASSWORD = $env:RB_QA_ADMIN_PASSWORD
    $env:FRONTEND_DIR = Join-Path $repository 'vue/dist'
    $env:RAINBOW_AUTH_UI_URL = "http://127.0.0.1:$Port/rainbow-bridge/"
    Start-OwnedUIServer
    Stop-OwnedUIServer
    [IO.File]::WriteAllText((Join-Path $instance 'ui-fixture-server-stopped'), 'owned temporary instance stopped for a boundary fixture')
    go run ./script/qa/release-state-fixture -instance $instance
    if ($LASTEXITCODE -ne 0) { throw 'Stopped-instance release state fixture preparation failed' }
    Start-OwnedUIServer
    Push-Location tests/e2e
    try { npx playwright test --config=playwright.scope-transfer.config.ts; $result = $LASTEXITCODE } finally { Pop-Location }
    Write-Output "UI evidence directory: $instance"
    if ($result -ne 0) { throw 'Scope and transfer frontend integration failed' }
} finally {
    Stop-OwnedUIServer
    foreach ($variable in $variables) { [Environment]::SetEnvironmentVariable($variable, $previous[$variable], 'Process') }
    Pop-Location
}
