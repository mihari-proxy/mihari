"""Exercise the remote PowerShell handoff without network or installation."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import zipfile

import pytest

INSTALL = Path(__file__).parent
MARKER = '# MIHARI_INSTALL_CAPABILITY: replacement_confirmation_v1'
CAPS = '{"schema":"mihari.install-script/v1","capabilities":["replacement_confirmation_v1"]}'


def literal(value):
    return "'" + str(value).replace("'", "''") + "'"


def run_ps(tmp_path, source, env=None):
    exe = shutil.which('powershell') or shutil.which('pwsh')
    if not exe:
        pytest.skip('PowerShell is unavailable')
    driver = tmp_path / 'driver.ps1'
    driver.write_text("$ErrorActionPreference='Stop'\n" + source)
    return subprocess.run([exe, '-NoProfile', '-NonInteractive', '-File', str(driver)],
                          capture_output=True, text=True, env=env, timeout=20)


def installer_source(capabilities=CAPS, marker=True, action=None):
    return ('param([string]$BundleDir,[string]$Channel,[switch]$Capabilities)\n' +
            (MARKER + '\n' if marker else '') +
            'if ($Capabilities) { ' + capabilities + '; return }\n' +
            (action or "Set-Content -LiteralPath (Join-Path $BundleDir 'installed') -Value ($env:MIHARI_YES + ':' + $Channel)"))


def run_flow(tmp_path, inner, verified=True, yes=True, inherited=None, accept_download=False, checksum=None, channel="main"):
    bundle = tmp_path / 'fixture.zip'
    with zipfile.ZipFile(bundle, 'w') as archive:
        archive.writestr('install-aio.ps1', inner)
    source = (INSTALL / 'install-aio-remote.ps1').read_text()
    # Replace only transport with a fixture; production checksum, extraction,
    # static marker, query, and installation handoff run unchanged.
    mocks = '\nfunction Download-FileWithProgress($url,$dest) { Copy-Item -LiteralPath ' + literal(bundle) + ' -Destination $dest }\n'
    mocks += 'function Invoke-WebRequest { [pscustomobject]@{ Content="latest v1.0.0`nwindows-amd64 https://fixture.invalid/bundle.zip ' + (checksum if checksum is not None else hashlib.sha256(bundle.read_bytes()).hexdigest()) + '" } }\n'
    mocks += "function mihari { Set-Content -LiteralPath " + literal(tmp_path / "path-executed") + " -Value yes; throw 'PATH binary must never be executed' }\n"
    if accept_download:
        mocks += "function Confirm { return " + ("$false" if accept_download == "cancel" else "$true") + " }\n"
    source = source.replace('# Detect platform (mirrors install.ps1).', mocks + '\n# Detect platform (mirrors install.ps1).')
    driver = '& ([scriptblock]::Create(' + literal(source) + '))' + (' -Yes' if yes else '') + (' -Channel ' + channel if channel else '') + '\n'
    env = dict(os.environ, USERPROFILE=str(tmp_path), LOCALAPPDATA=str(tmp_path), PROCESSOR_ARCHITECTURE='AMD64')
    for key in ('MIHARI_INSTALL_TEST_MODE', 'MIHARI_YES', 'MIHARI_BUNDLE_URL', 'MIHARI_INDEX_URL', 'MIHARI_CHANNEL'):
        env.pop(key, None)
    if inherited is not None:
        env['MIHARI_YES'] = inherited
    if not verified:
        env['MIHARI_BUNDLE_URL'] = 'https://fixture.invalid/unverified.zip'
    return run_ps(tmp_path, driver, env), tmp_path / 'Downloads' / 'mihari-aio'


def test_remote_full_flow_old_bundle_never_executes(tmp_path):
    result, extracted = run_flow(tmp_path, "param([string]$BundleDir)\nSet-Content -LiteralPath (Join-Path $BundleDir 'installed') -Value yes")
    assert result.returncode != 0, result.stdout
    assert not (extracted / 'installed').exists()
    assert (extracted / 'install-aio.ps1').exists()
    assert 'current install-aio.ps1' in result.stderr
    assert '-BundleDir' in result.stderr


def test_remote_full_flow_unverified_override_never_executes(tmp_path):
    inner = installer_source(literal(CAPS))
    result, extracted = run_flow(tmp_path, inner, verified=False)
    assert result.returncode != 0, result.stdout
    assert not (extracted / 'installed').exists()


@pytest.mark.parametrize('yes,inherited,accept,expected', [
    (True, None, False, '1:main'), (False, '1', False, '1:main'),
    (False, None, True, ':main'), (False, 'true', True, 'true:main'),
])
def test_remote_full_flow_forwards_only_explicit_yes(tmp_path, yes, inherited, accept, expected):
    result, extracted = run_flow(tmp_path, installer_source(literal(CAPS)), yes=yes, inherited=inherited, accept_download=accept)
    assert result.returncode == 0, result.stderr
    assert (extracted / 'installed').read_text().strip() == expected
    assert not (tmp_path / 'path-executed').exists()


def handoff_block():
    source = (INSTALL / 'install-aio-remote.ps1').read_text()
    return source.split('# BEGIN VERIFIED LOCAL HANDOFF\n', 1)[1].split('# END VERIFIED LOCAL HANDOFF', 1)[0]


@pytest.mark.parametrize('raw', ['{}', '{"schema":"wrong","capabilities":["replacement_confirmation_v1"]}',
    CAPS + '{}', CAPS + '\v', '[' + CAPS + ']', CAPS.replace('replacement_confirmation_v1', 'other'),
    CAPS.replace('"schema":', '"schema":"wrong","schema":')])
def test_remote_capability_query_rejects_invalid_envelopes(tmp_path, raw):
    inner = tmp_path / 'install-aio.ps1'
    inner.write_text(installer_source(literal(raw)))
    result = run_ps(tmp_path, handoff_block() + '\nInvoke-VerifiedLocalInstaller -Installer ' + literal(inner) +
                    ' -BundleDir ' + literal(tmp_path) + ' -VerifiedSource $true -ExplicitYes $true\n')
    assert result.returncode != 0
    assert not (tmp_path / 'installed').exists()


@pytest.mark.parametrize('checksum', ['', '0' * 64, 'invalid'])
def test_remote_full_flow_requires_successful_checksum(tmp_path, checksum):
    result, extracted = run_flow(tmp_path, installer_source(literal(CAPS)), checksum=checksum)
    assert result.returncode != 0
    assert not (extracted / 'installed').exists()


def test_remote_full_flow_no_terminal_fails_before_download(tmp_path):
    result, extracted = run_flow(tmp_path, installer_source(literal(CAPS)), yes=False)
    assert result.returncode != 0
    assert 'Confirmation is required' in result.stderr
    assert not extracted.exists()


@pytest.mark.parametrize('probe', ["Start-Sleep -Seconds 20", "[Console]::Out.Write(('x' * 5000))", "[Console]::Error.Write(('x' * 5000)); " + literal(CAPS), "exit 7"])
def test_remote_capability_probe_is_bounded_and_checks_exit(tmp_path, probe):
    inner = tmp_path / 'install-aio.ps1'
    inner.write_text(installer_source(probe))
    result = run_ps(tmp_path, handoff_block() + '\nInvoke-VerifiedLocalInstaller -Installer ' + literal(inner) +
                    ' -BundleDir ' + literal(tmp_path) + ' -VerifiedSource $true\n')
    assert result.returncode != 0
    assert not (tmp_path / 'installed').exists()


@pytest.mark.parametrize('prior', [None, 'true', '1'])
@pytest.mark.parametrize('failure', ['', 'throw "inner failure"', 'exit 7', 'Write-Error "inner failure" -ErrorAction Continue'])
def test_remote_handoff_restores_environment_and_checks_failure(tmp_path, prior, failure):
    inner = tmp_path / 'install-aio.ps1'
    inner.write_text(installer_source(literal(CAPS), action="Set-Content -LiteralPath (Join-Path $BundleDir 'installed') -Value $env:MIHARI_YES\n" + failure))
    source = handoff_block() + '\n'
    source += ('Remove-Item Env:MIHARI_YES -ErrorAction SilentlyContinue' if prior is None else '$env:MIHARI_YES=' + literal(prior)) + '\n'
    source += '$failed=$false\n$LASTEXITCODE=83\ntry { Invoke-VerifiedLocalInstaller -Installer ' + literal(inner) + ' -BundleDir ' + literal(tmp_path) + ' -ExplicitYes $true -VerifiedSource $true } catch { $failed=$true }\n'
    source += '@{failed=$failed; value=$env:MIHARI_YES; exists=(Test-Path Env:MIHARI_YES)} | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, source)
    assert result.returncode == 0, result.stderr
    state = json.loads(result.stdout)
    assert state['failed'] == bool(failure)
    assert state['value'] == prior
    assert state['exists'] == (prior is not None)
    assert (tmp_path / 'installed').read_text().strip() == '1'


@pytest.mark.parametrize('marker', [MARKER + ' extra', ' ' + MARKER, MARKER.lower()])
def test_remote_near_marker_does_not_authorize_any_execution(tmp_path, marker):
    inner = tmp_path / 'install-aio.ps1'
    sentinel = tmp_path / 'probe-executed'
    inner.write_text(marker + '\nSet-Content -LiteralPath ' + literal(sentinel) + ' -Value yes\n')
    result = run_ps(tmp_path, handoff_block() + '\nInvoke-VerifiedLocalInstaller -Installer ' + literal(inner) +
                    ' -BundleDir ' + literal(tmp_path) + ' -VerifiedSource $true\n')
    assert result.returncode != 0
    assert not sentinel.exists()


def test_remote_current_installer_capability_has_no_install_side_effects(tmp_path):
    # Exercise the released local script's real early capability path. The
    # actual install call is replaced by a safe sentinel after that return.
    source = (INSTALL / 'install-aio.ps1').read_text()
    prefix = source.split("$ErrorActionPreference = 'Stop'", 1)[0]
    inner = tmp_path / 'install-aio.ps1'
    inner.write_text(prefix + "Set-Content -LiteralPath (Join-Path $BundleDir 'installed') -Value yes\n")
    result = run_ps(tmp_path, handoff_block() + '\nInvoke-VerifiedLocalInstaller -Installer ' + literal(inner) +
                    ' -BundleDir ' + literal(tmp_path) + ' -VerifiedSource $true\n')
    assert result.returncode == 0, result.stderr
    assert (tmp_path / 'installed').exists()


def test_remote_unverified_function_never_queries_script(tmp_path):
    inner = tmp_path / 'install-aio.ps1'
    sentinel = tmp_path / 'probe-executed'
    inner.write_text(MARKER + '\nSet-Content -LiteralPath ' + literal(sentinel) + ' -Value yes\n')
    result = run_ps(tmp_path, handoff_block() + '\nInvoke-VerifiedLocalInstaller -Installer ' + literal(inner) +
                    ' -BundleDir ' + literal(tmp_path) + ' -VerifiedSource $false -ExplicitYes $true\n')
    assert result.returncode != 0
    assert not sentinel.exists()


@pytest.mark.parametrize('answer,expected', [('', False), ('n', False), ('yes', True), ('y', True), ('yesterday', False)])
def test_remote_download_confirmation_defaults_to_no(tmp_path, answer, expected):
    source = (INSTALL / 'install-aio-remote.ps1').read_text()
    # Replace only terminal detection; run the real Confirm and mocked host read.
    source = source.replace("[Console]::IsInputRedirected -or [Environment]::GetCommandLineArgs() -contains '-NonInteractive'", '$false')
    driver = "$env:MIHARI_INSTALL_TEST_MODE='1'\n$env:MIHARI_YES='true'\n. ([scriptblock]::Create(" + literal(source) + "))\n"
    driver += 'function Read-Host { ' + literal(answer) + ' }\nConfirm "Download?" | ConvertTo-Json -Compress\n'
    result = run_ps(tmp_path, driver)
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) is expected


def test_remote_full_flow_cancellation_is_failure_before_download(tmp_path):
    result, extracted = run_flow(tmp_path, installer_source(literal(CAPS)), yes=False, accept_download='cancel')
    assert result.returncode != 0
    assert 'Canceled' in result.stderr
    assert not extracted.exists()


def test_remote_full_flow_omits_unspecified_channel(tmp_path):
    result, extracted = run_flow(tmp_path, installer_source(literal(CAPS)), channel=None)
    assert result.returncode == 0, result.stderr
    assert (extracted / 'installed').read_text().strip() == '1:'


def test_remote_full_flow_inner_failure_is_not_success(tmp_path):
    result, extracted = run_flow(tmp_path, installer_source(literal(CAPS), action='exit 7'))
    assert result.returncode != 0
    assert 'Local installation failed' in result.stderr


def test_remote_handoff_preserves_handled_optional_lookup_errors(tmp_path):
    inner = tmp_path / 'install-aio.ps1'
    inner.write_text(installer_source(literal(CAPS), action="Get-Command no-such-mihari-193 -ErrorAction SilentlyContinue\nSet-Content -LiteralPath (Join-Path $BundleDir 'installed') -Value yes\n"))
    result = run_ps(tmp_path, handoff_block() + '\nInvoke-VerifiedLocalInstaller -Installer ' + literal(inner) +
                    ' -BundleDir ' + literal(tmp_path) + ' -VerifiedSource $true\n')
    assert result.returncode == 0, result.stderr
    assert (tmp_path / 'installed').exists()
