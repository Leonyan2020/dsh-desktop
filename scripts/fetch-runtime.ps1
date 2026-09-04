# Download portable Node + pnpm into a runtime folder for distribution.
param(
    [string]$OutDir = ""
)

$ErrorActionPreference = "Stop"
$NodeVersion = "22.23.2"
$PnpmVersion = "10.34.5"

if (-not $OutDir) {
    $OutDir = Join-Path (Split-Path -Parent $PSScriptRoot) "third_party\runtime"
}

$NodeDir = Join-Path $OutDir "node"
$PnpmExe = Join-Path $OutDir "pnpm.exe"
$Zip = Join-Path $env:TEMP "node-v$NodeVersion-win-x64.zip"
# Prefer npmmirror in CN; fall back to nodejs.org
$NodeUrls = @(
    "https://cdn.npmmirror.com/binaries/node/v$NodeVersion/node-v$NodeVersion-win-x64.zip",
    "https://nodejs.org/dist/v$NodeVersion/node-v$NodeVersion-win-x64.zip"
)
$PnpmUrl = "https://github.com/pnpm/pnpm/releases/download/v$PnpmVersion/pnpm-win-x64.exe"

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

function Download-File([string]$Url, [string]$OutFile) {
    if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
        & curl.exe -L --retry 3 --retry-delay 2 --connect-timeout 20 --fail -o $OutFile $Url
        if ($LASTEXITCODE -ne 0) { throw "curl failed for $Url ($LASTEXITCODE)" }
    } else {
        Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing
    }
}

if (-not (Test-Path (Join-Path $NodeDir "node.exe"))) {
    Write-Host "Downloading Node $NodeVersion ..."
    $ok = $false
    foreach ($NodeUrl in $NodeUrls) {
        try {
            Write-Host "  try $NodeUrl"
            Download-File $NodeUrl $Zip
            $ok = $true
            break
        } catch {
            Write-Warning $_.Exception.Message
            Remove-Item $Zip -Force -ErrorAction SilentlyContinue
        }
    }
    if (-not $ok) { throw "Failed to download Node $NodeVersion from all mirrors" }
    if (Test-Path $NodeDir) { Remove-Item $NodeDir -Recurse -Force }
    $Extract = Join-Path $env:TEMP "node-extract-$NodeVersion"
    if (Test-Path $Extract) { Remove-Item $Extract -Recurse -Force }
    Expand-Archive -Path $Zip -DestinationPath $Extract -Force
    $Inner = Join-Path $Extract "node-v$NodeVersion-win-x64"
    Move-Item $Inner $NodeDir
    Remove-Item $Extract -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item $Zip -Force -ErrorAction SilentlyContinue
    Set-Content -Path (Join-Path $OutDir "node-version.txt") -Value $NodeVersion -Encoding ascii
} else {
    $verFile = Join-Path $OutDir "node-version.txt"
    $have = if (Test-Path $verFile) { (Get-Content $verFile -Raw).Trim() } else { "" }
    if ($have -ne $NodeVersion) {
        Write-Host "Node version mismatch ($have -> $NodeVersion), refreshing..."
        Remove-Item $NodeDir -Recurse -Force
        & $PSCommandPath -OutDir $OutDir
        return
    }
    Write-Host "Node already present: $NodeDir"
}

# Keep a slim Node: only node.exe (+ license). We ship standalone pnpm, so npm tree is useless bloat.
$Keep = @("node.exe", "LICENSE", "LICENSE.MD", "license")
Get-ChildItem $NodeDir -Force | Where-Object {
    $Keep -notcontains $_.Name
} | ForEach-Object {
    Remove-Item $_.FullName -Recurse -Force -ErrorAction SilentlyContinue
}
if (-not (Test-Path (Join-Path $NodeDir "node.exe"))) {
    throw "node.exe missing after slim"
}

if (-not (Test-Path $PnpmExe)) {
    Write-Host "Downloading pnpm $PnpmVersion ..."
    if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
        & curl.exe -L --retry 3 --retry-delay 2 --fail -o $PnpmExe $PnpmUrl
        if ($LASTEXITCODE -ne 0) { throw "curl pnpm download failed ($LASTEXITCODE)" }
    } else {
        Invoke-WebRequest -Uri $PnpmUrl -OutFile $PnpmExe -UseBasicParsing
    }
} else {
    Write-Host "pnpm already present: $PnpmExe"
}

Write-Host "Runtime ready:"
Write-Host "  $NodeDir\node.exe"
Write-Host "  $PnpmExe"
