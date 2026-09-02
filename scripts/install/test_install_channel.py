"""Channel resolution tests for install.sh / install.ps1 (script 1)."""

from __future__ import annotations

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import subprocess
import threading

import pytest

SCRIPT_DIR = Path(__file__).resolve().parent
INSTALL_SH = SCRIPT_DIR / "install.sh"
INSTALL_PS1 = SCRIPT_DIR / "install.ps1"
CANONICAL_DEV = "v0.9.0-dev.3"
COMPACT_RELEASES = (
    '[{"tag_name":"v0.8.0-dev.99"},{"tag_name":"v0.9.0"},'
    '{"tag_name":"v0.9.0-dev"},{"tag_name":"v0.9.0-dev.1","draft":true},'
    f'{{"tag_name":"{CANONICAL_DEV}"}}]'
)
# GitHub list JSON nests author/assets objects. PS 5.1 ConvertFrom-Json flattens
# arrays, and a \{[^{}]*\} fallback only matches those inner objects.
NESTED_GITHUB_RELEASES = json.dumps(
    [
        {"tag_name": "v0.8.0-dev.99", "draft": False, "assets": [{"name": "x"}]},
        {"tag_name": "v0.9.0", "draft": False, "author": {"login": "bot"}},
        {"tag_name": "v0.9.0-dev", "draft": False},
        {"tag_name": "v0.9.0-dev.1", "draft": True, "assets": [{"name": "y"}]},
        {
            "tag_name": CANONICAL_DEV,
            "draft": False,
            "assets": [{"name": "mihari-windows-amd64.exe", "id": 1}],
        },
    ],
    separators=(",", ":"),
)


def posix_shell() -> str | None:
    override = os.environ.get("MIHARI_TEST_SHELL")
    if override:
        return override
    git_sh = Path(r"C:\Program Files\Git\bin\sh.exe")
    if git_sh.is_file():
        return str(git_sh)
    for name in ("sh", "bash"):
        path = shutil.which(name)
        if path and "system32" not in path.lower():
            return path
    return None


def powershell() -> str | None:
    return shutil.which("powershell") or shutil.which("pwsh")


requires_sh = pytest.mark.skipif(posix_shell() is None, reason="POSIX sh is not available")
requires_ps = pytest.mark.skipif(powershell() is None, reason="PowerShell is not available")


class GitHubListServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self):
        super().__init__(("127.0.0.1", 0), GitHubListHandler)
        self.lock = threading.Lock()
        self.paths: list[str] = []
        self.queries: list[str] = []
        self.pages: dict[str, tuple[bytes, dict[str, str]]] = {}
        self.default_body = COMPACT_RELEASES.encode()
        self.fail = False

    def record(self, path: str, query: str) -> None:
        with self.lock:
            self.paths.append(path)
            self.queries.append(query)

    def snapshot(self) -> tuple[list[str], list[str]]:
        with self.lock:
            return list(self.paths), list(self.queries)


class GitHubListHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *args):
        return

    def do_GET(self):
        parsed = self.path.split("?", 1)
        path = parsed[0]
        query = parsed[1] if len(parsed) > 1 else ""
        self.server.record(path, query)
        if self.server.fail:
            self.send_error(500)
            return
        if path.endswith("/releases/latest"):
            self.send_error(404)
            return
        page = ""
        for part in query.split("&"):
            if part.startswith("page="):
                page = part.split("=", 1)[1]
        key = page or "1"
        if key in self.server.pages:
            body, headers = self.server.pages[key]
        else:
            body, headers = self.server.default_body, {}
        self.send_response(200)
        for name, value in headers.items():
            self.send_header(name, value)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


@pytest.fixture
def github_server():
    server = GitHubListServer()
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


def parse_test_output(text: str) -> dict[str, str]:
    got = {}
    for line in text.splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            if key in {"CHANNEL", "EXPLICIT", "URL", "INDEX_URL", "HANDOFF", "LATEST", "TARGET_TAG", "INSTALLED", "DOWNGRADE"}:
                got[key] = value
    return got


