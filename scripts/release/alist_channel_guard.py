#!/usr/bin/env python3
"""Snapshot and compare the stable AList index without writing AList."""
import argparse
import hashlib
import json
from pathlib import Path

from alist_client import connect, fail

STABLE_INDEX_PATH = "/mihari-release/mihari/index.txt"


def _require_stable_index(path):
    if path != STABLE_INDEX_PATH:
        fail("path is not the stable channel index")


def _read_stable_index(alist, path):
    try:
        return alist.content(path)
    except Exception:
        fail("unable to read the stable channel index")


def snapshot(alist, path, output_dir):
    """Read the stable index and write index.txt plus metadata.json."""
    _require_stable_index(path)
    output_dir = Path(output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    body = _read_stable_index(alist, path)
    raw = b"" if body is None else body.encode("utf-8")
    (output_dir / "index.txt").write_bytes(raw)
    (output_dir / "metadata.json").write_text(
        json.dumps(
            {
                "channel": "stable",
                "existed": body is not None,
                "path": path,
                "sha256": hashlib.sha256(raw).hexdigest(),
            },
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )


def compare(alist, path, expected_dir):
    """Fail closed if the live stable index bytes or existence differ."""
    _require_stable_index(path)
    expected_dir = Path(expected_dir)
    try:
        expected = (expected_dir / "index.txt").read_bytes()
        metadata = json.loads((expected_dir / "metadata.json").read_text(encoding="utf-8"))
        existed = metadata["existed"]
    except (OSError, KeyError, TypeError, json.JSONDecodeError, UnicodeDecodeError):
        fail("unable to read isolation snapshot")
    live = _read_stable_index(alist, path)
    live_bytes = b"" if live is None else live.encode("utf-8")
    if live_bytes != expected or (live is not None) != existed:
        fail("foreign channel index changed during this mutation")


def main():
    parser = argparse.ArgumentParser(
        description="Snapshot and compare the stable AList index."
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    snapshot_parser = subparsers.add_parser("snapshot")
    snapshot_parser.add_argument("--path", required=True)
    snapshot_parser.add_argument("--output-dir", required=True)

    compare_parser = subparsers.add_parser("compare")
    compare_parser.add_argument("--path", required=True)
    compare_parser.add_argument("--expected-dir", required=True)

    args = parser.parse_args()
    alist = connect()
    if args.command == "snapshot":
        snapshot(alist, args.path, args.output_dir)
        return
    compare(alist, args.path, args.expected_dir)


if __name__ == "__main__":
    main()
