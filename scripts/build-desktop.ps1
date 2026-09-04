# Compile dsh-desktop + fetch portable Node/pnpm into build/bin/runtime
param(
    [switch]$SkipRuntime
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

Write-Host "=== wails build ==="
Get-Process dsh-desktop -ErrorAction SilentlyContinue | Stop-Process -Force
# Keep third_party/runtime; only clean bin output
wails build
if ($LASTEXITCODE -ne 0) { throw "wails build failed" }

if (-not $SkipRuntime) {
    Write-Host "=== fetch runtime ==="
    $rt = Join-Path $Root "third_party\runtime"
    & (Join-Path $PSScriptRoot "fetch-runtime.ps1") -OutDir $rt
    # Mirror into build/bin for local green runs
    $binRt = Join-Path $Root "build\bin\runtime"
    if (Test-Path $binRt) { Remove-Item $binRt -Recurse -Force }
    New-Item -ItemType Directory -Force -Path (Split-Path $binRt) | Out-Null
    Copy-Item $rt $binRt -Recurse -Force
}

Write-Host "Done: $(Join-Path $Root 'build\bin\dsh-desktop.exe')"
