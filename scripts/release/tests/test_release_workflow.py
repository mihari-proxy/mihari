"""Behavioral regression checks for stable and dev release workflow invariants."""

import copy
import os
from pathlib import Path
import shlex
import subprocess

import pytest
import yaml


WORKFLOW = Path(__file__).resolve().parents[3] / ".github" / "workflows" / "release-dev.yml"
RETRACT_DEV_WORKFLOW = Path(__file__).resolve().parents[3] / ".github" / "workflows" / "retract-dev.yml"
STABLE_WORKFLOW = Path(__file__).resolve().parents[3] / ".github" / "workflows" / "release.yml"
STABLE_RETRACT_WORKFLOW = Path(__file__).resolve().parents[3] / ".github" / "workflows" / "retract.yml"
CI_WORKFLOW = Path(__file__).resolve().parents[3] / ".github" / "workflows" / "ci.yml"
PAGES_WORKFLOW = Path(__file__).resolve().parents[3] / ".github" / "workflows" / "pages.yml"
CHANGELOG_CHECK_WORKFLOW = (
    Path(__file__).resolve().parents[3] / ".github" / "workflows" / "changelog-check.yml"
)
PR_SOURCE_CHECK_WORKFLOW = (
    Path(__file__).resolve().parents[3] / ".github" / "workflows" / "pr-source-check.yml"
)
AGENTS = Path(__file__).resolve().parents[3] / "AGENTS.md"
CONTRIBUTING = Path(__file__).resolve().parents[3] / ".github" / "CONTRIBUTING.md"
CONTRIBUTING_ZH_CN = Path(__file__).resolve().parents[3] / ".github" / "CONTRIBUTING.zh-CN.md"
PR_TEMPLATE = Path(__file__).resolve().parents[3] / ".github" / "PULL_REQUEST_TEMPLATE.md"
RELEASE_DOCUMENT = Path(__file__).resolve().parents[3] / "docs" / "RELEASE.md"
DISTRIBUTION_DOCUMENT = Path(__file__).resolve().parents[3] / "docs" / "distribution.md"
DESIGN_DOCUMENT = (
    Path(__file__).resolve().parents[3]
    / "docs"
    / "superpowers"
    / "specs"
    / "2026-08-24-prerelease-alist-index-design.md"
)

RELEASE_SAFETY_TESTS = (
    "scripts/release/tests/test_release_policy.py "
    "scripts/release/tests/test_github_release_policy.py "
    "scripts/release/tests/test_changelog_policy.py "
    "scripts/release/tests/test_changelog_branch_policy.py "
    "scripts/release/tests/test_release_workflow.py "
    "scripts/release/tests/test_alist_client.py "
    "scripts/release/tests/test_alist_index.py "
    "scripts/release/tests/test_release_alist.py "
    "scripts/release/tests/test_retract_alist.py "
    "scripts/release/tests/test_regenerate_index.py "
    "scripts/release/tests/test_alist_channel_guard.py -q"
)


def _workflow_step(document, job_name, *, name=None, uses=None):
    matches = [
        step
        for step in document["jobs"][job_name]["steps"]
        if (name is None or step.get("name") == name)
        and (uses is None or step.get("uses") == uses)
    ]
    assert len(matches) == 1
    return matches[0]


def _shell_invocations(run):
    normalized = run.replace("\\\n", " ")
    invocations = []
    for line in normalized.splitlines():
        lexer = shlex.shlex(line, posix=True, punctuation_chars=";&|")
        lexer.whitespace_split = True
        lexer.commenters = "#"
        invocation = []
        for token in lexer:
            if token and all(character in ";&|" for character in token):
                if invocation:
                    invocations.append(invocation)
                    invocation = []
                continue
            invocation.append(token)
        if invocation:
            invocations.append(invocation)
    return invocations


def _flag_values(command, flag):
    values = []
    index = 0
    while index < len(command):
        argument = command[index]
        if argument == flag:
            assert index + 1 < len(command), f"{flag} requires a value"
            values.append(command[index + 1])
            index += 2
            continue
        if argument.startswith(f"{flag}="):
            value = argument[len(flag) + 1 :]
            assert value, f"{flag} requires a value"
            values.append(value)
        index += 1
    return values


def _linker_x_assignments(ldflags):
    arguments = shlex.split(ldflags)
    assignments = []
    index = 0
    while index < len(arguments):
        argument = arguments[index]
        if argument == "-X":
            assert index + 1 < len(arguments), "-X requires an assignment"
            assignments.append(arguments[index + 1])
            index += 2
            continue
        if argument.startswith("-X="):
            assignment = argument[len("-X=") :]
            assert assignment, "-X requires an assignment"
            assignments.append(assignment)
        index += 1
    return assignments


