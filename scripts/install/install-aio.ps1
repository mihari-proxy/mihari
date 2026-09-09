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
# MIHARI_YES=1 accepts this installation's replacement compatibility risks.
# Downgrade/unknown prompts default to No; redirected input requires explicit yes.
# Environment overrides:
#   $env:MIHARI_BIN    mihari binary install dir (default %LOCALAPPDATA%\Programs\mihari)
#   $env:MIHARI_DATA   data root (default %USERPROFILE%\.mihari)
param([string]$BundleDir, [string]$Channel, [switch]$Capabilities)
# MIHARI_INSTALL_CAPABILITY: replacement_confirmation_v1
if ($Capabilities) {
  @{ schema = 'mihari.install-script/v1'; capabilities = @('replacement_confirmation_v1') } | ConvertTo-Json -Compress
  return
}
$ErrorActionPreference = 'Stop'

function Info($m) { Write-Host "* $m" -ForegroundColor Cyan }
function Fail($m) { Write-Host "error: $m" -ForegroundColor Red; throw $m }

# BEGIN REPLACEMENT CONFIRMATION
# Kept in both standalone installers; exercised with one shared version fixture.
function Convert-ReplacementVersion([string]$Value) {
  $Value = $Value.Trim()
  if ($Value -cnotmatch '^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-dev\.(0|[1-9][0-9]*))?$') { return $null }
  $parts = @($Matches[1], $Matches[2], $Matches[3], $Matches[4])
  $numbers = @()
  foreach ($part in $parts[0..2]) {
    $number = 0L
    if (-not [long]::TryParse($part, [ref]$number)) { return $null }
    $numbers += $number
  }
  $numbers += $(if ($null -eq $parts[3]) { 1L } else { 0L })
  $number = 0L
  if ($null -ne $parts[3] -and -not [long]::TryParse($parts[3], [ref]$number)) { return $null }
  $numbers += $number
  return ,$numbers
}

function Get-ReplacementRisk([string]$Current, [string]$Target) {
  $a = Convert-ReplacementVersion $Current
  $b = Convert-ReplacementVersion $Target
  if ($null -eq $a -or $null -eq $b) { return 'unknown' }
  for ($n = 0; $n -lt 5; $n++) {
    if ($a[$n] -gt $b[$n]) { return 'downgrade' }
    if ($a[$n] -lt $b[$n]) { return 'none' }
  }
  return 'none'
}

