# mihari

Mihari is a new, independent local manager for mihomo. It targets Windows, Linux, and macOS equally and is designed around a single daemon-owned control plane shared by the CLI, TUI, and browser panels.

The current runtime slice provides an authenticated local daemon control API over a Windows named pipe or Unix domain socket. It can install, validate, supervise, query, and restart mihomo while keeping the controller on loopback. It also owns subscription persistence, bounded automatic refresh, validated config generation, reload rollback, and offline profile switching. Mihari's control API does not bind a TCP port.

## Current commands

Launch the interactive TUI (attached terminal only):

```console
mihari
```

Bare `mihari` starts the TUI only when stdin is an interactive terminal. Non-interactive or piped invocations reject the bare entry point so automation never accidentally enters full-screen mode. Explicit CLI subcommands always remain available and retain `--json` for machine-readable output.

The TUI talks only to the local daemon through `internal/control/client` over the native IPC control plane. It never opens the mihomo controller, never receives the controller secret, and never performs durable writes itself. Bracketed paste and Ctrl+V in search and form fields use the pure-Go `github.com/atotto/clipboard` helper; Mihari itself never writes secrets to the clipboard. The current TUI includes a standalone first-run Setup route, Overview, expandable Proxies, active/closed Connections with local GeoIP details, Rules/Providers, a bounded structured Logs stream, subscription management forms, and a categorized System page. Setup installs the core, can add an initial subscription, prepares local GeoIP data, and asks the daemon to persist validated local endpoints. The Web GUI page remains capability-gated and unavailable until the gateway lifecycle work lands in Phase 5. Rule order is never sorted; onboarding, system, provider, and subscription mutations run through the daemon mutation coordinator, and destructive or broad operations require confirmation.

Run the daemon in the foreground:

```console
mihari daemon
```

Query its status:

```console
mihari status
mihari status --json
```

Manage and inspect mihomo through the daemon:

```console
mihari core status
mihari core install
mihari core update
mihari core restart
mihari proxy groups
mihari proxy select GROUP PROXY
mihari proxy test GROUP
mihari connections list
mihari connections close ID
mihari connections close-all --yes
mihari rules list
mihari traffic --follow
mihari logs --follow
```

Manage subscriptions through the same daemon mutation coordinator:

```console
mihari sub add NAME URL
mihari sub list
mihari sub show ID
mihari sub refresh ID
mihari sub use ID
mihari sub enable ID
mihari sub disable ID
mihari sub set ID --interval 6h --auto-refresh=true
mihari sub remove ID --yes
```

Subscription URLs are stored only in the daemon-private catalog and are omitted from list/show responses and normal errors. Each valid profile has an independent cache, so `sub use` works without provider network access. Generated configuration always restores Mihari's managed loopback controller, secret, and port invariants before `mihomo -t` and reload.

GeoIP connection details are resolved locally by the daemon. Country and ASN MMDB files are downloaded from the public `Loyalsoldier/geoip` release branch, verified against the matching `.sha256sum` files, validated as MMDB databases, and refreshed when either local file is missing or at least 30 days old. A failed refresh retains the previous valid database pair and does not disable other connection details.

Every explicit CLI command supports human-readable output and `--json`. `--json` emits a versioned success or error envelope and stable process exit codes for automation.

## Platform targets

- Windows amd64 and arm64
- Linux amd64 and arm64
- macOS amd64 and arm64

All release binaries are CGO-free.

## Development

```console
go test ./...
go test -race ./...
go vet ./...
```

The architecture and staged delivery scope are recorded in the [architecture design](docs/superpowers/specs/2026-08-03-mihari-architecture-design.md) and [delivery roadmap](docs/superpowers/plans/2026-08-03-mihari-delivery-roadmap.md).

Phase 4 (Full TUI) is complete on the development host: package and repository tests, race detection, `go vet`, formatting, and CGO-free six-target cross-builds all pass. A follow-up Phase 4 polish pass covers unified search/form paste (bracketed paste and Ctrl+V), focus-aware rail/row chrome, contextual footers, monitor IEC layout, and subscription refresh-all with safe LastError persistence. Web GUI install/lifecycle support remains Phase 5.
