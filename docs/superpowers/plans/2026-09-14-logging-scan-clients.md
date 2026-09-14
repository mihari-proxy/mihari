# Error logging audit: control, mihomo, web, and TUI clients

Date: 2026-09-14

Scope: one finite scan of every non-test Go file below. The scan used the complete `rg --files` list, then reviewed error creation/conversion, `Close`/write/flush/deadline results, goroutine ownership, reporter injection, and public response boundaries. Tests and diffs were inspected only as evidence; generated fixtures and real services were not used.

## Inventory coverage

| Directory/package | Production files | Result |
| --- | ---: | --- |
| `internal/control/client` | 8 | Local IPC and snapshot errors retain causes; optional reporter records only local failures and remote envelopes remain debug observations. |
| `internal/control/credential` | 2 | File/system errors pass through; malformed hex now retains the parser cause. Unix provider uses the same behavior. |
| `internal/control/protocol` | 16 | DTO validation remains safe and stable. Installation and machine-log decoders now retain actual parser/I/O causes without importing diagnostics. |
| `internal/control/server` | 16 | Request rejection, owner fallback, snapshots, and stream handshake paths were checked. Stable envelopes remain unchanged. |
| `internal/control/transport` | 7 | Native dial/listen/ownership causes are returned to the caller. Cleanup and identity failures are joined by the existing owner. |
| `internal/control/transport/testutil` | 1 | Test support only; cleanup is registered with `testing.T`, outside runtime logging. |
| `internal/mihomo` | 4 | REST and WebSocket adapters preserve HTTP/body/transport/close causes; cancellation is INFO and normal close is quiet. |
| `internal/web` | 5 | Proxy, static hosting, listener/shutdown, request rejection, and WebSocket relay boundaries were checked and repaired below. |
| `internal/tui` | 16 | Run-owned logger lifetime, installation/update workers, cleanup, relaunch, and public message boundaries were checked. |
| `internal/tui/session` | 3 | Session and stream generations join their producers; client-owned transport diagnostics are not replayed as duplicate final failures. |
| `internal/tui/ui` | 31 | Local task/export reporter lifetime and warning ownership were checked. Pure rendering/input helpers have no failure owner. |
| `internal/tui/pages/connections` | 5 | Presentation and typed async result flow only; no independent resource/logger owner. |
| `internal/tui/pages/logs` | 2 | In-memory display buffer and view only; no file export owner. |
| `internal/tui/pages/overview` | 2 | Presentation only. |
| `internal/tui/pages/proxies` | 4 | Client results cross as typed page messages; no duplicate reporter. |
| `internal/tui/pages/rules` | 1 | Client results cross as typed page messages; no duplicate reporter. |
| `internal/tui/pages/setup` | 6 | Setup validation/probe results remain user state; listener probes have no injected file reporter. |
| `internal/tui/pages/subscriptions` | 2 | Mutation errors remain typed page results; daemon/runtime is the mutation logging owner. |
| `internal/tui/pages/system` | 2 | Daemon mutations remain daemon-owned; local tasks borrow Run's reporter only before cleanup. |
| `internal/tui/pages/webgui` | 1 | Daemon mutations remain daemon-owned; browser-open failure remains a local page result. |

Total: 134 production Go files. There is no `.codegraph/` index in this worktree.

## Candidate disposition ledger

