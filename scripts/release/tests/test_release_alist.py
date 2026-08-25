import hashlib
import importlib.util
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest
import requests

from alist_client import AList, AListError, PLATFORMS, bundle_name
from test_alist_topology_fake import TopologyFake


def load_module(name):
    path = Path(__file__).parent.parent / (name + ".py")
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


def seed_storage_root(fake, *, has_dev_channel=True):
    # Old Fake lists "/" by first logical/fs segment. These two fs-shaped
    # siblings make storage_root_entries see mihari [+ mihari-dev]
    # without preloading the forbidden logical path /mihari-release.
    fake.dirs.add("/mihari")
    if has_dev_channel:
        fake.dirs.add("/mihari-dev")


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


def test_stable_publish_inputs_require_a_40_lowercase_commit_sha():
    base = "/mihari-release/mihari"
    with pytest.raises(SystemExit):
        release.validate_inputs("v1.2.3", "stable", base, None)
    with pytest.raises(SystemExit):
        release.validate_inputs("v1.2.3", "stable", base, "A" * 40)
    release.validate_inputs("v1.2.3", "stable", base, "a" * 40)


def test_dev_upload_writes_buildinfo_before_complete(tmp_path):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    path = release.upload_version_dir(alist, dist, "/mihari-release/mihari-dev", "v1.2.3-dev.1", "a" * 40, "dev")
    assert alist.uploaded[-2:] == [f"{path}/BUILDINFO", f"{path}/COMPLETE"]
    assert alist.files[f"{path}/BUILDINFO"] == b"version=v1.2.3-dev.1\ncommit=" + b"a" * 40 + b"\n"


def incomplete_version_dir(base, version):
    return f"{base}/{version}"


def expected_partial_manifest(dist):
    sums = {}
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        sums[name] = hashlib.sha256((dist / name).read_bytes()).hexdigest()
    return "".join(f"{sums[name]}  {name}\n" for name in sorted(sums)).encode()


def test_incomplete_directory_conflicting_bundle_is_refused_without_mutation(tmp_path):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    directory = incomplete_version_dir(base, version)
    conflicting_bundle = bundle_name("linux", "amd64")
    alist.dirs.add(directory)
    alist.files[f"{directory}/{conflicting_bundle}"] = b"conflicting bundle bytes"
    before_files = dict(alist.files)
    before_dirs = set(alist.dirs)
    before_uploads = list(alist.uploaded)

    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")

    assert alist.files == before_files
    assert alist.dirs == before_dirs
    assert alist.uploaded == before_uploads


@pytest.mark.parametrize(
    "name,content",
    [
        ("BUILDINFO", b"version=v1.2.3-dev.1\ncommit=" + b"b" * 40 + b"\n"),
        ("SHA256SUMS.txt", b"not-a-canonical-checksum-manifest\n"),
    ],
)
def test_incomplete_directory_conflicting_identity_metadata_is_refused_before_writes(tmp_path, name, content):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    directory = incomplete_version_dir(base, version)
    alist.dirs.add(directory)
    alist.files[f"{directory}/{name}"] = content
    before_files = dict(alist.files)
    before_uploads = list(alist.uploaded)

    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")

    assert alist.files == before_files
    assert alist.uploaded == before_uploads


def test_incomplete_directory_recovers_identical_objects_by_uploading_only_missing_files(tmp_path):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    directory = incomplete_version_dir(base, version)
    preserved_bundle = bundle_name("linux", "amd64")
    alist.dirs.add(directory)
    alist.files[f"{directory}/{preserved_bundle}"] = (dist / preserved_bundle).read_bytes()
    alist.files[f"{directory}/SHA256SUMS.txt"] = expected_partial_manifest(dist)
    alist.files[f"{directory}/BUILDINFO"] = b"version=v1.2.3-dev.1\ncommit=" + b"a" * 40 + b"\n"

    release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")

    assert f"{directory}/{preserved_bundle}" not in alist.uploaded
    assert f"{directory}/SHA256SUMS.txt" not in alist.uploaded
    assert f"{directory}/BUILDINFO" not in alist.uploaded
    assert alist.uploaded[-1] == f"{directory}/COMPLETE"
    assert set(alist.files) == {
        f"{directory}/{bundle_name(goos, goarch)}" for goos, goarch in PLATFORMS
    } | {f"{directory}/SHA256SUMS.txt", f"{directory}/BUILDINFO", f"{directory}/COMPLETE"}


