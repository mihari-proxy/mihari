#!/usr/bin/env python3
"""AList drive distribution for mihari all-in-one releases (design §4.5).

Runs inside the release.yml job after the GitHub release is published:
  1. upload the 6 platform bundles (+ aio-only SHA256SUMS) into an immutable
     per-version directory, then a COMPLETE marker (skip if already complete);
  2. resolve the root index.txt sign (placeholder on first release);
  3. build index.txt (latest line + per-platform signed direct link + sha256)
     and overwrite-upload it (the publish-complete signal);
  4. render the root downloader scripts (script 3) with the index link injected;
  5. prune old versions (keep N, index-pointed always retained);
  6. emit the signed URLs to GITHUB_ENV for the release-notes append step.

Operations target the AList v3 REST API (login / fs/get / fs/list / fs/put /
fs/remove / fs/mkdir) via requests — the standard surface the design's alist-cli
wraps. The whole block is conditional on ALIST_URL in the workflow and is
verified post-release (§9 prerequisites: alist-cli fork + secrets).
"""
import argparse
import hashlib
import os
import re
import sys
from pathlib import Path
from urllib.parse import quote

import requests

DEFAULT_BASE_PATH = "/mihari"
DEFAULT_KEEP_VERSIONS = 5
PLATFORMS = [
    "linux/amd64", "linux/arm64",
    "darwin/amd64", "darwin/arm64",
    "windows/amd64", "windows/arm64",
]
SEMVER_RE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")
INDEX_PLACEHOLDER = "__MIHARI_INDEX_URL__"


def fail(message):
    print(f"::error::{message}", file=sys.stderr)
    sys.exit(1)


def info(message):
    print(f"::notice::{message}")


def semver_key(name):
    match = SEMVER_RE.match(name)
    return tuple(int(x) for x in match.groups()) if match else None


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def bundle_name(goos, goarch):
    ext = ".zip" if goos == "windows" else ".tar.gz"
    return f"mihari-all-in-one-{goos}-{goarch}{ext}"


class AList:
    """Thin client over the AList v3 REST API."""

    def __init__(self, base_url, username, password):
        self.base = base_url.rstrip("/")
        self.session = requests.Session()
        token = self._login(username, password)
        self.session.headers["Authorization"] = token

    def _post(self, path, **kwargs):
        response = self.session.post(self.base + path, timeout=120, **kwargs)
        response.raise_for_status()
        return response.json()

    def _login(self, username, password):
        data = self._post("/api/auth/login", json={"username": username, "password": password})
        if data.get("code") != 200:
            fail(f"alist login failed: {data.get('message')}")
        return data["data"]["token"]

    def get(self, path):
        return self._post("/api/fs/get", json={"path": path, "password": ""})

    def exists(self, path):
        return self.get(path).get("code") == 200

    def sign_of(self, path):
        data = self.get(path)
        if data.get("code") != 200:
            return None
        return data["data"].get("sign", "")

    def list_dir(self, path):
        data = self._post("/api/fs/list", json={"path": path, "password": "", "page": 1, "per_page": 0, "refresh": False})
        return data.get("data", {}).get("content") or []

    def mkdir(self, path):
        self._post("/api/fs/mkdir", json={"path": path})

    def upload(self, local, remote_path):
        with open(local, "rb") as handle:
            response = self.session.put(
                self.base + "/api/fs/put",
                headers={
                    "File-Path": quote(remote_path, safe=""),
                    "As-Task": "false",
                    "Content-Type": "application/octet-stream",
                },
                data=handle,
                timeout=900,
            )
        response.raise_for_status()

    def upload_text(self, text, remote_path):
        response = self.session.put(
            self.base + "/api/fs/put",
            headers={
                "File-Path": quote(remote_path, safe=""),
                "As-Task": "false",
                "Content-Type": "text/plain",
            },
            data=text.encode("utf-8"),
            timeout=120,
        )
        response.raise_for_status()

    def remove(self, dir_path, names):
        self._post("/api/fs/remove", json={"dir": dir_path, "names": list(names)})

    def signed_url(self, path, sign):
        # /p<path>?sign=... is AList's proxy/stream route (design §4.4 URL form).
        return f"{self.base}/p{path}?sign={sign}"


