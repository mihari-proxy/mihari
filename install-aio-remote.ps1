# mihari all-in-one REMOTE downloader for Windows (script 3). Fetches the
# platform bundle from the AList drive (index.txt -> public direct link +
# sha256), verifies it, extracts, and hands off to the local installer
# (script 2) inside the bundle.
#   & ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1)))
#   & ([scriptblock]::Create((irm https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/install-aio-remote.ps1))) -Yes
#
# One-time: only the first offline install goes through this downloader; later
# reinstalls run script 2 (install-aio.ps1) directly from an existing bundle.
#
# Environment overrides:
#   $env:MIHARI_INDEX_URL   index.txt public direct link (default: the fixed public URL below)
#   $env:MIHARI_BUNDLE_URL  explicit bundle URL (skips index + sha256)
param([switch]$Yes)
$ErrorActionPreference = 'Stop'

# Fixed public direct link to the root index.txt. mihari distribution is fully
# public (signing disabled on the AList drive), so this URL is stable and
# identical across releases — copy-paste, never hand-edit. The release workflow
# uploads index.txt to this exact path each publish.
$indexUrl = if ($env:MIHARI_INDEX_URL) { $env:MIHARI_INDEX_URL } else { 'https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt' }
$bundleUrl = $env:MIHARI_BUNDLE_URL

function Info($m) { Write-Host "* $m" -ForegroundColor Cyan }
function Fail($m) { Write-Host "error: $m" -ForegroundColor Red; exit 1 }
function Confirm($p) {
  # Read-Host reads the host console (not the stdin pipe), so no /dev/tty
  # special-casing is needed (design 4.4 step 2).
  if ($Yes) { return $true }
  $ans = Read-Host "$p [y/N]"
  return $ans -match '^[Yy]'
}

# Detect platform (mirrors install.ps1).
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { Fail "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
}
$platform = "windows-$arch"

# Resolve bundle URL + expected sha256 + latest version.
$latest = ''; $wantSum = ''; $resolvedUrl = $bundleUrl
if (-not $resolvedUrl) {
  # index.txt line format: "<key> <rest...>". key="latest" -> <version>;
  # key="<goos>-<goarch>" -> <public_url> <sha256>.
  $index = ''
  try { $index = (Invoke-WebRequest -Uri $indexUrl -UseBasicParsing).Content } catch { $index = '' }
  if (-not $index) { Fail "尚未发布完成：无法获取 index（请稍后重试，或检查网络/网盘可用性）。" }
  foreach ($line in ($index -split "`n")) {
    $line = $line.Trim()
    if (-not $line -or $line.StartsWith('#') -or $line.StartsWith('//')) { continue }
    $fields = $line -split '\s+'
    if ($fields[0] -eq 'latest') { $latest = $fields[1] }
    elseif ($fields[0] -eq $platform) { $resolvedUrl = $fields[1]; $wantSum = $fields[2] }
  }
  if (-not $latest) { Fail "尚未发布完成：index 无 latest 版本（可能正在发布或已撤回）。" }
  if (-not $resolvedUrl) { Fail "index 未包含本平台 $platform 的包。" }
}

# Version judgment: PATH mihari only, local (no daemon). Single source of truth
# vs index.latest; empty -> unknown. (Equality-only compare: == latest ->
# "reinstall", != latest -> "upgrade" with honest versions shown. Full semver is
# not needed since judgment only informs the prompt, never gates the install.)
$haveMihari = $false; $current = ''
$mihariCmd = Get-Command mihari -ErrorAction SilentlyContinue
if ($mihariCmd) {
  $haveMihari = $true
  try {
    $vobj = (& mihari self version --json) | Out-String | ConvertFrom-Json
    $current = $vobj.version
  } catch { $current = '' }
}

if (-not $haveMihari) {
  Info ("未检测到 mihari，安装最新版" + $(if ($latest) { " ($latest)" }) + "…")
} elseif (-not $current) {
  Info '检测到 mihari 但版本未知（二进制可能损坏）。'
  if (-not (Confirm '  重新安装（修复）？')) { Info '已取消。'; exit 0 }
} elseif ($latest -and $current -eq $latest) {
  Info "已是最新版本 ($current)。"
  if (-not (Confirm '  重新安装（用于修复）？')) { Info '已取消。'; exit 0 }
} else {
  Info ("当前已安装 $current" + $(if ($latest) { "，最新版本为 $latest" }) + "。")
  if (-not (Confirm '  安装？')) { Info '已取消。'; exit 0 }
}

# Download to a temp file (outside the work dir) so the work dir can be fully
# cleared before extraction — PS 5.1 Expand-Archive does not reliably overwrite
# existing files (design 4.4 step 4).
$workdir = Join-Path $env:USERPROFILE 'Downloads\mihari-aio'
New-Item -ItemType Directory -Force -Path $workdir | Out-Null
$tmpArchive = Join-Path ([IO.Path]::GetTempPath()) ("mihari-aio-" + ([guid]::NewGuid().ToString('N')) + ".zip")
Info "下载 $resolvedUrl …"
Invoke-WebRequest -Uri $resolvedUrl -OutFile $tmpArchive -UseBasicParsing
if ($wantSum) {
  $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $tmpArchive).Hash.ToLower()
  if ($got -ne $wantSum.ToLower()) { Remove-Item -LiteralPath $tmpArchive -Force; Fail "sha256 校验失败：期望 $wantSum，实际 $got。" }
  Info 'sha256 校验通过。'
}
Info "解压到 $workdir …"
if (Test-Path -LiteralPath $workdir) { Get-ChildItem -LiteralPath $workdir | Remove-Item -Recurse -Force }
Expand-Archive -LiteralPath $tmpArchive -DestinationPath $workdir -Force
Remove-Item -LiteralPath $tmpArchive -Force

# Hand off to the local installer inside the bundle (script 2) via a scriptblock
# that reads the file content as a string — bypassing ExecutionPolicy and Mark of
# the Web; the bundle dir is injected via -BundleDir (design 4.4 step 5).
$localInstaller = Join-Path $workdir 'install-aio.ps1'
if (-not (Test-Path -LiteralPath $localInstaller)) { Fail '包内缺少 install-aio.ps1。' }
& ([scriptblock]::Create([IO.File]::ReadAllText($localInstaller))) -BundleDir $workdir
