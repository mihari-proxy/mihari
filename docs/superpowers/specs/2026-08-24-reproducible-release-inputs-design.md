# Reproducible Release Inputs Design

**Date:** 2026-08-24

## Problem

Re-running the `v0.9.0-dev.2` release from the same source commit produced different Mihari binaries. The first build ran before the release tag existed and Go recorded a pseudo-version in module metadata; the second ran after the tag existed and recorded `v0.9.0-dev.2`. The injected user-facing version was unchanged, but the executable bytes and therefore every all-in-one archive checksum changed. The release preflight correctly rejected those conflicting assets.

The all-in-one builder also resolves the latest mihomo release and GeoIP's mutable `release` branch during every workflow run. Even after removing VCS metadata from Mihari binaries, an upstream change could still make a retry produce different bytes.

## Goals

- The same source commit, version, toolchain, and checked-in input lock produce byte-identical raw Mihari binaries and all-in-one bundles before and after a release tag exists.
- Release workflows never discover mutable upstream inputs while building.
- Every downloaded mihomo and GeoIP payload is bounded and SHA-256 verified.
- Runtime GeoIP refresh behavior remains backward compatible.
- The public release asset set remains exactly the existing 14 files; the lock is source metadata, not a release artifact.

## Non-goals

- Replacing GitHub Actions, AList publication, or the existing release/tag policy.
- Changing the stable/dev version formats or public archive layout.
- Automatically updating the lock during a release workflow.
- Adding a new dependency or changing a persisted runtime format.

## Design

### Deterministic Mihari binaries

Both stable and dev workflows build with:

```console
go build -buildvcs=false -trimpath ...
```

`-buildvcs=false` prevents tag-dependent VCS/module metadata from changing the binary. `-trimpath` continues to remove checkout-path differences. The canonical release version remains injected through `internal/buildinfo.Version` with `-ldflags -X`.

The repository already pins the build environment through `go 1.26.0`, `toolchain go1.26.5`, and `actions/setup-go` using `go-version-file: go.mod`. Tests will guard that workflow wiring.

### Checked-in all-in-one input lock

`scripts/release-inputs.lock.json` is the single source of truth for upstream AIO inputs. Its schema is `mihari-aio-input-lock/v1` and contains:

- mihomo repository, channel, release ID, tag, and exactly six platform assets;
- each mihomo asset's numeric asset ID, exact name, immutable versioned URL, byte size, and SHA-256;
- GeoIP repository and exact 40-hex commit;
- Country and ASN file names, immutable raw URLs, and SHA-256 values.

The exact six required `GOOS/GOARCH` platform keys are `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`. JSON is canonical and ends in one newline; it has no generated timestamp.

The loader reads at most 1 MiB, requires UTF-8 JSON, rejects unknown fields and trailing JSON, validates every identifier/digest/size, enforces the exact platform set, and constrains URLs to the expected HTTPS hosts and repository/tag/commit paths. Validation happens before network requests or output mutation.

### Builder behavior

`scripts/build-all-in-one` requires `--lock scripts/release-inputs.lock.json`. It no longer calls the GitHub latest-release endpoint and no longer reads GeoIP from a mutable branch. It downloads the exact locked assets and verifies their locked sizes and digests before packaging.

GeoIP's downloader gains an optional expected-SHA-256 verification mode. Exactly one checksum source is allowed:

- existing runtime callers provide `ChecksumURL` and keep current behavior;
- the AIO builder provides `ExpectedSHA256`, so it performs no checksum-sidecar request.

Archive metadata is canonical rather than inherited from the build host. Entries
are ordered deterministically and use fixed timestamps. `mihari`, `mihomo`, and
`install-aio.sh` are archived as `0755`; PowerShell installers, `core-channel`,
and both MMDB files are archived as `0644`. Tar and ZIP therefore remain
byte-identical even when checkout or staging permissions differ across hosts.

### Resolver command

An independently invoked maintenance command updates the lock:

```console
go run ./scripts/resolve-release-inputs --channel stable --out scripts/release-inputs.lock.json
```

It is used while preparing and reviewing a release PR, never by a release workflow. It resolves one mihomo release, selects the exact supported assets, resolves GeoIP's `release` ref to an exact commit, downloads and validates every payload, then atomically replaces the lock through a same-directory temporary file. Any error leaves the previous lock untouched.

### Failure and security properties

- Missing, malformed, incomplete, or unexpected lock data fails closed.
- Redirects may not downgrade HTTPS, and locked URLs may not contain credentials, queries, or fragments.
- Downloads remain context-aware, status checked, size bounded, and digest verified.
- No secret, token, or complete sensitive URL is emitted in errors.
- Release retries compare against identical locally rebuilt bytes, so the existing preflight remains a meaningful conflict detector.

## Verification

- Unit tests for strict lock decoding and validation.
- Unit tests for GeoIP expected-digest mode and mutually exclusive checksum modes.
- Resolver tests using local HTTP fixtures, including atomic failure preservation.
- Builder tests proving it performs no mutable discovery and produces identical bundles on repeated runs.
- Workflow tests that inspect parsed YAML and verify both release builds use `-buildvcs=false`, the pinned Go file, and the checked-in lock.
- A local before/after-tag reproducibility check for a release binary.
- Full Go tests, race tests, vet, formatting, and cross-platform CGO-free builds before integration.
