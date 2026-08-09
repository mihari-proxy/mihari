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

from alist_client import DEFAULT_BASE_PATH, connect, fail, info


def _load_retract():
    # retract-alist.py has a hyphen in its name — import it manually, exactly as
    # test_alist_client.py does for release-alist.py.
    path = Path(__file__).with_name("retract-alist.py")
    spec = importlib.util.spec_from_file_location("retract_alist", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    parser = argparse.ArgumentParser(
        description="Regenerate index.txt for the current AList topology."
    )
    parser.add_argument("--base-path", default=DEFAULT_BASE_PATH)
    args = parser.parse_args()

    retract = _load_retract()
    alist = connect()

    latest = retract.highest_complete(alist, args.base_path, excluded=None)
    if latest is None:
        fail("no COMPLETE version dir found — nothing to rebuild index from")
    info(f"highest complete version: {latest}")
    retract.rebuild_index(alist, args.base_path, latest)
    info("index.txt regenerated with current public_url() links")


if __name__ == "__main__":
    main()
