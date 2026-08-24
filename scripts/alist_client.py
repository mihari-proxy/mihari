"""Shared AList v3 client + helpers for the release/retract workflows.

Both scripts/release-alist.py (publish) and scripts/retract-alist.py
(withdraw) talk to the same self-hosted AList drive via the v3 REST API
(login / fs/list / fs/put / fs/remove / fs/mkdir) and share the
same notion of a version directory, bundle names, and semver ordering.
Centralizing the client keeps the two flows consistent and avoids drifting
two copies of the API surface.
"""
import hashlib
import os
import sys
from pathlib import Path
from urllib.parse import quote

import requests

MAX_DOWNLOAD_BYTES = 64 * 1024 * 1024
MAX_TEXT_BYTES = 1024 * 1024
DOWNLOAD_TIMEOUT = 120
LIST_PAGE_SIZE = 200

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


class AListError(RuntimeError):
    """Raised for sanitized AList transport or API failures."""


def fail(message):
    print(f"::error::{message}", file=sys.stderr)
    sys.exit(1)


def storage_root_entries(alist):
    try:
        entries = alist.list_dir("/")
    except Exception:
        fail("unable to inspect release root")
    if not isinstance(entries, list):
        fail("unable to inspect release root")
    mihari = [e for e in entries if isinstance(e, dict) and e.get("name") == "mihari"]
    if len(mihari) != 1 or mihari[0].get("is_dir") is not True:
        fail("unable to inspect release root")
    return entries


def info(message):
    print(f"::notice::{message}")


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

    def _post(self, path, allowed_codes=(200,), **kwargs):
        try:
            response = self.session.post(self.base + path, timeout=120, **kwargs)
            response.raise_for_status()
            data = response.json()
        except (requests.RequestException, ValueError) as error:
            raise AListError("alist operation failed") from error
        if not isinstance(data, dict) or data.get("code") not in allowed_codes:
            raise AListError("alist operation failed")
        return data

    def _login(self, username, password):
        data = self._post("/api/auth/login", json={"username": username, "password": password})
        try:
            token = data["data"]["token"]
        except (KeyError, TypeError) as error:
            raise AListError("alist login failed") from error
        if not isinstance(token, str) or not token:
            raise AListError("alist login failed")
        return token

    def exists(self, path):
        parent, name = path.rsplit("/", 1)
        if not name:
            raise AListError("invalid object path")
        return any(entry["name"] == name for entry in self.list_dir(parent or "/"))

    def list_dir(self, path):
        entries = []
        names = set()
        expected_total = None
        expected_pages = None
        page = 1
        while True:
            data = self._post(
                "/api/fs/list",
                json={
                    "path": self._fs_path(path),
                    "password": "",
                    "page": page,
                    "per_page": LIST_PAGE_SIZE,
                    "refresh": False,
                },
            )
            payload = data.get("data")
            if not isinstance(payload, dict) or "content" not in payload:
                raise AListError("invalid directory listing")
            content = payload["content"]
            if content is None:
                content = []
            total = payload.get("total")
            response_page = payload.get("page")
            per_page = payload.get("per_page")
            has_more = payload.get("has_more")
            pages_total = payload.get("pages_total")
            pagination = (response_page, per_page, has_more, pages_total)
            unpaged = all(value is None for value in pagination)
            if unpaged:
                if (
                    not isinstance(content, list)
                    or not isinstance(total, int)
                    or isinstance(total, bool)
                    or total < 0
                    or page != 1
                ):
                    raise AListError("invalid directory listing")
                has_more = False
            else:
                integer_fields = (total, response_page, per_page, pages_total)
                if (
                    not isinstance(content, list)
                    or any(not isinstance(value, int) or isinstance(value, bool) for value in integer_fields)
                    or not isinstance(has_more, bool)
                ):
                    raise AListError("invalid directory listing")
                expected_page_count = min(
                    LIST_PAGE_SIZE, max(total - (page - 1) * LIST_PAGE_SIZE, 0)
                )
                if (
                    total < 0
                    or response_page != page
                    or per_page != LIST_PAGE_SIZE
                    or pages_total != (total + LIST_PAGE_SIZE - 1) // LIST_PAGE_SIZE
                    or has_more != (page < pages_total)
                    or len(content) != expected_page_count
                ):
                    raise AListError("invalid directory listing")
                if expected_total is None:
                    expected_total = total
                    expected_pages = pages_total
                elif total != expected_total or pages_total != expected_pages:
                    raise AListError("invalid directory listing")
            for entry in content:
                if (
                    not isinstance(entry, dict)
                    or not isinstance(entry.get("name"), str)
                    or not entry["name"]
                    or not isinstance(entry.get("is_dir"), bool)
                    or entry["name"] in names
                ):
                    raise AListError("invalid directory listing")
                names.add(entry["name"])
                entries.append(entry)
            if not has_more:
                if len(entries) != total:
                    raise AListError("invalid directory listing")
                return entries
            page += 1

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

    def _check_write(self, response, _remote_path):
        """AList always answers HTTP 200 with the real status in the JSON body's
        `code` (200 = ok). raise_for_status alone swallows a write failure as a
        silent success. Require the documented success code and raise a sanitized
        normal exception so callers can retry and recover transactions."""
        try:
            response.raise_for_status()
            data = response.json()
        except (requests.RequestException, ValueError) as error:
            raise AListError("alist write failed") from error
        if not isinstance(data, dict) or data.get("code") != 200:
            raise AListError("alist write failed")

    def mkdir(self, path):
        self._post("/api/fs/mkdir", json={"path": self._fs_path(path)})

    def upload(self, local, remote_path):
        try:
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
        except requests.RequestException as error:
            raise AListError("alist write failed") from error
        self._check_write(response, remote_path)

    def upload_text(self, text, remote_path):
        try:
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
        except requests.RequestException as error:
            raise AListError("alist write failed") from error
        self._check_write(response, remote_path)

    def remove(self, dir_path, names):
        self._post("/api/fs/remove", json={"dir": self._fs_path(dir_path), "names": list(names)})

    def content(self, path: str, max_bytes: int = MAX_TEXT_BYTES) -> str | None:
        """Read a remote text file via its public proxy route. Returns None when
        the file does not exist. Used by retract to read the root index.txt and
        a version dir's SHA256SUMS.txt."""
        if not self.exists(path):
            return None
        return self.read_bytes(path, max_bytes=max_bytes).decode("utf-8", errors="strict")

    def read_bytes(
        self, path: str, max_bytes: int = MAX_DOWNLOAD_BYTES, timeout: int = DOWNLOAD_TIMEOUT
    ) -> bytes:
        """Download a public object with a strict response-size limit."""
        response = None
        try:
            response = self.session.get(
                self.public_url(path), timeout=timeout, stream=True
            )
            response.raise_for_status()
            chunks = []
            total = 0
            for chunk in response.iter_content(1024 * 1024):
                if not chunk:
                    continue
                total += len(chunk)
                if total > max_bytes:
                    raise ValueError(f"remote object exceeds {max_bytes} bytes")
                chunks.append(chunk)
        except requests.RequestException as error:
            raise AListError("alist read failed") from error
        finally:
            if response is not None:
                response.close()
        return b"".join(chunks)

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
    try:
        return AList(base_url, username, password)
    except AListError:
        fail("unable to connect to AList")
