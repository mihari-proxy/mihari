"""Run the production POSIX confirmation functions with isolated fake helpers."""
import json
import os
from pathlib import Path
import shlex
import subprocess
import pytest

INSTALL = Path(__file__).parent


def confirmation_error(preview="a" * 64):
    return {"schema": "mihari.error/v1", "error": {
        "code": "invalid_argument", "message": "Older Mihari versions may fail to load current data.",
        "details": {"reason": "replacement_confirmation_required", "risk": "downgrade",
                    "targets": [{"roles": ["service"], "version": "v2.0.0"}],
                    "target_version": "v1.0.0", "preview_id": preview}}}


def run_posix(tmp_path, error=None, consent="accept", explicit=False, second=0):
    if os.name != "posix":
        pytest.skip("Native POSIX helper fixture paths require a POSIX host")
    source = (INSTALL / "root-apply.sh.in").read_text()
    if "# BEGIN REPLACEMENT CONFIRMATION\n" in source:
        block = source.split("# BEGIN REPLACEMENT CONFIRMATION\n", 1)[1].split("# END REPLACEMENT CONFIRMATION", 1)[0]
    else:
        block = 'apply_with_confirmation() { "$entry" service apply --request "$stage/request.json" --json; }'
    fixture = tmp_path / "fixture"
    fixture.write_text(error if isinstance(error, str) else json.dumps(error or confirmation_error(), separators=(",", ":")) + "\n")
    helper = tmp_path / "helper"
    helper.write_text('#!/bin/sh\nprintf "%s\\n" "$@" >> "$ARG_LOG"\n'
                      'if [ ! -f "$COUNT" ]; then : > "$COUNT"; cat "$FIXTURE" >&2; exit 2; fi\n'
                      'exit "$SECOND"\n')
    helper.chmod(0o700)
    stage = tmp_path / "stage"
    stage.mkdir()
    override = {"accept": "confirm_replacement() { return 0; }", "cancel": "confirm_replacement() { return 1; }", "real": ""}[consent]
    command = '\n'.join(["set -eu", "umask 077", "stage=" + shlex.quote(str(stage)),
                          "entry=" + shlex.quote(str(helper)), "explicit_yes=" + str(int(explicit)),
                          'fail() { printf "%s\\n" "$1" >&2; exit 1; }', block, override, "apply_with_confirmation"])
    env = dict(os.environ, ARG_LOG=str(tmp_path / "argv"), COUNT=str(tmp_path / "count"), FIXTURE=str(fixture), SECOND=str(second))
    result = subprocess.run(["sh", "-c", command], env=env, capture_output=True, text=True, timeout=10, start_new_session=True)
    return result, (tmp_path / "argv").read_text().splitlines()


@pytest.mark.skipif(os.name != "posix", reason="POSIX shell")
def test_posix_confirmation_reuses_original_preview(tmp_path):
    result, args = run_posix(tmp_path)
    assert result.returncode == 0, result.stderr
    assert args.count("--expected-preview") == 1
    assert args[args.index("--expected-preview") + 1] == "a" * 64
    assert args.count("--yes") == 1


@pytest.mark.parametrize("mutation", ["duplicate", "bad", "wrong_code", "empty", "uppercase", "multiline", "trailing", "control", "oversize", "reason"])
def test_posix_invalid_error_never_retries(tmp_path, mutation):
    value = confirmation_error()
    if mutation == "wrong_code": value["error"]["code"] = "invalid_state"
    if mutation == "reason": value["error"]["details"]["reason"] = "other"
    if mutation == "empty": value["error"]["details"]["preview_id"] = ""
    if mutation == "uppercase": value["error"]["details"]["preview_id"] = "A" * 64
    raw = json.dumps(value, separators=(",", ":"))
    if mutation == "duplicate": raw = raw.replace('"preview_id":', '"preview_id":"' + 'b' * 64 + '","preview_id":')
    if mutation == "bad": raw = "invalid JSON"
    if mutation == "multiline": raw = raw.replace(',"error"', ',\n"error"')
    if mutation == "trailing": raw += '{}'
    if mutation == "control": raw = raw.replace("Older", "\\u001bOlder")
    if mutation == "oversize": raw = raw.replace("Older", "x" * 70000)
    result, args = run_posix(tmp_path, raw + "\n")
    assert result.returncode != 0
    assert args.count("apply") == 1


@pytest.mark.parametrize("consent", ["cancel", "real"])
def test_posix_cancel_or_no_terminal_never_retries(tmp_path, consent):
    result, args = run_posix(tmp_path, consent=consent)
    assert result.returncode != 0
    assert args.count("apply") == 1


def test_posix_changed_preview_never_third_call(tmp_path):
    result, args = run_posix(tmp_path, second=2)
    assert result.returncode == 2
    assert args.count("apply") == 2


def test_posix_explicit_yes_first_call_only(tmp_path):
    result, args = run_posix(tmp_path, explicit=True)
    assert result.returncode == 2
    assert args.count("apply") == 1
    assert args.count("--yes") == 1
    assert "--expected-preview" not in args


