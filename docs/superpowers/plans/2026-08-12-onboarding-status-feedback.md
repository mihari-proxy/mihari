# Onboarding 全流程状态反馈 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Design rationale: `docs/superpowers/specs/2026-08-12-onboarding-status-feedback-design.md`.

**Goal:** Give the onboarding five-step flow end-to-end status visibility — port-occupancy pre-check with one-key auto-fix on step 1, local core/GeoIP readiness hints on steps 2/4, and a full summary (ports + restart hint, core source/version, subscription, GeoIP, service registration) on the review step.

**Architecture:** Two real protocol gaps only. Extend `GET /v1/core` with optional `localReady/localVersion` backed by a new read-only `runtime.Manager.LocalCore` (reusing `Installer.DetectVersion`). Add `GET /v1/service/status` backed by a read-only `serviceStatus` func injected into `runtime.Manager` (no reverse dependency on the service package). All other review data already exists in each step's mutation response and is currently discarded by `actionResultMsg` — retain it on the `Model`. Port pre-check is pure-TUI local `net.Listen`. Each TUI step fetches read-only state on entry with a generation guard that rejects stale results.

**Tech Stack:** Go 1.26, Bubble Tea v2, Lip Gloss v2, `net` (loopback listen probe), `net/http`, `CGO_ENABLED=0`.

## Global Constraints

- Work only on branch `feat/onboarding-status-feedback`; leave the user's pre-existing `.gitignore`/`.claude/` working-tree changes untouched unless they block compilation.
- Use Red–Green–Refactor for every behavior change; never commit with a failing or skipped test.
- Backward compatibility is mandatory: only add optional JSON fields or new endpoints. Do not change existing `CoreStatus` field semantics, the onboarding `Complete` contract, JSON envelope, CLI exit codes, or persistence formats.
- Do not add dependencies.
- Tests must not access public networks, real OS services, real `mihomo -v`, or installed binaries. Fake the installer, the service status func, and the control client.
- Pre-check `net.Listen` probes close the socket immediately and never contend with core startup.
- Read-only local detection (core/geoip) and the service status fetch are enhancements: on failure they fall back to legacy static copy or "unknown" and must **not** block onboarding.
- Run `gofmt -l cmd internal` before every commit — CI's Format step rejects unformatted code (test/build passing ≠ format passing).
- Commit messages are conventional + Chinese summary, one commit per task. This is a squash-merge repo: use plain commits, never amend+force-push to "fix" CI.

---

## File Structure

- Modify `internal/control/protocol/runtime.go`: add `LocalReady`, `LocalVersion` to `CoreStatus`.
- Modify `internal/control/protocol/runtime_test.go`: assert optional local fields round-trip.
- Create `internal/control/protocol/service.go`: `ServiceStatus` DTO.
- Create `internal/control/protocol/service_test.go`: DTO round-trip.
- Modify `internal/runtime/manager.go`: add `LocalCore`, the `serviceStatus` injection field, and `ServiceStatus`; declare `core.LocalCoreInfo`.
- Modify `internal/core/install.go`: add `LocalCoreInfo` type (or place alongside `InstallResult`).
- Modify `internal/runtime/manager_test.go` (or adjacent): cover `LocalCore` and `ServiceStatus` with fakes.
- Modify `internal/control/server/runtime.go`: extend `RuntimeAPI` with `LocalCore`; merge local detection into `coreStatus`.
- Create `internal/control/server/service.go`: `serviceStatusAPI`, `serviceRoutes`, `serviceStatus` handler.
- Modify `internal/control/server/server.go` (routes wiring): register `serviceRoutes`.
- Modify `internal/control/server/server_test.go`: `GET /v1/core` local fields; `GET /v1/service/status`.
- Modify `internal/control/client/runtime.go`: add `Core`, `ServiceStatus` (`GeoIPStatus` already exists).
- Modify `internal/control/client/runtime_test.go`: decode new fields/endpoints.
- Modify `internal/tui/pages/setup/model.go`: extend `Client`; add Model fields; step-entry detection; result write-back; port probe + auto-fix; review/service rendering.
- Modify `internal/tui/pages/setup/model_test.go`: enrich `fakeClient`; cover all three feature areas + regression.
- Modify `internal/tui/ui/strings.go`: add readiness, review, and port-occupancy copy.
- Modify `cmd/mihari/main.go` (or the runtime assembly point): inject `serviceStatus: serviceManager.Status` into the runtime.
- Modify `docs/architecture.md`, `README.md`, `README.zh-CN.md`: document onboarding local pre-check + port pre-check + new endpoint.