| # | Candidate | Disposition and evidence |
| ---: | --- | --- |
| 1 | `protocol.DecodeMachineLogRequest` collapsed reader and JSON/time parser failures into a fresh API error. | Fixed with a private protocol error wrapper implementing `Unwrap`/`As`. Public `APIError` text and JSON are unchanged. `TestDecodeMachineLogRequest_PreservesReadAndParseCauses` failed before and passes after. |
| 2 | `protocol.DecodeMachineLogPayload` discarded base64 and JSON parser causes. | Fixed through the same cycle-free wrapper. Limits remain 1 MiB record / 2 MiB frame. `TestDecodeMachineLogPayload_PreservesDecodeCauses` is the regression. |
| 3 | `InstallationStatus.UnmarshalJSON` discarded token/decode/type causes. | Fixed through the protocol-local wrapper; validation-only rejection still has no fabricated cause. `TestInstallationStatus_UnmarshalPreservesParserCause` covers the parser path. |
| 4 | Other protocol DTO files. | No I/O/parser implementations or cause conversions; they are stable field DTOs and validation helpers. No change. |
| 5 | Credential hex decoding discarded `hex.InvalidByteError`. | Fixed in private and Unix-provider parsers with safe `APIError` plus internal cause. `TestLoadRejectsMalformedCredential` proves the private path; Unix uses identical code and is build-tagged. |
| 6 | Private credential publication ignored close/remove cleanup errors after a failed write/close. | Fixed by joining cleanup errors into the returned owner error. File publication and delete semantics are unchanged. |
| 7 | Native control transport. | Existing code returns or joins listen, chmod, peer-proof, identity, close, and removal failures. No reporter is added at this low-level platform boundary. |
| 8 | `control/client.localError` historically replaced transport errors. | Existing shared implementation routes local paths through `localRuntimeOutcome`, retaining the public classification plus cause. Ordinary CLI has no reporter and does not create a file. |
| 9 | Runtime request marshal/create/read/decode and response close. | Existing shared implementation wraps actual causes, reports local failures once, treats remote envelopes as debug observations, and reports close-only failure as WARN. Focused diagnostics tests cover typed JSON and reader errors. |
| 10 | Control WebSocket stream cancellation/termination. | Existing shared implementation records caller cancellation at INFO, keeps normal close quiet, and records oversized/unexpected close once. Callback errors remain caller-owned. |
| 11 | Snapshot request creation/transport. | Fixed request creation to retain the URL parser cause and use `localRuntimeOutcome` for transport classification. Public snapshot errors remain safe. |
| 12 | Snapshot frame/object/field decoding discarded JSON, number, and time causes. | Fixed all parser branches to wrap `machine snapshot failed` with the original cause; semantic schema/hash mismatches remain cause-less safe rejections. |
| 13 | Snapshot header failure ignored response-body close failure. | Fixed by joining cleanup under the same public API error. `TestOpenMachineSnapshot_PreservesHeaderDecodeAndCloseCauses` proves both causes remain reachable. |
| 14 | Idle timeout and explicit snapshot close could close the body twice and lose the timeout close cause. | Fixed with `sync.Once`; timed reads wait for the owned close and join its failure. `TestIdleBody_TimeoutPreservesCloseCauseOnce` failed with two closes before the repair. |
| 15 | Snapshot hash writes ignore errors. | `sha256.Hash.Write` is documented by its concrete contract to return `len(p), nil`; intentional, no failure exists to report. |
| 16 | `networkSourceReader.Close`. | State-only close with no owned resource and no error source; intentional nil. |
| 17 | Control server JSON/body and operation-ID rejection. | Existing shared changes retain decoder cause and record expected rejection at INFO without operation-ID invention. Wire envelopes remain unchanged. |
| 18 | Snapshot source/set close. | Existing handler joins its producer first and reports close-only cleanup at WARN. A completed stream is not rewritten as failure. |
| 19 | Snapshot write deadline result was ignored; flush/write errors were already returned. | Fixed deadline handling while retaining `http.ErrNotSupported` compatibility. `TestWriteSnapshotFrame_PreservesDeadlineAndFlushCauses` covers both results. The redundant pre-frame flush was removed; each frame still flushes after write. |
| 20 | Control WebSocket accept failure was silent. | Fixed as an INFO request rejection. `TestRuntimeStream_RecordsHandshakeRejection` checks ownership; normal/induced connection closing remains quiet. |
| 21 | Generic control JSON response writes ignored encoder/writer failures. | Fixed with a handler-scoped writer observer that records `response.write.failed` after the handler returns. Snapshot writes retain their existing explicit owner and acknowledge the observed writer cause, preventing a duplicate record. `TestHandler_ReportsJSONResponseWriteFailure` failed before the repair. |
| 22 | Mihomo REST request/status/body/decode/close. | Existing shared implementation retains raw URL, bounded 256 KiB failure body, transport/read/decode causes, and joins close with a failed operation; successful-operation close alone is WARN. Public API errors remain concise. |
| 23 | Mihomo WebSocket handshake/read/cancel/close. | Pinned `github.com/coder/websocket v1.8.15` defines `CloseNow() error`. The adapter now reports a genuine close-only cleanup failure at WARN; nil, normal/going-away CloseError, `net.ErrClosed`, EOF, and caller cancellation remain quiet. Existing bounded handshake details and returned read/decode faults are unchanged. `TestReportStreamClose_ReportsOnlyGenuineCloseFailure` covers the distinction. |
| 24 | Web controller URL parse and listener bind discarded native causes. | Fixed using safe public API errors with internal causes. `TestNewControllerProxy_PreservesURLParseCause` and `TestServerServe_PreservesListenCause` failed before the repair. |
| 25 | Web shutdown ignored `http.Server.Shutdown` failure and could wait forever for `Serve`. | Fixed by retaining shutdown cause, forcing `Close` after failed graceful shutdown, and joining the serve result. `TestServerServe_PreservesShutdownFailure` uses only a loopback ephemeral listener. |
| 26 | Reverse proxy transport/status/body/close. | Existing shared observer records one final result, captures only consumed response bytes, retains 256 KiB, and does not alter the browser response. Close-only success is WARN. |
| 27 | Static panel source/read/write failures were converted to 404/503 without diagnostics. | Fixed PanelSource, index read/write, and `http.FileServer` response-write paths under `static.failed`; public status/body is unchanged. Tests cover source, rewritten-index write, and ordinary-file write causes. Missing paths remain expected 404 without a fabricated failure. |
| 28 | Optional static `dist`/SPA `Stat` lookups. | `fs.ErrNotExist` remains normal routing. Non-not-exist `Stat` failures are now recorded: WARN when the optional dist or SPA fallback succeeds, ERROR when the failed lookup determines the final 404. Independent final lookup failures are aggregated into the same owner record. `TestServeStatic_ReportsNonNotExistStatCause` covers the final branch. |
| 29 | WebSocket handshake/relay details. | Existing relay joins both directions and suppresses only induced close/cancel results. Genuine policy/fault causes survive. Failed upstream handshake now includes the actual target URL in its bounded diagnostic; browser status/body/headers remain sanitized. |
| 30 | TUI prepared update `Check` borrowed a live reporter without joining Run cleanup. | Fixed by sharing the same cancel/register/join lifecycle as `Prepare`. `TestPreparedUpdate_RunCancelsAndJoinsCheck` failed by timeout before the repair; `ApplyPrepared` still uses the original nil-reporter updater after cleanup. |
| 31 | TUI installation/session/export worker ownership. | Existing workers cancel and join before logging closes. Session retry polling relies on control-client diagnostics and typed page events rather than replaying the same failure. Expected stream end is DEBUG; retry/degraded events retain their existing level. |
| 32 | Export warning also present in final aggregate produced duplicate WARN + ERROR records. | Fixed by filtering warnings already reachable from the final error before emitting the separate WARN. UI warning state remains true. `TestLocalTaskDiagnostics_ExportFinalFailureContainsWarningLoggedOnce` failed with two records before the repair. Nil reporter still avoids inspecting opaque errors. |
| 33 | TUI output writes, Focus calls, random fallback, and loopback port-probe close. | These are best-effort terminal/UI operations without an injected reporter; Q2 permits no file record when no live reporter is available. Operation-ID generators either use Run injection or an existing monotonic fallback. No logger lifetime was extended. |

