"""Safe, shared mutation of channel-specific AList release indexes."""
import os
import re
import tempfile
from pathlib import Path


_VERSION_PATTERNS = {
    "stable": re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+$"),
    "dev": re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+-dev\.[0-9]+$"),
}


class IndexMutationError(RuntimeError):
    """Raised when an index mutation cannot be committed or recovered safely."""


def parse_latest(index_text: str | None) -> str | None:
    """Return the version in the first ``latest`` line, if one is present."""
    for line in (index_text or "").splitlines():
        fields = line.split()
        if len(fields) >= 2 and fields[0] == "latest":
            return fields[1]
    return None


def _validate(channel: str, body: str, allow_empty: bool) -> None:
    version_pattern = _VERSION_PATTERNS.get(channel)
    if version_pattern is None:
        raise IndexMutationError("refusing to write an index for an unknown channel")
    if not isinstance(body, str):
        raise IndexMutationError("refusing to write a non-text index")
    if not body:
        if allow_empty:
            return
        raise IndexMutationError("refusing to write an empty index outside retraction")
    latest = parse_latest(body)
    if latest is None:
        raise IndexMutationError("refusing to write an index without a latest version")
    if version_pattern.fullmatch(latest) is None:
        raise IndexMutationError("refusing to write an invalid channel index")


def _backup_paths(channel: str) -> tuple[Path, Path, Path]:
    runner_temp = Path(os.environ.get("RUNNER_TEMP", tempfile.gettempdir()))
    backup_root = runner_temp / "mihari-index-backup" / channel
    return backup_root, backup_root / "index.txt", backup_root / "index.lock"


def _restore_previous(alist, path: str, previous: str | None) -> bool:
    if previous is None:
        base, name = path.rsplit("/", 1)
        alist.remove(base, [name])
        return not alist.exists(path) and alist.content(path) is None
    alist.upload_text(previous, path)
    return alist.content(path) == previous


def write_index_reliably(
    alist,
    path: str,
    body: str,
    expected_previous: str | None,
    channel: str,
    allow_empty: bool = False,
) -> None:
    """Commit an AList index with bounded retries and verified recovery.

    The current object is compared to the caller's observed value before the
    first remote mutation, preventing a stale workflow from overwriting newer
    channel state. This local lock only coordinates processes on one runner.
    """
    _validate(channel, body, allow_empty)
    live_body = alist.content(path)
    if live_body == body:
        return
    if live_body != expected_previous:
        raise IndexMutationError("stale index observed before mutation")

    backup_root, backup_path, lock_path = _backup_paths(channel)
    backup_root.mkdir(parents=True, exist_ok=True)
    backup_path.write_bytes((live_body or "").encode("utf-8"))
    try:
        lock_fd = os.open(lock_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except FileExistsError as error:
        raise IndexMutationError(f"channel index mutation is already in progress: {channel}") from error
    else:
        os.close(lock_fd)

    try:
        for _ in range(2):
            try:
                alist.upload_text(body, path)
                if alist.content(path) == body:
                    return
            except Exception:
                continue
        try:
            recovered = _restore_previous(alist, path, live_body)
        except Exception:
            recovered = False
        if not recovered:
            raise IndexMutationError(
                f"index write and recovery failed; manual recovery required using {backup_path}"
            )
        raise IndexMutationError("index write verification failed; previous index restored")
    finally:
        try:
            lock_path.unlink()
        except FileNotFoundError:
            pass
