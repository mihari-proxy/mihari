# mihari one-line installer for Windows (PowerShell).
#   irm https://raw.githubusercontent.com/mihari-proxy/mihari/main/scripts/install/install.ps1 | iex
# File invocation reads -Channel from $args. irm | iex uses $env:MIHARI_CHANNEL.
#
# Environment overrides:
#   $env:MIHARI_REPO        owner/repo (default mihari-proxy/mihari)
#   $env:MIHARI_BIN         install dir (default %LOCALAPPDATA%\Programs\mihari)
#   $env:MIHARI_VERSION     release tag to install (default: channel latest)
#   $env:MIHARI_CHANNEL     main|dev when -Channel is omitted
#   $env:MIHARI_NO_INSTALL  set to 1 to download only
$ErrorActionPreference = 'Stop'

function Info($m) { Write-Host "* $m" -ForegroundColor Cyan }
function Fail($m) { Write-Host "error: $m" -ForegroundColor Red; throw $m }

$channel = ''
$explicit = 0
$i = 0
while ($i -lt $args.Count) {
  $token = [string]$args[$i]
  if ($token -match '^(?i)-channel:(.+)$') {
    $channel = $Matches[1]
    $explicit = 1
    $i++
    continue
  }
  if ($token -match '^(?i)-channel$') {
    if ($i + 1 -ge $args.Count) { Fail 'missing -Channel value' }
    $channel = [string]$args[$i + 1]
    $explicit = 1
    $i += 2
    continue
  }
  Fail "unknown argument: $token"
}
if ($explicit -eq 0 -and $env:MIHARI_CHANNEL) {
  $channel = $env:MIHARI_CHANNEL
  $explicit = 1
}
if ($channel -and $channel -cnotin @('main', 'dev')) {
  Fail 'mihari channel must be main or dev'
}

$repo = if ($env:MIHARI_REPO) { $env:MIHARI_REPO } else { 'mihari-proxy/mihari' }
$binDir = if ($env:MIHARI_BIN) { $env:MIHARI_BIN } else { Join-Path $env:LOCALAPPDATA 'Programs\mihari' }
$githubApi = if ($env:MIHARI_GITHUB_API) { $env:MIHARI_GITHUB_API.TrimEnd('/') } else { 'https://api.github.com' }

