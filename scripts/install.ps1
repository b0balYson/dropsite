# Install the dropsite CLI on Windows from GitHub Releases, without admin rights.
#
#   irm https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.ps1 | iex
#
# Environment:
#   DROPSITE_VERSION        release tag to install (default: latest)
#   DROPSITE_INSTALL_DIR    target directory (default: %LOCALAPPDATA%\Programs\dropsite)
#   DROPSITE_DOWNLOAD_BASE  where binaries and SHA256SUMS live (overrides the GitHub URL)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Repo = 'b0balYson/dropsite'
$Name = 'dropsite'

function Get-Arch {
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    switch ($arch) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { throw "install: unsupported architecture: $arch" }
    }
}

function Get-DownloadBase {
    if ($env:DROPSITE_DOWNLOAD_BASE) { return $env:DROPSITE_DOWNLOAD_BASE.TrimEnd('/') }
    if ($env:DROPSITE_VERSION -and $env:DROPSITE_VERSION -ne 'latest') {
        return "https://github.com/$Repo/releases/download/$($env:DROPSITE_VERSION)"
    }
    return "https://github.com/$Repo/releases/latest/download"
}

function Get-ExpectedHash([string]$SumsFile, [string]$Asset) {
    foreach ($line in Get-Content -LiteralPath $SumsFile) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -eq 2 -and $parts[1].TrimStart('*') -eq $Asset) { return $parts[0].ToLower() }
    }
    throw "install: no checksum for $Asset in SHA256SUMS"
}

function Add-ToUserPath([string]$Dir) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @()
    if ($userPath) { $entries = $userPath -split ';' | Where-Object { $_ } }
    if ($entries -contains $Dir) { return }
    [Environment]::SetEnvironmentVariable('Path', (($entries + $Dir) -join ';'), 'User')
    $env:Path = "$env:Path;$Dir"
    Write-Host "Added $Dir to your user PATH (open a new terminal to pick it up)."
}

$asset = "$Name-windows-$(Get-Arch).exe"
$base = Get-DownloadBase
$dir = if ($env:DROPSITE_INSTALL_DIR) { $env:DROPSITE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\$Name" }
$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    Write-Host "Downloading $asset from $base"
    $bin = Join-Path $tmp $asset
    $sums = Join-Path $tmp 'SHA256SUMS'
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $bin
    Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sums

    $expected = Get-ExpectedHash $sums $asset
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $bin).Hash.ToLower()
    if ($expected -ne $actual) { throw "install: checksum mismatch for ${asset}: expected $expected, got $actual" }

    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $target = Join-Path $dir "$Name.exe"
    Move-Item -Force -LiteralPath $bin -Destination $target
    Write-Host "Installed $(& $target version) to $target"
    Add-ToUserPath $dir
    Write-Host "Next: $Name login --server https://api.<your-domain> --token -"
}
finally {
    Remove-Item -Recurse -Force -LiteralPath $tmp -ErrorAction SilentlyContinue
}
