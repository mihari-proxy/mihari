import importlib.util
from pathlib import Path

import pytest
import hashlib
from alist_client import PLATFORMS, bundle_name


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
    def exists(self, p): return p in self.files or p in self.dirs
    def read_bytes(self, p, **kwargs): return self.files[p]
    def content(self, p):
        b = self.files.get(p)
        return b.decode() if b is not None else None
    def list_dir(self, p):
        pref = p.rstrip("/") + "/"
        names = {k[len(pref):].split("/", 1)[0] for k in self.files if k.startswith(pref)} | {k[len(pref):].split("/", 1)[0] for k in self.dirs if k.startswith(pref)}
        return [{"name": n, "is_dir": True} for n in names]
    def remove(self, p, names):
        for n in names:
            pref = p.rstrip("/") + "/" + n
            self.files = {k: v for k, v in self.files.items() if not (k == pref or k.startswith(pref + "/"))}
            self.dirs = {k for k in self.dirs if not (k == pref or k.startswith(pref + "/"))}
    def upload_text(self, text, p): self.files[p] = text.encode()
    def public_url(self, p): return "https://example.invalid" + p


def add_complete(fake, base, version):
    d = f"{base}/{version}"
    fake.dirs.add(d)
    fake.files[f"{d}/COMPLETE"] = (version + "\n").encode()
    fake.files[f"{d}/BUILDINFO"] = (f"version={version}\ncommit={'a'*40}\n").encode()
    sums = []
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        payload = name.encode()
        fake.files[f"{d}/{name}"] = payload
        sums.append(f"{hashlib.sha256(payload).hexdigest()}  {name}")
    fake.files[f"{d}/SHA256SUMS.txt"] = ("\n".join(sums) + "\n").encode()


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


def test_dev_retract_rebuilds_highest_remaining_complete(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    base = "/mihari-release/mihari-dev"
    add_complete(fake, base, "v1.0.0-dev.1")
    add_complete(fake, base, "v1.1.0-dev.1")
    fake.files[f"{base}/v1.0.0-dev.1/BUILDINFO"] = b"version=v1.0.0-dev.1\ncommit=" + b"a" * 40 + b"\n"
    fake.files[f"{base}/v1.1.0-dev.1/BUILDINFO"] = b"version=v1.1.0-dev.1\ncommit=" + b"a" * 40 + b"\n"
    fake.files[f"{base}/index.txt"] = b"latest v1.1.0-dev.1\n"
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
    with pytest.raises(SystemExit):
        retract_mod.retract(fake, base, "v1.1.0-dev.1", "dev", "b" * 40)
    assert fake.exists(f"{base}/v1.1.0-dev.1")


def test_retract_missing_dev_directory_is_idempotent(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = Fake()
    retract_mod.retract(fake, "/mihari-release/mihari-dev", "v1.1.0-dev.1", "dev", "a" * 40)
