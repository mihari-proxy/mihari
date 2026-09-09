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
#   $env:MIHARI_BUNDLE_URL  explicit bundle URL (unverified; automatic handoff refused)
# -Yes or exactly MIHARI_YES=1 accepts this installation's compatibility risks.
param([switch]$Yes, [string]$Channel)
$ErrorActionPreference = 'Stop'
$explicitYes = [bool]($Yes -or $env:MIHARI_YES -ceq '1')

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
  if ($explicitYes) { return $true }
  if ([Console]::IsInputRedirected -or [Environment]::GetCommandLineArgs() -contains '-NonInteractive') {
    throw 'Confirmation is required. Re-run interactively or use -Yes / MIHARI_YES=1.'
  }
  $ans = Read-Host ((FixEncoding $p) + ' [y/N]')
  return $ans -match '^(?i:y|yes)$'
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
# This only describes inert preparation; the local installer owns final risk confirmation.
function Show-InstallPlan {
  $ver = if ($latest) { $latest } else { '(unknown)' }
  Write-Host ''
  Write-Host ("Ready to download mihari $ver") -ForegroundColor Yellow
  Write-Host ("  Platform : $platform")
  Write-Host '  The local installer will inspect the actual replacement targets and confirm compatibility risks before installation.'
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

# BEGIN VERIFIED LOCAL HANDOFF
function Invoke-VerifiedLocalInstaller([string]$Installer, [string]$BundleDir, [string]$Channel, [bool]$ExplicitYes, [bool]$VerifiedSource) {
  if (-not $VerifiedSource) { throw 'Automatic installer handoff requires a checksum-verified bundle. MIHARI_BUNDLE_URL does not provide execution trust.' }
  $unsupported = 'This bundle contains an installer without replacement confirmation support. The verified bundle has been kept. Use the current install-aio.ps1 with -BundleDir pointing to this extracted bundle to install it safely.'
  $unsupported += " Extracted bundle: $BundleDir"
  # Read once: the same verified script bytes are queried and then invoked.
  $source = [IO.File]::ReadAllText($Installer)
  if ($source -cnotmatch '(?m)^# MIHARI_INSTALL_CAPABILITY: replacement_confirmation_v1\r?$') { throw $unsupported }
  if (-not ('MihariInstallerCapabilityProbe' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Text;
using System.Diagnostics;
public static class MihariInstallerCapabilityProbe {
  public static string Query(string executable, string command, string root) {
    using (var p = new Process()) {
      p.StartInfo = new ProcessStartInfo(executable, "-NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand " + command) {
        UseShellExecute=false, CreateNoWindow=true, RedirectStandardOutput=true, RedirectStandardError=true, WorkingDirectory=root };
      p.StartInfo.EnvironmentVariables.Clear();
      foreach (var key in new [] {"SystemRoot", "WINDIR"}) {
        var value = Environment.GetEnvironmentVariable(key);
        if (value != null) p.StartInfo.EnvironmentVariables[key] = value;
      }
      foreach (var key in new [] {"USERPROFILE", "LOCALAPPDATA", "APPDATA", "HOME", "TMP", "TEMP"}) p.StartInfo.EnvironmentVariables[key] = root;
      if (!p.Start()) return null;
      // Read one byte asynchronously per pipe: memory stays bounded even without newlines.
      var stdout = new MemoryStream(); var stderr = new MemoryStream();
      var a = new byte[1]; var b = new byte[1];
      var ar = p.StandardOutput.BaseStream.ReadAsync(a, 0, 1);
      var br = p.StandardError.BaseStream.ReadAsync(b, 0, 1);
      bool ae = false, be = false, invalid = false;
      var timer = Stopwatch.StartNew();
      try {
        while (!(ae && be && p.HasExited)) {
          if (timer.ElapsedMilliseconds >= 3000) { invalid = true; break; }
          if (!ae && ar.IsCompleted) { if (ar.Result == 0) ae = true; else { stdout.WriteByte(a[0]); ar = p.StandardOutput.BaseStream.ReadAsync(a, 0, 1); } }
          if (!be && br.IsCompleted) { if (br.Result == 0) be = true; else { stderr.WriteByte(b[0]); br = p.StandardError.BaseStream.ReadAsync(b, 0, 1); } }
          if (stdout.Length > 4096 || stderr.Length > 4096) { invalid = true; break; }
          if ((!ae && !ar.IsCompleted) && (!be && !br.IsCompleted)) System.Threading.Thread.Sleep(1);
        }
      } finally {
        if (!p.HasExited) p.Kill();
        p.WaitForExit();
      }
      return invalid || p.ExitCode != 0 ? null : Encoding.UTF8.GetString(stdout.ToArray());
    }
  }
}
'@
  }
  $probeRoot = Join-Path ([IO.Path]::GetTempPath()) ('mihari-capability-' + [guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $probeRoot | Out-Null
  try {
    # Use this host's absolute executable, never an executable resolved from PATH.
    $hostName = if ($PSVersionTable.PSEdition -eq 'Desktop') { 'powershell.exe' } elseif ($env:OS -eq 'Windows_NT') { 'pwsh.exe' } else { 'pwsh' }
    $executable = Join-Path $PSHOME $hostName
    $probeScript = Join-Path $probeRoot 'capabilities.ps1'
    [IO.File]::WriteAllText($probeScript, $source, [Text.Encoding]::UTF8)
    $command = "& '" + $probeScript.Replace("'", "''") + "' -Capabilities"
    $encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $raw = [MihariInstallerCapabilityProbe]::Query($executable, $encodedCommand, $probeRoot)
    # Fixed v1 response: one object, no duplicate fields, trailing objects, or extra output.
    $schema = '"schema"[ \t\r\n]*:[ \t\r\n]*"mihari\.install-script/v1"'
    $capability = '"capabilities"[ \t\r\n]*:[ \t\r\n]*\[[ \t\r\n]*"replacement_confirmation_v1"[ \t\r\n]*\]'
    $shape = '\A[ \t\r\n]*\{[ \t\r\n]*(?:' + $schema + '[ \t\r\n]*,[ \t\r\n]*' + $capability + '|' + $capability + '[ \t\r\n]*,[ \t\r\n]*' + $schema + ')[ \t\r\n]*\}[ \t\r\n]*\z'
    if (-not $raw -or $raw -cnotmatch $shape) { throw $unsupported }
  } catch {
    throw $unsupported
  } finally {
    Remove-Item -LiteralPath $probeRoot -Recurse -Force
  }
  $previousYes = $env:MIHARI_YES
  $previousExitCode = $global:LASTEXITCODE
  $invocation = $null
  try {
    if ($ExplicitYes) { $env:MIHARI_YES = '1' }
    $global:LASTEXITCODE = 0
    $arguments = @{ BundleDir = $BundleDir }
    if ($Channel) { $arguments.Channel = $Channel }
    # A nested pipeline contains script 'exit' without terminating the caller.
    # Its error stream retains reported non-terminating errors which $? can miss,
    # while preserving intentionally silent optional lookups in the installer.
    $invocation = [powershell]::Create([System.Management.Automation.RunspaceMode]::CurrentRunspace)
    [void]$invocation.AddScript($source).AddParameters($arguments)
    $invocation.Invoke()
    if ($invocation.Streams.Error.Count -gt 0 -or $global:LASTEXITCODE -ne 0) { throw 'Local installation failed.' }
  } finally {
    if ($null -ne $invocation) { $invocation.Dispose() }
    $global:LASTEXITCODE = $previousExitCode
    if ($null -eq $previousYes) { Remove-Item Env:MIHARI_YES -ErrorAction SilentlyContinue }
    else { $env:MIHARI_YES = $previousYes }
  }
}
# END VERIFIED LOCAL HANDOFF

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

# Download acceptance is not replacement consent.
Show-InstallPlan
if (-not (Confirm 'Download and prepare the bundle?')) { throw 'Canceled. No installation changes were made.' }

# Download to a temp file (outside the work dir) so the work dir can be fully
# cleared before extraction — PS 5.1 Expand-Archive does not reliably overwrite
# existing files (design 4.4 step 4).
$workdir = Join-Path $env:USERPROFILE 'Downloads\mihari-aio'
New-Item -ItemType Directory -Force -Path $workdir | Out-Null
$tmpArchive = Join-Path ([IO.Path]::GetTempPath()) ("mihari-aio-" + ([guid]::NewGuid().ToString('N')) + ".zip")
Info "Downloading $resolvedUrl …"
Download-FileWithProgress -url $resolvedUrl -dest $tmpArchive
$verifiedSource = $false
if ($wantSum) {
  if ($wantSum -cnotmatch '^[0-9a-fA-F]{64}$') { Remove-Item -LiteralPath $tmpArchive -Force; Fail 'The index checksum is invalid.' }
  $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $tmpArchive).Hash.ToLower()
  if ($got -ne $wantSum.ToLower()) { Remove-Item -LiteralPath $tmpArchive -Force; Fail "SHA-256 verification failed: expected $wantSum, got $got." }
  $verifiedSource = $true
  Info 'SHA-256 verification passed.'
}
Info "Extracting to $workdir …"
if (Test-Path -LiteralPath $workdir) { Get-ChildItem -LiteralPath $workdir | Remove-Item -Recurse -Force }
Expand-Archive -LiteralPath $tmpArchive -DestinationPath $workdir -Force
Remove-Item -LiteralPath $tmpArchive -Force

# Only checksum-verified bundles can authorize capability probing and handoff.
$localInstaller = Join-Path $workdir 'install-aio.ps1'
if (-not (Test-Path -LiteralPath $localInstaller)) { Fail 'The package is missing install-aio.ps1.' }
Invoke-VerifiedLocalInstaller -Installer $localInstaller -BundleDir $workdir -Channel $Channel -ExplicitYes $explicitYes -VerifiedSource $verifiedSource
