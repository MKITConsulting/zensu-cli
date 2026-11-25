#!/usr/bin/env pwsh
# Zensu CLI installer for Windows (PowerShell).
#   irm https://zensu.dev/install.ps1 | iex
# Env overrides: ZENSU_VERSION (default latest), ZENSU_INSTALL_DIR (default $env:LOCALAPPDATA\Programs\zensu), ZENSU_RELEASE_URL, ZENSU_REQUIRE_SIGNATURE=1, ZENSU_OPENSSL, ZENSU_NO_MODIFY_PATH=1.
#Requires -Version 5.1

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = 'MKITConsulting/zensu-cli'
$Bin = 'zensu.exe'

function Get-ReleaseKeys {
    @('7hr40IQBkj9kWZmBYO/AlfOH8fk/L27k5btCyV9Ciyg=')
}

function Get-SelfTestKey {
    'PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw='
}

function Get-SelfTestSignature {
    'kqAJqfDUyrhyDoILX2QlQKKye1QWUD+Ps3YiI+vbadoIWsHkPhWZbkWPNhPQ8R2MOHsurrQwKu6wDSkWErsMAA=='
}

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
    try { (Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$Repo/releases/latest").tag_name } catch { $null }
}

function Resolve-Version {
    $version = if ($env:ZENSU_VERSION) { $env:ZENSU_VERSION } else { 'latest' }
    if ($version -ceq 'latest') {
        if ($env:ZENSU_RELEASE_URL) { throw 'ZENSU_RELEASE_URL needs ZENSU_VERSION, because a mirror cannot resolve the latest release' }
        $version = Get-LatestVersion
    }
    if (-not $version) { throw 'could not resolve a release version' }
    $version = [string]$version
    if (-not $version.StartsWith('v', [StringComparison]::Ordinal)) { $version = "v$version" }
    if ($version.Substring(1) -cnotmatch '\A[0-9A-Za-z.+-]+\z') { throw "invalid version $version" }
    return $version
}

function Write-Note([string]$Message) {
    [Console]::Error.WriteLine("Note: $Message.")
}

function Invoke-Download([string]$Url, [string]$Path) {
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Path
        return $true
    } catch {
        return $false
    }
}

function Save-Download([string]$Url, [string]$Path) {
    if (-not (Invoke-Download $Url $Path)) { throw "could not download $Url" }
}

function Invoke-Quiet([string]$Exe, [string[]]$Arguments) {
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $Exe @Arguments 2>$null
        if ($LASTEXITCODE -eq 0) { return (@($output) -join "`n") }
    } catch {
    }
    return $null
}

