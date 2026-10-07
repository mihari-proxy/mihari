"""Darwin installers warn that macOS support is incomplete and require confirmation."""

from __future__ import annotations

import os
from pathlib import Path
import shutil
import subprocess

import pytest

SCRIPT_DIR = Path(__file__).resolve().parent
INSTALL_SH = SCRIPT_DIR / "install.sh"
INSTALL_AIO_SH = SCRIPT_DIR / "install-aio.sh"
INSTALL_AIO_REMOTE_SH = SCRIPT_DIR / "install-aio-remote.sh"
WARNING = "macOS is currently unsupported. Support is incomplete and use is not recommended."
PROMPT = "Continue anyway? [y/N]"
CANCELLED = "Cancelled; macOS installation was not confirmed. Use MIHARI_YES=1 to continue anyway."


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


requires_sh = pytest.mark.skipif(posix_shell() is None, reason="POSIX sh is not available")
SCRIPTS = [INSTALL_SH, INSTALL_AIO_SH, INSTALL_AIO_REMOTE_SH]


def run_script(script: Path, extra_env: dict[str, str], args: list[str] | None = None) -> subprocess.CompletedProcess[str]:
    shell = posix_shell()
    assert shell is not None
    env = os.environ.copy()
    for key in ("MIHARI_CHANNEL", "MIHARI_INDEX_URL", "MIHARI_BUNDLE_URL", "MIHARI_YES", "MIHARI_TEST_OS", "MIHARI_TEST_MACOS_CONFIRM"):
        env.pop(key, None)
    env.update(extra_env)
    env["MIHARI_INSTALL_TEST_MODE"] = "1"
    env.setdefault("MIHARI_TEST_ARCH", "amd64")
    return subprocess.run(
        [shell, script.as_posix(), *(args or [])],
        cwd=str(SCRIPT_DIR),
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        errors="replace",
        timeout=30,
    )


@requires_sh
@pytest.mark.parametrize("script", SCRIPTS, ids=lambda path: path.name)
def test_darwin_requires_confirmation(script: Path):
    result = run_script(script, {"MIHARI_TEST_OS": "darwin"})
    assert result.returncode != 0, result.stdout
    assert WARNING in result.stderr
    assert PROMPT in result.stderr
    assert CANCELLED in result.stderr
    assert "CHANNEL=" not in result.stdout


@requires_sh
@pytest.mark.parametrize("script", SCRIPTS, ids=lambda path: path.name)
def test_darwin_confirmation_yes_continues(script: Path):
    result = run_script(script, {"MIHARI_TEST_OS": "darwin", "MIHARI_TEST_MACOS_CONFIRM": "y"})
    assert result.returncode == 0, result.stderr
    assert WARNING in result.stderr
    assert PROMPT in result.stderr
    assert CANCELLED not in result.stderr
    assert "CHANNEL=" in result.stdout


@requires_sh
@pytest.mark.parametrize("script", SCRIPTS, ids=lambda path: path.name)
def test_darwin_explicit_yes_skips_prompt_and_continues(script: Path):
    result = run_script(script, {"MIHARI_TEST_OS": "darwin", "MIHARI_YES": "1"})
    assert result.returncode == 0, result.stderr
    assert WARNING in result.stderr
    assert PROMPT not in result.stderr
    assert "CHANNEL=" in result.stdout


@requires_sh
@pytest.mark.parametrize("script", SCRIPTS, ids=lambda path: path.name)
def test_linux_install_omits_macos_warning(script: Path):
    result = run_script(script, {"MIHARI_TEST_OS": "linux"})
    assert result.returncode == 0, result.stderr
    assert WARNING not in result.stderr
    assert PROMPT not in result.stderr
    assert "CHANNEL=" in result.stdout


@requires_sh
def test_remote_yes_flag_skips_prompt_and_continues():
    result = run_script(INSTALL_AIO_REMOTE_SH, {"MIHARI_TEST_OS": "darwin"}, ["--yes"])
    assert result.returncode == 0, result.stderr
    assert WARNING in result.stderr
    assert PROMPT not in result.stderr
    assert "CHANNEL=" in result.stdout