def test_incomplete_directory_extra_object_is_refused_without_mutation(tmp_path):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    directory = incomplete_version_dir(base, version)
    alist.dirs.add(directory)
    alist.files[f"{directory}/unexpected-metadata.txt"] = b"unexpected"
    before_files = dict(alist.files)
    before_dirs = set(alist.dirs)

    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")

    assert alist.files == before_files
    assert alist.dirs == before_dirs


def test_incomplete_directory_listing_error_fails_closed_without_leaking_remote_details(tmp_path, capsys):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    alist.dirs.add("/mihari-release/mihari-dev/v1.2.3-dev.1")
    alist.list_dir = lambda _path: (_ for _ in ()).throw(
        RuntimeError("https://cloud.invalid/list?token=partial-secret response-body")
    )

    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, "/mihari-release/mihari-dev", "v1.2.3-dev.1", "a" * 40, "dev")

    captured = capsys.readouterr()
    assert "partial-secret" not in captured.err
    assert "response-body" not in captured.err


def test_existing_complete_conflict_is_refused(tmp_path):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    path = "/mihari-release/mihari-dev/v1.2.3-dev.1"
    alist.dirs.add(path)
    alist.files[f"{path}/COMPLETE"] = b"v1.2.3-dev.1\n"
    alist.files[f"{path}/BUILDINFO"] = b"version=v1.2.3-dev.1\ncommit=" + b"b" * 40 + b"\n"
    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, "/mihari-release/mihari-dev", "v1.2.3-dev.1", "a" * 40, "dev")


def test_existing_complete_with_same_sha_but_different_checksums_is_not_overwritten(tmp_path):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")
    completed = f"{base}/{version}"
    original_sums = alist.files[f"{completed}/SHA256SUMS.txt"]
    (dist / bundle_name("linux", "amd64")).write_bytes(b"different-release-artifact")

    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")

    assert alist.files[f"{completed}/SHA256SUMS.txt"] == original_sums


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


def test_completed_directory_read_error_fails_closed_without_leaking_remote_details(tmp_path, capsys):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    base = "/mihari-release/mihari-dev"
    release.upload_version_dir(alist, dist, base, "v1.2.3-dev.1", "a" * 40, "dev")
    alist.read_bytes = lambda _path, **_kwargs: (_ for _ in ()).throw(
        RuntimeError("https://cloud.invalid/p/file?token=top-secret response-body")
    )

    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, base, "v1.2.3-dev.1", "a" * 40, "dev")

    captured = capsys.readouterr()
    assert "top-secret" not in captured.err
    assert "response-body" not in captured.err


def test_completed_directory_metadata_read_error_fails_closed_without_leaking_details(tmp_path, capsys):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")
    complete = f"{base}/{version}/COMPLETE"
    original_content = alist.content

    def content(path):
        if path == complete:
            raise RuntimeError("https://cloud.invalid/p/COMPLETE?token=metadata-secret response-body")
        return original_content(path)

    alist.content = content
    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")

    captured = capsys.readouterr()
    assert "metadata-secret" not in captured.err
    assert "response-body" not in captured.err


@pytest.mark.parametrize(
    "error",
    [
        requests.HTTPError("https://cloud.invalid/SHA256SUMS?token=http-secret"),
        ValueError("remote object exceeds 1048576 bytes"),
    ],
)
def test_completed_directory_checksum_metadata_http_or_size_error_fails_closed(tmp_path, error):
    alist = FakeAList()
    dist = make_dist(tmp_path)
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")
    sums_path = f"{base}/{version}/SHA256SUMS.txt"
    original_content = alist.content

    def content(path):
        if path == sums_path:
            raise error
        return original_content(path)

    alist.content = content
    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, base, version, "a" * 40, "dev")


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
    sums = {}
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        content = name.encode()
        alist.files[f"{directory}/{name}"] = content
        sums[name] = hashlib.sha256(content).hexdigest()
    alist.files[f"{directory}/SHA256SUMS.txt"] = "".join(
        f"{sums[name]}  {name}\n" for name in sorted(sums)
    ).encode()


def test_stable_accepts_legacy_complete_without_buildinfo_but_dev_rejects_it():
    alist = FakeAList()
    base = "/mihari-release/mihari"
    add_verified_complete(alist, base, "v1.2.3")
    del alist.files[f"{base}/v1.2.3/BUILDINFO"]

    assert release.verified_directory(alist, base, "v1.2.3", "stable") is not None
    assert release.verified_directory(alist, base, "v1.2.3", "dev") is None