@pytest.mark.skipif(os.name != "posix", reason="POSIX controlling terminal")
@pytest.mark.parametrize("answer,accepted", [("y\n", True), ("n\n", False), ("\n", False)])
def test_posix_real_terminal_defaults_no(tmp_path, answer, accepted):
    import pty
    import select
    import time
    source = (INSTALL / "root-apply.sh.in").read_text()
    block = source.split("# BEGIN REPLACEMENT CONFIRMATION\n", 1)[1].split("# END REPLACEMENT CONFIRMATION", 1)[0]
    fixture = tmp_path / "error"
    fixture.write_text(json.dumps(confirmation_error(), separators=(",", ":")) + "\n")
    pid, fd = pty.fork()
    if pid == 0:
        os.execv("/bin/sh", ["sh", "-c", block + '\nconfirm_replacement ' + shlex.quote(str(fixture))])
    output = b""
    try:
        deadline = time.monotonic() + 5
        while b"[y/N]" not in output and time.monotonic() < deadline:
            ready, _, _ = select.select([fd], [], [], 0.1)
            if ready:
                output += os.read(fd, 8192)
        assert b"[y/N]" in output, output
        assert b"service: v2.0.0 -> v1.0.0" in output
        os.write(fd, answer.encode())
        # Drain terminal echo while waiting: BSD PTY slave close may wait for
        # the master to consume queued output before the child can exit.
        while time.monotonic() < deadline:
            ready, _, _ = select.select([fd], [], [], 0.01)
            if ready:
                try:
                    output += os.read(fd, 8192)
                except OSError as exc:
                    import errno
                    if exc.errno != errno.EIO:  # Linux PTY EOF
                        raise
            waited, status = os.waitpid(pid, os.WNOHANG)
            if waited:
                assert (os.waitstatus_to_exitcode(status) == 0) is accepted
                return
        pytest.fail("terminal confirmation did not exit")
    finally:
        os.close(fd)
        try:
            os.kill(pid, 9)
            os.waitpid(pid, 0)
        except ProcessLookupError:
            pass


def test_posix_go_encoding_json_order_and_html_escape(tmp_path):
    error = confirmation_error()
    error["error"]["message"] += " service: v2.0.0 -> v1.0.0."
    raw = json.dumps(error, sort_keys=True, separators=(",", ":")).replace(">", "\\u003e") + "\n"
    result, args = run_posix(tmp_path, raw)
    assert result.returncode == 0, result.stderr
    assert args.count("apply") == 2


@pytest.mark.skipif(os.name != "posix", reason="Native POSIX shell argument paths")
@pytest.mark.parametrize("env_yes,remote_yes,want", [("1", "0", "1"), ("true", "0", "0"), ("01", "0", "0"), ("", "1", "1"), ("", "0", "0")])
@pytest.mark.parametrize("bootstrap_mode", ["", "offline", "online"])
def test_posix_yes_crosses_clean_environment_as_position(tmp_path, env_yes, remote_yes, want, bootstrap_mode):
    source = (INSTALL / "root-apply.sh.in").read_text()
    # Replace only the privileged process-launch boundary with an argv recorder.
    source = source.replace('$elevate /usr/bin/env -i', 'capture /usr/bin/env -i', 1)
    command = '\n'.join(['set -eu', 'id() { printf "0\\n"; }', 'capture() { printf "%s\\n" "$@"; }', source,
                          'root_apply v1.0.0 main /candidate /bundle ' + remote_yes + ' ' + bootstrap_mode])
    result = subprocess.run(["sh", "-c", command], env=dict(os.environ, MIHARI_YES=env_yes), capture_output=True, text=True, timeout=5)
    assert result.returncode == 0, result.stderr
    args = result.stdout.splitlines()
    assert args[:3] == ["/usr/bin/env", "-i", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"]
    assert args[-2:] == [want, bootstrap_mode or "offline"]
    assert args[args.index("--") + 1:][:4] == ["v1.0.0", "main", "/candidate", "/bundle"]


# Both standalone Windows installers run this same semantic fixture. On Windows
# prefer Windows PowerShell 5.1; pwsh on Unix provides additional portable checks.
import shutil


def ps_executable():
    return shutil.which("powershell") or shutil.which("pwsh")


def ps_literal(value):
    return "'" + str(value).replace("'", "''") + "'"


def run_ps(tmp_path, source, env=None):
    exe = ps_executable()
    if exe is None:
        pytest.skip("PowerShell unavailable; Windows PowerShell 5.1 acceptance remains required")
    driver = tmp_path / "replacement-driver.ps1"
    driver.write_text("$ErrorActionPreference='Stop'\n" + source, encoding="utf-8")
    return subprocess.run([exe, "-NoProfile", "-NonInteractive", "-File", str(driver)],
                          env=env, capture_output=True, text=True, timeout=20)


def ps_replacement_block(name):
    source = (INSTALL / name).read_text()
    if "# BEGIN REPLACEMENT CONFIRMATION\n" not in source:
        return "function Get-ReplacementRisk([string]$Current,[string]$Target) { return 'none' }\n"
    return source.split("# BEGIN REPLACEMENT CONFIRMATION\n", 1)[1].split("# END REPLACEMENT CONFIRMATION", 1)[0]


@pytest.mark.parametrize("name", ["install.ps1", "install-aio.ps1"])
def test_windows_shared_version_matrix(tmp_path, name):
    fixture = INSTALL / "testdata/replacement_versions.json"
    result = run_ps(tmp_path, ps_replacement_block(name) + '\n' +
                    '$cases = Get-Content -Raw -LiteralPath ' + ps_literal(fixture) + ' | ConvertFrom-Json\n' +
                    '@($cases | ForEach-Object { Get-ReplacementRisk $_.current $_.target }) | ConvertTo-Json -Compress\n')
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) == [case["risk"] for case in json.loads(fixture.read_text())]


