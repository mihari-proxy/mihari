# mihari

Mihari is a new, independent local manager for mihomo. It targets Windows, Linux, and macOS equally and is designed around a single daemon-owned control plane shared by the CLI, the future TUI, and browser panels.

The current runtime slice provides an authenticated local daemon control API over a Windows named pipe or Unix domain socket. It can install, validate, supervise, query, and restart mihomo while keeping the controller on loopback. Mihari's control API does not bind a TCP port.

## Current commands

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

Subscriptions, TUI, and Web panels remain planned work and are not implemented yet.
