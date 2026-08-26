import hashlib
import importlib.util
from pathlib import Path

import pytest

from alist_client import PLATFORMS, bundle_name


def load_module(name):
    path = Path(__file__).parent.parent / (name + ".py")
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


regenerate = load_module("regenerate-index")


class FakeAList:
    def __init__(self):
        self.files = {}
        self.dirs = set()
        self.uploads = []

    def exists(self, path):
        return path in self.files or path in self.dirs

    def content(self, path):
        value = self.files.get(path)
        return value.decode() if value is not None else None

    def read_bytes(self, path, **_kwargs):
        return self.files[path]

    def list_dir(self, path):
        prefix = path.rstrip("/") + "/"
        names = {
            item[len(prefix) :].split("/", 1)[0]
            for item in list(self.files) + list(self.dirs)
            if item.startswith(prefix)
        }
        return [
            {
                "name": name,
                "is_dir": f"{prefix}{name}" in self.dirs,
            }
            for name in names
        ]

    def upload_text(self, text, path):
        self.uploads.append((path, text))
        self.files[path] = text.encode()

    def remove(self, base, names):
        for name in names:
            self.files.pop(f"{base.rstrip('/')}/{name}", None)

    def public_url(self, path):
        return "https://example.invalid" + path


def add_complete(fake, base, version):
    directory = f"{base}/{version}"
    fake.dirs.add(directory)
    fake.files[f"{directory}/COMPLETE"] = f"{version}\n".encode()
    fake.files[f"{directory}/BUILDINFO"] = (
        f"version={version}\ncommit={'a' * 40}\n".encode()
    )
    sums = {}
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        payload = name.encode()
        fake.files[f"{directory}/{name}"] = payload
        sums[name] = hashlib.sha256(payload).hexdigest()
    fake.files[f"{directory}/SHA256SUMS.txt"] = "".join(
        f"{sums[name]}  {name}\n" for name in sorted(sums)
    ).encode()


def test_regenerate_index_uses_highest_verified_stable_release_and_reliable_writer(
    tmp_path, monkeypatch
):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = FakeAList()
    base = "/mihari-release/mihari"
    add_complete(fake, base, "v1.2.3")
    add_complete(fake, base, "v1.3.0")
    index_path = f"{base}/index.txt"
    previous = b"latest v1.2.3\nlegacy index bytes\n"
    fake.files[index_path] = previous

    regenerate.regenerate_index(fake, base)

    body = fake.files[index_path].decode()
    assert body.startswith("latest v1.3.0\n")
    assert len(body.splitlines()) == 1 + len(PLATFORMS)
    assert fake.uploads == [(index_path, body)]
    backup = tmp_path / "mihari-index-backup" / "stable" / "index.txt"
    assert backup.read_bytes() == previous


def test_regenerate_index_rejects_noncanonical_stable_base_path_without_mutation():
    fake = FakeAList()

    with pytest.raises(SystemExit):
        regenerate.regenerate_index(fake, "/mihari-release/other")

    assert fake.uploads == []


def test_regenerate_index_rebuilds_dev_index_for_highest_complete(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = FakeAList()
    base = "/mihari-release/mihari-dev"
    add_complete(fake, base, "v1.2.3-dev.1")
    add_complete(fake, base, "v1.2.3-dev.2")
    regenerate.regenerate_index(fake, base, channel="dev")
    body = fake.files[f"{base}/index.txt"].decode()
    assert body.startswith("latest v1.2.3-dev.2\n")
    assert len(body.splitlines()) == 1 + len(PLATFORMS)


def test_regenerate_index_rejects_dev_channel_with_stable_path():
    fake = FakeAList()
    with pytest.raises(SystemExit):
        regenerate.regenerate_index(fake, "/mihari-release/mihari", channel="dev")
    assert fake.uploads == []


def test_regenerate_index_help_names_stable_default_path(monkeypatch, capsys):
    monkeypatch.setattr("sys.argv", ["regenerate-index.py", "--help"])
    with pytest.raises(SystemExit) as exc:
        regenerate.main()
    assert exc.value.code == 0
    out = capsys.readouterr().out
    assert "stable" in out
    assert "/mihari-release/mihari" in out


def test_main_rejects_explicit_empty_base_path(monkeypatch, capsys):
    fake = FakeAList()
    monkeypatch.setattr(regenerate, "connect", lambda: fake)
    monkeypatch.setattr(
        "sys.argv",
        ["regenerate-index.py", "--channel", "dev", "--base-path", ""],
    )
    with pytest.raises(SystemExit):
        regenerate.main()
    err = capsys.readouterr().err
    assert "invalid release base path" in err
    assert fake.uploads == []


def test_main_channel_dev_without_base_path_targets_dev_root(monkeypatch, capsys):
    captured = {}

    def fake_connect():
        return object()

    def fake_regenerate(alist, base_path, channel="stable"):
        captured["base_path"] = base_path
        captured["channel"] = channel

    monkeypatch.setattr(regenerate, "connect", fake_connect)
    monkeypatch.setattr(regenerate, "regenerate_index", fake_regenerate)
    monkeypatch.setattr("sys.argv", ["regenerate-index.py", "--channel", "dev"])
    regenerate.main()
    assert captured == {
        "base_path": "/mihari-release/mihari-dev",
        "channel": "dev",
    }
    logged = capsys.readouterr()
    text = logged.out + logged.err
    assert "regenerating dev index at /mihari-release/mihari-dev/index.txt" in text

