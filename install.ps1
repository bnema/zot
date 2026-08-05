# zut installer for Windows (PowerShell).
#
# Usage (in PowerShell):
#   iwr -useb https://raw.githubusercontent.com/bnema/zut/main/install.ps1 | iex
#
# Or with arguments:
#   $env:ZUT_VERSION = "v0.1.0"
#   $env:ZUT_PREFIX  = "$HOME\bin"
#   iwr -useb https://raw.githubusercontent.com/bnema/zut/main/install.ps1 | iex
#
# Detects architecture, downloads the matching .zip from the GitHub
# release, verifies the sha256 against checksums.txt, extracts zut.exe,
# and moves it into $ZUT_PREFIX (defaults to $HOME\bin, added to PATH
# via the User environment if missing).
#
# $env:GITHUB_TOKEN is optional for the public repo. Set it to a PAT
# with `contents:read` scope if you hit GitHub API rate limits (or if
# you are installing from a private fork); the script then uses it for
# the version lookup and every download.


[CmdletBinding()]
param(
  [string]$Version = $env:ZUT_VERSION,
  [string]$Prefix  = $env:ZUT_PREFIX
)

$ErrorActionPreference = "Stop"

$owner  = "bnema"
$repo   = "zut"
$binary = "zut"

# Build Authorization header list once; used on every HTTP call so the
# script works against private repos when $env:GITHUB_TOKEN is set.
$headers = @{}
if ($env:GITHUB_TOKEN) { $headers["Authorization"] = "Bearer $($env:GITHUB_TOKEN)" }

if (-not $Version) { $Version = "latest" }
if (-not $Prefix)  { $Prefix  = Join-Path $HOME "bin" }

function Msg($m)  { Write-Host "==> $m" -ForegroundColor Cyan }
function Warn($m) { Write-Warning $m }
function Die($m)  { Write-Error $m; exit 1 }

# ---- detect architecture ----

switch -wildcard ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { $arch = "amd64" }
  "ARM64" { $arch = "arm64" }
  default { Die "unsupported arch: $($env:PROCESSOR_ARCHITECTURE)" }
}

# ARM64 Windows isn't shipped (see .goreleaser.yaml ignore rule) — fall
# back to amd64 which runs fine under ARM64 emulation.
if ($arch -eq "arm64") {
  Warn "windows/arm64 is not published; falling back to amd64"
  $arch = "amd64"
}

# ---- resolve version ----
#
# Resolve "latest" through the GitHub releases API. This works the same
# on Windows PowerShell 5.1 and PowerShell 7+, unlike scraping the
# /releases/latest redirect target: on PS7 the final URL lives at
# $resp.BaseResponse.RequestMessage.RequestUri while on PS5.1 it is
# $resp.BaseResponse.ResponseUri, and relying on either breaks on the
# other runtime. The API returns the tag directly, so there is nothing
# to scrape.

if ($Version -eq "latest") {
  $apiUrl = "https://api.github.com/repos/$owner/$repo/releases/latest"
  # GitHub's API wants a User-Agent; Invoke-RestMethod sets one, but be
  # explicit so corporate proxies that strip it don't trip a 403.
  $apiHeaders = @{} + $headers
  if (-not $apiHeaders.ContainsKey("User-Agent")) { $apiHeaders["User-Agent"] = "zut-installer" }
  $apiHeaders["Accept"] = "application/vnd.github+json"

  try {
    $api = Invoke-RestMethod -UseBasicParsing -Headers $apiHeaders -Uri $apiUrl
  } catch {
    $status = $null
    try { $status = [int]$_.Exception.Response.StatusCode } catch {}
    if ($status -eq 404) {
      Die "no published release found for $owner/$repo (the repo may have no releases yet)"
    } elseif ($status -eq 401 -or $status -eq 403) {
      Die "GitHub API request was rejected ($status). If the repo is private, set `$env:GITHUB_TOKEN to a PAT with contents:read; otherwise you may be rate-limited (try again later or set `$env:GITHUB_TOKEN)."
    } else {
      Die "could not resolve latest version: $($_.Exception.Message)"
    }
  }

  $Version = $api.tag_name
  if (-not $Version) {
    Die "could not resolve latest version: GitHub API returned no tag_name for $owner/$repo"
  }
}

if (-not $Version.StartsWith("v")) { $Version = "v$Version" }
$verNum = $Version.TrimStart("v")

# ---- download + verify + extract ----

$archive     = "${binary}_${verNum}_windows_${arch}.zip"
$baseUrl     = "https://github.com/$owner/$repo/releases/download/$Version"
$archiveUrl  = "$baseUrl/$archive"
$checksumUrl = "$baseUrl/checksums.txt"

$tmp = New-Item -ItemType Directory -Path (Join-Path $env:TEMP ("zut-install-" + [System.Guid]::NewGuid().ToString("N").Substring(0,8)))

try {
  Msg "downloading $archive"
  Invoke-WebRequest -UseBasicParsing -Headers $headers -Uri $archiveUrl -OutFile (Join-Path $tmp $archive)

  Msg "verifying checksum"
  $checksumFile = Join-Path $tmp "checksums.txt"
  Invoke-WebRequest -UseBasicParsing -Headers $headers -Uri $checksumUrl -OutFile $checksumFile
  $expected = Get-Content -LiteralPath $checksumFile | ForEach-Object {
    $line = $_.Trim()
    if ($line) {
      $parts = $line -split "\s+"
      if ($parts.Count -ge 2 -and $parts[($parts.Count - 1)] -eq $archive) { $line }
    }
  } | Select-Object -First 1
  if (-not $expected) { Die "no checksum for $archive in checksums.txt" }
  $expectedHash = ($expected -split "\s+")[0]

  $actualHash = (Get-FileHash -Path (Join-Path $tmp $archive) -Algorithm SHA256).Hash.ToLower()
  if ($expectedHash.ToLower() -ne $actualHash) {
    Die "checksum mismatch: expected $expectedHash, got $actualHash"
  }

  Msg "extracting"
  Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath $tmp -Force

  $exe = Join-Path $tmp "$binary.exe"
  if (-not (Test-Path $exe)) { Die "archive did not contain $binary.exe" }

  Msg "installing to $Prefix\$binary.exe"
  New-Item -ItemType Directory -Path $Prefix -Force | Out-Null
  Copy-Item $exe (Join-Path $Prefix "$binary.exe") -Force

  # ---- PATH hint ----

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  $parts = $userPath -split ";" | Where-Object { $_ }
  if (-not ($parts -contains $Prefix)) {
    Warn "$Prefix is not on your user PATH"
    Warn "adding it for future sessions..."
    [Environment]::SetEnvironmentVariable("Path", ($userPath.TrimEnd(";") + ";" + $Prefix), "User")
    Warn "open a new terminal to pick up the change, or run:"
    Warn "  `$env:Path = `"$Prefix;`$env:Path`""
  }

  Msg "installed. run:  zut --help"
}
finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
