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
param([switch]$Yes, [string]$Channel)
$ErrorActionPreference = 'Stop'

# Fixed public direct links. mihari distribution is fully public (signing
# disabled on the AList drive), so these URLs are stable and identical across
# releases — copy-paste, never hand-edit. The downloader itself is always taken
# from the stable root; --channel dev only selects the dev index.
$stableIndexUrl = 'https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt'
$devIndexUrl = 'https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt'
if (-not $Channel -and $env:MIHARI_CHANNEL) { $Channel = $env:MIHARI_CHANNEL }
if ($Channel -and $Channel -cnotin @('main', 'dev')) { throw 'mihari channel must be main or dev' }
if ($env:MIHARI_INDEX_URL) {
  $indexUrl = $env:MIHARI_INDEX_URL
} elseif ($Channel -eq 'dev') {
  $indexUrl = $devIndexUrl
} else {
  $indexUrl = $stableIndexUrl
}
$bundleUrl = $env:MIHARI_BUNDLE_URL

# PS 5.1's irm decodes UTF-8 bytes from the signless octet-stream response as
# ISO-8859-1, so Chinese string literals turn into mojibake (code points 128-255)
# before the scriptblock runs. Detect that and round-trip the bytes back through
# Latin-1 -> UTF-8 to recover the text. No-op under ReadAllText / -File / PS 7.
function FixEncoding($s) {
  if (-not $s) { return $s }
  $mojibake = $false
  foreach ($c in [char[]]$s) { $cp = [int]$c; if ($cp -ge 128 -and $cp -le 255) { $mojibake = $true; break } }
  if (-not $mojibake) { return $s }
  return [System.Text.Encoding]::UTF8.GetString([System.Text.Encoding]::GetEncoding('ISO-8859-1').GetBytes($s))
}
function Info($m) { Write-Host ("* " + (FixEncoding $m)) -ForegroundColor Cyan }
function Fail($m) { $f = FixEncoding $m; Write-Host ("error: " + $f) -ForegroundColor Red; throw $f }
function Confirm($p) {
  # Read-Host reads the host console (not the stdin pipe), so no /dev/tty
  # special-casing is needed (design 4.4 step 2).
  if ($Yes) { return $true }
  $ans = Read-Host ((FixEncoding $p) + ' [y/N]')
  return $ans -match '^[Yy]'
}
# Stream one response to disk with progress. This is also the compatibility
# fallback when the origin does not provide a reliable byte-range contract.
function Download-SingleFileWithProgress($url, $dest) {
  Add-Type -AssemblyName System.Net.Http
  $client = New-Object System.Net.Http.HttpClient
  $client.Timeout = [TimeSpan]::FromMinutes(30)
  try {
    $resp = $client.GetAsync($url, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
    $resp.EnsureSuccessStatusCode() | Out-Null
    $total = 0L
    [long]::TryParse([string]$resp.Content.Headers.ContentLength, [ref]$total) | Out-Null
    $inStream = $resp.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
    $outStream = [IO.File]::Create($dest)
    try {
      $buffer = New-Object byte[] 81920
      $read = 0L
      while (($n = $inStream.Read($buffer, 0, $buffer.Length)) -gt 0) {
        $outStream.Write($buffer, 0, $n)
        $read += $n
        if ($total -gt 0) {
          $pct = [int]($read * 100 / $total)
          Write-Progress -Activity (FixEncoding 'Downloading the mihari package') -Status (FixEncoding ('Downloaded {0:N1} / {1:N1} MB' -f ($read/1MB), ($total/1MB))) -PercentComplete $pct
        } else {
          Write-Progress -Activity (FixEncoding 'Downloading the mihari package') -Status (FixEncoding ('Downloaded {0:N1} MB' -f ($read/1MB)))
        }
      }
    } finally { $outStream.Dispose() }
  } finally {
    if ($resp) { $resp.Dispose() }
    $client.Dispose()
  }
  Write-Progress -Activity (FixEncoding 'Downloading the mihari package') -Completed
}

# Return the remote length only when a bytes=0-0 probe proves strict Range
# support. A 200 response (or an incomplete Content-Range) deliberately selects
# the single-stream compatibility path.
function Get-RangeDownloadLength($url) {
  Add-Type -AssemblyName System.Net.Http
  $client = New-Object System.Net.Http.HttpClient
  $client.Timeout = [TimeSpan]::FromSeconds(30)
  $request = New-Object System.Net.Http.HttpRequestMessage([System.Net.Http.HttpMethod]::Get, $url)
  $request.Headers.Range = New-Object System.Net.Http.Headers.RangeHeaderValue(0, 0)
  try {
    $response = $client.SendAsync($request, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
    if ([int]$response.StatusCode -ne 206) { return 0L }
    $range = $response.Content.Headers.ContentRange
    if (-not $range -or $range.From -ne 0 -or $range.To -ne 0 -or -not $range.Length -or $range.Length -le 0) { return 0L }
    return [long]$range.Length
  } catch {
    return 0L
  } finally {
    if ($response) { $response.Dispose() }
    $request.Dispose()
    $client.Dispose()
  }
}

function Download-FileWithProgress($url, $dest) {
  $total = Get-RangeDownloadLength $url
  $segments = 4
  if ($total -lt $segments) {
    Download-SingleFileWithProgress $url $dest
    return
  }

  $partsDir = $dest + '.parts-' + [guid]::NewGuid().ToString('N')
  New-Item -ItemType Directory -Path $partsDir | Out-Null
  $pool = [runspacefactory]::CreateRunspacePool(1, $segments)
  $pool.Open()
  $workers = @()
  $workerScript = {
    param($DownloadUrl, $PartPath, [long]$Start, [long]$End, [long]$WholeLength)
    Add-Type -AssemblyName System.Net.Http
    $client = New-Object System.Net.Http.HttpClient
    $client.Timeout = [TimeSpan]::FromMinutes(30)
    $request = New-Object System.Net.Http.HttpRequestMessage([System.Net.Http.HttpMethod]::Get, $DownloadUrl)
    $request.Headers.Range = New-Object System.Net.Http.Headers.RangeHeaderValue($Start, $End)
    try {
      $response = $client.SendAsync($request, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
      $range = $response.Content.Headers.ContentRange
      if ([int]$response.StatusCode -ne 206 -or -not $range -or $range.From -ne $Start -or $range.To -ne $End -or $range.Length -ne $WholeLength) {
        throw "invalid range response for bytes=$Start-$End"
      }
      $input = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
      $output = [IO.File]::Create($PartPath)
      try { $input.CopyTo($output) } finally { $output.Dispose(); $input.Dispose() }
      $expected = $End - $Start + 1
      if ((Get-Item -LiteralPath $PartPath).Length -ne $expected) {
        throw "invalid segment length for bytes=$Start-$End"
      }
    } finally {
      if ($response) { $response.Dispose() }
      $request.Dispose()
      $client.Dispose()
    }
  }

  try {
    $baseSize = [long][Math]::Floor($total / $segments)
    for ($index = 0; $index -lt $segments; $index++) {
      $start = $index * $baseSize
      $end = if ($index -eq $segments - 1) { $total - 1 } else { $start + $baseSize - 1 }
      $partPath = Join-Path $partsDir ('part-{0:D2}' -f $index)
      $powershell = [powershell]::Create()
      $powershell.RunspacePool = $pool
      [void]$powershell.AddScript($workerScript).AddArgument($url).AddArgument($partPath).AddArgument($start).AddArgument($end).AddArgument($total)
      $workers += [pscustomobject]@{ PowerShell = $powershell; Handle = $powershell.BeginInvoke(); Path = $partPath }
    }

    while (($workers | Where-Object { -not $_.Handle.IsCompleted }).Count -gt 0) {
      $read = [long](($workers | ForEach-Object { if (Test-Path -LiteralPath $_.Path) { (Get-Item -LiteralPath $_.Path).Length } else { 0 } } | Measure-Object -Sum).Sum)
      $pct = [int]($read * 100 / $total)
      Write-Progress -Activity (FixEncoding 'Downloading the mihari package') -Status (FixEncoding ('Downloaded {0:N1} / {1:N1} MB' -f ($read/1MB), ($total/1MB))) -PercentComplete $pct
      Start-Sleep -Milliseconds 100
    }
    foreach ($worker in $workers) { $worker.PowerShell.EndInvoke($worker.Handle) | Out-Null }

    $output = [IO.File]::Create($dest)
    try {
      foreach ($worker in $workers) {
        $input = [IO.File]::OpenRead($worker.Path)
        try { $input.CopyTo($output) } finally { $input.Dispose() }
      }
    } finally { $output.Dispose() }
    if ((Get-Item -LiteralPath $dest).Length -ne $total) { throw 'merged download length mismatch' }
  } catch {
    Remove-Item -LiteralPath $dest -Force -ErrorAction SilentlyContinue
    throw
  } finally {
    foreach ($worker in $workers) { $worker.PowerShell.Dispose() }
    $pool.Dispose()
    Remove-Item -LiteralPath $partsDir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Progress -Activity (FixEncoding 'Downloading the mihari package') -Completed
  }
}
# Print a short install plan + what's affected, then confirm. Runs once before the
# download, unifying the per-branch confirms that used to scatter the version block.
function Show-InstallPlan {
  $binHint = if ($env:MIHARI_BIN) { $env:MIHARI_BIN } else { Join-Path $env:LOCALAPPDATA 'Programs\mihari' }
  $dataHint = if ($env:MIHARI_DATA) { $env:MIHARI_DATA } else { Join-Path $env:USERPROFILE '.mihari' }
  $ver = if ($latest) { $latest } else { '(unknown)' }
  Write-Host ''
  Write-Host (FixEncoding "Ready to install mihari $ver") -ForegroundColor Yellow
  Write-Host (FixEncoding "  Platform : $platform")
  Write-Host (FixEncoding "  Source   : $resolvedUrl")
  Write-Host (FixEncoding "  Binary   : $binHint")
  Write-Host (FixEncoding "  Data     : $dataHint")
  Write-Host (FixEncoding "  Action   : Register the system service (requires UAC elevation)")
  Write-Host (FixEncoding "  Preserve : mihari.yaml, subscriptions, panel state, and other user settings")
  Write-Host ''
}

function Test-CanonicalStable([string]$tag) {
  return [bool]($tag -cmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$')
}
function Test-CanonicalDev([string]$tag) {
  return [bool]($tag -cmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$')
}
function Write-RemoteTestState {
  $workdir = Join-Path $env:USERPROFILE 'Downloads\mihari-aio'
  if ($Channel) {
    $handoff = "& ([scriptblock]::Create((Get-Content -Raw '$workdir\install-aio.ps1'))) -Channel $Channel -BundleDir $workdir"
  } else {
    $handoff = "& ([scriptblock]::Create((Get-Content -Raw '$workdir\install-aio.ps1'))) -BundleDir $workdir"
  }
  Write-Output "CHANNEL=$Channel"
  Write-Output ("EXPLICIT=" + $(if ($Channel) { '1' } else { '0' }))
  Write-Output "INDEX_URL=$indexUrl"
  Write-Output "HANDOFF=$handoff"
  Write-Output "LATEST=$latest"
}

# Tests dot-source this standalone script to exercise the real downloader
# against a local HTTP server without running the installation flow.
if ($env:MIHARI_INSTALL_TEST_MODE -eq '1') {
  $sourced = ($MyInvocation.InvocationName -eq '.' -or $MyInvocation.Line -match '^\s*\.')
  if ($sourced) { return }
  $latest = ''
  if (-not $bundleUrl -and $env:MIHARI_INDEX_URL) {
    # Fall through to index fetch for latest-shape tests.
  } else {
    Write-RemoteTestState
    return
  }
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
  try {
    $resp = Invoke-WebRequest -Uri $indexUrl -UseBasicParsing
    # PS 5.1 returns .Content as [byte[]] for application/octet-stream (AList serves
    # every file with that content-type), which would defeat the string -split below.
    # Decode to UTF-8 text so parsing works on any PS version / content-type.
    if ($resp.Content -is [byte[]]) { $index = [System.Text.Encoding]::UTF8.GetString($resp.Content) } else { $index = $resp.Content }
  } catch { $index = '' }
  if (-not $index) { Fail "The release index is unavailable. Try again later or check network and storage availability." }
  foreach ($line in ($index -split "`n")) {
    $line = $line.Trim()
    if (-not $line -or $line.StartsWith('#') -or $line.StartsWith('//')) { continue }
    $fields = $line -split '\s+'
    if ($fields[0] -eq 'latest') { $latest = $fields[1] }
    elseif ($fields[0] -eq $platform) { $resolvedUrl = $fields[1]; $wantSum = $fields[2] }
  }
  if (-not $latest) { Fail "The index has no latest release. Publication may be in progress or the release may have been withdrawn." }
  if ($env:MIHARI_INSTALL_TEST_MODE -ne '1') {
    if (-not $resolvedUrl) { Fail "The index has no package for $platform." }
  }
  if ($Channel) {
    if ($Channel -eq 'dev') {
      if (-not (Test-CanonicalDev $latest)) { Fail 'dev index latest must be vX.Y.Z-dev.N' }
    } elseif (-not (Test-CanonicalStable $latest)) {
      Fail 'main index latest must be vX.Y.Z'
    }
  }
}

if ($env:MIHARI_INSTALL_TEST_MODE -eq '1') {
  Write-RemoteTestState
  return
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
  Info ("No mihari installation was found. Installing the latest version" + $(if ($latest) { " ($latest)" }))
} elseif (-not $current) {
  Info 'Mihari was found, but its version is unknown. The executable may be damaged; reinstalling to repair it.'
} elseif ($latest -and $current -eq $latest) {
  Info "The latest version ($current) is already installed. Reinstalling to repair it."
} else {
  Info ("Currently installed: $current" + $(if ($latest) { "; latest version: $latest. Upgrading." }))
}

# Install plan + confirm before the (large) download. -Yes skips it.
if (-not $Yes) {
  Show-InstallPlan
  if (-not (Confirm 'Start installation?')) { Info 'Canceled.'; exit 0 }
}

# Download to a temp file (outside the work dir) so the work dir can be fully
# cleared before extraction — PS 5.1 Expand-Archive does not reliably overwrite
# existing files (design 4.4 step 4).
$workdir = Join-Path $env:USERPROFILE 'Downloads\mihari-aio'
New-Item -ItemType Directory -Force -Path $workdir | Out-Null
$tmpArchive = Join-Path ([IO.Path]::GetTempPath()) ("mihari-aio-" + ([guid]::NewGuid().ToString('N')) + ".zip")
Info "Downloading $resolvedUrl …"
Download-FileWithProgress -url $resolvedUrl -dest $tmpArchive
if ($wantSum) {
  $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $tmpArchive).Hash.ToLower()
  if ($got -ne $wantSum.ToLower()) { Remove-Item -LiteralPath $tmpArchive -Force; Fail "SHA-256 verification failed: expected $wantSum, got $got." }
  Info 'SHA-256 verification passed.'
}
Info "Extracting to $workdir …"
if (Test-Path -LiteralPath $workdir) { Get-ChildItem -LiteralPath $workdir | Remove-Item -Recurse -Force }
Expand-Archive -LiteralPath $tmpArchive -DestinationPath $workdir -Force
Remove-Item -LiteralPath $tmpArchive -Force

# Hand off to the local installer inside the bundle (script 2) via a scriptblock
# that reads the file content as a string — bypassing ExecutionPolicy and Mark of
# the Web; the bundle dir is injected via -BundleDir (design 4.4 step 5).
$localInstaller = Join-Path $workdir 'install-aio.ps1'
if (-not (Test-Path -LiteralPath $localInstaller)) { Fail 'The package is missing install-aio.ps1.' }
if ($Channel) {
  & ([scriptblock]::Create([IO.File]::ReadAllText($localInstaller))) -Channel $Channel -BundleDir $workdir
} else {
  & ([scriptblock]::Create([IO.File]::ReadAllText($localInstaller))) -BundleDir $workdir
}
