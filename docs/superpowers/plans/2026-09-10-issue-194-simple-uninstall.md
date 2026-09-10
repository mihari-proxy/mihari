# Issue #194 Simple Uninstall Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development. User selected gpt-5.6-terra high development and gpt-6-astra review. No commits. This replaces the old 18-task plan.

**Goal:** Complete service uninstall plus recognized-file removal, with the existing approved System confirmation UI.
**Architecture:** A small app filename checker and sequential runner, called by CLI/TUI after normal client cleanup. No transaction/identity/recovery subsystem.
**Tech Stack:** Existing Go1.26.5, Cobra, Bubble Tea, service/platform APIs; no new dependency.
**Spec:** ../specs/2026-09-10-issue-194-uninstall-design.md

## Global Constraints

- Only service uninstall and generated-file removal. Do not expand scope or build large modules.
- Worktree /home/kinema/dev/mihari/.worktrees/issue-194; branch codex/issue-194-reset-uninstall. Preserve unrelated changes. No commit/stage/push/PR/live service operations.
- Exact UI label Completely Uninstall Mihari; Maintenance below Logging, above About; default Cancel; other sections untouched; fixed app strings English.
- No installer identity observer, digest registry, transaction log, recovery system, maintenance endpoints, helper or script. Windows running-image deletion failure remains explicit failure with separate-copy retry instruction.
- Unknown entries stop; do not permit arbitrary downloaded/cache subtrees without user approval. Missing roots skip. Do not follow links while scanning/deleting.
- Tests use temp dirs/fake service; Go1.26.5 ordinary kinema; current checkout ancestor causes baseline failures, use existing trusted mirror for broad tests. No security check weakening or user-directory chmod.
- Reviewer tests only current bounded deliverable and actual contract; do not revive superseded requirements. Native Windows runtime unavailable is disclosed, not replaced with compilation claims.

## Task 1: Remove superseded prototype and implement filename check

**Files:** Remove this session's new internal/app/uninstall_*.go and internal/platform/readonly_directory_windows*.go; restore only this session's factoring in installation_observer_unix.go and layout_capture_unix.go from old task-1 baseline snapshot. Preserve docs. Create app/uninstall_files.go and uninstall_files_test.go.
**Interface:** `UninstallTarget{Path string; Kind string}`; `CheckUninstallFiles(context.Context, []UninstallTarget) error`. Kinds data, base, logs, program, control. Read-only check, no service or mutation.

- [x] Save exact pre-cleanup source; restore/remove only prototype-owned paths using old snapshot, not git reset/clean.
- [x] Create compiling refusal checker and tests for recognized root files, nested names/rotation/ID patterns, unknown entries, symlink, absent roots, cancellation. Full input fixture must otherwise be valid.
```go
if err := CheckUninstallFiles(ctx, []UninstallTarget{{Path: root, Kind: "data"}}); err != nil { t.Fatal(err) }
// Add unknown.txt and expect rejection without changing either file.
```
- [x] Run `go test ./internal/app -run '^TestCheckUninstallFiles_' -count=1` and capture behavioral RED.
- [x] Implement exact filename rules from source inventory; no glob that turns whole data root into arbitrary payload. Emit typed English error with unknown relative name. Implement read-only traversal using existing stdlib, check ctx, do not follow links.
- [x] Run focused GREEN, verify source removals limited to prototype, report accepted name rules and any refused downloaded contents.

## Task 2: Sequential uninstall use case

**Files:** app/uninstall.go, uninstall_test.go; only narrow platform suffix helpers if existing layout cannot supply fixed K/path entries.
**Interface:** `Uninstaller` constructor captures resolved layout + existing ServiceController + elevation/probe functions. `Preview(ctx) ([]UninstallTarget,error)` and `Run(ctx, progress func(string)) error`; no caller-supplied execute paths, registry, digest, state persistence.
- [x] Add test-first service failure/no deletion, unknown files/no service action, successful fake uninstall/removal, absent roots retry, second-check unknown refusal, removal failure stops program phase.
```go
// Fake service records Uninstall; inject remove callback to fail for data.
// Assert program still exists and returned error names failed action.
```
- [x] Run focused behavioral RED using refusal shape; implement minimal sequence in spec. Resolve K only at exact fixed control root, skip nonexistent roots. Existing service stop/uninstall + status wait bounded30s with context; unknown active daemon refuse, no process identity framework.
- [x] Roots clean/dedup once; check all roots before service action and after stop. Use ordinary removals that never follow links. Program-in-use errors give separate-copy retry text.
- [x] Run focused GREEN/race + affected app/service tests with fake/native-free fixtures. Keep dependency hooks local/minimal and no generic filesystem framework.

## Task 3: CLI/TUI and process cleanup wiring

**Files:** internal/cli/service.go/root dependency definitions; internal/tui/pages/system/model.go; internal/tui/model.go/run.go plus small adjacent uninstall file; cmd/mihari wiring. Tests alongside those files.
**Interface:** Same app Uninstaller injected once; presentation callbacks Preview and Run. CLI renders existing error/envelope contracts; TUI exits then runs.
- [ ] Write tests first: Maintenance placement/single row, repeated modal default Cancel, Enter/Esc cancel, no duplicate execution; no mutation before TUI cleanup.
- [ ] Write CLI tests plain uninstall unchanged, --purge without --yes rejects before action, --purge --yes calls app once, English progress and error output, JSON single envelope. Add exact snapshot/call-order assertions with fake functions.
- [ ] Run targeted RED. Implement `service uninstall --purge --yes`; attach TUI action and reuse run cleanup sequence before calling app. Close CLI logging/private handles before deletion too, avoid post-delete log/Setup recreation. No new daemon endpoints.
- [ ] Recognize only existing Unix installJournalPathAllowed names (install-transaction.json, transactions/<32hex>/{transaction-id,unit,unit-bootstrap,ready.json,validation-launch.json}); add an integration regression for artifacts produced by existing service uninstall. This is a finite filename rule, not a new transaction system.
- [ ] Run affected cli/tui/cmd tests and race; cross-build affected platforms.

## Task 4: End-to-end verification and concise docs

**Files:** focused internal/integration/uninstall_test.go where cross-package gap exists; README.md/docs/unix-layout.md narrow user behavior note; AGENTS only narrow stopped app deletion exception. No CHANGELOG.
- [ ] Add any missing observable cross-package regression before necessary fixes: cancellation, execution order, unknown content refusal, partial error/no success text. No implementation-mirroring tests.
- [ ] Verify all new paths use actual app runner; remove dead prototype interfaces/strings and ensure unrelated System rows unchanged.
- [ ] Run trusted-mirror `go test ./...`, `go test -race ./...`, `go vet ./...`, gofmt, relevant existing Python layout security tests and six CGO0 builds. Document actual failures/native limitations; don't invoke real services or clean installed Mihari.
- [ ] Astra final diff review, fix only actionable scope findings. Deliver changed-file/test summary without commit/PR.
