# Reproducible Release Inputs Implementation Plan

> **For agentic workers:** Use `superpowers:subagent-driven-development` task by task. For every behavioral change, apply `superpowers:test-driven-development`; before any completion claim apply `superpowers:verification-before-completion`.

**Goal:** Make stable and dev release retries byte-reproducible by suppressing tag-dependent Go metadata and pinning every external AIO input in a strict checked-in lock.

**Architecture:** Release workflows build from their approved immutable source SHA with an exactly pinned Go toolchain and `-buildvcs=false`. The AIO builder loads a validated source-tree lock before any I/O and consumes only exact mihomo/GeoIP artifacts. A separate, manually invoked resolver updates that lock atomically during release preparation.

**Tech stack:** Go 1.26.5, GitHub Actions YAML, Python workflow regression tests, `httptest`, standard-library JSON/HTTP/crypto packages.

**Spec:** `docs/superpowers/specs/2026-08-24-reproducible-release-inputs-design.md`

---

## Task 1: Define and strictly validate the release input lock

**Files:**

- Create: `scripts/internal/releaseinputs/lock.go`
- Create: `scripts/internal/releaseinputs/lock_test.go`
- Create: `scripts/release-inputs.lock.json`

1. Write table-driven failing tests for valid decoding, size limit, unknown/trailing JSON, schema/repository/channel validation, exact six-platform membership, numeric IDs/sizes, SHA-256 values, immutable HTTPS URL shape, and exact GeoIP commit paths.
2. Run only the new tests and confirm they fail because the loader does not exist.
3. Implement the smallest typed schema, bounded strict decoder, canonical encoder, and semantic validator that makes the tests pass.
4. Add the approved mihomo v1.19.30 inputs and the exact GeoIP inputs to the checked-in lock: commit `69986b5d098c8d723a2c4d56317bc10cd5669c02`, Country SHA-256 `26a2c3c3791b36303a1c70bac18320c4e6bd40950286224a38f2756c0f7d0ca2`, and ASN SHA-256 `82abcabdf4d0ecb34da45e4f0f9bc30bf933cfbfec446b89a2215fae5b1fdbdc`. Verify the resulting lock loads successfully.
5. Run the package tests and format changed Go files.

## Task 2: Add locked-digest GeoIP downloads without changing runtime behavior

**Files:**

- Modify: `internal/geoip/downloader.go`
- Modify: `internal/geoip/downloader_test.go`

1. Add failing tests showing an inline expected SHA-256 succeeds without requesting a sidecar, a mismatch is rejected, and zero or two checksum sources fail before any request.
2. Run the focused tests and confirm behavioral failures.
3. Add `ExpectedSHA256` to `DownloadSpec`, validate exactly one checksum mode, and reuse the existing bounded download/checksum comparison path.
4. Run the GeoIP package tests and confirm all legacy checksum-URL callers remain green.

## Task 3: Implement an atomic release-input resolver

**Files:**

- Modify: `internal/core/release.go`
- Modify: related `internal/core/*_test.go`
- Create: `scripts/resolve-release-inputs/main.go`
- Create: `scripts/resolve-release-inputs/main_test.go`
- Use: `scripts/internal/releaseinputs` for the shared lock DTO, canonical encoder, and validation created in Task 1.

1. Add failing tests for decoding GitHub numeric release/asset IDs, selecting exactly the six supported assets, resolving GeoIP `release` to a 40-hex commit, hashing every downloaded payload, canonical output, and preserving an existing lock after failure.
2. Run focused tests and confirm the missing resolver behavior.
3. Implement the resolver with injectable HTTP clients/base URLs, bounded downloads, existing core selection semantics, and same-directory temporary-file plus rename.
4. Run the resolver against fixtures, then run the relevant core and resolver package tests.
5. Regenerate `scripts/release-inputs.lock.json` with the production command and confirm the semantic content matches the reviewed upstream inputs.

## Task 4: Make the AIO builder consume only the checked-in lock

**Files:**

- Modify: `scripts/build-all-in-one/main.go`
- Modify: `scripts/build-all-in-one/main_test.go`

1. Replace mutable-discovery fixture expectations with failing tests that require `--lock`, prove invalid locks fail before requests/output creation, assert no latest-release or mutable GeoIP request occurs, reject locked size/digest mismatches, and build all six platforms from locked fixtures.
2. Add a failing repeat-build test asserting byte-identical archives.
3. Add failing cross-host metadata tests: repack identical stages under different filesystem permissions, require identical bytes, and assert canonical `0755`/`0644` entry modes in both tar and ZIP.
4. Implement lock-first loading and direct construction of the selected core/GeoIP inputs; remove the builder's latest-release and GeoIP-base discovery options, and write fixed archive metadata instead of host-derived modes.
5. Run builder tests twice and compare output digests.

## Task 5: Wire deterministic inputs into both release workflows

**Files:**

- Modify: `.github/workflows/release.yml`
- Modify: `.github/workflows/release-dev.yml`
- Modify: `scripts/test_release_workflow.py`

1. Add failing workflow tests that parse both workflow documents and require `go-version-file: go.mod`, `go build -buildvcs=false -trimpath`, and `build-all-in-one --lock scripts/release-inputs.lock.json`.
2. Run the focused Python test and confirm failure against current YAML.
3. Update both workflows with the deterministic build flag and explicit lock path.
4. Run workflow policy tests and a local two-build check around a temporary exact-version tag/ref without mutating protected remote tags.

## Task 6: Document the release-input update and retry contract

**Files:**

- Modify: `docs/RELEASE.md`
- Modify: `docs/distribution.md`
- Modify: `.github/CONTRIBUTING.md`
- Modify: `CHANGELOG.md`

1. Document that release workflows never resolve latest inputs, how maintainers regenerate and review the lock, and why `-buildvcs=false` is required.
2. Document that the lock is not a public asset and that the 14-file release contract remains unchanged.
3. Add an unreleased changelog entry for reproducible release retries.
4. Check commands and file names against the implementation.

## Task 7: Verify, review, and integrate through dev

1. Run focused package and workflow tests.
2. Run `gofmt -l` on changed Go files, `go test ./...`, `go test -race ./...`, and `go vet ./...`.
3. Run CGO-disabled builds for Windows/Linux/macOS amd64/arm64 without writing tracked artifacts.
4. Inspect `git diff` for secrets, mutable URLs in workflow build paths, untracked artifacts, and unrelated edits.
5. Request independent code review and resolve only verified findings.
6. Commit with DCO, push the fix branch, open a PR to `dev`, wait for required checks/bot review, and merge after user-approved policy allows it.
7. Preserve the immutable, pre-fix `v0.9.0-dev.2` release. Dispatch a new `v0.9.0-dev.3` release through Actions, verify its 14 assets, then re-run `v0.9.0-dev.3` and confirm the existing release/assets are accepted unchanged.
8. Promote `dev` to `main` by PR, dispatch the stable `v0.9.0` Action from the exact main SHA, and verify GitHub/AList downloads and checksums before completing the goal.
