param([ValidateSet('Start','Stop','Restart')][string]$Mode)
$ErrorActionPreference = 'Stop'
$instancePath = [IO.Path]::GetFullPath($env:RB_BACKUP_UI_INSTANCE)
$allowedRoot = [IO.Path]::GetFullPath('D:/kylin/rainbow_bridge/output/backup-ui-20261025')
if (-not $instancePath.StartsWith($allowedRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing non-test instance directory' }
$pidFile = Join-Path $instancePath 'owned-process.json'
if ($Mode -in @('Stop','Restart') -and (Test-Path -LiteralPath $pidFile)) {
    $owned = Get-Content -LiteralPath $pidFile -Raw | ConvertFrom-Json
    $process = Get-Process -Id $owned.id -ErrorAction SilentlyContinue
    if ($process) {
        if ($process.Path -ne $owned.exe -or $process.StartTime.ToUniversalTime().Ticks -ne $owned.ticks) { throw 'Test process identity changed; refusing stop' }
        Stop-Process -Id $process.Id
        $process.WaitForExit(15000) | Out-Null
    }
}
if ($Mode -in @('Start','Restart')) {
    $port = [int]$env:RB_BACKUP_UI_PORT
    if ($port -in @(18080,18081) -or $port -lt 18090 -or $port -gt 18120) { throw 'Refusing non-test port' }
    $listener = Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue
    if ($listener) { throw 'Test port is occupied' }
    $exe = Join-Path $allowedRoot 'rainbow-backup-ui.exe'
    $env:RAINBOW_ADMIN_USERNAME = $env:RB_QA_ADMIN_USERNAME
    $env:RAINBOW_ADMIN_INITIAL_PASSWORD = $env:RB_QA_ADMIN_PASSWORD
    $env:FRONTEND_DIR = 'D:/kylin/rainbow_bridge/vue/dist'
    $process = Start-Process -FilePath $exe -ArgumentList '--config', (Join-Path $instancePath 'config.yaml') -WorkingDirectory $instancePath -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $instancePath 'stdout.log') -RedirectStandardError (Join-Path $instancePath 'stderr.log')
    @{id=$process.Id; exe=$exe; ticks=$process.StartTime.ToUniversalTime().Ticks} | ConvertTo-Json | Set-Content -LiteralPath $pidFile
    $ready = $false
    for ($attempt = 0; $attempt -lt 100; $attempt++) {
        try { $response = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$port/rainbow-bridge/api/v1/version" -TimeoutSec 2; if ($response.StatusCode -eq 200) { $ready = $true; break } } catch {}
        if ($process.HasExited) { throw 'Temporary backup process exited; inspect its local logs' }
        Start-Sleep -Milliseconds 200
    }
    if (-not $ready) { throw 'Temporary instance did not become ready' }
    Write-Output "Temporary backup instance ready on port $port"
}
