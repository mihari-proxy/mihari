"""Tests for GitHub release identity policy using sanitized API response shapes."""

import json
from pathlib import Path
import subprocess
import sys

import pytest

from github_release_policy import (
    EXPECTED_DEV_ASSETS,
    ReleasePolicyError,
    validate_release_document,
    validate_stable_latest,
    validate_tag_chain,
)


VERSION = "v0.9.0-dev.1"
RELEASE_NAME = "Mihari v0.9.0-dev.1 (dev)"
MARKER = "<!-- mihari-dev-release -->"
SHA = "a" * 40
SCRIPT = Path(__file__).with_name("github_release_policy.py")


def release_fixture(asset_names):
    return {
        "tag_name": VERSION,
        "name": RELEASE_NAME,
        "body": f"Development build\n{MARKER}",
        "draft": False,
        "prerelease": True,
        "target_commitish": "dev",
        "assets": [{"name": name} for name in asset_names],
    }


def stable_fixture(tag_name):
    return {"tag_name": tag_name, "draft": False, "prerelease": False}


def run_policy(*arguments):
    return subprocess.run(
        [sys.executable, str(SCRIPT), *arguments],
        capture_output=True,
        check=False,
        text=True,
    )


def test_expected_dev_assets_are_the_fixed_fourteen_release_files():
    assert EXPECTED_DEV_ASSETS == frozenset(
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


def test_final_release_accepts_real_response_without_make_latest():
    validate_release_document(
        release_fixture(sorted(EXPECTED_DEV_ASSETS)),
        VERSION,
        RELEASE_NAME,
        MARKER,
        "final",
    )


def test_preflight_accepts_matching_subset_but_rejects_extra_assets():
    subset = sorted(EXPECTED_DEV_ASSETS)[:3]
    validate_release_document(release_fixture(subset), VERSION, RELEASE_NAME, MARKER, "preflight")
    with pytest.raises(ReleasePolicyError):
        validate_release_document(
            release_fixture(subset + ["unexpected.bin"]),
            VERSION,
            RELEASE_NAME,
            MARKER,
            "preflight",
        )


def test_preflight_accepts_an_explicit_empty_asset_list():
    validate_release_document(release_fixture([]), VERSION, RELEASE_NAME, MARKER, "preflight")


def test_preflight_rejects_a_missing_assets_field():
    document = release_fixture([])
    document.pop("assets")

    with pytest.raises(ReleasePolicyError):
        validate_release_document(document, VERSION, RELEASE_NAME, MARKER, "preflight")


@pytest.mark.parametrize("assets", [None, {}, "not-a-list"])
def test_preflight_rejects_a_non_list_assets_field(assets):
    document = release_fixture([])
    document["assets"] = assets

    with pytest.raises(ReleasePolicyError):
        validate_release_document(document, VERSION, RELEASE_NAME, MARKER, "preflight")


@pytest.mark.parametrize("target_commitish", ["main", "dev", SHA, None])
def test_release_document_treats_target_commitish_as_diagnostic(target_commitish):
    document = release_fixture(sorted(EXPECTED_DEV_ASSETS))
    if target_commitish is None:
        document.pop("target_commitish")
    else:
        document["target_commitish"] = target_commitish

    validate_release_document(document, VERSION, RELEASE_NAME, MARKER, "final")


@pytest.mark.parametrize(
    "document,version,release_name,marker,mode",
    [
        (release_fixture(["mihari-linux-amd64", "mihari-linux-amd64"]), VERSION, RELEASE_NAME, MARKER, "preflight"),
        ({**release_fixture([]), "prerelease": False}, VERSION, RELEASE_NAME, MARKER, "preflight"),
        ({**release_fixture([]), "draft": True}, VERSION, RELEASE_NAME, MARKER, "preflight"),
        (release_fixture([]), VERSION, RELEASE_NAME, "<!-- wrong marker -->", "preflight"),
        (release_fixture([]), "v0.9.0-dev.2", RELEASE_NAME, MARKER, "preflight"),
        (release_fixture(sorted(EXPECTED_DEV_ASSETS)[:-1]), VERSION, RELEASE_NAME, MARKER, "final"),
        (release_fixture([]), VERSION, RELEASE_NAME, MARKER, "unsupported"),
    ],
)
def test_release_document_rejects_invalid_identity_or_assets(document, version, release_name, marker, mode):
    with pytest.raises(ReleasePolicyError):
        validate_release_document(document, version, release_name, marker, mode)


def test_tag_chain_accepts_lightweight_and_annotated_tags():
    validate_tag_chain([{"type": "commit", "sha": SHA}], SHA)
    validate_tag_chain(
        [
            {"type": "tag", "sha": "b" * 40},
            {"type": "tag", "sha": "c" * 40},
            {"type": "commit", "sha": SHA},
        ],
        SHA,
    )


@pytest.mark.parametrize(
    "chain",
    [
        [{"type": "commit", "sha": "d" * 40}],
        [{"type": "tree", "sha": SHA}],
        [{"type": "commit", "sha": "b" * 40}, {"type": "commit", "sha": SHA}],
        [{"type": "tag", "sha": SHA}, {"type": "commit", "sha": SHA}],
        [
            {"type": "tag", "sha": "b" * 40, "unexpected": "field"},
            {"type": "commit", "sha": SHA},
        ],
        [{"type": "tag", "sha": 1}, {"type": "commit", "sha": SHA}],
    ],
)
def test_tag_chain_rejects_wrong_commit_noncommit_endpoint_and_repeated_sha(chain):
    with pytest.raises(ReleasePolicyError):
        validate_tag_chain(chain, SHA)


def test_stable_latest_rejects_dev_prerelease_and_regression():
    with pytest.raises(ReleasePolicyError):
        validate_stable_latest(stable_fixture("v0.9.0"), stable_fixture(VERSION), VERSION)
    with pytest.raises(ReleasePolicyError):
        validate_stable_latest(
            stable_fixture("v0.9.0"),
            {"tag_name": "v0.9.1", "draft": False, "prerelease": True},
            VERSION,
        )
    with pytest.raises(ReleasePolicyError):
        validate_stable_latest(stable_fixture("v0.9.0"), stable_fixture("v0.8.9"), VERSION)


def test_stable_latest_accepts_a_stable_version_advance():
    validate_stable_latest(stable_fixture("v0.9.0"), stable_fixture("v0.10.0"), VERSION)


@pytest.mark.parametrize("tag_name", ["v01.2.3", "v1.02.3", "v1.2.03"])
def test_stable_latest_rejects_a_leading_zero_in_each_version_segment(tag_name):
    with pytest.raises(ReleasePolicyError):
        validate_stable_latest(stable_fixture("v0.9.0"), stable_fixture(tag_name), VERSION)


def test_policy_cli_latest_is_self_contained_without_adjacent_release_policy(tmp_path):
    isolated_script = tmp_path / "github_release_policy.py"
    before_path = tmp_path / "before.json"
    after_path = tmp_path / "after.json"
    isolated_script.write_text(SCRIPT.read_text(encoding="utf-8"), encoding="utf-8")
    before_path.write_text(json.dumps(stable_fixture("v0.9.0")), encoding="utf-8")
    after_path.write_text(json.dumps(stable_fixture("v0.9.1")), encoding="utf-8")

    result = subprocess.run(
        [
            sys.executable,
            str(isolated_script),
            "latest",
            "--before",
            str(before_path),
            "--after",
            str(after_path),
            "--dev-version",
            VERSION,
        ],
        capture_output=True,
        check=False,
        text=True,
    )

    assert result.returncode == 0, result.stderr


def test_policy_cli_rejects_non_string_intermediate_tag_sha_without_traceback(tmp_path):
    chain_path = tmp_path / "invalid-chain.json"
    chain_path.write_text(
        json.dumps([{"type": "tag", "sha": 1}, {"type": "commit", "sha": SHA}]),
        encoding="utf-8",
    )

    rejected = run_policy("tag-chain", "--chain", str(chain_path), "--expected-sha", SHA)

    assert rejected.returncode == 1
    assert "Traceback" not in rejected.stderr
    assert '"sha": 1' not in rejected.stderr


def test_policy_cli_reports_malformed_json_as_invalid_input(tmp_path):
    document_path = tmp_path / "malformed.json"
    document_path.write_text('{"body":', encoding="utf-8")

    rejected = run_policy(
        "release",
        "--document",
        str(document_path),
        "--version",
        VERSION,
        "--release-name",
        RELEASE_NAME,
        "--marker",
        MARKER,
        "--mode",
        "preflight",
    )

    assert rejected.returncode == 1
    assert rejected.stderr == "release policy validation failed: invalid JSON input\n"


def test_policy_cli_rejects_deep_json_without_traceback_or_input_echo(tmp_path):
    document_path = tmp_path / "deep.json"
    sensitive_value = "sensitive-deep-json"
    document_path.write_text(
        "[" * 4_000 + json.dumps(sensitive_value) + "]" * 4_000,
        encoding="utf-8",
    )

    rejected = run_policy(
        "release",
        "--document",
        str(document_path),
        "--version",
        VERSION,
        "--release-name",
        RELEASE_NAME,
        "--marker",
        MARKER,
        "--mode",
        "preflight",
    )

    assert rejected.returncode == 1
    assert rejected.stderr.startswith("release policy validation failed: ")
    assert "Traceback" not in rejected.stderr
    assert sensitive_value not in rejected.stderr


def test_policy_cli_validates_local_json_and_never_echoes_document_contents(tmp_path):
    release_path = tmp_path / "release.json"
    chain_path = tmp_path / "chain.json"
    before_path = tmp_path / "before.json"
    after_path = tmp_path / "after.json"
    release_path.write_text(json.dumps(release_fixture(sorted(EXPECTED_DEV_ASSETS))), encoding="utf-8")
    chain_path.write_text(json.dumps([{"type": "commit", "sha": SHA}]), encoding="utf-8")
    before_path.write_text(json.dumps(stable_fixture("v0.9.0")), encoding="utf-8")
    after_path.write_text(json.dumps(stable_fixture("v0.9.1")), encoding="utf-8")

    assets = run_policy("assets")
    assert assets.returncode == 0
    assert assets.stdout.splitlines() == sorted(EXPECTED_DEV_ASSETS)
    assert run_policy("release", "--document", str(release_path), "--version", VERSION, "--release-name", RELEASE_NAME, "--marker", MARKER, "--mode", "final").returncode == 0
    assert run_policy("tag-chain", "--chain", str(chain_path), "--expected-sha", SHA).returncode == 0
    assert run_policy("latest", "--before", str(before_path), "--after", str(after_path), "--dev-version", VERSION).returncode == 0

    release_path.write_text(json.dumps({"body": "sensitive-release-body"}), encoding="utf-8")
    rejected = run_policy("release", "--document", str(release_path), "--version", VERSION, "--release-name", RELEASE_NAME, "--marker", MARKER, "--mode", "final")
    assert rejected.returncode == 1
    assert "sensitive-release-body" not in rejected.stderr
