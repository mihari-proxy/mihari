"""Pure release version and distribution-path policy helpers."""

import re


_VERSION_PATTERNS = {
    "stable": re.compile(r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$"),
    "dev": re.compile(
        r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-dev\.(0|[1-9][0-9]*)$"
    ),
}

_BASE_PATHS = {
    "stable": "/mihari-release/mihari",
    "dev": "/mihari-release/mihari-dev",
}


def parse_version(value: str, channel: str) -> tuple[int, int, int, int | None]:
    """Parse a release version accepted by the specified release channel."""
    pattern = _VERSION_PATTERNS.get(channel)
    if pattern is None:
        raise ValueError("unsupported release channel")

    if not isinstance(value, str):
        raise ValueError("invalid release version")
    match = pattern.fullmatch(value)
    if match is None:
        raise ValueError("invalid release version")

    components = tuple(int(component) for component in match.groups())
    if channel == "stable":
        major, minor, patch = components
        return major, minor, patch, None

    major, minor, patch, dev_number = components
    return major, minor, patch, dev_number


def compare_versions(left: str, right: str, channel: str) -> int:
    """Compare two same-channel release versions."""
    left_version = parse_version(left, channel)
    right_version = parse_version(right, channel)
    if left_version < right_version:
        return -1
    if left_version > right_version:
        return 1
    return 0


def expected_base_path(channel: str) -> str:
    """Return the only allowed AList base path for the release channel."""
    try:
        return _BASE_PATHS[channel]
    except KeyError as error:
        raise ValueError("unsupported release channel") from error


def validate_base_path(channel: str, base_path: str) -> None:
    """Reject base paths that do not exactly match the release channel policy."""
    expected_path = expected_base_path(channel)
    if not base_path or ".." in base_path or base_path != expected_path:
        raise ValueError("invalid release base path")
