# mihari

Mihari is a new, independent local manager for mihomo. It targets Windows, Linux, and macOS equally and is designed around a single daemon-owned control plane shared by the CLI, TUI, and browser panels.

The current runtime slice provides an authenticated local daemon control API over a Windows named pipe or Unix domain socket. It can install, validate, supervise, query, and restart mihomo while keeping the controller on loopback. It also owns subscription persistence, bounded automatic refresh, validated config generation, reload rollback, and offline profile switching. Mihari's control API does not bind a TCP port.

## Current commands

Launch the interactive TUI:

```console
mihari
```

The current TUI includes a standalone first-run Setup route, Overview, expandable Proxies, active/closed Connections with local GeoIP details, Rules/Providers, a bounded structured Logs stream, and subscription management forms. Setup installs the core, can add an initial subscription, prepares local GeoIP data, and asks the daemon to persist validated local endpoints. Rule order is never sorted; onboarding, provider, and subscription mutations run through the daemon mutation coordinator, and destructive or broad operations require confirmation.

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

`--json` emits a versioned success or error envelope and stable process exit codes for automation.

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

The remaining TUI management pages and Web GUI support are being delivered in later roadmap stages.
