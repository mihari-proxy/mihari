#!/usr/bin/env python3
"""Publish immutable, channel-scoped mihari AIO bundles to AList."""
import argparse
import hashlib
import os
import re
from pathlib import Path

from alist_client import AListError, DEFAULT_BASE_PATH, DEFAULT_KEEP_VERSIONS, PLATFORMS, bundle_name, connect, fail, info, sha256_file
from alist_index import IndexMutationError, parse_latest, write_index_reliably as _write_index_reliably
from release_policy import compare_versions, parse_version, validate_base_path

SHA_RE = re.compile(r"^[0-9a-f]{40}$")
SUM_LINE_RE = re.compile(r"^([0-9a-f]{64})  ([^\r\n]+)\n$")


class RemoteScanError(RuntimeError):
    """Raised internally when a remote release scan cannot be completed."""


def validate_inputs(version, channel, base_path, commit_sha):
    try:
        parse_version(version, channel)
        validate_base_path(channel, base_path)
    except ValueError as error:
        fail(str(error))
    if not valid_commit_sha(commit_sha):
        fail("publishing requires a 40-lowercase-hex commit SHA")


def buildinfo(version, commit_sha):
    return f"version={version}\ncommit={commit_sha}\n"


def valid_commit_sha(value):
    return bool(value and SHA_RE.fullmatch(value))


def local_sums(dist_dir):
    sums = {}
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        local = Path(dist_dir) / name
        if not local.exists():
            fail(f"bundle artifact missing: {local}")
        sums[name] = sha256_file(local)
    return sums


def sums_manifest(sums):
    """Return the canonical checksum manifest for the six release bundles."""
    return "".join(f"{sums[name]}  {name}\n" for name in sorted(sums))


def parse_sums(text):
    """Parse only an exact canonical six-bundle checksum manifest."""
    if not isinstance(text, str):
        return None
    result = {}
    for line in text.splitlines(keepends=True):
        match = SUM_LINE_RE.fullmatch(line)
        if match is None:
            return None
        digest, name = match.groups()
        if name in result:
            return None
        result[name] = digest
    required = {bundle_name(goos, goarch) for goos, goarch in PLATFORMS}
    if set(result) != required or sums_manifest(result) != text:
        return None
    return result


def verify_remote_files(alist, version_dir, sums):
    """Hash every bounded remote bundle byte stream against its remote sum."""
    for name, digest in sums.items():
        remote = f"{version_dir}/{name}"
        try:
            if not alist.exists(remote):
                return False
            actual = hashlib.sha256(alist.read_bytes(remote)).hexdigest()
        except Exception as error:
            raise RemoteScanError from error
        if actual != digest:
            return False
    return True


def preflight_incomplete_directory(alist, base_path, version, sums, commit_sha):
    """Return missing files only when an incomplete directory is safe to resume."""
    version_dir = f"{base_path}/{version}"
    bundle_names = {bundle_name(goos, goarch) for goos, goarch in PLATFORMS}
    expected_metadata = {
        "SHA256SUMS.txt": sums_manifest(sums).encode(),
        "BUILDINFO": buildinfo(version, commit_sha).encode(),
    }
    expected_names = bundle_names | set(expected_metadata) | {"COMPLETE"}
    try:
        directory_exists = alist.exists(version_dir)
        if not directory_exists:
            if any(alist.exists(f"{version_dir}/{name}") for name in expected_names):
                return None
            return False, expected_names - {"COMPLETE"}
        parent_entries = alist.list_dir(base_path)
        entries = alist.list_dir(version_dir)
        if not isinstance(parent_entries, list) or not isinstance(entries, list):
            return None
        parent_matches = [entry for entry in parent_entries if isinstance(entry, dict) and entry.get("name") == version]
        if directory_exists != (len(parent_matches) == 1 and parent_matches[0].get("is_dir") is True):
            return None
        listed = {}
        for entry in entries:
            if not isinstance(entry, dict):
                return None
            name = entry.get("name")
            if not isinstance(name, str) or entry.get("is_dir") is not False or name in listed:
                return None
            listed[name] = entry
        if set(listed) - expected_names:
            return None

        missing = set()
        for name in expected_names:
            remote = f"{version_dir}/{name}"
            exists = alist.exists(remote)
            if exists != (name in listed):
                return None
            if not exists:
                missing.add(name)
                continue
            remote_bytes = alist.read_bytes(remote)
            if name in bundle_names:
                if hashlib.sha256(remote_bytes).hexdigest() != sums[name]:
                    return None
            elif name == "COMPLETE":
                return None
            elif remote_bytes != expected_metadata[name]:
                return None
    except Exception:
        return None
    return directory_exists, missing


