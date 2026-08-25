"""Safe, shared mutation of channel-specific AList release indexes."""
import hashlib
import json
import os
import tempfile
import time
from pathlib import Path

from release_policy import parse_version

_LOCK_WAIT_TIMEOUT_SECONDS = 10
_LOCK_WAIT_INTERVAL_SECONDS = 0.05


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
    if channel not in {"stable", "dev"}:
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
    try:
        parse_version(latest, channel)
    except ValueError as error:
        raise IndexMutationError("refusing to write an invalid channel index") from error


def _backup_paths(channel: str) -> tuple[Path, Path, Path, Path]:
    runner_temp = Path(os.environ.get("RUNNER_TEMP", tempfile.gettempdir()))
    backup_root = runner_temp / "mihari-index-backup" / channel
    return (
        backup_root,
        backup_root / "index.txt",
        backup_root / "metadata.json",
        backup_root / "index.lock",
    )


def _acquire_lock(lock_path: Path, channel: str) -> int:
    deadline = time.monotonic() + _LOCK_WAIT_TIMEOUT_SECONDS
    while True:
        try:
            lock_fd = os.open(lock_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        except FileExistsError as error:
            if time.monotonic() >= deadline:
                raise IndexMutationError(
                    f"channel index mutation is already in progress: {channel}"
                ) from error
            time.sleep(_LOCK_WAIT_INTERVAL_SECONDS)
        else:
            return lock_fd


def write_index_reliably(
    alist,
    path: str,
    body: str,
    expected_previous: str | None,
    channel: str,
    allow_empty: bool = False,
) -> None:
    """Commit an AList index with one upload and authoritative readback.

    The current object is compared to the caller's observed value while a
    local lock is held, preventing a waiting workflow from overwriting newer
    channel state. An ambiguous post-write state is preserved for manual
    recovery instead of being overwritten. This local lock only coordinates
    processes on one runner.
    """
    _validate(channel, body, allow_empty)
    backup_root, backup_path, metadata_path, lock_path = _backup_paths(channel)
    backup_root.mkdir(parents=True, exist_ok=True)
    lock_fd = _acquire_lock(lock_path, channel)

    try:
        os.close(lock_fd)
        lock_fd = None

        live_body = alist.content(path)
        if live_body == body:
            return
        if live_body != expected_previous:
            raise IndexMutationError("stale index observed before mutation")

        backup_bytes = (live_body or "").encode("utf-8")
        backup_path.write_bytes(backup_bytes)
        metadata_path.write_text(
            json.dumps(
                {
                    "channel": channel,
                    "existed": live_body is not None,
                    "path": path,
                    "sha256": hashlib.sha256(backup_bytes).hexdigest(),
                },
                sort_keys=True,
            )
            + "\n",
            encoding="utf-8",
        )
        try:
            alist.upload_text(body, path)
        except Exception:
            pass
        try:
            observed = alist.content(path)
        except Exception as error:
            raise IndexMutationError(
                f"index readback is uncertain; manual recovery required using {backup_path}"
            ) from error
        if observed == body:
            return
        if observed == live_body:
            raise IndexMutationError("index write failed; index remained unchanged")
        raise IndexMutationError(
            f"stale index observed after mutation; manual recovery required using {backup_path}"
        )
    finally:
        if lock_fd is not None:
            try:
                os.close(lock_fd)
            except OSError:
                pass
        try:
            lock_path.unlink()
        except FileNotFoundError:
            pass
