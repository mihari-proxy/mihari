# Explicit install repair: foundation status

Date: 2026-09-08. PR #211 remains a draft targeting `dev`. This checkpoint publishes the implementation foundation for review; it is not an end-to-end feature acceptance report.

## Implemented scope

- Strict installation state and runtime record codecs, read-only classification, immutable plan binding, and a capability-based repair/fresh coordinator. Completion is persisted before the initial service start; a start failure does not rewrite the installation as interrupted.
- Protected installation-control storage, permanent locks and guarded publication on Unix and Windows; Darwin runtime storage; Windows Job, boot-session and runtime-record capabilities.
- Unix read-only installation observation, authenticated control status, injected CLI repair/fresh actions, and TUI confirmation/lifecycle handling. Prepared workers and log/file owners close before execution.
- English product strings in the new UI and existing setup/core/installer prompts.
- Previously prepared macOS shared-process-group changes and isolated hosted-runner regression coverage.

The fresh preview is restricted to the eleven approved managed names. Native enforcement still belongs to the execution adapters described below; a preview itself is not a filesystem capability.

## Remaining implementation

Native execution adapters and complete installation entrypoint assembly are pending. The new CLI/TUI actions are available only when their callbacks are supplied; production native callbacks are not yet connected. Windows runtime registration and the full service-start/stop handoff are not complete.

Existing installer, self-update and script entrypoints have not all switched to the new transaction model. Legacy automatic recovery paths still exist. The approved requirement to remove implicit recovery is therefore not yet delivered. Tasks 4–6 must be assembled and validated as one release unit before this feature is released.

## Local validation before the first foundation push

- Windows: full `go test ./...`, `go test -race ./...` and `go vet ./...` passed. Subsequent app audit corrections passed the full app suite, app race and vet again.
- Six `CGO_ENABLED=0` application builds passed: Windows/Linux/macOS, amd64/arm64.
- Installer and isolated-runner Python tests: 101 passed, 6 platform skips.
- Independent audits covered the state/plan invariants, control storage, Unix lease lifetime, Windows runtime storage and CLI/TUI lifecycle. Confirmed issues were corrected before pushing.

CI and bot review are tracked against the pushed commit in the PR. Cross-compilation does not establish native privilege, durability or end-to-end repair correctness. Local tests do not install system services or access real subscriptions. Native owner fixtures that require a different Windows token are skipped explicitly; hosted macOS execution is not evidence for the minimum supported macOS version.
