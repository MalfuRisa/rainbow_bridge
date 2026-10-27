param([int]$Port = 18097, [switch]$SkipBuild)
$ErrorActionPreference = 'Stop'
if ($Port -lt 18090 -or $Port -gt 18120) { throw 'Only isolated UI test ports 18090-18120 are allowed' }
if (Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue) { throw 'Test port is occupied; refusing to replace it' }
$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$runRoot = Join-Path $repository 'output/backup-ui-20261025'
$instance = Join-Path $runRoot ('instance-' + [Guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Path $instance | Out-Null
New-Item -ItemType Directory -Path (Join-Path $instance 'control') | Out-Null
New-Item -ItemType Directory -Path (Join-Path $instance 'browser-downloads') | Out-Null
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
  cookie_name: rb_backup_ui_qa
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
$variables = @('RB_BACKUP_UI_INSTANCE','RB_BACKUP_UI_PORT','RB_QA_ADMIN_USERNAME','RB_QA_ADMIN_PASSWORD','RAINBOW_AUTH_UI_URL')
$previous = @{}
foreach ($variable in $variables) { $previous[$variable] = [Environment]::GetEnvironmentVariable($variable, 'Process') }
Push-Location $repository
try {
    if (-not $SkipBuild) {
        Push-Location vue
        try { npm run build; if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' } } finally { Pop-Location }
        go build -tags=dev -o (Join-Path $runRoot 'rainbow-backup-ui.exe') .
        if ($LASTEXITCODE -ne 0) { throw 'Temporary backend build failed' }
    }
    $env:RB_BACKUP_UI_INSTANCE = $instance
    $env:RB_BACKUP_UI_PORT = [string]$Port
    $env:RB_QA_ADMIN_USERNAME = 'backup_qa'
    $env:RB_QA_ADMIN_PASSWORD = 'Qa!' + [Guid]::NewGuid().ToString('N')
    $env:RAINBOW_AUTH_UI_URL = "http://127.0.0.1:$Port/rainbow-bridge/"
    Push-Location tests/e2e
    try {
        & powershell.exe -NoProfile -File backup/instance.ps1 -Mode Start
        if ($LASTEXITCODE -ne 0) { throw 'Temporary instance start failed' }
        npx playwright test --config=playwright.backup.config.ts
        $result = $LASTEXITCODE
    } finally {
        & powershell.exe -NoProfile -File backup/instance.ps1 -Mode Stop
        Pop-Location
    }
    Write-Output "Evidence directory: $instance"
    if ($result -ne 0) { throw 'Backup frontend real integration failed' }
} finally {
    foreach ($variable in $variables) { [Environment]::SetEnvironmentVariable($variable, $previous[$variable], 'Process') }
    Pop-Location
}
