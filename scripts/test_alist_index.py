import pytest

from alist_index import IndexMutationError, write_index_reliably


class FakeAList:
    def __init__(self, files=None, reads=None, fail_restore=False):
        self.files = dict(files or {})
        self.reads = list(reads or [])
        self.fail_restore = fail_restore
        self.calls = []

    def content(self, path):
        self.calls.append(("read", path))
        if self.reads:
            value = self.reads.pop(0)
            if value is not Ellipsis:
                return value
        return self.files.get(path)

    def upload_text(self, body, path):
        self.calls.append(("upload", path, body))
        if self.fail_restore and body == "latest v1.0.0-dev.1\n":
            raise OSError("restore unavailable")
        self.files[path] = body

    def remove(self, base, names):
        self.calls.append(("remove", base, tuple(names)))
        for name in names:
            self.files.pop(f"{base.rstrip('/')}/{name}", None)

    def exists(self, path):
        self.calls.append(("exists", path))
        return path in self.files


def index_path():
    return "/mihari-release/mihari-dev/index.txt"


def mutations(fake):
    return [call for call in fake.calls if call[0] in {"upload", "remove"}]


def test_stale_live_index_is_rejected_before_first_mutation(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    fake = FakeAList({index_path(): "latest v1.1.0-dev.1\n"})

    with pytest.raises(IndexMutationError, match="stale"):
        write_index_reliably(
            fake,
            index_path(),
            "latest v1.2.0-dev.1\n",
            "latest v1.0.0-dev.1\n",
            "dev",
        )

    assert mutations(fake) == []
    assert fake.calls[0] == ("read", index_path())


def test_failed_first_index_write_deletes_new_index_and_confirms_absence(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    path = index_path()
    fake = FakeAList(reads=[None, "corrupt", "corrupt"], files={})

    with pytest.raises(IndexMutationError, match="previous index restored"):
        write_index_reliably(fake, path, "latest v1.2.0-dev.1\n", None, "dev")

    assert path not in fake.files
    assert ("remove", "/mihari-release/mihari-dev", ("index.txt",)) in fake.calls
    assert ("exists", path) in fake.calls


def test_failed_recovery_reports_backup_path(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    path = index_path()
    previous = "latest v1.0.0-dev.1\n"
    fake = FakeAList({path: previous}, fail_restore=True)
    original_upload = fake.upload_text

    def corrupt_new_index(body, remote):
        if body == previous:
            original_upload(body, remote)
        else:
            fake.calls.append(("upload", remote, body))
            fake.files[remote] = "corrupt"

    fake.upload_text = corrupt_new_index

    with pytest.raises(IndexMutationError) as error:
        write_index_reliably(fake, path, "latest v1.2.0-dev.1\n", previous, "dev")

    assert str(tmp_path / "mihari-index-backup" / "dev" / "index.txt") in str(error.value)


def test_upload_failure_after_remote_mutation_restores_previous_index(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    path = index_path()
    previous = "latest v1.0.0-dev.1\n"
    desired = "latest v1.2.0-dev.1\n"

    class MutatingFailureAList(FakeAList):
        def upload_text(self, body, remote):
            self.calls.append(("upload", remote, body))
            self.files[remote] = body
            if body == desired:
                raise OSError("upload response lost")

    fake = MutatingFailureAList({path: previous})

    with pytest.raises(IndexMutationError, match="previous index restored"):
        write_index_reliably(fake, path, desired, previous, "dev")

    assert fake.files[path] == previous
    assert [call for call in fake.calls if call == ("upload", path, desired)] == [
        ("upload", path, desired),
        ("upload", path, desired),
    ]


def test_empty_body_requires_explicit_retraction_mode(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    path = index_path()
    fake = FakeAList({path: "latest v1.0.0-dev.1\n"})

    with pytest.raises(IndexMutationError, match="empty"):
        write_index_reliably(fake, path, "", "latest v1.0.0-dev.1\n", "dev")
    assert mutations(fake) == []

    write_index_reliably(fake, path, "", "latest v1.0.0-dev.1\n", "dev", allow_empty=True)
    assert fake.files[path] == ""


def test_equal_live_index_is_idempotent_without_mutation(tmp_path, monkeypatch):
    monkeypatch.setenv("RUNNER_TEMP", str(tmp_path))
    body = "latest v1.2.0-dev.1\n"
    fake = FakeAList({index_path(): body})

    write_index_reliably(fake, index_path(), body, "latest v1.0.0-dev.1\n", "dev")

    assert mutations(fake) == []