---

### Task 1: `GET /v1/core` reports local core readiness

**Files:**
- Modify: `internal/control/protocol/runtime.go`, `internal/control/protocol/runtime_test.go`
- Modify: `internal/core/install.go`
- Modify: `internal/runtime/manager.go`, `internal/runtime/manager_test.go`
- Modify: `internal/control/server/runtime.go`, `internal/control/server/server_test.go`
- Modify: `internal/control/client/runtime.go`, `internal/control/client/runtime_test.go`

**Interfaces:**
- Produces: `protocol.CoreStatus` gains `LocalReady bool` (`json:"localReady,omitempty"`) and `LocalVersion string` (`json:"localVersion,omitempty"`).
- Produces: `core.LocalCoreInfo{Ready bool; Version string}`.
- Produces: `func (m *Manager) LocalCore(context.Context) (core.LocalCoreInfo, error)`.
- Produces: `RuntimeAPI` gains `LocalCore(context.Context) (core.LocalCoreInfo, error)`.
- Produces: `func (c *Client) Core(context.Context) (protocol.CoreStatus, error)`.
- Preserves: all existing `CoreStatus` fields; `GET /v1/core` existing consumers (CLI `RuntimeClient.Core`, 51 callers).

- [ ] **Step 1: Write failing tests**

  - `protocol`: `TestCoreStatusMarshalsLocalReadinessOptionally` — encode `{LocalReady:true, LocalVersion:"v1.18.5"}`, assert JSON contains `"localReady":true` and `"localVersion":"v1.18.5"`; encode a zero value, assert neither key appears.
  - `runtime`: `TestManagerLocalCoreReflectsDetectVersion` — install a fake `CoreInstaller` whose `DetectVersion` returns `("v1.18.5", nil)`; `LocalCore` returns `{Ready:true, Version:"v1.18.5"}`. With `DetectVersion` returning `("", err)`, returns `{Ready:false, Version:""}`. Assert no store write, no lock.
  - `server`: `TestCoreStatusEndpointIncludesLocalReadiness` — fake runtime supplies `LocalCore` + `Snapshot`; `GET /v1/core` body contains `localReady`/`localVersion`. When runtime does not implement the new method (assert against a minimal fake missing it), the endpoint still returns 200 with the legacy fields (graceful — the field is optional).
  - `client`: `TestClientCoreDecodesLocalReadiness` — `httptest` serves a `CoreStatus` JSON with the new fields; `Client.Core` decodes them.

- [ ] **Step 2: Run focused tests and verify RED**

  `go test -run 'TestCoreStatusMarshalsLocalReadinessOptionally|TestManagerLocalCoreReflectsDetectVersion|TestCoreStatusEndpointIncludesLocalReadiness|TestClientCoreDecodesLocalReadiness' ./internal/control/protocol ./internal/runtime ./internal/control/server ./internal/control/client`

  Expected: compilation fails — `LocalReady/LocalVersion`, `LocalCoreInfo`, `LocalCore`, `Client.Core` do not exist.

- [ ] **Step 3: Implement**

  - Add `LocalCoreInfo` in `internal/core/install.go` (next to `InstallResult`).
  - `runtime.Manager.LocalCore`: if `m.installer == nil` → `{Ready:false}`; else `version, err := m.installer.DetectVersion(ctx, m.installRequest.BinaryPath)`; `Ready = err == nil && version != ""`. No lock, no store mutation (matches the `Source=="setup"` fast-path predicate at `manager.go:374`, DRY).
  - Add `LocalCore` to `RuntimeAPI` (`server/runtime.go:23`); update all in-package `RuntimeAPI` fakes in `server_test.go`.
  - `coreStatus` handler: after `coreStatusDTO(snapshot)`, call `s.runtime.LocalCore` via type assertion on a small `localCoreAPI` interface (mirror `onboardingAPI` pattern) so legacy runtimes without the method still return 200; set `LocalReady`/`LocalVersion`.
  - `client.Core`: `doRuntime(GET, "/v1/core", nil, &result)`.

- [ ] **Step 4: Run tests and verify GREEN**

  `go test ./internal/control/protocol ./internal/core ./internal/runtime ./internal/control/server ./internal/control/client`

