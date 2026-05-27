# triagent install script (Windows / PowerShell)
# Usage: irm https://sourcehawk.github.io/triagent/install.ps1 | iex
#
# Env vars:
#   $env:TRIAGENT_VERSION       (default: latest)
#   $env:TRIAGENT_INSTALL_DIR   (default: $env:LOCALAPPDATA\Programs\triagent)
#   $env:TRIAGENT_BASE_URL      override download root for testing

$ErrorActionPreference = 'Stop'
# Defensive TLS pin for older PowerShell hosts (PS 5.1 on stock Windows).
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo       = 'sourcehawk/triagent'
$installDir = if ($env:TRIAGENT_INSTALL_DIR) { $env:TRIAGENT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\triagent' }
$baseUrl    = if ($env:TRIAGENT_BASE_URL)    { $env:TRIAGENT_BASE_URL }    else { "https://github.com/$repo/releases/download" }

function Die([string]$msg)  { Write-Host "triagent: $msg" -ForegroundColor Red; exit 1 }
function Info([string]$msg) { Write-Host "triagent: $msg" }

# --- detect arch (Windows: amd64 only in v1) --------------------------------
if (-not [Environment]::Is64BitOperatingSystem) {
  Die 'only 64-bit Windows is supported — see https://github.com/sourcehawk/triagent/releases'
}
$arch = 'amd64'

# --- resolve version --------------------------------------------------------
$version = $env:TRIAGENT_VERSION
if (-not $version) {
  Info 'resolving latest release...'
  try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -UseBasicParsing
    $version = $release.tag_name
  } catch {
    Die "failed to query GitHub API (rate-limited?) — set `$env:TRIAGENT_VERSION=vX.Y.Z to skip"
  }
}

Info "installing $version for windows/$arch to $installDir"

# --- download archive + checksums ------------------------------------------
$trimV   = $version -replace '^v', ''
$archive = "triagent_${trimV}_windows_${arch}.zip"
$tmpdir  = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "triagent-install-$([guid]::NewGuid())")
try {
  Info "downloading $archive..."
  Invoke-WebRequest -Uri "$baseUrl/$version/$archive"      -OutFile (Join-Path $tmpdir $archive)        -UseBasicParsing
  Invoke-WebRequest -Uri "$baseUrl/$version/checksums.txt" -OutFile (Join-Path $tmpdir 'checksums.txt') -UseBasicParsing

  # --- verify checksum ----------------------------------------------------
  Info 'verifying checksum...'
  $entry = (Get-Content (Join-Path $tmpdir 'checksums.txt')) | Where-Object { $_ -match " $([regex]::Escape($archive))$" }
  if (-not $entry) { Die "no checksum entry for $archive" }
  $expected = ($entry -split '\s+')[0].ToLower()
  $actual   = (Get-FileHash -Algorithm SHA256 (Join-Path $tmpdir $archive)).Hash.ToLower()
  if ($expected -ne $actual) { Die "checksum mismatch (expected $expected, got $actual)" }

  # --- extract ------------------------------------------------------------
  Info 'extracting...'
  Expand-Archive -Path (Join-Path $tmpdir $archive) -DestinationPath $tmpdir -Force

  # --- install ------------------------------------------------------------
  New-Item -ItemType Directory -Path $installDir -Force | Out-Null
  Copy-Item -Path (Join-Path $tmpdir 'triagent.exe')     -Destination (Join-Path $installDir 'triagent.exe')     -Force
  Copy-Item -Path (Join-Path $tmpdir 'triagent-mcp.exe') -Destination (Join-Path $installDir 'triagent-mcp.exe') -Force
}
finally {
  Remove-Item -Recurse -Force $tmpdir -ErrorAction SilentlyContinue
}

# --- PATH update -----------------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$installDir*") {
  $newPath = if ($userPath) { "$userPath;$installDir" } else { $installDir }
  [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
  Info "added $installDir to user PATH. Open a new terminal for the change to take effect."
}

Write-Host ''
Info 'installed. Run: triagent start'