def test_legacy_stable_complete_is_read_only_and_cannot_be_republished(tmp_path):
    alist = FakeAList()
    base = "/mihari-release/mihari"
    add_verified_complete(alist, base, "v1.2.3")
    del alist.files[f"{base}/v1.2.3/BUILDINFO"]
    dist = tmp_path / "matching-dist"
    dist.mkdir()
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        (dist / name).write_bytes(name.encode())

    with pytest.raises(SystemExit):
        release.upload_version_dir(alist, dist, base, "v1.2.3", "a" * 40, "stable")


def test_stable_accepts_complete_with_valid_buildinfo():
    alist = FakeAList()
    base = "/mihari-release/mihari"
    add_verified_complete(alist, base, "v1.2.3")

    assert release.verified_directory(alist, base, "v1.2.3", "stable") is not None


@pytest.mark.parametrize(
    "path_suffix,extra",
    [
        ("BUILDINFO", "commit=" + "a" * 40 + "\n"),
        ("BUILDINFO", "unexpected=value\n"),
        ("SHA256SUMS.txt", "not-a-checksum-line\n"),
        ("SHA256SUMS.txt", "0" * 64 + "  mihari-all-in-one-linux-amd64.tar.gz\n"),
    ],
)
def test_completed_directory_rejects_noncanonical_identity_or_checksum_manifest(path_suffix, extra):
    alist = FakeAList()
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    add_verified_complete(alist, base, version)
    path = f"{base}/{version}/{path_suffix}"
    alist.files[path] += extra.encode()

    assert release.verified_directory(alist, base, version, "dev") is None


def test_completed_directory_rejects_duplicate_checksum_manifest_entry():
    alist = FakeAList()
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    add_verified_complete(alist, base, version)
    path = f"{base}/{version}/SHA256SUMS.txt"
    alist.files[path] += alist.files[path].splitlines(keepends=True)[0]

    assert release.verified_directory(alist, base, version, "dev") is None


def test_lower_dev_version_is_rejected_from_highest_complete(tmp_path):
    alist = FakeAList()
    add_verified_complete(alist, "/mihari-release/mihari-dev", "v1.2.0-dev.2")
    alist.files["/mihari-release/mihari-dev/index.txt"] = b""
    with pytest.raises(SystemExit):
        release.ensure_monotonic_version(alist, "/mihari-release/mihari-dev", "v1.1.0-dev.1", "dev")


def test_candidate_metadata_read_error_blocks_monotonic_publish_without_leaking_remote_details(
    capsys,
):
    alist = FakeAList()
    base = "/mihari-release/mihari-dev"
    add_verified_complete(alist, base, "v9.0.0-dev.1")
    buildinfo_path = f"{base}/v9.0.0-dev.1/BUILDINFO"
    original_content = alist.content

    def content(path):
        if path == buildinfo_path:
            raise RuntimeError("https://cloud.invalid/BUILDINFO?token=candidate-secret body")
        return original_content(path)

    alist.content = content
    before_files = dict(alist.files)
    before_dirs = set(alist.dirs)
    before_uploaded = list(alist.uploaded)

    with pytest.raises(SystemExit):
        release.ensure_monotonic_version(alist, base, "v1.0.0-dev.1", "dev")

    assert alist.files == before_files
    assert alist.dirs == before_dirs
    assert alist.uploaded == before_uploaded
    captured = capsys.readouterr()
    assert "candidate-secret" not in captured.err
    assert "body" not in captured.err


def test_index_read_error_fails_closed_without_leaking_remote_details(capsys):
    alist = FakeAList()
    base = "/mihari-release/mihari-dev"
    alist.content = lambda _path: (_ for _ in ()).throw(
        RuntimeError("https://cloud.invalid/index?token=index-secret body")
    )

    with pytest.raises(SystemExit):
        release.ensure_monotonic_version(alist, base, "v1.2.3-dev.1", "dev")

    captured = capsys.readouterr()
    assert "index-secret" not in captured.err
    assert "body" not in captured.err


def test_candidate_list_error_fails_closed_without_leaking_remote_details(capsys):
    alist = FakeAList()
    base = "/mihari-release/mihari-dev"
    alist.list_dir = lambda _path: (_ for _ in ()).throw(
        RuntimeError("https://cloud.invalid/list?token=list-secret body")
    )

    with pytest.raises(SystemExit):
        release.ensure_monotonic_version(alist, base, "v1.2.3-dev.1", "dev")

    captured = capsys.readouterr()
    assert "list-secret" not in captured.err
    assert "body" not in captured.err


