"""Static regression checks for GitHub dev-release recovery wiring."""

import os
from pathlib import Path
import subprocess

import yaml


WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "release-dev.yml"
STABLE_WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "release.yml"
STABLE_RETRACT_WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "retract.yml"
CI_WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "ci.yml"
AGENTS = Path(__file__).resolve().parents[1] / "AGENTS.md"
CONTRIBUTING = Path(__file__).resolve().parents[1] / ".github" / "CONTRIBUTING.md"
RELEASE_DOCUMENT = Path(__file__).resolve().parents[1] / "docs" / "RELEASE.md"
DISTRIBUTION_DOCUMENT = Path(__file__).resolve().parents[1] / "docs" / "distribution.md"

RELEASE_SAFETY_TESTS = (
    "scripts/test_release_policy.py "
    "scripts/test_github_release_policy.py "
    "scripts/test_release_workflow.py "
    "scripts/test_alist_client.py "
    "scripts/test_alist_index.py "
    "scripts/test_release_alist.py "
    "scripts/test_retract_alist.py "
    "scripts/test_regenerate_index.py -q"
)


def test_stable_release_and_retract_pin_the_stable_alist_channel():
    stable_release_workflow = STABLE_WORKFLOW.read_text(encoding="utf-8")
    stable_retract_workflow = STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8")

    assert "--channel stable" in stable_release_workflow
    assert '--commit-sha "${SHA}"' in stable_release_workflow
    assert "--channel stable" in stable_retract_workflow

    for workflow in (stable_release_workflow, stable_retract_workflow):
        assert "ALIST_BASE_PATH" not in workflow
        assert "--base-path /mihari-release/mihari" in workflow