def apply_ps_compat_env(command_env: dict[str, str], home: Path | None = None) -> None:
    # Unix pwsh (ubuntu/macos CI) has no LOCALAPPDATA/USERPROFILE/PROCESSOR_ARCHITECTURE.
    if home is None:
        existing = command_env.get("USERPROFILE") or command_env.get("HOME") or os.path.expanduser("~") or "."
        home = Path(existing)
    if not command_env.get("USERPROFILE"):
        command_env["USERPROFILE"] = str(home)
    if not command_env.get("LOCALAPPDATA"):
        command_env["LOCALAPPDATA"] = str(home / "AppData" / "Local")
    if not command_env.get("PROCESSOR_ARCHITECTURE"):
        command_env["PROCESSOR_ARCHITECTURE"] = "AMD64"


def run_install_sh(tmp_path: Path, args: list[str], env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    shell = posix_shell()
    assert shell is not None
    command_env = os.environ.copy()
    command_env.update(env)
    command_env["MIHARI_INSTALL_TEST_MODE"] = "1"
    command_env["MIHARI_TEST_OS"] = "linux"
    command_env["MIHARI_TEST_ARCH"] = "amd64"
    command_env["MIHARI_DATA"] = str(tmp_path)
    command_env["HOME"] = str(tmp_path / "home")
    return subprocess.run(
        [shell, str(INSTALL_SH), *args],
        cwd=str(SCRIPT_DIR),
        env=command_env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        errors="replace",
        timeout=30,
    )


def run_install_ps1(tmp_path: Path, args: list[str], env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    exe = powershell()
    assert exe is not None
    command_env = os.environ.copy()
    command_env.update(env)
    command_env["MIHARI_INSTALL_TEST_MODE"] = "1"
    command_env["MIHARI_DATA"] = str(tmp_path)
    apply_ps_compat_env(command_env, tmp_path / "home")
    ps_path = str(INSTALL_PS1).replace("\\", "/")
    args_literal = ", ".join("'{0}'".format(arg.replace("'", "''")) for arg in args)
    wrapper = tmp_path / "run-install.ps1"
    wrapper.write_text(
        "$ErrorActionPreference = 'Stop'\n"
        f"$code = [IO.File]::ReadAllText('{ps_path}', [Text.Encoding]::UTF8)\n"
        "$wrapped = @'\n"
        f"$args = @({args_literal});\n"
        "'@ + $code\n"
        "& ([scriptblock]::Create($wrapped))\n",
        encoding="utf-8",
    )
    return subprocess.run(
        [exe, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(wrapper)],
        cwd=str(SCRIPT_DIR),
        env=command_env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        errors="replace",
        timeout=60,
    )


@requires_sh
def test_script1_sh_default_url_is_latest(tmp_path: Path):
    sidecar = tmp_path / "mihari-channel"
    sidecar.write_text("dev\n", encoding="utf-8")
    result = run_install_sh(tmp_path, [], {})
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert "/releases/latest/download/mihari-linux-amd64" in got.get("URL", "")
    assert got.get("EXPLICIT") == "0"
    assert sidecar.read_text(encoding="utf-8") == "dev\n"


@requires_sh
def test_script1_sh_channel_flag_and_equals(tmp_path: Path, github_server: GitHubListServer):
    api = f"http://127.0.0.1:{github_server.server_address[1]}"
    for args in (["--channel", "dev"], ["--channel=dev"]):
        github_server.paths.clear()
        result = run_install_sh(tmp_path, args, {"MIHARI_GITHUB_API": api})
        assert result.returncode == 0, result.stderr + result.stdout
        got = parse_test_output(result.stdout)
        assert got.get("CHANNEL") == "dev"
        assert got.get("EXPLICIT") == "1"
        assert f"/releases/download/{CANONICAL_DEV}/mihari-linux-amd64" in got.get("URL", "")
        assert (tmp_path / "mihari-channel").read_text(encoding="utf-8") == "dev\n"
        paths, queries = github_server.snapshot()
        assert any(path.endswith("/releases") for path in paths)
        assert any("per_page=100" in query for query in queries)
        assert not any(path.endswith("/releases/latest") for path in paths)


@requires_sh
def test_script1_sh_follows_next_not_last(tmp_path: Path, github_server: GitHubListServer):
    port = github_server.server_address[1]
    base = f"http://127.0.0.1:{port}/repos/mihari-proxy/mihari/releases"
    github_server.pages["1"] = (
        b'[{"tag_name":"v0.9.0-dev.1"}]',
        {
            "Link": f'<{base}?page=2>; rel="next", <{base}?page=9>; rel="last"',
        },
    )
    github_server.pages["2"] = (b'[{"tag_name":"v0.9.0-dev.4"}]', {})
    github_server.pages["9"] = (b'[{"tag_name":"v0.9.0-dev.99"}]', {})
    result = run_install_sh(tmp_path, ["--channel", "dev"], {"MIHARI_GITHUB_API": f"http://127.0.0.1:{port}"})
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert "/releases/download/v0.9.0-dev.4/" in got.get("URL", "")
    _, queries = github_server.snapshot()
    assert not any("page=9" in query for query in queries)


@requires_sh
def test_script1_sh_reports_downgrade_for_older_pin(tmp_path: Path):
    result = run_install_sh(
        tmp_path,
        [],
        {
            "MIHARI_VERSION": "v0.8.2",
            "MIHARI_TEST_INSTALLED_VERSION": "v0.9.0-dev.8",
        },
    )
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert got.get("TARGET_TAG") == "v0.8.2"
    assert got.get("INSTALLED") == "v0.9.0-dev.8"
    assert got.get("DOWNGRADE") == "1"


@requires_sh
def test_script1_sh_newer_pin_is_not_downgrade(tmp_path: Path):
    result = run_install_sh(
        tmp_path,
        [],
        {
            "MIHARI_VERSION": "v0.9.0",
            "MIHARI_TEST_INSTALLED_VERSION": "v0.8.2",
        },
    )
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert got.get("DOWNGRADE") == "0"


@requires_sh
def test_script1_sh_pinned_version_wins_and_does_not_write(tmp_path: Path):
    sidecar = tmp_path / "mihari-channel"
    sidecar.write_text("main\n", encoding="utf-8")
    result = run_install_sh(tmp_path, [], {"MIHARI_VERSION": "v0.9.0-dev.3"})
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert "/releases/download/v0.9.0-dev.3/mihari-linux-amd64" in got.get("URL", "")
    assert sidecar.read_text(encoding="utf-8") == "main\n"


@requires_sh
def test_script1_sh_rejects_unknown_and_invalid_before_download(tmp_path: Path, github_server: GitHubListServer):
    api = f"http://127.0.0.1:{github_server.server_address[1]}"
    unknown = run_install_sh(tmp_path, ["--nope"], {"MIHARI_GITHUB_API": api})
    assert unknown.returncode != 0
    missing = run_install_sh(tmp_path, ["--channel"], {"MIHARI_GITHUB_API": api})
    assert missing.returncode != 0
    invalid = run_install_sh(tmp_path, ["--channel", "stable"], {"MIHARI_GITHUB_API": api})
    assert invalid.returncode != 0
    extra = run_install_sh(tmp_path, ["leftover"], {"MIHARI_GITHUB_API": api})
    assert extra.returncode != 0
    empty = run_install_sh(tmp_path, ["--channel", ""], {"MIHARI_GITHUB_API": api})
    assert empty.returncode != 0
    assert github_server.snapshot()[0] == []


@requires_sh
def test_unix_install_scripts_chown_new_channel_root():
    for path in (INSTALL_SH, INSTALL_AIO_SH):
        text = path.read_text(encoding="utf-8")
        assert '[ -d "$root" ] || created=1' in text, path.name
        assert 'chown "$uid:$gid" "$root"' in text, path.name
        assert 'chown "$uid:$gid" "$root/mihari-channel"' in text, path.name


@requires_sh
def test_script1_sh_dev_failure_does_not_fallback_latest(tmp_path: Path, github_server: GitHubListServer):
    github_server.fail = True
    result = run_install_sh(
        tmp_path,
        ["--channel", "dev"],
        {"MIHARI_GITHUB_API": f"http://127.0.0.1:{github_server.server_address[1]}"},
    )
    assert result.returncode != 0
    assert "MIHARI_VERSION=" in result.stderr
    assert "/releases/latest" not in result.stdout
    assert not (tmp_path / "mihari-channel").exists()


@requires_ps
def test_script1_ps1_has_no_param_block():
    text = INSTALL_PS1.read_text(encoding="utf-8")
    assert "param(" not in text


@requires_ps
def test_script1_ps1_reports_downgrade_for_older_pin(tmp_path: Path):
    result = run_install_ps1(
        tmp_path,
        [],
        {
            "MIHARI_VERSION": "v0.8.2",
            "MIHARI_TEST_INSTALLED_VERSION": "v0.9.0-dev.8",
        },
    )
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert got.get("TARGET_TAG") == "v0.8.2"
    assert got.get("INSTALLED") == "v0.9.0-dev.8"
    assert got.get("DOWNGRADE") == "1"


@requires_ps
def test_script1_ps1_default_url_is_latest(tmp_path: Path):
    sidecar = tmp_path / "mihari-channel"
    sidecar.write_text("dev\n", encoding="utf-8")
    result = run_install_ps1(tmp_path, [], {})
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert "/releases/latest/download/mihari-windows-" in got.get("URL", "")
    assert got.get("EXPLICIT") == "0"
    assert sidecar.read_text(encoding="utf-8") == "dev\n"


@requires_ps
def test_script1_ps1_without_windows_profile_env(tmp_path: Path):
    result = run_install_ps1(
        tmp_path,
        [],
        {"LOCALAPPDATA": "", "USERPROFILE": "", "PROCESSOR_ARCHITECTURE": ""},
    )
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert "/releases/latest/download/mihari-windows-" in got.get("URL", "")


@requires_ps
def test_script1_ps1_channel_args_and_env(tmp_path: Path, github_server: GitHubListServer):
    api = f"http://127.0.0.1:{github_server.server_address[1]}"
    result = run_install_ps1(tmp_path, ["-Channel", "dev"], {"MIHARI_GITHUB_API": api})
    assert result.returncode == 0, result.stderr + result.stdout
    got = parse_test_output(result.stdout)
    assert got.get("CHANNEL") == "dev"
    assert f"/releases/download/{CANONICAL_DEV}/mihari-windows-" in got.get("URL", "")
    assert (tmp_path / "mihari-channel").read_text(encoding="utf-8") == "dev\n"

    colon = run_install_ps1(tmp_path, ["-Channel:main"], {"MIHARI_GITHUB_API": api, "MIHARI_CHANNEL": "dev"})
    assert colon.returncode == 0, colon.stderr
    got = parse_test_output(colon.stdout)
    assert got.get("CHANNEL") == "main"
    assert "/releases/latest/download/" in got.get("URL", "")


@requires_ps
def test_script1_ps1_nested_github_json_picks_canonical_dev(tmp_path: Path, github_server: GitHubListServer):
    github_server.default_body = NESTED_GITHUB_RELEASES.encode()
    api = f"http://127.0.0.1:{github_server.server_address[1]}"
    result = run_install_ps1(tmp_path, ["-Channel", "dev"], {"MIHARI_GITHUB_API": api})
    assert result.returncode == 0, result.stderr + result.stdout
    got = parse_test_output(result.stdout)
    assert f"/releases/download/{CANONICAL_DEV}/mihari-windows-" in got.get("URL", "")


@requires_ps
def test_script1_ps1_env_channel_without_flag(tmp_path: Path, github_server: GitHubListServer):
    api = f"http://127.0.0.1:{github_server.server_address[1]}"
    result = run_install_ps1(tmp_path, [], {"MIHARI_CHANNEL": "dev", "MIHARI_GITHUB_API": api})
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert got.get("CHANNEL") == "dev"
    assert got.get("EXPLICIT") == "1"


@requires_ps
def test_script1_ps1_rejects_invalid_channel(tmp_path: Path):
    result = run_install_ps1(tmp_path, ["-Channel", "stable"], {})
    assert result.returncode != 0
    upper = run_install_ps1(tmp_path, ["-Channel", "DEV"], {})
    assert upper.returncode != 0


def test_unix_install_scripts_validate_sudo_user_before_eval():
    for path in (INSTALL_SH, INSTALL_AIO_SH):
        text = path.read_text(encoding="utf-8")
        assert '*[!A-Za-z0-9._-]*' in text, path.name


def test_ps1_channel_and_tag_matches_are_case_sensitive():
    for path in (INSTALL_PS1, INSTALL_AIO_PS1, INSTALL_AIO_REMOTE_PS1):
        text = path.read_text(encoding="utf-8")
        assert "-cnotin" in text, path.name
    for path in (INSTALL_PS1, INSTALL_AIO_REMOTE_PS1):
        text = path.read_text(encoding="utf-8")
        assert "-cmatch" in text, path.name


def test_script1_ps1_writes_sidecar_after_binary_commit():
    text = INSTALL_PS1.read_text(encoding="utf-8")
    block = text[text.index("Invoke-WebRequest -Uri $url -OutFile $tmp") :]
    assert block.index("Start-Service -Name mihari") < block.index("Write-MihariChannel $channel")
    assert block.rindex("Move-Item -LiteralPath $tmp -Destination $dest -Force") < block.rindex(
        "Write-MihariChannel $channel"
    )
    assert '"draft"\\s*:\\s*true' in text


INSTALL_AIO_SH = SCRIPT_DIR / "install-aio.sh"
INSTALL_AIO_PS1 = SCRIPT_DIR / "install-aio.ps1"


def make_aio_bundle(root: Path, windows: bool) -> Path:
    bundle = root / "bundle"
    bin_name = "mihari.exe" if windows else "mihari"
    core_name = "mihomo.exe" if windows else "mihomo"
    (bundle / "data" / "bin").mkdir(parents=True)
    (bundle / "data" / "geoip").mkdir(parents=True)
    (bundle / bin_name).write_bytes(b"mihari-bin")
    (bundle / "data" / "bin" / core_name).write_bytes(b"mihomo-bin")
    (bundle / "data" / "bin" / "core-channel").write_text("stable\n", encoding="utf-8")
    (bundle / "data" / "geoip" / "GeoLite2-Country.mmdb").write_bytes(b"country")
    (bundle / "data" / "geoip" / "GeoLite2-ASN.mmdb").write_bytes(b"asn")
    return bundle


def run_install_aio_sh(
    tmp_path: Path, args: list[str], extra_env: dict[str, str] | None = None
) -> subprocess.CompletedProcess[str]:
    shell = posix_shell()
    assert shell is not None
    env = os.environ.copy()
    env.pop("MIHARI_CHANNEL", None)
    env["MIHARI_INSTALL_TEST_MODE"] = "1"
    env["MIHARI_BIN"] = str(tmp_path / "bin")
    env["MIHARI_DATA"] = str(tmp_path / "data")
    env["HOME"] = str(tmp_path / "home")
    if extra_env:
        env.update(extra_env)
    sh_args = [Path(arg).as_posix() if ":\\" in arg or arg.startswith("\\\\") else arg for arg in args]
    return subprocess.run(
        [shell, Path(INSTALL_AIO_SH).as_posix(), *sh_args],
        cwd=str(SCRIPT_DIR),
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        errors="replace",
        timeout=30,
    )


def run_install_aio_ps1(
    tmp_path: Path, args: list[str], extra_env: dict[str, str] | None = None
) -> subprocess.CompletedProcess[str]:
    exe = powershell()
    assert exe is not None
    env = os.environ.copy()
    env.pop("MIHARI_CHANNEL", None)
    env["MIHARI_INSTALL_TEST_MODE"] = "1"
    env["MIHARI_BIN"] = str(tmp_path / "bin")
    env["MIHARI_DATA"] = str(tmp_path / "data")
    apply_ps_compat_env(env, tmp_path / "home")
    if extra_env:
        env.update(extra_env)
    ps_path = str(INSTALL_AIO_PS1).replace("\\", "/")
    pieces = []
    i = 0
    while i < len(args):
        token = args[i]
        if token.startswith("-") and i + 1 < len(args) and not args[i + 1].startswith("-"):
            pieces.append(token)
            pieces.append("'{0}'".format(args[i + 1].replace("'", "''")))
            i += 2
            continue
        pieces.append("'{0}'".format(token.replace("'", "''")))
        i += 1
    invoke = " ".join(pieces)
    command = (
        "$ErrorActionPreference='Stop'; "
        f"$code = [IO.File]::ReadAllText('{ps_path}', [Text.Encoding]::UTF8); "
        f"& ([scriptblock]::Create($code)) {invoke}"
    )
    return subprocess.run(
        [exe, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command],
        cwd=str(SCRIPT_DIR),
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        errors="replace",
        timeout=60,
    )


@requires_sh
def test_script2_sh_channel_dev_does_not_use_flag_as_bundle_dir(tmp_path: Path):
    bundle = make_aio_bundle(tmp_path, windows=False)
    for args in (["--channel", "dev", str(bundle)], ["--channel=dev", str(bundle)], [str(bundle), "--channel", "dev"]):
        sidecar = tmp_path / "data" / "mihari-channel"
        if sidecar.exists():
            sidecar.unlink()
        result = run_install_aio_sh(tmp_path, args)
        assert result.returncode == 0, result.stderr + " args=" + str(args)
        assert sidecar.read_text(encoding="utf-8") == "dev\n"
        assert (tmp_path / "data" / "bin" / "core-channel").read_text(encoding="utf-8") == "stable\n"


@requires_sh
def test_script2_sh_reports_downgrade_for_older_bundle(tmp_path: Path):
    bundle = make_aio_bundle(tmp_path, windows=False)
    result = run_install_aio_sh(
        tmp_path,
        [str(bundle)],
        {
            "MIHARI_TEST_INSTALLED_VERSION": "v0.9.0-dev.8",
            "MIHARI_TEST_TARGET_VERSION": "v0.8.2",
        },
    )
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert got.get("TARGET_TAG") == "v0.8.2"
    assert got.get("INSTALLED") == "v0.9.0-dev.8"
    assert got.get("DOWNGRADE") == "1"


@requires_sh
def test_script2_sh_unspecified_does_not_write_or_delete(tmp_path: Path):
    bundle = make_aio_bundle(tmp_path, windows=False)
    data = tmp_path / "data"
    data.mkdir()
    sidecar = data / "mihari-channel"
    sidecar.write_text("main\n", encoding="utf-8")
    yaml_path = data / "mihari.yaml"
    yaml_path.write_text("keep: true\n", encoding="utf-8")
    result = run_install_aio_sh(tmp_path, [str(bundle)])
    assert result.returncode == 0, result.stderr
    assert sidecar.read_text(encoding="utf-8") == "main\n"
    assert yaml_path.read_text(encoding="utf-8") == "keep: true\n"


@requires_ps
def test_script2_ps1_channel_writes_sidecar_keeps_core_channel(tmp_path: Path):
    bundle = make_aio_bundle(tmp_path, windows=True)
    data = tmp_path / "data"
    data.mkdir()
    yaml_path = data / "mihari.yaml"
    yaml_path.write_text("keep: true\n", encoding="utf-8")
    result = run_install_aio_ps1(tmp_path, ["-BundleDir", str(bundle), "-Channel", "dev"])
    assert result.returncode == 0, result.stderr + result.stdout
    assert (data / "mihari-channel").read_text(encoding="utf-8") == "dev\n"
    assert (data / "bin" / "core-channel").read_text(encoding="utf-8") == "stable\n"
    assert yaml_path.read_text(encoding="utf-8") == "keep: true\n"


@requires_ps
def test_script2_ps1_reports_downgrade_for_older_bundle(tmp_path: Path):
    bundle = make_aio_bundle(tmp_path, windows=True)
    result = run_install_aio_ps1(
        tmp_path,
        ["-BundleDir", str(bundle)],
        {
            "MIHARI_TEST_INSTALLED_VERSION": "v0.9.0-dev.8",
            "MIHARI_TEST_TARGET_VERSION": "v0.8.2",
        },
    )
    assert result.returncode == 0, result.stderr + result.stdout
    got = parse_test_output(result.stdout)
    assert got.get("TARGET_TAG") == "v0.8.2"
    assert got.get("INSTALLED") == "v0.9.0-dev.8"
    assert got.get("DOWNGRADE") == "1"


@requires_ps
def test_script2_ps1_unspecified_leaves_sidecar(tmp_path: Path):
    bundle = make_aio_bundle(tmp_path, windows=True)
    data = tmp_path / "data"
    data.mkdir()
    sidecar = data / "mihari-channel"
    sidecar.write_text("main\n", encoding="utf-8")
    result = run_install_aio_ps1(tmp_path, ["-BundleDir", str(bundle)])
    assert result.returncode == 0, result.stderr
    assert sidecar.read_text(encoding="utf-8") == "main\n"


INSTALL_AIO_REMOTE_SH = SCRIPT_DIR / "install-aio-remote.sh"
INSTALL_AIO_REMOTE_PS1 = SCRIPT_DIR / "install-aio-remote.ps1"
PUBLIC_STABLE_INDEX = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt"
PUBLIC_DEV_INDEX = "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt"


class IndexServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, body: bytes):
        super().__init__(("127.0.0.1", 0), IndexHandler)
        self.body = body
        self.paths: list[str] = []
        self.lock = threading.Lock()


class IndexHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *args):
        return

    def do_GET(self):
        with self.server.lock:
            self.server.paths.append(self.path)
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(self.server.body)))
        self.end_headers()
        self.wfile.write(self.server.body)


