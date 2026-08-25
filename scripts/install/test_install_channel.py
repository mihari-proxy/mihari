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
            if key in {"CHANNEL", "EXPLICIT", "URL"}:
                got[key] = value
    return got


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
    assert github_server.snapshot()[0] == []


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
