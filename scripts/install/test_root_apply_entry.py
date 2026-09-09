"""Exercise bootstrap entry selection without root, network, or service IO."""
import hashlib
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import unittest


INSTALL = Path(__file__).parent


@unittest.skipUnless(
    os.name == "posix" and shutil.which("sh") and (shutil.which("sha256sum") or shutil.which("shasum")),
    "requires native POSIX shell and a SHA256 tool",
)
class RootApplyEntryTests(unittest.TestCase):
    def run_selection(self, installed=True, offline=False, valid_checksum=True):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            old = root / "old"
            old.write_text("#!/bin/sh\n[ \"$3\" = --help ] && exit 0\nprintf 'old-entry\\n'\n")
            old.chmod(0o700)
            new = root / "official"
            new.write_text("#!/bin/sh\nprintf 'new-entry\\n'\n")
            digest = hashlib.sha256(new.read_bytes()).hexdigest() if valid_checksum else "0" * 64
            (root / "manifest").write_text(f"{digest}  mihari-linux-amd64\n")
            (root / "stage").mkdir()
            source = (INSTALL / "root-apply.sh.in").read_text()
            start = source.index("entry=/usr/local/lib/mihari/mihari\n")
            end = source.index("# BEGIN REQUEST JSON", start)
            selection = source[start:end].replace("entry=/usr/local/lib/mihari/mihari", "entry=" + shlex.quote(str(old)), 1)
            script = "set -eu\n" + "\n".join([
                "stage=" + shlex.quote(str(root / "stage")),
                "official=" + shlex.quote(str(new)),
                "manifest=" + shlex.quote(str(root / "manifest")),
                "candidate=" + shlex.quote(str(root / "offline") if offline else ""),
                "os=linux; arch=amd64; tag=v0.9.3-dev.2",
                "trusted_entry() { return " + ("0" if installed else "1") + "; }",
                'fail() { printf "%s\\n" "$1" >&2; exit 1; }',
                'checksum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d" " -f1; }',
                'root_fetch() { case "$1" in */SHA256SUMS.txt) cp "$manifest" "$2";; *) cp "$official" "$2";; esac; }',
                selection,
                '"$entry" service apply',
            ])
            return subprocess.run(["sh", "-c", script], capture_output=True, text=True, timeout=10)

    def test_online_upgrade_executes_verified_new_entry(self):
        result = self.run_selection()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "new-entry\n")

    def test_first_install_executes_verified_entry(self):
        result = self.run_selection(installed=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "new-entry\n")

    def test_offline_install_keeps_trusted_installed_entry(self):
        result = self.run_selection(offline=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "old-entry\n")

    def test_bad_checksum_never_executes_an_apply_entry(self):
        result = self.run_selection(valid_checksum=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("official binary checksum mismatch", result.stderr)
        self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
