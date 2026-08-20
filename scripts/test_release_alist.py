import hashlib
import importlib.util
from pathlib import Path
from types import SimpleNamespace

import pytest

from alist_client import PLATFORMS, bundle_name


def load_module(name):
    path = Path(__file__).with_name(name + ".py")
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


release = load_module("release-alist")


class FakeAList:
    def __init__(self):
        self.files = {}
        self.dirs = set()
        self.uploaded = []

    def mkdir(self, path):
        self.dirs.add(path)

    def exists(self, path):
        return path in self.files or path in self.dirs

    def upload(self, local, remote):
        self.files[remote] = Path(local).read_bytes()
        self.uploaded.append(remote)

    def upload_text(self, text, remote):
        self.files[remote] = text.encode()
        self.uploaded.append(remote)

    def content(self, path):
        value = self.files.get(path)
        return value.decode() if value is not None else None

    def read_bytes(self, path, **kwargs):
        return self.files[path]

    def list_dir(self, path):
        prefix = path.rstrip("/") + "/"
        names = set()
        for item in list(self.files) + list(self.dirs):
            if item.startswith(prefix):
                rest = item[len(prefix):].split("/", 1)[0]
                names.add(rest)
        return [{"name": name, "is_dir": f"{prefix}{name}" in self.dirs or any(k.startswith(f"{prefix}{name}/") for k in self.files)} for name in names]

    def remove(self, base, names):
        for name in names:
            prefix = base.rstrip("/") + "/" + name
            self.files = {k: v for k, v in self.files.items() if not (k == prefix or k.startswith(prefix + "/"))}
            self.dirs = {k for k in self.dirs if not (k == prefix or k.startswith(prefix + "/"))}

    def public_url(self, path):
        return "https://example.invalid" + path


def make_dist(tmp_path):
    dist = tmp_path / "dist"
    dist.mkdir()
    for goos, goarch in PLATFORMS:
        (dist / bundle_name(goos, goarch)).write_bytes(f"{goos}-{goarch}".encode())
    return dist


def test_dev_requires_dev_path_version_and_sha(tmp_path):
    with pytest.raises(SystemExit):
        release.validate_inputs("v1.2.3", "dev", release.DEFAULT_BASE_PATH, None)
    with pytest.raises(SystemExit):
        release.validate_inputs("v1.2.3-dev.1", "dev", release.DEFAULT_BASE_PATH, "a" * 40)
    release.validate_inputs("v1.2.3-dev.1", "dev", "/mihari-release/mihari-dev", "a" * 40)


def test_new_stable_publish_requires_commit_sha(tmp_path):
    with pytest.raises(SystemExit):
        release.upload_version_dir(FakeAList(), make_dist(tmp_path), "/mihari-release/mihari", "v1.2.3", None, "stable")


def test_dev_upload_writes_buildinfo_before_complete_and_skips_root_scripts(tmp_path):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    path = release.upload_version_dir(alist, dist, "/mihari-release/mihari-dev", "v1.2.3-dev.1", "a" * 40, "dev")
    assert alist.uploaded[-2:] == [f"{path}/BUILDINFO", f"{path}/COMPLETE"]
    assert alist.files[f"{path}/BUILDINFO"] == b"version=v1.2.3-dev.1\ncommit=" + b"a" * 40 + b"\n"
    assert all("install-aio" not in item for item in alist.uploaded)


def test_existing_complete_conflict_is_refused(tmp_path):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    path = "/mihari-release/mihari-dev/v1.2.3-dev.1"
    alist.dirs.add(path)
    alist.files[f"{path}/COMPLETE"] = b"v1.2.3-dev.1\n"
    alist.files[f"{path}/BUILDINFO"] = b"version=v1.2.3-dev.1\ncommit=" + b"b" * 40 + b"\n"
    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, "/mihari-release/mihari-dev", "v1.2.3-dev.1", "a" * 40, "dev")


def test_same_version_same_sha_is_idempotent_only_after_byte_verification(tmp_path):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    base = "/mihari-release/mihari-dev"
    release.upload_version_dir(alist, dist, base, "v1.2.3-dev.1", "a" * 40, "dev")
    before = list(alist.uploaded)
    release.upload_version_dir(alist, dist, base, "v1.2.3-dev.1", "a" * 40, "dev")
    assert alist.uploaded == before
    alist.files[f"{base}/v1.2.3-dev.1/{bundle_name('linux', 'amd64')}"] = b"tampered"
    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, base, "v1.2.3-dev.1", "a" * 40, "dev")


def test_new_upload_refuses_tampered_remote_bytes_before_complete(tmp_path):
    class CorruptingAList(FakeAList):
        def upload(self, local, remote):
            super().upload(local, remote)
            if remote.endswith(bundle_name("linux", "amd64")):
                self.files[remote] = b"tampered"

    alist = CorruptingAList()
    base = "/mihari-release/mihari-dev"
    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, make_dist(tmp_path), base, "v1.2.3-dev.1", "a" * 40, "dev")
    assert f"{base}/v1.2.3-dev.1/COMPLETE" not in alist.files


