import pytest

from release_policy import (
    compare_versions,
    expected_base_path,
    parse_version,
    validate_base_path,
)


@pytest.mark.parametrize(
    "left,right,expected",
    [
        ("v0.9.0-dev.1", "v0.9.0-dev.2", -1),
        ("v0.9.0-dev.2", "v0.9.0-dev.1", 1),
        ("v0.9.0-dev.1", "v0.10.0-dev.1", -1),
        ("v0.9.0-dev.1", "v0.9.0-dev.1", 0),
    ],
)
def test_compare_versions_orders_dev_releases(left, right, expected):
    assert compare_versions(left, right, "dev") == expected


@pytest.mark.parametrize(
    "value,channel",
    [
        ("v1.2.3-dev.1", "stable"),
        ("v1.2.3", "dev"),
        ("v1.2.3-dev.1", "preview"),
        ("1.2.3", "stable"),
        ("v1.2", "stable"),
        ("v1.2.3-dev.-1", "dev"),
    ],
)
def test_parse_version_rejects_wrong_channel_or_shape(value, channel):
    with pytest.raises(ValueError):
        parse_version(value, channel)


def test_parse_version_returns_components():
    assert parse_version("v1.2.3", "stable") == (1, 2, 3, None)
    assert parse_version("v1.2.3-dev.7", "dev") == (1, 2, 3, 7)


def test_compare_versions_rejects_cross_channel_shapes():
    with pytest.raises(ValueError):
        compare_versions("v1.2.3", "v1.2.3-dev.1", "stable")


@pytest.mark.parametrize(
    "channel,expected",
    [
        ("stable", "/mihari-release/mihari"),
        ("dev", "/mihari-release/mihari-dev"),
    ],
)
def test_expected_base_path_is_channel_specific(channel, expected):
    assert expected_base_path(channel) == expected


@pytest.mark.parametrize(
    "channel,path",
    [
        ("stable", ""),
        ("stable", "/mihari-release/mihari/../secret"),
        ("stable", "/mihari-release/mihari-dev"),
        ("dev", "/mihari-release/mihari"),
        ("dev", "/mihari-release/other"),
        ("preview", "/mihari-release/mihari"),
    ],
)
def test_validate_base_path_rejects_empty_traversal_and_cross_channel(channel, path):
    with pytest.raises(ValueError):
        validate_base_path(channel, path)


@pytest.mark.parametrize(
    "channel,path",
    [
        ("stable", "/mihari-release/mihari"),
        ("dev", "/mihari-release/mihari-dev"),
    ],
)
def test_validate_base_path_accepts_exact_channel_path(channel, path):
    assert validate_base_path(channel, path) is None
