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
    "scripts/test_retract_alist.py -q"
)


def test_stable_release_and_retract_pin_the_stable_alist_channel():
    stable_release_workflow = STABLE_WORKFLOW.read_text(encoding="utf-8")
    stable_retract_workflow = STABLE_RETRACT_WORKFLOW.read_text(encoding="utf-8")

    assert "--channel stable" in stable_release_workflow
    assert '--commit-sha "${SHA}"' in stable_release_workflow
    assert "--channel stable" in stable_retract_workflow


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
        install_index = next(index for index, step in enumerate(steps) if step.get("name") == install_step_name)
        install = steps[install_index]["run"]

        assert checkout_index < install_index
        assert "python -m pip install --disable-pip-version-check -r scripts/requirements-release.txt" in install
        assert "pip install requests" not in install

    release_checkout = next(
        step for step in release["jobs"]["release"]["steps"] if step.get("uses") == "actions/checkout@v7"
    )
    assert release_checkout["with"]["ref"] == "${{ needs.resolve.outputs.sha }}"


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


def test_ci_runs_release_safety_suite_from_pinned_requirements_on_all_integration_branches():
    document = yaml.safe_load(CI_WORKFLOW.read_text(encoding="utf-8"))

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


def test_release_documents_describe_batch_a_as_code_prepared_and_keep_p2_alist_unavailable():
    release = RELEASE_DOCUMENT.read_text(encoding="utf-8")
    distribution = DISTRIBUTION_DOCUMENT.read_text(encoding="utf-8")

    for document in (release, distribution):
        assert "代码已准备，远程 dev/试发需授权" in document
        assert "P2 AList 发布与撤回 workflow 尚不可用" in document
        assert "publish-dev-alist.yml" not in document
        assert "retract-dev.yml" not in document

    assert "Actions 产物或 dev 版本目录" not in distribution
    assert "/mihari-release/mihari-dev" not in distribution