function Initialize-ReplacementNative {
  if ('MihariReplacementNative' -as [type]) { return }
  Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Text;
using System.Diagnostics;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;
public static class MihariReplacementNative {
  [StructLayout(LayoutKind.Sequential)] struct Info {
    public uint Attributes; public System.Runtime.InteropServices.ComTypes.FILETIME Creation, Access, Write;
    public uint Volume, SizeHigh, SizeLow, Links, IndexHigh, IndexLow;
  }
  [DllImport("kernel32.dll", SetLastError=true)] static extern bool GetFileInformationByHandle(SafeFileHandle h, out Info info);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern uint GetFinalPathNameByHandle(SafeFileHandle h, StringBuilder p, uint n, uint flags);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern uint GetLongPathName(string path, StringBuilder result, uint size);
  public static string LongPath(string path) {
    var result = new StringBuilder(32768);
    uint size = GetLongPathName(path, result, (uint)result.Capacity);
    if (size == 0 || size >= result.Capacity) throw new IOException("Cannot resolve replacement parent.");
    return result.ToString();
  }
  public static string[] Identity(FileStream stream) {
    Info i;
    if (!GetFileInformationByHandle(stream.SafeFileHandle, out i)) throw new IOException("Cannot observe replacement file identity.");
    var p = new StringBuilder(32768);
    uint n = GetFinalPathNameByHandle(stream.SafeFileHandle, p, (uint)p.Capacity, 0);
    if (n == 0 || n >= p.Capacity) throw new IOException("Cannot resolve replacement path.");
    return new [] { p.ToString().ToLowerInvariant(), i.Volume + ":" + i.IndexHigh + ":" + i.IndexLow };
  }
  public static string Probe(string path, string root) {
    using (var p = new Process()) {
      p.StartInfo = new ProcessStartInfo(path, "self version --json") { UseShellExecute=false, CreateNoWindow=true,
        RedirectStandardOutput=true, RedirectStandardError=true, WorkingDirectory=root };
      p.StartInfo.EnvironmentVariables.Clear();
      foreach (var key in new [] {"SystemRoot", "WINDIR"}) {
        string value = Environment.GetEnvironmentVariable(key);
        if (value != null) p.StartInfo.EnvironmentVariables[key] = value;
      }
      foreach (var key in new [] {"USERPROFILE", "LOCALAPPDATA", "APPDATA", "HOME", "TMP", "TEMP"}) p.StartInfo.EnvironmentVariables[key] = root;
      p.StartInfo.EnvironmentVariables["MIHARI_DATA"] = Path.Combine(root, "data");
      p.StartInfo.EnvironmentVariables["MIHARI_CONTROL_ENDPOINT"] = "\\\\.\\pipe\\mihari-probe-" + Guid.NewGuid().ToString("N");
      p.StartInfo.EnvironmentVariables["MIHARI_CONTROL_CREDENTIAL"] = Path.Combine(root, "token");
      p.StartInfo.EnvironmentVariables["MIHARI_INSTALL_ROOT"] = Path.Combine(root, "install");
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

function Convert-ReplacementPath([string]$Path) {
  # PowerShell's filesystem location can differ from the CLR process directory.
  return $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
}

function Convert-ReplacementCanonicalPath([string]$Path) {
  # GetFinalPathNameByHandle uses extended paths, whereas a missing entry is
  # resolved through its long parent path. Keep one representation for both.
  if ($Path.StartsWith('\\?\UNC\', [StringComparison]::OrdinalIgnoreCase)) {
    $Path = '\\' + $Path.Substring(8)
  } elseif ($Path.StartsWith('\\?\', [StringComparison]::OrdinalIgnoreCase)) {
    $Path = $Path.Substring(4)
  }
  return $Path.ToLowerInvariant()
}

function Get-ReplacementFile([string]$Path) {
  $full = Convert-ReplacementPath $Path
  if ($env:OS -eq 'Windows_NT') {
    $cursor = [IO.Path]::GetDirectoryName($full)
    while ($cursor) {
      try { $parent = Get-Item -LiteralPath $cursor -Force -ErrorAction Stop }
      catch [System.Management.Automation.ItemNotFoundException] { $parent = $null }
      if ($parent -and ($parent.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unsafe replacement path.' }
      $cursor = [IO.Path]::GetDirectoryName($cursor)
    }
  }
  try { $item = Get-Item -LiteralPath $full -Force -ErrorAction Stop }
  catch [System.Management.Automation.ItemNotFoundException] { $item = $null }
  if ($null -eq $item) {
    if ($env:OS -eq 'Windows_NT') {
      $existing = [IO.Path]::GetDirectoryName($full)
      $suffix = [IO.Path]::GetFileName($full)
      while (-not [IO.Directory]::Exists($existing)) {
        $suffix = Join-Path ([IO.Path]::GetFileName($existing)) $suffix
        $existing = [IO.Path]::GetDirectoryName($existing)
        if (-not $existing) { throw 'Cannot resolve replacement parent.' }
      }
      Initialize-ReplacementNative
      $full = Join-Path ([MihariReplacementNative]::LongPath($existing)) $suffix
    }
    return [pscustomobject][ordered]@{ Path=(Convert-ReplacementCanonicalPath $full); Exists=$false; Identity=''; Digest='' }
  }
  if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Unsafe replacement file.' }
  $stream = [IO.File]::Open($full, 'Open', 'Read', 'Read')
  try {
    if ($env:OS -eq 'Windows_NT') {
      Initialize-ReplacementNative
      $identity = [MihariReplacementNative]::Identity($stream)
    } else {
      # Portable test hosts; Windows always uses volume/file ID and final path.
      $identity = @($full.ToLowerInvariant(), $item.CreationTimeUtc.Ticks.ToString())
    }
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $digest = [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() } finally { $sha.Dispose() }
    return [pscustomobject][ordered]@{ Path=(Convert-ReplacementCanonicalPath $identity[0]); Exists=$true; Identity=$identity[1]; Digest=$digest }
  } finally { $stream.Dispose() }
}

function Test-ReplacementProbeTrust([string]$Path) {
  if ($env:OS -ne 'Windows_NT') { return $false }
  $admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
  $cursor = Convert-ReplacementPath $Path
  try {
    while ($cursor) {
      $item = Get-Item -LiteralPath $cursor -Force
      if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { return $false }
      if ($admin) {
        $acl = Get-Acl -LiteralPath $cursor
        if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin @('S-1-5-18','S-1-5-32-544')) { return $false }
        foreach ($rule in $acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
          if ($rule.AccessControlType -eq 'Allow' -and ($rule.FileSystemRights -band 0x000D0156) -ne 0 -and
              $rule.IdentityReference.Value -notin @('S-1-5-18','S-1-5-32-544')) { return $false }
        }
      }
      $cursor = [IO.Path]::GetDirectoryName($cursor)
    }
    return $true
  } catch { return $false }
}

function Get-ReplacementVersion([string]$Path) {
  if (-not (Test-ReplacementProbeTrust $Path)) { return '' }
  $root = Join-Path ([IO.Path]::GetTempPath()) ('mihari-version-' + [Guid]::NewGuid().ToString('N'))
  try {
    New-Item -ItemType Directory -Path $root | Out-Null
    Initialize-ReplacementNative
    $raw = [MihariReplacementNative]::Probe($Path, $root)
    # Only the single version envelope is accepted; no arbitrary output is shown.
    if (-not $raw -or $raw -notmatch '^\s*\{\s*"(?:schema|version)"\s*:\s*"[^"\\]*"\s*,\s*"(?:schema|version)"\s*:\s*"[^"\\]*"\s*\}\s*$') { return '' }
    if ([regex]::Matches($raw, '"version"\s*:').Count -ne 1 -or [regex]::Matches($raw, '"schema"\s*:').Count -ne 1) { return '' }
    $value = ConvertFrom-Json -InputObject $raw
    if ($value.schema -cne 'mihari/v1' -or $value.version -isnot [string] -or $null -eq (Convert-ReplacementVersion $value.version)) { return '' }
    return $value.version.Trim()
  } catch { return '' } finally { if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force } }
}

function Get-ReplacementService {
  if ($env:MIHARI_INSTALL_TEST_MODE -eq '1') { return [pscustomobject]@{ Definition=''; Path=''; Running=$false } }
  $svc = Get-CimInstance -ClassName Win32_Service -Filter "Name='mihari'" -ErrorAction Stop
  if ($null -eq $svc) { return [pscustomobject]@{ Definition=''; Path=''; Running=$false } }
  # Do not guess the executable in an ambiguous, unquoted SCM command line.
  $command = [string]$svc.PathName
  if ($command -match '^"([^"]+)"(?:\s|$)') { $path = $Matches[1] }
  elseif ($command -match '^(\S+\.exe)(?:\s|$)') { $path = $Matches[1] }
  else { throw 'Cannot resolve the registered Mihari service executable safely.' }
  $definition = [ordered]@{ PathName=$command; StartName=$svc.StartName; StartMode=$svc.StartMode; ServiceType=$svc.ServiceType } | ConvertTo-Json -Compress
  return [pscustomobject]@{ Definition=$definition; Path=$path; Running=($svc.State -eq 'Running') }
}

function Get-ReplacementPreview([string]$Candidate, [string]$TargetVersion, [string[]]$Targets) {
  $candidateFile = Get-ReplacementFile $Candidate
  if (-not $candidateFile.Exists) { throw 'Replacement candidate is missing.' }
  $service = Get-ReplacementService
  $files = @()
  $risk = 'none'
  foreach ($path in @($Targets | Sort-Object -Unique)) {
    $file = Get-ReplacementFile $path
    if (@($files | Where-Object { $_.File.Path -eq $file.Path }).Count -gt 0) { continue }
    $version = ''
    if ($file.Exists) {
      $version = Get-ReplacementVersion $path
      if ((Get-ReplacementFile $path | ConvertTo-Json -Compress) -cne ($file | ConvertTo-Json -Compress)) { throw 'Installation changed during version inspection. Retry the installation.' }
      $oneRisk = Get-ReplacementRisk $version $TargetVersion
      if ($oneRisk -eq 'downgrade' -or ($oneRisk -eq 'unknown' -and $risk -eq 'none')) { $risk = $oneRisk }
    }
    $files += [pscustomobject][ordered]@{ SourcePath=$path; File=$file; Version=$version }
  }
  return [pscustomobject][ordered]@{ CandidateDigest=$candidateFile.Digest; TargetVersion=$TargetVersion; Targets=$files; Service=$service.Definition; Risk=$risk; StopRequired=$service.Running }
}

function Assert-ReplacementPreview($Expected, [string]$Candidate, [string[]]$Targets) {
  if ((Get-ReplacementFile $Candidate).Digest -cne $Expected.CandidateDigest) { throw 'Replacement candidate changed. Retry the installation.' }
  if ((Get-ReplacementService).Definition -cne $Expected.Service) { throw 'Service installation changed. Retry the installation.' }
  $actual = @($Targets | ForEach-Object { Get-ReplacementFile $_ } | Sort-Object Path -Unique)
  $before = @($Expected.Targets | ForEach-Object { $_.File } | Sort-Object Path -Unique)
  if (($actual | ConvertTo-Json -Depth 8 -Compress) -cne ($before | ConvertTo-Json -Depth 8 -Compress)) { throw 'Installation changed. Retry the installation.' }
}

function Confirm-Replacement($Preview, [bool]$ExplicitYes) {
  if ($Preview.Risk -ne 'none') {
    Write-Host ('Replacement compatibility risk: ' + $Preview.Risk) -ForegroundColor Yellow
    if ($Preview.Risk -eq 'unknown') { Write-Host 'The installed or target version could not be determined; compatibility cannot be determined.' -ForegroundColor Yellow }
    foreach ($target in $Preview.Targets) {
      if ($target.File.Exists) { Write-Host ('Installed: ' + $(if ($target.Version) { $target.Version } else { 'unknown' }) + ' -> ' + $(if ($Preview.TargetVersion) { $Preview.TargetVersion } else { 'unknown' })) }
    }
    Write-Host 'Older Mihari versions may not support settings, subscriptions, state, or generated files written by newer versions. Mihari may fail to start or read data, which can appear as data loss. Downgrading is not a supported configuration migration and does not automatically roll back on-disk state. Back up your data before continuing.' -ForegroundColor Yellow
  }
  if ($Preview.StopRequired) { Write-Host 'The running Mihari service or process will be stopped for installation.' -ForegroundColor Yellow }
  if ($ExplicitYes -or ($Preview.Risk -eq 'none' -and -not $Preview.StopRequired)) { return $true }
  if ([Console]::IsInputRedirected -or [Environment]::GetCommandLineArgs() -contains '-NonInteractive') { throw 'Confirmation is required. Re-run interactively or set MIHARI_YES=1.' }
  try { $answer = Read-Host 'Continue with this replacement? [y/N]' } catch { return $false }
  return $answer -cmatch '^(y|Y|yes|YES)$'
}
function Get-ReplacementServiceDestination {
  $root = $env:MIHARI_INSTALL_ROOT
  if (-not $root) {
    $programs = if ($env:ProgramFiles) { $env:ProgramFiles } else { 'C:\Program Files' }
    $root = Join-Path $programs 'Mihari'
  }
  return Convert-ReplacementPath (Join-Path $root 'mihari.exe')
}

function Get-ReplacementEnvironment {
  # UAC may start in a different working directory. Carry the actual roots
  # selected by this invocation, not relative strings that can select new ones.
  $dataRoot = if ($env:MIHARI_DATA) { $env:MIHARI_DATA } else { Join-Path $env:USERPROFILE '.mihari' }
  return [pscustomobject]@{
    MIHARI_DATA=(Convert-ReplacementPath $dataRoot)
    MIHARI_INSTALL_ROOT=[IO.Path]::GetDirectoryName((Get-ReplacementServiceDestination))
    ProgramFiles=$env:ProgramFiles
  }
}

function Invoke-ReplacementAction($Plan) {
  Assert-ReplacementPreview $Plan.Preview $Plan.Candidate $Plan.Targets
  switch ($Plan.Action) {
    'Stop' {
      if ((Get-ReplacementService).Running) { Stop-Service -Name mihari -Force }
      if ($Plan.StopProcesses) { Get-Process -Name mihari -ErrorAction SilentlyContinue | Stop-Process -Force }
    }
    'Swap' {
      $running = (Get-ReplacementService).Running
      if ($running) { Stop-Service -Name mihari -Force }
      try {
        Assert-ReplacementPreview $Plan.Preview $Plan.Candidate $Plan.Targets
        Copy-Item -LiteralPath $Plan.Candidate -Destination $Plan.Destination -Force
      } finally { if ($running) { Start-Service -Name mihari } }
    }
    'Service' {
      # Recheck after stopping, immediately before the existing staging command.
      $running = (Get-ReplacementService).Running
      if ($running) { Stop-Service -Name mihari -Force }
      try {
        Assert-ReplacementPreview $Plan.Preview $Plan.Candidate $Plan.Targets
        $global:LASTEXITCODE = 0
        & $Plan.Candidate @($Plan.ServiceArgs)
        if ($LASTEXITCODE -ne 0) { throw 'Mihari service operation failed.' }
      } catch {
        if ($running) { Start-Service -Name mihari -ErrorAction SilentlyContinue }
        throw
      }
    }
    default { throw 'Invalid replacement action.' }
  }
}

function Invoke-ReplacementElevated($Plan) {
  if ($isAdmin) { Invoke-ReplacementAction $Plan; return }
  # The initiating user's private temporary directory carries the original
  # preview. No path, channel, or other input is inserted into PowerShell code.
  $root = Join-Path ([IO.Path]::GetTempPath()) ('mihari-confirmation-' + [Guid]::NewGuid().ToString('N'))
  $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
  $acl = New-Object Security.AccessControl.DirectorySecurity
  $acl.SetOwner($sid)
  $acl.SetAccessRuleProtection($true, $false)
  foreach ($who in @($sid, [Security.Principal.SecurityIdentifier]'S-1-5-18', [Security.Principal.SecurityIdentifier]'S-1-5-32-544')) {
    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($who, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')))
  }
  try {
    if ($PSVersionTable.PSEdition -eq 'Core') {
      [IO.FileSystemAclExtensions]::Create((New-Object IO.DirectoryInfo($root)), $acl)
    } else { [IO.Directory]::CreateDirectory($root, $acl) | Out-Null }
    $request = Join-Path $root 'request.json'
    $worker = Join-Path $root 'worker.ps1'
    $Plan | ConvertTo-Json -Depth 12 -Compress | Set-Content -LiteralPath $request -Encoding UTF8
    $functions = @('Convert-ReplacementVersion','Get-ReplacementRisk','Initialize-ReplacementNative','Convert-ReplacementPath','Convert-ReplacementCanonicalPath','Get-ReplacementFile',
      'Test-ReplacementProbeTrust','Get-ReplacementVersion','Get-ReplacementService','Get-ReplacementPreview',
      'Assert-ReplacementPreview','Invoke-ReplacementAction')
    $code = "`$ErrorActionPreference='Stop'`n"
    foreach ($name in $functions) { $code += 'function ' + $name + ' {' + (Get-Command $name).Definition + "}`n" }
    $code += @'
try {
  $directory = Get-Item -LiteralPath $PSScriptRoot -Force
  if ($directory.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Unsafe confirmation handoff.' }
  $acl = Get-Acl -LiteralPath $PSScriptRoot
  $owner = $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
  if (-not $acl.AreAccessRulesProtected) { throw 'Unprotected confirmation handoff.' }
  foreach ($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
    if ($rule.AccessControlType -eq 'Allow' -and $rule.IdentityReference.Value -notin @($owner,'S-1-5-18','S-1-5-32-544')) { throw 'Unsafe confirmation handoff permissions.' }
  }
  $file = Join-Path $PSScriptRoot 'request.json'
  if ((Get-Item -LiteralPath $file).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Unsafe confirmation request.' }
  $plan = Get-Content -Raw -LiteralPath $file | ConvertFrom-Json
  Remove-Item Env:MIHARI_INSTALL_TEST_MODE -ErrorAction SilentlyContinue
  if ($plan.Environment) {
    foreach ($key in @('MIHARI_DATA','MIHARI_INSTALL_ROOT','ProgramFiles')) {
      [Environment]::SetEnvironmentVariable($key, [string]$plan.Environment.$key, 'Process')
    }
  }
  Invoke-ReplacementAction $plan
  exit 0
} catch {
  Write-Error $_ -ErrorAction Continue
  exit 1
}
'@
    [IO.File]::WriteAllText($worker, $code, (New-Object Text.UTF8Encoding($true)))
    $process = Start-Process -FilePath powershell -Verb RunAs -Wait -PassThru -ArgumentList @('-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File',('"' + $worker + '"'))
    if ($process.ExitCode -ne 0) { throw 'Elevated installation failed. Some installation steps may already have completed.' }
  } finally { if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force } }
}

function Set-ReplacementWrittenTarget($Preview, [string]$Path) {
  # Advance only the file this operation just wrote; all other observations,
  # including the service definition and service copy, remain the original ones.
  $file = Get-ReplacementFile $Path
  if ($file.Digest -cne $Preview.CandidateDigest) { throw 'Mihari was copied, but its installed content changed. Further installation stopped.' }
  $found = $false
  foreach ($target in $Preview.Targets) {
    $sameEntry = $target.File.Path -ieq $file.Path
    # A missing entry has no native final-path spelling yet. Only that case
    # falls back to the original absolute destination selected by this command.
    if ($sameEntry -or (-not $target.File.Exists -and (Convert-ReplacementPath $target.SourcePath) -ieq (Convert-ReplacementPath $Path))) {
      $target.File = $file
      $found = $true
    }
  }
  if (-not $found) { throw 'Copied target was absent from the confirmed installation.' }
}
# END REPLACEMENT CONFIRMATION

if (-not $BundleDir) { $BundleDir = if ($PSScriptRoot) { $PSScriptRoot } else { (Get-Location).Path } }
$BundleDir = Convert-ReplacementPath $BundleDir
if (-not $Channel -and $env:MIHARI_CHANNEL) { $Channel = $env:MIHARI_CHANNEL }
if ($Channel -and $Channel -cnotin @('main', 'dev')) { Fail 'mihari channel must be main or dev' }
$mihariSrc = Join-Path $BundleDir 'mihari.exe'
$mihomoSrc = Join-Path $BundleDir 'data\bin\mihomo.exe'
if (-not (Test-Path -LiteralPath $mihariSrc)) { Fail "all-in-one bundle not found at $BundleDir (expected mihari.exe)" }
if (-not (Test-Path -LiteralPath $mihomoSrc)) { Fail "bundled mihomo core missing at $mihomoSrc" }
if (-not (Test-Path -LiteralPath (Join-Path $BundleDir 'data\geoip\GeoLite2-Country.mmdb'))) { Fail "bundled GeoIP Country missing at $BundleDir\data\geoip" }
if (-not (Test-Path -LiteralPath (Join-Path $BundleDir 'data\geoip\GeoLite2-ASN.mmdb'))) { Fail "bundled GeoIP ASN missing at $BundleDir\data\geoip" }

$binDir = if ($env:MIHARI_BIN) { $env:MIHARI_BIN } else { Join-Path $env:LOCALAPPDATA 'Programs\mihari' }
$binDir = Convert-ReplacementPath $binDir
$dataDir = if ($env:MIHARI_DATA) { $env:MIHARI_DATA } else { Join-Path $env:USERPROFILE '.mihari' }
$dataDir = Convert-ReplacementPath $dataDir
$dest = Join-Path $binDir 'mihari.exe'

$isAdmin = $false
if ($env:MIHARI_INSTALL_TEST_MODE -ne '1') {
  $isAdmin = ([Security.Principal.WindowsPrincipal] `
    [Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
}

$serviceView = Get-ReplacementService
$targets = @($dest)
if ($env:MIHARI_INSTALL_TEST_MODE -ne '1') { $targets += Get-ReplacementServiceDestination }
$preview = Get-ReplacementPreview -Candidate $mihariSrc -TargetVersion '' -Targets $targets
if ($preview.Service -cne $serviceView.Definition) { throw 'Service installation changed. Retry the installation.' }
$processes = @()
if ($env:MIHARI_INSTALL_TEST_MODE -ne '1') { $processes = @(Get-Process -Name mihari -ErrorAction SilentlyContinue) }
if ($processes.Count -gt 0) { $preview.StopRequired = $true }
if (-not (Confirm-Replacement $preview ($env:MIHARI_YES -eq '1'))) { throw 'Cancelled. No installation changes were made.' }
Assert-ReplacementPreview $preview $mihariSrc $targets
$changed = $false
try {
  if ($preview.StopRequired) {
    Invoke-ReplacementElevated ([pscustomobject]@{ Action='Stop'; Preview=$preview; Candidate=$mihariSrc; Targets=$targets; StopProcesses=($processes.Count -gt 0) })
    $changed = $true
  }
  Assert-ReplacementPreview $preview $mihariSrc $targets
  New-Item -ItemType Directory -Force -Path $binDir | Out-Null

  # 1. mihari binary -> binDir.
  Info "Installing mihari to $dest"
  Assert-ReplacementPreview $preview $mihariSrc $targets
  Copy-Item -LiteralPath $mihariSrc -Destination $dest -Force
  $changed = $true
  Set-ReplacementWrittenTarget $preview $dest
  Assert-ReplacementPreview $preview $mihariSrc $targets

  # Add install dir to the user PATH if missing (mirrors install.ps1).
  if ($env:MIHARI_INSTALL_TEST_MODE -ne '1') {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -and $userPath -notlike "*$binDir*") {
      Info "Adding $binDir to PATH"
      [Environment]::SetEnvironmentVariable('Path', "$userPath;$binDir", 'User')
      $env:Path = "$env:Path;$binDir"
    }
  }

  # 2. Data overlay -> MIHARI_DATA (bundle authoritative for core + GeoIP; user
  #    config / panel state below is never touched).
  New-Item -ItemType Directory -Force -Path (Join-Path $dataDir 'bin') | Out-Null
  New-Item -ItemType Directory -Force -Path (Join-Path $dataDir 'geoip') | Out-Null
  Info "Replacing the mihomo core and GeoIP files in $dataDir"
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

  # 3. The service command stages the machine copy. Its original observation is
  # retained after writing the PATH copy, including across the existing UAC step.
  Assert-ReplacementPreview $preview $dest $targets
  $serviceArgs = if ($serviceView.Path) { @('service','reinstall') } else { @('service','install') }
  Invoke-ReplacementElevated ([pscustomobject]@{ Action='Service'; Preview=$preview; Candidate=$dest; Targets=$targets; ServiceArgs=$serviceArgs; Environment=(Get-ReplacementEnvironment) })
  if (-not $serviceView.Path) {
    if ($isAdmin) { & $dest service start; if ($LASTEXITCODE -ne 0) { throw 'Service start failed.' } }
    else {
      $process = Start-Process -FilePath $dest -ArgumentList @('service','start') -Verb RunAs -Wait -PassThru
      if ($process.ExitCode -ne 0) { throw 'Service start failed.' }
    }
  }
} catch {
  if ($changed) { Write-Warning 'Some installation steps completed, but installation did not finish. The program or bundled data may already have changed.' }
  throw
}
Write-Host "`nAll-in-one installation completed. Restart your terminal, then run mihari to get started." -ForegroundColor Green
