import hashlib
import io
import json
import subprocess
import sys
import tarfile
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import platform_install_e2e as e2e


def test_unix_install_prepares_command_directory_on_hosted_runner(monkeypatch, tmp_path):
    command_dir = tmp_path / "bin"
    command_dir.mkdir()
    script = tmp_path / "scripts/install/install-aio.sh"
    script.parent.mkdir(parents=True)
    script.write_text("fixture")
    monkeypatch.setattr(e2e, "ROOT", tmp_path)
    monkeypatch.setattr(e2e, "UNIX_COMMAND", str(command_dir / "mihari"))
    monkeypatch.setattr(e2e, "host_system", lambda: "linux")
    monkeypatch.setenv("GITHUB_ACTIONS", "true")
    monkeypatch.setenv("RUNNER_ENVIRONMENT", "github-hosted")
    inspected = []
    commands = []
    monkeypatch.setattr(e2e, "_require_trusted_ancestors", inspected.append)

    def run(argv, **kwargs):
        commands.append(argv)
        return subprocess.CompletedProcess(argv, 0, "\n".join(e2e.UNIX_SUCCESS), "")

    monkeypatch.setattr(e2e, "_run", run)
    e2e._install_unix("bundle.tar.gz")
    assert ["sudo", "chown", "0:0", str(command_dir)] in commands
    assert ["sudo", "chmod", "755", str(command_dir)] in commands
    assert inspected == [str(tmp_path), str(command_dir)]


def test_unix_install_refuses_to_repair_a_workstation(monkeypatch):
    monkeypatch.delenv("GITHUB_ACTIONS", raising=False)
    monkeypatch.delenv("RUNNER_ENVIRONMENT", raising=False)
    monkeypatch.setattr(e2e, "_run", lambda *args, **kwargs: pytest.fail("must refuse before running commands"))
    with pytest.raises(SystemExit, match="github-hosted"):
        e2e._install_unix("bundle.tar.gz")


def test_geoip_inputs_are_the_existing_synthetic_mmdb_fixtures():
    country, asn = e2e.geoip_fixtures()
    assert e2e.sha256_hex(country) == "b37601903448683d241af52893c8cbf0fed461e0cdebe0bfaca01891fdeb6db9"
    assert e2e.sha256_hex(asn) == "75901b98ed6e58d3bd41af9985044b747a7ec0be1369f930c24f5e044427181a"


def test_windows_purge_runs_outside_the_installed_roots():
    paths = {"path_command": "installed-command.exe", "program_file": "installed-service.exe"}
    assert e2e._service_argv("windows", paths, False) == ["installed-command.exe", "service", "uninstall"]
    assert e2e._service_argv("windows", paths, True, windows_binary="temporary.exe") == [
        "temporary.exe", "service", "uninstall", "--purge", "--yes",
    ]
    with pytest.raises(ValueError):
        e2e._service_argv("windows", paths, True)


def test_windows_execute_uses_valid_geoip_and_temporary_purge_binary(monkeypatch, tmp_path):
    paths = {"path_command": str(tmp_path / "command.exe"), "program_file": str(tmp_path / "service.exe")}
    commands = []
    bundles = []

    def run(argv, **kwargs):
        commands.append(argv)
        if argv[0] == "go":
            Path(argv[argv.index("-o") + 1]).write_bytes(b"built-program")
        return subprocess.CompletedProcess(argv, 0, e2e.KEEP_TEXT + "\n" + e2e.PURGE_TEXT, "")

    def install(bundle_dir, env):
        root = Path(bundle_dir)
        bundles.append((
            (root / "mihari.exe").read_bytes(),
            (root / "data/geoip/GeoLite2-Country.mmdb").read_bytes(),
            (root / "data/geoip/GeoLite2-ASN.mmdb").read_bytes(),
        ))

    monkeypatch.setattr(e2e, "host_system", lambda: "windows")
    monkeypatch.setattr(e2e, "install_paths", lambda system: paths)
    monkeypatch.setattr(e2e, "_run", run)
    monkeypatch.setattr(e2e, "_install_windows", install)
    monkeypatch.setattr(e2e, "_assert_installed", lambda *args: None)
    monkeypatch.setattr(e2e, "_service_registered", lambda system: False)
    monkeypatch.setattr(e2e, "_paths_present", lambda *args: {})
    monkeypatch.setattr(e2e, "assert_kept", lambda *args: None)
    monkeypatch.setattr(e2e, "assert_purged", lambda *args: None)
    e2e.execute()
    assert bundles == [(b"built-program", *e2e.geoip_fixtures())] * 2
    assert commands[1] == [paths["path_command"], "service", "uninstall"]
    assert commands[2] == [commands[0][commands[0].index("-o") + 1], "service", "uninstall", "--purge", "--yes"]
    assert commands[2][0] not in paths.values()


