import importlib.util
from pathlib import Path

import pytest
import hashlib
from alist_client import AListError, PLATFORMS, bundle_name
from test_alist_topology_fake import TopologyFake


def load_module():
    path = Path(__file__).with_name("retract-alist.py")
    spec = importlib.util.spec_from_file_location("retract_alist", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


retract_mod = load_module()


class Fake:
    def __init__(self):
        self.files = {}
        self.dirs = set()
        self.calls = []
        self.index_reads = []
        self.fail_target_remove = False

    def exists(self, p):
        self.calls.append(("exists", p))
        return p in self.files or p in self.dirs

    def read_bytes(self, p, **kwargs):
        self.calls.append(("read_bytes", p))
        return self.files[p]

    def content(self, p):
        self.calls.append(("content", p))
        if p.endswith("/index.txt") and self.index_reads:
            return self.index_reads.pop(0)
        b = self.files.get(p)
        return b.decode() if b is not None else None

    def list_dir(self, p):
        self.calls.append(("list_dir", p))
        pref = p.rstrip("/") + "/"
        names = {k[len(pref):].split("/", 1)[0] for k in self.files if k.startswith(pref)} | {k[len(pref):].split("/", 1)[0] for k in self.dirs if k.startswith(pref)}
        return [{"name": n, "is_dir": True} for n in names]

    def remove(self, p, names):
        self.calls.append(("remove", p, tuple(names)))
        if self.fail_target_remove and any(name.startswith("v") for name in names):
            raise OSError("https://alist.invalid/remove?token=remove-secret response-body")
        for n in names:
            pref = p.rstrip("/") + "/" + n
            self.files = {k: v for k, v in self.files.items() if not (k == pref or k.startswith(pref + "/"))}
            self.dirs = {k for k in self.dirs if not (k == pref or k.startswith(pref + "/"))}

    def upload_text(self, text, p):
        self.calls.append(("upload", p, text))
        self.files[p] = text.encode()

    def public_url(self, p): return "https://example.invalid" + p


def seed_storage_root(fake, *, has_dev_channel=True):
    # Old Fake lists "/" by first logical/fs segment. These two fs-shaped
    # siblings make storage_root_entries see mihari [+ mihari-dev]
    # without preloading the forbidden logical path /mihari-release.
    fake.dirs.add("/mihari")
    if has_dev_channel:
        fake.dirs.add("/mihari-dev")


def add_complete(fake, base, version):
    d = f"{base}/{version}"
    fake.dirs.add(d)
    fake.files[f"{d}/COMPLETE"] = (version + "\n").encode()
    fake.files[f"{d}/BUILDINFO"] = (f"version={version}\ncommit={'a'*40}\n").encode()
    sums = {}
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        payload = name.encode()
        fake.files[f"{d}/{name}"] = payload
        sums[name] = hashlib.sha256(payload).hexdigest()
    fake.files[f"{d}/SHA256SUMS.txt"] = "".join(
        f"{sums[name]}  {name}\n" for name in sorted(sums)
    ).encode()


def _add_topology_complete(fake, base, version):
    directory = f"{base}/{version}"
    fake.mkdir(directory)
    fake.upload_text(f"{version}\n", f"{directory}/COMPLETE")
    fake.upload_text(
        f"version={version}\ncommit={'a' * 40}\n", f"{directory}/BUILDINFO"
    )
    sums = {}
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        fake.upload_text(name, f"{directory}/{name}")
        sums[name] = hashlib.sha256(name.encode()).hexdigest()
    fake.upload_text(
        "".join(f"{sums[name]}  {name}\n" for name in sorted(sums)),
        f"{directory}/SHA256SUMS.txt",
    )


def root_index(base, version):
    return f"{base}/index.txt", f"latest {version}\n"


def mutations(fake):
    return [call for call in fake.calls if call[0] in {"upload", "remove"}]


def test_stable_retraction_accepts_legacy_complete_without_buildinfo_but_dev_rejects_it():
    fake = Fake()
    base = "/mihari-release/mihari"
    add_complete(fake, base, "v1.2.3")
    del fake.files[f"{base}/v1.2.3/BUILDINFO"]

    assert retract_mod.verified_directory(fake, base, "v1.2.3", "stable") is not None
    assert retract_mod.verified_directory(fake, base, "v1.2.3", "dev") is None


def test_stable_retraction_accepts_complete_with_valid_buildinfo():
    fake = Fake()
    base = "/mihari-release/mihari"
    add_complete(fake, base, "v1.2.3")

    assert retract_mod.verified_directory(fake, base, "v1.2.3", "stable") is not None


@pytest.mark.parametrize(
    "extra",
    [
        "unexpected=value\n",
        "commit=" + "a" * 40 + "\n",
    ],
)
def test_retraction_requires_exact_buildinfo_for_new_release_directories(extra):
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    add_complete(fake, base, version)
    path = f"{base}/{version}/BUILDINFO"
    fake.files[path] += extra.encode()

    assert retract_mod.verified_directory(fake, base, version, "dev", "a" * 40) is None


@pytest.mark.parametrize(
    "invalid_manifest",
    [
        pytest.param(lambda manifest: manifest + manifest.splitlines(keepends=True)[0], id="duplicate"),
        pytest.param(lambda manifest: manifest + "0" * 64 + "  unexpected-bundle\n", id="extra"),
        pytest.param(lambda manifest: manifest + "not-a-checksum-line\n", id="malformed"),
        pytest.param(lambda manifest: "".join(reversed(manifest.splitlines(keepends=True))), id="out-of-order"),
        pytest.param(lambda manifest: manifest.rstrip("\n"), id="missing-final-newline"),
        pytest.param(lambda manifest: manifest + "\n", id="blank-line"),
    ],
)
def test_retract_refuses_noncanonical_checksum_manifest_without_mutation(invalid_manifest):
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    version = "v1.2.3-dev.1"
    add_complete(fake, base, version)
    sums_path = f"{base}/{version}/SHA256SUMS.txt"
    fake.files[sums_path] = invalid_manifest(fake.files[sums_path].decode()).encode()
    fake.files[f"{base}/index.txt"] = f"latest {version}\n".encode()
    seed_storage_root(fake, has_dev_channel=True)

    with pytest.raises(SystemExit):
        retract_mod.retract(fake, base, version, "dev", "a" * 40)

    assert fake.exists(f"{base}/{version}")
    assert mutations(fake) == []


def test_dev_retract_rebuilds_highest_remaining_complete(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    add_complete(fake, base, "v1.0.0-dev.1")
    add_complete(fake, base, "v1.1.0-dev.1")
    fake.files[f"{base}/v1.0.0-dev.1/BUILDINFO"] = b"version=v1.0.0-dev.1\ncommit=" + b"a" * 40 + b"\n"
    fake.files[f"{base}/v1.1.0-dev.1/BUILDINFO"] = b"version=v1.1.0-dev.1\ncommit=" + b"a" * 40 + b"\n"
    fake.files[f"{base}/index.txt"] = b"latest v1.1.0-dev.1\n"
    seed_storage_root(fake, has_dev_channel=True)
    retract_mod.retract(fake, base, "v1.1.0-dev.1", "dev", "a" * 40)
    assert fake.files[f"{base}/index.txt"].startswith(b"latest v1.0.0-dev.1\n")


def test_retract_rejects_stable_path_for_dev():
    with pytest.raises(SystemExit):
        retract_mod.validate_inputs("v1.0.0-dev.1", "dev", "/mihari-release/mihari", "a" * 40)


def test_dev_retract_refuses_mismatched_identity(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    add_complete(fake, base, "v1.1.0-dev.1")
    seed_storage_root(fake, has_dev_channel=True)
    with pytest.raises(SystemExit):
        retract_mod.retract(fake, base, "v1.1.0-dev.1", "dev", "b" * 40)
    assert fake.exists(f"{base}/v1.1.0-dev.1")


def test_retract_missing_dev_directory_is_idempotent(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    seed_storage_root(fake, has_dev_channel=True)
    retract_mod.retract(fake, "/mihari-release/mihari-dev", "v1.1.0-dev.1", "dev", "a" * 40)

    assert fake.files == {}
    assert mutations(fake) == []
    assert "/mihari" in fake.dirs
    assert "/mihari-dev" in fake.dirs


def test_latest_retraction_switches_verified_index_before_removing_target(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.1.0-dev.1"
    replacement = "v1.0.0-dev.1"
    add_complete(fake, base, target)
    add_complete(fake, base, replacement)
    path, previous = root_index(base, target)
    fake.files[path] = previous.encode()
    seed_storage_root(fake, has_dev_channel=True)

    retract_mod.retract(fake, base, target, "dev", "a" * 40)

    target_remove = ("remove", base, (target,))
    index_upload = next(call for call in fake.calls if call[:2] == ("upload", path))
    assert fake.calls.index(index_upload) < fake.calls.index(target_remove)
    assert fake.files[path].startswith(f"latest {replacement}\n".encode())
    assert not fake.exists(f"{base}/{target}")


def test_latest_retraction_keeps_target_when_index_commit_fails(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.1.0-dev.1"
    add_complete(fake, base, target)
    add_complete(fake, base, "v1.0.0-dev.1")
    path, previous = root_index(base, target)
    fake.files[path] = previous.encode()
    seed_storage_root(fake, has_dev_channel=True)

    def fail_index_write(text, remote):
        fake.calls.append(("upload", remote, text))
        if remote == path:
            raise OSError("https://alist.invalid/index?token=index-secret response-body")
        fake.files[remote] = text.encode()

    fake.upload_text = fail_index_write
    with pytest.raises(SystemExit):
        retract_mod.retract(fake, base, target, "dev", "a" * 40)

    assert fake.exists(f"{base}/{target}")
    assert ("remove", base, (target,)) not in mutations(fake)
    assert fake.files[path] == previous.encode()


def test_remove_failure_after_index_commit_preserves_new_index_and_rerun_removes_orphan(tmp_path, monkeypatch, capsys):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.1.0-dev.1"
    replacement = "v1.0.0-dev.1"
    add_complete(fake, base, target)
    add_complete(fake, base, replacement)
    path, _ = root_index(base, target)
    fake.files[path] = f"latest {target}\n".encode()
    fake.fail_target_remove = True
    seed_storage_root(fake, has_dev_channel=True)

    with pytest.raises(SystemExit):
        retract_mod.retract(fake, base, target, "dev", "a" * 40)

    captured = capsys.readouterr()
    assert "remove-secret" not in captured.err
    assert "response-body" not in captured.err
    assert fake.files[path].startswith(f"latest {replacement}\n".encode())
    assert fake.exists(f"{base}/{target}")

    fake.fail_target_remove = False
    fake.calls.clear()
    retract_mod.retract(fake, base, target, "dev", "a" * 40)

    assert mutations(fake) == [("remove", base, (target,))]
    assert not fake.exists(f"{base}/{target}")
    assert fake.files[path].startswith(f"latest {replacement}\n".encode())


def test_last_latest_retraction_commits_empty_index_before_removing_target(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.1.0-dev.1"
    add_complete(fake, base, target)
    path, _ = root_index(base, target)
    fake.files[path] = f"latest {target}\n".encode()
    seed_storage_root(fake, has_dev_channel=True)

    retract_mod.retract(fake, base, target, "dev", "a" * 40)

    assert fake.files[path] == b""
    assert fake.calls.index(("upload", path, "")) < fake.calls.index(("remove", base, (target,)))


def test_latest_retraction_aborts_when_remaining_candidate_cannot_be_read(
    tmp_path, monkeypatch, capsys
):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.1.0-dev.1"
    remaining = "v1.0.0-dev.1"
    add_complete(fake, base, target)
    add_complete(fake, base, remaining)
    path, previous = root_index(base, target)
    fake.files[path] = previous.encode()
    remaining_buildinfo = f"{base}/{remaining}/BUILDINFO"
    seed_storage_root(fake, has_dev_channel=True)
    original_content = fake.content

    def content(remote):
        if remote == remaining_buildinfo:
            raise RuntimeError(
                "https://cloud.invalid/BUILDINFO?token=remaining-secret response-body"
            )
        return original_content(remote)

    fake.content = content

    with pytest.raises(SystemExit):
        retract_mod.retract(fake, base, target, "dev", "a" * 40)

    assert fake.files[path] == previous.encode()
    assert fake.exists(f"{base}/{target}")
    assert mutations(fake) == []
    captured = capsys.readouterr()
    assert "remaining-secret" not in captured.err
    assert "response-body" not in captured.err


def test_latest_retraction_aborts_on_malformed_directory_listing(
    tmp_path, monkeypatch
):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.1.0-dev.1"
    add_complete(fake, base, target)
    path, previous = root_index(base, target)
    fake.files[path] = previous.encode()
    seed_storage_root(fake, has_dev_channel=True)
    original = fake.list_dir
    fake.list_dir = (
        lambda path: original(path) if path == "/" else [{"name": 123, "is_dir": True}]
    )

    with pytest.raises(SystemExit):
        retract_mod.retract(fake, base, target, "dev", "a" * 40)

    assert fake.files[path] == previous.encode()
    assert fake.exists(f"{base}/{target}")
    assert mutations(fake) == []


def test_non_latest_retraction_leaves_index_bytes_unchanged(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.0.0-dev.1"
    latest = "v1.1.0-dev.1"
    add_complete(fake, base, target)
    add_complete(fake, base, latest)
    path = f"{base}/index.txt"
    original = f"latest {latest}\nlinux-amd64 https://example.invalid/a deadbeef\n".encode()
    fake.files[path] = original
    seed_storage_root(fake, has_dev_channel=True)

    retract_mod.retract(fake, base, target, "dev", "a" * 40)

    assert fake.files[path] == original
    assert mutations(fake) == [("remove", base, (target,))]


def test_non_latest_retraction_fails_closed_when_index_changes_concurrently(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.0.0-dev.1"
    add_complete(fake, base, target)
    path = f"{base}/index.txt"
    fake.files[path] = b"latest v1.1.0-dev.1\n"
    fake.index_reads = ["latest v1.1.0-dev.1\n", "latest v9.0.0-dev.1\n"]
    seed_storage_root(fake, has_dev_channel=True)

    with pytest.raises(SystemExit):
        retract_mod.retract(fake, base, target, "dev", "a" * 40)

    assert fake.exists(f"{base}/{target}")
    assert mutations(fake) == []


def test_missing_target_referenced_by_index_fails_closed(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.1.0-dev.1"
    path, previous = root_index(base, target)
    fake.files[path] = previous.encode()
    seed_storage_root(fake, has_dev_channel=True)

    with pytest.raises(SystemExit):
        retract_mod.retract(fake, base, target, "dev", "a" * 40)

    assert mutations(fake) == []


def test_missing_target_not_referenced_by_index_is_idempotent(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    target = "v1.1.0-dev.1"
    path = f"{base}/index.txt"
    fake.files[path] = b"latest v1.2.0-dev.1\n"
    seed_storage_root(fake, has_dev_channel=True)

    retract_mod.retract(fake, base, target, "dev", "a" * 40)

    assert fake.files[path] == b"latest v1.2.0-dev.1\n"
    assert mutations(fake) == []


def test_dev_retract_noops_when_storage_root_has_mihari_but_no_dev_channel():
    fake = TopologyFake()
    retract_mod.retract(fake, "/mihari-release/mihari-dev", "v1.0.0-dev.1", "dev", "a" * 40)
    assert not any(
        op in {"remove", "upload", "upload_text", "mkdir"} for op, _, _ in fake.ops
    )
    assert fake.files == {}
    assert fake.dirs == {"/", "/mihari"}
    assert not any(fs == "/mihari-release" for _, _, fs in fake.ops)


def test_dev_retract_fails_closed_when_storage_root_lacks_mihari():
    fake = TopologyFake()
    fake.dirs.discard("/mihari")
    with pytest.raises(SystemExit):
        retract_mod.retract(fake, "/mihari-release/mihari-dev", "v1.0.0-dev.1", "dev", "a" * 40)
    assert not any(op in {"remove", "upload", "upload_text", "mkdir"} for op, _, _ in fake.ops)


def test_dev_retract_fails_closed_when_storage_root_list_raises():
    fake = TopologyFake()
    original = fake.list_dir

    def list_dir(path):
        if path == "/":
            raise AListError("invalid directory listing")
        return original(path)

    fake.list_dir = list_dir
    with pytest.raises(SystemExit):
        retract_mod.retract(fake, "/mihari-release/mihari-dev", "v1.0.0-dev.1", "dev", "a" * 40)
    assert not any(op in {"remove", "upload", "upload_text"} for op, _, _ in fake.ops)


def test_dev_retract_uses_existing_logic_when_channel_root_exists(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = TopologyFake()
    fake.dirs.add("/mihari-dev")
    _add_topology_complete(fake, "/mihari-release/mihari-dev", "v1.0.0-dev.1")
    _add_topology_complete(fake, "/mihari-release/mihari-dev", "v1.1.0-dev.1")
    fake.upload_text("latest v1.1.0-dev.1\n", "/mihari-release/mihari-dev/index.txt")
    retract_mod.retract(
        fake, "/mihari-release/mihari-dev", "v1.1.0-dev.1", "dev", "a" * 40
    )
    body = fake.content("/mihari-release/mihari-dev/index.txt")
    assert body is not None and body.startswith("latest v1.0.0-dev.1\n")
    assert not fake.exists("/mihari-release/mihari-dev/v1.1.0-dev.1")
    assert not any(fs == "/mihari-release" for _, _, fs in fake.ops)
