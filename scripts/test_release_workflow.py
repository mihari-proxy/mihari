"""Static regression checks for GitHub release workflow metadata handling."""

from pathlib import Path


WORKFLOW = Path(__file__).resolve().parents[1] / ".github" / "workflows" / "release-dev.yml"


def test_release_workflow_uses_policy_instead_of_write_only_get_fields():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert ".make_latest" not in workflow
    assert ".target_commitish == $sha" not in workflow
    assert "github_release_policy.py release" in workflow
    assert "github_release_policy.py tag-chain" in workflow
    assert "github_release_policy.py latest" in workflow
    assert "-F make_latest=false" in workflow


def test_dev_release_identity_is_explicit_and_verified_on_reuse():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    assert "DEV_RELEASE_NAME: Mihari ${{ inputs.version }} (dev)" in workflow
    assert "<!-- mihari-dev-release -->" in workflow
    assert '-f "name=${DEV_RELEASE_NAME}"' in workflow
    assert '-f "body=${DEV_RELEASE_BODY}"' in workflow


def test_existing_release_is_preflighted_before_tag_mutation():
    workflow = WORKFLOW.read_text(encoding="utf-8")

    preflight = workflow.index("Preflight existing release before tag mutation")
    tag_mutation = workflow.index("Create or verify immutable tag")
    assert preflight < tag_mutation
    assert 'conflicts with the requested dev release' in workflow