function Resolve-Application([string]$Name) {
    if ($Name.Contains('\') -or $Name.Contains('/')) {
        if (Test-Path -LiteralPath $Name -PathType Leaf) { return (Resolve-Path -LiteralPath $Name).ProviderPath }
        return $null
    }
    $command = Get-Command -Name $Name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { return $command.Path }
    return $null
}

function Write-PublicKey([string]$Path, [string]$Key) {
    [IO.File]::WriteAllText($Path, "-----BEGIN PUBLIC KEY-----`nMCowBQYDK2VwAyEA$Key`n-----END PUBLIC KEY-----`n", [Text.Encoding]::ASCII)
}

function Write-Signature([string]$Path, [string]$Text) {
    try {
        [IO.File]::WriteAllBytes($Path, [Convert]::FromBase64String($Text))
        return $true
    } catch {
        return $false
    }
}

function Test-Signature([string]$Exe, [string]$Key, [string]$Message, [string]$Signature) {
    return $null -ne (Invoke-Quiet $Exe @('pkeyutl', '-verify', '-pubin', '-inkey', $Key, '-rawin', '-in', $Message, '-sigfile', $Signature))
}

function Test-OpenSsl3([string]$Exe) {
    $version = Invoke-Quiet $Exe @('version')
    return ($null -ne $version) -and ($version -cmatch '\AOpenSSL ([3-9]|[1-9][0-9])')
}

function Find-Ed25519Verifier([string]$Dir, [string]$Candidate) {
    $exe = Resolve-Application $Candidate
    if (-not $exe -or -not (Test-OpenSsl3 $exe)) { return $null }
    $pem = Join-Path $Dir 'selftest.pem'
    $message = Join-Path $Dir 'selftest.msg'
    $signature = Join-Path $Dir 'selftest.sig'
    Write-PublicKey $pem (Get-SelfTestKey)
    [IO.File]::WriteAllBytes($message, [byte[]]@(0x72))
    if (-not (Write-Signature $signature (Get-SelfTestSignature))) { return $null }
    if (Test-Signature $exe $pem $message $signature) { return $exe }
    return $null
}

function Find-OpenSsl([string]$Dir) {
    if ($env:ZENSU_OPENSSL) { return Find-Ed25519Verifier $Dir $env:ZENSU_OPENSSL }
    $candidates = @('openssl')
    if ($env:ProgramFiles) {
        $candidates += Join-Path $env:ProgramFiles 'Git\mingw64\bin\openssl.exe'
        $candidates += Join-Path $env:ProgramFiles 'Git\usr\bin\openssl.exe'
    }
    foreach ($candidate in $candidates) {
        $exe = Find-Ed25519Verifier $Dir $candidate
        if ($exe) { return $exe }
    }
    return $null
}

function Assert-SignatureOptional([string]$Message) {
    if ($env:ZENSU_REQUIRE_SIGNATURE -and $env:ZENSU_REQUIRE_SIGNATURE -ne '0') { throw $Message }
}

function Test-SignedRelease([string]$Ver) {
    $major = $Ver.Split('.')[0]
    $minor = if ($Ver.Contains('.')) { $Ver.Substring($Ver.IndexOf('.') + 1).Split('.')[0] } else { $Ver }
    if ($major -cnotmatch '\A[0-9]+\z' -or $minor -cnotmatch '\A[0-9]+\z') { return $true }
    $minor = $minor.TrimStart('0')
    return ($major -match '[1-9]') -or ($minor.Length -gt 2) -or (($minor.Length -gt 0) -and ([int]$minor -gt 18))
}

function Confirm-Signature([string]$Dir, [string]$Url, [string]$Version, [string]$Ver) {
    $sigFile = Join-Path $Dir 'checksums.txt.sig'
    $name = $Url.Substring($Url.LastIndexOf('/') + 1)
    if (-not (Invoke-Download $Url $sigFile)) {
        if (Test-SignedRelease $Ver) {
            throw "could not download the signature $name of release $Version; every zensu release after v0.18.0 is signed, so nothing is installed without it"
        }
        Assert-SignatureOptional "release $Version carries no signature ($name)"
        Write-Note "release $Version carries no signature, so only its checksums are checked"
        return
    }
    $openssl = Find-OpenSsl $Dir
    if (-not $openssl) {
        Assert-SignatureOptional 'verifying the release signature needs OpenSSL 3 or later with Ed25519 support; install it or set ZENSU_OPENSSL to its path'
        Write-Note 'OpenSSL 3 or later with Ed25519 support was not found, so the release signature was not checked; set ZENSU_REQUIRE_SIGNATURE=1 to require it'
        return
    }
    $pem = Join-Path $Dir 'release.pem'
    $sigBin = Join-Path $Dir 'checksums.sig.bin'
    $checksums = Join-Path $Dir 'checksums.txt'
    foreach ($key in Get-ReleaseKeys) {
        Write-PublicKey $pem $key
        foreach ($line in [IO.File]::ReadAllLines($sigFile)) {
            $sig = $line -replace '[ \t\r]', ''
            if (-not $sig) { continue }
            if (-not (Write-Signature $sigBin $sig)) { continue }
            if (Test-Signature $openssl $pem $checksums $sigBin) {
                Write-Host 'Verified the release signature.'
                return
            }
        }
    }
    throw "the signature of zensu_${Ver}_checksums.txt matches no zensu release key, so the download may have been tampered with"
}

function Confirm-Checksum([string]$Dir, [string]$File) {
    $want = $null
    foreach ($line in [IO.File]::ReadAllLines((Join-Path $Dir 'checksums.txt'))) {
        $fields = $line.Trim() -split '\s+'
        if ($fields.Count -ge 2 -and ($fields[1] -ceq $File -or $fields[1] -ceq "*$File")) {
            $want = $fields[0]
            break
        }
    }
    if (-not $want) { throw "no checksum entry for $File" }
    $got = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $Dir $File)).Hash
    if ($want -ne $got) { throw "checksum mismatch for $File" }
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

function Install-Zensu {
    $arch = Get-Arch
    $version = Resolve-Version
    $ver = $version.Substring(1)
    $releaseUrl = if ($env:ZENSU_RELEASE_URL) { $env:ZENSU_RELEASE_URL } else { "https://github.com/$Repo/releases/download" }
    if ($releaseUrl.EndsWith('/')) { $releaseUrl = $releaseUrl.Substring(0, $releaseUrl.Length - 1) }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("zensu-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $archive = "zensu_${ver}_windows_${arch}.zip"
        $checksums = "zensu_${ver}_checksums.txt"
        $base = "$releaseUrl/$version"
        Write-Host "Downloading $archive ($version) ..."
        Save-Download "$base/$checksums" (Join-Path $tmp 'checksums.txt')
        Confirm-Signature $tmp "$base/$checksums.sig" $version $ver
        Save-Download "$base/$archive" (Join-Path $tmp $archive)
        Confirm-Checksum $tmp $archive

        Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath $tmp -Force

        $dir = if ($env:ZENSU_INSTALL_DIR) { $env:ZENSU_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\zensu' }
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        Move-Item -Path (Join-Path $tmp $Bin) -Destination (Join-Path $dir $Bin) -Force
        Write-Host "Installed zensu to $(Join-Path $dir $Bin)"

        if (-not $env:ZENSU_NO_MODIFY_PATH -or $env:ZENSU_NO_MODIFY_PATH -eq '0') { Add-ToUserPath $dir }
    }
    finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
}

function Invoke-Installer {
    try {
        Install-Zensu
    } catch {
        if (-not $MyInvocation.ScriptName) { throw }
        [Console]::Error.WriteLine("error: $($_.Exception.Message)")
        exit 1
    }
}

Invoke-Installer
