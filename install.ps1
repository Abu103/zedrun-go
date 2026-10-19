# zedrun-go installer for Windows.
#
#   irm https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.ps1 | iex
#
# Environment overrides:
#   ZEDRUN_REPO      owner/repo         (default zedrun/zedrun-go)
#   ZEDRUN_BASE_URL  release asset base (default the latest GitHub release)
#   ZEDRUN_HOME      install directory  (default %LOCALAPPDATA%\zedrun)
[CmdletBinding()]
param(
    [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'

$Repo = if ($env:ZEDRUN_REPO) { $env:ZEDRUN_REPO } else { 'zedrun/zedrun-go' }
$Base = if ($env:ZEDRUN_BASE_URL) { $env:ZEDRUN_BASE_URL } else { "https://github.com/$Repo/releases/latest/download" }
$InstallDir = if ($env:ZEDRUN_HOME) { $env:ZEDRUN_HOME } else { Join-Path $env:LOCALAPPDATA 'zedrun' }
$Exe = Join-Path $InstallDir 'zedrun-go.exe'

if ($Uninstall) {
    if (Test-Path $Exe) {
        Remove-Item $Exe -Force
        Write-Host "removed $Exe"
    } else {
        Write-Host "nothing to remove at $Exe"
    }
    exit 0
}

$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$Asset = "zedrun-go-windows-$Arch.exe"
$Url = "$Base/$Asset"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

Write-Host "downloading $Url"
Invoke-WebRequest -Uri $Url -OutFile $Exe -UseBasicParsing

& $Exe --version
if ($LASTEXITCODE -ne 0) {
    Remove-Item $Exe -Force -ErrorAction SilentlyContinue
    throw "the downloaded binary did not run; leaving your system unchanged"
}

$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable('Path', "$UserPath;$InstallDir", 'User')
    Write-Host "added $InstallDir to your user PATH (restart your shell)"
}

Write-Host "installed to $Exe"