def _assert_bundle_job_has_no_mutable_input_discovery(bundle_job):
    assert "GITHUB_TOKEN" not in bundle_job.get("env", {})
    for step in bundle_job["steps"]:
        assert "GITHUB_TOKEN" not in step.get("env", {})
        for command in _shell_invocations(step.get("run", "")):
            is_lock_resolver = any(
                "scripts/tools/resolve-release-inputs" in argument.replace("\\", "/")
                for argument in command
            )
            is_latest_api = any(
                "/releases/latest" in argument for argument in command
            )
            is_implicit_latest_release = any(
                command[index : index + 3] == ["gh", "release", "view"]
                for index in range(len(command) - 2)
            )

            assert not is_lock_resolver, (
                "bundle job must not invoke the input-lock resolver"
            )
            assert not is_latest_api, "bundle job must not query the latest-release API"
            assert not is_implicit_latest_release, (
                "bundle job must not resolve the latest release"
            )


def _assert_release_build_wiring(document):
    build = _workflow_step(document, "build", name="Build")
    commands = [
        command
        for command in _shell_invocations(build["run"])
        if command[:2] == ["go", "build"]
    ]
    assert len(commands) == 1, "release build step must invoke go build exactly once"
    command = commands[0]

    buildvcs_flags = [
        argument
        for argument in command
        if argument == "-buildvcs" or argument.startswith("-buildvcs=")
    ]
    trimpath_flags = [
        argument
        for argument in command
        if argument == "-trimpath" or argument.startswith("-trimpath=")
    ]
    assert buildvcs_flags == ["-buildvcs=false"]
    assert trimpath_flags == ["-trimpath"]

    ldflags_values = _flag_values(command, "-ldflags")
    assert len(ldflags_values) == 1, "release build requires exactly one -ldflags"
    version_symbol = "github.com/mihari-proxy/mihari/internal/buildinfo.Version"
    version_prefix = f"{version_symbol}="
    version_values = [
        assignment[len(version_prefix) :]
        for assignment in _linker_x_assignments(ldflags_values[0])
        if assignment.startswith(version_prefix)
    ]
    assert version_values == ["${VERSION}"]


def _assert_release_bundle_wiring(document):
    bundle_job = document["jobs"]["bundle"]
    _assert_bundle_job_has_no_mutable_input_discovery(bundle_job)
    bundle = _workflow_step(document, "bundle", name="Build all-in-one bundles")
    commands = [
        command
        for command in _shell_invocations(bundle["run"])
        if command[:3] == ["go", "run", "./scripts/tools/build-all-in-one"]
    ]
    assert len(commands) == 1, "bundle step must invoke the AIO builder exactly once"
    lock_values = _flag_values(commands[0], "--lock")
    assert lock_values == ["scripts/release/release-inputs.lock.json"]


def _assert_release_setup_go_wiring(document):
    action = "actions/setup-go"
    for job_name in ("build", "bundle"):
        setup_go_steps = [
            step
            for step in document["jobs"][job_name]["steps"]
            if step.get("uses") == action
            or str(step.get("uses", "")).startswith(f"{action}@")
        ]
        assert len(setup_go_steps) == 1, (
            f"{job_name} job must contain exactly one setup-go step"
        )
        setup_go = setup_go_steps[0]
        assert setup_go["uses"] == "actions/setup-go@v7"
        assert setup_go.get("with", {}).get("go-version-file") == "go.mod"


@pytest.fixture
def bundle_job_with_resolver_in_another_step():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    bundle_job = copy.deepcopy(document["jobs"]["bundle"])
    for step in bundle_job["steps"]:
        step.get("env", {}).pop("GITHUB_TOKEN", None)
    bundle_job["steps"].insert(
        1,
        {
            "name": "Resolve inputs behind the build step",
            "run": (
                "go run ./scripts/tools/resolve-release-inputs "
                "--channel stable --out scripts/release/release-inputs.lock.json"
            ),
        },
    )
    return bundle_job


def test_release_build_jobs_use_pinned_go_and_reproducible_binary_flags():
    for workflow_path in (STABLE_WORKFLOW, WORKFLOW):
        document = yaml.safe_load(workflow_path.read_text(encoding="utf-8"))
        _assert_release_setup_go_wiring(document)
        _assert_release_build_wiring(document)


