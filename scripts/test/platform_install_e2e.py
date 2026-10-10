"""Offline install and uninstall checks for the platform e2e job.

Importing this module does not run the installer. The ``run`` subcommand
is the only path that installs a service.
"""

import hashlib
import io
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path

VERSION = "v0.0.0-dev.0"
CHANNEL = "dev"
LDFLAG = "-X github.com/mihari-proxy/mihari/internal/buildinfo.Version=v0.0.0-dev.0"
_SHA256 = re.compile(r"^[0-9a-f]{64}$")

UNIX_MEMBERS = (
    "mihari",
    "data/bin/mihomo",
    "data/geoip/GeoLite2-Country.mmdb",
    "data/geoip/GeoLite2-ASN.mmdb",
)


def pack_unix_bundle(mihari: bytes, core: bytes, country: bytes, asn: bytes) -> bytes:
    payload = {
        "mihari": mihari,
        "data/bin/mihomo": core,
        "data/geoip/GeoLite2-Country.mmdb": country,
        "data/geoip/GeoLite2-ASN.mmdb": asn,
    }
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        for name in UNIX_MEMBERS:
            data = payload[name]
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.type = tarfile.REGTYPE
            info.mode = 0o644
            archive.addfile(info, io.BytesIO(data))
    return buffer.getvalue()


def unix_members(blob: bytes) -> list[str]:
    with tarfile.open(fileobj=io.BytesIO(blob), mode="r:gz") as archive:
        return archive.getnames()


def sha256_hex(payload: bytes) -> str:
    return hashlib.sha256(payload).hexdigest()


def manifest_document(bundle_sha256: str, binary_sha256: str) -> dict:
    for digest in (binary_sha256, bundle_sha256):
        if _SHA256.fullmatch(digest) is None:
            raise ValueError(f"sha256 must be 64 lowercase hex digits: {digest}")
    return {"binaries": [binary_sha256], "bundles": [bundle_sha256]}


def manifest_bytes(document: dict) -> bytes:
    return (json.dumps(document, indent=2) + "\n").encode("utf-8")


def write_windows_bundle(root: Path, mihari: bytes, core: bytes, country: bytes, asn: bytes) -> None:
    files = {
        "mihari.exe": mihari,
        "data/bin/mihomo.exe": core,
        "data/geoip/GeoLite2-Country.mmdb": country,
        "data/geoip/GeoLite2-ASN.mmdb": asn,
    }
    for relative, data in files.items():
        path = root.joinpath(*relative.split("/"))
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)


_ANSI = re.compile(r"\x1b\[[0-9;]*m")
UNIX_SUCCESS = ("Installation complete.", "Run mihari to open the TUI.")
MACOS_WARNING = "warning: macOS is currently unsupported. Support is incomplete and use is not recommended."
WINDOWS_SUCCESS = "All-in-one installation completed. Restart your terminal, then run mihari to get started."
_PATH_KEYS = {
    "linux": ("program", "data", "base", "path_command"),
    "darwin": ("program", "data", "base", "path_command"),
    "windows": ("program", "data", "path_command"),
}


def _plain(text: str) -> str:
    return _ANSI.sub("", text)


def assert_install_success(system: str, stdout: str, stderr: str) -> None:
    plain_out = _plain(stdout)
    plain_err = _plain(stderr)
    if system in ("linux", "darwin"):
        for sentence in UNIX_SUCCESS:
            if sentence not in plain_out:
                raise SystemExit(sentence)
        if system == "darwin" and MACOS_WARNING not in plain_err:
            raise SystemExit(MACOS_WARNING)
        return
    if system == "windows" and WINDOWS_SUCCESS in plain_out:
        return
    raise SystemExit(system)


def assert_service_version(stdout: str) -> None:
    try:
        document = json.loads(stdout)
    except json.JSONDecodeError as exc:
        raise SystemExit("version") from exc
    if document.get("schema") != "mihari/v1" or document.get("version") != VERSION:
        raise SystemExit("version")


def assert_kept(system: str, service_installed: bool, paths_present: dict[str, bool]) -> None:
    _assert_paths(system, service_installed, paths_present, expected=True)


def assert_purged(system: str, service_installed: bool, paths_present: dict[str, bool]) -> None:
    _assert_paths(system, service_installed, paths_present, expected=False)