def verify_remote_metadata(alist, version_dir, sums, version, commit_sha):
    """Verify that the two generated metadata files are byte-for-byte exact."""
    try:
        return (
            alist.read_bytes(f"{version_dir}/SHA256SUMS.txt") == sums_manifest(sums).encode()
            and alist.read_bytes(f"{version_dir}/BUILDINFO") == buildinfo(version, commit_sha).encode()
        )
    except Exception:
        return False


def verified_directory(alist, base_path, version, channel, expected_commit=None, expected_sums=None):
    """Return remote sums only for a completed directory with verified bytes."""
    version_dir = f"{base_path}/{version}"
    try:
        if not alist.exists(f"{version_dir}/COMPLETE"):
            return None
        if alist.content(f"{version_dir}/COMPLETE") != f"{version}\n":
            return None
        remote_info = alist.content(f"{version_dir}/BUILDINFO")
        if remote_info is None:
            if channel != "stable" or expected_commit is not None:
                return None
        else:
            match = re.fullmatch(r"version=([^\n]+)\ncommit=([0-9a-f]{40})\n", remote_info)
            if match is None or remote_info != buildinfo(version, match.group(2)):
                return None
            if expected_commit is not None and match.group(2) != expected_commit:
                return None
        sums = parse_sums(alist.content(f"{version_dir}/SHA256SUMS.txt"))
    except Exception as error:
        raise RemoteScanError from error
    if sums is None or (expected_sums is not None and sums != expected_sums):
        return None
    return sums if verify_remote_files(alist, version_dir, sums) else None


def upload_version_dir(alist, dist_dir, base_path, version, commit_sha=None, channel="stable"):
    """Upload data then identity metadata, never replacing a completed directory."""
    sums = local_sums(dist_dir)
    version_dir = f"{base_path}/{version}"
    if not valid_commit_sha(commit_sha):
        fail("new publishing requires a 40-hex commit SHA")
    try:
        complete_exists = alist.exists(f"{version_dir}/COMPLETE")
    except Exception:
        fail("unable to inspect existing release directory")
    if complete_exists:
        try:
            verified = verified_directory(
                alist, base_path, version, channel, commit_sha, sums
            )
        except RemoteScanError:
            fail(f"completed version dir conflicts or cannot be verified: {version_dir}")
        if verified is None:
            fail(f"completed version dir conflicts or cannot be verified: {version_dir}")
        info(f"version dir {version_dir} already complete and verified")
        return version_dir
    preflight = preflight_incomplete_directory(alist, base_path, version, sums, commit_sha)
    if preflight is None:
        fail(f"incomplete version dir conflicts or cannot be verified: {version_dir}")
    directory_exists, missing = preflight
    if not directory_exists:
        alist.mkdir(version_dir)
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        if name in missing:
            alist.upload(str(Path(dist_dir) / name), f"{version_dir}/{name}")
    if "SHA256SUMS.txt" in missing:
        alist.upload_text(sums_manifest(sums), f"{version_dir}/SHA256SUMS.txt")
    if "BUILDINFO" in missing:
        alist.upload_text(buildinfo(version, commit_sha), f"{version_dir}/BUILDINFO")
    if not verify_remote_files(alist, version_dir, sums) or not verify_remote_metadata(alist, version_dir, sums, version, commit_sha):
        fail(f"uploaded release data failed verification: {version_dir}")
    alist.upload_text(f"{version}\n", f"{version_dir}/COMPLETE")
    return version_dir


def release_entries(alist, base_path, channel):
    """List valid-version directory entries or raise a sanitized scan error."""
    try:
        entries = alist.list_dir(base_path)
        if not isinstance(entries, list):
            raise ValueError("remote directory listing is not a list")
    except Exception as error:
        raise RemoteScanError from error
    versions = []
    for entry in entries:
        if not isinstance(entry, dict):
            raise RemoteScanError
        name = entry.get("name")
        is_dir = entry.get("is_dir")
        if not isinstance(name, str) or not isinstance(is_dir, bool):
            raise RemoteScanError
        if not is_dir:
            continue
        try:
            parse_version(name, channel)
        except ValueError:
            continue
        versions.append(name)
    return versions


def complete_versions(alist, base_path, channel, entries=None):
    """Return byte-verified completed release versions from a single listing."""
    entries = release_entries(alist, base_path, channel) if entries is None else entries
    versions = []
    for name in entries:
        if verified_directory(alist, base_path, name, channel) is not None:
            versions.append(name)
    return versions


def ensure_monotonic_version(alist, base_path, version, channel):
    try:
        indexed = parse_latest(alist.content(f"{base_path}/index.txt"))
        candidates = complete_versions(alist, base_path, channel)
    except Exception:
        fail("unable to inspect release baseline")
    if indexed:
        try:
            parse_version(indexed, channel)
            candidates.append(indexed)
        except ValueError:
            pass
    if candidates and compare_versions(version, max(candidates, key=lambda item: parse_version(item, channel)), channel) < 0:
        fail("release version is lower than the channel's highest complete version")