def test_windows_cleanup_copies_installed_binary_before_purge(monkeypatch, tmp_path):
    installed = tmp_path / "installed.exe"
    installed.write_bytes(b"installed-program")
    monkeypatch.setenv("LOCALAPPDATA", str(tmp_path))
    monkeypatch.setenv("USERPROFILE", str(tmp_path))
    monkeypatch.setattr(e2e, "host_system", lambda: "windows")
    monkeypatch.setattr(e2e, "install_paths", lambda system: {"path_command": str(installed)})
    monkeypatch.setattr(e2e, "_service_registered", lambda system: True)
    monkeypatch.setattr(e2e, "_cleanup_paths", lambda system: [])
    commands = []

    def run(argv, **kwargs):
        commands.append(argv)
        assert Path(argv[0]).read_bytes() == b"installed-program"
        assert argv[0] != str(installed)
        assert kwargs["check"] is False
        return subprocess.CompletedProcess(argv, 0, "", "")

    monkeypatch.setattr(e2e, "_run", run)
    e2e.cleanup()
    assert len(commands) == 1
    assert commands[0][1:] == ["service", "uninstall", "--purge", "--yes"]
    assert not Path(commands[0][0]).exists()
    assert installed.read_bytes() == b"installed-program"


def test_unix_bundle_has_only_the_installer_members():
    blob = e2e.pack_unix_bundle(b"mihari-bytes", b"core", b"country", b"asn")
    assert e2e.unix_members(blob) == [
        "mihari",
        "data/bin/mihomo",
        "data/geoip/GeoLite2-Country.mmdb",
        "data/geoip/GeoLite2-ASN.mmdb",
    ]
    with tarfile.open(fileobj=io.BytesIO(blob), mode="r:gz") as archive:
        mihari = archive.extractfile("mihari").read()
    assert mihari == b"mihari-bytes"


def test_windows_bundle_uses_exe_names(tmp_path: Path):
    e2e.write_windows_bundle(tmp_path, b"exe", b"core", b"country", b"asn")
    assert (tmp_path / "mihari.exe").read_bytes() == b"exe"
    assert (tmp_path / "data" / "bin" / "mihomo.exe").read_bytes() == b"core"
    assert (tmp_path / "data" / "geoip" / "GeoLite2-Country.mmdb").read_bytes() == b"country"
    assert (tmp_path / "data" / "geoip" / "GeoLite2-ASN.mmdb").read_bytes() == b"asn"


def test_manifest_pins_bundle_and_binary_only():
    mihari = b"mihari-bytes"
    blob = e2e.pack_unix_bundle(mihari, b"core", b"country", b"asn")
    document = e2e.manifest_document(e2e.sha256_hex(blob), e2e.sha256_hex(mihari))
    assert document == {
        "binaries": [hashlib.sha256(mihari).hexdigest()],
        "bundles": [hashlib.sha256(blob).hexdigest()],
    }
    parsed = json.loads(e2e.manifest_bytes(document).decode("utf-8"))
    assert list(parsed) == ["binaries", "bundles"]
    assert len(parsed["binaries"][0]) == 64


def test_unix_success_text_and_macos_warning():
    e2e.assert_install_success("linux", "Installation complete.\nRun mihari to open the TUI.\n", "")
    e2e.assert_install_success(
        "darwin",
        "Installation complete.\nRun mihari to open the TUI.\n",
        "warning: macOS is currently unsupported. Support is incomplete and use is not recommended.\n",
    )
    with pytest.raises(SystemExit):
        e2e.assert_install_success("darwin", "Installation complete.\nRun mihari to open the TUI.\n", "")


def test_version_json_must_match_the_fixed_tag():
    e2e.assert_service_version('{"schema":"mihari/v1","version":"v0.0.0-dev.0"}\n')
    with pytest.raises(SystemExit):
        e2e.assert_service_version('{"schema":"mihari/v1","version":"dev"}\n')


def test_keep_leaves_directories_and_purge_removes_them():
    present = {"program": True, "data": True, "base": True, "path_command": True}
    e2e.assert_kept("linux", False, present)
    e2e.assert_purged("linux", False, {key: False for key in present})
    with pytest.raises(SystemExit):
        e2e.assert_kept("linux", True, present)


def test_commands_use_the_fixed_version_and_existing_installers():
    assert e2e.build_command("go", "mihari") == [
        "go", "build", "-trimpath", "-ldflags", e2e.LDFLAG, "-o", "mihari", "./cmd/mihari",
    ]
    assert e2e.unix_install_command("scripts/install/install-aio.sh", "bundle.tar.gz") == [
        "sudo", "/usr/bin/env",
        "MIHARI_VERSION=v0.0.0-dev.0",
        "MIHARI_CHANNEL=dev",
        "MIHARI_YES=1",
        "scripts/install/install-aio.sh",
        "--channel", "dev",
        "bundle.tar.gz",
    ]
    assert e2e.windows_install_command("scripts/install/install-aio.ps1", r"C:\bundle") == [
        "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass",
        "-File", "scripts/install/install-aio.ps1",
        "-BundleDir", r"C:\bundle",
        "-Channel", "dev",
    ]
    assert e2e.service_command("/usr/local/bin/mihari", False) == [
        "sudo", "/usr/local/bin/mihari", "service", "uninstall",
    ]
    assert e2e.service_command("/usr/local/bin/mihari", True) == [
        "sudo", "/usr/local/bin/mihari", "service", "uninstall", "--purge", "--yes",
    ]