def test_windows_capabilities_does_not_touch_bundle(tmp_path):
    missing = tmp_path / "missing"
    result = run_ps(tmp_path, '& ' + ps_literal(INSTALL / "install-aio.ps1") +
                    ' -Capabilities -BundleDir ' + ps_literal(missing))
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) == {"schema": "mihari.install-script/v1", "capabilities": ["replacement_confirmation_v1"]}
    assert not missing.exists()


@pytest.mark.parametrize("explicit,expected", [(False, False), (True, True)])
def test_windows_aio_unknown_candidate_requires_consent_before_overlay(tmp_path, explicit, expected):
    bundle = tmp_path / 'bundle'
    for name in ['mihari.exe', 'data/bin/mihomo.exe', 'data/geoip/GeoLite2-Country.mmdb', 'data/geoip/GeoLite2-ASN.mmdb']:
        file = bundle / name
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_bytes(b'candidate')
    installed = tmp_path / 'installed'
    installed.mkdir()
    (installed / 'mihari.exe').write_bytes(b'old')
    data = tmp_path / 'data'
    data.mkdir()
    (data / 'mihari-channel').write_bytes(b'main\n')
    env = dict(os.environ, MIHARI_INSTALL_TEST_MODE='1', MIHARI_BIN=str(installed), MIHARI_DATA=str(data),
               USERPROFILE=str(tmp_path / 'profile'), LOCALAPPDATA=str(tmp_path / 'local'), MIHARI_YES='1' if explicit else '')
    result = run_ps(tmp_path, '& ' + ps_literal(INSTALL / 'install-aio.ps1') + ' -BundleDir ' + ps_literal(bundle) + ' -Channel dev', env)
    assert (result.returncode == 0) is expected, result.stderr
    if expected:
        assert (installed / 'mihari.exe').read_bytes() == b'candidate'
        assert 'compatibility' in result.stdout.lower()
    else:
        assert (installed / 'mihari.exe').read_bytes() == b'old'
        assert (data / 'mihari-channel').read_bytes() == b'main\n'
        assert list(data.iterdir()) == [data / 'mihari-channel']


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
@pytest.mark.parametrize('mutation', ['candidate', 'target', 'service', 'targets'])
def test_windows_preview_rejects_changed_inputs(tmp_path, name, mutation):
    candidate = tmp_path / 'candidate.exe'
    target = tmp_path / 'installed.exe'
    candidate.write_bytes(b'new')
    target.write_bytes(b'old')
    source = ps_replacement_block(name) + '\n'
    source += "function Get-ReplacementService { [pscustomobject]@{ Definition=$script:definition; Running=$false } }\n"
    source += "$script:definition='original'\n"
    source += '$candidate=' + ps_literal(candidate) + '\n$targets=@(' + ps_literal(target) + ')\n'
    source += "$p=Get-ReplacementPreview $candidate 'v1.0.0' $targets\n"
    if mutation == 'candidate': source += "[IO.File]::WriteAllText($candidate,'changed')\n"
    if mutation == 'target': source += "[IO.File]::WriteAllText($targets[0],'changed')\n"
    if mutation == 'service': source += "$script:definition='changed'\n"
    if mutation == 'targets': source += "$targets += $candidate\n"
    source += 'Assert-ReplacementPreview $p $candidate $targets\n'
    result = run_ps(tmp_path, source)
    assert result.returncode != 0
    assert 'changed' in result.stderr.lower(), result.stderr


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_fresh_install_does_not_require_yes(tmp_path, name):
    candidate = tmp_path / 'candidate.exe'
    candidate.write_bytes(b'new')
    source = ps_replacement_block(name) + '\n'
    source += "function Get-ReplacementService { [pscustomobject]@{ Definition=''; Running=$false } }\n"
    source += '$p=Get-ReplacementPreview ' + ps_literal(candidate) + " '' @(" + ps_literal(tmp_path / 'missing.exe') + ')\n'
    source += 'Confirm-Replacement $p $false | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) is True


