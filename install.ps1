#!/usr/bin/env pwsh
# Zensu CLI installer for Windows (PowerShell).
#   irm https://zensu.dev/install.ps1 | iex
# Env overrides: ZENSU_VERSION (default latest), ZENSU_INSTALL_DIR (default $env:LOCALAPPDATA\Programs\zensu).
#Requires -Version 5.1

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = 'MKITConsulting/zensu-cli'
$Bin = 'zensu.exe'

function Get-Arch {
    $a = $env:PROCESSOR_ARCHITECTURE
    if (-not $a) { try { $a = (uname -m) } catch { $a = 'AMD64' } }
    if ($a -match 'AMD64|x86_64') { return 'amd64' }
    if ($a -match 'ARM64|aarch64') {
        Write-Warning 'No native windows/arm64 build; using amd64 (runs under emulation).'
        return 'amd64'
    }
    throw "unsupported architecture: $a"
}

function Get-LatestVersion {
    (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest").tag_name
}

function Add-ToUserPath([string]$Dir) {
    $isWin = if ($null -ne (Get-Variable -Name IsWindows -ErrorAction SilentlyContinue)) { $IsWindows } else { $true }
    if (-not $isWin) { Write-Host "Note: add $Dir to your PATH."; return }
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $parts = @($userPath -split ';' | Where-Object { $_ -ne '' })
    if ($parts -notcontains $Dir) {
        [Environment]::SetEnvironmentVariable('Path', (($parts + $Dir) -join ';'), 'User')
        Write-Host "Added $Dir to your user PATH. Restart your terminal to use 'zensu'."
    }
    if (($env:Path -split ';') -notcontains $Dir) { $env:Path = "$env:Path;$Dir" }
}

$arch = Get-Arch
$version = if ($env:ZENSU_VERSION) { $env:ZENSU_VERSION } else { Get-LatestVersion }
if (-not $version) { throw 'could not resolve a release version' }
$ver = $version.TrimStart('v')

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("zensu-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $archive = "zensu_${ver}_windows_${arch}.zip"
    $base = "https://github.com/$Repo/releases/download/$version"
    Write-Host "Downloading $archive ($version) ..."
    Invoke-WebRequest "$base/$archive" -OutFile (Join-Path $tmp $archive)
    Invoke-WebRequest "$base/zensu_${ver}_checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

    $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -like "*$archive" } | Select-Object -First 1
    if (-not $line) { throw "no checksum entry for $archive" }
    $want = ($line -split '\s+')[0]
    $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $archive)).Hash
    if ($want.ToLower() -ne $got.ToLower()) { throw "checksum mismatch for $archive" }

    Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath $tmp -Force

    $dir = if ($env:ZENSU_INSTALL_DIR) { $env:ZENSU_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\zensu' }
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    Move-Item -Path (Join-Path $tmp $Bin) -Destination (Join-Path $dir $Bin) -Force
    Write-Host "Installed zensu to $(Join-Path $dir $Bin)"

    Add-ToUserPath $dir
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
