"""Shared AList v3 client + helpers for the release/retract workflows.

Both scripts/release-alist.py (publish) and scripts/retract-alist.py
(withdraw) talk to the same self-hosted AList drive via the v3 REST API
(login / fs/get / fs/list / fs/put / fs/remove / fs/mkdir) and share the
same notion of a version directory, bundle names, and semver ordering.
Centralizing the client keeps the two flows consistent and avoids drifting
two copies of the API surface.
"""
import hashlib
import os
import re
import sys
from pathlib import Path
from urllib.parse import quote

import requests

# AList topology quirk: the fs API (list/get/put/mkdir) addresses files under
# the root storage (paths rooted at /), so this is the *fs* base path. The /p
# download route needs a different prefix — see AList.public_url. Verified
# 2026-08; if a /mihari mount is ever restored, set this back to "/mihari".
DEFAULT_BASE_PATH = "/mihari-release/mihari"
DEFAULT_KEEP_VERSIONS = 5
# (goos, goarch) pairs so the release/retract `for goos, goarch in PLATFORMS`
# loops unpack correctly. v0.2.0 crashed at release-alist.py:47 because these
# were "goos/goarch" strings; test_alist_client.test_platforms_* pins the shape.
PLATFORMS = [
    ("linux", "amd64"), ("linux", "arm64"),
    ("darwin", "amd64"), ("darwin", "arm64"),
    ("windows", "amd64"), ("windows", "arm64"),
]
SEMVER_RE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")


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
        return self._post("/api/fs/get", json={"path": self._fs_path(path), "password": ""})

    def exists(self, path):
        return self.get(path).get("code") == 200

    def list_dir(self, path):
        data = self._post("/api/fs/list", json={"path": self._fs_path(path), "password": "", "page": 1, "per_page": 0, "refresh": False})
        return data.get("data", {}).get("content") or []

    def _fs_path(self, path):
        """Convert a logical base_path path into the path the AList fs API needs.

        AList topology quirk (verified 2026-08-10): EVERY fs API op (get/list/
        put/mkdir/remove) resolves its path RELATIVE to the storage root_folder
        (/mihari-release), prepending the root again — so a logical path like
        "/mihari-release/mihari/X" must be handed to the fs API as "/mihari/X", or
        it lands at (writes) / is looked up at (reads) the doubled physical
        "/mihari-release/mihari-release/mihari/X". The /p/public download route,
        by contrast, serves the logical (virtual absolute) path verbatim. So the
        fs-API path = logical path with its leading mount segment dropped, and
        ALL fs ops (read AND write) go through this so reads, writes, and public
        downloads agree on one location. public_url does NOT transform (it takes
        the logical path). Bound to the current topology; if the drive is
        restructured so the fs API takes the logical path verbatim, make this
        return `path` unchanged.
        """
        rest = path.lstrip("/")
        sep = rest.find("/")
        if sep <= 0:
            return path
        return "/" + rest[sep + 1:]

    def _check_write(self, response, remote_path):
        """AList always answers HTTP 200 with the real status in the JSON body's
        `code` (200 = ok). raise_for_status alone swallows a write failure as a
        silent success — surface the message instead. Defensive: only fail when a
        `code` is present and non-200, so a non-standard success body can't
        false-alarm."""
        response.raise_for_status()
        try:
            data = response.json()
        except ValueError:
            return
        if data.get("code") not in (200, None):
            fail(f"alist write failed for {remote_path}: {data.get('message')}")

    def mkdir(self, path):
        self._post("/api/fs/mkdir", json={"path": self._fs_path(path)})

    def upload(self, local, remote_path):
        with open(local, "rb") as handle:
            response = self.session.put(
                self.base + "/api/fs/put",
                headers={
                    "File-Path": quote(self._fs_path(remote_path), safe=""),
                    "As-Task": "false",
                    "Content-Type": "application/octet-stream",
                },
                data=handle,
                timeout=900,
            )
        self._check_write(response, remote_path)

    def upload_text(self, text, remote_path):
        response = self.session.put(
            self.base + "/api/fs/put",
            headers={
                "File-Path": quote(self._fs_path(remote_path), safe=""),
                "As-Task": "false",
                "Content-Type": "text/plain",
            },
            data=text.encode("utf-8"),
            timeout=120,
        )
        self._check_write(response, remote_path)

    def remove(self, dir_path, names):
        self._post("/api/fs/remove", json={"dir": self._fs_path(dir_path), "names": list(names)})

    def content(self, path):
        """Read a remote text file via its public proxy route. Returns None when
        the file does not exist. Used by retract to read the root index.txt and
        a version dir's SHA256SUMS.txt."""
        if not self.exists(path):
            return None
        response = self.session.get(self.public_url(path), timeout=120)
        response.raise_for_status()
        return response.text

    def public_url(self, path):
        # Turn an fs API path into a working public download URL. AList topology
        # quirk (verified 2026-08): the fs API (list/get/put/mkdir) addresses
        # files under the root storage (paths like /mihari-release/mihari/…), but
        # the /p download route only serves the explicit "/public" mount point,
        # so "/public" must be injected between /p and the fs path. No `?sign=` —
        # mihari distribution is fully public (signing disabled on the drive).
        # Fragile: bound to the current AList layout; if the drive is restructured
        # (e.g. a /mihari mount restored), drop the "/public" infix and set
        # DEFAULT_BASE_PATH back to /mihari. Derived from self.base (ALIST_URL);
        # the install scripts hardcode the same domain — keep them in sync.
        return f"{self.base}/p/public{path}"


def connect():
    """Build an AList client from the standard ALIST_* environment variables,
    failing loudly if any required one is missing."""
    base_url = os.environ.get("ALIST_URL")
    username = os.environ.get("ALIST_USERNAME")
    password = os.environ.get("ALIST_PASSWORD")
    if not base_url or not username or not password:
        fail("ALIST_URL / ALIST_USERNAME / ALIST_PASSWORD are required")
    return AList(base_url, username, password)
