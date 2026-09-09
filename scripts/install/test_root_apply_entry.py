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
    def run_selection(self, installed=True, offline=False, valid_checksum=True, capable=True, latest_capable=True, online_candidate=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            old = root / "old"
            old.write_text("#!/bin/sh\nif [ \"$3\" = --help ]; then printf '%s\\n' "+("--yes --expected-preview" if capable else "--json")+"; exit 0; fi\nprintf 'old-entry\\n'\n")
            old.chmod(0o700)
            new = root / "official"
            new.write_text("#!/bin/sh\nif [ \"$3\" = --help ]; then printf '%s\\n' "+("--yes --expected-preview" if latest_capable else "--json")+"; exit 0; fi\nprintf 'new-entry\\n'\n")
            digest = hashlib.sha256(new.read_bytes()).hexdigest() if valid_checksum else "0" * 64
            (root / "manifest").write_text(f"{digest}  mihari-linux-amd64\n")
            (root / "stage").mkdir()
            (root / "offline").write_text("fixed candidate bytes")
            source = (INSTALL / "root-apply.sh.in").read_text()
            start = source.index("entry=/usr/local/lib/mihari/mihari\n")
            end = source.index("# BEGIN REQUEST JSON", start)
            selection = source[start:end].replace("entry=/usr/local/lib/mihari/mihari", "entry=" + shlex.quote(str(old)), 1)
            script = "set -eu\n" + "\n".join([
                "stage=" + shlex.quote(str(root / "stage")),
                "official=" + shlex.quote(str(new)),
                "manifest=" + shlex.quote(str(root / "manifest")),
                "candidate=" + shlex.quote(str(root / "offline") if offline or online_candidate else ""),
                "os=linux; arch=amd64; channel=dev; tag=v0.9.3-dev.2",
                "bootstrap_mode=" + ("online" if online_candidate else "offline"),
                "original_candidate=$candidate",
                'original_digest=$(if [ -n "$candidate" ]; then cksum "$candidate"; fi)',
                "trusted_entry() { return " + ("0" if installed else "1") + "; }",
                'fail() { printf "%s\\n" "$1" >&2; exit 1; }',
                'checksum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d" " -f1; }',
                'root_fetch() { [ "$bootstrap_mode" = online ] || [ -z "$candidate" ] || [ "$candidate" = "$stage/candidate" ] || fail "unexpected offline network"; case "$1" in *api.github.com*) printf \'{"tag_name":"v0.9.4-dev.1"}\' > "$2";; */SHA256SUMS.txt) cp "$manifest" "$2";; *) cp "$official" "$2";; esac; }',
                selection,
                '[ -z "$original_candidate" ] || [ "$candidate" = "$original_candidate" ] || fail "candidate changed"',
                '[ -z "$original_candidate" ] || [ "$(cksum "$candidate")" = "$original_digest" ] || fail "candidate bytes changed"',
                '[ "$tag" = v0.9.3-dev.2 ] || fail "candidate tag changed"',
                '"$entry" service apply',
            ])
            return subprocess.run(["sh", "-c", script], capture_output=True, text=True, timeout=10)

    def test_remote_candidate_bootstraps_helper_on_clean_host(self):
        result = self.run_selection(installed=False, online_candidate=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "new-entry\n")

    def test_remote_candidate_bootstraps_helper_when_installed_helper_is_old(self):
        result = self.run_selection(capable=False, online_candidate=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "new-entry\n")

    def test_offline_candidate_on_clean_host_refuses_without_network(self):
        result = self.run_selection(installed=False, offline=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Offline installation requires", result.stderr)
        self.assertNotIn("unexpected offline network", result.stderr)

    def test_online_upgrade_keeps_capable_trusted_entry(self):
        result = self.run_selection()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "old-entry\n")

    def test_first_install_executes_verified_entry(self):
        result = self.run_selection(installed=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "new-entry\n")

    def test_offline_install_keeps_trusted_installed_entry(self):
        result = self.run_selection(offline=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "old-entry\n")

    def test_old_online_helper_is_replaced_by_current_capable_helper(self):
        result = self.run_selection(capable=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "new-entry\n")

    def test_old_offline_helper_refuses_without_network(self):
        result = self.run_selection(offline=True, capable=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("offline", result.stderr.lower())
        self.assertEqual(result.stdout, "")

    def test_first_confirmation_release_without_capable_helper_refuses(self):
        result = self.run_selection(installed=False, latest_capable=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("confirmation", result.stderr.lower())
        self.assertEqual(result.stdout, "")

    def test_bad_checksum_never_executes_an_apply_entry(self):
        result = self.run_selection(valid_checksum=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("official binary checksum mismatch", result.stderr)
        self.assertEqual(result.stdout, "")


@unittest.skipUnless(os.name == "posix" and shutil.which("sh"), "requires native POSIX shell")
class RemoteReplacementConsentTests(unittest.TestCase):
    def test_explicit_confirmation_reaches_root_apply(self):
        source = (INSTALL / "install-aio-remote.sh").read_text()
        parse = source[source.index('YES=0'):source.index('if [ "$CHANNEL_EXPLICIT" -eq 0 ]')]
        call = next(line for line in source.splitlines() if line.startswith('root_apply "$release_tag"'))
        for args, value, expected in [([], "", "0"), (["--yes"], "", "1"), (["-y"], "", "1"), ([], "1", "1"), ([], "true", "0")]:
            with self.subTest(args=args, environment=value):
                script = parse + '\nrelease_tag=v1.0.0; CHANNEL=main; candidate_dir=/fixture; archive=/fixture/aio\n' + 'root_apply() { printf "%s\n" "${5:-missing}" "${6:-missing}"; }\n' + call + '\n'
                env = dict(os.environ, MIHARI_YES=value)
                result = subprocess.run(["sh", "-c", script, "remote", *args], env=env, capture_output=True, text=True, timeout=5)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.splitlines(), [expected, "online"])


if __name__ == "__main__":
    unittest.main()
