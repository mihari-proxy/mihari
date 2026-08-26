"""Validate that a stable release changelog has closed Unreleased into the version section."""

import argparse
import datetime
from pathlib import Path
import re
import sys


_MAX_CHANGELOG_BYTES = 1_048_576
_STABLE_VERSION_PATTERN = re.compile(
    r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$"
)
_H2_PATTERN = re.compile(r"^##[ \t]+(.+?)[ \t]*$")
_UNRELEASED_HEADING = re.compile(r"^\[Unreleased\]$")
_VERSION_HEADING = re.compile(
    r"^\[(v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))\]"
    r"[ \t]+-[ \t]+(\d{4}-\d{2}-\d{2})$"
)
_BULLET_PATTERN = re.compile(r"^[ \t]*[-*+][ \t]+\S")


class ChangelogPolicyError(ValueError):
    """Raised when CHANGELOG.md cannot be used for a stable release."""


def validate_stable_changelog(text: str, version: str) -> None:
    """Require a closed Unreleased section and a matching first stable version section."""
    if not isinstance(text, str) or not isinstance(version, str):
        raise ChangelogPolicyError("invalid changelog input")
    if _STABLE_VERSION_PATTERN.fullmatch(version) is None:
        raise ChangelogPolicyError("version must match the required stable format")

    lines = text.splitlines()
    headings: list[tuple[int, str]] = []
    for index, line in enumerate(lines):
        match = _H2_PATTERN.fullmatch(line)
        if match is not None:
            headings.append((index, match.group(1)))

    if not headings or _UNRELEASED_HEADING.fullmatch(headings[0][1]) is None:
        raise ChangelogPolicyError("changelog is missing the Unreleased section")

    version_headings: list[tuple[int, str]] = []
    for position, (index, heading) in enumerate(headings[1:], start=1):
        parsed = _VERSION_HEADING.fullmatch(heading)
        if parsed is None:
            raise ChangelogPolicyError("stable version heading is invalid")
        section_version, date_text = parsed.group(1), parsed.group(2)
        try:
            year, month, day = (int(part) for part in date_text.split("-"))
            datetime.date(year, month, day)
        except ValueError as error:
            raise ChangelogPolicyError("stable version heading date is invalid") from error
        version_headings.append((index, section_version))
        next_index = headings[position + 1][0] if position + 1 < len(headings) else len(lines)
        body = lines[index + 1 : next_index]
        if section_version == version and not any(_BULLET_PATTERN.match(item) for item in body):
            raise ChangelogPolicyError("stable version section has no entries")

    unreleased_end = headings[1][0] if len(headings) > 1 else len(lines)
    unreleased_body = lines[headings[0][0] + 1 : unreleased_end]
    if any(_BULLET_PATTERN.match(item) for item in unreleased_body):
        raise ChangelogPolicyError("Unreleased section still contains changelog entries")

    if not version_headings:
        raise ChangelogPolicyError("changelog is missing the required stable version section")

    seen: set[str] = set()
    for _, section_version in version_headings:
        if section_version in seen:
            raise ChangelogPolicyError("changelog contains a duplicate version heading")
        seen.add(section_version)

    if version not in seen:
        raise ChangelogPolicyError("changelog is missing the required stable version section")
    if version_headings[0][1] != version:
        raise ChangelogPolicyError("stable version section is not the first version after Unreleased")


def _read_changelog(path_value: str) -> str:
    path = Path(path_value)
    try:
        with path.open("rb") as source:
            data = source.read(_MAX_CHANGELOG_BYTES + 1)
        if len(data) > _MAX_CHANGELOG_BYTES:
            raise ChangelogPolicyError("changelog exceeds the allowed size")
        return data.decode("utf-8-sig")
    except ChangelogPolicyError:
        raise
    except (OSError, UnicodeDecodeError) as error:
        raise ChangelogPolicyError("invalid changelog input") from error


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--changelog", required=True)
    parser.add_argument("--version", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    """Run the changelog gate and return a process status without exposing inputs."""
    arguments = _parser().parse_args(argv)
    try:
        validate_stable_changelog(_read_changelog(arguments.changelog), arguments.version)
    except ChangelogPolicyError as error:
        print(f"changelog policy validation failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
