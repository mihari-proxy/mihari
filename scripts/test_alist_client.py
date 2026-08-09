"""Regression tests for the alist_client shared constants/helpers.

Guards the PLATFORMS shape that release-alist.py and retract-alist.py consume
via `for goos, goarch in PLATFORMS`. The first v0.2.0 release crashed at
release-alist.py:47 (ValueError: too many values to unpack) because PLATFORMS
was a list of "goos/goarch" strings — unpacking an 11-char string into two
names yields too many values. These tests pin PLATFORMS to (goos, goarch)
pairs so neither the publish nor the retract flow can regress.
"""
import importlib.util
from pathlib import Path

from alist_client import AList, PLATFORMS, bundle_name, semver_key


def test_platforms_unpack_as_goos_goarch_pairs():
    # The exact unpacking the publish/retract loops perform — must not raise.
    names = [bundle_name(goos, goarch) for goos, goarch in PLATFORMS]
    assert len(names) == len(PLATFORMS)


def test_platforms_are_two_tuples_not_strings():
    # The bug: a "linux/amd64" string unpacks into >2 values. Reject that shape.
    for entry in PLATFORMS:
        assert not isinstance(entry, str), f"PLATFORMS entry must be a pair, got string {entry!r}"
        goos, goarch = entry
        assert isinstance(goos, str) and isinstance(goarch, str)


def test_platforms_cover_six_targets():
    assert len(PLATFORMS) == 6


def test_bundle_name_formats():
    assert bundle_name("linux", "amd64") == "mihari-all-in-one-linux-amd64.tar.gz"
    assert bundle_name("darwin", "arm64") == "mihari-all-in-one-darwin-arm64.tar.gz"
    assert bundle_name("windows", "arm64") == "mihari-all-in-one-windows-arm64.zip"


def test_semver_key_orders_versions():
    assert semver_key("v0.2.0") == (0, 2, 0)
    assert semver_key("v0.10.3") == (0, 10, 3)
    assert semver_key("not-a-version") is None


def test_public_url_is_signless_proxy_route():
    # AList.__init__ would hit the network (_login); bypass it and set only the
    # attribute public_url reads.
    alist = AList.__new__(AList)
    alist.base = "https://cloud.example.com"
    # public_url injects "/public" between /p and the fs path (AList topology quirk).
    assert alist.public_url("/mihari-release/mihari/index.txt") == "https://cloud.example.com/p/public/mihari-release/mihari/index.txt"
    bundle = alist.public_url("/mihari-release/mihari/v0.3.0/mihari-all-in-one-linux-amd64.tar.gz")
    assert "?sign=" not in bundle
    assert bundle == "https://cloud.example.com/p/public/mihari-release/mihari/v0.3.0/mihari-all-in-one-linux-amd64.tar.gz"


def test_fs_path_strips_mount_segment():
    # AList fs-path quirk: EVERY fs API op (get/list/put/mkdir/remove) resolves
    # its path relative to the storage root (/mihari-release), prepending it
    # again — so /mihari-release/mihari/X must be passed as /mihari/X or it
    # reads/writes the doubled location. _fs_path drops the first segment for
    # ALL fs ops (read and write) so they agree with /p/public downloads.
    alist = AList.__new__(AList)
    assert alist._fs_path("/mihari-release/mihari/v0.3.0/mihari-all-in-one-linux-amd64.tar.gz") == "/mihari/v0.3.0/mihari-all-in-one-linux-amd64.tar.gz"
    assert alist._fs_path("/mihari-release/mihari/index.txt") == "/mihari/index.txt"
    # fs/remove's `dir` / fs:list of base_path is the bare base_path.
    assert alist._fs_path("/mihari-release/mihari") == "/mihari"
    # No leading segment to strip → returned unchanged (no crash on odd shapes).
    assert alist._fs_path("/mihari-release") == "/mihari-release"


def _load_release_alist():
    # release-alist.py has a hyphen in its name, so import it manually.
    path = Path(__file__).with_name("release-alist.py")
    spec = importlib.util.spec_from_file_location("release_alist", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_build_index_emits_public_urls_no_sign():
    release = _load_release_alist()

    class FakeAList:
        base = "https://cloud.example.com"

        def exists(self, path):
            return True

        def public_url(self, path):
            return f"{self.base}/p/public{path}"

    # Stub sha256_file so build_index needs no real bundle files on disk.
    release.sha256_file = lambda _path: "deadbeef" * 8

    body, index_url = release.build_index(FakeAList(), "dist", "/mihari-release/mihari", "v0.3.0")
    lines = body.splitlines()
    assert lines[0] == "latest v0.3.0"
    assert len(lines) == 1 + len(PLATFORMS)
    for line in lines[1:]:
        platform, url, digest = line.split()
        assert url.startswith("https://cloud.example.com/p/public/mihari-release/mihari/v0.3.0/")
        assert "?sign=" not in url
        assert digest == "deadbeef" * 8
    assert index_url == "https://cloud.example.com/p/public/mihari-release/mihari/index.txt"


def test_install_scripts_hardcode_public_index_url():
    repo_root = Path(__file__).resolve().parent.parent
    for name in ("install-aio-remote.sh", "install-aio-remote.ps1"):
        text = (repo_root / name).read_text(encoding="utf-8")
        assert "__MIHARI_INDEX_URL__" not in text, f"{name} still has the CI placeholder"
        assert "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt" in text, (
            f"{name} lacks the fixed public index URL"
        )
