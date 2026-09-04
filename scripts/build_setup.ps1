# Stage green dist + Inno Setup installer for DSH Desktop
param(
    [switch]$SkipBuild,
    [switch]$SkipRuntime,
    [switch]$SkipBundledDsh,
    [switch]$SkipInstaller
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$DistApp = Join-Path $ProjectRoot "dist\dsh-desktop"
$MainExe = "dsh-desktop.exe"
$InnoScript = Join-Path $ProjectRoot "dsh-desktop.iss"
$InnoCandidates = @(
    "C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
    "C:\Program Files\Inno Setup 6\ISCC.exe",
    (Join-Path $env:LOCALAPPDATA "Programs\Inno Setup 6\ISCC.exe")
)
$InnoPath = $InnoCandidates | Where-Object { Test-Path $_ } | Select-Object -First 1

Set-Location $ProjectRoot
Write-Host "=== DSH Desktop Setup build ==="

if (-not $SkipBuild) {
    Write-Host "[1/5] Compile + runtime..."
    $buildArgs = @()
    if ($SkipRuntime) { $buildArgs += "-SkipRuntime" }
    & (Join-Path $PSScriptRoot "build-desktop.ps1") @buildArgs
    if ($LASTEXITCODE -ne 0) { throw "build-desktop.ps1 failed" }
} else {
    Write-Host "[1/5] Skip compile"
    if (-not $SkipRuntime) {
        & (Join-Path $PSScriptRoot "fetch-runtime.ps1") -OutDir (Join-Path $ProjectRoot "third_party\runtime")
    }
}

$RuntimeDir = Join-Path $ProjectRoot "third_party\runtime"
$Bundled = Join-Path $ProjectRoot "third_party\bundled-dsh"
$BinDir = Join-Path $ProjectRoot "build\bin"
$AppExe = Join-Path $BinDir $MainExe
if (-not (Test-Path $AppExe)) { throw "Missing $MainExe in $BinDir" }
if (-not (Test-Path (Join-Path $RuntimeDir "node\node.exe"))) {
    throw "Missing bundled Node — run fetch-runtime.ps1"
}
if (-not (Test-Path (Join-Path $RuntimeDir "pnpm.exe"))) {
    throw "Missing bundled pnpm — run fetch-runtime.ps1"
}

if (-not $SkipBundledDsh) {
    Write-Host "[2/5] Prepare bundled Harness runtime..."
    & (Join-Path $PSScriptRoot "prepare-bundled-dsh.ps1")
    if ($LASTEXITCODE -ne 0) { throw "prepare-bundled-dsh.ps1 failed" }
} else {
    Write-Host "[2/5] Skip bundled Harness"
}

if (-not (Test-Path (Join-Path $Bundled "version.txt"))) {
    throw "Missing bundled-dsh\version.txt"
}

Write-Host "[3/5] Stage $DistApp..."
if (Test-Path $DistApp) { Remove-Item $DistApp -Recurse -Force }
New-Item -ItemType Directory -Force -Path $DistApp | Out-Null
Copy-Item $AppExe $DistApp -Force
Copy-Item $RuntimeDir (Join-Path $DistApp "runtime") -Recurse -Force
Copy-Item $Bundled (Join-Path $DistApp "bundled-dsh") -Recurse -Force
Copy-Item (Join-Path $ProjectRoot "README.md") (Join-Path $DistApp "README.md") -Force -ErrorAction SilentlyContinue

Write-Host "[4/5] Staged top-level:"
Get-ChildItem $DistApp | ForEach-Object { Write-Host "  $($_.Name)" }

if (-not $SkipInstaller) {
    Write-Host "[5/5] Inno Setup..."
    if (-not $InnoPath) {
        Write-Warning "Inno Setup 6 not found — green dist only: $DistApp"
    } else {
        & $InnoPath $InnoScript
        if ($LASTEXITCODE -ne 0) { throw "Inno Setup failed" }
    }
} else {
    Write-Host "[5/5] Skip installer"
}

Write-Host ""
Write-Host "Done."
Write-Host "  Green:  $DistApp"
$Setup = Join-Path $ProjectRoot "dist\dsh-desktop_Setup.exe"
if (Test-Path $Setup) { Write-Host "  Setup:  $Setup" }