@pytest.mark.parametrize("mutation", ["move", "replace"])
def test_setup_go_guard_binds_pinned_action_to_the_bundle_job(mutation):
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    bundle_steps = document["jobs"]["bundle"]["steps"]
    setup_go = next(
        step for step in bundle_steps if step.get("uses") == "actions/setup-go@v7"
    )
    if mutation == "move":
        bundle_steps.remove(setup_go)
        document["jobs"]["release"]["steps"].append(setup_go)
    else:
        setup_go["uses"] = "actions/setup-go@main"

    with pytest.raises(AssertionError):
        _assert_release_setup_go_wiring(document)


def test_release_bundle_jobs_consume_the_reviewed_input_lock_without_resolving_latest():
    for workflow_path in (STABLE_WORKFLOW, WORKFLOW):
        document = yaml.safe_load(workflow_path.read_text(encoding="utf-8"))
        _assert_release_bundle_wiring(document)


def test_build_wiring_guard_rejects_later_buildvcs_override():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    build = _workflow_step(document, "build", name="Build")
    build["run"] = build["run"].replace(
        "go build -buildvcs=false",
        "go build -buildvcs=false -buildvcs=true",
        1,
    )

    with pytest.raises(AssertionError):
        _assert_release_build_wiring(document)


def test_build_wiring_guard_rejects_later_wrong_version_assignment():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    build = _workflow_step(document, "build", name="Build")
    canonical = "github.com/mihari-proxy/mihari/internal/buildinfo.Version=${VERSION}"
    build["run"] = build["run"].replace(
        canonical,
        f"{canonical} -X github.com/mihari-proxy/mihari/internal/buildinfo.Version=v9.9.9",
        1,
    )

    with pytest.raises(AssertionError):
        _assert_release_build_wiring(document)


def test_bundle_wiring_guard_rejects_second_lock_override():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    bundle = _workflow_step(document, "bundle", name="Build all-in-one bundles")
    bundle["run"] = bundle["run"].replace(
        "--lock scripts/release/release-inputs.lock.json",
        "--lock scripts/release-inputs.lock.json --lock=scripts/unreviewed.lock.json",
        1,
    )

    with pytest.raises(AssertionError):
        _assert_release_bundle_wiring(document)


def test_bundle_job_guard_rejects_resolver_after_shell_separator():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    bundle_job = document["jobs"]["bundle"]
    bundle_job["steps"].append(
        {
            "name": "Hide mutable discovery after another command",
            "run": (
                "echo ok; go run ./scripts/tools/resolve-release-inputs "
                "--channel stable --out scripts/release/release-inputs.lock.json"
            ),
        }
    )

    with pytest.raises(AssertionError, match="must not invoke the input-lock resolver"):
        _assert_bundle_job_has_no_mutable_input_discovery(bundle_job)


def test_bundle_job_guard_rejects_gh_release_view_with_output_redirection():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    bundle_job = document["jobs"]["bundle"]
    bundle_job["steps"].append(
        {
            "name": "Hide latest release lookup behind redirection",
            "run": "gh release view > /tmp/latest.txt",
        }
    )

    with pytest.raises(AssertionError, match="must not resolve the latest release"):
        _assert_bundle_job_has_no_mutable_input_discovery(bundle_job)


def test_bundle_job_guard_detects_resolver_hidden_in_another_step(
    bundle_job_with_resolver_in_another_step,
):
    with pytest.raises(AssertionError, match="must not invoke the input-lock resolver"):
        _assert_bundle_job_has_no_mutable_input_discovery(
            bundle_job_with_resolver_in_another_step
        )


def test_bundle_job_guard_allows_comments_and_messages_about_latest_releases():
    document = yaml.safe_load(STABLE_WORKFLOW.read_text(encoding="utf-8"))
    bundle_job = copy.deepcopy(document["jobs"]["bundle"])
    for step in bundle_job["steps"]:
        step.get("env", {}).pop("GITHUB_TOKEN", None)
    bundle_job["steps"].append(
        {
            "name": "Explain locked inputs",
            "run": (
                "# The latest release is resolved only during reviewed maintenance.\n"
                'echo "The latest release inputs are already locked."'
            ),
        }
    )

    _assert_bundle_job_has_no_mutable_input_discovery(bundle_job)


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
        assert "python -m pip install --disable-pip-version-check -r scripts/release/requirements.txt" in install
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
    validation = "python scripts/release/github_release_policy.py latest"
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


def test_release_workflow_limits_tag_peeling():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert "for depth in $(seq 1 7)" in workflow
    assert "jq -c '{type: .object.type, sha: .object.sha}'" in workflow
    assert "jq -s . /tmp/tag-chain.jsonl" in workflow