- [ ] **Step 5: Commit**

  ```console
  git add internal/control/protocol/runtime.go internal/control/protocol/runtime_test.go internal/core/install.go internal/runtime/manager.go internal/runtime/manager_test.go internal/control/server/runtime.go internal/control/server/server_test.go internal/control/client/runtime.go internal/control/client/runtime_test.go
  git commit -s -m "feat(control): GET /v1/core 暴露本地 core 就绪与版本"
  ```

---

### Task 2: `GET /v1/service/status` endpoint

**Files:**
- Create: `internal/control/protocol/service.go`, `internal/control/protocol/service_test.go`
- Modify: `internal/runtime/manager.go`, `internal/runtime/manager_test.go`
- Create: `internal/control/server/service.go`
- Modify: `internal/control/server/runtime.go` (register routes), `internal/control/server/server_test.go`
- Modify: `internal/control/client/runtime.go`, `internal/control/client/runtime_test.go`
- Modify: `cmd/mihari/main.go` (or runtime assembly point)

**Interfaces:**
- Produces: `protocol.ServiceStatus{Schema string; Status string}` (`Status` = `service.StatusKind`: `running/stopped/unknown/not_installed`).
- Produces: `runtime.Manager` gains `serviceStatus func() (service.StatusKind, error)` + `func (m *Manager) ServiceStatus(context.Context) (protocol.ServiceStatus, error)`.
- Produces: `serviceStatusAPI interface { ServiceStatus(context.Context) (protocol.ServiceStatus, error) }`, `serviceRoutes`, `GET /v1/service/status`.
- Produces: `func (c *Client) ServiceStatus(context.Context) (protocol.ServiceStatus, error)`.
- Preserves: `service.Manager.Status()` (`service.go:219`) Windows SCM fallback behavior unchanged.

- [ ] **Step 1: Write failing tests**

  - `protocol`: `TestServiceStatusMarshals` — `{Schema:"mihari/v1", Status:"running"}` round-trips with `"status":"running"`.
  - `runtime`: `TestManagerServiceStatusDelegatesToInjectedFunc` — inject `serviceStatus: func() (service.StatusKind, error) { return service.StatusRunning, nil }`; `ServiceStatus` returns `{Status:"running"}`. Error path returns `{Status:"unknown"}` (never propagates raw service errors as 500 — map to `unknown` + nil err so the endpoint stays 200; this is advisory data).
  - `server`: `TestServiceStatusEndpoint` — runtime implements `serviceStatusAPI`; `GET /v1/service/status` returns the status. A runtime lacking the interface returns `CodeInvalidState` (409), matching `onboardingAPI` handling.
  - `client`: `TestClientServiceStatus` — httptest decodes `ServiceStatus`.
  - `cmd/mihari` (assembly): an assertion test or a code review checkpoint that `serviceManager.Status` is wired into the runtime's `serviceStatus` field. If the assembly is not unit-testable, leave an explicit checkpoint comment and verify by `go build ./...` + manual run.

- [ ] **Step 2: Run focused tests and verify RED**

  `go test -run 'TestServiceStatus' ./internal/control/protocol ./internal/runtime ./internal/control/server ./internal/control/client`

  Expected: compilation fails — `ServiceStatus`, `serviceStatusAPI`, `Client.ServiceStatus` do not exist.

- [ ] **Step 3: Implement**

  - `protocol/service.go`: `ServiceStatus` DTO.
  - `runtime.Manager`: add `serviceStatus func() (service.StatusKind, error)` field; set via the existing Options/constructor or a `SetServiceStatus` setter (choose whichever matches the current assembly style at the runtime construction point). `ServiceStatus(ctx)`: if `m.serviceStatus == nil` → `{Status: string(service.StatusUnknown)}`; else `st, err := m.serviceStatus()`; on err → `unknown`; else `{Status: string(st)}`.
  - `server/service.go`: `serviceStatusAPI` interface, `serviceRoutes(mux)` registering `GET /v1/service/status`, `serviceStatus` handler using `s.runtime.(serviceStatusAPI)` type assertion (mirror `onboarding.go:22-34`).
  - Register `s.serviceRoutes(mux)` inside `runtimeRoutes` (`server/runtime.go:50-72`).
  - `client.ServiceStatus`: `doRuntime(GET, "/v1/service/status", nil, &result)`.
  - `cmd/mihari/main.go`: locate the `runtime.Manager` construction (same scope as `serviceManager := service.New(...)` at `main.go:59`); inject `serviceStatus: serviceManager.Status`.