def _assert_paths(system: str, service_installed: bool, paths_present: dict[str, bool], expected: bool) -> None:
    if service_installed:
        raise SystemExit("service")
    try:
        keys = _PATH_KEYS[system]
    except KeyError as exc:
        raise SystemExit(system) from exc
    for key in keys:
        if paths_present.get(key) is not expected:
            raise SystemExit(key)


ROOT = Path(__file__).resolve().parents[2]


def geoip_fixtures() -> tuple[bytes, bytes]:
    root = ROOT / "internal" / "app" / "testdata" / "migration-mmdb"
    return (root / "country.mmdb").read_bytes(), (root / "asn.mmdb").read_bytes()


PROGRAM_DIR = "/usr/local/lib/mihari"
TRUST_DIR = PROGRAM_DIR + "/install-trust"
MANIFEST = TRUST_DIR + "/manifest.json"
PROGRAM_FILE = PROGRAM_DIR + "/mihari"
UNIX_COMMAND = "/usr/local/bin/mihari"
LINUX_DATA = "/var/lib/mihari/data"
LINUX_BASE = "/var/lib/mihari"
DARWIN_BASE = "/Library/Application Support/mihari"
DARWIN_DATA = DARWIN_BASE + "/data"
DARWIN_PLIST = "/Library/LaunchDaemons/mihari.plist"
WINDOWS_PROGRAM = r"C:\Program Files\Mihari"
WINDOWS_PROGRAM_FILE = WINDOWS_PROGRAM + r"\mihari.exe"
KEEP_TEXT = "service uninstall ok"
PURGE_TEXT = "Mihari has been completely uninstalled"


def build_command(go: str, output: str) -> list[str]:
    return [go, "build", "-trimpath", "-ldflags", LDFLAG, "-o", output, "./cmd/mihari"]


def unix_install_command(script: str, archive: str) -> list[str]:
    return [
        "sudo", "/usr/bin/env",
        f"MIHARI_VERSION={VERSION}",
        f"MIHARI_CHANNEL={CHANNEL}",
        "MIHARI_YES=1",
        script,
        "--channel", CHANNEL,
        archive,
    ]


def windows_install_command(script: str, bundle_dir: str) -> list[str]:
    return [
        "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass",
        "-File", script,
        "-BundleDir", bundle_dir,
        "-Channel", CHANNEL,
    ]


def service_command(binary: str, purge: bool) -> list[str]:
    argv = ["sudo", binary, "service", "uninstall"]
    if purge:
        argv.extend(["--purge", "--yes"])
    return argv


def host_system() -> str:
    if sys.platform == "win32":
        return "windows"
    if sys.platform == "darwin":
        return "darwin"
    if sys.platform.startswith("linux"):
        return "linux"
    raise SystemExit(sys.platform)


def install_paths(system: str) -> dict[str, str]:
    if system == "linux":
        return {
            "program": PROGRAM_DIR,
            "program_file": PROGRAM_FILE,
            "data": LINUX_DATA,
            "base": LINUX_BASE,
            "path_command": UNIX_COMMAND,
        }
    if system == "darwin":
        return {
            "program": PROGRAM_DIR,
            "program_file": PROGRAM_FILE,
            "data": DARWIN_DATA,
            "base": DARWIN_BASE,
            "path_command": UNIX_COMMAND,
        }
    if system == "windows":
        local = os.environ["LOCALAPPDATA"]
        command_dir = os.path.join(local, "Programs", "mihari")
        return {
            "program": WINDOWS_PROGRAM,
            "program_file": WINDOWS_PROGRAM_FILE,
            "data": os.path.join(os.environ["USERPROFILE"], ".mihari"),
            "path_command": os.path.join(command_dir, "mihari.exe"),
            "path_dir": command_dir,
        }
    raise SystemExit(system)


def _run(
    argv: list[str],
    *,
    env: dict[str, str] | None = None,
    cwd: str | None = None,
    check: bool = True,
    timeout: int = 600,
) -> subprocess.CompletedProcess[str]:
    print("+ " + " ".join(argv), flush=True)
    result = subprocess.run(
        argv,
        cwd=cwd,
        env=env,
        stdin=subprocess.DEVNULL,
        text=True,
        capture_output=True,
        timeout=timeout,
    )
    if result.stdout:
        print(result.stdout, end="" if result.stdout.endswith("\n") else "\n")
    if result.stderr:
        print(result.stderr, file=sys.stderr, end="" if result.stderr.endswith("\n") else "\n")
    if check and result.returncode != 0:
        raise SystemExit(f"command failed ({result.returncode}): {argv[0]}")
    return result


