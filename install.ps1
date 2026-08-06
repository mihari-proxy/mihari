# mihari one-line installer for Windows (PowerShell).
#   irm https://raw.githubusercontent.com/LeeShunEE/mihari/main/install.ps1 | iex
#
# Environment overrides:
#   $env:MIHARI_REPO        owner/repo (default LeeShunEE/mihari)
#   $env:MIHARI_BIN         install dir (default %LOCALAPPDATA%\Programs\mihari)
#   $env:MIHARI_VERSION     release tag to install (default: latest)
#   $env:MIHARI_NO_INSTALL  set to 1 to download only
$ErrorActionPreference = 'Stop'

$repo = if ($env:MIHARI_REPO) { $env:MIHARI_REPO } else { 'LeeShunEE/mihari' }
$binDir = if ($env:MIHARI_BIN) { $env:MIHARI_BIN } else { Join-Path $env:LOCALAPPDATA 'Programs\mihari' }

function Info($m) { Write-Host "* $m" -ForegroundColor Cyan }

# Detect architecture.
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { throw "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
}

# Asset names carry no version (mihari-<os>-<arch>[.exe]), so the stable
# /releases/latest/download/ path works for the default case.
$asset = "mihari-windows-$arch.exe"
if ($env:MIHARI_VERSION) {
  $url = "https://github.com/$repo/releases/download/$($env:MIHARI_VERSION)/$asset"
} else {
  $url = "https://github.com/$repo/releases/latest/download/$asset"
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
    Write-Host "`n[OK] Updated. Manage with: mihari status | mihari sub add <url>" -ForegroundColor Green
    return
  }

  # Not running (fresh install, or service stopped): just put the exe in place.
  Move-Item -LiteralPath $tmp -Destination $dest -Force
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
