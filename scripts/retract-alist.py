#!/usr/bin/env python3
"""Channel-safe AList release retraction."""
import argparse
import hashlib
import re

from alist_client import DEFAULT_BASE_PATH, PLATFORMS, bundle_name, connect, fail, info
from alist_index import IndexMutationError, parse_latest, write_index_reliably as _write_index_reliably
from release_policy import parse_version, validate_base_path

SHA_RE = re.compile(r"^[0-9a-f]{40}$")
SUM_LINE_RE = re.compile(r"^([0-9a-f]{64})  ([^\r\n]+)\n$")


class RemoteScanError(RuntimeError):
    """Raised internally when a remote release scan is ambiguous."""


def validate_inputs(version, channel, base_path, commit_sha):
    try:
        parse_version(version, channel)
        validate_base_path(channel, base_path)
    except ValueError as error:
        fail(str(error))
    if channel == "dev" and (not commit_sha or not SHA_RE.fullmatch(commit_sha)):
        fail("dev retraction requires a 40-hex commit SHA")


def sums_manifest(sums):
    """Return the publisher's canonical checksum manifest format."""
    return "".join(f"{sums[name]}  {name}\n" for name in sorted(sums))


def parse_sums(text):
    """Parse only an exact canonical six-bundle checksum manifest."""
    if not isinstance(text, str):
        return None
    sums = {}
    for line in text.splitlines(keepends=True):
        match = SUM_LINE_RE.fullmatch(line)
        if match is None:
            return None
        digest, name = match.groups()
        if name in sums:
            return None
        sums[name] = digest
    expected = {bundle_name(goos, goarch) for goos, goarch in PLATFORMS}
    if set(sums) != expected or sums_manifest(sums) != text:
        return None
    return sums


def verified_directory(alist, base_path, version, channel, commit_sha=None):
    """Return sums only for a complete, identity-matching, byte-verified dir."""
    try:
        directory = f"{base_path}/{version}"
        if not alist.exists(f"{directory}/COMPLETE") or alist.content(f"{directory}/COMPLETE") != f"{version}\n":
            return None
        metadata = alist.content(f"{directory}/BUILDINFO")
        if metadata is None:
            if channel != "stable":
                return None
        else:
            match = re.fullmatch(r"version=([^\n]+)\ncommit=([0-9a-f]{40})\n", metadata)
            if match is None or match.group(1) != version:
                return None
            commit = match.group(2)
            if commit_sha is not None and commit != commit_sha:
                return None
        sums = parse_sums(alist.content(f"{directory}/SHA256SUMS.txt"))
        if sums is None:
            return None
        for name, digest in sums.items():
            remote = f"{directory}/{name}"
            if not alist.exists(remote):
                return None
            actual = hashlib.sha256(alist.read_bytes(remote)).hexdigest()
            if actual != digest:
                return None
        return sums
    except Exception as error:
        raise RemoteScanError from error


def valid_identity(alist, base_path, version, channel, commit_sha=None):
    """Compatibility predicate for callers that only need the validity result."""
    return verified_directory(alist, base_path, version, channel, commit_sha) is not None


def highest_complete(alist, base_path, excluded, channel="stable"):
    try:
        entries = alist.list_dir(base_path)
        if not isinstance(entries, list):
            raise ValueError("remote directory listing is not a list")
    except Exception as error:
        raise RemoteScanError from error
    candidates = []
    for entry in entries:
        if not isinstance(entry, dict):
            raise RemoteScanError
        version = entry.get("name")
        is_dir = entry.get("is_dir")
        if not isinstance(version, str) or not isinstance(is_dir, bool):
            raise RemoteScanError
        if not is_dir or version == excluded:
            continue
        try:
            parse_version(version, channel)
        except ValueError:
            continue
        if verified_directory(alist, base_path, version, channel) is not None:
            candidates.append(version)
    return max(candidates, key=lambda item: parse_version(item, channel)) if candidates else None


def index_body(alist, base_path, new_latest, channel):
    sums = verified_directory(alist, base_path, new_latest, channel)
    if sums is None:
        fail(f"new-latest directory cannot be byte verified: {new_latest}")
    lines = [f"latest {new_latest}"]
    for goos, goarch in PLATFORMS:
        name = bundle_name(goos, goarch)
        remote = f"{base_path}/{new_latest}/{name}"
        try:
            exists = alist.exists(remote)
        except Exception:
            fail("unable to verify replacement release bundles")
        if name not in sums or not exists:
            fail(f"new-latest bundle missing or unsigned: {remote}")
        lines.append(f"{goos}-{goarch} {alist.public_url(remote)} {sums[name]}")
    return "\n".join(lines) + "\n"


def write_index_reliably(alist, path, body, previous_body, channel="stable", allow_empty=False):
    """Adapt shared mutation errors to this workflow's established exit path."""
    try:
        _write_index_reliably(alist, path, body, previous_body, channel, allow_empty=allow_empty)
    except IndexMutationError as error:
        fail(str(error))
    except Exception:
        fail("unable to commit release index")


def read_index(alist, path):
    """Read the index without exposing remote failures in workflow logs."""
    try:
        return alist.content(path)
    except Exception:
        fail("unable to read release index")


def directory_exists(alist, directory):
    """Check retraction target presence without exposing remote failures."""
    try:
        return alist.exists(directory)
    except Exception:
        fail("unable to inspect retraction target")


def remove_target(alist, base_path, version):
    """Remove a verified target after its distribution index is safe."""
    try:
        alist.remove(base_path, [version])
    except Exception:
        fail("unable to remove retracted release directory")


def retract(alist, base_path, version, channel="stable", commit_sha=None):
    validate_inputs(version, channel, base_path, commit_sha)
    root_index = f"{base_path}/index.txt"
    directory = f"{base_path}/{version}"
    if not directory_exists(alist, directory):
        if parse_latest(read_index(alist, root_index)) == version:
            fail("refusing to retract a version still referenced by the index")
        return

    try:
        identity_valid = valid_identity(alist, base_path, version, channel, commit_sha)
    except RemoteScanError:
        fail("unable to verify retraction target identity")
    if not identity_valid:
        fail("refusing to retract a directory with mismatched identity")

    observed_index = read_index(alist, root_index)
    if parse_latest(observed_index) != version:
        if read_index(alist, root_index) != observed_index:
            fail("refusing to remove a non-latest version after index changed")
        remove_target(alist, base_path, version)
        if read_index(alist, root_index) != observed_index:
            fail("non-latest retraction changed the release index")
        return

    try:
        new_latest = highest_complete(alist, base_path, version, channel)
    except RemoteScanError:
        fail("unable to inspect remaining release versions")
    if new_latest is None:
        write_index_reliably(alist, root_index, "", observed_index, channel, allow_empty=True)
    else:
        try:
            replacement_body = index_body(alist, base_path, new_latest, channel)
        except RemoteScanError:
            fail("unable to verify replacement release directory")
        write_index_reliably(
            alist, root_index, replacement_body, observed_index, channel
        )
    remove_target(alist, base_path, version)


def main():
    parser = argparse.ArgumentParser(description="Retract a mihari version from AList.")
    parser.add_argument("--version", required=True)
    parser.add_argument("--channel", choices=("stable", "dev"), default="stable")
    parser.add_argument("--base-path", default=DEFAULT_BASE_PATH)
    parser.add_argument("--commit-sha")
    args = parser.parse_args()
    retract(connect(), args.base_path, args.version, args.channel, args.commit_sha)
    info(f"retraction of {args.version} complete on the AList drive")


if __name__ == "__main__":
    main()