- [ ] **Step 4: Run tests and verify GREEN**

  `go test ./internal/control/protocol ./internal/runtime ./internal/control/server ./internal/control/client ./cmd/mihari`

- [ ] **Step 5: Commit**

  ```console
  git add internal/control/protocol/service.go internal/control/protocol/service_test.go internal/runtime/manager.go internal/runtime/manager_test.go internal/control/server/service.go internal/control/server/runtime.go internal/control/server/server_test.go internal/control/client/runtime.go internal/control/client/runtime_test.go cmd/mihari/main.go
  git commit -s -m "feat(control): 新增 GET /v1/service/status 服务注册状态端点"
  ```

---

### Task 3: Local core/GeoIP readiness hints on `stepCore` / `stepGeoIP`

**Files:**
- Modify: `internal/tui/pages/setup/model.go`, `internal/tui/pages/setup/model_test.go`
- Modify: `internal/tui/ui/strings.go`

**Interfaces:**
- Extends setup `Client`: `Core(context.Context) (protocol.CoreStatus, error)`; `GeoIPStatus(context.Context) (protocol.GeoIPStatus, error)` (endpoint already exists).
- Produces Model fields: `coreLocal protocol.CoreStatus`, `coreLocalLoaded bool`, `coreLocalGen uint64`; `geoipLocal protocol.GeoIPStatus`, `geoipLocalLoaded bool`, `geoipLocalGen uint64`.
- Produces messages: `coreLocalResultMsg{gen uint64; status protocol.CoreStatus; err error}`, `geoipLocalResultMsg{gen uint64; status protocol.GeoIPStatus; err error}`.
- Produces strings: `SetupCoreLocalReady`, `SetupCoreWillDownload`, `SetupGeoIPLocalReady`, `SetupGeoIPWillDownload`.

- [ ] **Step 1: Write failing tests**

  Enrich `fakeClient` with `Core`/`GeoIPStatus` returning controllable values. Reuse `loadedModel` then set `model.step = stepCore` and dispatch the step-entry command.

  - `TestSetupStepCoreShowsLocalCoreReady` — `fakeClient.Core` returns `CoreStatus{LocalReady:true, LocalVersion:"v1.18.5"}`; after the entry fetch resolves, `View()` contains `SetupCoreLocalReady`-derived copy including `v1.18.5`.
  - `TestSetupStepCoreShowsWillDownloadWhenNotReady` — `LocalReady:false` → copy contains `SetupCoreWillDownload`.
  - `TestSetupStepGeoIPShowsLocalReadyAndSkipStillWorks` — `GeoIPStatus` with `Country.Available && ASN.Available` → ready copy; `s` still advances to `stepReview`.
  - `TestSetupLocalDetectionStaleResultsIgnored` — dispatch a result with an outdated `gen`; assert `coreLocalLoaded`/`geoipLocalLoaded` unchanged.
  - `TestSetupLocalDetectionFallsBackToStaticOnSuccess` — `fakeClient.Core` returns err; `View()` falls back to existing `SetupCoreBody` static copy and does not block Enter.

- [ ] **Step 2: Run focused tests and verify RED**

  `go test -run 'TestSetupStepCore|TestSetupStepGeoIP|TestSetupLocalDetection' ./internal/tui/pages/setup`

  Expected: compilation fails — `Core`/`GeoIPStatus` not on `Client`; new Model fields/messages/strings absent.

- [ ] **Step 3: Implement**

  - Add `Core`/`GeoIPStatus` to the setup `Client` interface; implement on `fakeClient`.
  - Add Model fields + result messages with generation.
  - On step transition into `stepCore` (in the `actionResultMsg`/`enter` paths that set `m.step = stepCore`), return a batched command fetching `m.client.Core`, tagged with a fresh `coreLocalGen`. Same for `stepGeoIP` with `GeoIPStatus`.
  - Handle the result messages in `Update`: only accept when `gen == m.coreLocalGen`/`geoipLocalGen`; store status + loaded=true; on err leave loaded=false.
  - `View()` `stepCore`/`stepGeoIP` branches: when loaded, render ready/will-download copy; else render existing static copy (`SetupCoreBody`/`SetupGeoIPBody`). Keep `stepGeoIP` `s` skip unchanged.
  - Add the four strings in `ui/strings.go`.

