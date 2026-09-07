"""The entire root bridge must remain reproducibly generated and standalone."""
from pathlib import Path
import subprocess
import sys

INSTALL = Path(__file__).parent
SCRIPTS = ("install.sh", "install-aio.sh", "install-aio-remote.sh")


def test_full_root_apply_block_drift_and_regeneration(tmp_path):
    for name in (*SCRIPTS, "root-apply.sh.in"):
        (tmp_path / name).write_bytes((INSTALL / name).read_bytes())
    command = [sys.executable, str(INSTALL / "generate_root_apply.py"), "--directory", str(tmp_path)]
    assert subprocess.run([*command, "--check"], capture_output=True).returncode == 0
    for name in SCRIPTS:
        path = tmp_path / name
        original = path.read_text(encoding="utf-8")
        path.write_text(original.replace("# BEGIN ROOT APPLY", "# BEGIN ROOT APPLY\n# full-block drift", 1), encoding="utf-8")
        result = subprocess.run([*command, "--check"], capture_output=True, text=True)
        assert result.returncode != 0, f"complete privileged bridge drift was accepted in {name}"
        assert name in result.stdout + result.stderr
        assert subprocess.run(command, capture_output=True).returncode == 0
        assert path.read_text(encoding="utf-8") == original
        assert subprocess.run([*command, "--check"], capture_output=True).returncode == 0
