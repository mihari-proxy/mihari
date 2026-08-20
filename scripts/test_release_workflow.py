"""Static regression checks for GitHub dev-release recovery wiring."""

import os
from pathlib import Path
import subprocess

import yaml


WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "release-dev.yml"


def test_release_workflow_uses_policy_instead_of_write_only_get_fields():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert ".make_latest" not in workflow
    assert ".target_commitish == $sha" not in workflow
    assert "github_release_policy.py release" in workflow
    assert "github_release_policy.py tag-chain" in workflow
    assert "github_release_policy.py latest" in workflow
    assert "-F make_latest=false" in workflow


def test_release_recovery_preflights_existing_assets_and_only_uploads_missing():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert "github_release_policy.py assets" in workflow
    assert "--mode preflight" in workflow
    assert "--mode final" in workflow
    assert workflow.index("--mode preflight") < workflow.index("--mode final")
    assert "download_and_verify_assets" in workflow
    assert "missing-assets" in workflow
    assert "gh release upload \"${VERSION}\" \"dist/${asset}\"" in workflow
    assert "--clobber" not in workflow


def test_release_workflow_checks_stable_latest_before_and_after_mutation():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert "/releases/latest" in workflow
    assert "/tmp/latest-before.json" in workflow
    assert "/tmp/latest-after.json" in workflow
    assert workflow.index("/tmp/latest-before.json") < workflow.index("--mode preflight")
    assert workflow.index("--mode final") < workflow.index("/tmp/latest-after.json")


def test_release_workflow_is_github_only_and_limits_tag_peeling():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert "ALIST_" not in workflow
    assert "mihari-dev" not in workflow
    assert "for depth in $(seq 1 7)" in workflow
    assert "jq -c '{type: .object.type, sha: .object.sha}'" in workflow
    assert "jq -s . /tmp/tag-chain.jsonl" in workflow


def test_dev_version_validation_never_logs_raw_dispatch_input():
    workflow = WORKFLOW.read_text(encoding="utf-8")
    guard_start = workflow.index("Guard dev ref and version")
    guard_end = workflow.index("Resolve immutable source commit")
    guard = workflow[guard_start:guard_end]

    assert 'echo "${VERSION}"' not in guard
    assert "version '${VERSION}'" not in guard
    assert "::error::version must match the dev prerelease format" in guard


def _run_dev_version_guard(version):
    document = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    guard = next(
        step["run"]
        for step in document["jobs"]["resolve"]["steps"]
        if step.get("name") == "Guard dev ref and version"
    )
    return subprocess.run(
        [_bash_for_workflow_guard(), "-c", guard],
        capture_output=True,
        check=False,
        env={**os.environ, "GITHUB_REF": "refs/heads/dev", "VERSION": version},
        text=True,
    )


def _bash_for_workflow_guard():
    windows_git_bash = Path(r"C:\Program Files\Git\bin\bash.exe")
    if os.name == "nt" and windows_git_bash.is_file():
        return str(windows_git_bash)
    return "bash"


def test_dev_version_guard_rejects_multiline_input_without_echoing_it():
    for valid_version in ("v0.3.0-dev.1", "v12.34.56-dev.789"):
        accepted = _run_dev_version_guard(valid_version)
        assert accepted.returncode == 0, accepted.stderr

    payload = "v0.3.0-dev.1\n::warning::injected-workflow-command"
    rejected = _run_dev_version_guard(payload)

    assert rejected.returncode != 0
    assert "::warning::" not in rejected.stdout
    assert payload not in rejected.stdout
    assert payload not in rejected.stderr


def test_dev_release_identity_is_explicit_and_verified_on_reuse():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert "DEV_RELEASE_NAME: Mihari ${{ inputs.version }} (dev)" in workflow
    assert "<!-- github-release-dev -->" in workflow
    assert '-f "name=${DEV_RELEASE_NAME}"' in workflow
    assert '-f "body=${DEV_RELEASE_BODY}"' in workflow


def test_existing_release_is_preflighted_before_tag_mutation():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    preflight = workflow.index("Preflight existing release before tag mutation")
    tag_mutation = workflow.index("Create or verify immutable tag")
    assert preflight < tag_mutation
    assert "--mode preflight" in workflow[preflight:tag_mutation]