- [ ] **Step 4: Run tests and verify GREEN**

  `go test ./internal/tui/pages/setup ./internal/tui/ui`

- [ ] **Step 5: Commit**

  ```console
  git add internal/tui/pages/setup/model.go internal/tui/pages/setup/model_test.go internal/tui/ui/strings.go
  git commit -s -m "feat(tui/setup): stepCore/stepGeoIP 显示本地资源就绪提示"
  ```

---

### Task 4: Review summary across the full flow

**Files:**
- Modify: `internal/tui/pages/setup/model.go`, `internal/tui/pages/setup/model_test.go`
- Modify: `internal/tui/ui/strings.go`

**Interfaces:**
- Extends setup `Client`: `ServiceStatus(context.Context) (protocol.ServiceStatus, error)`.
- Produces Model fields (result write-back): `coreResult protocol.CoreInstallResult`, `addedSubscription *protocol.Subscription`, `geoipResult *protocol.GeoIPUpdateResult`, `geoipSkipped bool`; service: `serviceStatus protocol.ServiceStatus`, `serviceLoaded bool`, `serviceGen uint64`.
- Produces message: `serviceStatusMsg{gen uint64; status protocol.ServiceStatus; err error}`.
- Produces strings: review line labels and value templates (core source, subscription-none, geoip-skip/fail, service states, restart-required suffix).

- [ ] **Step 1: Write failing tests**

  Enrich `fakeClient`: `InstallCore` returns `CoreInstallResult{Version:"v1.18.5", Updated:false}` (local) or `{Updated:true}` (fresh); `AddSubscription` returns a `Subscription{Name:"Main"}`; `UpdateGeoIP` returns a `GeoIPUpdateResult` whose status has `Country.Available && ASN.Available`; add `ServiceStatus` returning `{Status:"running"}` / `{Status:"not_installed"}`.

  - `TestSetupReviewSummarizesLocalCoreAndSubscriptionAndGeoIP` — drive the full flow (endpoints → core → subscription → geoip → review) like the existing `TestSetupRunsCoreOptionalSubscriptionGeoIPAndCompletes`; at `stepReview` assert `View()` contains core version + 「本地已有」(Updated=false), subscription name, GeoIP Country/ASN ready.
  - `TestSetupReviewMarksFreshCoreInstall` — `Updated:true` → 「新装」.
  - `TestSetupReviewShowsSkippedSubscriptionAndGeoIP` — blank subscription + `s` skip → 「未添加（已跳过）」/「已跳过」.
  - `TestSetupReviewShowsRestartRequiredWhenEndpointsChanged` — change a port; `status.RestartRequired=true` → review port line suffix 「（需重启生效）」.
  - `TestSetupReviewShowsServiceStatus` — `ServiceStatus` running → 「running」; not_installed → 「未注册为开机自启」. Stale `serviceStatusMsg` (old gen) ignored.
  - `TestSetupExistingReviewRegressionStillShowsEndpoints` — keep the existing `TestSetupReviewShowsEndpointsWithoutLegacyWebGUIUnavailableCopy` passing (update expected copy to the new layout).

- [ ] **Step 2: Run focused tests and verify RED**

  `go test -run 'TestSetupReview' ./internal/tui/pages/setup`

  Expected: failures — write-back fields/service fetch/review copy absent.

- [ ] **Step 3: Implement**

  - Add `ServiceStatus` to setup `Client`; implement on `fakeClient`.
  - In each mutation closure, write back the result onto `m` before returning the message:
    - `installCore` (`model.go:416`): capture `result`; after success set `m.coreResult = result`.
    - `addSubscription` (`model.go:424`): set `m.addedSubscription = &result.Subscription`.
    - `updateGeoIP` (`model.go:432`): set `m.geoipResult = &result`; in the `stepGeoIP` `s`-skip branch set `m.geoipSkipped = true`.
    - (The closures currently return `actionResultMsg` built from locals — refactor minimally to write to `m` first. Keep `actionResultMsg` shape stable.)
  - On entry to `stepReview`, batch a `ServiceStatus` fetch tagged with `serviceGen`. Handle `serviceStatusMsg` with the gen guard.
  - Rewrite the `View()` `stepReview` branch to render the five rows per spec §5.3, using `endpointsChanged() && m.status.RestartRequired` for the restart suffix. While service is not loaded, show `LoadingLabel`; on err show 「未知」.
  - Add review strings in `ui/strings.go`.

