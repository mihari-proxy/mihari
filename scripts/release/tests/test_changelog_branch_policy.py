"""PRs into dev must not edit CHANGELOG.md except official release-prep or main sync."""

from pathlib import Path
import subprocess
import sys

import pytest

from changelog_branch_policy import ChangelogBranchPolicyError, validate_dev_changelog_change


SCRIPT = Path(__file__).parent.parent / "changelog_branch_policy.py"
SECRET_REF = "feat/secret-changelog-ref"


def run_policy(*arguments):
    return subprocess.run(
        [sys.executable, str(SCRIPT), *arguments],
        capture_output=True,
        check=False,
        text=True,
    )


def test_allows_prs_that_do_not_touch_changelog():
    validate_dev_changelog_change(
        head_ref="feat/channel",
        changelog_changed=False,
        head_matches_main=False,
    )


def test_allows_main_to_dev_sync_even_when_changelog_changed():
    validate_dev_changelog_change(
        head_ref="main",
        changelog_changed=True,
        head_matches_main=True,
    )


def test_allows_official_release_prep_branch():
    validate_dev_changelog_change(
        head_ref="chore/release-v0.9.0",
        changelog_changed=True,
        head_matches_main=False,
    )


def test_allows_restoring_changelog_to_match_main():
    validate_dev_changelog_change(
        head_ref="fix/restore-changelog",
        changelog_changed=True,
        head_matches_main=True,
    )


def test_rejects_feature_pr_changelog_edits_that_do_not_match_main():
    with pytest.raises(ChangelogBranchPolicyError, match="CHANGELOG"):
        validate_dev_changelog_change(
            head_ref="feat/channel",
            changelog_changed=True,
            head_matches_main=False,
        )


def test_cli_rejects_feature_changelog_edit_without_echoing_the_ref():
    result = run_policy(
        "--head-ref",
        SECRET_REF,
        "--changelog-changed",
        "true",
        "--head-matches-main",
        "false",
    )

    assert result.returncode == 1
    assert "Traceback" not in result.stderr
    assert SECRET_REF not in result.stderr
    assert SECRET_REF not in result.stdout
    assert result.stderr.startswith("changelog branch policy validation failed:")


def test_cli_accepts_unchanged_changelog():
    result = run_policy(
        "--head-ref",
        SECRET_REF,
        "--changelog-changed",
        "false",
        "--head-matches-main",
        "false",
    )

    assert result.returncode == 0, result.stderr
    assert result.stdout == ""
    assert result.stderr == ""