def test_monotonic_scan_includes_highest_version_from_second_listing_page():
    alist = FakeAList()
    base = "/mihari-release/mihari-dev"
    lower = "v1.0.0-dev.1"
    highest = "v9.0.0-dev.1"
    add_verified_complete(alist, base, lower)
    add_verified_complete(alist, base, highest)
    first_page = [{"name": lower, "is_dir": True}] + [
        {"name": f"notes-{index}", "is_dir": True} for index in range(199)
    ]

    class Response:
        def __init__(self, content, page, has_more):
            self.body = {
                "code": 200,
                "data": {
                    "content": content,
                    "total": 201,
                    "page": page,
                    "per_page": 200,
                    "has_more": has_more,
                    "pages_total": 2,
                },
            }

        def raise_for_status(self):
            pass

        def json(self):
            return self.body

    class Session:
        def __init__(self):
            self.responses = [
                Response(first_page, 1, True),
                Response([{"name": highest, "is_dir": True}], 2, False),
            ]

        def post(self, *_args, **_kwargs):
            return self.responses.pop(0)

    client = AList.__new__(AList)
    client.base = "https://cloud.invalid"
    client.session = Session()
    alist.list_dir = client.list_dir

    with pytest.raises(SystemExit):
        release.ensure_monotonic_version(alist, base, "v2.0.0-dev.1", "dev")


def test_version_shaped_entry_with_malformed_directory_flag_blocks_scan():
    alist = FakeAList()
    base = "/mihari-release/mihari-dev"
    highest = "v9.0.0-dev.1"
    add_verified_complete(alist, base, highest)
    alist.list_dir = lambda _path: [{"name": highest, "is_dir": None}]

    with pytest.raises(SystemExit):
        release.ensure_monotonic_version(alist, base, "v1.0.0-dev.1", "dev")


def test_dev_retention_never_removes_stable_directories():
    alist = FakeAList()
    stable = "/mihari-release/mihari"
    dev = "/mihari-release/mihari-dev"
    add_verified_complete(alist, stable, "v9.0.0")
    add_verified_complete(alist, dev, "v1.0.0-dev.1")
    add_verified_complete(alist, dev, "v1.1.0-dev.1")

    release.prune_versions(alist, dev, "v1.1.0-dev.1", 1, "dev")

    assert f"{stable}/v9.0.0/COMPLETE" in alist.files
    assert f"{dev}/v1.0.0-dev.1/COMPLETE" not in alist.files


def test_retention_removes_incomplete_channel_version_directories():
    alist = FakeAList()
    base = "/mihari-release/mihari-dev"
    incomplete = f"{base}/v1.0.0-dev.1"
    alist.dirs.add(incomplete)
    add_verified_complete(alist, base, "v1.1.0-dev.1")

    release.prune_versions(alist, base, "v1.1.0-dev.1", 5, "dev")

    assert incomplete not in alist.dirs


def test_post_index_retention_list_failure_skips_safely_without_leaking_details(tmp_path, capsys):
    class LateListFailureAList(FakeAList):
        list_calls = 0

        def list_dir(self, path):
            if path == "/":
                return super().list_dir(path)
            self.list_calls += 1
            if self.list_calls > 2:
                raise RuntimeError("https://cloud.invalid/list?token=retention-secret body")
            return super().list_dir(path)

    alist = LateListFailureAList()
    seed_storage_root(alist)
    base = "/mihari-release/mihari-dev"
    args = SimpleNamespace(
        version="v1.2.3-dev.1", dist_dir=make_dist(tmp_path), repo_root=tmp_path,
        base_path=base, commit_sha="a" * 40, channel="dev", keep_versions=5,
    )

    release.publish(alist, args)

    assert f"{base}/index.txt" in alist.files
    captured = capsys.readouterr()
    assert "retention-secret" not in captured.out
    assert "body" not in captured.out


def test_publish_dev_bootstraps_missing_channel_root(tmp_path):
    alist = TopologyFake()
    args = SimpleNamespace(
        version="v1.2.3-dev.1",
        dist_dir=make_dist(tmp_path),
        repo_root=tmp_path,
        base_path="/mihari-release/mihari-dev",
        commit_sha="a" * 40,
        channel="dev",
        keep_versions=5,
    )
    release.publish(alist, args)
    assert any(
        op == "mkdir" and logical == "/mihari-release/mihari-dev" and fs == "/mihari-dev"
        for op, logical, fs in alist.ops
    )
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)
    index = f"{args.base_path}/index.txt"
    assert alist.content(index) is not None
    assert "latest v1.2.3-dev.1" in alist.content(index)
    assert index not in alist.files