def _capture(argv: list[str], timeout: int = 60) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(argv, stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout)


def file_sha256(path: str) -> str:
    try:
        return sha256_hex(Path(path).read_bytes())
    except PermissionError:
        if host_system() == "windows":
            raise
        result = _capture(["sudo", "cat", path])
        if result.returncode != 0:
            raise SystemExit(f"path_command {path}")
        return sha256_hex(result.stdout)


def _write_manifest(payload: bytes) -> None:
    _run(["sudo", "mkdir", "-p", "--", TRUST_DIR])
    _run(["sudo", "chmod", "755", PROGRAM_DIR, TRUST_DIR])
    fd, temporary = tempfile.mkstemp(prefix="mihari-manifest-")
    os.close(fd)
    try:
        Path(temporary).write_bytes(payload)
        _run(["sudo", "rm", "-f", "--", MANIFEST])
        _run(["sudo", "cp", temporary, MANIFEST])
        _run(["sudo", "chown", "0:0", MANIFEST])
        _run(["sudo", "chmod", "644", MANIFEST])
    finally:
        os.remove(temporary)
    _require_trusted_ancestors(MANIFEST)


def _require_trusted_ancestors(path: str) -> None:
    """Fail with the ancestor stat. Do not change ownership of / or /usr."""
    current = path
    while current != "/":
        if os.path.islink(current):
            raise SystemExit(f"symlink {current}")
        try:
            info = os.stat(current, follow_symlinks=False)
        except OSError as exc:
            raise SystemExit(f"{current} {exc}") from exc
        mode = stat.S_IMODE(info.st_mode)
        if info.st_uid != 0 or (mode & 0o022) != 0:
            raise SystemExit(f"{current} uid={info.st_uid} mode={mode:04o}")
        if current == path and stat.S_ISREG(info.st_mode) and info.st_nlink != 1:
            raise SystemExit(f"{current} nlink={info.st_nlink}")
        if host_system() == "darwin":
            listed = _run(["ls", "-lde", current])
            extra = [line for line in listed.stdout.splitlines()[1:] if line.strip()]
            if extra:
                raise SystemExit(extra[0])
        current = os.path.dirname(current)


def _pin_unix(bundle: bytes, program: bytes) -> None:
    document = manifest_document(sha256_hex(bundle), sha256_hex(program))
    _write_manifest(manifest_bytes(document))