def build_index(alist, dist_dir, base_path, version):
    lines = [f"latest {version}"]
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        remote = f"{base_path}/{version}/{name}"
        try:
            exists = alist.exists(remote)
        except Exception:
            fail("unable to inspect uploaded bundle")
        if not exists:
            fail(f"uploaded bundle not found: {remote}")
        lines.append(f"{goos}-{goarch} {alist.public_url(remote)} {sha256_file(Path(dist_dir) / name)}")
    return "\n".join(lines) + "\n", alist.public_url(f"{base_path}/index.txt")


def write_index_reliably(alist, path, body, previous_body, channel="stable"):
    """Adapt shared mutation errors to this workflow's established exit path."""
    try:
        _write_index_reliably(alist, path, body, previous_body, channel)
    except IndexMutationError as error:
        fail(str(error))
    except Exception:
        fail("unable to write release index")


def upload_root_scripts(alist, repo_root, base_path):
    for filename in ("install-aio-remote.sh", "install-aio-remote.ps1"):
        source = Path(repo_root) / "scripts" / "install" / filename
        if not source.exists():
            fail(f"downloader script missing: {source}")
        alist.upload_text(source.read_text(encoding="utf-8"), f"{base_path}/{filename}")


def prune_versions(alist, base_path, version, keep, channel="stable"):
    try:
        entries = release_entries(alist, base_path, channel)
    except RemoteScanError:
        try:
            entries = release_entries(alist, base_path, channel)
        except RemoteScanError:
            info("unable to list release versions; skipping retention")
            return
    incomplete = []
    for name in entries:
        try:
            is_complete = alist.exists(f"{base_path}/{name}/COMPLETE")
        except Exception:
            info("unable to inspect release versions; skipping retention")
            return
        if not is_complete:
            incomplete.append(name)
    for name in incomplete:
        try:
            alist.remove(base_path, [name])
        except Exception:
            info("unable to clean incomplete release versions; skipping retention")
            return
    try:
        versions = complete_versions(alist, base_path, channel, entries)
    except RemoteScanError:
        info("unable to verify release versions; skipping retention")
        return
    versions.sort(key=lambda item: parse_version(item, channel), reverse=True)
    for old in versions[max(keep, 1):]:
        if old != version:
            try:
                alist.remove(base_path, [old])
            except Exception:
                info("unable to prune old release versions; skipping retention")
                return


def emit_env(name, value):
    if os.environ.get("GITHUB_ENV"):
        with open(os.environ["GITHUB_ENV"], "a", encoding="utf-8") as handle:
            handle.write(f"{name}={value}\n")


def publish(alist, args):
    """Publish only after a final monotonic scan immediately before index write."""
    ensure_monotonic_version(alist, args.base_path, args.version, args.channel)
    version_dir = upload_version_dir(alist, args.dist_dir, args.base_path, args.version, args.commit_sha, args.channel)
    ensure_monotonic_version(alist, args.base_path, args.version, args.channel)
    try:
        previous = alist.content(f"{args.base_path}/index.txt")
    except Exception:
        fail("unable to read release index")
    index, index_url = build_index(alist, args.dist_dir, args.base_path, args.version)
    write_index_reliably(alist, f"{args.base_path}/index.txt", index, previous, args.channel)
    if args.channel == "stable":
        upload_root_scripts(alist, args.repo_root, args.base_path)
    prune_versions(alist, args.base_path, args.version, args.keep_versions, args.channel)
    return version_dir, index_url


def main():
    parser = argparse.ArgumentParser(description="Publish mihari aio bundles to AList.")
    parser.add_argument("--version", required=True)
    parser.add_argument("--dist-dir", required=True)
    parser.add_argument("--repo-root", required=True)
    parser.add_argument("--channel", choices=("stable", "dev"), default="stable")
    parser.add_argument("--base-path", default=DEFAULT_BASE_PATH)
    parser.add_argument("--keep-versions", type=int, default=DEFAULT_KEEP_VERSIONS)
    parser.add_argument("--commit-sha")
    args = parser.parse_args()
    validate_inputs(args.version, args.channel, args.base_path, args.commit_sha)
    alist = connect()
    try:
        version_dir, index_url = publish(alist, args)
    except (AListError, RemoteScanError):
        fail("AList publish operation failed")
    emit_env("ALIST_VERSION_DIR", version_dir)
    info(f"published {args.version} to {args.base_path}; index at {index_url}")


if __name__ == "__main__":
    main()
