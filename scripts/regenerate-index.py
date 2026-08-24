#!/usr/bin/env python3
"""One-shot index.txt regeneration for the current AList topology.

The on-disk index.txt carries bundle URLs (and sha256) baked in when it was last
uploaded. After an AList layout change — e.g. the /mihari mount was removed and
the files now live under /mihari-release/mihari (served via /p/public/...) — an
older index.txt still points at dead /p/mihari/... links, so installs fetch the
index fine but then fail on every bundle URL.

This rewrites index.txt to match the *current* topology: it finds the highest
version dir with a COMPLETE marker, reads that dir's SHA256SUMS.txt, and re-emits
index.txt with public_url() links (current base_path + /public prefix, no sign).
Run locally once with AList write credentials after a layout change; the release
workflow does the same rebuild on every publish, so this is only needed to fix
an index that predates a manual AList reconfiguration.

Usage:
    ALIST_URL=https://... ALIST_USERNAME=... ALIST_PASSWORD=... \
        python scripts/regenerate-index.py
"""
import argparse
import importlib.util
from pathlib import Path

from alist_client import connect, fail, info
from release_policy import expected_base_path, validate_base_path


def _load_retract():
    # retract-alist.py has a hyphen in its name — import it manually, exactly as
    # test_alist_client.py does for release-alist.py.
    path = Path(__file__).with_name("retract-alist.py")
    spec = importlib.util.spec_from_file_location("retract_alist", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def regenerate_index(alist, base_path, channel="stable"):
    """Rebuild the channel index through the shared reliable writer."""
    try:
        validate_base_path(channel, base_path)
    except ValueError as error:
        fail(str(error))
    retract = _load_retract()
    index_path = f"{base_path}/index.txt"
    previous = retract.read_index(alist, index_path)
    try:
        latest = retract.highest_complete(
            alist, base_path, excluded=None, channel=channel
        )
    except retract.RemoteScanError:
        fail("unable to inspect release versions")
    if latest is None:
        fail("no COMPLETE version dir found — nothing to rebuild index from")
    info(f"highest complete version: {latest}")
    try:
        body = retract.index_body(alist, base_path, latest, channel)
    except retract.RemoteScanError:
        fail("unable to verify release directory")
    retract.write_index_reliably(
        alist, index_path, body, previous, channel=channel
    )


def main():
    parser = argparse.ArgumentParser(
        description="Regenerate index.txt for the current AList topology."
    )
    parser.add_argument(
        "--channel",
        choices=("stable", "dev"),
        default="stable",
        help="Index channel to rebuild (default: stable, writes /mihari-release/mihari/index.txt)",
    )
    parser.add_argument("--base-path", default=None)
    args = parser.parse_args()
    channel = args.channel
    base_path = args.base_path or expected_base_path(channel)
    info(f"regenerating {channel} index at {base_path}/index.txt")
    alist = connect()
    regenerate_index(alist, base_path, channel)
    info("index.txt regenerated with current public_url() links")


if __name__ == "__main__":
    main()

