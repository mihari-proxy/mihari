#!/usr/bin/env python3
"""AList drive distribution for mihari all-in-one releases (design §4.5).

Runs inside the release.yml job after the GitHub release is published:
  1. upload the 6 platform bundles (+ per-version SHA256SUMS + COMPLETE) into an
     immutable per-version directory (skip if COMPLETE already exists);
  2. resolve the root index.txt sign (placeholder on first release);
  3. build index.txt (latest line + per-platform signed direct link + sha256)
     and overwrite-upload it (the publish-complete signal);
  4. render the root downloader scripts (script 3) with the index link injected;
  5. prune old versions (keep N, index-pointed always retained);
  6. emit the signed URLs to GITHUB_ENV for the release-notes append step.

The AList REST client and shared constants live in alist_client.py, imported by
both this publish flow and the retract flow.
"""
import argparse
import os
from pathlib import Path

from alist_client import (
    AList,
    DEFAULT_BASE_PATH,
    DEFAULT_KEEP_VERSIONS,
    PLATFORMS,
    SEMVER_RE,
    bundle_name,
    connect,
    fail,
    info,
    semver_key,
    sha256_file,
)


def upload_version_dir(alist, dist_dir, base_path, version):
    """Steps 1-2: upload bundles + per-version SHA256SUMS + COMPLETE. Immutable —
    skip the whole directory when COMPLETE already exists (idempotent re-run /
    rebuild after retract)."""
    version_dir = f"{base_path}/{version}"
    if alist.exists(f"{version_dir}/COMPLETE"):
        info(f"version dir {version_dir} already complete — skipping upload")
        return version_dir
    alist.mkdir(version_dir)
    sums_lines = []
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        local = Path(dist_dir) / name
        if not local.exists():
            fail(f"bundle artifact missing: {local}")
        alist.upload(str(local), f"{version_dir}/{name}")
        sums_lines.append(f"{sha256_file(local)}  {name}")
    # Per-version aio-only checksums: retract step 4 reads this to rebuild index
    # when the retracted version was latest (design §4.5 step 2 / §4.6 step 4).
    alist.upload_text("\n".join(sums_lines) + "\n", f"{version_dir}/SHA256SUMS.txt")
    alist.upload_text(f"{version}\n", f"{version_dir}/COMPLETE")
    return version_dir


def build_index(alist, dist_dir, base_path, version):
    """Steps 2-3: return the index.txt body (latest line + per-platform
    <public_url> <sha256>) plus the root index's public direct link. Public
    links need no per-file sign, so there's no first-release placeholder dance."""
    root_index_path = f"{base_path}/index.txt"

    lines = [f"latest {version}"]
    for goos, goarch in PLATFORMS:
        platform = f"{goos}-{goarch}"
        name = bundle_name(goos, goarch)
        remote = f"{base_path}/{version}/{name}"
        if not alist.exists(remote):
            fail(f"uploaded bundle not found: {remote}")
        public = alist.public_url(remote)
        digest = sha256_file(Path(dist_dir) / name)
        lines.append(f"{platform} {public} {digest}")
    return "\n".join(lines) + "\n", alist.public_url(root_index_path)


def upload_root_scripts(alist, repo_root, base_path):
    """Step 4: upload the (now static) downloader scripts to the drive root.
    They hardcode the public INDEX_URL, so no injection is needed; they're
    overwritten each release purely to keep the AList copy self-healing."""
    for filename in ("install-aio-remote.sh", "install-aio-remote.ps1"):
        source = Path(repo_root) / filename
        if not source.exists():
            fail(f"downloader script missing: {source}")
        alist.upload_text(source.read_text(encoding="utf-8"), f"{base_path}/{filename}")


def prune_versions(alist, base_path, version, keep):
    """Step 5: keep the index-pointed version (not counted) + the newest keep-1
    others; incomplete dirs (no COMPLETE) are deleted first without counting.
    Index read failure → retry once, then skip pruning entirely (never block the
    release on retention)."""
    try:
        entries = alist.list_dir(base_path)
    except Exception:
        try:
            entries = alist.list_dir(base_path)
        except Exception as error:
            info(f"index/list failed twice — skipping retention: {error}")
            return

    version_dirs = [e["name"] for e in entries if e.get("is_dir") and semver_key(e["name"]) is not None]
    incomplete = [name for name in version_dirs if not alist.exists(f"{base_path}/{name}/COMPLETE")]
    for name in incomplete:
        info(f"removing incomplete version dir: {name}")
        alist.remove(base_path, [name])
    version_dirs = [name for name in version_dirs if name not in incomplete and name != version]

    # Keep the newest (keep-1) beyond the index-pointed version; delete the rest.
    version_dirs.sort(key=semver_key, reverse=True)
    survivors = set(version_dirs[: max(keep - 1, 0)])
    for name in version_dirs:
        if name not in survivors:
            info(f"retention pruning old version: {name}")
            alist.remove(base_path, [name])


def emit_env(name, value):
    gh_env = os.environ.get("GITHUB_ENV")
    if not gh_env:
        return
    with open(gh_env, "a", encoding="utf-8") as handle:
        handle.write(f"{name}={value}\n")


def main():
    parser = argparse.ArgumentParser(description="Publish mihari aio bundles to AList (design §4.5).")
    parser.add_argument("--version", required=True)
    parser.add_argument("--dist-dir", required=True, help="directory holding the 6 bundle artifacts")
    parser.add_argument("--repo-root", required=True, help="checkout root (for script 3 sources)")
    parser.add_argument("--base-path", default=DEFAULT_BASE_PATH)
    parser.add_argument("--keep-versions", type=int, default=DEFAULT_KEEP_VERSIONS)
    args = parser.parse_args()

    if not SEMVER_RE.match(args.version):
        fail(f"version {args.version!r} must match {SEMVER_RE.pattern}")

    alist = connect()
    version_dir = upload_version_dir(alist, args.dist_dir, args.base_path, args.version)
    index_body, index_url = build_index(alist, args.dist_dir, args.base_path, args.version)
    alist.upload_text(index_body, f"{args.base_path}/index.txt")
    upload_root_scripts(alist, args.repo_root, args.base_path)
    prune_versions(alist, args.base_path, args.version, args.keep_versions)

    emit_env("ALIST_VERSION_DIR", version_dir)
    info(f"published {args.version} to {args.base_path}; index at {index_url}")


if __name__ == "__main__":
    main()
