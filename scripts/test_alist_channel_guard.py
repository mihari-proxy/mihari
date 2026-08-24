import hashlib
import json
from pathlib import Path

import pytest

import alist_channel_guard as guard

STABLE_INDEX = "/mihari-release/mihari/index.txt"


class GuardFake:
    def __init__(self, body=None):
        self.body = body

    def content(self, path):
        assert path == STABLE_INDEX
        return self.body


def test_snapshot_missing_index_records_existed_false(tmp_path):
    output = tmp_path / "stable-isolation"
    guard.snapshot(GuardFake(None), STABLE_INDEX, output)
    assert (output / "index.txt").read_bytes() == b""
    metadata = json.loads((output / "metadata.json").read_text(encoding="utf-8"))
    assert metadata["channel"] == "stable"
    assert metadata["existed"] is False
    assert metadata["path"] == STABLE_INDEX
    assert metadata["sha256"] == hashlib.sha256(b"").hexdigest()


def test_compare_accepts_unchanged_bytes(tmp_path):
    output = tmp_path / "stable-isolation"
    body = "latest v1.2.3\n"
    guard.snapshot(GuardFake(body), STABLE_INDEX, output)
    guard.compare(GuardFake(body), STABLE_INDEX, output)


def test_compare_rejects_changed_index_without_logging_body(tmp_path, capsys):
    output = tmp_path / "stable-isolation"
    guard.snapshot(GuardFake("latest v1.2.3\n"), STABLE_INDEX, output)
    with pytest.raises(SystemExit):
        guard.compare(GuardFake("latest v9.0.0\n"), STABLE_INDEX, output)
    err = capsys.readouterr().err
    assert "foreign channel index changed during this mutation" in err
    assert "latest " not in err
    assert "v9.0.0" not in err


def test_guard_rejects_non_stable_index_path(tmp_path):
    output = tmp_path / "stable-isolation"
    fake = GuardFake("latest v1.2.3\n")
    for path in (
        "/mihari-release/mihari-dev/index.txt",
        "/mihari-release/mihari/other.txt",
        "/mihari/index.txt",
        "/",
    ):
        with pytest.raises(SystemExit):
            guard.snapshot(fake, path, output)
        with pytest.raises(SystemExit):
            guard.compare(fake, path, output)
