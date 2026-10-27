param(
    [switch]$KeepRunning,
    [switch]$SkipBuild,
    [string]$RunId = ""
)

$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
if (-not $RunId) {
    $RunId = (Get-Date -Format "yyyyMMdd-HHmmss") + "-" + ([guid]::NewGuid().ToString("N").Substring(0, 6))
}
$runDir = Join-Path $repo ".local-qa/runtime-business/$RunId"
$artifactsDir = Join-Path $runDir "artifacts"
$binDir = Join-Path $runDir "bin"
$binary = Join-Path $binDir "rainbow_bridge_runtime_business.exe"
$versionTag = "qa-runtime-business-$RunId"
New-Item -ItemType Directory -Force -Path $runDir, $artifactsDir, $binDir | Out-Null

$head = (git -C $repo rev-parse HEAD).Trim()
$status = git -C $repo status --porcelain
$dirtyCount = @($status).Count
$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

if (-not $SkipBuild) {
    Push-Location $repo
    try {
        $ldflags = "-X main.Version=$versionTag -X main.GitCommit=$head -X main.BuildTime=$buildTime"
        & go build -trimpath -ldflags $ldflags -o $binary .
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
} elseif (-not (Test-Path $binary)) {
    throw "-SkipBuild was used but $binary does not exist"
}

$bytes = New-Object byte[] 24
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
try {
    $rng.GetBytes($bytes)
} finally {
    $rng.Dispose()
}
$adminPassword = [Convert]::ToBase64String($bytes).Replace("=", "").Replace("+", "A").Replace("/", "B")
$adminUsername = "qa_admin_" + $RunId.Replace("-", "_").ToLowerInvariant()

$env:RB_QA_RUN_ID = $RunId
$env:RB_QA_RUN_DIR = $runDir
$env:RB_QA_ARTIFACTS_DIR = $artifactsDir
$env:RB_QA_BINARY = $binary
$env:RB_QA_ADMIN_USERNAME = $adminUsername
$env:RB_QA_ADMIN_PASSWORD = $adminPassword
$env:RB_QA_VERSION_TAG = $versionTag
$env:RB_QA_KEEP_RUNNING = if ($KeepRunning) { "1" } else { "0" }
$env:RB_QA_REPO = $repo
$env:RB_QA_GIT_HEAD = $head
$env:RB_QA_DIRTY = "$dirtyCount changed paths"

$runner = Join-Path $repo "tests/e2e/runtime-business/run.mjs"
Push-Location (Join-Path $repo "tests/e2e")
try {
    & node $runner
    $exitCode = $LASTEXITCODE
} finally {
    Pop-Location
}

Write-Host ""
Write-Host "Artifacts: $artifactsDir"
Write-Host "Summary:   $(Join-Path $artifactsDir 'summary.json')"
if ($KeepRunning) {
    Write-Host "Instances were left running intentionally. Use the stop commands printed by the runner."
} else {
    Write-Host "Owned instances were stopped by the runner."
}
exit $exitCode