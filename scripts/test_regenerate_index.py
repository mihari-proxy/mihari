import hashlib
import importlib.util
from pathlib import Path

import pytest

from alist_client import PLATFORMS, bundle_name


def load_module(name):
    path = Path(__file__).with_name(name + ".py")
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
