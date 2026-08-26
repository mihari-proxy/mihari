# mihari all-in-one LOCAL installer for Windows (script 2). Offline: lays down
# the mihari binary + bundled mihomo core + GeoIP from <BundleDir> with zero
# network.
#   powershell -File install-aio.ps1 [-BundleDir <path>]   (default: script dir)
#
# Bundle layout (produced by scripts/build-all-in-one):
#   mihari.exe             -> $binDir\mihari.exe
#   data\bin\mihomo.exe    -> $MIHARI_DATA\bin\mihomo.exe       (overwrite)
#   data\bin\core-channel  -> $MIHARI_DATA\bin\core-channel     (overwrite if present)
#   data\geoip\*.mmdb      -> $MIHARI_DATA\geoip\*.mmdb         (overwrite)
#
# Never touches: mihari.yaml, subscriptions\, control.token, onboarding.json,
# logs\, web\ (user-private config and panel state stay intact).
#
# Environment overrides:
#   $env:MIHARI_BIN    mihari binary install dir (default %LOCALAPPDATA%\Programs\mihari)
#   $env:MIHARI_DATA   data root (default %USERPROFILE%\.mihari)
param([string]$BundleDir, [string]$Channel)
$ErrorActionPreference = 'Stop'

function Info($m) { Write-Host "* $m" -ForegroundColor Cyan }
function Fail($m) { Write-Host "error: $m" -ForegroundColor Red; throw $m }

if (-not $BundleDir) { $BundleDir = if ($PSScriptRoot) { $PSScriptRoot } else { (Get-Location).Path } }
if (-not $Channel -and $env:MIHARI_CHANNEL) { $Channel = $env:MIHARI_CHANNEL }
if ($Channel -and $Channel -cnotin @('main', 'dev')) { Fail 'mihari channel must be main or dev' }
# Invoke-MihariService runs a `mihari service ...` step, elevating via UAC when
# the current session is not admin (service control needs elevation on Windows).
function Invoke-MihariService([string[]]$ServiceArgs) {
  if ($isAdmin) {
    & $dest @ServiceArgs
  } else {
    Info ("提权执行: mihari " + ($ServiceArgs -join ' '))
    Start-Process -FilePath $dest -ArgumentList $ServiceArgs -Verb RunAs -Wait
  }
}

$mihariSrc = Join-Path $BundleDir 'mihari.exe'
$mihomoSrc = Join-Path $BundleDir 'data\bin\mihomo.exe'
if (-not (Test-Path -LiteralPath $mihariSrc)) { Fail "all-in-one bundle not found at $BundleDir (expected mihari.exe)" }
if (-not (Test-Path -LiteralPath $mihomoSrc)) { Fail "bundled mihomo core missing at $mihomoSrc" }
if (-not (Test-Path -LiteralPath (Join-Path $BundleDir 'data\geoip\GeoLite2-Country.mmdb'))) { Fail "bundled GeoIP Country missing at $BundleDir\data\geoip" }
if (-not (Test-Path -LiteralPath (Join-Path $BundleDir 'data\geoip\GeoLite2-ASN.mmdb'))) { Fail "bundled GeoIP ASN missing at $BundleDir\data\geoip" }

$binDir = if ($env:MIHARI_BIN) { $env:MIHARI_BIN } else { Join-Path $env:LOCALAPPDATA 'Programs\mihari' }
$dataDir = if ($env:MIHARI_DATA) { $env:MIHARI_DATA } else { Join-Path $env:USERPROFILE '.mihari' }
$dest = Join-Path $binDir 'mihari.exe'

New-Item -ItemType Directory -Force -Path $binDir | Out-Null

