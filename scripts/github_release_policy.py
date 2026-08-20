"""Pure validation rules for GitHub development releases.

This module intentionally performs no network or credential operations.  The
workflow supplies sanitized GitHub API JSON through bounded local files.
"""

import argparse
import json
from pathlib import Path
import re
import sys


EXPECTED_DEV_ASSETS: frozenset[str] = frozenset(
    {
        "mihari-linux-amd64",
        "mihari-linux-arm64",
        "mihari-darwin-amd64",
        "mihari-darwin-arm64",
        "mihari-windows-amd64.exe",
        "mihari-windows-arm64.exe",
        "mihari-all-in-one-linux-amd64.tar.gz",
        "mihari-all-in-one-linux-arm64.tar.gz",
        "mihari-all-in-one-darwin-amd64.tar.gz",
        "mihari-all-in-one-darwin-arm64.tar.gz",
        "mihari-all-in-one-windows-amd64.zip",
        "mihari-all-in-one-windows-arm64.zip",
        "SHA256SUMS.txt",
        "AIO_SHA256SUMS.txt",
    }
)

_MAX_JSON_BYTES = 1_048_576
_SHA_PATTERN = re.compile(r"[0-9a-f]{40}")
_STABLE_VERSION_PATTERN = re.compile(r"^v([0-9]+)\.([0-9]+)\.([0-9]+)$")


class ReleasePolicyError(ValueError):
    """Raised when GitHub release metadata violates the release policy."""


def validate_release_document(
    document: dict,
    version: str,
    release_name: str,
    marker: str,
    mode: str,
) -> None:
    """Validate a dev Release GET response for preflight or final acceptance."""
    if mode not in {"preflight", "final"}:
        raise ReleasePolicyError("invalid validation mode")
    if not isinstance(document, dict):
        raise ReleasePolicyError("release document is invalid")
    if document.get("tag_name") != version or document.get("name") != release_name:
        raise ReleasePolicyError("release identity mismatch")
    if document.get("prerelease") is not True or document.get("draft") is not False:
        raise ReleasePolicyError("release is not a published prerelease")
    body = document.get("body")
    if not isinstance(body, str) or marker not in body:
        raise ReleasePolicyError("release marker mismatch")

    assets = document.get("assets", [])
    if not isinstance(assets, list):
        raise ReleasePolicyError("release assets are invalid or duplicated")
    names: list[str] = []
    for asset in assets:
        if not isinstance(asset, dict) or not isinstance(asset.get("name"), str):
            raise ReleasePolicyError("release assets are invalid or duplicated")
        names.append(asset["name"])
    if len(names) != len(set(names)):
        raise ReleasePolicyError("release assets are invalid or duplicated")

    actual = set(names)
    if not actual.issubset(EXPECTED_DEV_ASSETS):
        raise ReleasePolicyError("release contains unexpected assets")
    if mode == "final" and actual != EXPECTED_DEV_ASSETS:
        raise ReleasePolicyError("release asset set is incomplete")


def validate_tag_chain(chain: list[dict], expected_sha: str) -> None:
    """Validate a bounded GitHub tag-object chain ending at ``expected_sha``."""
    if not isinstance(chain, list) or not chain or len(chain) > 8:
        raise ReleasePolicyError("invalid tag chain length")
    if _SHA_PATTERN.fullmatch(expected_sha) is None:
        raise ReleasePolicyError("invalid expected commit")

    seen: set[str] = set()
    for index, item in enumerate(chain):
        if not isinstance(item, dict) or set(item) != {"type", "sha"}:
            raise ReleasePolicyError("invalid or repeated tag object")
        object_type, sha = item.get("type"), item.get("sha")
        if (
            not isinstance(object_type, str)
            or not isinstance(sha, str)
            or _SHA_PATTERN.fullmatch(sha) is None
            or sha in seen
        ):
            raise ReleasePolicyError("invalid or repeated tag object")
        seen.add(sha)
        if index < len(chain) - 1 and object_type != "tag":
            raise ReleasePolicyError("tag chain ended before the final object")
    if chain[-1] != {"type": "commit", "sha": expected_sha}:
        raise ReleasePolicyError("tag does not resolve to the expected commit")


def validate_stable_latest(before: dict, after: dict, dev_version: str) -> None:
    """Ensure a dev operation did not make `/releases/latest` unsafe or regress."""
    versions: list[tuple[int, int, int]] = []
    for document in (before, after):
        if not isinstance(document, dict):
            raise ReleasePolicyError("latest endpoint returned an invalid release")
        if document.get("draft") is not False or document.get("prerelease") is not False:
            raise ReleasePolicyError("latest endpoint returned a non-stable release")
        tag_name = document.get("tag_name")
        if not isinstance(tag_name, str) or tag_name == dev_version:
            raise ReleasePolicyError("dev release became stable latest")
        try:
            versions.append(_parse_stable_version(tag_name))
        except ValueError as error:
            raise ReleasePolicyError("latest endpoint returned an invalid stable version") from error

    if versions[1] < versions[0]:
        raise ReleasePolicyError("stable latest moved backwards")


def _parse_stable_version(value: str) -> tuple[int, int, int]:
    """Parse a stable version for the latest-release monotonicity check."""
    match = _STABLE_VERSION_PATTERN.fullmatch(value)
    if match is None:
        raise ValueError("invalid stable version")
    return tuple(int(component) for component in match.groups())


def _read_json(path_value: str) -> object:
    path = Path(path_value)
    try:
        with path.open("rb") as source:
            data = source.read(_MAX_JSON_BYTES + 1)
        if len(data) > _MAX_JSON_BYTES:
            raise ReleasePolicyError("JSON input exceeds the allowed size")
        return json.loads(data.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ReleasePolicyError("invalid JSON input") from error


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("assets", help="print the fixed expected asset names")

    release = commands.add_parser("release", help="validate a Release response")
    release.add_argument("--document", required=True)
    release.add_argument("--version", required=True)
    release.add_argument("--release-name", required=True)
    release.add_argument("--marker", required=True)
    release.add_argument("--mode", required=True)

    tag_chain = commands.add_parser("tag-chain", help="validate a peeled tag chain")
    tag_chain.add_argument("--chain", required=True)
    tag_chain.add_argument("--expected-sha", required=True)

    latest = commands.add_parser("latest", help="validate stable latest before and after")
    latest.add_argument("--before", required=True)
    latest.add_argument("--after", required=True)
    latest.add_argument("--dev-version", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    """Run the policy CLI and return a process status without exposing inputs."""
    arguments = _parser().parse_args(argv)
    try:
        if arguments.command == "assets":
            print("\n".join(sorted(EXPECTED_DEV_ASSETS)))
        elif arguments.command == "release":
            validate_release_document(
                _read_json(arguments.document),
                arguments.version,
                arguments.release_name,
                arguments.marker,
                arguments.mode,
            )
        elif arguments.command == "tag-chain":
            validate_tag_chain(_read_json(arguments.chain), arguments.expected_sha)
        elif arguments.command == "latest":
            validate_stable_latest(
                _read_json(arguments.before),
                _read_json(arguments.after),
                arguments.dev_version,
            )
    except ReleasePolicyError as error:
        print(f"release policy validation failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