def test_windows_ordinary_fixed_tag_download_before_confirmation(tmp_path):
    # Replace OS/network boundaries only. Run the real installer, including its
    # download and confirmation point; pre-confirmation writes are observable.
    installed = tmp_path / 'installed'
    installed.mkdir()
    old = installed / 'mihari.exe'
    old.write_bytes(b'old')
    events = tmp_path / 'events'
    source = "function Get-CimInstance { return $null }\n"
    source += "function Get-Service { return $null }\n"
    source += "function Invoke-RestMethod { [pscustomobject]@{tag_name='v1.0.0'} }\n"
    source += "function Invoke-WebRequest { param($Uri,$OutFile,[switch]$UseBasicParsing) [IO.File]::WriteAllText($OutFile,'new'); [IO.File]::AppendAllText(" + ps_literal(events) + ",$Uri+\"`n\") }\n"
    source += '$code = Get-Content -Raw -LiteralPath ' + ps_literal(INSTALL / 'install.ps1') + '\n'
    source += "$code = [regex]::Replace($code, '(?s)\\$isAdmin = \\(\\[Security.Principal.WindowsPrincipal\\].*?WindowsBuiltinRole]::Administrator\\)', '$isAdmin = $false')\n"
    source += '& ([scriptblock]::Create($code))'
    env = dict(os.environ, MIHARI_BIN=str(installed), MIHARI_DATA=str(tmp_path / 'data'),
               USERPROFILE=str(tmp_path / 'profile'), LOCALAPPDATA=str(tmp_path / 'local'),
               MIHARI_NO_INSTALL='1', MIHARI_YES='', PROCESSOR_ARCHITECTURE='AMD64')
    env.pop('MIHARI_INSTALL_TEST_MODE', None)
    result = run_ps(tmp_path, source, env)
    assert result.returncode != 0
    assert old.read_bytes() == b'old'
    assert events.exists(), result.stderr
    assert '/releases/download/v1.0.0/' in events.read_text()
    assert 'Confirmation is required' in result.stderr