def add_verified_complete(alist, base, version, commit="a" * 40):
    directory = f"{base}/{version}"
    alist.dirs.add(directory)
    alist.files[f"{directory}/COMPLETE"] = f"{version}\n".encode()
    alist.files[f"{directory}/BUILDINFO"] = f"version={version}\ncommit={commit}\n".encode()
    sums = []
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        content = name.encode()
        alist.files[f"{directory}/{name}"] = content
        sums.append(f"{hashlib.sha256(content).hexdigest()}  {name}")
    alist.files[f"{directory}/SHA256SUMS.txt"] = ("\n".join(sums) + "\n").encode()


def test_stable_accepts_legacy_complete_without_buildinfo_but_dev_rejects_it():
    alist = FakeAList()
    base = "/mihari-release/mihari"
    add_verified_complete(alist, base, "v1.2.3")
    del alist.files[f"{base}/v1.2.3/BUILDINFO"]

    assert release.verified_directory(alist, base, "v1.2.3", "stable") is not None
    assert release.verified_directory(alist, base, "v1.2.3", "dev") is None


def test_stable_accepts_complete_with_valid_buildinfo():
    alist = FakeAList()
    base = "/mihari-release/mihari"
    add_verified_complete(alist, base, "v1.2.3")

    assert release.verified_directory(alist, base, "v1.2.3", "stable") is not None


def test_lower_dev_version_is_rejected_from_highest_complete(tmp_path):
    alist = FakeAList()
    add_verified_complete(alist, "/mihari-release/mihari-dev", "v1.2.0-dev.2")
    alist.files["/mihari-release/mihari-dev/index.txt"] = b""
    with pytest.raises(SystemExit):
        release.ensure_monotonic_version(alist, "/mihari-release/mihari-dev", "v1.1.0-dev.1", "dev")


def test_dev_complete_with_empty_commit_is_not_a_monotonic_baseline():
    alist = FakeAList()
    add_verified_complete(alist, "/mihari-release/mihari-dev", "v9.0.0-dev.1", commit="")
    assert release.complete_versions(alist, "/mihari-release/mihari-dev", "dev") == []


def test_publish_rechecks_index_at_commit_point_and_preserves_newer_index(tmp_path):
    base = "/mihari-release/mihari-dev"
    alist = FakeAList()
    original_content = alist.content
    calls = {"index": 0}
    def content(path):
        if path == f"{base}/index.txt":
            calls["index"] += 1
            if calls["index"] == 4:
                add_verified_complete(alist, base, "v9.0.0-dev.1")
                alist.files[path] = b"latest v9.0.0-dev.1\n"
        return original_content(path)
    alist.content = content
    args = SimpleNamespace(version="v1.2.3-dev.1", dist_dir=make_dist(tmp_path), repo_root=tmp_path,
                           base_path=base, commit_sha="a" * 40, channel="dev", keep_versions=5)
    with pytest.raises(SystemExit):
        release.publish(alist, args)
    assert alist.files[f"{base}/index.txt"] == b"latest v9.0.0-dev.1\n"


def test_write_index_reliably_restores_on_readback_failure(tmp_path, monkeypatch):
    alist = FakeAList()
    alist.files["/mihari-release/mihari-dev/index.txt"] = b"latest v1.0.0-dev.1\n"
    original = alist.upload_text
    calls = {"n": 0}
    def upload(text, path):
        calls["n"] += 1
        if calls["n"] <= 2:
            alist.files[path] = b"corrupt"
        else:
            original(text, path)
    alist.upload_text = upload
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    with pytest.raises(SystemExit):
        release.write_index_reliably(alist, "/mihari-release/mihari-dev/index.txt", "latest v1.2.0-dev.1\n", "latest v1.0.0-dev.1\n", "dev")
    assert alist.files["/mihari-release/mihari-dev/index.txt"] == b"latest v1.0.0-dev.1\n"


def test_write_index_fails_closed_when_competitor_changes_body_during_upload(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    alist = FakeAList()
    path = "/mihari-release/mihari-dev/index.txt"
    alist.files[path] = b"latest v1.0.0-dev.1\n"
    original = alist.upload_text
    def competing_upload(text, remote):
        original(text, remote)
        if remote == path:
            alist.files[remote] = b"latest v9.0.0-dev.1\n"
    alist.upload_text = competing_upload
    with pytest.raises(SystemExit):
        release.write_index_reliably(alist, path, "latest v1.2.0-dev.1\n", "latest v1.0.0-dev.1\n", "dev")
    assert alist.files[path] == b"latest v9.0.0-dev.1\n"