def test_stable_alist_mutations_share_a_job_level_serialization_group():
    release = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    retract = yaml.safe_load(STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8"))

    expected = {"group": "mihari-stable-alist", "cancel-in-progress": False}
    assert release["jobs"]["release"].get("concurrency") == expected
    assert retract["jobs"]["retract"].get("concurrency") == expected


def test_stable_release_resolves_and_uses_the_approved_source_sha():
    workflow = STABLE_WORKFLOW.read_text(encoding="utf-8")
    document = yaml.safe_load(workflow)
    resolve_steps = document["jobs"]["resolve"]["steps"]
    checkout = next(step for step in resolve_steps if step.get("uses") == "actions/checkout@v7")
    source = next(step["run"] for step in resolve_steps if step.get("name") == "Resolve immutable source commit")

    assert 'git rev-parse "${GITHUB_REF}^{}"' in workflow
    assert "should_release=false" in workflow
    assert checkout["with"]["ref"] == "${{ github.event_name == 'workflow_dispatch' && 'main' || github.ref }}"
    assert "^[0-9a-f]{40}$" in source
    assert '[ "${SHA}" = "${INPUT_COMMIT_SHA}" ]' in source
    assert '[ "$(git rev-parse HEAD)" = "${SHA}" ]' in source

    for job_name in ("build", "bundle", "release"):
        job = document["jobs"][job_name]
        assert "resolve" in job["needs"]
        expected_condition = "needs.resolve.outputs.should_release == 'true'"
        if job_name == "release":
            expected_condition += (
                " && (github.event_name == 'push' || "
                "(github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main'))"
            )
        assert job["if"] == expected_condition
        job_checkout = next(step for step in job["steps"] if step.get("uses") == "actions/checkout@v7")
        assert job_checkout["with"]["ref"] == "${{ needs.resolve.outputs.sha }}"


def test_stable_release_secrets_job_allows_dispatch_only_from_main():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    release = document["jobs"]["release"]

    assert release["needs"] == ["resolve", "build", "bundle"]
    assert release["if"] == (
        "needs.resolve.outputs.should_release == 'true' && "
        "(github.event_name == 'push' || "
        "(github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main'))"
    )


def test_stable_release_validates_dispatch_identity_without_interpolating_or_logging_it():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    steps = document["jobs"]["resolve"]["steps"]
    guard = next(step["run"] for step in steps if step.get("name") == "Guard stable version")
    source = next(step for step in steps if step.get("name") == "Resolve immutable source commit")

    assert 'echo "${VERSION}"' not in guard
    assert "version '${VERSION}'" not in guard
    assert source["env"]["INPUT_COMMIT_SHA"] == "${{ inputs.commit_sha }}"
    assert "${{ inputs.commit_sha }}" not in source["run"]
    assert "INPUT_COMMIT_SHA" in source["run"]


def test_stable_tag_release_requires_a_commit_reachable_from_trusted_main_before_release():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    resolve = document["jobs"]["resolve"]
    source = next(step["run"] for step in resolve["steps"] if step.get("name") == "Resolve immutable source commit")

    assert resolve["outputs"]["should_release"] == "${{ steps.source.outputs.should_release || steps.gate.outputs.should_release }}"
    assert "should_resolve=false" in next(
        step["run"] for step in resolve["steps"] if step.get("name") == "Guard stable version"
    )
    assert "git fetch --no-tags origin +refs/heads/main:refs/remotes/origin/main" in source
    assert 'git merge-base --is-ancestor "${SHA}" origin/main' in source
    assert "::error::stable tag must reference a commit reachable from main" in source
    assert source.index('git rev-parse "${GITHUB_REF}^{}"') < source.index('git merge-base --is-ancestor "${SHA}" origin/main')
    assert source.index('git merge-base --is-ancestor "${SHA}" origin/main') < source.index('should_release=true')


def _git(repo, *args):
    return subprocess.run(
        ["git", *args],
        capture_output=True,
        check=True,
        cwd=repo,
        text=True,
    ).stdout.strip()


def _stable_tag_repository(tmp_path, tag_name, tag_on_main):
    remote = tmp_path / "origin.git"
    repository = tmp_path / "repository"
    _git(tmp_path, "init", "--bare", str(remote))
    _git(tmp_path, "init", str(repository))
    _git(repository, "config", "user.name", "Workflow Test")
    _git(repository, "config", "user.email", "workflow-test@example.invalid")
    _git(repository, "checkout", "-b", "main")
    (repository / "release.txt").write_text("base\n", encoding="utf-8")
    _git(repository, "add", "release.txt")
    _git(repository, "commit", "-m", "base")
    base = _git(repository, "rev-parse", "HEAD")
    _git(repository, "remote", "add", "origin", str(remote))
    _git(repository, "push", "-u", "origin", "main")

    if tag_on_main:
        _git(repository, "tag", tag_name, base)
        (repository / "release.txt").write_text("main tip\n", encoding="utf-8")
        _git(repository, "commit", "-am", "advance main")
        _git(repository, "push", "origin", "main")
    else:
        _git(repository, "checkout", "-b", "release-candidate", base)
        (repository / "release.txt").write_text("off main\n", encoding="utf-8")
        _git(repository, "commit", "-am", "off-main release candidate")
        _git(repository, "tag", tag_name)

    _git(repository, "checkout", tag_name)
    return repository, _git(repository, "rev-parse", "HEAD")


def _run_stable_tag_source(repository, tag_name, output_path):
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    source = next(
        step["run"]
        for step in document["jobs"]["resolve"]["steps"]
        if step.get("name") == "Resolve immutable source commit"
    )
    return subprocess.run(
        [_bash_for_workflow_guard(), "-c", source],
        capture_output=True,
        check=False,
        cwd=repository,
        env={
            **os.environ,
            "GITHUB_EVENT_NAME": "push",
            "GITHUB_OUTPUT": str(output_path),
            "GITHUB_REF": f"refs/tags/{tag_name}",
        },
        text=True,
    )


def test_stable_tag_source_accepts_a_historical_main_ancestor(tmp_path):
    repository, sha = _stable_tag_repository(tmp_path, "v1.2.3", tag_on_main=True)
    output = tmp_path / "github-output"

    result = _run_stable_tag_source(repository, "v1.2.3", output)

    assert result.returncode == 0, result.stderr
    assert output.read_text(encoding="utf-8") == f"sha={sha}\nshould_release=true\n"


def test_stable_tag_source_rejects_an_off_main_commit(tmp_path):
    repository, _ = _stable_tag_repository(tmp_path, "v1.2.4", tag_on_main=False)
    output = tmp_path / "github-output"

    result = _run_stable_tag_source(repository, "v1.2.4", output)

    assert result.returncode != 0
    assert "::error::stable tag must reference a commit reachable from main" in result.stdout


def test_alist_runtime_dependencies_are_pinned_after_checkout_in_both_stable_workflows():
    release = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    retract = yaml.safe_load(STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8"))

    for workflow, job_name, install_step_name in (
        (release, "release", "Publish to AList drive"),
        (retract, "retract", "Retract from AList drive"),
    ):
        steps = workflow["jobs"][job_name]["steps"]
        checkout_index = next(index for index, step in enumerate(steps) if step.get("uses") == "actions/checkout@v7")
        setup_index = next(index for index, step in enumerate(steps) if step.get("uses") == "actions/setup-python@v7")
        install_index = next(index for index, step in enumerate(steps) if step.get("name") == install_step_name)
        install = steps[install_index]["run"]

        assert checkout_index < setup_index < install_index
        assert steps[setup_index]["with"] == {"python-version": "3.12"}
        assert "python -m pip install --disable-pip-version-check -r scripts/requirements-release.txt" in install
        assert "pip install requests" not in install

    release_checkout = next(
        step for step in release["jobs"]["release"]["steps"] if step.get("uses") == "actions/checkout@v7"
    )
    assert release_checkout["with"]["ref"] == "${{ needs.resolve.outputs.sha }}"


def test_stable_alist_backup_artifacts_require_mutation_failure_or_cancellation():
    release = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    retract = yaml.safe_load(STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8"))

    for workflow, job_name, mutation_name in (
        (release, "release", "Publish to AList drive"),
        (retract, "retract", "Retract from AList drive"),
    ):
        steps = workflow["jobs"][job_name]["steps"]
        mutation_index = next(index for index, step in enumerate(steps) if step.get("name") == mutation_name)
        mutation = steps[mutation_index]
        backup_index = next(
            index
            for index, step in enumerate(steps)
            if step.get("name") == "Upload stable index recovery backup"
        )
        backup = steps[backup_index]

        assert mutation["id"] == "alist_mutation"
        assert mutation_index < backup_index
        assert backup["if"] == (
            "env.ALIST_CONFIGURED == 'true' && "
            "((failure() && steps.alist_mutation.outcome == 'failure') || "
            "(cancelled() && steps.alist_mutation.outcome == 'cancelled'))"
        )
        # A later job cancellation after a successful mutation must not match.
        assert "|| cancelled())" not in backup["if"]
        assert backup["uses"] == "actions/upload-artifact@v7"
        assert backup["with"]["path"] == "${{ runner.temp }}/mihari-index-backup/stable/**"
        assert backup["with"]["if-no-files-found"] == "ignore"
        assert backup["with"]["retention-days"] == 3


def test_stable_alist_secrets_are_scoped_only_to_the_mutation_step():
    release = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    retract = yaml.safe_load(STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8"))
    expected_secret_env = {
        "ALIST_URL": "${{ secrets.ALIST_URL }}",
        "ALIST_USERNAME": "${{ secrets.ALIST_USERNAME }}",
        "ALIST_PASSWORD": "${{ secrets.ALIST_PASSWORD }}",
    }

    for workflow, job_name in ((release, "release"), (retract, "retract")):
        job = workflow["jobs"][job_name]
        expected_job_env = {"ALIST_CONFIGURED", "SHA"}
        if job_name == "release":
            expected_job_env.add("MIHARI_KEEP_VERSIONS")
        assert set(job["env"]) == expected_job_env
        assert job["env"]["ALIST_CONFIGURED"] == "${{ secrets.ALIST_URL != '' }}"
        assert "ALIST_USERNAME" not in job["env"]["ALIST_CONFIGURED"]
        assert "ALIST_PASSWORD" not in job["env"]["ALIST_CONFIGURED"]
        for secret_name in expected_secret_env:
            assert secret_name not in job["env"]

        mutation = next(step for step in job["steps"] if step.get("id") == "alist_mutation")
        assert mutation["env"] == expected_secret_env
        assert mutation["if"] == "env.ALIST_CONFIGURED == 'true'"

        for step in job["steps"]:
            if step is mutation:
                continue
            step_text = str(step)
            for secret_name in expected_secret_env:
                assert f"secrets.{secret_name}" not in step_text

        configured_steps = [
            step
            for step in job["steps"]
            if step.get("uses") == "actions/setup-python@v7"
            or step.get("name") == "Append offline install commands to release notes"
        ]
        for step in configured_steps:
            assert step["if"] == "env.ALIST_CONFIGURED == 'true'"


def test_stable_retract_validation_does_not_echo_an_invalid_dispatch_version():
    document = yaml.safe_load(STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8"))
    guard = next(
        step["run"]
        for step in document["jobs"]["retract"]["steps"]
        if step.get("name") == "Gate (semver + confirm)"
    )

    assert 'echo "${VERSION}"' not in guard
    assert "version '${VERSION}'" not in guard
    assert "::error::version must match the required stable format" in guard


def test_stable_retract_runs_secrets_only_from_a_verified_main_checkout():
    document = yaml.safe_load(STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8"))
    resolve = document["jobs"]["resolve"]
    retract = document["jobs"]["retract"]
    resolve_checkout = next(step for step in resolve["steps"] if step.get("uses") == "actions/checkout@v7")
    retract_checkout = next(step for step in retract["steps"] if step.get("uses") == "actions/checkout@v7")
    source_guard = next(step["run"] for step in retract["steps"] if step.get("name") == "Verify trusted source commit")

    assert document["permissions"] == {"contents": "read"}
    assert resolve["outputs"]["sha"] == "${{ steps.source.outputs.sha }}"
    assert resolve_checkout["with"] == {"ref": "main", "fetch-depth": 0}
    assert retract["name"] == "retract stable release"
    assert retract["if"] == "github.ref == 'refs/heads/main' && needs.resolve.outputs.sha != ''"
    assert retract["permissions"] == {"contents": "write"}
    assert retract_checkout["with"] == {"ref": "${{ needs.resolve.outputs.sha }}", "fetch-depth": 0}
    assert '[ "$(git rev-parse HEAD)" = "${SHA}" ]' in source_guard


def _stable_retract_release_step():
    document = yaml.safe_load(STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8"))
    return next(
        step["run"]
        for step in document["jobs"]["retract"]["steps"]
        if step.get("name") == "Delete GitHub release"
    )


def _bash_path(path):
    value = path.resolve().as_posix()
    if os.name == "nt" and len(value) >= 3 and value[1:3] == ":/":
        return f"/{value[0].lower()}{value[2:]}"
    return value


def _run_stable_retract_release_step(tmp_path, scenario):
    fake_bin = tmp_path / "bin"
    fake_bin.mkdir()
    fake_gh = fake_bin / "gh"
    fake_gh.write_text(
        """#!/usr/bin/env bash
printf '%s\\n' "$*" >> "${GH_CALL_LOG}"
if [ "$1" = "api" ]; then
  case "${GH_SCENARIO}" in
    exists) exit 0 ;;
    not_found) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
    forbidden) echo "gh: Resource not accessible (HTTP 403)" >&2; exit 1 ;;
  esac
fi
if [ "$1" = "release" ] && [ "$2" = "delete" ]; then
  exit 0
fi
exit 97
""",
        encoding="utf-8",
        newline="\n",
    )
    fake_gh.chmod(0o755)
    call_log = tmp_path / "gh-calls.txt"
    script = (
        f'export PATH="{_bash_path(fake_bin)}:$PATH"\n'
        f'export GH_CALL_LOG="{_bash_path(call_log)}"\n'
        f'export GH_SCENARIO="{scenario}"\n'
        f'{_stable_retract_release_step()}'
    )
    result = subprocess.run(
        [_bash_for_workflow_guard(), "-e", "-o", "pipefail", "-c", script],
        capture_output=True,
        check=False,
        encoding="utf-8",
        env={
            **os.environ,
            "GITHUB_REPOSITORY": "mihari-proxy/mihari",
            "VERSION": "v1.2.3",
        },
    )
    calls = call_log.read_text(encoding="utf-8").splitlines()
    return result, calls


def test_stable_retract_deletes_an_existing_release_without_deleting_its_tag(tmp_path):
    result, calls = _run_stable_retract_release_step(tmp_path, "exists")

    assert result.returncode == 0, result.stderr
    assert calls == [
        "api repos/mihari-proxy/mihari/releases/tags/v1.2.3",
        "release delete v1.2.3 --yes",
    ]
    assert all("--cleanup-tag" not in call for call in calls)


def test_stable_retract_treats_only_an_explicit_release_404_as_idempotent(tmp_path):
    result, calls = _run_stable_retract_release_step(tmp_path, "not_found")

    assert result.returncode == 0, result.stderr
    assert calls == ["api repos/mihari-proxy/mihari/releases/tags/v1.2.3"]


def test_stable_retract_fails_closed_on_non_404_release_lookup_errors(tmp_path):
    result, calls = _run_stable_retract_release_step(tmp_path, "forbidden")

    assert result.returncode != 0
    assert calls == ["api repos/mihari-proxy/mihari/releases/tags/v1.2.3"]
    assert "::error::unable to determine whether the GitHub release exists" in result.stdout


def test_stable_retract_confirmation_copy_names_permanent_removals_and_retained_tag():
    document = yaml.safe_load(STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8"))
    description = document[True]["workflow_dispatch"]["inputs"]["confirm"]["description"]
    guard = next(
        step["run"]
        for step in document["jobs"]["retract"]["steps"]
        if step.get("name") == "Gate (semver + confirm)"
    )

    for copy in (description, guard):
        assert "permanently remove" in copy
        assert "Release/assets and AList distribution" in copy
        assert "canonical stable tag" in copy
        assert "retained" in copy


def test_stable_retract_never_uses_raw_version_in_display_or_prevalidation_output():
    workflow = STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8")
    document = yaml.safe_load(workflow)
    guard = next(
        step["run"]
        for step in document["jobs"]["retract"]["steps"]
        if step.get("name") == "Gate (semver + confirm)"
    )

    assert "name: retract ${{ inputs.version }}" not in workflow
    assert document["jobs"]["retract"]["name"] == "retract stable release"
    assert 'echo "${VERSION}"' not in guard
    assert "version '${VERSION}'" not in guard


def _run_version_gate(workflow_path, job_name, step_name, version, output_path):
    document = yaml.safe_load(workflow_path.read_text(encoding="utf-8"))
    guard = next(
        step["run"]
        for step in document["jobs"][job_name]["steps"]
        if step.get("name") == step_name
    )
    return subprocess.run(
        [_bash_for_workflow_guard(), "-c", guard],
        capture_output=True,
        check=False,
        env={
            **os.environ,
            "CONFIRM": "true",
            "GITHUB_EVENT_NAME": "workflow_dispatch",
            "GITHUB_OUTPUT": str(output_path),
            "VERSION": version,
        },
        text=True,
    )


def test_stable_version_gates_validate_the_complete_canonical_value(tmp_path):
    gates = (
        (STABLE_WORKFLOW, "resolve", "Guard stable version"),
        (STABLE_WORKFLOW, "build", "Version gate"),
        (STABLE_WORKFLOW, "release", "Version gate (final)"),
        (STABLE_RETRACT_WORKFLOW, "retract", "Gate (semver + confirm)"),
    )

    for workflow, job_name, step_name in gates:
        for version in ("v0.0.0", "v1.2.3", "v12.34.56"):
            accepted = _run_version_gate(workflow, job_name, step_name, version, tmp_path / "output")
            assert accepted.returncode == 0, accepted.stderr

        for invalid in (
            "v01.2.3",
            "v1.02.3",
            "v1.2.03",
            "v1.2.3\n::warning::injected-workflow-command",
        ):
            rejected = _run_version_gate(workflow, job_name, step_name, invalid, tmp_path / "output")
            assert rejected.returncode != 0
            assert "::warning::injected-workflow-command" not in rejected.stdout
            assert "::warning::injected-workflow-command" not in rejected.stderr
            assert invalid not in rejected.stdout
            assert invalid not in rejected.stderr


def test_stable_publication_verifies_the_current_remote_tag_immediately_before_and_after():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    steps = document["jobs"]["release"]["steps"]
    before_index = next(
        index for index, step in enumerate(steps) if step.get("name") == "Create or verify current stable tag"
    )
    publish_index = next(index for index, step in enumerate(steps) if step.get("name") == "Publish GitHub release")
    after_index = next(
        index for index, step in enumerate(steps) if step.get("name") == "Verify current stable tag after publication"
    )
    before = steps[before_index]["run"]
    after = steps[after_index]["run"]

    assert before_index + 1 == publish_index
    assert publish_index + 1 == after_index
    assert steps[publish_index]["id"] == "github_release"
    assert steps[after_index]["if"] == (
        "!cancelled() && "
        "(steps.github_release.outcome == 'success' || steps.github_release.outcome == 'failure')"
    )
    assert steps[before_index]["env"] == {"GH_TOKEN": "${{ secrets.GITHUB_TOKEN }}"}
    assert steps[after_index]["env"] == {"GH_TOKEN": "${{ secrets.GITHUB_TOKEN }}"}
    assert 'GITHUB_EVENT_NAME' in before
    assert 'gh api --method POST "repos/${GITHUB_REPOSITORY}/git/refs"' in before
    assert 'ref=refs/tags/${VERSION}' in before
    assert 'sha=${SHA}' in before
    assert "tag-push release requires the pushed tag to still exist" in before
    assert before.rstrip().endswith("verify_tag_ref")
    for verification in (before, after):
        assert "git/ref/tags/${VERSION}" in verification
        assert "for depth in $(seq 1 7)" in verification
        assert "jq -c '{type: .object.type, sha: .object.sha}'" in verification
        assert "github_release_policy.py tag-chain" in verification
        assert '--expected-sha "${SHA}"' in verification
    assert "--method POST" not in after
    assert "github_release_policy.py tag-chain" in steps[after_index]["run"]


def test_stable_workflow_input_descriptions_do_not_advertise_noncanonical_versions():
    release = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    retract = yaml.safe_load(STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8"))

    for document in (release, retract):
        description = document[True]["workflow_dispatch"]["inputs"]["version"]["description"]
        assert "leading zeroes are rejected" in description
        assert "^v[0-9]+" not in description


def test_release_workflow_uses_policy_instead_of_write_only_get_fields():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert ".make_latest" not in workflow
    assert ".target_commitish == $sha" not in workflow
    assert "github_release_policy.py release" in workflow
    assert "github_release_policy.py tag-chain" in workflow
    assert "github_release_policy.py latest" in workflow


def test_dev_release_create_uses_api_types_and_reports_api_errors():
    document = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    create = next(
        step["run"]
        for step in document["jobs"]["publish"]["steps"]
        if step.get("name") == "Create prerelease and upload missing assets"
    )

    assert "-F make_latest=false" not in create
    assert "-F prerelease=true -F draft=false -f make_latest=false" in create
    assert "cat /tmp/release-create.json >&2" in create
    assert "cat /tmp/release-create.err >&2" in create


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


def test_release_workflow_validates_stable_latest_before_any_mutation():
    document = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    publish_steps = document["jobs"]["publish"]["steps"]
    before = next(
        step["run"]
        for step in publish_steps
        if step.get("name") == "Read and validate stable latest before dev mutation"
    )
    workflow = WORKFLOW.read_text(encoding="utf-8")
    validation = "python scripts/github_release_policy.py latest"
    self_comparison = (
        "--before /tmp/latest-before.json --after /tmp/latest-before.json --dev-version \"${VERSION}\""
    )

    assert "/releases/latest" in before
    assert validation in before
    assert self_comparison in before
    validation_index = workflow.index(validation)
    for mutation in (
        'gh api --method POST "repos/${GITHUB_REPOSITORY}/git/refs"',
        'gh api --method POST "repos/${GITHUB_REPOSITORY}/releases"',
        'gh release upload "${VERSION}" "dist/${asset}"',
    ):
        assert validation_index < workflow.index(mutation)


def test_release_workflow_revalidates_tag_after_final_asset_verification():
    document = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    final = next(
        step["run"]
        for step in document["jobs"]["publish"]["steps"]
        if step.get("name") == "Final verify prerelease and stable latest"
    )
    workflow = WORKFLOW.read_text(encoding="utf-8")
    helper_start = final.index("verify_final_tag_ref()")
    helper_end = final.index("download_and_verify_assets()")
    helper = final[helper_start:helper_end]

    assert workflow.index('gh release upload "${VERSION}" "dist/${asset}"') < workflow.index("/tmp/final-release.json")
    assert final.index("/tmp/final-release.json") < final.rindex("download_and_verify_assets")
    assert final.rindex("download_and_verify_assets") < final.rindex("verify_final_tag_ref")
    assert final.rindex("verify_final_tag_ref") < final.index("/tmp/latest-after.json")
    assert "git/ref/tags/${VERSION}" in helper
    assert "for depth in $(seq 1 7)" in helper
    assert "jq -c '{type: .object.type, sha: .object.sha}'" in helper
    assert "github_release_policy.py tag-chain" in helper


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

    injected_command = "::warning::injected-workflow-command"
    payload = f"v0.3.0-dev.1\n{injected_command}"
    rejected = _run_dev_version_guard(payload)

    assert rejected.returncode != 0
    assert injected_command not in rejected.stdout
    assert injected_command not in rejected.stderr
    assert payload not in rejected.stdout
    assert payload not in rejected.stderr


def test_dev_version_guard_rejects_leading_zero_components():
    for version in (
        "v00.3.0-dev.1",
        "v0.03.0-dev.1",
        "v0.3.00-dev.1",
        "v0.3.0-dev.01",
    ):
        rejected = _run_dev_version_guard(version)
        assert rejected.returncode != 0
        assert version not in rejected.stdout
        assert version not in rejected.stderr


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


def test_ci_runs_release_safety_suite_from_pinned_requirements_on_all_integration_branches():
    document = yaml.safe_load(CI_WORKFLOW.read_text(encoding="utf-8"))

    assert document["permissions"] == {"contents": "read"}
    assert document[True]["push"]["branches"] == ["main", "dev", "master"]
    assert set(document["jobs"]) == {
        "lint",
        "unit",
        "test",
        "govulncheck",
        "race",
        "vet-format",
        "coverage",
        "build",
        "cross-build",
    }

    steps = document["jobs"]["unit"]["steps"]
    install = next(step for step in steps if step.get("name") == "Install release-safety test dependencies")
    safety = next(step for step in steps if step.get("name") == "Test release safety policies")

    assert install["run"] == (
        "python -m pip install --disable-pip-version-check "
        "-r scripts/requirements-release-test.txt"
    )
    assert safety["run"] == f"python -m pytest {RELEASE_SAFETY_TESTS}"


def test_branch_governance_keeps_feature_work_off_main_and_dev_without_promising_review_rules():
    agents = AGENTS.read_text(encoding="utf-8")
    contributing = CONTRIBUTING.read_text(encoding="utf-8")
    normalized_agents = agents.replace("`", "")

    assert "main 或 dev 分支上直接修改或提交" in normalized_agents
    assert "一次性 main PR" in normalized_agents
    assert "main 或 dev 分支创建 commit" in normalized_agents
    assert "feat/*、fix/* ──PR──> dev ──晋级 PR──> main" in contributing
    assert "hotfix/*（从 main） ──PR──> main" in contributing
    assert "main ──同步 PR──> dev" in contributing
    assert "普通 PR 使用 squash merge" in contributing
    assert "晋级和 `main → dev` 同步使用 merge commit" in contributing
    assert "不设定固定审核人数或 bypass 规则" in contributing
    assert "至少等待一个审核通过" not in contributing


def _markdown_section(document, heading):
    start = document.index(heading)
    next_heading = document.find("\n## ", start + len(heading))
    if next_heading == -1:
        return document[start:]
    return document[start:next_heading]


def test_release_document_top_level_table_requires_main_for_each_stable_dispatch():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    trigger_section = _markdown_section(release, "## 工作流触发机制")
    top_level_table = trigger_section[: trigger_section.index("各发布 workflow")]
    release_row = next(line for line in top_level_table.splitlines() if line.startswith("| **Release**"))
    retract_row = next(line for line in top_level_table.splitlines() if line.startswith("| **Retract**"))

    assert "从 `main`" in release_row
    assert "从 `main`" in retract_row


def test_release_document_scopes_main_dispatch_to_stable_release_and_retract_sections():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    stable_dispatch = _markdown_section(release, "## 手动触发发布")
    retract = _markdown_section(release, "## 回滚发布（致命错误撤回）")

    assert "必须选择 `main` 分支/ref" in stable_dispatch
    assert "选择 `main` 分支/ref" in retract


def test_release_document_requires_tag_ruleset_because_pre_and_post_checks_are_not_atomic():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    stable_dispatch = _markdown_section(release, "## 手动触发发布")

    assert "前后复核并非原子操作" in stable_dispatch
    assert "stable tag-target ruleset" in stable_dispatch
    assert "真实 stable 发布前" in stable_dispatch
    assert "禁止更新和删除" in stable_dispatch
    assert "另行授权" in stable_dispatch
    assert "配置并回读" in stable_dispatch


def test_release_documents_keep_retracted_stable_tags_and_require_a_higher_fix_version():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    distribution = DISTRIBUTION_DOCUMENT.read_text(encoding="utf-8")
    release_retract = _markdown_section(release, "## 回滚发布（致命错误撤回）")
    distribution_retract = _markdown_section(distribution, "## 五、版本撤回（致命错误）")

    for section in (release_retract, distribution_retract):
        assert "永久移除" in section
        assert "Release" in section
        assert "AList" in section
        assert "保留 canonical stable tag" in section
        assert "更高版本号" in section
        assert "同版本号重发" not in section
        assert "--cleanup-tag" not in section


def test_release_document_requires_authorized_tag_ruleset_for_real_dev_release():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    channel_overview = _markdown_section(release, "## Stable 与 Dev 发布通道")
    dev_dispatch = _markdown_section(release, "### Dev 手动发布")

    for section in (channel_overview, dev_dispatch):
        assert "不可变" not in section

    assert "校验 canonical `vX.Y.Z-dev.N` 版本身份" in channel_overview
    assert "身份不符即拒绝且不覆盖" in channel_overview
    assert "canonical `vX.Y.Z-dev.N` 格式" in dev_dispatch
    assert "dev 前后复核并非原子操作" in dev_dispatch
    assert "真实 dev 发布前" in dev_dispatch
    assert "dev tag-target ruleset" in dev_dispatch
    assert "另行授权" in dev_dispatch
    assert "配置并回读" in dev_dispatch


def test_distribution_document_explains_how_to_recover_the_uploaded_index_backup():
    distribution = DISTRIBUTION_DOCUMENT.read_text(encoding="utf-8")
    publication = _markdown_section(distribution, "### 发布顺序与非原子 index 窗口")

    assert "事务前要求权威实时内容等于调用方观察到的原值" in publication
    assert "只执行一次 PUT 和一次权威 readback" in publication
    assert "回读等于目标内容：提交成功" in publication
    assert "回读仍等于原值：以 index unchanged 失败" in publication
    assert "必须从头重跑完整 release/retract workflow" in publication
    assert "不在 writer 内再次 PUT" in publication
    assert "才允许下一次写入" not in publication
    assert "第三方值和不确定 readback 均转入人工恢复" in publication
    assert "不得触发自动 rollback" in publication
    assert "人工恢复" in publication
    assert "不提供 compare-and-swap（CAS）或原子 rename" in publication
    assert "事务前检查与单次 PUT 之间仍有竞态" in publication

    assert "Stable release 与 retract workflow 共用 channel concurrency" in publication
    assert "人工 `regenerate-index` 或 artifact 恢复前" in publication
    assert "相关 release/retract workflow 均未运行" in publication
    assert "整个期间，都必须禁止其他人工或自动 writer" in publication

    assert "stable-index-backup-<run_id>-<attempt>" in publication
    assert "保留 3 天" in publication
    assert "`index.txt` 保存原始字节" in publication
    assert "`metadata.json` 保存" in publication
    for field in ("`existed`", "`channel`", "`path`", "`sha256`"):
        assert field in publication

    assert "下载" in publication
    assert "恢复" in publication
    assert "`existed=false` 表示原对象不存在" in publication
    assert "删除该 index" in publication
    assert "`existed=true` 且 `index.txt` 为空表示恢复为空文件" in publication
    assert "`existed=true` 且非空则逐字节恢复其内容" in publication
    assert "恢复后再次下载并逐字节核对" in publication


def test_distribution_document_limits_backup_artifacts_to_alist_mutation_failures_or_cancellation():
    distribution = DISTRIBUTION_DOCUMENT.read_text(encoding="utf-8")
    publication = _markdown_section(distribution, "### 发布顺序与非原子 index 窗口")

    assert "仅当 AList mutation 失败或 mutation 期间取消" in publication
    assert "AList mutation 已成功后" in publication
    assert "不会上传该 artifact" in publication
    assert "无需恢复旧 index" in publication


def test_release_documents_describe_batch_a_as_code_prepared_and_keep_p2_alist_unavailable():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    distribution = DISTRIBUTION_DOCUMENT.read_text(encoding="utf-8")

    for document in (release, distribution):
        assert "dev 发布代码已准备" in document
        assert "P2 AList 发布与撤回 workflow 尚不可用" in document
        assert "publish-dev-alist.yml" not in document
        assert "retract-dev.yml" not in document

    assert "Actions 产物或 dev 版本目录" not in distribution
    assert "不创建或操作该目录" in distribution

    assert "GitHub dev tag、prerelease 与 14 个 assets" in release
    assert "仅写 GitHub" in release
    assert "不写 AList" in release
    assert "lightweight 或 annotated" in release
