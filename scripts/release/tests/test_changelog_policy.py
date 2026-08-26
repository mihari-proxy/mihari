"""Stable changelog gate: official releases must close Unreleased into the version section."""

from pathlib import Path
import subprocess
import sys

import pytest

from changelog_policy import ChangelogPolicyError, validate_stable_changelog


SCRIPT = Path(__file__).parent.parent / "changelog_policy.py"
VERSION = "v0.9.0"
SECRET = "secret-changelog-body"


def valid_changelog(version=VERSION, *, unreleased="", body="- fix a user-visible bug."):
    return (
        "# Changelog\n"
        "\n"
        "## [Unreleased]\n"
        "\n"
        "### Added\n"
        "\n"
        f"{unreleased}"
        "### Fixed\n"
        "\n"
        f"## [{version}] - 2026-08-26\n"
        "\n"
        "### Fixed\n"
        "\n"
        f"{body}\n"
        "\n"
        "## [v0.8.2] - 2026-08-18\n"
        "\n"
        "### Fixed\n"
        "\n"
        "- older stable fix.\n"
    )


def run_policy(*arguments):
    return subprocess.run(
        [sys.executable, str(SCRIPT), *arguments],
        capture_output=True,
        check=False,
        text=True,
    )


def test_accepts_closed_unreleased_and_matching_version_section():
    validate_stable_changelog(valid_changelog(), VERSION)


def test_accepts_crlf_and_blank_unreleased_categories():
    validate_stable_changelog(valid_changelog().replace("\n", "\r\n"), VERSION)


def test_rejects_dev_version_argument():
    with pytest.raises(ChangelogPolicyError, match="stable format"):
        validate_stable_changelog(valid_changelog(), "v0.9.0-dev.1")


def test_rejects_missing_unreleased_section():
    text = valid_changelog().replace("## [Unreleased]\n\n### Added\n\n### Fixed\n\n", "")
    with pytest.raises(ChangelogPolicyError, match="Unreleased"):
        validate_stable_changelog(text, VERSION)


def test_rejects_unreleased_leftover_bullet():
    with pytest.raises(ChangelogPolicyError, match="Unreleased"):
        validate_stable_changelog(
            valid_changelog(unreleased="- leftover channel note.\n\n"),
            VERSION,
        )


def test_rejects_missing_required_version_section():
    with pytest.raises(ChangelogPolicyError, match="version section"):
        validate_stable_changelog(valid_changelog(version="v0.9.1"), VERSION)


def test_rejects_version_section_that_is_not_first_after_unreleased():
    text = (
        "# Changelog\n"
        "\n"
        "## [Unreleased]\n"
        "\n"
        "## [v0.8.2] - 2026-08-18\n"
        "\n"
        "- older fix.\n"
        "\n"
        "## [v0.9.0] - 2026-08-26\n"
        "\n"
        "- too late.\n"
    )
    with pytest.raises(ChangelogPolicyError, match="first version"):
        validate_stable_changelog(text, VERSION)


def test_rejects_version_section_without_entries():
    with pytest.raises(ChangelogPolicyError, match="no entries"):
        validate_stable_changelog(valid_changelog(body=""), VERSION)


def test_rejects_duplicate_version_heading():
    text = valid_changelog() + "\n## [v0.9.0] - 2026-08-27\n\n- duplicate.\n"
    with pytest.raises(ChangelogPolicyError, match="duplicate"):
        validate_stable_changelog(text, VERSION)


def test_rejects_version_heading_without_date():
    text = valid_changelog().replace("## [v0.9.0] - 2026-08-26", "## [v0.9.0]")
    with pytest.raises(ChangelogPolicyError, match="heading"):
        validate_stable_changelog(text, VERSION)


def test_rejects_invalid_version_date():
    text = valid_changelog().replace("2026-08-26", "2026-13-40")
    with pytest.raises(ChangelogPolicyError, match="date"):
        validate_stable_changelog(text, VERSION)


def test_cli_accepts_a_closed_changelog(tmp_path):
    path = tmp_path / "CHANGELOG.md"
    path.write_text(valid_changelog(), encoding="utf-8")

    result = run_policy("--changelog", str(path), "--version", VERSION)

    assert result.returncode == 0, result.stderr
    assert result.stdout == ""
    assert result.stderr == ""


def test_cli_rejects_leftover_unreleased_without_echoing_inputs(tmp_path):
    path = tmp_path / "CHANGELOG.md"
    path.write_text(
        valid_changelog(unreleased=f"- {SECRET}\n\n"),
        encoding="utf-8",
    )

    result = run_policy("--changelog", str(path), "--version", VERSION)

    assert result.returncode == 1
    assert "Traceback" not in result.stderr
    assert SECRET not in result.stderr
    assert SECRET not in result.stdout
    assert VERSION not in result.stderr
    assert result.stderr.startswith("changelog policy validation failed:")


def test_cli_rejects_oversized_changelog_without_echoing_it(tmp_path):
    path = tmp_path / "CHANGELOG.md"
    path.write_bytes(b"# Changelog\n" + b"x" * (1_048_576))

    result = run_policy("--changelog", str(path), "--version", VERSION)

    assert result.returncode == 1
    assert "Traceback" not in result.stderr
    assert "xxxx" not in result.stderr
    assert VERSION not in result.stderr