function Test-CanonicalDev([string]$tag) {
  return [bool]($tag -cmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$')
}

function Parse-Canonical([string]$tag) {
  if ($tag -cmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$') {
    return @{ Major = [int]$Matches[1]; Minor = [int]$Matches[2]; Patch = [int]$Matches[3]; Dev = [int]$Matches[4]; IsDev = $true }
  }
  if ($tag -cmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
    return @{ Major = [int]$Matches[1]; Minor = [int]$Matches[2]; Patch = [int]$Matches[3]; Dev = 0; IsDev = $false }
  }
  return $null
}

function Compare-Canonical([string]$left, [string]$right) {
  $a = Parse-Canonical $left
  $b = Parse-Canonical $right
  if (-not $a -or -not $b) { return 0 }
  if ($a.Major -ne $b.Major) { return [Math]::Sign($a.Major - $b.Major) }
  if ($a.Minor -ne $b.Minor) { return [Math]::Sign($a.Minor - $b.Minor) }
  if ($a.Patch -ne $b.Patch) { return [Math]::Sign($a.Patch - $b.Patch) }
  if ($a.IsDev -ne $b.IsDev) { if ($a.IsDev) { return -1 } else { return 1 } }
  if ($a.IsDev) { return [Math]::Sign($a.Dev - $b.Dev) }
  return 0
}

function Get-NextReleaseLink($headers) {
  $values = @()
  foreach ($key in @('Link', 'link')) {
    if ($headers[$key]) { $values += $headers[$key] }
  }
  foreach ($value in $values) {
    foreach ($part in (([string]$value) -split ',')) {
      if ($part -match 'rel\s*=\s*"next"' -and $part -match '<([^>]+)>') {
        return $Matches[1]
      }
    }
  }
  return $null
}

function Resolve-DevTag {
  $url = "$githubApi/repos/$repo/releases?per_page=100"
  $best = $null
  for ($page = 0; $page -lt 5; $page++) {
    try {
      $req = [Net.HttpWebRequest]::Create($url)
      $req.Method = 'GET'
      $req.UserAgent = 'mihari'
      $req.Accept = 'application/vnd.github+json'
      $req.KeepAlive = $false
      $req.ServicePoint.Expect100Continue = $false
      $resp = $req.GetResponse()
    } catch {
      Fail 'failed to list GitHub Releases; set MIHARI_VERSION=vX.Y.Z-dev.N'
    }
    try {
      $headers = @{}
      foreach ($name in $resp.Headers.AllKeys) { $headers[$name] = $resp.Headers[$name] }
      $reader = New-Object IO.StreamReader($resp.GetResponseStream())
      $body = $reader.ReadToEnd()
      $reader.Close()
      if ([Text.Encoding]::UTF8.GetByteCount($body) -gt 8MB) {
        Fail 'mihari release list is too large; set MIHARI_VERSION=vX.Y.Z-dev.N'
      }
    } finally {
      if ($resp) { $resp.Close() }
    }
    $releases = @()
    try {
      # PS 5.1 flattens a top-level JSON array into one object. Wrapping
      # preserves per-release tag_name / draft on nested GitHub payloads.
      $parsed = ConvertFrom-Json -InputObject ('{"items":' + $body + '}')
      if ($null -ne $parsed.items) { $releases = @($parsed.items) }
    } catch {
      try { $releases = @($body | ConvertFrom-Json) } catch { $releases = @() }
    }
    foreach ($rel in $releases) {
      if ($rel.draft) { continue }
      $tag = [string]$rel.tag_name
      if (-not $tag) { $tag = [string]$rel.tagName }
      if ($tag -match ' ') { continue }
      if (-not (Test-CanonicalDev $tag)) { continue }
      if (-not $best -or (Compare-Canonical $tag $best) -gt 0) { $best = $tag }
    }
    if (-not $best) {
      $tagHits = [regex]::Matches([string]$body, '"tag_name"\s*:\s*"([^"]*)"')
      for ($i = 0; $i -lt $tagHits.Count; $i++) {
        $tag = $tagHits[$i].Groups[1].Value
        $start = $tagHits[$i].Index
        $end = if ($i + 1 -lt $tagHits.Count) { $tagHits[$i + 1].Index } else { $body.Length }
        $slice = $body.Substring($start, $end - $start)
        if ($slice -match '"draft"\s*:\s*true') { continue }
        if (-not (Test-CanonicalDev $tag)) { continue }
        if (-not $best -or (Compare-Canonical $tag $best) -gt 0) { $best = $tag }
      }
    }
    $next = Get-NextReleaseLink $headers
    if (-not $next) { break }
    $url = $next
  }
  if (-not $best) { Fail 'no canonical mihari dev release; set MIHARI_VERSION=vX.Y.Z-dev.N' }
  return $best
}

function Write-MihariChannel([string]$name) {
  $root = if ($env:MIHARI_DATA) { $env:MIHARI_DATA } else { Join-Path $env:USERPROFILE '.mihari' }
  New-Item -ItemType Directory -Force -Path $root | Out-Null
  $path = Join-Path $root 'mihari-channel'
  $tmp = Join-Path $root ('.mihari-channel.tmp-' + [Guid]::NewGuid().ToString('N'))
  [IO.File]::WriteAllText($tmp, ($name + "`n"))
  Move-Item -LiteralPath $tmp -Destination $path -Force
}

# Detect architecture.
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { throw "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
}

# Asset names carry no version (mihari-<os>-<arch>[.exe]), so the stable
# /releases/latest/download/ path works for the default case.
$asset = "mihari-windows-$arch.exe"
$targetTag = [string]$env:MIHARI_VERSION
if ($env:MIHARI_VERSION) {
  $url = "https://github.com/$repo/releases/download/$($env:MIHARI_VERSION)/$asset"
} elseif ($channel -eq 'dev') {
  $targetTag = Resolve-DevTag
  $url = "https://github.com/$repo/releases/download/$targetTag/$asset"
} else {
  $url = "https://github.com/$repo/releases/latest/download/$asset"
}

$installed = [string]$env:MIHARI_TEST_INSTALLED_VERSION
if (-not $installed -and $env:MIHARI_INSTALL_TEST_MODE -ne '1') {
  $existing = Join-Path $binDir 'mihari.exe'
  if (Test-Path -LiteralPath $existing) {
    try { $installed = [string](& $existing self version 2>$null) } catch { $installed = '' }
  }
}

$downgrade = 0
$installedTag = $installed
$compareTag = $targetTag
if ($installedTag -and $installedTag -notmatch '^v') { $installedTag = 'v' + $installedTag }
if ($compareTag -and $compareTag -notmatch '^v') { $compareTag = 'v' + $compareTag }
if ($installedTag -and $compareTag -and (Compare-Canonical $installedTag $compareTag) -gt 0) {
  $downgrade = 1
}

if ($env:MIHARI_INSTALL_TEST_MODE -eq '1') {
  if ($explicit -eq 1) { Write-MihariChannel $channel }
  Write-Output "CHANNEL=$channel"
  Write-Output "EXPLICIT=$explicit"
  Write-Output "URL=$url"
  Write-Output "TARGET_TAG=$targetTag"
  Write-Output "INSTALLED=$installed"
  Write-Output "DOWNGRADE=$downgrade"
  return
}

if ($downgrade -eq 1) {
  Write-Host "* Installing older Mihari $targetTag over $installed." -ForegroundColor Yellow
  Write-Host "* Settings, subscriptions, and generated files from the current version may be unsupported, fail to load, or look like they disappeared." -ForegroundColor Yellow
  Write-Host "* Downgrade is not a supported config migration and does not roll disk state back." -ForegroundColor Yellow
  if ($env:MIHARI_YES -ne '1') {
    $ans = Read-Host "  Continue with downgrade? [y/N]"
    if (-not $ans -or $ans -notmatch '^[Yy]') {
      Info "Cancelled, no changes made."
      return
    }
  }
}

$dest = Join-Path $binDir 'mihari.exe'

New-Item -ItemType Directory -Force -Path $binDir | Out-Null

$isAdmin = ([Security.Principal.WindowsPrincipal] `
  [Security.Principal.WindowsIdentity]::GetCurrent()
  ).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)

# Inspect any existing install so we can upgrade in place. A running service
# holds an open handle on mihari.exe, so it must be stopped before the file
# can be replaced.
$svc = Get-Service -Name 'mihari' -ErrorAction SilentlyContinue
$svcRunning = $svc -and $svc.Status -eq 'Running'

if ($svcRunning) {
  Write-Host "* 检测到 mihari 服务正在运行。" -ForegroundColor Yellow
  $ans = Read-Host "  停止服务、更新 exe 并重启？[Y/n]"
  if ($ans -and $ans -notmatch '^[Yy]') {
    Info "已取消，未做任何更改。"
    return
  }
}

# Download to a temp file first so a running mihari.exe can't block the
# download, then swap it into place.
$tmp = Join-Path $binDir ("mihari.exe.new-" + [Guid]::NewGuid().ToString('N'))
Info "Downloading $asset from $repo..."
try {
  Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing

  # Add install dir to the user PATH if missing.
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if ($userPath -notlike "*$binDir*") {
    Info "Adding $binDir to your PATH"
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$binDir", 'User')
    $env:Path = "$env:Path;$binDir"
  }

  if ($svcRunning) {
    # Upgrade a running service: stop -> replace exe -> restart. Service
    # control needs elevation, so bundle all three into a single elevated step.
    Info "Stopping service, updating exe, and restarting..."
    $swap = "Stop-Service -Name mihari -Force; " +
            "Move-Item -LiteralPath '$tmp' -Destination '$dest' -Force; " +
            "Start-Service -Name mihari"
    if ($isAdmin) {
      Invoke-Expression $swap
    } else {
      Info "Elevating to restart the Windows service..."
      Start-Process powershell -Verb RunAs -Wait -ArgumentList '-NoProfile', '-Command', $swap
    }
    if ($explicit -eq 1) { Write-MihariChannel $channel }
    Write-Host "`n[OK] Updated. Manage with: mihari status | mihari sub add <url>" -ForegroundColor Green
    return
  }

  # Not running (fresh install, or service stopped): just put the exe in place.
  Move-Item -LiteralPath $tmp -Destination $dest -Force
  if ($explicit -eq 1) { Write-MihariChannel $channel }
} catch {
  if (Test-Path $tmp) { Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue }
  throw
}

if ($svc) {
  # Service already registered and pointing at the same path; the new exe is in
  # place, nothing else to do.
  Write-Host "`n[OK] Updated. Manage with: mihari status | mihari sub add <url>" -ForegroundColor Green
  return
}

if ($env:MIHARI_NO_INSTALL -eq '1') {
  Info "Downloaded. Run: mihari daemon (or: mihari service install && mihari service start)"
  return
}

Info "Registering the mihari service..."
# Installing a Windows service requires elevation.
if ($isAdmin) {
  & $dest service install
  & $dest service start
} else {
  Info "Elevating to register the Windows service..."
  Start-Process -FilePath $dest -ArgumentList 'service install' -Verb RunAs -Wait
  Start-Process -FilePath $dest -ArgumentList 'service start' -Verb RunAs -Wait
}

Write-Host "`n[OK] Done. Manage with: mihari status | mihari sub add <url>" -ForegroundColor Green