def test_dev_publish_and_retract_use_independent_alist_lock():
    release_dev = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    retract_dev = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    expected = {"group": "mihari-dev-alist", "cancel-in-progress": False}
    assert release_dev["jobs"]["publish"].get("concurrency") == expected
    assert retract_dev["jobs"]["retract"].get("concurrency") == expected
    assert "mihari-stable-alist" not in WORKFLOW.read_text(encoding="utf-8")
    assert "mihari-stable-alist" not in RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8")
    assert release_dev.get("concurrency", {}).get("group") == "dev-release-${{ inputs.version }}"


def test_dev_alist_secrets_are_scoped_only_to_the_mutation_step():
    release = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    retract = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    expected_secret_env = {
        "ALIST_URL": "${{ secrets.ALIST_URL }}",
        "ALIST_USERNAME": "${{ secrets.ALIST_USERNAME }}",
        "ALIST_PASSWORD": "${{ secrets.ALIST_PASSWORD }}",
    }
    allowed_job_env = {
        "ALIST_CONFIGURED",
        "SHA",
        "GH_TOKEN",
        "DEV_RELEASE_NAME",
        "DEV_RELEASE_BODY",
        "MIHARI_KEEP_VERSIONS",
    }

    for workflow, job_name in ((release, "publish"), (retract, "retract")):
        job = workflow["jobs"][job_name]
        assert {"ALIST_CONFIGURED", "SHA"} <= set(job["env"])
        assert set(job["env"]) <= allowed_job_env
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
            assert "secrets.ALIST_USERNAME" not in step_text
            assert "secrets.ALIST_PASSWORD" not in step_text
            if "secrets.ALIST_URL" in step_text:
                assert "secrets.ALIST_URL != ''" in step_text
                assert "${{ secrets.ALIST_URL }}" not in step_text


def test_dev_alist_mutation_uses_compare_first_exit():
    cases = (
        (WORKFLOW, "publish", "Publish to AList drive", "release-alist.py", "publish_status"),
        (RETRACT_DEV_WORKFLOW, "retract", "Retract from AList drive", "retract-alist.py", "retract_status"),
    )
    for path, job, step_name, writer, status_var in cases:
        document = yaml.safe_load(path.read_text(encoding="utf-8"))
        run = _workflow_step(document, job, name=step_name)["run"]
        assert "set +e" in run
        assert run.index("alist_channel_guard.py snapshot") < run.index(writer)
        assert run.index(writer) < run.index("alist_channel_guard.py compare")
        compare_exit = 'if [ "${compare_status}" -ne 0 ]; then exit "${compare_status}"; fi'
        assert compare_exit in run
        assert run.index(compare_exit) < run.index(f'exit "${{{status_var}}}"')


def test_dev_retract_peel_expected_sha_is_release_sha_not_job_sha():
    document = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    peel = _workflow_step(document, "retract", name="Peel canonical tag for identity SHA")
    run = peel["run"]
    assert "github_release_policy.py tag-chain" in run
    assert '--expected-sha "${RELEASE_SHA}"' in run or '--expected-sha "${tag_sha}"' in run
    assert '--expected-sha "${SHA}"' not in run
    mutation = _workflow_step(document, "retract", name="Retract from AList drive")
    assert '--commit-sha "${RELEASE_SHA}"' in mutation["run"]
    assert '--commit-sha "${SHA}"' not in mutation["run"]


def test_dev_alist_isolation_artifacts_are_separate_from_writer_backup():
    expected_if = (
        "env.ALIST_CONFIGURED == 'true' && "
        "((failure() && steps.alist_mutation.outcome == 'failure') || "
        "(cancelled() && steps.alist_mutation.outcome == 'cancelled'))"
    )
    for path, job in ((WORKFLOW, "publish"), (RETRACT_DEV_WORKFLOW, "retract")):
        document = yaml.safe_load(path.read_text(encoding="utf-8"))
        text = path.read_text(encoding="utf-8")
        steps = document["jobs"][job]["steps"]
        dev_backup = next(
            step for step in steps
            if str(step.get("with", {}).get("name", "")).startswith("dev-index-backup-")
        )
        isolation = next(
            step for step in steps
            if str(step.get("with", {}).get("name", "")).startswith("stable-index-isolation-")
        )
        assert dev_backup["if"] == expected_if
        assert isolation["if"] == expected_if
        assert dev_backup["with"]["path"] == "${{ runner.temp }}/mihari-index-backup/dev/**"
        assert isolation["with"]["path"] == "${{ runner.temp }}/mihari-index-backup/stable-isolation/**"
        assert isolation["with"]["path"] != "${{ runner.temp }}/mihari-index-backup/stable/**"
        assert "${{ runner.temp }}/mihari-index-backup/stable/**" not in text
        for artifact in (dev_backup, isolation):
            assert artifact["with"]["if-no-files-found"] == "ignore"
            assert artifact["with"]["retention-days"] == 3


