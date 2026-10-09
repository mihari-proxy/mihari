"""Offline install and uninstall checks for the platform e2e job.

Importing this module does not run the installer. The ``run`` subcommand
is the only path that installs a service.
"""

import hashlib
import io
import json
import re
import tarfile
from pathlib import Path

VERSION = "v0.0.0-dev.0"
CHANNEL = "dev"
LDFLAG = "-X github.com/mihari-proxy/mihari/internal/buildinfo.Version=v0.0.0-dev.0"
_SHA256 = re.compile(r"^[0-9a-f]{64}$")

UNIX_MEMBERS = (
    "mihari",
    "data/bin/mihomo",
    "data/geoip/GeoLite2-Country.mmdb",
    "data/geoip/GeoLite2-ASN.mmdb",
)


def pack_unix_bundle(mihari: bytes, core: bytes, country: bytes, asn: bytes) -> bytes:
    payload = {
        "mihari": mihari,
        "data/bin/mihomo": core,
        "data/geoip/GeoLite2-Country.mmdb": country,
        "data/geoip/GeoLite2-ASN.mmdb": asn,
    }
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        for name in UNIX_MEMBERS:
            data = payload[name]
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.type = tarfile.REGTYPE
            info.mode = 0o644
            archive.addfile(info, io.BytesIO(data))
    return buffer.getvalue()


def unix_members(blob: bytes) -> list[str]:
    with tarfile.open(fileobj=io.BytesIO(blob), mode="r:gz") as archive:
        return archive.getnames()


def sha256_hex(payload: bytes) -> str:
    return hashlib.sha256(payload).hexdigest()


def manifest_document(bundle_sha256: str, binary_sha256: str) -> dict:
    for digest in (binary_sha256, bundle_sha256):
        if _SHA256.fullmatch(digest) is None:
            raise ValueError(f"sha256 must be 64 lowercase hex digits: {digest}")
    return {"binaries": [binary_sha256], "bundles": [bundle_sha256]}


def manifest_bytes(document: dict) -> bytes:
    return (json.dumps(document, indent=2) + "\n").encode("utf-8")


def write_windows_bundle(root: Path, mihari: bytes, core: bytes, country: bytes, asn: bytes) -> None:
    files = {
        "mihari.exe": mihari,
        "data/bin/mihomo.exe": core,
        "data/geoip/GeoLite2-Country.mmdb": country,
        "data/geoip/GeoLite2-ASN.mmdb": asn,
    }
    for relative, data in files.items():
        path = root.joinpath(*relative.split("/"))
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)


_ANSI = re.compile(r"\x1b\[[0-9;]*m")
UNIX_SUCCESS = ("Installation complete.", "Run mihari to open the TUI.")
MACOS_WARNING = "warning: macOS is currently unsupported. Support is incomplete and use is not recommended."
WINDOWS_SUCCESS = "All-in-one installation completed. Restart your terminal, then run mihari to get started."
_PATH_KEYS = {
    "linux": ("program", "data", "base", "path_command"),
    "darwin": ("program", "data", "base", "path_command"),
    "windows": ("program", "data", "path_command"),
}


def _plain(text: str) -> str:
    return _ANSI.sub("", text)


def assert_install_success(system: str, stdout: str, stderr: str) -> None:
    plain_out = _plain(stdout)
    plain_err = _plain(stderr)
    if system in ("linux", "darwin"):
        for sentence in UNIX_SUCCESS:
            if sentence not in plain_out:
                raise SystemExit(sentence)
        if system == "darwin" and MACOS_WARNING not in plain_err:
            raise SystemExit(MACOS_WARNING)
        return
    if system == "windows" and WINDOWS_SUCCESS in plain_out:
        return
    raise SystemExit(system)


def assert_service_version(stdout: str) -> None:
    try:
        document = json.loads(stdout)
    except json.JSONDecodeError as exc:
        raise SystemExit("version") from exc
    if document.get("schema") != "mihari/v1" or document.get("version") != VERSION:
        raise SystemExit("version")


def assert_kept(system: str, service_installed: bool, paths_present: dict[str, bool]) -> None:
    _assert_paths(system, service_installed, paths_present, expected=True)


def assert_purged(system: str, service_installed: bool, paths_present: dict[str, bool]) -> None:
    _assert_paths(system, service_installed, paths_present, expected=False)


def _assert_paths(system: str, service_installed: bool, paths_present: dict[str, bool], expected: bool) -> None:
    if service_installed:
        raise SystemExit("service")
    try:
        keys = _PATH_KEYS[system]
    except KeyError as exc:
        raise SystemExit(system) from exc
    for key in keys:
        if paths_present.get(key) is not expected:
            raise SystemExit(key)