$isAdmin = $false
if ($env:MIHARI_INSTALL_TEST_MODE -ne '1') {
  $isAdmin = ([Security.Principal.WindowsPrincipal] `
    [Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
}

$svc = $null
if ($env:MIHARI_INSTALL_TEST_MODE -ne '1') {
  # A running service / foreground process holds an open handle on mihari.exe and
  # the MMDBs — stop both before overwriting (mirrors install.ps1's svcRunning
  # branch and adds the foreground-daemon lock the aio overlay introduces).
  $svc = Get-Service -Name 'mihari' -ErrorAction SilentlyContinue
  $svcRunning = $svc -and $svc.Status -eq 'Running'
  $proc = Get-Process -Name 'mihari' -ErrorAction SilentlyContinue
  $stopSteps = @()
  if ($svcRunning) { $stopSteps += 'Stop-Service -Name mihari -Force' }
  if ($proc) { $stopSteps += 'Stop-Process -Name mihari -Force -ErrorAction SilentlyContinue' }
  if ($stopSteps.Count -gt 0) {
    Info '检测到运行中的 mihari，停止以释放文件锁…'
    $stopCmd = $stopSteps -join '; '
    if ($isAdmin) {
      Invoke-Expression $stopCmd
    } else {
      Start-Process powershell -Verb RunAs -Wait -ArgumentList '-NoProfile', '-Command', $stopCmd
    }
    Start-Sleep -Seconds 1
  }
}

# 1. mihari binary -> binDir.
Info "安装 mihari 到 $dest"
Copy-Item -LiteralPath $mihariSrc -Destination $dest -Force

# Add install dir to the user PATH if missing (mirrors install.ps1).
if ($env:MIHARI_INSTALL_TEST_MODE -ne '1') {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if ($userPath -and $userPath -notlike "*$binDir*") {
    Info "将 $binDir 加入 PATH"
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$binDir", 'User')
    $env:Path = "$env:Path;$binDir"
  }
}

# 2. Data overlay -> MIHARI_DATA (bundle authoritative for core + GeoIP; user
#    config / panel state below is never touched).
New-Item -ItemType Directory -Force -Path (Join-Path $dataDir 'bin') | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $dataDir 'geoip') | Out-Null
Info "覆盖 mihomo 核心与 GeoIP 到 $dataDir"
Copy-Item -LiteralPath $mihomoSrc -Destination (Join-Path $dataDir 'bin\mihomo.exe') -Force
$sidecarSrc = Join-Path $BundleDir 'data\bin\core-channel'
if (Test-Path -LiteralPath $sidecarSrc) {
  Copy-Item -LiteralPath $sidecarSrc -Destination (Join-Path $dataDir 'bin\core-channel') -Force
}
Copy-Item -LiteralPath (Join-Path $BundleDir 'data\geoip\GeoLite2-Country.mmdb') -Destination (Join-Path $dataDir 'geoip\GeoLite2-Country.mmdb') -Force
Copy-Item -LiteralPath (Join-Path $BundleDir 'data\geoip\GeoLite2-ASN.mmdb') -Destination (Join-Path $dataDir 'geoip\GeoLite2-ASN.mmdb') -Force

if ($Channel) {
  $channelRoot = if ($env:MIHARI_DATA) { $env:MIHARI_DATA } else { Join-Path $env:USERPROFILE '.mihari' }
  New-Item -ItemType Directory -Force -Path $channelRoot | Out-Null
  $channelPath = Join-Path $channelRoot 'mihari-channel'
  $channelTmp = Join-Path $channelRoot ('.mihari-channel.tmp-' + [Guid]::NewGuid().ToString('N'))
  [IO.File]::WriteAllText($channelTmp, ($Channel + "`n"))
  Move-Item -LiteralPath $channelTmp -Destination $channelPath -Force
}

if ($env:MIHARI_INSTALL_TEST_MODE -eq '1') { return }

# 3. Service: reinstall when registered (re-stages the service copy from the
#    freshly installed PATH binary, closing the "service vs PATH" version drift),
#    install when fresh. Service control needs elevation (Invoke-MihariService).
if ($svc) {
  Info '已注册服务，执行 service reinstall 同步新版本…'
  Invoke-MihariService 'service', 'reinstall'
} else {
  Info '注册并启动服务…'
  Invoke-MihariService 'service', 'install'
  Invoke-MihariService 'service', 'start'
}

Write-Host "`n✅ aio 版安装完成！请重启终端，然后运行 mihari 开始使用。" -ForegroundColor Green
