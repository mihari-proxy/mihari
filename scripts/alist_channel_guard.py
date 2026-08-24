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


def snapshot(alist, path, output_dir):
    """Read the stable index and write index.txt plus metadata.json."""
    _require_stable_index(path)
    output_dir = Path(output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    body = alist.content(path)
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
    """Fail closed if the live stable index bytes differ from the snapshot."""
    _require_stable_index(path)
    expected = (Path(expected_dir) / "index.txt").read_bytes()
    live = alist.content(path)
    live_bytes = b"" if live is None else live.encode("utf-8")
    if live_bytes != expected:
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
