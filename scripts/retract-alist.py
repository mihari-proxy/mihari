#!/usr/bin/env python3
"""Channel-safe AList release retraction."""
import argparse
import hashlib
import re

from alist_client import DEFAULT_BASE_PATH, PLATFORMS, bundle_name, connect, fail, info
from alist_index import IndexMutationError, parse_latest, write_index_reliably as _write_index_reliably
from release_policy import parse_version, validate_base_path

SHA_RE = re.compile(r"^[0-9a-f]{40}$")


def validate_inputs(version, channel, base_path, commit_sha):
    try:
        parse_version(version, channel)
        validate_base_path(channel, base_path)
    except ValueError as error:
        fail(str(error))
    if channel == "dev" and (not commit_sha or not SHA_RE.fullmatch(commit_sha)):
        fail("dev retraction requires a 40-hex commit SHA")


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
        sums = {}
        for line in (alist.content(f"{directory}/SHA256SUMS.txt") or "").splitlines():
            fields = line.split(None, 1)
            if len(fields) == 2 and re.fullmatch(r"[0-9a-f]{64}", fields[0]):
                sums[fields[1].strip()] = fields[0]
        expected = {bundle_name(goos, goarch) for goos, goarch in PLATFORMS}
        if set(sums) != expected:
            return None
        for name, digest in sums.items():
            remote = f"{directory}/{name}"
            if not alist.exists(remote):
                return None
            actual = hashlib.sha256(alist.read_bytes(remote)).hexdigest()
            if actual != digest:
                return None
        return sums
    except Exception:
        return None


def valid_identity(alist, base_path, version, channel, commit_sha=None):
    """Compatibility predicate for callers that only need the validity result."""
    return verified_directory(alist, base_path, version, channel, commit_sha) is not None


def highest_complete(alist, base_path, excluded, channel="stable"):
    try:
        entries = alist.list_dir(base_path)
    except Exception:
        fail("unable to list release versions")
    candidates = []
    for entry in entries:
        version = entry.get("name")
        if not entry.get("is_dir") or version == excluded:
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

    if not valid_identity(alist, base_path, version, channel, commit_sha):
        fail("refusing to retract a directory with mismatched identity")

    observed_index = read_index(alist, root_index)
    if parse_latest(observed_index) != version:
        if read_index(alist, root_index) != observed_index:
            fail("refusing to remove a non-latest version after index changed")
        remove_target(alist, base_path, version)
        if read_index(alist, root_index) != observed_index:
            fail("non-latest retraction changed the release index")
        return

    new_latest = highest_complete(alist, base_path, version, channel)
    if new_latest is None:
        write_index_reliably(alist, root_index, "", observed_index, channel, allow_empty=True)
    else:
        write_index_reliably(
            alist,
            root_index,
            index_body(alist, base_path, new_latest, channel),
            observed_index,
            channel,
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