- [ ] **Step 4: Run tests and verify GREEN**

  `go test ./internal/tui/pages/setup ./internal/tui/ui`

- [ ] **Step 5: Commit**

  ```console
  git add internal/tui/pages/setup/model.go internal/tui/pages/setup/model_test.go internal/tui/ui/strings.go
  git commit -s -m "feat(tui/setup): stepReview 汇总端口/core/订阅/GeoIP/服务状态"
  ```

---

### Task 5: Port occupancy pre-check + one-key auto-fix on `stepEndpoints`

**Files:**
- Modify: `internal/tui/pages/setup/model.go`, `internal/tui/pages/setup/model_test.go`
- Modify: `internal/tui/ui/strings.go`

**Interfaces:**
- Produces: `probeEndpoint(addr string) bool` (package-level helper).
- Produces: `findAvailablePorts(current [3]string) [3]string` (package-level helper).
- Produces Model fields: `portProbe [3]bool`, `portProbeLoaded bool`, `portProbeGen uint64`, plus a small probe-now helper.
- Produces message: `portProbeMsg{gen uint64; results [3]bool}`.
- Produces strings: `SetupPortInUse`, `SetupPortAutoFixHint`, and the `✗ in use` / `✓` markers (or reuse `ui.StatusDot` tones).

- [ ] **Step 1: Write failing tests**

  Use real loopback addresses the test controls (do not assume 9190/9090/9191 are free on CI):

  - `TestProbeEndpointDetectsOccupiedAndFree` — `l, _ := net.Listen("tcp", "127.0.0.1:0")`; `defer l.Close()`; `probeEndpoint(l.Addr().String())` is false; a freshly closed address probes true.
  - `TestFindAvailablePortsSkipsOccupied` — occupy one dynamic port; `findAvailablePorts` starting at that port returns the next free one (verified by `probeEndpoint`); the three results are distinct.
  - `TestSetupEndpointsMarksOccupiedPortRed` — `loadedModel`; set `model.inputs[i]` to an occupied dynamic address; trigger a probe (dispatch the debounced probe command or set `portProbe` directly for the unit); `View()` contains the danger marker for that line and `SetupPortInUse`.
  - `TestSetupEndpointsEnterAutoFixesOccupiedPorts` — with one occupied input, `Enter` does **not** advance to `stepCore`; instead it rewrites inputs to free ports and re-probes; a second `Enter` (now free) advances.
  - `TestSetupEndpointsEnterAdvancesWhenAllFree` — all-free probes → first `Enter` goes to `stepCore` (preserve existing behavior; regression with `TestSetupRejectsInvalidOrCollidingEndpointsBeforeAdvancing`).
  - `TestSetupPortProbeStaleResultsIgnored` — outdated `gen` ignored.
  - `TestSetupPortProbeDoesNotBlockOnUnknown` — `net.Listen` permission failure path leaves that port 「unknown」 (not red, not blocking) — model with an injected probe func that returns an error/unknown to simulate.

- [ ] **Step 2: Run focused tests and verify RED**

  `go test -run 'TestProbeEndpoint|TestFindAvailablePorts|TestSetupEndpoints|TestSetupPortProbe' ./internal/tui/pages/setup`

  Expected: compilation failures for the helpers/messages; assertion failures for the markers.

