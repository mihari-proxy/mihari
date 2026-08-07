#!/usr/bin/env python3
"""AList drive retraction for a fatally-broken mihari release (design §4.6).

Runs inside the retract.yml workflow (manual dispatch, double-confirm). Performs
the AList side of the withdrawal — steps 2-4 of the design flow — while step 5
(`gh release delete --cleanup-tag`) runs as a shell step in the workflow:

  2. read index.txt to learn whether the retracted version is the current latest;
  3. remove the AList version directory /mihari/<version>/;
  4. rebuild index.txt ONLY when the retracted version was latest — point it at
     the highest remaining COMPLETE version (sign from fs/get, sha256 read from
     that version dir's SHA256SUMS.txt), or leave it empty if none remain.

Withdrawal removes distribution channels only; already-installed users are not
recallable (design §4.6 boundary). The AList client and shared helpers come from
alist_client.py, shared with the publish flow.
"""
import argparse

from alist_client import (
    DEFAULT_BASE_PATH,
    PLATFORMS,
    bundle_name,
    connect,
    fail,
    info,
    semver_key,
)


def parse_latest(index_text):
    """Extract the `latest <version>` value from an index.txt body, or None."""
    for line in (index_text or "").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or line.startswith("//"):
            continue
        fields = line.split()
        if fields and fields[0] == "latest" and len(fields) >= 2:
            return fields[1]
    return None


def highest_complete(alist, base_path, excluded):
    """Highest remaining version dir that has a COMPLETE marker (and is not the
    excluded/retracted one). None if no complete version remains — incomplete
    dirs are skipped so index never points at a publish-interrupted residue."""
    entries = alist.list_dir(base_path)
    candidates = [
        e["name"] for e in entries
        if e.get("is_dir")
        and e["name"] != excluded
        and semver_key(e["name"]) is not None
        and alist.exists(f"{base_path}/{e['name']}/COMPLETE")
    ]
    if not candidates:
        return None
    candidates.sort(key=semver_key, reverse=True)
    return candidates[0]


def rebuild_index(alist, base_path, new_latest):
    """Rewrite index.txt to point at new_latest: latest line + one per-platform
    signed direct link (fs/get sign) + sha256 (read from that dir's
    SHA256SUMS.txt). Uploads the new body."""
    sums_text = alist.content(f"{base_path}/{new_latest}/SHA256SUMS.txt")
    if sums_text is None:
        fail(f"new-latest {new_latest} has no SHA256SUMS.txt — cannot rebuild index")
    sums = {}
    for line in sums_text.splitlines():
        parts = line.split(None, 1)
        if len(parts) == 2:
            sums[parts[1].strip()] = parts[0].strip()

    lines = [f"latest {new_latest}"]
    for goos, goarch in PLATFORMS:
        platform = f"{goos}-{goarch}"
        name = bundle_name(goos, goarch)
        remote = f"{base_path}/{new_latest}/{name}"
        sign = alist.sign_of(remote)
        if sign is None:
            fail(f"new-latest bundle missing for sign: {remote}")
        signed = alist.signed_url(remote, sign)
        digest = sums.get(name)
        if not digest:
            fail(f"SHA256SUMS.txt in {new_latest} missing entry for {name}")
        lines.append(f"{platform} {signed} {digest}")
    alist.upload_text("\n".join(lines) + "\n", f"{base_path}/index.txt")
    info(f"index.txt rebuilt to point at {new_latest}")


def retract(alist, base_path, version):
    root_index = f"{base_path}/index.txt"
    current_latest = parse_latest(alist.content(root_index))

    # Step 3: always remove the retracted version dir (idempotent if absent).
    version_dir = f"{base_path}/{version}"
    if alist.exists(version_dir):
        info(f"removing AList version dir: {version_dir}")
        alist.remove(base_path, [version])
    else:
        info(f"AList version dir not present (already removed?): {version_dir}")

    # Step 4: rebuild index only when we just removed the current latest.
    if current_latest != version:
        info(
            f"retracted {version} was not the current latest "
            f"({current_latest!r}) — index.txt unchanged"
        )
        return

    new_latest = highest_complete(alist, base_path, excluded=version)
    if new_latest is None:
        info("no complete version remains — setting index.txt empty")
        alist.upload_text("", root_index)
        return
    rebuild_index(alist, base_path, new_latest)


def main():
    parser = argparse.ArgumentParser(description="Retract a mihari version from AList (design §4.6).")
    parser.add_argument("--version", required=True)
    parser.add_argument("--base-path", default=DEFAULT_BASE_PATH)
    args = parser.parse_args()

    retract(connect(), args.base_path, args.version)
    info(f"retraction of {args.version} complete on the AList drive")


if __name__ == "__main__":
    main()