def test_ensure_channel_root_fails_closed_without_mihari_sibling(tmp_path, capsys):
    alist = TopologyFake()
    alist.dirs.discard("/mihari")
    args = SimpleNamespace(
        version="v1.2.3-dev.1",
        dist_dir=make_dist(tmp_path),
        repo_root=tmp_path,
        base_path="/mihari-release/mihari-dev",
        commit_sha="a" * 40,
        channel="dev",
        keep_versions=5,
    )
    with pytest.raises(SystemExit):
        release.publish(alist, args)
    captured = capsys.readouterr()
    assert "unable to inspect release root" in captured.err
    assert any(op == "list_dir" and logical == "/" for op, logical, _ in alist.ops)
    assert not any(op == "mkdir" for op, _, _ in alist.ops)
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)


def test_ensure_channel_root_is_noop_when_mihari_dev_already_a_directory(tmp_path):
    alist = TopologyFake()
    alist.dirs.add("/mihari-dev")
    release.ensure_channel_root(alist, "/mihari-release/mihari-dev", "dev")
    assert not any(op == "mkdir" for op, _, _ in alist.ops)
    args = SimpleNamespace(
        version="v1.2.3-dev.1",
        dist_dir=make_dist(tmp_path),
        repo_root=tmp_path,
        base_path="/mihari-release/mihari-dev",
        commit_sha="a" * 40,
        channel="dev",
        keep_versions=5,
    )
    release.publish(alist, args)
    assert not any(
        op == "mkdir" and logical == "/mihari-release/mihari-dev"
        for op, logical, _ in alist.ops
    )
    assert alist.content(f"{args.base_path}/index.txt") is not None
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)


def test_ensure_channel_root_fails_when_mihari_dev_is_not_a_directory(tmp_path, capsys):
    alist = TopologyFake()
    alist.files["/mihari-dev"] = b"not-a-directory"
    args = SimpleNamespace(
        version="v1.2.3-dev.1",
        dist_dir=make_dist(tmp_path),
        repo_root=tmp_path,
        base_path="/mihari-release/mihari-dev",
        commit_sha="a" * 40,
        channel="dev",
        keep_versions=5,
    )
    with pytest.raises(SystemExit):
        release.publish(alist, args)
    captured = capsys.readouterr()
    assert "channel base path is not a directory" in captured.err
    assert not any(op == "mkdir" for op, _, _ in alist.ops)
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)


def test_stable_publish_does_not_list_storage_root_or_mkdir_dev(tmp_path):
    alist = TopologyFake()
    repo_root = tmp_path / "repo"
    write_root_installers(repo_root)
    args = SimpleNamespace(
        version="v1.2.3",
        dist_dir=make_dist(tmp_path),
        repo_root=repo_root,
        base_path="/mihari-release/mihari",
        commit_sha="a" * 40,
        channel="stable",
        keep_versions=5,
    )
    release.publish(alist, args)
    assert not any(op == "list_dir" and logical == "/" for op, logical, _ in alist.ops)
    assert not any(op == "mkdir" and "mihari-dev" in logical for op, logical, _ in alist.ops)
    assert not any(fs == "/mihari-release" for _, _, fs in alist.ops)
    assert alist.content("/mihari-release/mihari/index.txt") is not None
    assert "/mihari-release/mihari/index.txt" not in alist.files


def test_ensure_channel_root_skips_stable_without_listing():
    alist = TopologyFake()
    release.ensure_channel_root(alist, "/mihari-release/mihari", "stable")
    assert alist.ops == []


def test_publish_dev_never_uploads_root_installers(tmp_path):
    alist = FakeAList()
    seed_storage_root(alist)
    repo_root = tmp_path / "repo"
    write_root_installers(repo_root)
    base = "/mihari-release/mihari-dev"
    args = SimpleNamespace(
        version="v1.2.3-dev.1", dist_dir=make_dist(tmp_path), repo_root=repo_root,
        base_path=base, commit_sha="a" * 40, channel="dev", keep_versions=5,
    )

    release.publish(alist, args)

    assert f"{base}/install-aio-remote.sh" not in alist.files
    assert f"{base}/install-aio-remote.ps1" not in alist.files