def test_windows_ordinary_service_started_during_download_uses_confirmed_stop(tmp_path):
    installed = tmp_path / 'installed'
    installed.mkdir()
    dest = installed / 'mihari.exe'
    dest.write_bytes(b'old')
    events = tmp_path / 'events'
    source = "$global:replacementServiceState='Stopped'\n"
    source += '$global:replacementEvents=' + ps_literal(events) + '\n'
    source += "function Get-CimInstance { [pscustomobject]@{PathName=" + ps_literal('"' + str(dest) + '" daemon') + "; StartName='LocalSystem'; StartMode='Auto'; ServiceType='Own Process'; State=$global:replacementServiceState} }\n"
    source += "function Invoke-RestMethod { [pscustomobject]@{tag_name='v1.0.0'} }\n"
    source += "function Invoke-WebRequest { param($Uri,$OutFile,[switch]$UseBasicParsing) [IO.File]::WriteAllText($OutFile,'new'); $global:replacementServiceState='Running' }\n"
    source += "function Stop-Service { param($Name,[switch]$Force) $global:replacementServiceState='Stopped'; [IO.File]::AppendAllText($global:replacementEvents,\"stop`n\") }\n"
    source += "function Start-Service { param($Name) $global:replacementServiceState='Running'; [IO.File]::AppendAllText($global:replacementEvents,\"start`n\") }\n"
    source += "function Copy-Item { param($LiteralPath,$Destination,[switch]$Force) if ($global:replacementServiceState -eq 'Running') { throw 'The executable is locked by the running service.' }; Microsoft.PowerShell.Management\\Copy-Item -LiteralPath $LiteralPath -Destination $Destination -Force; [IO.File]::AppendAllText($global:replacementEvents,\"copy`n\") }\n"
    source += '$code=Get-Content -Raw -LiteralPath ' + ps_literal(INSTALL / 'install.ps1') + '\n'
    # Isolate the administrator-token/PATH reads, while retaining the real
    # preview, confirmation, swap action, content checks and file replacement.
    source += "$code=[regex]::Replace($code,'(?s)\\$isAdmin = \\(\\[Security.Principal.WindowsPrincipal\\].*?WindowsBuiltinRole]::Administrator\\)','$isAdmin = $true')\n"
    source += '$code=$code.Replace(' + ps_literal("[Environment]::GetEnvironmentVariable('Path', 'User')") + ", '$binDir')\n"
    source += '& ([scriptblock]::Create($code))\n'
    env = dict(os.environ, MIHARI_BIN=str(installed), MIHARI_DATA=str(tmp_path / 'data'),
               USERPROFILE=str(tmp_path / 'profile'), LOCALAPPDATA=str(tmp_path / 'local'),
               MIHARI_NO_INSTALL='1', MIHARI_YES='1', MIHARI_CHANNEL='', MIHARI_VERSION='', PROCESSOR_ARCHITECTURE='AMD64')
    env.pop('MIHARI_INSTALL_TEST_MODE', None)
    result = run_ps(tmp_path, source, env)
    assert result.returncode == 0, result.stderr
    assert 'will be stopped for installation' in result.stdout
    assert dest.read_bytes() == b'new'
    assert events.read_text().splitlines() == ['stop', 'copy', 'start']


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_native_probe_is_bounded_and_isolated(tmp_path, name):
    if os.name != 'nt':
        pytest.skip('Requires native Windows PowerShell 5.1 executable fixture')
    # A .NET Framework executable built by Windows PowerShell, not a real Mihari.
    exe = shutil.which('powershell')
    assert exe, 'Windows PowerShell 5.1 must be available in the Windows job'
    child = tmp_path / 'probe.exe'
    code = r'''using System;
class Probe {
 static void Main() {
  if (Environment.GetEnvironmentVariable("MIHARI_PROBE_SECRET") != null) { Environment.Exit(9); }
  string mode = System.IO.File.ReadAllText(System.IO.Path.Combine(AppDomain.CurrentDomain.BaseDirectory,"mode"));
  if (mode == "hang") { System.Threading.Thread.Sleep(30000); }
  if (mode == "flood") { Console.Write(new string('x',100000)); return; }
  Console.Write("{\"schema\":\"mihari/v1\",\"version\":\"v2.0.0\"}");
 }
}'''
    source = ps_replacement_block(name) + '\nAdd-Type -TypeDefinition ' + ps_literal(code) + ' -OutputAssembly ' + ps_literal(child) + ' -OutputType ConsoleApplication\n'
    source += 'Initialize-ReplacementNative\n$env:MIHARI_PROBE_SECRET="do-not-inherit"\n'
    source += '$root=' + ps_literal(tmp_path / 'isolation') + '\nNew-Item -ItemType Directory $root | Out-Null\n'
    source += '$child=' + ps_literal(child) + '\n$mode=' + ps_literal(tmp_path / 'mode') + '\n'
    source += "[IO.File]::WriteAllText($mode,'good')\n$good=[MihariReplacementNative]::Probe($child,$root)\n"
    source += "[IO.File]::WriteAllText($mode,'hang')\n$t=[Diagnostics.Stopwatch]::StartNew(); $hang=[MihariReplacementNative]::Probe($child,$root); $elapsed=$t.ElapsedMilliseconds\n"
    source += "[IO.File]::WriteAllText($mode,'flood')\n$flood=[MihariReplacementNative]::Probe($child,$root)\n"
    source += '@{good=$good; hang=$hang; flood=$flood; elapsed=$elapsed} | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    got = json.loads(result.stdout)
    assert json.loads(got['good'])['version'] == 'v2.0.0'
    assert got['hang'] is None
    assert got['flood'] is None
    assert got['elapsed'] < 6000


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_service_copy_change_after_main_write_stops_service_action(tmp_path, name):
    candidate = tmp_path / 'candidate.exe'
    dest = tmp_path / 'dest.exe'
    service = tmp_path / 'service.exe'
    candidate.write_bytes(b'new')
    dest.write_bytes(b'old')
    service.write_bytes(b'old-service')
    events = tmp_path / 'events'
    source = ps_replacement_block(name) + '\n'
    source += "function Get-ReplacementService { [pscustomobject]@{Definition='same'; Running=$true} }\n"
    source += "function Stop-Service { [IO.File]::WriteAllText(" + ps_literal(events) + ",'stopped') }\n"
    source += '$candidate=' + ps_literal(candidate) + '\n$dest=' + ps_literal(dest) + '\n$targets=@($dest,' + ps_literal(service) + ')\n'
    source += "$p=Get-ReplacementPreview $candidate 'v1.0.0' $targets\nCopy-Item $candidate $dest -Force\nSet-ReplacementWrittenTarget $p $dest\n"
    source += '[IO.File]::WriteAllText(' + ps_literal(service) + ",'changed')\n"
    source += "Invoke-ReplacementAction ([pscustomobject]@{Action='Service'; Preview=$p; Candidate=$dest; Targets=$targets; ServiceArgs=@('service','reinstall')})\n"
    result = run_ps(tmp_path, source)
    assert result.returncode != 0
    assert not events.exists()
    assert dest.read_bytes() == b'new'
    assert service.read_bytes() == b'changed'


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_native_user_writable_old_binary_is_not_run_as_admin(tmp_path, name):
    if os.name != 'nt':
        pytest.skip('Requires Windows ACL and administrator token')
    source = ps_replacement_block(name) + '\n'
    source += "$admin=([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)\n"
    source += "if (-not $admin) { Write-Output 'not-admin'; exit 0 }\n"
    old = tmp_path / 'old.exe'
    old.write_bytes(b'not-executable')
    source += '$path=' + ps_literal(old) + '\n'
    source += "$acl=Get-Acl -LiteralPath $path\n$users=[Security.Principal.SecurityIdentifier]'S-1-5-32-545'\n"
    source += "$acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($users,'Modify','Allow')))\nSet-Acl -LiteralPath $path -AclObject $acl\n"
    source += 'Test-ReplacementProbeTrust $path | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    if result.stdout.strip() == 'not-admin':
        pytest.skip('Native host token is not elevated; elevated ACL acceptance remains required')
    assert json.loads(result.stdout) is False


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_service_action_preserves_argument_boundaries(tmp_path, name):
    candidate = tmp_path / 'candidate with spaces.ps1'
    argv = tmp_path / 'argv'
    candidate.write_text('@{count=$args.Count; firstIsString=($args[0] -is [string]); secondIsString=($args[1] -is [string]); values=@($args | ForEach-Object {[string]$_})} | ConvertTo-Json -Compress | Set-Content -LiteralPath ' + ps_literal(argv))
    source = ps_replacement_block(name) + '\n'
    source += "function Get-ReplacementService { [pscustomobject]@{Definition='same'; Running=$false} }\n"
    source += '$candidate=' + ps_literal(candidate) + '\n$targets=@($candidate)\n'
    source += "$p=Get-ReplacementPreview $candidate 'v1.0.0' $targets\n"
    source += "Invoke-ReplacementAction ([pscustomobject]@{Action='Service'; Preview=$p; Candidate=$candidate; Targets=$targets; ServiceArgs=@('service','reinstall')})\n"
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    assert json.loads(argv.read_text(encoding='utf-8-sig')) == {'count': 2, 'firstIsString': True, 'secondIsString': True, 'values': ['service', 'reinstall']}


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_full_flow_refuses_before_service_path_or_data_writes(tmp_path, name):
    root = tmp_path / 'root'
    installed = root / 'program'
    installed.mkdir(parents=True)
    (installed / 'mihari.exe').write_bytes(b'old-path')
    service = root / 'service'
    service.mkdir()
    (service / 'mihari.exe').write_bytes(b'old-service')
    data = root / 'data'
    data.mkdir()
    (data / 'mihari-channel').write_bytes(b'main\n')
    bundle = tmp_path / 'bundle'
    for item in ['mihari.exe','data/bin/mihomo.exe','data/geoip/GeoLite2-Country.mmdb','data/geoip/GeoLite2-ASN.mmdb']:
        path = bundle / item
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(b'new')
    events = tmp_path / 'mutation-events'
    before = {str(p.relative_to(root)): p.read_bytes() for p in root.rglob('*') if p.is_file()}
    source = "function Get-CimInstance { [pscustomobject]@{PathName=" + ps_literal('"' + str(service / 'mihari.exe') + '" daemon') + "; StartName='LocalSystem'; StartMode='Auto'; ServiceType='Own Process'; State='Running'} }\n"
    source += "function Get-Service { [pscustomobject]@{Status='Running'} }\nfunction Get-Process { return $null }\n"
    source += "function Stop-Service { [IO.File]::WriteAllText(" + ps_literal(events) + ",'stop') }\n"
    source += "function Start-Process { [IO.File]::WriteAllText(" + ps_literal(events) + ",'elevate') }\n"
    source += "function Invoke-RestMethod { [pscustomobject]@{tag_name='v1.0.0'} }\n"
    source += "function Invoke-WebRequest { param($Uri,$OutFile,[switch]$UseBasicParsing) [IO.File]::WriteAllText($OutFile,'new') }\n"
    source += '$code=Get-Content -Raw -LiteralPath ' + ps_literal(INSTALL / name) + '\n'
    source += "$code=[regex]::Replace($code,'(?s)\\$isAdmin = \\(\\[Security.Principal.WindowsPrincipal\\].*?WindowsBuiltinRole]::Administrator\\)','$isAdmin = $false')\n"
    source += '& ([scriptblock]::Create($code))'
    if name == 'install-aio.ps1': source += ' -BundleDir ' + ps_literal(bundle) + ' -Channel dev'
    env = dict(os.environ, MIHARI_BIN=str(installed), MIHARI_DATA=str(data), MIHARI_INSTALL_ROOT=str(service), MIHARI_CHANNEL='dev' if name == 'install-aio.ps1' else 'main',
               USERPROFILE=str(tmp_path / 'profile'), LOCALAPPDATA=str(tmp_path / 'local'), MIHARI_YES='', PROCESSOR_ARCHITECTURE='AMD64')
    env.pop('MIHARI_INSTALL_TEST_MODE', None)
    result = run_ps(tmp_path, source, env)
    assert result.returncode != 0
    assert 'Confirmation is required' in result.stderr, result.stderr
    assert not events.exists()
    assert before == {str(p.relative_to(root)): p.read_bytes() for p in root.rglob('*') if p.is_file()}


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
@pytest.mark.parametrize('raw,expected', [
    ('{"schema":"mihari/v1","version":"v2.0.0"}', 'v2.0.0'),
    ('{"version":" 1.2.3 ","schema":"mihari/v1"}', '1.2.3'),
    ('{"schema":"mihari/v1","version":"v2.0.0","version":"v1.0.0"}', ''),
    ('{"schema":"mihari/v1","version":"v2.0.0","\\u0076ersion":"v1.0.0"}', ''),
    ('{"schema":"mihari/v1","version":"v2.0.0"}{}', ''),
    ('{"schema":"wrong","version":"v2.0.0"}', ''),
    ('{"schema":"mihari/v1","version":"v999999999999999999999.0.0"}', ''),
])
def test_windows_version_probe_accepts_only_one_safe_envelope(tmp_path, name, raw, expected):
    # Replace the native process boundary, leaving production parsing and
    # temporary isolation/cleanup in place. No candidate is executed here.
    source = ps_replacement_block(name) + '\n'
    source += "Add-Type 'public static class MihariReplacementNative { public static string Raw; public static string Probe(string p,string r) { return Raw; } }'\n"
    source += "function Test-ReplacementProbeTrust { return $true }\n"
    source += '[MihariReplacementNative]::Raw=' + ps_literal(raw) + '\n'
    source += '(Get-ReplacementVersion ' + ps_literal(tmp_path / 'old.exe') + ') | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) == expected