def upload_version_dir(alist, dist_dir, base_path, version):
    """Steps 1-2: upload bundles + aio SHA256SUMS, then COMPLETE. Immutable —
    skip the whole directory when COMPLETE already exists (idempotent re-run /
    rebuild after retract)."""
    version_dir = f"{base_path}/{version}"
    if alist.exists(f"{version_dir}/COMPLETE"):
        info(f"version dir {version_dir} already complete — skipping upload")
        return version_dir
    alist.mkdir(version_dir)
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        local = Path(dist_dir) / name
        if not local.exists():
            fail(f"bundle artifact missing: {local}")
        alist.upload(str(local), f"{version_dir}/{name}")
    alist.upload_text(f"{version}\n", f"{version_dir}/COMPLETE")
    return version_dir


def build_index(alist, dist_dir, base_path, version):
    """Steps 2-3: resolve the root index sign + each bundle sign, then return
    the index.txt body (latest line + per-platform <signed_url> <sha256>)."""
    root_index_path = f"{base_path}/index.txt"
    index_sign = alist.sign_of(root_index_path)
    if index_sign is None:
        # First release: upload an empty placeholder so fs/get yields a sign.
        alist.upload_text("", root_index_path)
        index_sign = alist.sign_of(root_index_path) or ""

    lines = [f"latest {version}"]
    for goos, goarch in PLATFORMS:
        platform = f"{goos}-{goarch}"
        name = bundle_name(goos, goarch)
        remote = f"{base_path}/{version}/{name}"
        sign = alist.sign_of(remote)
        if sign is None:
            fail(f"uploaded bundle not found for sign: {remote}")
        signed = alist.signed_url(remote, sign)
        digest = sha256_file(Path(dist_dir) / name)
        lines.append(f"{platform} {signed} {digest}")
    return "\n".join(lines) + "\n", alist.signed_url(root_index_path, index_sign)


def render_root_scripts(alist, repo_root, base_path, index_signed_url):
    """Step 4: inject the root index signed link into script 3 and upload to the
    drive root (content is constant across releases; overwritten each time).
    Returns the rendered scripts' signed direct links for release notes."""
    rendered = {}
    for filename in ("install-aio-remote.sh", "install-aio-remote.ps1"):
        source = Path(repo_root) / filename
        if not source.exists():
            fail(f"downloader script missing: {source}")
        text = source.read_text(encoding="utf-8").replace(INDEX_PLACEHOLDER, index_signed_url)
        remote = f"{base_path}/{filename}"
        alist.upload_text(text, remote)
        sign = alist.sign_of(remote)
        if sign is None:
            fail(f"rendered script not found for sign: {remote}")
        rendered[filename] = alist.signed_url(remote, sign)
    return rendered


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

    base_url = os.environ.get("ALIST_URL")
    username = os.environ.get("ALIST_USERNAME")
    password = os.environ.get("ALIST_PASSWORD")
    if not base_url or not username or not password:
        fail("ALIST_URL / ALIST_USERNAME / ALIST_PASSWORD are required")

    alist = AList(base_url, username, password)
    version_dir = upload_version_dir(alist, args.dist_dir, args.base_path, args.version)
    index_body, index_signed_url = build_index(alist, args.dist_dir, args.base_path, args.version)
    alist.upload_text(index_body, f"{args.base_path}/index.txt")
    rendered = render_root_scripts(alist, args.repo_root, args.base_path, index_signed_url)
    prune_versions(alist, args.base_path, args.version, args.keep_versions)

    emit_env("ALIST_INDEX_URL", index_signed_url)
    emit_env("ALIST_VERSION_DIR", version_dir)
    emit_env("ALIST_REMOTE_SH_URL", rendered["install-aio-remote.sh"])
    emit_env("ALIST_REMOTE_PS1_URL", rendered["install-aio-remote.ps1"])
    info(f"published {args.version} to {args.base_path}; index at {index_signed_url}")


if __name__ == "__main__":
    main()