def test_dev_complete_with_empty_commit_is_not_a_monotonic_baseline():
    alist = FakeAList()
    add_verified_complete(alist, "/mihari-release/mihari-dev", "v9.0.0-dev.1", commit="")
    assert release.complete_versions(alist, "/mihari-release/mihari-dev", "dev") == []


def test_publish_rechecks_index_at_commit_point_and_preserves_newer_index(tmp_path):
    base = "/mihari-release/mihari-dev"
    alist = FakeAList()
    seed_storage_root(alist)
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


def test_write_index_reliably_preserves_unknown_readback_for_manual_recovery(tmp_path, monkeypatch):
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
    assert alist.files["/mihari-release/mihari-dev/index.txt"] == b"corrupt"
    assert calls["n"] == 1
    assert (
        tmp_path / "mihari-index-backup" / "dev" / "index.txt"
    ).read_bytes() == b"latest v1.0.0-dev.1\n"


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
    assert alist.uploaded.count(path) == 1


def test_index_writer_read_error_fails_closed_without_leaking_remote_details(capsys):
    alist = FakeAList()
    alist.content = lambda _path: (_ for _ in ()).throw(
        RuntimeError("https://cloud.invalid/index?token=index-write-secret body")
    )

    with pytest.raises(SystemExit):
        release.write_index_reliably(
            alist,
            "/mihari-release/mihari-dev/index.txt",
            "latest v1.2.0-dev.1\n",
            "latest v1.0.0-dev.1\n",
            "dev",
        )

    captured = capsys.readouterr()
    assert "index-write-secret" not in captured.err
    assert "body" not in captured.err


def write_root_installers(repo_root):
    install_dir = repo_root / "scripts" / "install"
    install_dir.mkdir(parents=True)
    for filename in ("install-aio-remote.sh", "install-aio-remote.ps1"):
        (install_dir / filename).write_text(filename, encoding="utf-8")


def test_publish_uploads_stable_root_installers_only_after_index_success(tmp_path):
    alist = FakeAList()
    repo_root = tmp_path / "repo"
    write_root_installers(repo_root)
    base = "/mihari-release/mihari"
    args = SimpleNamespace(
        version="v1.2.3", dist_dir=make_dist(tmp_path), repo_root=repo_root,
        base_path=base, commit_sha="a" * 40, channel="stable", keep_versions=5,
    )

    release.publish(alist, args)

    index = f"{base}/index.txt"
    assert alist.uploaded.index(index) < alist.uploaded.index(f"{base}/install-aio-remote.sh")
    assert f"{base}/install-aio-remote.ps1" in alist.files


def test_index_failure_skips_stable_root_installers_and_retention(tmp_path, monkeypatch):
    alist = FakeAList()
    repo_root = tmp_path / "repo"
    write_root_installers(repo_root)
    base = "/mihari-release/mihari"
    add_verified_complete(alist, base, "v1.0.0")
    args = SimpleNamespace(
        version="v1.2.3", dist_dir=make_dist(tmp_path), repo_root=repo_root,
        base_path=base, commit_sha="a" * 40, channel="stable", keep_versions=1,
    )
    monkeypatch.setattr(release, "write_index_reliably", lambda *_args: release.fail("index unavailable"))

    with pytest.raises(SystemExit):
        release.publish(alist, args)

    assert f"{base}/install-aio-remote.sh" not in alist.files
    assert f"{base}/v1.0.0/COMPLETE" in alist.files


def test_main_translates_upload_client_error_without_leaking_remote_details(
    tmp_path, monkeypatch, capsys
):
    class UploadFailureAList(FakeAList):
        def upload(self, _local, _remote):
            raise AListError(
                "https://cloud.invalid/upload?token=upload-secret response-body"
            )

    dist = make_dist(tmp_path)
    repo_root = tmp_path / "repo"
    write_root_installers(repo_root)
    monkeypatch.setattr(release, "connect", lambda: UploadFailureAList())
    monkeypatch.setattr(
        sys,
        "argv",
        [
            "release-alist.py",
            "--version",
            "v1.2.3",
            "--dist-dir",
            str(dist),
            "--repo-root",
            str(repo_root),
            "--base-path",
            "/mihari-release/mihari",
            "--commit-sha",
            "a" * 40,
        ],
    )

    with pytest.raises(SystemExit):
        release.main()

    captured = capsys.readouterr()
    assert "upload-secret" not in captured.err
    assert "response-body" not in captured.err