## Public and ownership boundaries

- Log records and machine snapshot/export payloads preserve raw content, including URLs, paths, secrets, and configuration fragments. Snapshot/client code performs no second redaction; the historical `redacted` counters remain wire-compatible.
- Normal API errors, browser responses, TUI messages, `/v1` DTOs, exit codes, and state fields remain concise. Internal causes are reachable only through error unwrapping and the diagnostic reporter.
- A daemon/runtime owner records executed business failures. Control client records local IPC/decode failures only when a reporter is injected; remote envelopes are observations, not a second final failure. TUI local workers use the Run-owned reporter and join before cleanup.
- No dependency, protocol field, frame/record limit, network exposure, persistent format, or transaction behavior changed.

## Verification evidence

Red tests were observed for the new protocol cause tests, snapshot decode/close test, deadline test, Web URL/listen/static tests, TUI Check join test, idle close-once test, credential/installation cause tests, and export duplicate test.

Green focused/package evidence on Windows:

```text
go test ./internal/control/protocol ./internal/control/credential
go test ./internal/control/client -run 'Test(OpenMachineSnapshot_PreservesHeaderDecodeAndCloseCauses|IdleBody_TimeoutPreservesCloseCauseOnce)$'
go test ./internal/mihomo
go test ./internal/web
go test ./internal/tui/ui
go test ./internal/control/server
go test ./internal/tui -run '^TestPreparedUpdater_HTTPWarningsBorrowLiveReporter$'
```

The single final assigned-range suite passed:

```text
go test ./internal/control/... ./internal/mihomo ./internal/web ./internal/tui/...
```

Repository-wide test/race/vet and cross-platform build checks belong to the root final verification. Unix provider behavior is compile-checked there because its file is excluded on Windows.