- [ ] **Step 3: Implement**

  - `probeEndpoint`: `net.Listen("tcp", addr)`; on success `Close()` immediately and return true; on failure return false.
  - `findAvailablePorts`: for each of the three input addresses, if `probeEndpoint` is false, walk `port+1` (re-derive host:port via `netip.ParseAddrPort`) until `probeEndpoint` is true (cap +1024); ensure mutual distinctness (re-run validate's distinct rule); leave already-free ports unchanged.
  - On `onboardingResultMsg` filling inputs, kick an initial probe. On input value change in `updateEndpoints`/`forwardTextInput`, schedule a debounced (~600ms) probe command tagged with a fresh `portProbeGen`. Handle `portProbeMsg` with the gen guard.
  - `Enter` in `updateEndpoints`: run `validateEndpoints` first (format), then read `portProbe`; if any occupied → call `findAvailablePorts`, write back into `m.inputs[i]`, schedule a re-probe, set `m.lastError = SetupPortAutoFixHint`, stay on `stepEndpoints`. If all free → `m.step = stepCore` (current behavior).
  - `renderInputs`: when `portProbeLoaded`, append `✓` (`theme.Success`) or `✗ in use` (`theme.Danger`) per port and color the occupied value with `theme.Danger`.
  - Add strings in `ui/strings.go`.

- [ ] **Step 4: Run tests and verify GREEN**

  `go test ./internal/tui/pages/setup ./internal/tui/ui`

- [ ] **Step 5: Commit**

  ```console
  git add internal/tui/pages/setup/model.go internal/tui/pages/setup/model_test.go internal/tui/ui/strings.go
  git commit -s -m "feat(tui/setup): stepEndpoints 端口占用预检与一键换端口"
  ```

---

### Task 6: Integration regression, docs, and CI format gate

**Files:**
- Modify: `docs/architecture.md`, `README.md`, `README.zh-CN.md`
- Verify: no source changes needed unless regression found.

- [ ] **Step 1: Full local verification**

  ```console
  go test ./internal/tui/pages/setup ./internal/control/server ./internal/control/protocol ./internal/control/client ./internal/runtime ./internal/core ./internal/service
  go test ./...
  go test -race ./...
  go vet ./...
  gofmt -l cmd internal
  ```

  All must pass; `gofmt -l` must print nothing.

- [ ] **Step 2: Cross-platform compile check (CGO-free)**

  ```console
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
  GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build ./...
  GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build ./...
  ```

- [ ] **Step 3: Update docs**

  - `docs/architecture.md`: onboarding local pre-check (core/GeoIP) + port occupancy pre-check + new `GET /v1/service/status` + `CoreStatus.localReady/localVersion`.
  - `README.md` / `README.zh-CN.md`: refresh TUI onboarding capability copy if user-visible behavior changed.

- [ ] **Step 4: Manual end-to-end checklist (requires user-authorized real environment)**

  - Occupy 9190/9090/9191 (e.g. `python -m http.server` bound to each) → stepEndpoints marks red, Enter auto-fixes to free ports, core starts without `managed port is unavailable`.
  - aio preset core+MMDB environment → stepCore/stepGeoIP show 「将直接使用」, Enter passes instantly offline; clean environment shows 「将下载」.
  - Complete full onboarding → review shows ports (+restart hint when changed) / core source+version / subscription / GeoIP / service status; skipped items show 「未添加/已跳过」; unregistered service shows the prompt.

- [ ] **Step 5: Commit docs**

  ```console
  git add docs/architecture.md README.md README.zh-CN.md
  git commit -s -m "docs: onboarding 状态反馈与端口预检说明"
  ```

---

## Verification (acceptance mapping)

Maps to spec §12. Each must be true before opening the PR:

- [ ] Occupied 9190/9090/9191 → red marker + auto-fix; switched ports let core start without `managed port is unavailable`.
- [ ] aio preset env: stepCore/stepGeoIP show 「将直接使用」, Enter passes offline; clean env shows 「将下载」.
- [ ] Full onboarding review shows ports (+restart hint) / core (version+source) / subscription / GeoIP / service; skips labeled; unregistered service labeled.
- [ ] `GET /v1/core` new fields + `GET /v1/service/status` are additive; existing CLI, control protocol fields, onboarding `Complete` contract, persistence unchanged.
- [ ] Every new/changed behavior has a test; `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt -l` all clean; cross-platform CGO-free builds pass.

## Notes for the implementer

- `fakeClient` (`model_test.go:14`) currently returns bare `CoreInstallResult`/`SubscriptionResult`/`GeoIPUpdateResult` (Schema+Revision only). Tasks 3/4 enrich it — keep existing assertions in mind (e.g. `TestSetupRunsCoreOptionalSubscriptionGeoIPAndCompletes` checks `installCalls`, not result payload).
- Keep `actionResultMsg` (`model.go:42`) structurally stable; write results onto `m` inside the closures rather than expanding the message.
- The `coreStatus` endpoint has 51 `CoreStatus` callers — new fields are optional and `omitempty`; existing decoders ignore them.
- Runtime assembly point for `serviceStatus` injection: `serviceManager` is created at `cmd/mihari/main.go:59`; locate the nearby `runtime.Manager` construction (or use a setter) to inject `serviceManager.Status` without introducing a `runtime → service` package import cycle (inject the func, not the `*Manager`).
