"""Validate that PRs into dev do not edit CHANGELOG.md outside official release-prep."""

import argparse
import sys


class ChangelogBranchPolicyError(ValueError):
    """Raised when a PR into dev is not allowed to change CHANGELOG.md."""


def validate_dev_changelog_change(
    *,
    head_ref: str,
    changelog_changed: bool,
    head_matches_main: bool,
) -> None:
    """Allow unchanged files, main sync, official release-prep, or restore-to-main."""
    if not isinstance(head_ref, str) or not isinstance(changelog_changed, bool) or not isinstance(
        head_matches_main, bool
    ):
        raise ChangelogBranchPolicyError("invalid changelog branch input")
    if "\n" in head_ref or "\r" in head_ref:
        raise ChangelogBranchPolicyError("invalid changelog branch input")
    if not changelog_changed:
        return
    if head_ref == "main" or head_ref.startswith("chore/release-") or head_matches_main:
        return
    raise ChangelogBranchPolicyError("feature PRs targeting dev must not modify CHANGELOG.md")


def _parse_bool(value: str) -> bool:
    if value == "true":
        return True
    if value == "false":
        return False
    raise ChangelogBranchPolicyError("invalid changelog branch input")


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--head-ref", required=True)
    parser.add_argument("--changelog-changed", required=True)
    parser.add_argument("--head-matches-main", required=True)
    return parser


def main(argv: list[str] | None = None) -> int:
    """Run the branch changelog gate without echoing untrusted inputs."""
    arguments = _parser().parse_args(argv)
    try:
        validate_dev_changelog_change(
            head_ref=arguments.head_ref,
            changelog_changed=_parse_bool(arguments.changelog_changed),
            head_matches_main=_parse_bool(arguments.head_matches_main),
        )
    except ChangelogBranchPolicyError as error:
        print(f"changelog branch policy validation failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