@pytest.mark.parametrize('changed', [False, True])
def test_windows_native_uac_handoff_consumes_original_preview(tmp_path, changed):
    if os.name != 'nt':
        pytest.skip('Requires Windows ACLs for private UAC request directory')
    candidate = tmp_path / 'new.exe'
    target = tmp_path / 'target.exe'
    candidate.write_bytes(b'new')
    target.write_bytes(b'old')
    source = ps_replacement_block('install.ps1') + '\n'
    source += "function Get-ReplacementService { [pscustomobject]@{Definition=''; Running=$false} }\n"
    # Start the exact generated worker at the existing token, replacing only the
    # interactive UAC launch. It receives the production private JSON handoff.
    source += "function Start-Process { param($FilePath,$Verb,[switch]$Wait,[switch]$PassThru,$ArgumentList)\n"
    if changed: source += '[IO.File]::WriteAllText(' + ps_literal(target) + ",'changed-after-preview')\n"
    source += "$worker=$ArgumentList[-1].Trim('\"')\n& powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $worker\n[pscustomobject]@{ExitCode=$LASTEXITCODE}\n}\n"
    source += '$isAdmin=$false\n$candidate=' + ps_literal(candidate) + '\n$targets=@(' + ps_literal(target) + ')\n'
    source += "$p=Get-ReplacementPreview $candidate 'v1.0.0' $targets\n"
    source += 'Invoke-ReplacementElevated ([pscustomobject]@{Action="Swap"; Preview=$p; Candidate=$candidate; Targets=$targets; Destination=$targets[0]})\n'
    env = dict(os.environ)
    env.pop('MIHARI_INSTALL_TEST_MODE', None)
    result = run_ps(tmp_path, source, env)
    assert (result.returncode == 0) is (not changed), result.stderr
    if changed:
        assert 'Installation changed' in result.stderr, result.stderr
    assert target.read_bytes() == (b'changed-after-preview' if changed else b'new')


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_uac_environment_binds_original_relative_roots(tmp_path, name):
    source = ps_replacement_block(name) + '\n'
    source += 'Set-Location -LiteralPath ' + ps_literal(tmp_path) + '\n'
    source += "$env:MIHARI_INSTALL_ROOT='relative-install'\n$env:MIHARI_DATA='relative-data'\n"
    source += '$expected=Get-ReplacementServiceDestination\n$transfer=Get-ReplacementEnvironment\n'
    source += 'Set-Location -LiteralPath ' + ps_literal(tmp_path.parent) + '\n'
    source += '$env:MIHARI_INSTALL_ROOT=$transfer.MIHARI_INSTALL_ROOT\n$env:MIHARI_DATA=$transfer.MIHARI_DATA\n'
    source += '@{before=$expected; after=(Get-ReplacementServiceDestination); data=[IO.Path]::GetFullPath($env:MIHARI_DATA)} | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    result = json.loads(result.stdout)
    assert Path(result['before']) == tmp_path / 'relative-install' / 'mihari.exe'
    assert result['before'] == result['after']
    assert Path(result['data']) == tmp_path / 'relative-data'


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_written_target_uses_observed_canonical_path_not_alias_spelling(tmp_path, name):
    candidate = tmp_path / 'candidate.exe'
    dest = tmp_path / 'installed.exe'
    service = tmp_path / 'service.exe'
    candidate.write_bytes(b'new')
    dest.write_bytes(b'old')
    service.write_bytes(b'old-service')
    source = ps_replacement_block(name) + '\n'
    source += "function Get-ReplacementService { [pscustomobject]@{Definition='same'; Running=$false} }\n"
    source += '$candidate=' + ps_literal(candidate) + '\n$dest=' + ps_literal(dest) + '\n$targets=@($dest,' + ps_literal(service) + ')\n'
    source += "$p=Get-ReplacementPreview $candidate 'v1.0.0' $targets\n"
    # Model the observation saved when Windows resolved an alternate short-path
    # spelling: File.Path remains the actual observed canonical directory entry.
    source += '$entry=$p.Targets | Where-Object {$_.SourcePath -eq $dest}\n$entry.SourcePath=' + ps_literal(tmp_path / 'INSTAL~1.EXE') + '\n'
    source += '$otherBefore=($p.Targets | Where-Object {$_.File.Path -ne $entry.File.Path}) | ConvertTo-Json -Depth 8 -Compress\n'
    source += 'Copy-Item -LiteralPath $candidate -Destination $dest -Force\nSet-ReplacementWrittenTarget $p $dest\n'
    source += '$otherAfter=($p.Targets | Where-Object {$_.File.Path -ne $entry.File.Path}) | ConvertTo-Json -Depth 8 -Compress\n'
    source += 'Assert-ReplacementPreview $p $candidate $targets\n($otherBefore -ceq $otherAfter) | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) is True


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_written_target_keeps_distinct_hardlink_observation(tmp_path, name):
    candidate = tmp_path / 'candidate.exe'
    dest = tmp_path / 'installed.exe'
    linked = tmp_path / 'service.exe'
    candidate.write_bytes(b'new')
    dest.write_bytes(b'old')
    os.link(dest, linked)
    source = ps_replacement_block(name) + '\n'
    source += "function Get-ReplacementService { [pscustomobject]@{Definition='same'; Running=$false} }\n"
    source += '$candidate=' + ps_literal(candidate) + '\n$dest=' + ps_literal(dest) + '\n$linked=' + ps_literal(linked) + '\n$targets=@($dest,$linked)\n'
    source += "$p=Get-ReplacementPreview $candidate 'v1.0.0' $targets\n"
    source += '$before=($p.Targets | Where-Object {$_.SourcePath -eq $linked}) | ConvertTo-Json -Depth 8 -Compress\n'
    source += 'Copy-Item -LiteralPath $candidate -Destination $dest -Force\nSet-ReplacementWrittenTarget $p $dest\n'
    source += '$after=($p.Targets | Where-Object {$_.SourcePath -eq $linked}) | ConvertTo-Json -Depth 8 -Compress\n'
    source += '@{count=$p.Targets.Count; preserved=($before -ceq $after)} | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) == {'count': 2, 'preserved': True}


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
@pytest.mark.parametrize('fresh', [False, True])
def test_windows_native_short_path_written_target(tmp_path, name, fresh):
    if os.name != 'nt':
        pytest.skip('Requires Windows short-path resolution')
    candidate = tmp_path / 'candidate.exe'
    dest = (tmp_path / 'Long installed program directory' / 'mihari.exe') if fresh else (tmp_path / 'installed executable with long name.exe')
    candidate.write_bytes(b'new')
    if fresh:
        dest.parent.mkdir()
    else:
        dest.write_bytes(b'old')
    source = ps_replacement_block(name) + '\n'
    source += r'''Add-Type 'using System.Text; using System.Runtime.InteropServices; public static class MihariShortPath { [DllImport("kernel32.dll", CharSet=CharSet.Unicode)] public static extern uint GetShortPathName(string p, StringBuilder s, uint n); }'
'''
    source += "function Get-ReplacementService { [pscustomobject]@{Definition='same'; Running=$false} }\n"
    source += '$candidate=' + ps_literal(candidate) + '\n$dest=' + ps_literal(dest) + '\n'
    source += '$query=' + ps_literal(dest.parent if fresh else dest) + '\n'
    source += '$buffer=New-Object Text.StringBuilder(32768)\n$n=[MihariShortPath]::GetShortPathName($query,$buffer,32768)\n$short=$buffer.ToString()\n'
    if fresh: source += "$short=Join-Path $short 'mihari.exe'\n"
    source += "if ($n -eq 0 -or $short -ieq $dest) { Write-Output 'no-short-name'; exit 0 }\n"
    source += "$p=Get-ReplacementPreview $candidate 'v1.0.0' @($short,$dest)\nif ($p.Targets.Count -ne 1) { throw 'Alias targets were not deduplicated.' }\nCopy-Item $candidate $dest -Force\nSet-ReplacementWrittenTarget $p $dest\nAssert-ReplacementPreview $p $candidate @($short)\nWrite-Output 'accepted'\n"
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    if result.stdout.strip() == 'no-short-name':
        pytest.skip('Native filesystem does not provide a short-path alias')
    assert result.stdout.strip() == 'accepted'


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_fresh_alias_dedup_keeps_one_canonical_entry_after_copy(tmp_path, name):
    candidate = tmp_path / 'candidate.exe'
    candidate.write_bytes(b'new')
    short_dir = tmp_path / 'a-short-directory'
    long_dir = tmp_path / 'z-long-directory'
    short_dir.mkdir()
    long_dir.mkdir()
    dest = long_dir / 'mihari.exe'
    alias = short_dir / 'mihari.exe'
    source = ps_replacement_block(name) + '\n'
    # Only replace the native path/identity API. Real production observations
    # hash real files and perform the fresh-target dedup/update/assert sequence.
    native = r'''using System.IO;
public static class MihariReplacementNative {
 public static string Alias, Long;
 public static string LongPath(string path) { return path == Alias ? Long : path; }
 public static string[] Identity(FileStream stream) { return new [] { @"\\?\" + stream.Name, "fixture-file-id" }; }
}'''
    source += 'Add-Type -TypeDefinition ' + ps_literal(native) + '\n'
    source += '[MihariReplacementNative]::Alias=' + ps_literal(short_dir) + '\n[MihariReplacementNative]::Long=' + ps_literal(long_dir) + '\n'
    source += "$env:OS='Windows_NT'\nfunction Get-ReplacementService { [pscustomobject]@{Definition=''; Running=$false} }\n"
    source += '$candidate=' + ps_literal(candidate) + '\n$dest=' + ps_literal(dest) + '\n$alias=' + ps_literal(alias) + '\n'
    source += "$p=Get-ReplacementPreview $candidate 'v1.0.0' @($alias,$dest)\n"
    source += '$retainedAlias=($p.Targets.Count -eq 1 -and $p.Targets[0].SourcePath -eq $alias)\n'
    source += 'Copy-Item $candidate $dest -Force\nSet-ReplacementWrittenTarget $p $dest\nAssert-ReplacementPreview $p $candidate @($dest)\n'
    source += '$retainedAlias | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) is True


@pytest.mark.parametrize('name', ['install.ps1', 'install-aio.ps1'])
def test_windows_canonical_path_representation_includes_extended_unc(tmp_path, name):
    paths = [r'C:\Program Files\Mihari\mihari.exe', r'\\?\C:\Program Files\Mihari\mihari.exe',
             r'\\server\share\Mihari\mihari.exe', r'\\?\UNC\server\share\Mihari\mihari.exe']
    source = ps_replacement_block(name) + '\n'
    source += "if (-not (Get-Command Convert-ReplacementCanonicalPath -ErrorAction SilentlyContinue)) { function Convert-ReplacementCanonicalPath([string]$Path) { $Path.ToLowerInvariant() } }\n"
    source += '@(' + ','.join(ps_literal(p) for p in paths) + ') | ForEach-Object { Convert-ReplacementCanonicalPath $_ } | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) == [paths[0].lower(), paths[0].lower(), paths[2].lower(), paths[2].lower()]
