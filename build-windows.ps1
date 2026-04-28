param(
    [string]$OutputPath = (Join-Path $PSScriptRoot "codex2api.exe"),
    [switch]$Clean,
    [switch]$InstallFrontendDeps
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-CommandExists {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name
    )

    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Command not found: $Name"
    }
}

$repoRoot = $PSScriptRoot
$frontendDir = Join-Path $repoRoot "frontend"
$frontendNodeModules = Join-Path $frontendDir "node_modules"
$frontendDist = Join-Path $frontendDir "dist"
$resolvedOutput = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutputPath)
$outputDir = Split-Path -Parent $resolvedOutput

$goCommand = "go.exe"
$npmCommand = "npm.cmd"

Assert-CommandExists $goCommand
Assert-CommandExists $npmCommand

if (-not (Test-Path $frontendDir)) {
    throw "Frontend directory not found: $frontendDir"
}

Push-Location $repoRoot
try {
    if ($Clean) {
        Write-Host ""
        Write-Host "==> Clean old artifacts" -ForegroundColor Cyan
        if (Test-Path $frontendDist) {
            Remove-Item -LiteralPath $frontendDist -Recurse -Force
        }
        if (Test-Path $resolvedOutput) {
            Remove-Item -LiteralPath $resolvedOutput -Force
        }
    }

    if ($InstallFrontendDeps -or -not (Test-Path $frontendNodeModules)) {
        Write-Host ""
        Write-Host "==> Install frontend dependencies" -ForegroundColor Cyan
        Push-Location $frontendDir
        try {
            & $npmCommand ci
        }
        finally {
            Pop-Location
        }
    }

    Write-Host ""
    Write-Host "==> Build frontend" -ForegroundColor Cyan
    Push-Location $frontendDir
    try {
        & $npmCommand run build
    }
    finally {
        Pop-Location
    }

    if (-not (Test-Path $frontendDist)) {
        throw "Frontend dist directory not found after build: $frontendDist"
    }

    Write-Host ""
    Write-Host "==> Build backend EXE" -ForegroundColor Cyan
    if ($outputDir -and -not (Test-Path $outputDir)) {
        New-Item -ItemType Directory -Path $outputDir | Out-Null
    }

    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"

    & $goCommand build -trimpath -ldflags "-s -w" -o $resolvedOutput .

    if (-not (Test-Path $resolvedOutput)) {
        throw "Output file not found after build: $resolvedOutput"
    }

    $file = Get-Item -LiteralPath $resolvedOutput
    Write-Host ""
    Write-Host "Build completed: $($file.FullName)" -ForegroundColor Green
    Write-Host ("File size: {0:N2} MB" -f ($file.Length / 1MB))
}
finally {
    Pop-Location
}
