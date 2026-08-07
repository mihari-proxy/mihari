"""Regression tests for the alist_client shared constants/helpers.

Guards the PLATFORMS shape that release-alist.py and retract-alist.py consume
via `for goos, goarch in PLATFORMS`. The first v0.2.0 release crashed at
release-alist.py:47 (ValueError: too many values to unpack) because PLATFORMS
was a list of "goos/goarch" strings — unpacking an 11-char string into two
names yields too many values. These tests pin PLATFORMS to (goos, goarch)
pairs so neither the publish nor the retract flow can regress.
"""
from alist_client import PLATFORMS, bundle_name, semver_key


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