def run_remote_sh(args: list[str], env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    shell = posix_shell()
    assert shell is not None
    command_env = os.environ.copy()
    command_env.pop("MIHARI_CHANNEL", None)
    command_env.pop("MIHARI_INDEX_URL", None)
    command_env.pop("MIHARI_BUNDLE_URL", None)
    command_env.update(env)
    command_env["MIHARI_INSTALL_TEST_MODE"] = "1"
    return subprocess.run(
        [shell, Path(INSTALL_AIO_REMOTE_SH).as_posix(), *args],
        cwd=str(SCRIPT_DIR),
        env=command_env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        errors="replace",
        timeout=30,
    )


def run_remote_ps1(args: list[str], env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    exe = powershell()
    assert exe is not None
    command_env = os.environ.copy()
    command_env.pop("MIHARI_CHANNEL", None)
    command_env.pop("MIHARI_INDEX_URL", None)
    command_env.pop("MIHARI_BUNDLE_URL", None)
    command_env.update(env)
    command_env["MIHARI_INSTALL_TEST_MODE"] = "1"
    apply_ps_compat_env(command_env)
    ps_path = str(INSTALL_AIO_REMOTE_PS1).replace("\\", "/")
    pieces = []
    i = 0
    while i < len(args):
        token = args[i]
        if token.startswith("-") and i + 1 < len(args) and not args[i + 1].startswith("-"):
            pieces.append(token)
            pieces.append("'{0}'".format(args[i + 1].replace("'", "''")))
            i += 2
            continue
        pieces.append(token if token.startswith("-") else "'{0}'".format(token.replace("'", "''")))
        i += 1
    invoke = " ".join(pieces)
    command = (
        "$ErrorActionPreference='Stop'; "
        f"$code = [IO.File]::ReadAllText('{ps_path}', [Text.Encoding]::UTF8); "
        f"& ([scriptblock]::Create($code)) {invoke}"
    )
    return subprocess.run(
        [exe, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command],
        env=command_env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        errors="replace",
        timeout=60,
    )


@requires_sh
def test_script3_sh_default_index_is_stable():
    result = run_remote_sh([], {})
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert got.get("INDEX_URL") == PUBLIC_STABLE_INDEX
    assert PUBLIC_DEV_INDEX not in result.stdout
    assert '--channel' not in got.get("HANDOFF", "")


@requires_sh
def test_script3_sh_channel_dev_uses_dev_index_and_handoff():
    result = run_remote_sh(["--channel", "dev"], {})
    assert result.returncode == 0, result.stderr + result.stdout
    got = parse_test_output(result.stdout)
    assert got.get("INDEX_URL") == PUBLIC_DEV_INDEX
    assert PUBLIC_STABLE_INDEX not in got.get("INDEX_URL", "")
    assert '--channel "dev"' in got.get("HANDOFF", "") or "--channel dev" in got.get("HANDOFF", "")


@requires_sh
def test_script3_sh_dev_rejects_stable_latest_without_bundle_download(tmp_path: Path):
    server = IndexServer(b"latest v0.8.2\nlinux-amd64 http://127.0.0.1/bundle deadbeef\n")
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        url = f"http://127.0.0.1:{server.server_address[1]}/index.txt"
        result = run_remote_sh(["--yes", "--channel", "dev"], {"MIHARI_INDEX_URL": url})
        assert result.returncode != 0, result.stdout
        assert server.paths.count("/index.txt") >= 1
        assert not any("bundle" in path for path in server.paths)
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


@requires_sh
def test_script3_sh_unknown_flag_fails_before_fetch():
    result = run_remote_sh(["--nope"], {"MIHARI_INDEX_URL": "http://127.0.0.1:1/index.txt"})
    assert result.returncode != 0


@requires_ps
def test_script3_ps1_channel_dev_uses_dev_index_and_handoff():
    result = run_remote_ps1(["-Channel", "dev"], {})
    assert result.returncode == 0, result.stderr + result.stdout
    got = parse_test_output(result.stdout)
    assert got.get("INDEX_URL") == PUBLIC_DEV_INDEX
    assert "-Channel" in got.get("HANDOFF", "")


@requires_ps
def test_script3_ps1_default_index_is_stable():
    result = run_remote_ps1([], {})
    assert result.returncode == 0, result.stderr
    got = parse_test_output(result.stdout)
    assert got.get("INDEX_URL") == PUBLIC_STABLE_INDEX
    assert "-Channel" not in got.get("HANDOFF", "")