def test_dev_retract_resolve_outputs_sha_and_alist_runs_before_github_delete():
    document = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    resolve = document["jobs"]["resolve"]
    retract = document["jobs"]["retract"]
    assert resolve["outputs"]["sha"] == "${{ steps.source.outputs.sha }}"
    assert "refs/heads/dev" in retract["if"]
    checkout = next(step for step in retract["steps"] if step.get("uses") == "actions/checkout@v7")
    assert checkout["with"]["ref"] == "${{ needs.resolve.outputs.sha }}"
    names = [step.get("name") for step in retract["steps"]]
    assert names.index("Retract from AList drive") < names.index("Delete GitHub prerelease")


def test_dev_alist_writers_pin_channel_base_path_and_commit_sha():
    publish = WORKFLOW.read_text(encoding="utf-8")
    retract = RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8")
    assert "--channel dev" in publish
    assert "--base-path /mihari-release/mihari-dev" in publish
    assert '--commit-sha "${SHA}"' in publish
    assert "--channel dev" in retract
    assert "--base-path /mihari-release/mihari-dev" in retract


def test_dev_publish_mutates_alist_after_final_github_verify():
    workflow = WORKFLOW.read_text(encoding="utf-8")
    assert workflow.index("Final verify prerelease and stable latest") < workflow.index(
        "Publish to AList drive"
    )


def test_dev_release_notes_append_index_url_with_stable_root_downloaders():
    workflow = WORKFLOW.read_text(encoding="utf-8")
    assert "<!-- aio-install-dev -->" in workflow
    assert "mihari-dev/index.txt" in workflow
    assert "mihari-release/mihari/install-aio-remote.sh" in workflow
    assert "| MIHARI_INDEX_URL=" in workflow
    assert "$env:MIHARI_INDEX_URL=" in workflow
    assert "mihari-release/mihari/install-aio-remote.ps1" in workflow


def test_dev_retract_github_delete_is_idempotent_and_retains_canonical_tag():
    document = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    delete = _workflow_step(document, "retract", name="Delete GitHub prerelease")
    run = delete["run"]
    assert "gh release delete" in run
    assert "--cleanup-tag" not in run
    assert "canonical dev tag retained" in run
    assert "github_release_policy.py release" in run
    assert "--mode preflight" in run
    assert "<!-- github-release-dev -->" in run
    assert 'Mihari ${VERSION} (dev)' in run
    assert '>/dev/null' not in run
    gate = next(
        step["run"]
        for step in document["jobs"]["retract"]["steps"]
        if str(step.get("name", "")).startswith("Gate")
    )
    assert 'echo "${VERSION}"' not in gate


