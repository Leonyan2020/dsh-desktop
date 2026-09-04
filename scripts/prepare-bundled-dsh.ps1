# Pre-install @deepseek-ai/dsh with bundled Node/pnpm so Setup can ship a ready runtime.
param(
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Runtime = Join-Path $Root "third_party\runtime"
$Node = Join-Path $Runtime "node\node.exe"
$Pnpm = Join-Path $Runtime "pnpm.exe"
$OutRoot = Join-Path $Root "third_party\bundled-dsh"

if (-not (Test-Path $Node)) { throw "Missing $Node — run fetch-runtime.ps1 first" }
if (-not (Test-Path $Pnpm)) { throw "Missing $Pnpm — run fetch-runtime.ps1 first" }

if (-not $Version) {
    $Version = (& npm view @deepseek-ai/dsh version).Trim()
    if (-not $Version) { throw "Cannot resolve @deepseek-ai/dsh version from npm" }
}

$Dest = Join-Path $OutRoot $Version
Write-Host "Preparing bundled dsh $Version -> $Dest"

if (Test-Path $Dest) { Remove-Item $Dest -Recurse -Force }
New-Item -ItemType Directory -Force -Path $Dest | Out-Null

$pkg = @{
    name = "dsh-runtime-bundled"
    private = $true
    dependencies = @{
        "@deepseek-ai/dsh" = $Version
        "@koromix/koffi-win32-x64" = "3.2.0"
    }
    pnpm = @{
        onlyBuiltDependencies = @(
            "@deepseek-ai/dsh-subprocess-local",
            "@google/genai",
            "koffi",
            "node-pty",
            "protobufjs"
        )
    }
} | ConvertTo-Json -Depth 6
# ConvertTo-Json may mess key order; write manually for reliability
@"
{
  "name": "dsh-runtime-bundled",
  "private": true,
  "dependencies": {
    "@deepseek-ai/dsh": "$Version",
    "@koromix/koffi-win32-x64": "3.2.0"
  },
  "pnpm": {
    "onlyBuiltDependencies": [
      "@deepseek-ai/dsh-subprocess-local",
      "@google/genai",
      "koffi",
      "node-pty",
      "protobufjs"
    ]
  }
}
"@ | Set-Content -Path (Join-Path $Dest "package.json") -Encoding utf8
@"
engine-strict=false
node-linker=hoisted
shamefully-hoist=true
"@ | Set-Content -Path (Join-Path $Dest ".npmrc") -Encoding ascii

$env:PATH = "$(Split-Path $Node);$env:PATH"
$env:Path = $env:PATH
Push-Location $Dest
try {
    & $Pnpm add "@koromix/koffi-win32-x64@3.2.0" --force
    if ($LASTEXITCODE -ne 0) { throw "pnpm add koffi-win32-x64 failed" }
    & $Pnpm add "@deepseek-ai/dsh@$Version" --force
    if ($LASTEXITCODE -ne 0) { throw "pnpm add dsh failed" }
    # Rebuild often fails when child scripts cannot resolve our portable node.exe.
    # Prefer verify; only rebuild if require('koffi') fails.
    & $Node -e "require('koffi'); require('@deepseek-ai/dsh/package.json'); console.log('bundle-ok')"
    if ($LASTEXITCODE -ne 0) {
        Write-Host "koffi verify failed — attempting pnpm rebuild..."
        & $Pnpm rebuild
        if ($LASTEXITCODE -ne 0) { throw "pnpm rebuild failed" }
        & $Node -e "require('koffi'); require('@deepseek-ai/dsh/package.json'); console.log('bundle-ok')"
        if ($LASTEXITCODE -ne 0) { throw "koffi/dsh verify failed" }
    }
} finally {
    Pop-Location
}

Set-Content -Path (Join-Path $OutRoot "version.txt") -Value $Version -Encoding ascii
Write-Host "Bundled dsh ready: $Dest"