def _install_unix(archive: str) -> None:
    _prepare_unix_command_directory()
    script = ROOT / "scripts" / "install" / "install-aio.sh"
    mode = script.stat().st_mode
    script.chmod(mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    result = _run(unix_install_command(str(script), archive), cwd=str(ROOT))
    assert_install_success(host_system(), result.stdout, result.stderr)


def _prepare_unix_command_directory() -> None:
    if os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("RUNNER_ENVIRONMENT") != "github-hosted":
        raise SystemExit("Unix install e2e requires a github-hosted runner")
    directory = os.path.dirname(UNIX_COMMAND)
    # Hosted images expose this shared tool directory as runner-owned or writable.
    # Prepare the default PATH destination on the disposable VM; do not relax the
    # installer policy or repair /usr or /usr/local. Never recurse into its files.
    if os.path.islink(directory):
        raise SystemExit(f"symlink {directory}")
    _require_trusted_ancestors(os.path.dirname(directory))
    _run(["ls", "-ld", directory])
    _run(["sudo", "chown", "0:0", directory])
    _run(["sudo", "chmod", "755", directory])
    _require_trusted_ancestors(directory)


def _install_windows(bundle_dir: str, env: dict[str, str]) -> None:
    script = str(ROOT / "scripts" / "install" / "install-aio.ps1")
    result = _run(windows_install_command(script, bundle_dir), env=env, cwd=str(ROOT))
    assert_install_success("windows", result.stdout, result.stderr)


def _service_argv(system: str, paths: dict[str, str], purge: bool, *, windows_binary: str | None = None) -> list[str]:
    binary = paths["path_command"]
    if system == "windows":
        if purge:
            if windows_binary is None:
                raise ValueError("Windows purge requires an executable outside the installed roots")
            binary = windows_binary
        argv = [binary, "service", "uninstall"]
        if purge:
            argv.extend(["--purge", "--yes"])
        return argv
    return service_command(binary, purge)


def _wait_until(probe, description: str) -> None:
    deadline = time.monotonic() + 30
    last = ""
    while True:
        ok, last = probe()
        if ok:
            return
        if time.monotonic() >= deadline:
            raise SystemExit(f"{description} {last}".strip())
        time.sleep(1)


def _require_running(system: str, paths: dict[str, str]) -> None:
    program = paths["program_file"]
    if system == "linux":
        def probe() -> tuple[bool, str]:
            active = _run(["sudo", "systemctl", "is-active", "mihari"], check=False)
            status = active.stdout.strip()
            if status != "active":
                return False, status
            pid = _run(["sudo", "systemctl", "show", "-p", "MainPID", "--value", "mihari"], check=False)
            pid_text = pid.stdout.strip()
            if pid.returncode != 0 or not pid_text.isdigit() or pid_text == "0":
                return False, pid_text
            exe = _run(["sudo", "readlink", "-f", f"/proc/{pid_text}/exe"], check=False)
            found = exe.stdout.strip()
            return exe.returncode == 0 and found == program, found

        _wait_until(probe, "service")
        return
    if system == "darwin":
        def probe() -> tuple[bool, str]:
            printed = _run(["sudo", "launchctl", "print", "system/mihari"], check=False)
            text = printed.stdout
            return printed.returncode == 0 and program in text and "state = running" in text, text

        _wait_until(probe, "service")
        return
    script = (
        "$svc = Get-Service -Name mihari -ErrorAction SilentlyContinue; "
        "if ($null -eq $svc) { Write-Output missing; exit 1 }; "
        "Write-Output $svc.Status; "
        "if ($svc.Status -ne 'Running') { exit 1 }; "
        "$path = (Get-CimInstance Win32_Service -Filter \"Name='mihari'\").PathName; "
        "Write-Output $path; "
        "if ($path -notlike '*C:\\Program Files\\Mihari\\mihari.exe*') { exit 1 }"
    )

    def probe() -> tuple[bool, str]:
        result = _run(["powershell.exe", "-NoProfile", "-Command", script], check=False)
        return result.returncode == 0, result.stdout.strip()

    _wait_until(probe, "service")


def _service_registered(system: str) -> bool:
    if system == "linux":
        return _run(["systemctl", "cat", "mihari.service"], check=False).returncode == 0
    if system == "darwin":
        printed = _run(["sudo", "launchctl", "print", "system/mihari"], check=False)
        return printed.returncode == 0 or os.path.exists(DARWIN_PLIST)
    script = "if (Get-Service -Name mihari -ErrorAction SilentlyContinue) { exit 0 } else { exit 1 }"
    return _run(["powershell.exe", "-NoProfile", "-Command", script], check=False).returncode == 0


def _paths_present(system: str, paths: dict[str, str]) -> dict[str, bool]:
    return {key: os.path.lexists(paths[key]) for key in _PATH_KEYS[system]}


def _user_path_contains(directory: str) -> bool:
    import winreg

    with winreg.OpenKey(winreg.HKEY_CURRENT_USER, "Environment") as key:
        value, _ = winreg.QueryValueEx(key, "Path")
    want = os.path.normcase(os.path.normpath(directory))
    literal = os.path.normcase(r"%LOCALAPPDATA%\Programs\mihari")
    for part in str(value).split(os.pathsep):
        cleaned = os.path.normcase(os.path.normpath(part.strip().strip('"')))
        if cleaned in (want, literal):
            return True
    return False


def _assert_installed(system: str, paths: dict[str, str]) -> None:
    version = _run([paths["program_file"], "self", "version", "--json"])
    assert_service_version(version.stdout)
    _require_running(system, paths)
    if file_sha256(paths["path_command"]) != file_sha256(paths["program_file"]):
        raise SystemExit("path_command")
    if system == "windows" and not _user_path_contains(paths["path_dir"]):
        raise SystemExit("path_command")


def _existing_binary(paths: dict[str, str]) -> str | None:
    for key in ("path_command", "program_file"):
        if os.path.isfile(paths[key]):
            return paths[key]
    return None


def _cleanup_paths(system: str) -> list[str]:
    if system == "linux":
        return [PROGRAM_DIR, LINUX_BASE, UNIX_COMMAND]
    if system == "darwin":
        return [PROGRAM_DIR, UNIX_COMMAND, DARWIN_PLIST, DARWIN_BASE]
    paths = [WINDOWS_PROGRAM]
    profile = os.environ.get("USERPROFILE")
    local = os.environ.get("LOCALAPPDATA")
    if profile:
        paths.append(os.path.join(profile, ".mihari"))
    if local:
        paths.append(os.path.join(local, "Programs", "mihari", "mihari.exe"))
    return paths


def _delete_path(system: str, path: str) -> None:
    if system == "windows":
        env = os.environ.copy()
        env["MIHARI_CLEANUP_PATH"] = path
        script = "Remove-Item -LiteralPath $env:MIHARI_CLEANUP_PATH -Recurse -Force -ErrorAction Stop"
        result = _run(["powershell.exe", "-NoProfile", "-Command", script], env=env, check=False)
        if result.returncode != 0:
            raise SystemExit(1)
        return
    result = _run(["sudo", "rm", "-rf", "--", path], check=False)
    if result.returncode != 0:
        raise SystemExit(1)


def execute() -> None:
    system = host_system()
    paths = install_paths(system)
    build_env = os.environ.copy()
    build_env["CGO_ENABLED"] = "0"
    with tempfile.TemporaryDirectory(prefix="mihari-e2e-") as work:
        binary_name = "mihari.exe" if system == "windows" else "mihari"
        built = os.path.join(work, binary_name)
        _run(build_command("go", built), env=build_env, cwd=str(ROOT))
        program = Path(built).read_bytes()
        core = b"placeholder-core"
        country, asn = geoip_fixtures()
        if system == "windows":
            bundle_dir = os.path.join(work, "bundle")
            write_windows_bundle(Path(bundle_dir), program, core, country, asn)
            install_env = build_env.copy()
            install_env["MIHARI_YES"] = "1"
            _install_windows(bundle_dir, install_env)
        else:
            bundle = pack_unix_bundle(program, core, country, asn)
            archive = os.path.join(work, "bundle.tar.gz")
            Path(archive).write_bytes(bundle)
            _pin_unix(bundle, program)
            _install_unix(archive)
        _assert_installed(system, paths)
        kept = _run(_service_argv(system, paths, False))
        if KEEP_TEXT not in kept.stdout:
            raise SystemExit(KEEP_TEXT)
        assert_kept(system, _service_registered(system), _paths_present(system, paths))
        if system == "windows":
            _install_windows(bundle_dir, install_env)
        else:
            _pin_unix(bundle, program)
            _install_unix(archive)
        _assert_installed(system, paths)
        purged = _run(_service_argv(system, paths, True, windows_binary=built))
        if PURGE_TEXT not in purged.stdout:
            raise SystemExit(PURGE_TEXT)
        assert_purged(system, _service_registered(system), _paths_present(system, paths))


def cleanup() -> None:
    system = host_system()
    paths = install_paths(system) if system != "windows" or ("LOCALAPPDATA" in os.environ and "USERPROFILE" in os.environ) else {}
    if _service_registered(system):
        binary = _existing_binary(paths) if paths else None
        if binary is None:
            raise SystemExit(1)
        # Windows must release both installed images before deleting them.
        # Keep the temporary copy alive until the uninstall process exits.
        with tempfile.TemporaryDirectory(prefix="mihari-e2e-cleanup-") as work:
            standalone = os.path.join(work, "mihari.exe")
            if system == "windows":
                shutil.copy2(binary, standalone)
            uninstall = _service_argv(system, {"path_command": binary}, True, windows_binary=standalone)
            if _run(uninstall, check=False).returncode != 0:
                raise SystemExit(1)
    for path in _cleanup_paths(system):
        if os.path.lexists(path):
            _delete_path(system, path)


def main(argv: list[str]) -> None:
    if argv[1:] == ["run"]:
        execute()
        return
    if argv[1:] == ["cleanup"]:
        cleanup()
        return
    raise SystemExit("usage: platform_install_e2e.py run|cleanup")


if __name__ == "__main__":
    main(sys.argv)