def test_alist_runtime_dependencies_are_pinned_after_checkout_in_dev_workflows():
    release_dev = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    retract_dev = yaml.safe_load(RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8"))
    for workflow, job_name, install_step_name in (
        (release_dev, "publish", "Publish to AList drive"),
        (retract_dev, "retract", "Retract from AList drive"),
    ):
        steps = workflow["jobs"][job_name]["steps"]
        checkout_index = next(
            index for index, step in enumerate(steps) if step.get("uses") == "actions/checkout@v7"
        )
        setup_index = next(
            index for index, step in enumerate(steps) if step.get("uses") == "actions/setup-python@v7"
        )
        install_index = next(
            index for index, step in enumerate(steps) if step.get("name") == install_step_name
        )
        install = steps[install_index]["run"]
        assert checkout_index < setup_index < install_index
        assert steps[setup_index]["with"] == {"python-version": "3.12"}
        assert (
            "python -m pip install --disable-pip-version-check -r scripts/release/requirements.txt"
            in install
        )
        assert "pip install requests" not in install


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
    site = next(step for step in steps if step.get("name") == "Test site SEO invariants")
    safety = next(step for step in steps if step.get("name") == "Test release safety policies")

    assert install["run"] == (
        "python -m pip install --disable-pip-version-check "
        "-r scripts/release/requirements-test.txt"
    )
    assert site["run"] == "python -m pytest scripts/site/test_site.py -q"
    assert safety["run"] == f"python -m pytest {RELEASE_SAFETY_TESTS}"


def test_pages_workflow_publishes_site_from_main_only():
    document = yaml.safe_load(PAGES_WORKFLOW.read_text(encoding="utf-8"))
    triggers = document[True]
    assert triggers["push"]["branches"] == ["main"]
    assert "site/**" in triggers["push"]["paths"]
    assert "site/**" in triggers["pull_request"]["paths"]
    assert document["permissions"]["contents"] == "read"
    assert document["permissions"]["pages"] == "write"
    assert document["permissions"]["id-token"] == "write"

    setup = next(
        step
        for step in document["jobs"]["build"]["steps"]
        if str(step.get("uses", "")).startswith("actions/configure-pages@")
    )
    assert "enablement" not in setup.get("with", {})

    upload = next(
        step
        for step in document["jobs"]["build"]["steps"]
        if str(step.get("uses", "")).startswith("actions/upload-pages-artifact@")
    )
    assert upload["with"]["path"] == "site"

    deploy = document["jobs"]["deploy"]
    assert "github.event_name == 'push'" in deploy["if"]
    assert "github.ref == 'refs/heads/main'" in deploy["if"]
    assert deploy["environment"]["name"] == "github-pages"
    assert any(
        str(step.get("uses", "")).startswith("actions/deploy-pages@")
        for step in deploy["steps"]
    )


def test_branch_governance_keeps_feature_work_off_main_and_dev_without_promising_review_rules():
    agents = AGENTS.read_text(encoding="utf-8")
    contributing = CONTRIBUTING.read_text(encoding="utf-8")
    contributing_zh_cn = CONTRIBUTING_ZH_CN.read_text(encoding="utf-8")
    pr_template = PR_TEMPLATE.read_text(encoding="utf-8")
    normalized_agents = agents.replace("`", "")

    assert "功能 PR 不得修改 CHANGELOG.md" in normalized_agents
    assert "CHANGELOG.md" in agents[agents.index("## 8. 变更检查清单") : agents.index("## 9. Commit 规范")]
    assert "must not modify `CHANGELOG.md`" in contributing
    assert "do not modify or commit `CHANGELOG.md`" in contributing
    assert "不会修改或提交 `CHANGELOG.md`" in contributing_zh_cn
    assert "CHANGELOG.md" in pr_template
    assert "chore/release-" in pr_template
    assert "main 或 dev 分支上直接修改或提交" in normalized_agents
    assert "一次性 main PR" in normalized_agents
    assert "main 或 dev 分支创建 commit" in normalized_agents
    assert "指向 `dev` 的功能 PR 不得修改 `CHANGELOG.md`" in contributing_zh_cn
    assert "chore/release-*" in contributing_zh_cn
    assert "feat/*、fix/* ──PR──> dev ──晋级 PR──> main" in contributing_zh_cn
    assert "hotfix/*（从 main） ──PR──> main" in contributing_zh_cn
    assert "main ──同步 PR──> dev" in contributing_zh_cn
    assert "普通 PR 使用 squash merge" in contributing_zh_cn
    assert "晋级和 `main → dev` 同步使用 merge commit" in contributing_zh_cn
    assert "不设定固定审核人数或 bypass 规则" in contributing_zh_cn
    assert "至少等待一个审核通过" not in contributing_zh_cn


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


def test_release_document_records_active_tag_ruleset_and_non_atomic_tag_checks():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    stable_dispatch = _markdown_section(release, "## 手动触发发布")

    assert "该复核不是原子操作" in stable_dispatch
    assert "已 active" in stable_dispatch
    assert "`refs/tags/v*`" in stable_dispatch
    assert "禁止删除、更新和 non-fast-forward" in stable_dispatch


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


def test_release_document_records_verified_dev_release_and_active_tag_ruleset():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    channel_overview = _markdown_section(release, "## Stable 与 Dev 发布通道")
    dev_dispatch = _markdown_section(release, "### Dev 手动发布")

    for section in (channel_overview, dev_dispatch):
        assert "不可变" not in section

    assert "校验 canonical `vX.Y.Z-dev.N` 版本身份" in channel_overview
    assert "身份不符即拒绝且不覆盖" in channel_overview
    assert "canonical `vX.Y.Z-dev.N` 格式" in dev_dispatch
    assert "`v0.9.0-dev.2` 已" in dev_dispatch
    assert "前后复核不是原子操作" in dev_dispatch
    assert "已 active" in dev_dispatch
    assert "`refs/tags/v*`" in dev_dispatch
    assert "禁止删除、更新和 non-fast-forward" in dev_dispatch


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


def test_release_documents_record_verified_dev_github_release_and_unavailable_dev_alist():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    distribution = DISTRIBUTION_DOCUMENT.read_text(encoding="utf-8")
    repo_root = Path(__file__).resolve().parents[3]
    installers = (
        (repo_root / "scripts" / "install" / "install-aio-remote.sh").read_text(encoding="utf-8"),
        (repo_root / "scripts" / "install" / "install-aio-remote.ps1").read_text(encoding="utf-8"),
    )
    public_dev_index = (
        "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari-dev/index.txt"
    )
    public_stable_index = (
        "https://cloud.xn--30q18ry71c.com/p/public/mihari-release/mihari/index.txt"
    )

    for document in (release, distribution):
        assert "v0.9.0-dev.2" in document
        assert "retract-dev.yml" in document
        assert "/mihari-release/mihari-dev" in document
        assert public_dev_index in document
        assert "mihari-dev-alist" in document
        assert "publish-dev-alist.yml" not in document

    assert "当前不创建或操作该目录" not in distribution
    assert "尚未实现的 dev AList" not in distribution
    assert "Actions 产物或 dev 版本目录" not in distribution
    assert "/mihari-release/mihari/index.txt" in release
    assert "/mihari-release/mihari/index.txt" in distribution
    assert public_stable_index in distribution
    assert "| MIHARI_INDEX_URL=" in distribution
    assert "$env:MIHARI_INDEX_URL=" in distribution
    assert "mihari-release/mihari/install-aio-remote.sh" in distribution
    assert "mihari-release/mihari/install-aio-remote.ps1" in distribution
    for installer in installers:
        assert public_stable_index in installer
        assert public_dev_index in installer
        assert "mihari-dev/install-aio-remote.sh" not in installer
        assert "mihari-dev/install-aio-remote.ps1" not in installer

    assert "lightweight 或 annotated" in release


def test_release_documents_treat_historical_dev_alist_backfill_as_operator_rule():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    trigger = _markdown_section(release, "## 工作流触发机制")
    dev_dispatch = _markdown_section(release, "### Dev 手动发布")
    for section in (trigger, dev_dispatch):
        assert "人工操作规则" in section
        assert "不会因历史 GitHub-only 版本自动拒绝" in section


def test_release_document_dev_retract_noop_requires_confirmed_stable_root():
    release = _markdown_section(
        RELEASE_DOCUMENT.read_text(encoding="utf-8"), "### Dev 撤回"
    )
    assert "稳定目录 `mihari`" in release
    assert "unable to inspect release root" in release
    assert "缺少 `mihari-dev`" in release


def test_distribution_document_says_dev_retract_must_not_modify_stable_index():
    retract = _markdown_section(
        DISTRIBUTION_DOCUMENT.read_text(encoding="utf-8"), "## 五、版本撤回（致命错误）"
    )
    assert "不得修改" in retract
    assert "不得触碰稳定 `index.txt`" not in retract


def test_design_document_records_isolation_false_fail_and_logical_public_url():
    spec = DESIGN_DOCUMENT.read_text(encoding="utf-8")
    assert "foreign channel index changed during this mutation" in spec
    assert "重跑" in spec
    assert "{ALIST_URL}/p/public{path}" in spec
    assert "{ALIST_URL}/p/public{fs_path}" not in spec


def test_release_document_uses_main_dispatch_as_the_stable_runbook():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    workflow_table = _markdown_section(release, "## 工作流触发机制")
    runbook = _markdown_section(release, "## 发版流程")
    stable_dispatch = _markdown_section(release, "## 手动触发发布")

    assert "主路径：从 `main` 执行 `workflow_dispatch`" in workflow_table
    assert "`dev → main` 晋级 PR" in runbook
    assert "ref 选择 `main`" in runbook
    assert "`commit_sha`" in runbook
    assert "精确的 40 位" in runbook
    assert "不要把本地创建并推送 tag 作为当前稳定发版操作" in runbook
    assert "兼容入口仍接受 `v*` tag push" in stable_dispatch
    assert "不作为当前人工 runbook" in stable_dispatch


def test_stable_release_validates_changelog_before_build_and_publish():
    workflow = STABLE_WORKFLOW.read_text(encoding="utf-8")
    document = yaml.safe_load(workflow)
    resolve_steps = document["jobs"]["resolve"]["steps"]
    release_steps = document["jobs"]["release"]["steps"]

    resolve_source = next(
        index for index, step in enumerate(resolve_steps) if step.get("name") == "Resolve immutable source commit"
    )
    resolve_changelog = next(
        index for index, step in enumerate(resolve_steps) if step.get("name") == "Validate stable changelog"
    )
    release_gate = next(
        index for index, step in enumerate(release_steps) if step.get("name") == "Version gate (final)"
    )
    release_changelog = next(
        index for index, step in enumerate(release_steps) if step.get("name") == "Validate stable changelog"
    )
    release_tag = next(
        index
        for index, step in enumerate(release_steps)
        if step.get("name") == "Create or verify current stable tag"
    )

    assert resolve_source < resolve_changelog
    assert release_gate < release_changelog < release_tag
    assert resolve_steps[resolve_changelog]["if"] == "steps.gate.outputs.should_resolve == 'true'"
    for step in (resolve_steps[resolve_changelog], release_steps[release_changelog]):
        run = step["run"]
        assert "scripts/release/changelog_policy.py" in run
        assert "--changelog CHANGELOG.md" in run
        assert '--version "${VERSION}"' in run
        assert "echo" not in run
        assert "${VERSION}" not in run.replace('"${VERSION}"', "")


def test_changelog_check_workflow_gates_feature_prs_into_dev():
    workflow = CHANGELOG_CHECK_WORKFLOW.read_text(encoding="utf-8")
    document = yaml.safe_load(workflow)
    job = document["jobs"]["check"]
    policy = next(step for step in job["steps"] if step.get("name") == "Validate changelog ownership")

    assert document[True]["pull_request"]["branches"] == ["dev"]
    assert document["permissions"] == {"contents": "read"}
    assert "changelog_branch_policy.py" in policy["run"]
    assert policy["env"]["HEAD_REF"] == "${{ github.head_ref }}"
    assert "${{ github.head_ref }}" not in policy["run"]
    assert 'echo "${HEAD_REF}"' not in policy["run"]
    assert "--head-ref \"${HEAD_REF}\"" in policy["run"] or '--head-ref "${HEAD_REF}"' in policy["run"]
    assert "origin/main" in workflow
    assert "CHANGELOG.md" in workflow


def test_pr_source_check_allows_main_to_dev_sync_and_restricts_main_intake():
    document = yaml.safe_load(PR_SOURCE_CHECK_WORKFLOW.read_text(encoding="utf-8"))
    assert document[True]["pull_request"]["branches"] == ["main", "dev"]
    assert document["permissions"] == {"contents": "read"}

    step = document["jobs"]["check-source"]["steps"][0]
    condition = " ".join(str(step.get("if", "")).split())
    assert "github.base_ref == 'main'" in condition
    assert "github.head_ref != 'dev'" in condition
    assert "hotfix/" in condition
    assert "github.head_ref != 'main'" not in condition

    run = step["run"]
    assert "PR to main must come from 'dev' or 'hotfix/*'" in run
    assert "${{ github.head_ref }}" not in run
    assert step["env"]["HEAD_REF"] == "${{ github.head_ref }}"


def test_dev_release_does_not_require_changelog_gate():
    dev_release = WORKFLOW.read_text(encoding="utf-8")
    retract_dev = RETRACT_DEV_WORKFLOW.read_text(encoding="utf-8")
    retract_stable = STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8")

    for document in (dev_release, retract_dev, retract_stable):
        assert "changelog_policy.py" not in document
        assert "Validate stable changelog" not in document


def test_release_document_requires_changelog_gate_for_stable_only():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    runbook = _markdown_section(release, "## 发版流程")
    stable_dispatch = _markdown_section(release, "## 手动触发发布")
    checklist = _markdown_section(release, "## CI/CD 检查项")
    dev_dispatch = stable_dispatch[stable_dispatch.index("### Dev 手动发布") :]

    assert "CHANGELOG" in runbook
    assert "Unreleased" in runbook
    assert "fail closed" in runbook.lower() or "fail-closed" in runbook.lower() or "fail closed" in checklist.lower()
    assert "`release.yml`" in checklist or "release.yml" in checklist
    assert "CHANGELOG" in checklist
    assert "dev" in dev_dispatch.lower()
    assert "不要求" in dev_dispatch or "不必" in dev_dispatch or "不校验 CHANGELOG" in dev_dispatch
    assert "不得修改" in release
    assert "chore/release-" in release
    assert "### 1. 人手收口 CHANGELOG" in runbook
    assert "人工 PR" in runbook
    assert "不会修改 `CHANGELOG.md`" in runbook
    assert "不会创建 `chore/release" in runbook


def test_release_documents_scope_existing_asset_preflight_to_dev():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    reproducibility = _markdown_section(release, "## 可复现构建输入")
    distribution = DISTRIBUTION_DOCUMENT.read_text(encoding="utf-8")

    assert "仅 `release-dev.yml`" in reproducibility
    assert "dev workflow 会在 tag/asset mutation 前 fail closed" in reproducibility
    assert "`release.yml` 当前不提供同等的 existing-asset preflight" in reproducibility
    assert "不要从 dev 的 fail-closed preflight 推断 stable" in reproducibility
    assert "仅 dev release workflow" in distribution
    assert "stable release workflow 不提供同等 preflight" in distribution
